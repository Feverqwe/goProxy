package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

func parseStringToList(input string, expandWildcardDomains bool) []string {
	if input == "" {
		return []string{}
	}

	result := make([]string, 0, strings.Count(input, "\n")+1)
	for len(input) > 0 {
		line := input
		if newline := strings.IndexByte(input, '\n'); newline >= 0 {
			line = input[:newline]
			input = input[newline+1:]
		} else {
			input = ""
		}
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
		parts := strings.FieldsFunc(line, func(r rune) bool { return r == ',' || unicode.IsSpace(r) })
		for _, part := range parts {
			result = append(result, part)
			isNegation := strings.HasPrefix(part, "!")
			cleanToken := strings.TrimPrefix(part, "!")

			if expandWildcardDomains && strings.HasPrefix(cleanToken, "*.") {
				baseDomain := strings.TrimPrefix(cleanToken, "*.")
				if isNegation {
					baseDomain = "!" + baseDomain
				}
				result = append(result, baseDomain)
			}
		}
	}
	return result
}

func (c *ProxyConfig) preParseRuleLists(configDir string, cacheOnly bool, httpClientFunc HTTPClientFunc, forceReload bool, ttl int) {
	handleRule := func(ruleIndex int) {
		rule := &c.Rules[ruleIndex]

		var externalRule *RuleBaseConfig
		if rule.ExternalRule != "" {
			var err error
			externalRule, err = c.loadExternalRuleFile(rule.ExternalRule, configDir, cacheOnly, httpClientFunc, forceReload, ttl)
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
					list := c.loadExternalRuleList(cleanToken, expandWildcardDomains, configDir, cacheOnly, httpClientFunc, forceReload, ttl)
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

		var hosts hostRuleBuilder
		expandHostTokens := func(input string) {
			for _, token := range parseStringToList(input, false) {
				isNegation := strings.HasPrefix(token, "!")
				cleanToken := strings.TrimPrefix(token, "!")
				if isExternalRuleSource(cleanToken) {
					err := c.loadExternalHostRules(cleanToken, configDir, cacheOnly, httpClientFunc, forceReload, ttl, &hosts, isNegation)
					if err != nil {
						c.logger.Warn("Failed to load external rules from %s: %v", cleanToken, err)
					}
					continue
				}
				hosts.addString(token, false)
			}
		}

		expandHostTokens(rule.Hosts)
		rule.parsedIps = expandTokens(rule.Ips, false)
		if externalRule != nil {
			expandHostTokens(externalRule.Hosts)
			rule.parsedIps = append(rule.parsedIps, expandTokens(externalRule.Ips, false)...)
		}
		rule.parsedHosts = hosts.patterns
		rule.hostIndex = hosts.index

		if rule.Name == "" {
			if externalRule != nil && externalRule.Name != "" {
				rule.Name = externalRule.Name
			} else {
				rule.Name = "unnamed rule"
			}
		}
	}

	for i := range c.Rules {
		handleRule(i)
	}
}

func isExternalRuleSource(token string) bool {
	return strings.HasPrefix(token, "http://") || strings.HasPrefix(token, "https://") || strings.HasPrefix(token, "/") || strings.HasPrefix(token, "./")
}

func (c *ProxyConfig) loadExternalRuleFile(source string, configDir string, cacheOnly bool, httpClientFunc HTTPClientFunc, forceReload bool, ttl int) (*RuleBaseConfig, error) {
	if source == "" {
		return nil, nil
	}

	content, err := loadExternalRules(source, configDir, cacheOnly, httpClientFunc, c.logger, forceReload, ttl)
	if err != nil {
		return nil, fmt.Errorf("failed to load external rule file: %v", err)
	}

	var externalRule RuleBaseConfig
	if err := yaml.Unmarshal([]byte(content), &externalRule); err != nil {
		return nil, fmt.Errorf("failed to parse external rule file: %v", err)
	}

	return &externalRule, nil
}

func (c *ProxyConfig) loadExternalRuleList(url string, expandWildcardDomains bool, configDir string, cacheOnly bool, httpClientFunc HTTPClientFunc, forceReload bool, ttl int) []string {
	if url == "" {
		return []string{}
	}

	rulesContent, err := loadExternalRules(url, configDir, cacheOnly, httpClientFunc, c.logger, forceReload, ttl)
	if err != nil {
		c.logger.Warn("Failed to load external rules from %s: %v", url, err)
		return []string{}
	}

	return parseStringToList(rulesContent, expandWildcardDomains)
}

func (c *ProxyConfig) loadExternalHostRules(source string, configDir string, cacheOnly bool, httpClientFunc HTTPClientFunc, forceReload bool, ttl int, hosts *hostRuleBuilder, forceNegation bool) error {
	filePath, err := resolveExternalRulesPath(source, configDir, cacheOnly, httpClientFunc, c.logger, forceReload, ttl)
	if err != nil {
		return err
	}

	file, err := os.Open(filePath)
	if err != nil {
		return fmt.Errorf("failed to open file %s: %v", filePath, err)
	}
	defer file.Close()

	var capacity hostRuleCapacity
	if err := scanRuleTokens(file, func(token []byte) {
		capacity.observe(token, forceNegation)
	}); err != nil {
		return fmt.Errorf("failed to read file %s: %v", filePath, err)
	}
	hosts.reserve(capacity)
	if _, err := file.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("failed to rewind file %s: %v", filePath, err)
	}
	if err := scanRuleTokens(file, func(token []byte) {
		hosts.add(token, forceNegation)
	}); err != nil {
		return fmt.Errorf("failed to read file %s: %v", filePath, err)
	}
	return nil
}

// scanRuleTokens tokenizes a list without loading the whole file. A line whose
// first non-space characters are # or // is treated as a comment, matching the
// existing list syntax.
func scanRuleTokens(reader io.Reader, visit func([]byte)) error {
	buffered := bufio.NewReaderSize(reader, 64*1024)
	chunk := make([]byte, 64*1024)
	token := make([]byte, 0, 256)
	atLineStart := true
	commentLine := false
	pendingSlash := false

	emit := func() {
		if len(token) != 0 {
			visit(token)
			token = token[:0]
		}
	}

	for {
		n, err := buffered.Read(chunk)
		for _, b := range chunk[:n] {
			if commentLine {
				if b == '\n' {
					commentLine = false
					atLineStart = true
				}
				continue
			}

			if pendingSlash {
				pendingSlash = false
				if b == '/' {
					commentLine = true
					continue
				}
				token = append(token, '/')
				atLineStart = false
			}

			if b == '\n' {
				emit()
				atLineStart = true
				continue
			}
			if atLineStart {
				switch b {
				case ' ', '\t', '\r', '\v', '\f':
					continue
				case '#':
					commentLine = true
					continue
				case '/':
					pendingSlash = true
					continue
				default:
					atLineStart = false
				}
			}

			switch b {
			case ' ', '\t', '\r', '\v', '\f', ',':
				emit()
			default:
				token = append(token, b)
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
	}

	if pendingSlash {
		token = append(token, '/')
	}
	emit()
	return nil
}
