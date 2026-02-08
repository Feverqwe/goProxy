package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

func parseLogLevel(level string) int {
	switch strings.ToLower(level) {
	case "debug":
		return LogLevelDebug
	case "info":
		return LogLevelInfo
	case "warn":
		return LogLevelWarn
	case "error":
		return LogLevelError
	case "none":
		return LogLevelNone
	default:
		return LogLevelInfo
	}
}

func (c *ProxyConfig) ShouldLog(level int) bool {
	return level <= c.logLevelInt
}

func (c *ProxyConfig) GetAllIps() []string {
	var allIPs []string
	for _, rule := range c.Rules {
		allIPs = append(allIPs, rule.parsedIps...)
	}
	return allIPs
}

func (c *ProxyConfig) GetAllHosts() []string {
	var allHosts []string
	for _, rule := range c.Rules {
		allHosts = append(allHosts, rule.parsedHosts...)
	}
	return allHosts
}

func (c *ProxyConfig) GetAllURLs() []string {
	var allURLs []string
	for _, rule := range c.Rules {
		allURLs = append(allURLs, rule.parsedURLs...)
	}
	return allURLs
}

func (r *RuleConfig) GetParsedIps() []string {
	return r.parsedIps
}

func (r *RuleConfig) GetParsedHosts() []string {
	return r.parsedHosts
}

func (r *RuleConfig) GetParsedURLs() []string {
	return r.parsedURLs
}

func (c *ProxyConfig) GetProxyURL() string {
	if c.DefaultProxy == "" {
		return ""
	}

	proxyURL, exists := c.Proxies[c.DefaultProxy]
	if !exists {

		for _, url := range c.Proxies {
			return url
		}
		return ""
	}
	return proxyURL
}

func (c *ProxyConfig) GetAccessLogPath() string {
	if c.LogFile == "" {
		return ""
	}

	if filepath.IsAbs(c.LogFile) {
		return c.LogFile
	}

	profileDir := getProfilePath()
	return filepath.Join(profileDir, c.LogFile)
}

func (c *ProxyConfig) GetMaxLogSize() int {
	if c.MaxLogSize <= 0 {
		return 10
	}
	return c.MaxLogSize
}

func (c *ProxyConfig) GetMaxLogFiles() int {
	if c.MaxLogFiles <= 0 {
		return 5
	}
	return c.MaxLogFiles
}

func loadExternalRulesRelativeTo(source string, baseDir string) (string, error) {
	return loadExternalRulesRelativeToWithMode(source, baseDir, false)
}

func loadExternalRulesRelativeToWithMode(source string, baseDir string, cacheOnly bool) (string, error) {
	var filePath string
	var err error

	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		filePath, err = downloadAndCacheFileWithMode(source, cacheOnly)
		if err != nil {
			return "", err
		}
	} else {
		filePath = source

		if !filepath.IsAbs(filePath) {
			if baseDir != "" {
				filePath = filepath.Join(baseDir, filePath)
			} else {
				profileDir := getProfilePath()
				filePath = filepath.Join(profileDir, filePath)
			}
		}

		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			return "", fmt.Errorf("local file not found: %s", filePath)
		}
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file %s: %v", filePath, err)
	}

	return string(content), nil
}
