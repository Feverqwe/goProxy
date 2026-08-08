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
	IPResolutionTTL  = 5 * time.Minute
	dnsLookupTimeout = 10 * time.Second
)

type CacheManager struct {
	globCache map[string]glob.Glob
	cidrCache map[string]*net.IPNet
	dnsCache  *lru.LRU[string, []net.IP]
	dnsGroup  singleflight.Group
	mu        sync.RWMutex
}

type ExternalDNSOptions struct {
	Server     string
	SourceIPv4 string
	SourceIPv6 string
}

type externalDNSParameters struct {
	cacheKey  string
	flightKey string
	dnsAddr   string
	sourceIP  net.IP
}

func NewCacheManager() *CacheManager {
	dnsCache := lru.NewLRU[string, []net.IP](1000, nil, IPResolutionTTL)

	return &CacheManager{
		globCache: make(map[string]glob.Glob),
		cidrCache: make(map[string]*net.IPNet),
		dnsCache:  dnsCache,
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
	return c.ResolveHostContext(context.Background(), hostname)
}

func (c *CacheManager) ResolveHostContext(ctx context.Context, hostname string) ([]net.IP, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ip := net.ParseIP(hostname); ip != nil {
		return []net.IP{ip}, nil
	}

	return c.resolveCached(ctx, hostname, hostname, func(lookupCtx context.Context) ([]net.IP, error) {
		return net.DefaultResolver.LookupIP(lookupCtx, "ip", hostname)
	})
}

func cloneIPs(ips []net.IP) []net.IP {
	cloned := make([]net.IP, len(ips))
	for i, ip := range ips {
		cloned[i] = append(net.IP(nil), ip...)
	}
	return cloned
}

func (c *CacheManager) ResolveExternalHost(hostname string, options ExternalDNSOptions) ([]net.IP, error) {
	return c.ResolveExternalHostContext(context.Background(), hostname, options)
}

func (c *CacheManager) ResolveExternalHostContext(ctx context.Context, hostname string, options ExternalDNSOptions) ([]net.IP, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ip := net.ParseIP(hostname); ip != nil {
		return []net.IP{ip}, nil
	}

	parameters, err := makeExternalDNSParameters(hostname, options)
	if err != nil {
		return nil, err
	}

	return c.resolveCached(ctx, parameters.cacheKey, parameters.flightKey, func(lookupCtx context.Context) ([]net.IP, error) {
		resolver := net.DefaultResolver
		if parameters.dnsAddr != "" {
			resolver = &net.Resolver{
				PreferGo: true,
				Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
					d := net.Dialer{Timeout: 5 * time.Second}
					if parameters.sourceIP != nil {
						if strings.HasPrefix(network, "tcp") {
							d.LocalAddr = &net.TCPAddr{IP: parameters.sourceIP}
						} else {
							d.LocalAddr = &net.UDPAddr{IP: parameters.sourceIP}
						}
					}
					return d.DialContext(ctx, network, parameters.dnsAddr)
				},
			}
		}

		return resolver.LookupIP(lookupCtx, "ip", hostname)
	})
}

func (c *CacheManager) resolveCached(ctx context.Context, cacheKey, flightKey string, lookup func(context.Context) ([]net.IP, error)) ([]net.IP, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if ips, exists := c.dnsCache.Get(cacheKey); exists {
		return cloneIPs(ips), nil
	}

	resultCh := c.dnsGroup.DoChan(flightKey, func() (interface{}, error) {
		if ips, exists := c.dnsCache.Get(cacheKey); exists {
			return ips, nil
		}

		lookupCtx, cancel := context.WithTimeout(context.Background(), dnsLookupTimeout)
		defer cancel()
		ips, err := lookup(lookupCtx)
		if err != nil {
			return nil, err
		}

		cached := cloneIPs(ips)
		c.dnsCache.Add(cacheKey, cached)
		return cached, nil
	})

	select {
	case result := <-resultCh:
		if result.Err != nil {
			return nil, result.Err
		}
		return cloneIPs(result.Val.([]net.IP)), nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func makeExternalDNSParameters(hostname string, options ExternalDNSOptions) (externalDNSParameters, error) {
	parameters := externalDNSParameters{
		cacheKey: fmt.Sprintf("ext:%s:%s", options.Server, hostname),
	}
	if options.Server == "" {
		parameters.flightKey = parameters.cacheKey
		return parameters, nil
	}

	dnsHost := options.Server
	parameters.dnsAddr = options.Server
	if host, _, err := net.SplitHostPort(options.Server); err == nil {
		dnsHost = host
	} else {
		parameters.dnsAddr = net.JoinHostPort(options.Server, "53")
	}

	dnsIP := net.ParseIP(dnsHost)
	sourceIP := ""
	if dnsIP != nil {
		if dnsIP.To4() != nil {
			sourceIP = options.SourceIPv4
		} else {
			sourceIP = options.SourceIPv6
		}
	}
	if sourceIP != "" {
		parameters.sourceIP = net.ParseIP(sourceIP)
		if parameters.sourceIP == nil {
			return externalDNSParameters{}, fmt.Errorf("invalid DNS source IP %s", sourceIP)
		}
		if (dnsIP.To4() != nil) != (parameters.sourceIP.To4() != nil) {
			return externalDNSParameters{}, fmt.Errorf("DNS source IP %s has the wrong address family", sourceIP)
		}
	}

	parameters.flightKey = parameters.cacheKey + "\x00source=" + sourceIP
	return parameters, nil
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
