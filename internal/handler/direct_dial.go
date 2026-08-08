package handler

import (
	"context"
	"errors"
	"fmt"
	"net"
	"syscall"
	"time"

	"goProxy/internal/cache"
	"goProxy/internal/logging"
)

const (
	directDialTimeout   = 10 * time.Second
	directFallbackDelay = 250 * time.Millisecond
	minimumAttemptTime  = 2 * time.Second
)

type directDialer struct {
	cacheManager *cache.CacheManager
	dnsOptions   cache.ExternalDNSOptions
	logger       *logging.Logger
	selfGuard    *selfConnectionGuard
	timeout      time.Duration
}

type dialResult struct {
	conn    net.Conn
	err     error
	primary bool
}

type tcpDialCandidates struct {
	network   string
	ips       []net.IP
	localAddr *net.TCPAddr
	selfGuard *selfConnectionGuard
}

func newDirectRouteDialer(decision *ProxyDecision, logger *logging.Logger) *directDialer {
	return &directDialer{
		cacheManager: decision.cache,
		dnsOptions: cache.ExternalDNSOptions{
			Server:     decision.config.ExternalDns,
			SourceIPv4: decision.config.ExternalIp4,
			SourceIPv6: decision.config.ExternalIp6,
		},
		logger:    logger,
		selfGuard: decision.selfGuard,
		timeout:   directDialTimeout,
	}
}

func newTCPDialer(localAddr *net.TCPAddr, selfGuard *selfConnectionGuard) *net.Dialer {
	dialer := &net.Dialer{
		Timeout:   directDialTimeout,
		KeepAlive: 30 * time.Second,
		LocalAddr: localAddr,
	}
	if selfGuard != nil {
		dialer.ControlContext = func(_ context.Context, _ string, address string, _ syscall.RawConn) error {
			if selfGuard.isSelfConnection(address) {
				return fmt.Errorf("refusing to connect proxy to itself at %s", address)
			}
			return nil
		}
	}
	return dialer
}

func resolveTCPSource(sourceIP string, wantIPv4 bool) (*net.TCPAddr, error) {
	if sourceIP == "" {
		return nil, nil
	}

	ip, err := parseSourceIP(sourceIP, wantIPv4)
	if err != nil {
		return nil, err
	}
	return &net.TCPAddr{IP: ip}, nil
}

func (d *directDialer) DialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	timeout := d.timeout
	if timeout <= 0 {
		timeout = directDialTimeout
	}
	dialCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	extIp4 := d.dnsOptions.SourceIPv4
	extIp6 := d.dnsOptions.SourceIPv6
	if extIp4 == "" && extIp6 == "" && d.dnsOptions.Server == "" {
		return newTCPDialer(nil, d.selfGuard).DialContext(dialCtx, network, addr)
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

	ips, err := d.cacheManager.ResolveExternalHostContext(dialCtx, host, d.dnsOptions)
	if err != nil {
		if d.logger != nil {
			d.logger.Error("DNS Resolve Error for %s: %v", host, err)
		}
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

	if d.logger != nil {
		if local4 != nil {
			d.logger.Debug("IPv4 direct connections to %s bound to source IP: %s", addr, extIp4)
		}
		if local6 != nil {
			d.logger.Debug("IPv6 direct connections to %s bound to source IP: %s", addr, extIp6)
		}
	}

	v4Candidates := tcpDialCandidates{network: "tcp4", ips: ipv4, localAddr: local4, selfGuard: d.selfGuard}
	v6Candidates := tcpDialCandidates{network: "tcp6", ips: ipv6, localAddr: local6, selfGuard: d.selfGuard}

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
		conn, err := newTCPDialer(candidates.localAddr, candidates.selfGuard).DialContext(attemptCtx, candidates.network, targetAddr)
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
	returned := make(chan struct{})
	defer close(returned)
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
					case <-returned:
					}
					return
				}
			}

			conn, err := dialTCPSerial(raceCtx, port, candidates)
			select {
			case results <- dialResult{conn: conn, err: err, primary: isPrimary}:
			case <-returned:
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
