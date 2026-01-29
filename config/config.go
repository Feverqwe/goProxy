package config

import (
	"fmt"
	"net/url"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// RuleConfig represents a single rule with bypass patterns and proxy selection
type RuleConfig struct {
	Proxy string `yaml:"proxy,omitempty"`
	Ips   string `yaml:"ips,omitempty"`
	Hosts string `yaml:"hosts,omitempty"`
	URLs  string `yaml:"urls,omitempty"`
	Not   bool   `yaml:"not,omitempty"`
}

type ProxyConfig struct {
	DefaultProxy string            `yaml:"default_proxy"` // Default proxy key to use
	Proxies      map[string]string `yaml:"proxies"`       // Map of proxy keys to proxy URLs
	ListenAddr   string            `yaml:"listen_addr"`
	LogLevel     string            `yaml:"log_level"`
	Rules        []RuleConfig      `yaml:"rules"`
	IPCacheSize  int               `yaml:"ip_cache_size"`
}

// ShouldLog проверяет, нужно ли логировать сообщение в зависимости от уровня
func (c *ProxyConfig) ShouldLog(level string) bool {
	levels := map[string]int{
		"debug": 4,
		"info":  3,
		"warn":  2,
		"error": 1,
		"none":  0,
	}

	currentLevel, ok := levels[strings.ToLower(c.LogLevel)]
	if !ok {
		currentLevel = levels["info"] // По умолчанию info
	}

	messageLevel, ok := levels[strings.ToLower(level)]
	if !ok {
		messageLevel = levels["info"] // По умолчанию info
	}

	return messageLevel <= currentLevel
}

// LoadConfig загружает конфигурацию из файла
func LoadConfig(configPath string) (*ProxyConfig, error) {
	config := &ProxyConfig{
		DefaultProxy: "direct",
		Proxies: map[string]string{
			"socks5": "socks5://localhost:1080",
			"http":   "http://localhost:8081",
			"direct": "",
			"block":  "#",
		},
		ListenAddr: ":8080",
		LogLevel:   "info",
		Rules: []RuleConfig{
			{
				Proxy: "direct",
				Ips:   "192.168.1.0/24 10.0.0.0/8 172.16.0.0/12",
				Hosts: "localhost *.local *.example.com internal.company.com",
				URLs:  "http://internal-api.company.com/v1/* https://*.internal.com/api/*",
			},
			{
				Proxy: "socks5",
				Not:   true,
				Hosts: "*.google.com *.youtube.com",
			},
			{
				Proxy: "http",
				Hosts: "*.external.com api.*.com",
			},
			{
				Proxy: "block",
				Hosts: "*.malicious.com *.spam.com",
			},
		},
		IPCacheSize: 1000,
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Check if old JSON config exists and migrate it
		jsonConfigPath := strings.TrimSuffix(configPath, ".yaml") + ".json"
		if _, err := os.Stat(jsonConfigPath); err == nil {
			// Migrate from JSON to YAML by renaming
			if err := os.Rename(jsonConfigPath, configPath); err != nil {
				return nil, fmt.Errorf("error migrating config from JSON to YAML: %v", err)
			}
		} else {
			// Create default config file
			if err := saveDefaultConfig(configPath, config); err != nil {
				return nil, fmt.Errorf("error creating default config file: %v", err)
			}
			return config, nil
		}
	}

	file, err := os.Open(configPath)
	if err != nil {
		return nil, fmt.Errorf("error opening config file: %v", err)
	}
	defer file.Close()

	if err := yaml.NewDecoder(file).Decode(config); err != nil {
		return nil, fmt.Errorf("error parsing config file: %v", err)
	}

	return config, nil
}

// saveDefaultConfig сохраняет конфигурацию по умолчанию в файл
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

// ParseStringToList parses a string with comma or space separated values into a slice
func ParseStringToList(input string) []string {
	if input == "" {
		return []string{}
	}

	// Replace commas with spaces and split by spaces
	normalized := strings.ReplaceAll(input, ",", " ")
	parts := strings.Fields(normalized)

	// Filter out empty strings
	var result []string
	for _, part := range parts {
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}

// GetAllIps возвращает все IP-диапазоны обхода из всех правил
func (c *ProxyConfig) GetAllIps() []string {
	var allIPs []string
	for _, rule := range c.Rules {
		allIPs = append(allIPs, ParseStringToList(rule.Ips)...)
	}
	return allIPs
}

// GetAllHosts возвращает все домены обхода из всех правил
func (c *ProxyConfig) GetAllHosts() []string {
	var allHosts []string
	for _, rule := range c.Rules {
		allHosts = append(allHosts, ParseStringToList(rule.Hosts)...)
	}
	return allHosts
}

// GetAllURLs возвращает все URL паттерны обхода из всех правил
func (c *ProxyConfig) GetAllURLs() []string {
	var allURLs []string
	for _, rule := range c.Rules {
		allURLs = append(allURLs, ParseStringToList(rule.URLs)...)
	}
	return allURLs
}

// GetProxyURL возвращает URL прокси на основе выбранного ключа
func (c *ProxyConfig) GetProxyURL() string {
	if c.DefaultProxy == "" {
		return ""
	}

	proxyURL, exists := c.Proxies[c.DefaultProxy]
	if !exists {
		// Если указанный ключ не существует, используем первый доступный прокси
		for _, url := range c.Proxies {
			return url
		}
		return ""
	}
	return proxyURL
}

// GetProxyType определяет тип прокси на основе URL
func (c *ProxyConfig) GetProxyType() string {
	proxyURL := c.GetProxyURL()
	if proxyURL == "" {
		return "direct"
	}

	// Проверяем, содержит ли строка схему прокси
	if strings.Contains(proxyURL, "://") {
		parsed, err := url.Parse(proxyURL)
		if err == nil {
			return parsed.Scheme
		}
	}

	// Если нет схемы, возвращаем пустую строку - тип должен быть указан явно
	return ""
}

// GetProxyAddress возвращает адрес прокси без схемы
func (c *ProxyConfig) GetProxyAddress() string {
	proxyURL := c.GetProxyURL()
	if proxyURL == "" {
		return ""
	}

	// Если есть схема, извлекаем только хост:порт
	if strings.Contains(proxyURL, "://") {
		parsed, err := url.Parse(proxyURL)
		if err == nil {
			return parsed.Host
		}
	}

	// Если нет схемы, возвращаем как есть
	return proxyURL
}
