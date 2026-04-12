package config

import (
	"goProxy/internal/cache"
	"goProxy/internal/logging"
	"net/http"
)

type HTTPClientFunc func(string) (*http.Client, error)

type Logger interface {
	Debug(format string, v ...interface{})
	Info(format string, v ...interface{})
	Warn(format string, v ...interface{})
	Error(format string, v ...interface{})
	Printf(msg string, v ...interface{})
	Close() error
}

type RuleBaseConfig struct {
	Name         string `yaml:"name,omitempty"`
	Ips          string `yaml:"ips,omitempty"`
	Hosts        string `yaml:"hosts,omitempty"`
	ExternalRule string `yaml:"externalRule,omitempty"`

	parsedIps   []string
	parsedHosts []string
}

type RuleConfig struct {
	RuleBaseConfig `yaml:",inline"`
	Proxy          string `yaml:"proxy"`
}

type ProxyConfig struct {
	DefaultProxy    string            `yaml:"defaultProxy"`
	Proxies         map[string]string `yaml:"proxies"`
	ListenHttpAddr  string            `yaml:"listenHttpAddr"`
	ListenSocksAddr string            `yaml:"listenSocksAddr"`
	LogLevel        string            `yaml:"logLevel"`
	LogFile         string            `yaml:"logFile"`
	MaxLogSize      int               `yaml:"maxLogSize"`
	MaxLogFiles     int               `yaml:"maxLogFiles"`
	AutoReloadHours int               `yaml:"autoReloadHours"`
	Rules           []RuleConfig      `yaml:"rules"`
	ExternalIp4     string            `yaml:"externalIp4"`
	ExternalIp6     string            `yaml:"externalIp6"`
	ExternalIf      string            `yaml:"externalIf"`
	ExternalDns     string            `yaml:"externalDns"`

	logLevelInt int
	cache       *cache.CacheManager
	configPath  string
	logger      *logging.Logger
}
