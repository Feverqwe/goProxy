package cache

import (
	"net"

	"github.com/gobwas/glob"
)

// CacheManager управляет различными типами кешей для оптимизации производительности
type CacheManager struct {
	globCache map[string]glob.Glob
	cidrCache map[string]*net.IPNet
}

// NewCacheManager создает новый менеджер кешей
func NewCacheManager() *CacheManager {
	return &CacheManager{
		globCache: make(map[string]glob.Glob),
		cidrCache: make(map[string]*net.IPNet),
	}
}

// GetGlob возвращает скомпилированный glob-паттерн с кешированием
func (c *CacheManager) GetGlob(pattern string) (glob.Glob, error) {
	if g, exists := c.globCache[pattern]; exists {
		return g, nil
	}

	g, err := glob.Compile(pattern)
	if err != nil {
		return nil, err
	}

	c.globCache[pattern] = g
	return g, nil
}

// GetCIDRNet возвращает CIDR сеть с кешированием
func (c *CacheManager) GetCIDRNet(cidr string) (*net.IPNet, error) {
	if ipNet, exists := c.cidrCache[cidr]; exists {
		return ipNet, nil
	}

	_, ipNet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, err
	}

	c.cidrCache[cidr] = ipNet
	return ipNet, nil
}

// PrecompilePatterns предварительно компилирует паттерны для быстрого доступа
func (c *CacheManager) PrecompilePatterns(hostPatterns, urlPatterns, ipPatterns []string) {
	// Компилируем host паттерны
	for _, pattern := range hostPatterns {
		if g, err := glob.Compile(pattern); err == nil {
			c.globCache[pattern] = g
		}
	}

	// Компилируем URL паттерны
	for _, pattern := range urlPatterns {
		if g, err := glob.Compile(pattern); err == nil {
			c.globCache[pattern] = g
		}
	}

	// Компилируем IP паттерны
	for _, cidr := range ipPatterns {
		if _, ipNet, err := net.ParseCIDR(cidr); err == nil {
			c.cidrCache[cidr] = ipNet
		}
	}
}
