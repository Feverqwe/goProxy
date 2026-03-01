package cache

import (
	"context"
	"fmt"
	"math/rand"
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
	globCache map[string]glob.Glob
	cidrCache map[string]*net.IPNet
	dnsCache  *lru.LRU[string, []net.IP]
	dnsGroup  singleflight.Group
	mu        sync.RWMutex
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

func shuffledIps(ips []net.IP) []net.IP {
	shuffled := make([]net.IP, len(ips))
	copy(shuffled, ips)

	if len(shuffled) > 1 {
		rand.Shuffle(len(shuffled), func(i, j int) {
			shuffled[i], shuffled[j] = shuffled[j], shuffled[i]
		})
	}

	return shuffled
}

func (c *CacheManager) ResolveExternalHost(hostname, extDns string, getSourceIpByIps func(ips []net.IP) string) ([]net.IP, error) {
	if ip := net.ParseIP(hostname); ip != nil {
		return []net.IP{ip}, nil
	}

	cacheKey := fmt.Sprintf("ext:%s:%s", extDns, hostname)

	if ips, exists := c.dnsCache.Get(cacheKey); exists {
		return shuffledIps(ips), nil
	}

	isInflughtCache := true
	ipsInterface, err, _ := c.dnsGroup.Do(cacheKey, func() (interface{}, error) {
		isInflughtCache = false
		if ips, exists := c.dnsCache.Get(cacheKey); exists {
			return shuffledIps(ips), nil
		}

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		var resolver *net.Resolver
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
					dnsIp := net.ParseIP(dnsHost)
					sourceIP := getSourceIpByIps([]net.IP{dnsIp})
					d := net.Dialer{Timeout: time.Second * 5}
					if sourceIP != "" {
						d.LocalAddr = &net.UDPAddr{IP: net.ParseIP(sourceIP)}
					}
					return d.DialContext(ctx, "udp", dnsAddr)
				},
			}
		} else {
			resolver = net.DefaultResolver
		}

		ips, err := resolver.LookupIP(ctx, "ip", hostname)
		if err != nil {
			return nil, err
		}

		c.dnsCache.Add(cacheKey, ips)
		return ips, nil
	})

	if err != nil {
		return nil, err
	}

	ips := ipsInterface.([]net.IP)
	if isInflughtCache {
		ips = shuffledIps(ips)
	}

	return ips, nil
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
