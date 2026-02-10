package handler

import (
	"fmt"
	"net"

	"goProxy/cache"
	"goProxy/config"
	"goProxy/logging"

	lru "github.com/hashicorp/golang-lru/v2"
	explru "github.com/hashicorp/golang-lru/v2/expirable"
)

type ProxyDecisionResult struct {
	Proxy     string
	RuleName  string
	MatchType string // "host", "ip", or "default"
}

type ProxyDecision struct {
	config    *config.ProxyConfig
	cache     *cache.CacheManager
	hostCache *lru.Cache[string, ProxyDecisionResult]
	ipCache   *explru.LRU[string, ProxyDecisionResult]
	logger    *logging.Logger
}

func NewProxyDecision(config *config.ProxyConfig, cacheManager *cache.CacheManager, logger *logging.Logger) *ProxyDecision {
	hostCache, _ := lru.New[string, ProxyDecisionResult](1000)
	ipCache := explru.NewLRU[string, ProxyDecisionResult](1000, nil, cache.IPResolutionTTL)

	return &ProxyDecision{
		config:    config,
		cache:     cacheManager,
		hostCache: hostCache,
		ipCache:   ipCache,
		logger:    logger,
	}
}

func (d *ProxyDecision) matchesGlob(pattern, s string) bool {
	hostWithoutPort := s

	if h, _, err := net.SplitHostPort(s); err == nil {
		hostWithoutPort = h
	} else {
		hostWithoutPort = s
	}

	if pattern == hostWithoutPort {
		return true
	}

	g, err := d.cache.GetGlob(pattern)
	if err != nil {
		return false
	}

	return g.Match(hostWithoutPort)
}

func (d *ProxyDecision) GetProxyForHost(host string) (proxyURL string, decision ProxyDecisionResult, err error) {
	decision = d.getProxyDecision(host)
	var exists bool
	proxyURL, exists = d.config.Proxies[decision.Proxy]
	if !exists {
		err = fmt.Errorf("proxy key '%s' not found in proxies map", decision.Proxy)
	}
	return
}

func (d *ProxyDecision) getProxyDecision(host string) ProxyDecisionResult {
	if result, exists := d.hostCache.Get(host); exists {
		d.logger.Debug("Host cache hit for %s: proxy=%s, rule=%s", host, result.Proxy, result.RuleName)
		return result
	}

	if result, exists := d.ipCache.Get(host); exists {
		d.logger.Debug("IP cache hit for %s: proxy=%s, rule=%s", host, result.Proxy, result.RuleName)
		return result
	}

	result := d.evaluateRules(host)

	switch result.MatchType {
	case "ip":
		d.ipCache.Add(host, result)
	default:
		d.hostCache.Add(host, result)
	}

	return result
}

func (d *ProxyDecision) evaluateRules(host string) ProxyDecisionResult {
	for _, rule := range d.config.Rules {
		matchesRule := false
		matchType := ""

		ipRules := rule.GetParsedIps()
		hostRules := rule.GetParsedHosts()

		if len(hostRules) > 0 {
			for _, hostRule := range hostRules {
				if d.matchesGlob(hostRule, host) {
					matchesRule = true
					matchType = "host"
					break
				}
			}
		}

		if !matchesRule && len(ipRules) > 0 {
			targetIP := net.ParseIP(host)
			var targetIPs []net.IP

			if targetIP != nil {
				targetIPs = []net.IP{targetIP}
			} else {
				ips, err := d.cache.ResolveHost(host)
				if err == nil {
					targetIPs = ips
					d.logger.Debug("Resolved target host %s to %v", host, ips)
				}
			}

			if len(targetIPs) > 0 {
				for _, ipRule := range ipRules {
					ipNet, err := d.cache.GetCIDRNet(ipRule)
					if err == nil {
						for _, tip := range targetIPs {
							if ipNet.Contains(tip) {
								d.logger.Debug("Match: target %s (IP: %s) fits CIDR rule %s", host, tip, ipRule)
								matchesRule = true
								matchType = "ip"
								break
							}
						}
					} else {
						d.logger.Debug("Rule '%s' is not a CIDR, attempting DNS resolve", ipRule)

						ruleIPs, err := d.cache.ResolveHost(ipRule)
						if err == nil {
							for _, rip := range ruleIPs {
								for _, tip := range targetIPs {
									if rip.Equal(tip) {
										d.logger.Debug("Match: target %s (IP: %s) matches IP %s from rule domain %s", host, tip, rip, ipRule)
										matchesRule = true
										matchType = "ip"
										break
									}
								}
								if matchesRule {
									break
								}
							}
						} else {
							d.logger.Debug("Failed to resolve domain rule '%s': %v", ipRule, err)
						}
					}
					if matchesRule {
						break
					}
				}
			}
		}

		ruleName := rule.Name
		if ruleName == "" {
			ruleName = "unnamed rule"
		}

		if rule.Not {
			matchesRule = !matchesRule
		}

		if matchesRule {
			return ProxyDecisionResult{
				Proxy:     rule.Proxy,
				RuleName:  ruleName,
				MatchType: matchType,
			}
		}
	}

	return ProxyDecisionResult{
		Proxy:     d.config.DefaultProxy,
		RuleName:  "default",
		MatchType: "default",
	}
}
