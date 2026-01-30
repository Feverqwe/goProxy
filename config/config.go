package config

import (
	"fmt"
	"os"
	"path/filepath"
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

	// Pre-parsed lists for performance optimization
	parsedIps   []string
	parsedHosts []string
	parsedURLs  []string
}

// LogLevel constants
const (
	LogLevelDebug = 4
	LogLevelInfo  = 3
	LogLevelWarn  = 2
	LogLevelError = 1
	LogLevelNone  = 0
)

type ProxyConfig struct {
	DefaultProxy string            `yaml:"defaultProxy"` // Default proxy key to use
	Proxies      map[string]string `yaml:"proxies"`      // Map of proxy keys to proxy URLs
	ListenAddr   string            `yaml:"listenAddr"`
	LogLevel     string            `yaml:"logLevel"`
	logLevelInt  int               // Pre-processed log level for fast comparisons
	LogFile      string            `yaml:"logFile,omitempty"`     // Path to log file (optional)
	MaxLogSize   int               `yaml:"maxLogSize,omitempty"`  // Max log file size in MB (optional)
	MaxLogFiles  int               `yaml:"maxLogFiles,omitempty"` // Max number of log files to keep (optional)
	Rules        []RuleConfig      `yaml:"rules"`
}

// parseLogLevel converts log level string to integer constant
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
		return LogLevelInfo // Default to info
	}
}

// ShouldLog checks if a message should be logged based on the log level
func (c *ProxyConfig) ShouldLog(level int) bool {
	return level <= c.logLevelInt
}

// LoadConfig loads configuration from file
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
		LogFile:     "goProxy.log", // Default log file
		MaxLogSize:  10,            // 10 MB default max size
		MaxLogFiles: 5,             // Keep 5 log files by default
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
	}

	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		// Create default config file
		if err := saveDefaultConfig(configPath, config); err != nil {
			return nil, fmt.Errorf("error creating default config file: %v", err)
		}
		// Pre-parse log level for default config
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

	// Pre-parse log level for fast comparisons
	config.logLevelInt = parseLogLevel(config.LogLevel)

	// Pre-parse all rule strings for performance optimization
	config.preParseRuleLists()

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
// If expandWildcardDomains is true, patterns starting with `*.` will be expanded to include the base domain
func ParseStringToList(input string, expandWildcardDomains bool) []string {
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

			// Если указано расширение wildcard доменов и паттерн начинается с `*.`, добавляем также базовый домен
			if expandWildcardDomains && strings.HasPrefix(part, "*.") {
				baseDomain := strings.TrimPrefix(part, "*.")
				result = append(result, baseDomain)
			}
		}
	}
	return result
}

// preParseRuleLists pre-parses all rule strings for performance optimization
func (c *ProxyConfig) preParseRuleLists() {
	for i := range c.Rules {
		rule := &c.Rules[i]
		rule.parsedIps = ParseStringToList(rule.Ips, false)
		rule.parsedHosts = ParseStringToList(rule.Hosts, true)
		rule.parsedURLs = ParseStringToList(rule.URLs, false)
	}
}

// GetAllIps возвращает все IP-диапазоны обхода из всех правил
func (c *ProxyConfig) GetAllIps() []string {
	var allIPs []string
	for _, rule := range c.Rules {
		allIPs = append(allIPs, rule.parsedIps...)
	}
	return allIPs
}

// GetAllHosts возвращает все домены обхода из всех правил
func (c *ProxyConfig) GetAllHosts() []string {
	var allHosts []string
	for _, rule := range c.Rules {
		allHosts = append(allHosts, rule.parsedHosts...)
	}
	return allHosts
}

// GetAllURLs возвращает все URL паттерны обхода из всех правил
func (c *ProxyConfig) GetAllURLs() []string {
	var allURLs []string
	for _, rule := range c.Rules {
		allURLs = append(allURLs, rule.parsedURLs...)
	}
	return allURLs
}

// GetParsedIps returns pre-parsed IPs for a specific rule
func (r *RuleConfig) GetParsedIps() []string {
	return r.parsedIps
}

// GetParsedHosts returns pre-parsed hosts for a specific rule
func (r *RuleConfig) GetParsedHosts() []string {
	return r.parsedHosts
}

// GetParsedURLs returns pre-parsed URLs for a specific rule
func (r *RuleConfig) GetParsedURLs() []string {
	return r.parsedURLs
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

// GetAccessLogPath returns the log file path relative to the profile directory
func (c *ProxyConfig) GetAccessLogPath() string {
	if c.LogFile == "" {
		return ""
	}

	// If the path is already absolute, return it as is
	if filepath.IsAbs(c.LogFile) {
		return c.LogFile
	}

	// Otherwise, make it relative to the profile directory
	profileDir := getProfilePath()
	return filepath.Join(profileDir, c.LogFile)
}

// GetMaxLogSize returns the maximum log file size in MB
func (c *ProxyConfig) GetMaxLogSize() int {
	if c.MaxLogSize <= 0 {
		return 10 // Default to 10 MB
	}
	return c.MaxLogSize
}

// GetMaxLogFiles returns the maximum number of log files to keep
func (c *ProxyConfig) GetMaxLogFiles() int {
	if c.MaxLogFiles <= 0 {
		return 5 // Default to 5 files
	}
	return c.MaxLogFiles
}
