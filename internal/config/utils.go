package config

import (
	"fmt"
	"goProxy/internal/logging"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/skratchdot/open-golang/open"
)

func parseLogLevel(level string) int {
	switch strings.ToLower(level) {
	case "debug":
		return logging.LogLevelDebug
	case "info":
		return logging.LogLevelInfo
	case "warn":
		return logging.LogLevelWarn
	case "error":
		return logging.LogLevelError
	case "none":
		return logging.LogLevelNone
	default:
		return logging.LogLevelInfo
	}
}

func (c *ProxyConfig) getAllIps() []string {
	var allIPs []string
	for _, rule := range c.Rules {
		allIPs = append(allIPs, rule.parsedIps...)
	}
	return allIPs
}

func (c *ProxyConfig) getAllHosts() []string {
	var allHosts []string
	for _, rule := range c.Rules {
		allHosts = append(allHosts, rule.parsedHosts...)
	}
	return allHosts
}

func (r *RuleConfig) GetParsedIps() []string {
	return r.parsedIps
}

func (r *RuleConfig) GetParsedHosts() []string {
	return r.parsedHosts
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

func loadExternalRules(source string, baseDir string, cacheOnly bool, httpClientFunc HTTPClientFunc, logger *logging.Logger, forceReload bool, ttl int) (string, error) {
	var filePath string
	var err error

	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		filePath, err = downloadAndCacheFile(source, cacheOnly, httpClientFunc, logger, forceReload, ttl)
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

func OpenConfigDirectory(configPath string, logger *logging.Logger) {
	configDir := filepath.Dir(configPath)

	if !filepath.IsAbs(configDir) {
		absPath, err := filepath.Abs(configDir)
		if err != nil {
			logger.Error("Error getting absolute path for config directory: %v", err)
			return
		}
		configDir = absPath
	}

	if err := os.MkdirAll(configDir, 0700); err != nil {
		logger.Error("Error creating config directory: %v", err)
		return
	}

	if err := open.Run(configDir); err != nil {
		logger.Error("Error opening config directory: %v", err)
	}
}

func (c *ProxyConfig) autoDetectInterfaceIPs() {
	if c.ExternalIf == "" {
		return
	}

	iface, err := net.InterfaceByName(c.ExternalIf)
	if err != nil {
		c.logger.Error("Failed to find interface %s: %v", c.ExternalIf, err)
		return
	}

	addrs, err := iface.Addrs()
	if err != nil {
		c.logger.Error("Failed to get addresses for interface %s: %v", c.ExternalIf, err)
		return
	}

	for _, addr := range addrs {
		var ip net.IP
		switch v := addr.(type) {
		case *net.IPNet:
			ip = v.IP
		case *net.IPAddr:
			ip = v.IP
		}

		if ip == nil || ip.IsLoopback() {
			continue
		}

		if ip.To4() != nil {
			if c.ExternalIp4 == "" {
				c.ExternalIp4 = ip.String()
				c.logger.Debug("Auto-detected IPv4 for %s: %s", c.ExternalIf, c.ExternalIp4)
			}
		} else if ip.To16() != nil && ip.IsGlobalUnicast() {
			if c.ExternalIp6 == "" {
				c.ExternalIp6 = ip.String()
				c.logger.Debug("Auto-detected IPv6 for %s: %s", c.ExternalIf, c.ExternalIp6)
			}
		}
	}
}
