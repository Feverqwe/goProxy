package handler

import (
	"fmt"
	"net"
	"strings"
	"time"

	"goProxy/internal/cache"
	"goProxy/internal/config"
	"goProxy/internal/logging"

	explru "github.com/hashicorp/golang-lru/v2/expirable"
)

type ProxyDecisionResult struct {
	Proxy     string
	RuleName  string
	MatchType string // "host", "ip", "invert", or "default"
}

type ProxyDecision struct {
	config        *config.ProxyConfig
	cache         *cache.CacheManager
	decisionCache *explru.LRU[string, ProxyDecisionResult]
	logger        *logging.Logger
}

func NewProxyDecision(config *config.ProxyConfig, cacheManager *cache.CacheManager, logger *logging.Logger) *ProxyDecision {
	return newProxyDecision(config, cacheManager, logger, cache.IPResolutionTTL)
}

func newProxyDecision(config *config.ProxyConfig, cacheManager *cache.CacheManager, logger *logging.Logger, ttl time.Duration) *ProxyDecision {
	decisionCache := explru.NewLRU[string, ProxyDecisionResult](1000, nil, ttl)

	return &ProxyDecision{
		config:        config,
		cache:         cacheManager,
		decisionCache: decisionCache,
		logger:        logger,
	}
}

func (d *ProxyDecision) matchesGlob(pattern, s string) bool {
	pattern = normalizeHost(pattern)
	s = normalizeHost(s)

	if pattern == s {
		return true
	}

	g, err := d.cache.GetGlob(pattern)
	if err != nil {
		return false
	}

	return g.Match(s)
}

func (d *ProxyDecision) GetProxyForHost(host string) (proxyURL string, decision ProxyDecisionResult, err error) {
	host = normalizeHost(host)
	decision = d.getProxyDecision(host)
	var exists bool
	proxyURL, exists = d.config.Proxies[decision.Proxy]
	if !exists {
		err = fmt.Errorf("proxy key '%s' not found in proxies map", decision.Proxy)
	}
	return
}

func normalizeHost(host string) string {
	host = strings.TrimSpace(host)
	host = strings.TrimSuffix(host, ".")
	return strings.ToLower(host)
}

func (d *ProxyDecision) getProxyDecision(host string) ProxyDecisionResult {
	if result, exists := d.decisionCache.Get(host); exists {
		d.logger.Debug("Host cache hit for %s: proxy=%s, rule=%s", host, result.Proxy, result.RuleName)
		return result
	}

	result := d.evaluateRules(host)
	d.decisionCache.Add(host, result)

	return result
}

func (d *ProxyDecision) evaluateRules(host string) ProxyDecisionResult {
OuterLoop:
	for _, rule := range d.config.Rules {
		matchFound := false
		matchType := ""
		parsedHosts := rule.GetParsedHosts()
		parsedIps := rule.GetParsedIps()

		for _, pattern := range parsedHosts {
			isNegation := strings.HasPrefix(pattern, "!")
			actualPattern := strings.TrimPrefix(pattern, "!")

			if d.matchesGlob(actualPattern, host) {
				if isNegation {
					d.logger.Debug("Rule '%s' negated by host pattern '%s' for host '%s'. Skipping rule.", rule.Name, pattern, host)
					continue OuterLoop
				}
				d.logger.Debug("Rule '%s' match: target '%s', pattern '%s'", rule.Name, host, actualPattern)
				matchFound = true
				matchType = "host"
			}
		}

		if len(parsedIps) > 0 {
			targetIPs, err := d.cache.ResolveHost(host)
			if err == nil {
				d.logger.Debug("Resolved target host %s to %v", host, targetIPs)

				for _, ipPattern := range parsedIps {
					isNegation := strings.HasPrefix(ipPattern, "!")
					actualIP := strings.TrimPrefix(ipPattern, "!")

					if d.checkIPMatch(actualIP, targetIPs) {
						if isNegation {
							d.logger.Debug("Rule '%s' negated by IP pattern '%s' for host '%s'. Skipping rule.", rule.Name, ipPattern, host)
							continue OuterLoop
						}
						if !matchFound {
							d.logger.Debug("Rule '%s' match: target '%s', pattern '%s'", rule.Name, host, ipPattern)
							matchFound = true
							matchType = "ip"
						}
					}
				}
			}
		}

		if matchFound {
			return ProxyDecisionResult{
				Proxy:     rule.Proxy,
				RuleName:  rule.Name,
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

func (d *ProxyDecision) checkIPMatch(pattern string, targetIPs []net.IP) bool {
	if ipNet, err := d.cache.GetCIDRNet(pattern); err == nil {
		for _, tip := range targetIPs {
			if ipNet.Contains(tip) {
				return true
			}
		}
		return false
	}

	ruleIPs, err := d.cache.ResolveHost(pattern)
	if err == nil {
		for _, rip := range ruleIPs {
			for _, tip := range targetIPs {
				if rip.Equal(tip) {
					return true
				}
			}
		}
	} else {
		d.logger.Debug("Failed to resolve domain rule '%s': %v", pattern, err)
	}
	return false
}
