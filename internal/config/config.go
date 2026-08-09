package config

import (
	"fmt"
	"goProxy/internal/cache"
	"goProxy/internal/logging"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

const configSchemaFilename = "goproxy.schema.json"

func defaultConfig() *ProxyConfig {
	return &ProxyConfig{
		DefaultProxy: "direct",
		Proxies: map[string]string{
			"direct": "",
			"block":  "#",
		},
		ListenHttpAddr:  ":8080",
		ListenSocksAddr: ":1080",
		LogLevel:        "info",
		LogFile:         "goProxy.log",
		MaxLogSize:      10,
		MaxLogFiles:     5,
		Rules:           []RuleConfig{},
	}
}

func LoadConfig(configPath string, cacheManager *cache.CacheManager, httpClientFunc HTTPClientFunc, cacheOnly bool, logger *logging.Logger, forceReload bool) (*ProxyConfig, error) {
	config := &ProxyConfig{}

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
		config = defaultConfig()

		if err := saveDefaultConfig(configPath, config); err != nil {
			return nil, fmt.Errorf("error creating default config file: %v", err)
		}
	}

	config.cache = cacheManager
	config.configPath = configPath
	config.logger = logger

	config.afterLoad(httpClientFunc, cacheOnly, forceReload, config.AutoReloadHours*3600)

	return config, nil
}

func (c *ProxyConfig) afterLoad(httpClientFunc HTTPClientFunc, cacheOnly bool, forceReload bool, ttl int) {
	c.logLevelInt = parseLogLevel(c.LogLevel)

	if c.logger == nil {
		c.logger = logging.NewLogger(c)
	} else {
		c.logger.Reconfigure(c)
	}

	configDir := filepath.Dir(c.configPath)

	c.autoDetectInterfaceIPs()

	c.preParseRuleLists(configDir, cacheOnly, httpClientFunc, forceReload, ttl)

	c.cache.PrecompilePatterns(c.getAllHosts(), c.getAllIps())
}

func saveDefaultConfig(configPath string, config *ProxyConfig) error {
	configYAML, err := generateDocumentedYAML(config)
	if err != nil {
		return fmt.Errorf("error generating default config: %v", err)
	}

	configSchemaJSON, err := generateConfigSchema(config)
	if err != nil {
		return fmt.Errorf("error generating config schema: %v", err)
	}

	schemaPath := filepath.Join(filepath.Dir(configPath), configSchemaFilename)
	if err := os.WriteFile(schemaPath, configSchemaJSON, 0600); err != nil {
		return fmt.Errorf("error creating config schema: %v", err)
	}

	if err := os.WriteFile(configPath, configYAML, 0600); err != nil {
		return fmt.Errorf("error creating config file: %v", err)
	}

	return nil
}

func (c *ProxyConfig) GetLogger() *logging.Logger {
	return c.logger
}

func (c *ProxyConfig) GetLogLevelInt() int {
	return c.logLevelInt
}

func (c *ProxyConfig) ReloadConfig(httpClientFunc HTTPClientFunc, forceReload bool) (*ProxyConfig, error) {
	return LoadConfig(c.configPath, c.cache, httpClientFunc, false, c.logger, forceReload)
}
