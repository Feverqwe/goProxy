package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

func LoadConfig(configPath string) (*ProxyConfig, error) {
	config := &ProxyConfig{
		DefaultProxy: "direct",
		Proxies: map[string]string{
			"socks5": "socks5://localhost:1080",
			"http":   "http://localhost:8081",
			"direct": "",
			"block":  "#",
		},
		ListenAddr:  ":8080",
		LogLevel:    "info",
		LogFile:     "goProxy.log",
		MaxLogSize:  10,
		MaxLogFiles: 5,
		Rules: []RuleConfig{
			{
				RuleBaseConfig: RuleBaseConfig{
					Name:  "Local Networks",
					Ips:   "192.168.1.0/24 10.0.0.0/8 172.16.0.0/12",
					Hosts: "localhost *.local *.example.com internal.company.com",
					URLs:  "http://internal-api.company.com/v1/* https://*.internal.com/api/*",
				},
				Proxy: "direct",
			},
			{
				RuleBaseConfig: RuleBaseConfig{
					Name:  "Inverted Proxy Rule",
					Not:   true,
					Hosts: "*.google.com *.youtube.com",
				},
				Proxy: "socks5",
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
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := saveDefaultConfig(configPath, config); err != nil {
			return nil, fmt.Errorf("error creating default config file: %v", err)
		}

		config.logLevelInt = parseLogLevel(config.LogLevel)
		return config, nil
	}

	file, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("error opening config file: %v", err)
	}
	defer file.Close()

	if err := yaml.NewDecoder(file).Decode(config); err != nil {
		return nil, fmt.Errorf("error parsing config file: %v", err)
	}

	config.logLevelInt = parseLogLevel(config.LogLevel)

	configDir := filepath.Dir(configPath)
	config.preParseRuleLists(configDir, true, nil)

	return config, nil
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
