package cache

import (
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/gobwas/glob"
	lru "github.com/hashicorp/golang-lru/v2/expirable"
)

const (
	IPResolutionTTL = 5 * time.Minute
)

type CacheManager struct {
	globCache map[string]glob.Glob
	cidrCache map[string]*net.IPNet
	dnsCache  *lru.LRU[string, []net.IP]
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
	if ips, exists := c.dnsCache.Get(hostname); exists {
		return ips, nil
	}

	ips, err := net.LookupIP(hostname)
	if err != nil {
		return nil, err
	}

	c.dnsCache.Add(hostname, ips)

	return ips, nil
}

func (c *CacheManager) PrecompilePatterns(hostPatterns, urlPatterns, ipPatterns []string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.globCache = make(map[string]glob.Glob)
	c.cidrCache = make(map[string]*net.IPNet)

	for _, pattern := range hostPatterns {
		if g, err := glob.Compile(pattern); err == nil {
			c.globCache[pattern] = g
		}
	}

	for _, pattern := range urlPatterns {
		if g, err := glob.Compile(pattern); err == nil {
			c.globCache[pattern] = g
		}
	}

	for _, cidr := range ipPatterns {
		if _, ipNet, err := net.ParseCIDR(cidr); err == nil {
			c.cidrCache[cidr] = ipNet
		}
	}
}
