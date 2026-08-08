package handler

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"time"

	"goProxy/internal/cache"
)

const (
	directDialTimeout   = 10 * time.Second
	directFallbackDelay = 250 * time.Millisecond
	minimumAttemptTime  = 2 * time.Second
)

type dialResult struct {
	conn    net.Conn
	err     error
	primary bool
}

type tcpDialCandidates struct {
	network   string
	ips       []net.IP
	localAddr *net.TCPAddr
}

func newDirectDialer(localAddr *net.TCPAddr) *net.Dialer {
	return &net.Dialer{
		Timeout:   directDialTimeout,
		KeepAlive: 30 * time.Second,
		LocalAddr: localAddr,
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

func (p *ProxyHandler) dialDirectContext(ctx context.Context, network, addr string, cacheManager *cache.CacheManager, extIp4, extIp6, extDns string) (net.Conn, error) {
	if extIp4 == "" && extIp6 == "" && extDns == "" {
		return newDirectDialer(nil).DialContext(ctx, network, addr)
	}
	if network != "tcp" && network != "tcp4" && network != "tcp6" {
		return nil, fmt.Errorf("direct TCP dial does not support network %q", network)
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, fmt.Errorf("invalid address format %s: %w", addr, err)
	}

	local4, err := resolveTCPSource(extIp4, true)
	if err != nil {
		return nil, err
	}
	local6, err := resolveTCPSource(extIp6, false)
	if err != nil {
		return nil, err
	}

	ips, err := cacheManager.ResolveExternalHost(host, extDns, func(ips []net.IP) string {
		return getSourceIpByIps(ips, extIp4, extIp6)
	})
	if err != nil {
		p.logger.Error("DNS Resolve Error for %s: %v", host, err)
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("no IP addresses found for host: %s", host)
	}

	unbound := local4 == nil && local6 == nil
	var ipv4, ipv6 []net.IP
	preferredNetwork := ""
	for _, ip := range ips {
		if ip == nil {
			continue
		}
		isIPv4 := ip.To4() != nil
		if isIPv4 {
			if network == "tcp6" || (!unbound && local4 == nil) {
				continue
			}
			ipv4 = append(ipv4, ip)
			if preferredNetwork == "" {
				preferredNetwork = "tcp4"
			}
			continue
		}
		if network == "tcp4" || (!unbound && local6 == nil) {
			continue
		}
		ipv6 = append(ipv6, ip)
		if preferredNetwork == "" {
			preferredNetwork = "tcp6"
		}
	}
	if len(ipv4) == 0 && len(ipv6) == 0 {
		return nil, fmt.Errorf("no IP addresses for %s are compatible with the configured source IPs", host)
	}

	if local4 != nil {
		p.logger.Debug("IPv4 direct connections to %s bound to source IP: %s", addr, extIp4)
	}
	if local6 != nil {
		p.logger.Debug("IPv6 direct connections to %s bound to source IP: %s", addr, extIp6)
	}

	dialCtx, cancel := context.WithTimeout(ctx, directDialTimeout)
	defer cancel()
	v4Candidates := tcpDialCandidates{network: "tcp4", ips: ipv4, localAddr: local4}
	v6Candidates := tcpDialCandidates{network: "tcp6", ips: ipv6, localAddr: local6}

	if len(ipv6) == 0 {
		return dialTCPSerial(dialCtx, port, v4Candidates)
	}
	if len(ipv4) == 0 {
		return dialTCPSerial(dialCtx, port, v6Candidates)
	}
	if preferredNetwork == "tcp4" {
		return dialTCPHappyEyeballs(dialCtx, port, v4Candidates, v6Candidates)
	}
	return dialTCPHappyEyeballs(dialCtx, port, v6Candidates, v4Candidates)
}

func dialTCPSerial(ctx context.Context, port string, candidates tcpDialCandidates) (net.Conn, error) {
	var dialErrors []error
	for i, ip := range candidates.ips {
		attemptCtx, cancel := withAttemptDeadline(ctx, len(candidates.ips)-i)
		targetAddr := net.JoinHostPort(ip.String(), port)
		conn, err := newDirectDialer(candidates.localAddr).DialContext(attemptCtx, candidates.network, targetAddr)
		cancel()
		if err == nil {
			return conn, nil
		}
		dialErrors = append(dialErrors, fmt.Errorf("%s via %s: %w", targetAddr, sourceDescription(candidates.localAddr), err))
	}
	return nil, errors.Join(dialErrors...)
}

func withAttemptDeadline(ctx context.Context, attemptsRemaining int) (context.Context, context.CancelFunc) {
	deadline, ok := ctx.Deadline()
	if !ok || attemptsRemaining <= 1 {
		return context.WithCancel(ctx)
	}

	now := time.Now()
	timeRemaining := deadline.Sub(now)
	attemptTime := timeRemaining / time.Duration(attemptsRemaining)
	if attemptTime < minimumAttemptTime && timeRemaining > minimumAttemptTime {
		attemptTime = minimumAttemptTime
	}
	attemptDeadline := now.Add(attemptTime)
	if !attemptDeadline.Before(deadline) {
		return context.WithCancel(ctx)
	}
	return context.WithDeadline(ctx, attemptDeadline)
}

func sourceDescription(localAddr *net.TCPAddr) string {
	if localAddr == nil {
		return "the system-selected source"
	}
	return localAddr.IP.String()
}

func dialTCPHappyEyeballs(ctx context.Context, port string, primary, fallback tcpDialCandidates) (net.Conn, error) {
	raceCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan struct{})
	defer close(done)
	results := make(chan dialResult)
	startFallback := make(chan struct{}, 1)

	startDial := func(isPrimary bool, delay time.Duration, candidates tcpDialCandidates) {
		go func() {
			if delay > 0 {
				timer := time.NewTimer(delay)
				defer timer.Stop()
				select {
				case <-timer.C:
				case <-startFallback:
				case <-raceCtx.Done():
					select {
					case results <- dialResult{err: raceCtx.Err(), primary: isPrimary}:
					case <-done:
					}
					return
				}
			}

			conn, err := dialTCPSerial(raceCtx, port, candidates)
			select {
			case results <- dialResult{conn: conn, err: err, primary: isPrimary}:
			case <-done:
				if conn != nil {
					conn.Close()
				}
			}
		}()
	}

	startDial(true, 0, primary)
	startDial(false, directFallbackDelay, fallback)

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

	return nil, errors.Join(dialErrors...)
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
