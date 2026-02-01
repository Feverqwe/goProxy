package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

type RuleConfig struct {
	Proxy string `yaml:"proxy,omitempty"`
	Ips   string `yaml:"ips,omitempty"`
	Hosts string `yaml:"hosts,omitempty"`
	URLs  string `yaml:"urls,omitempty"`
	Not   bool   `yaml:"not,omitempty"`

	parsedIps   []string
	parsedHosts []string
	parsedURLs  []string
}

const (
	LogLevelDebug = 4
	LogLevelInfo  = 3
	LogLevelWarn  = 2
	LogLevelError = 1
	LogLevelNone  = 0
)

type ProxyConfig struct {
	DefaultProxy string            `yaml:"defaultProxy"`
	Proxies      map[string]string `yaml:"proxies"`
	ListenAddr   string            `yaml:"listenAddr"`
	LogLevel     string            `yaml:"logLevel"`
	logLevelInt  int
	LogFile      string       `yaml:"logFile,omitempty"`
	MaxLogSize   int          `yaml:"maxLogSize,omitempty"`
	MaxLogFiles  int          `yaml:"maxLogFiles,omitempty"`
	Rules        []RuleConfig `yaml:"rules"`
}

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

	config.preParseRuleLists()

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

func ParseStringToList(input string, expandWildcardDomains bool) []string {
	if input == "" {
		return []string{}
	}

	normalized := strings.ReplaceAll(input, ",", " ")
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

func (c *ProxyConfig) preParseRuleLists() {
	for i := range c.Rules {
		rule := &c.Rules[i]
		rule.parsedIps = ParseStringToList(rule.Ips, false)
		rule.parsedHosts = ParseStringToList(rule.Hosts, true)
		rule.parsedURLs = ParseStringToList(rule.URLs, false)
	}
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
