package handler

import (
	"net"
	"net/http"
	"strings"

	"goProxy/cache"
	"goProxy/config"
	"goProxy/logging"
)

type ProxyDecisionResult struct {
	Proxy    string
	RuleName string
}

type ProxyDecision struct {
	config *config.ProxyConfig
	cache  *cache.CacheManager
	logger *logging.Logger
}

func NewProxyDecision(config *config.ProxyConfig, cache *cache.CacheManager) *ProxyDecision {
	logger := logging.NewLogger(config)
	return &ProxyDecision{
		config: config,
		cache:  cache,
		logger: logger,
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

	for _, rule := range d.config.Rules {
		matchesRule := false

		// Use the parsed rules which now include both regular and external rules
		urlRules := rule.GetParsedURLs()
		ipRules := rule.GetParsedIps()
		hostRules := rule.GetParsedHosts()

		if len(urlRules) > 0 {
			fullURL := r.URL.String()
			for _, urlRule := range urlRules {
				if d.matchesURLPattern(urlRule, fullURL) {
					matchesRule = true
					break
				}
			}
		}

		if !matchesRule && len(hostRules) > 0 {
			for _, hostRule := range hostRules {
				if d.matchesGlob(hostRule, host) {
					matchesRule = true
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

		if rule.Not {

			if !matchesRule {
				ruleName := rule.Name
				if ruleName == "" {
					ruleName = "unnamed rule"
				}
				return ProxyDecisionResult{
					Proxy:    rule.Proxy,
					RuleName: ruleName,
				}
			}
		} else {

			if matchesRule {
				ruleName := rule.Name
				if ruleName == "" {
					ruleName = "unnamed rule"
				}
				return ProxyDecisionResult{
					Proxy:    rule.Proxy,
					RuleName: ruleName,
				}
			}
		}
	}

	return ProxyDecisionResult{
		Proxy:    d.config.DefaultProxy,
		RuleName: "default",
	}
}
