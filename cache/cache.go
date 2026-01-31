package cache

import (
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/gobwas/glob"
	lru "github.com/hashicorp/golang-lru/v2/expirable"
)

// CacheManager управляет различными типами кешей для оптимизации производительности
type CacheManager struct {
	globCache map[string]glob.Glob
	cidrCache map[string]*net.IPNet
	dnsCache  *lru.LRU[string, []net.IP]
}

// NewCacheManager создает новый менеджер кешей
func NewCacheManager() *CacheManager {
	// Создаем LRU кеш для DNS с TTL 5 минут и максимальным размером 1000 записей
	dnsCache := lru.NewLRU[string, []net.IP](1000, nil, 5*time.Minute)

	return &CacheManager{
		globCache: make(map[string]glob.Glob),
		cidrCache: make(map[string]*net.IPNet),
		dnsCache:  dnsCache,
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

// GetCIDRNet возвращает CIDR сеть с кешированием, поддерживая одиночные IP
func (c *CacheManager) GetCIDRNet(cidr string) (*net.IPNet, error) {
	if ipNet, exists := c.cidrCache[cidr]; exists {
		return ipNet, nil
	}

	normalizedCIDR := cidr
	// Проверяем, содержит ли строка уже маску
	if !strings.Contains(cidr, "/") {
		ip := net.ParseIP(cidr)
		if ip == nil {
			return nil, fmt.Errorf("invalid IP address: %s", cidr)
		}

		// Определяем версию IP и добавляем соответствующую маску
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

	c.cidrCache[cidr] = ipNet
	return ipNet, nil
}

// ResolveHost разрешает доменное имя в IP адреса с кешированием
func (c *CacheManager) ResolveHost(hostname string) ([]net.IP, error) {
	// Проверяем кеш (LRU автоматически управляет TTL)
	if ips, exists := c.dnsCache.Get(hostname); exists {
		return ips, nil
	}

	// Разрешаем доменное имя
	ips, err := net.LookupIP(hostname)
	if err != nil {
		return nil, err
	}

	// Сохраняем в кеш (LRU автоматически управляет TTL и размером)
	c.dnsCache.Add(hostname, ips)

	return ips, nil
}

// PrecompilePatterns предварительно компилирует паттерны для быстрого доступа
func (c *CacheManager) PrecompilePatterns(hostPatterns, urlPatterns, ipPatterns []string) {
	// Очищаем существующие кэши перед компиляцией новых паттернов
	c.globCache = make(map[string]glob.Glob)
	c.cidrCache = make(map[string]*net.IPNet)

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
