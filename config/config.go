package config

import (
	"crypto/sha256"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

type RuleConfig struct {
	Name          string `yaml:"name,omitempty"`
	Proxy         string `yaml:"proxy,omitempty"`
	Ips           string `yaml:"ips,omitempty"`
	Hosts         string `yaml:"hosts,omitempty"`
	URLs          string `yaml:"urls,omitempty"`
	ExternalIps   string `yaml:"externalIps,omitempty"`
	ExternalHosts string `yaml:"externalHosts,omitempty"`
	ExternalURLs  string `yaml:"externalURLs,omitempty"`
	Not           bool   `yaml:"not,omitempty"`

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
				Name:  "Local Networks",
				Proxy: "direct",
				Ips:   "192.168.1.0/24 10.0.0.0/8 172.16.0.0/12",
				Hosts: "localhost *.local *.example.com internal.company.com",
				URLs:  "http://internal-api.company.com/v1/* https://*.internal.com/api/*",
			},
			{
				Name:  "Inverted Proxy Rule",
				Proxy: "socks5",
				Not:   true,
				Hosts: "*.google.com *.youtube.com",
			},
			{
				Name:  "External Domains",
				Proxy: "http",
				Hosts: "*.external.com api.*.com",
			},
			{
				Name:  "Blocked Domains",
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

	// Split input into lines and remove comments from each line
	lines := strings.Split(input, "\n")
	var cleanedLines []string

	for _, line := range lines {
		// Remove everything after // or # comment markers
		if idx := strings.Index(line, "//"); idx != -1 {
			line = line[:idx]
		}
		if idx := strings.Index(line, "#"); idx != -1 {
			line = line[:idx]
		}
		cleanedLines = append(cleanedLines, strings.TrimSpace(line))
	}

	// Join the cleaned lines and process as before
	cleanedInput := strings.Join(cleanedLines, " ")
	normalized := strings.ReplaceAll(cleanedInput, ",", " ")
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

		type loadTask struct {
			sources         []string
			expandWildcards bool
			result          *[]string
		}

		tasks := []loadTask{
			{ParseStringToList(rule.ExternalIps, false), false, &rule.parsedIps},
			{ParseStringToList(rule.ExternalHosts, false), true, &rule.parsedHosts},
			{ParseStringToList(rule.ExternalURLs, false), false, &rule.parsedURLs},
		}

		var wg sync.WaitGroup
		var mu sync.Mutex

		for _, task := range tasks {
			for _, source := range task.sources {
				if source == "" {
					continue
				}

				wg.Add(1)
				go func(source string, expandWildcards bool, result *[]string) {
					defer wg.Done()
					rules := c.loadExternalRuleList(source, expandWildcards)
					mu.Lock()
					*result = append(*result, rules...)
					mu.Unlock()
				}(source, task.expandWildcards, task.result)
			}
		}

		wg.Wait()
	}
}

func (c *ProxyConfig) loadExternalRuleList(url string, expandWildcardDomains bool) []string {
	if url == "" {
		return []string{}
	}

	rulesContent, err := loadExternalRules(url)
	if err != nil {
		fmt.Printf("Warning: Failed to load external rules from %s: %v\n", url, err)
		return []string{}
	}

	return ParseStringToList(rulesContent, expandWildcardDomains)
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

func getCacheDir() string {
	profileDir := getProfilePath()
	cacheDir := filepath.Join(profileDir, "cache")

	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return filepath.Join(profileDir, "cache")
	}

	return cacheDir
}

func getCacheFilePath(url string) string {
	baseName := filepath.Base(url)
	if baseName == "" || baseName == "." || baseName == "/" {
		baseName = "rules"
	}

	hash := sha256.Sum256([]byte(url))
	filename := fmt.Sprintf("%s_%x.txt", baseName, hash[:8])

	return filepath.Join(getCacheDir(), filename)
}

func downloadAndCacheFile(url string) (string, error) {
	cacheFile := getCacheFilePath(url)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		if _, cacheErr := os.Stat(cacheFile); cacheErr == nil {
			return cacheFile, nil
		}
		return "", fmt.Errorf("failed to download %s: %v", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		if _, cacheErr := os.Stat(cacheFile); cacheErr == nil {
			return cacheFile, nil
		}
		return "", fmt.Errorf("failed to download %s: status %d", url, resp.StatusCode)
	}

	file, err := os.Create(cacheFile)
	if err != nil {
		return "", fmt.Errorf("failed to create cache file: %v", err)
	}
	defer file.Close()

	_, err = io.Copy(file, resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to write cache file: %v", err)
	}

	return cacheFile, nil
}

func loadExternalRules(source string) (string, error) {
	var filePath string
	var err error

	if strings.HasPrefix(source, "http://") || strings.HasPrefix(source, "https://") {
		filePath, err = downloadAndCacheFile(source)
		if err != nil {
			return "", err
		}
	} else {
		filePath = source

		if !filepath.IsAbs(filePath) {
			profileDir := getProfilePath()
			filePath = filepath.Join(profileDir, filePath)
		}

		if _, err := os.Stat(filePath); os.IsNotExist(err) {
			return "", fmt.Errorf("local file not found: %s", filePath)
		}
	}

	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file %s: %v", filePath, err)
	}

	// Return the raw content - ParseStringToList will handle comment filtering
	return string(content), nil
}
