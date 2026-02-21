package config

import (
	"fmt"
	"goProxy/cache"
	"goProxy/logging"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func LoadConfig(configPath string, cacheManager *cache.CacheManager, httpClientFunc HTTPClientFunc, cacheOnly bool, logger *logging.Logger) (*ProxyConfig, error) {
	config := &ProxyConfig{
		DefaultProxy: "direct",
		Proxies: map[string]string{
			"socks5": "socks5://localhost:1080",
			"http":   "http://localhost:8081",
			"direct": "",
			"block":  "#",
		},
		ListenHttpAddr:  ":8080",
		ListenSocksAddr: ":1080",
		LogLevel:        "info",
		LogFile:         "goProxy.log",
		MaxLogSize:      10,
		MaxLogFiles:     5,
		Rules: []RuleConfig{
			{
				RuleBaseConfig: RuleBaseConfig{
					Name:  "Local Networks",
					Ips:   "192.168.1.0/24 10.0.0.0/8 172.16.0.0/12",
					Hosts: "localhost *.local *.example.com internal.company.com",
				},
				Proxy: "direct",
			},
			{
				RuleBaseConfig: RuleBaseConfig{
					Name:  "Inverted Proxy Rule",
					Hosts: "*.google.com *.youtube.com",
				},
				Proxy: "socks5",
				Not:   true,
			},
			{
				RuleBaseConfig: RuleBaseConfig{
					Name:  "External Domains",
					Hosts: "*.external.com api.*.com",
				},
				Proxy: "http",
			},
			{
				RuleBaseConfig: RuleBaseConfig{
					Name:  "Blocked Domains",
					Hosts: "*.malicious.com *.spam.com",
				},
				Proxy: "block",
			},
		},
		cache:      cacheManager,
		configPath: configPath,
		logger:     logger,
	}

	if _, err := os.Stat(configPath); err == nil {
		file, err := os.Open(configPath)
		if err != nil {
			return nil, fmt.Errorf("error opening config file: %v", err)
		}
		defer file.Close()

		if err := yaml.NewDecoder(file).Decode(config); err != nil {
			return nil, fmt.Errorf("error parsing config file: %v", err)
		}
	} else {
		if err := saveDefaultConfig(configPath, config); err != nil {
			return nil, fmt.Errorf("error creating default config file: %v", err)
		}
	}

	config.afterLoad(httpClientFunc, cacheOnly)

	return config, nil
}

func (c *ProxyConfig) afterLoad(httpClientFunc HTTPClientFunc, cacheOnly bool) {
	c.logLevelInt = parseLogLevel(c.LogLevel)

	if c.logger == nil {
		c.logger = logging.NewLogger(c)
	} else {
		c.logger.Reconfigure(c)
	}

	configDir := filepath.Dir(c.configPath)

	c.autoDetectInterfaceIPs()

	c.preParseRuleLists(configDir, cacheOnly, httpClientFunc)

	c.cache.PrecompilePatterns(c.GetAllHosts(), c.GetAllIps())
}

func saveDefaultConfig(configPath string, config *ProxyConfig) error {
	file, err := os.Create(configPath)
	if err != nil {
		return fmt.Errorf("error creating config file: %v", err)
	}
	defer file.Close()

	encoder := yaml.NewEncoder(file)
	encoder.SetIndent(2)
	if err := encoder.Encode(config); err != nil {
		return fmt.Errorf("error encoding config to YAML: %v", err)
	}

	return nil
}

func (c *ProxyConfig) GetLogger() *logging.Logger {
	return c.logger
}

func (c *ProxyConfig) GetLogLevelInt() int {
	return c.logLevelInt
}

func (c *ProxyConfig) ReloadConfig(httpClientFunc HTTPClientFunc) (*ProxyConfig, error) {
	return LoadConfig(c.configPath, c.cache, httpClientFunc, false, c.logger)
}
