package handler

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

const directFallbackDelay = 250 * time.Millisecond

type dialResult struct {
	conn    net.Conn
	err     error
	primary bool
}

func newDirectDialer(localAddr *net.TCPAddr, resolver *net.Resolver) *net.Dialer {
	return &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
		LocalAddr: localAddr,
		Resolver:  resolver,
	}
}

func resolveTCPSource(sourceIP string, wantIPv4 bool) (*net.TCPAddr, error) {
	if sourceIP == "" {
		return nil, nil
	}

	localAddr, err := net.ResolveTCPAddr("tcp", net.JoinHostPort(sourceIP, "0"))
	if err != nil {
		return nil, fmt.Errorf("invalid TCP source IP %s: %w", sourceIP, err)
	}
	if localAddr.IP == nil {
		return nil, fmt.Errorf("invalid TCP source IP %s", sourceIP)
	}
	if wantIPv4 != (localAddr.IP.To4() != nil) {
		return nil, fmt.Errorf("TCP source IP %s has the wrong address family", sourceIP)
	}
	return localAddr, nil
}

func externalResolver(extDns, extIp4, extIp6 string) *net.Resolver {
	if extDns == "" {
		return nil
	}

	dnsAddr := extDns
	dnsHost, _, err := net.SplitHostPort(extDns)
	if err != nil {
		dnsHost = extDns
		dnsAddr = net.JoinHostPort(extDns, "53")
	}

	return &net.Resolver{
		PreferGo: true,
		Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			dialer := net.Dialer{Timeout: 5 * time.Second}
			dnsIP := net.ParseIP(dnsHost)
			sourceIP := getSourceIpByIps([]net.IP{dnsIP}, extIp4, extIp6)
			if sourceIP != "" {
				switch {
				case strings.HasPrefix(network, "tcp"):
					localAddr, err := net.ResolveTCPAddr("tcp", net.JoinHostPort(sourceIP, "0"))
					if err != nil {
						return nil, fmt.Errorf("invalid TCP DNS source IP %s: %w", sourceIP, err)
					}
					dialer.LocalAddr = localAddr
				case strings.HasPrefix(network, "udp"):
					localAddr, err := net.ResolveUDPAddr("udp", net.JoinHostPort(sourceIP, "0"))
					if err != nil {
						return nil, fmt.Errorf("invalid UDP DNS source IP %s: %w", sourceIP, err)
					}
					dialer.LocalAddr = localAddr
				}
			}
			return dialer.DialContext(ctx, network, dnsAddr)
		},
	}
}

func (p *ProxyHandler) dialDirectContext(ctx context.Context, network, addr, extIp4, extIp6, extDns string) (net.Conn, error) {
	resolver := externalResolver(extDns, extIp4, extIp6)

	local4, err := resolveTCPSource(extIp4, true)
	if err != nil {
		return nil, err
	}
	local6, err := resolveTCPSource(extIp6, false)
	if err != nil {
		return nil, err
	}

	switch {
	case local4 == nil && local6 == nil:
		return newDirectDialer(nil, resolver).DialContext(ctx, network, addr)
	case local6 == nil:
		p.logger.Debug("Direct connection to %s bound to source IP: %s", addr, extIp4)
		return newDirectDialer(local4, resolver).DialContext(ctx, network, addr)
	case local4 == nil:
		p.logger.Debug("Direct connection to %s bound to source IP: %s", addr, extIp6)
		return newDirectDialer(local6, resolver).DialContext(ctx, network, addr)
	default:
		return p.dialDirectDualStack(ctx, network, addr, local4, local6, resolver)
	}
}

func (p *ProxyHandler) dialDirectDualStack(ctx context.Context, network, addr string, local4, local6 *net.TCPAddr, resolver *net.Resolver) (net.Conn, error) {
	if network == "tcp4" {
		return newDirectDialer(local4, resolver).DialContext(ctx, network, addr)
	}
	if network == "tcp6" {
		return newDirectDialer(local6, resolver).DialContext(ctx, network, addr)
	}
	if network != "tcp" {
		return nil, fmt.Errorf("dual-stack direct dial does not support network %q", network)
	}

	p.logger.Debug("Direct connection to %s racing source IPs: %s and %s", addr, local6.IP, local4.IP)
	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	results := make(chan dialResult)
	startFallback := make(chan struct{}, 1)

	startDial := func(primary bool, delay time.Duration, dialNetwork string, localAddr *net.TCPAddr) {
		go func() {
			if delay > 0 {
				timer := time.NewTimer(delay)
				defer timer.Stop()
				select {
				case <-timer.C:
				case <-startFallback:
				case <-raceCtx.Done():
					select {
					case results <- dialResult{err: raceCtx.Err(), primary: primary}:
					case <-done:
					}
					return
				}
			}

			conn, err := newDirectDialer(localAddr, resolver).DialContext(raceCtx, dialNetwork, addr)
			select {
			case results <- dialResult{conn: conn, err: err, primary: primary}:
			case <-done:
				if conn != nil {
					conn.Close()
				}
			}
		}()
	}

	startDial(true, 0, "tcp6", local6)
	startDial(false, directFallbackDelay, "tcp4", local4)

	var dialErrors []error
	for range 2 {
		result := <-results
		if result.err == nil {
			cancel()
			return result.conn, nil
		}
		if result.primary {
			select {
			case startFallback <- struct{}{}:
			default:
			}
		}
		dialErrors = append(dialErrors, result.err)
	}

	return nil, fmt.Errorf("direct dial to %s failed: %w", addr, errors.Join(dialErrors...))
}

func getSourceIpByIps(ips []net.IP, extIp4, extIp6 string) string {
	_, sourceIP := getTargetAndSourceIp(ips, extIp4, extIp6)
	return sourceIP
}

func getTargetAndSourceIp(ips []net.IP, extIp4, extIp6 string) (net.IP, string) {
	for _, ip := range ips {
		if ip == nil {
			continue
		}
		isV4 := ip.To4() != nil

		if isV4 && extIp4 != "" {
			return ip, extIp4
		}
		if !isV4 && extIp6 != "" {
			return ip, extIp6
		}
	}

	if extIp4 == "" && extIp6 == "" {
		for _, ip := range ips {
			if ip != nil {
				return ip, ""
			}
		}
	}

	return nil, ""
}

func getProxyAddress(proxyURL *url.URL) (string, error) {
	host := proxyURL.Hostname()
	if host == "" {
		return "", fmt.Errorf("proxy URL has no host")
	}

	port := proxyURL.Port()
	if port == "" {
		switch proxyURL.Scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		default:
			return "", fmt.Errorf("unsupported proxy scheme: %s", proxyURL.Scheme)
		}
	}

	return net.JoinHostPort(host, port), nil
}

func (s *ProxyHandler) getSourceIp(addr string, extDns, extIp4, extIp6 string) (string, error) {
	host, _, _ := net.SplitHostPort(addr)
	ips, err := s.decision.cache.ResolveExternalHost(host, extDns, func(ips []net.IP) string {
		return getSourceIpByIps(ips, extIp4, extIp6)
	})
	if err != nil {
		return "", err
	}

	return getSourceIpByIps(ips, extIp4, extIp6), nil
}
