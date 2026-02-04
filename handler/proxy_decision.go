package handler

import (
	"net"
	"net/http"
	"strings"

	"goProxy/cache"
	"goProxy/config"
	"goProxy/logging"

	lru "github.com/hashicorp/golang-lru/v2"
	explru "github.com/hashicorp/golang-lru/v2/expirable"
)

type ProxyDecisionResult struct {
	Proxy     string
	RuleName  string
	MatchType string // "url", "host", "ip", or "default"
}

type ProxyDecision struct {
	config    *config.ProxyConfig
	cache     *cache.CacheManager
	logger    *logging.Logger
	hostCache *lru.Cache[string, ProxyDecisionResult]
	urlCache  *lru.Cache[string, ProxyDecisionResult]
	ipCache   *explru.LRU[string, ProxyDecisionResult]
}

func NewProxyDecision(config *config.ProxyConfig, cacheManager *cache.CacheManager) *ProxyDecision {
	logger := logging.NewLogger(config)

	hostCache, _ := lru.New[string, ProxyDecisionResult](1000)
	urlCache, _ := lru.New[string, ProxyDecisionResult](1000)
	ipCache := explru.NewLRU[string, ProxyDecisionResult](1000, nil, cache.IPResolutionTTL)

	return &ProxyDecision{
		config:    config,
		cache:     cacheManager,
		logger:    logger,
		hostCache: hostCache,
		urlCache:  urlCache,
		ipCache:   ipCache,
	}
}

func (d *ProxyDecision) matchesGlob(pattern, s string) bool {
	hostWithoutPort := s
	if strings.Contains(s, ":") {
		hostParts := strings.Split(s, ":")
		if len(hostParts) == 2 {
			hostWithoutPort = hostParts[0]
		}
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

func (d *ProxyDecision) matchesURLPattern(pattern, url string) bool {
	g, err := d.cache.GetGlob(pattern)
	if err != nil {
		return false
	}

	return g.Match(url)
}

func (d *ProxyDecision) GetProxyForRequest(r *http.Request) ProxyDecisionResult {
	host := r.URL.Hostname()
	fullURL := r.URL.String()

	if result, exists := d.urlCache.Get(fullURL); exists {
		if d.config.ShouldLog(config.LogLevelDebug) {
			d.logger.Debug("URL cache hit for %s: proxy=%s, rule=%s", fullURL, result.Proxy, result.RuleName)
		}
		return result
	}

	if result, exists := d.hostCache.Get(host); exists {
		if d.config.ShouldLog(config.LogLevelDebug) {
			d.logger.Debug("Host cache hit for %s: proxy=%s, rule=%s", host, result.Proxy, result.RuleName)
		}
		return result
	}

	if result, exists := d.ipCache.Get(host); exists {
		if d.config.ShouldLog(config.LogLevelDebug) {
			d.logger.Debug("IP cache hit for %s: proxy=%s, rule=%s", host, result.Proxy, result.RuleName)
		}
		return result
	}

	result := d.evaluateRules(r, host, fullURL)

	switch result.MatchType {
	case "url":
		d.urlCache.Add(fullURL, result)
	case "ip":
		d.ipCache.Add(host, result)
	default:
		d.hostCache.Add(host, result)
	}

	return result
}

func (d *ProxyDecision) evaluateRules(r *http.Request, host, fullURL string) ProxyDecisionResult {
	for _, rule := range d.config.Rules {
		matchesRule := false
		matchType := ""

		urlRules := rule.GetParsedURLs()
		ipRules := rule.GetParsedIps()
		hostRules := rule.GetParsedHosts()

		if len(urlRules) > 0 {
			for _, urlRule := range urlRules {
				if d.matchesURLPattern(urlRule, fullURL) {
					matchesRule = true
					matchType = "url"
					break
				}
			}
		}

		if !matchesRule && len(hostRules) > 0 {
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
					if d.config.ShouldLog(config.LogLevelDebug) {
						d.logger.Debug("Resolved target host %s to %v", host, ips)
					}
				}
			}

			if len(targetIPs) > 0 {
				for _, ipRule := range ipRules {
					ipNet, err := d.cache.GetCIDRNet(ipRule)
					if err == nil {
						for _, tip := range targetIPs {
							if ipNet.Contains(tip) {
								if d.config.ShouldLog(config.LogLevelDebug) {
									d.logger.Debug("Match: target %s (IP: %s) fits CIDR rule %s", host, tip, ipRule)
								}
								matchesRule = true
								matchType = "ip"
								break
							}
						}
					} else {
						if d.config.ShouldLog(config.LogLevelDebug) {
							d.logger.Debug("Rule '%s' is not a CIDR, attempting DNS resolve", ipRule)
						}

						ruleIPs, err := d.cache.ResolveHost(ipRule)
						if err == nil {
							for _, rip := range ruleIPs {
								for _, tip := range targetIPs {
									if rip.Equal(tip) {
										if d.config.ShouldLog(config.LogLevelDebug) {
											d.logger.Debug("Match: target %s (IP: %s) matches IP %s from rule domain %s", host, tip, rip, ipRule)
										}
										matchesRule = true
										matchType = "ip"
										break
									}
								}
								if matchesRule {
									break
								}
							}
						} else if d.config.ShouldLog(config.LogLevelDebug) {
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
