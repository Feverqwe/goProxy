package config

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

func parseStringToList(input string, expandWildcardDomains bool) []string {
	if input == "" {
		return []string{}
	}

	lines := strings.Split(input, "\n")
	var cleanedLines []string

	for _, line := range lines {
		if idx := strings.Index(line, "//"); idx != -1 {
			beforeComment := strings.TrimSpace(line[:idx])
			if beforeComment == "" {
				line = line[:idx]
			}
		}
		if idx := strings.Index(line, "#"); idx != -1 {
			beforeComment := strings.TrimSpace(line[:idx])
			if beforeComment == "" {
				line = line[:idx]
			}
		}
		cleanedLines = append(cleanedLines, strings.TrimSpace(line))
	}

	cleanedInput := strings.Join(cleanedLines, " ")
	normalized := strings.ReplaceAll(cleanedInput, ",", " ")
	parts := strings.Fields(normalized)

	var result []string
	for _, part := range parts {
		if part != "" {
			result = append(result, part)

			if expandWildcardDomains && strings.HasPrefix(part, "*.") {
				baseDomain := strings.TrimPrefix(part, "*.")
				result = append(result, baseDomain)
			}
		}
	}
	return result
}

func (c *ProxyConfig) preParseRuleLists(configDir string, cacheOnly bool, httpClientFunc HTTPClientFunc) {
	for i := range c.Rules {
		rule := &c.Rules[i]

		externalRule := &RuleBaseConfig{}
		if rule.ExternalRule != "" {
			var err error
			externalRule, err = c.loadExternalRuleFile(rule.ExternalRule, configDir, cacheOnly, httpClientFunc)
			if err != nil {
				c.logger.Warn("Failed to load external rule file: %v", err)
			}
		}

		expandTokens := func(input string, expandWildcardDomains bool) []string {
			if input == "" {
				return []string{}
			}
			tokens := parseStringToList(input, expandWildcardDomains)
			var expanded []string

			for _, t := range tokens {
				isNegation := strings.HasPrefix(t, "!")
				cleanToken := strings.TrimPrefix(t, "!")

				if strings.HasPrefix(cleanToken, "http://") || strings.HasPrefix(cleanToken, "https://") || strings.HasPrefix(cleanToken, "/") || strings.HasPrefix(cleanToken, "./") {
					list := c.loadExternalRuleList(cleanToken, expandWildcardDomains, configDir, cacheOnly, httpClientFunc)
					for _, line := range list {
						if isNegation {
							expanded = append(expanded, "!"+line)
						} else {
							expanded = append(expanded, line)
						}
					}
				} else {
					expanded = append(expanded, t)
				}
			}
			return expanded
		}

		rule.parsedHosts = append(expandTokens(rule.Hosts, true), expandTokens(externalRule.Hosts, true)...)
		rule.parsedIps = append(expandTokens(rule.Ips, false), expandTokens(externalRule.Ips, false)...)

		if rule.Name == "" {
			if externalRule.Name != "" {
				rule.Name = externalRule.Name
			} else {
				rule.Name = "unnamed rule"
			}
		}
	}
}

func (c *ProxyConfig) loadExternalRuleFile(source string, configDir string, cacheOnly bool, httpClientFunc HTTPClientFunc) (*RuleBaseConfig, error) {
	if source == "" {
		return &RuleBaseConfig{}, nil
	}

	content, err := loadExternalRules(source, configDir, cacheOnly, httpClientFunc, c.logger)
	if err != nil {
		return nil, fmt.Errorf("failed to load external rule file: %v", err)
	}

	var externalRule RuleBaseConfig
	if err := yaml.Unmarshal([]byte(content), &externalRule); err != nil {
		return nil, fmt.Errorf("failed to parse external rule file: %v", err)
	}

	return &externalRule, nil
}

func (c *ProxyConfig) loadExternalRuleList(url string, expandWildcardDomains bool, configDir string, cacheOnly bool, httpClientFunc HTTPClientFunc) []string {
	if url == "" {
		return []string{}
	}

	rulesContent, err := loadExternalRules(url, configDir, cacheOnly, httpClientFunc, c.logger)
	if err != nil {
		c.logger.Warn("Failed to load external rules from %s: %v", url, err)
		return []string{}
	}

	return parseStringToList(rulesContent, expandWildcardDomains)
}
