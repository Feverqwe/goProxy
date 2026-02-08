package config

import (
	"fmt"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

func ParseStringToList(input string, expandWildcardDomains bool) []string {
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

func (c *ProxyConfig) preParseRuleLists(configDir string) {
	c.preParseRuleListsWithMode(configDir, false)
}

func (c *ProxyConfig) preParseRuleListsWithMode(configDir string, cacheOnly bool) {
	for i := range c.Rules {
		rule := &c.Rules[i]

		if rule.ExternalRule != "" {
			externalRule, err := c.loadExternalRuleFileWithMode(rule.ExternalRule, configDir, cacheOnly)
			if err != nil {
				fmt.Printf("Warning: Failed to load external rule file from %s: %v\n", rule.ExternalRule, err)
			} else {
				c.mergeRuleFields(rule, externalRule)
			}
		}

		rule.parsedIps = ParseStringToList(rule.Ips, false)
		rule.parsedHosts = ParseStringToList(rule.Hosts, true)
		rule.parsedURLs = ParseStringToList(rule.URLs, false)

		type loadTask struct {
			sources         []string
			expandWildcards bool
			result          *[]string
		}

		tasks := []loadTask{
			{ParseStringToList(rule.ExternalIps, false), false, &rule.parsedIps},
			{ParseStringToList(rule.ExternalHosts, false), true, &rule.parsedHosts},
			{ParseStringToList(rule.ExternalURLs, false), false, &rule.parsedURLs},
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
					rules := c.loadExternalRuleListWithMode(source, expandWildcards, configDir, cacheOnly)
					mu.Lock()
					*result = append(*result, rules...)
					mu.Unlock()
				}(source, task.expandWildcards, task.result)
			}
		}

		wg.Wait()
	}
}

func (c *ProxyConfig) RefreshExternalRules(configDir string) {
	c.preParseRuleListsWithMode(configDir, false)
}

func (c *ProxyConfig) mergeRuleFields(mainRule *RuleConfig, externalRule *RuleBaseConfig) {
	if mainRule.Name == "" && externalRule.Name != "" {
		mainRule.Name = externalRule.Name
	}
	if !mainRule.Not && externalRule.Not {
		mainRule.Not = externalRule.Not
	}

	mainRule.Ips = strings.TrimSpace(mainRule.Ips + "\n" + externalRule.Ips)
	mainRule.Hosts = strings.TrimSpace(mainRule.Hosts + "\n" + externalRule.Hosts)
	mainRule.URLs = strings.TrimSpace(mainRule.URLs + "\n" + externalRule.URLs)

	mainRule.ExternalIps = strings.TrimSpace(mainRule.ExternalIps + "\n" + externalRule.ExternalIps)
	mainRule.ExternalHosts = strings.TrimSpace(mainRule.ExternalHosts + "\n" + externalRule.ExternalHosts)
	mainRule.ExternalURLs = strings.TrimSpace(mainRule.ExternalURLs + "\n" + externalRule.ExternalURLs)
}

func (c *ProxyConfig) loadExternalRuleFile(source string, configDir string) (*RuleBaseConfig, error) {
	return c.loadExternalRuleFileWithMode(source, configDir, false)
}

func (c *ProxyConfig) loadExternalRuleFileWithMode(source string, configDir string, cacheOnly bool) (*RuleBaseConfig, error) {
	if source == "" {
		return &RuleBaseConfig{}, nil
	}

	content, err := loadExternalRulesRelativeToWithMode(source, configDir, cacheOnly, c)
	if err != nil {
		return nil, fmt.Errorf("failed to load external rule file: %v", err)
	}

	var externalRule RuleBaseConfig
	if err := yaml.Unmarshal([]byte(content), &externalRule); err != nil {
		return nil, fmt.Errorf("failed to parse external rule file: %v", err)
	}

	return &externalRule, nil
}

func (c *ProxyConfig) loadExternalRuleList(url string, expandWildcardDomains bool, configDir string) []string {
	return c.loadExternalRuleListWithMode(url, expandWildcardDomains, configDir, false)
}

func (c *ProxyConfig) loadExternalRuleListWithMode(url string, expandWildcardDomains bool, configDir string, cacheOnly bool) []string {
	if url == "" {
		return []string{}
	}

	rulesContent, err := loadExternalRulesRelativeToWithMode(url, configDir, cacheOnly, c)
	if err != nil {
		fmt.Printf("Warning: Failed to load external rules from %s: %v\n", url, err)
		return []string{}
	}

	return ParseStringToList(rulesContent, expandWildcardDomains)
}
