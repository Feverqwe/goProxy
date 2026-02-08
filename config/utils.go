package config

import (
	"fmt"
	"goProxy/logger"
	"os"
	"path/filepath"
	"strings"
)

func parseLogLevel(level string) int {
	switch strings.ToLower(level) {
	case "debug":
		return logger.LogLevelDebug
	case "info":
		return logger.LogLevelInfo
	case "warn":
		return logger.LogLevelWarn
	case "error":
		return logger.LogLevelError
	case "none":
		return logger.LogLevelNone
	default:
		return logger.LogLevelInfo
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

func (c *ProxyConfig) GetProxyServerURL() string {
	if c.ListenAddr == "" {
		return "http://localhost:8080"
	}

	if c.ListenAddr[0] == ':' {
		return "http://localhost" + c.ListenAddr
	}

	return "http://" + c.ListenAddr
}

func loadExternalRules(source string, baseDir string, cacheOnly bool, httpClientFunc HTTPClientFunc) (string, error) {
	var filePath string
	var err error

	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		filePath, err = downloadAndCacheFile(source, cacheOnly, httpClientFunc)
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
