package cache

import (
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/gobwas/glob"
	lru "github.com/hashicorp/golang-lru/v2/expirable"
	"golang.org/x/sync/singleflight"
)

const (
	IPResolutionTTL = 5 * time.Minute
)

type CacheManager struct {
	globCache      map[string]glob.Glob
	cidrCache      map[string]*net.IPNet
	dnsCache       *lru.LRU[string, []net.IP]
	dnsGroup       singleflight.Group
	externalDNSMu  sync.Mutex
	externalDNSRun map[string]*externalDNSCall
	mu             sync.RWMutex
}

type externalDNSCall struct {
	done    chan struct{}
	cancel  context.CancelFunc
	waiters int
	ips     []net.IP
	err     error
}

func NewCacheManager() *CacheManager {
	dnsCache := lru.NewLRU[string, []net.IP](1000, nil, IPResolutionTTL)

	return &CacheManager{
		globCache:      make(map[string]glob.Glob),
		cidrCache:      make(map[string]*net.IPNet),
		dnsCache:       dnsCache,
		externalDNSRun: make(map[string]*externalDNSCall),
	}
}

func (c *CacheManager) GetGlob(pattern string) (glob.Glob, error) {
	c.mu.RLock()
	if g, exists := c.globCache[pattern]; exists {
		c.mu.RUnlock()
		return g, nil
	}
	c.mu.RUnlock()

	g, err := glob.Compile(pattern)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.globCache[pattern] = g
	c.mu.Unlock()
	return g, nil
}

func (c *CacheManager) GetCIDRNet(cidr string) (*net.IPNet, error) {
	c.mu.RLock()
	if ipNet, exists := c.cidrCache[cidr]; exists {
		c.mu.RUnlock()
		return ipNet, nil
	}
	c.mu.RUnlock()

	normalizedCIDR := cidr

	if !strings.Contains(cidr, "/") {
		ip := net.ParseIP(cidr)
		if ip == nil {
			return nil, fmt.Errorf("invalid IP address: %s", cidr)
		}

		if ip.To4() != nil {
			normalizedCIDR = cidr + "/32"
		} else {
			normalizedCIDR = cidr + "/128"
		}
	}

	_, ipNet, err := net.ParseCIDR(normalizedCIDR)
	if err != nil {
		return nil, err
	}

	c.mu.Lock()
	c.cidrCache[cidr] = ipNet
	c.mu.Unlock()
	return ipNet, nil
}

func (c *CacheManager) ResolveHost(hostname string) ([]net.IP, error) {
	if ip := net.ParseIP(hostname); ip != nil {
		return []net.IP{ip}, nil
	}

	if ips, exists := c.dnsCache.Get(hostname); exists {
		return ips, nil
	}

	ipsInterface, err, _ := c.dnsGroup.Do(hostname, func() (interface{}, error) {
		if ips, exists := c.dnsCache.Get(hostname); exists {
			return ips, nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		ips, err := net.DefaultResolver.LookupIP(ctx, "ip", hostname)
		if err != nil {
			return nil, err
		}

		c.dnsCache.Add(hostname, ips)
		return ips, nil
	})

	if err != nil {
		return nil, err
	}

	return ipsInterface.([]net.IP), nil
}

func cloneIPs(ips []net.IP) []net.IP {
	cloned := make([]net.IP, len(ips))
	for i, ip := range ips {
		cloned[i] = append(net.IP(nil), ip...)
	}
	return cloned
}

func (c *CacheManager) ResolveExternalHost(hostname, extDns string, getSourceIpByIps func(ips []net.IP) string) ([]net.IP, error) {
	return c.ResolveExternalHostContext(context.Background(), hostname, extDns, getSourceIpByIps)
}

func (c *CacheManager) ResolveExternalHostContext(ctx context.Context, hostname, extDns string, getSourceIpByIps func(ips []net.IP) string) ([]net.IP, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ip := net.ParseIP(hostname); ip != nil {
		return []net.IP{ip}, nil
	}

	cacheKey := fmt.Sprintf("ext:%s:%s", extDns, hostname)

	if ips, exists := c.dnsCache.Get(cacheKey); exists {
		return cloneIPs(ips), nil
	}

	call := c.getOrStartExternalDNSCall(cacheKey, hostname, extDns, getSourceIpByIps)
	select {
	case <-call.done:
		if call.err != nil {
			return nil, call.err
		}
		return cloneIPs(call.ips), nil
	case <-ctx.Done():
		c.releaseExternalDNSWaiter(cacheKey, call)
		return nil, ctx.Err()
	}
}

func (c *CacheManager) getOrStartExternalDNSCall(cacheKey, hostname, extDns string, getSourceIpByIps func(ips []net.IP) string) *externalDNSCall {
	c.externalDNSMu.Lock()
	if call, exists := c.externalDNSRun[cacheKey]; exists {
		call.waiters++
		c.externalDNSMu.Unlock()
		return call
	}

	lookupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	call := &externalDNSCall{
		done:    make(chan struct{}),
		cancel:  cancel,
		waiters: 1,
	}
	c.externalDNSRun[cacheKey] = call
	c.externalDNSMu.Unlock()

	go c.runExternalDNSCall(lookupCtx, cacheKey, hostname, extDns, getSourceIpByIps, call)
	return call
}

func (c *CacheManager) runExternalDNSCall(ctx context.Context, cacheKey, hostname, extDns string, getSourceIpByIps func(ips []net.IP) string, call *externalDNSCall) {
	defer call.cancel()

	if ips, exists := c.dnsCache.Get(cacheKey); exists {
		call.ips = ips
	} else {
		resolver := net.DefaultResolver
		if extDns != "" {
			dnsAddr := extDns
			dnsHost, _, err := net.SplitHostPort(extDns)
			if err != nil {
				dnsHost = extDns
				dnsAddr = net.JoinHostPort(extDns, "53")
			}

			resolver = &net.Resolver{
				PreferGo: true,
				Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
					dnsIP := net.ParseIP(dnsHost)
					sourceIP := getSourceIpByIps([]net.IP{dnsIP})
					d := net.Dialer{Timeout: 5 * time.Second}
					if sourceIP != "" {
						source := net.ParseIP(sourceIP)
						if strings.HasPrefix(network, "tcp") {
							d.LocalAddr = &net.TCPAddr{IP: source}
						} else {
							d.LocalAddr = &net.UDPAddr{IP: source}
						}
					}
					return d.DialContext(ctx, network, dnsAddr)
				},
			}
		}

		call.ips, call.err = resolver.LookupIP(ctx, "ip", hostname)
		if call.err == nil {
			c.dnsCache.Add(cacheKey, call.ips)
		}
	}

	c.externalDNSMu.Lock()
	if c.externalDNSRun[cacheKey] == call {
		delete(c.externalDNSRun, cacheKey)
	}
	c.externalDNSMu.Unlock()
	close(call.done)
}

func (c *CacheManager) releaseExternalDNSWaiter(cacheKey string, call *externalDNSCall) {
	c.externalDNSMu.Lock()
	defer c.externalDNSMu.Unlock()

	if c.externalDNSRun[cacheKey] != call {
		return
	}
	call.waiters--
	if call.waiters == 0 {
		delete(c.externalDNSRun, cacheKey)
		call.cancel()
	}
}

func (c *CacheManager) PrecompilePatterns(hostPatterns, ipPatterns []string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.globCache = make(map[string]glob.Glob)
	c.cidrCache = make(map[string]*net.IPNet)

	for _, pattern := range hostPatterns {
		cleanPattern := strings.TrimPrefix(pattern, "!")
		if _, exists := c.globCache[cleanPattern]; !exists {
			if g, err := glob.Compile(cleanPattern); err == nil {
				c.globCache[cleanPattern] = g
			}
		}
	}

	for _, cidr := range ipPatterns {
		cleanCidr := strings.TrimPrefix(cidr, "!")
		if _, exists := c.cidrCache[cleanCidr]; !exists {
			normalized := cleanCidr
			if !strings.Contains(cleanCidr, "/") {
				if ip := net.ParseIP(cleanCidr); ip != nil {
					if ip.To4() != nil {
						normalized += "/32"
					} else {
						normalized += "/128"
					}
				}
			}
			if _, ipNet, err := net.ParseCIDR(normalized); err == nil {
				c.cidrCache[cleanCidr] = ipNet
			}
		}
	}
}
