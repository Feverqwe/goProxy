package config

import (
	"fmt"
	"goProxy/logger"
	"strings"
	"sync"

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
				logger.Warn("Failed to load external rule file from %s: %v", rule.ExternalRule, err)
			}
		}

		parsedIps := parseStringToList(strings.TrimSpace(rule.Ips+"\n"+externalRule.Ips), false)
		parsedHosts := parseStringToList(strings.TrimSpace(rule.Hosts+"\n"+externalRule.Hosts), true)
		parsedURLs := parseStringToList(strings.TrimSpace(rule.URLs+"\n"+externalRule.URLs), false)

		type loadTask struct {
			sources         []string
			expandWildcards bool
			result          *[]string
		}

		tasks := []loadTask{
			{parseStringToList(strings.TrimSpace(rule.ExternalIps+"\n"+externalRule.ExternalIps), false), false, &parsedIps},
			{parseStringToList(strings.TrimSpace(rule.ExternalHosts+"\n"+externalRule.ExternalHosts), false), true, &parsedHosts},
			{parseStringToList(strings.TrimSpace(rule.ExternalURLs+"\n"+externalRule.ExternalURLs), false), false, &parsedURLs},
		}

		var wg sync.WaitGroup
		var mu sync.Mutex

		for _, task := range tasks {
			for _, source := range task.sources {
				if source == "" {
					continue
				}

				wg.Add(1)
				go func(source string, expandWildcards bool, result *[]string) {
					defer wg.Done()
					rules := c.loadExternalRuleList(source, expandWildcards, configDir, cacheOnly, httpClientFunc)
					mu.Lock()
					*result = append(*result, rules...)
					mu.Unlock()
				}(source, task.expandWildcards, task.result)
			}
		}

		wg.Wait()

		if rule.Name == "" && externalRule.Name != "" {
			rule.Name = externalRule.Name
		}

		rule.parsedHosts = parsedHosts
		rule.parsedIps = parsedIps
		rule.parsedURLs = parsedURLs
	}
}

func (c *ProxyConfig) loadExternalRuleFile(source string, configDir string, cacheOnly bool, httpClientFunc HTTPClientFunc) (*RuleBaseConfig, error) {
	if source == "" {
		return &RuleBaseConfig{}, nil
	}

	content, err := loadExternalRules(source, configDir, cacheOnly, httpClientFunc)
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

	rulesContent, err := loadExternalRules(url, configDir, cacheOnly, httpClientFunc)
	if err != nil {
		logger.Warn("Failed to load external rules from %s: %v", url, err)
		return []string{}
	}

	return parseStringToList(rulesContent, expandWildcardDomains)
}
