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
	Name         string `yaml:"name,omitempty" config_description:"Optional descriptive name used in logs."`
	Ips          string `yaml:"ips,omitempty" config_description:"Whitespace- or comma-separated IPs, CIDRs, hostnames, local paths, or HTTP(S) list URLs. Prefix an entry with ! to exclude it."`
	Hosts        string `yaml:"hosts,omitempty" config_description:"Whitespace- or comma-separated host globs, local paths, or HTTP(S) list URLs. Prefix an entry with ! to exclude it."`
	ExternalRule string `yaml:"externalRule,omitempty" config_description:"Path or URL of a YAML file containing name, ips, and/or hosts."`

	parsedIps   []string
	parsedHosts []string
	hostIndex   ruleHostIndex
}

type RuleConfig struct {
	RuleBaseConfig `yaml:",inline"`
	Proxy          string `yaml:"proxy" config_required:"true" config_description:"Name of the proxy route to use when this rule matches."`
}

type ProxyConfig struct {
	DefaultProxy     string            `yaml:"defaultProxy" config_required:"true" config_description:"Route used when no rule matches. The name must exist in proxies."`
	Proxies          map[string]string `yaml:"proxies" config_required:"true" config_description:"Named routes. An empty URL means a direct connection and # blocks the connection."`
	ListenHttpAddr   string            `yaml:"listenHttpAddr" config_description:"HTTP/HTTPS proxy listen address. An empty string disables the listener."`
	ListenSocksAddr  string            `yaml:"listenSocksAddr" config_description:"SOCKS5 proxy listen address. An empty string disables the listener."`
	LogLevel         string            `yaml:"logLevel" config_description:"Minimum logging level." config_enum:"debug,info,warn,error,none"`
	LogFile          string            `yaml:"logFile" config_description:"Log path, relative to the profile directory unless absolute. An empty string disables file logging."`
	MaxLogSize       int               `yaml:"maxLogSize" config_description:"Maximum log file size in megabytes before rotation." config_minimum:"1"`
	MaxLogFiles      int               `yaml:"maxLogFiles" config_description:"Number of rotated log files to retain." config_minimum:"1"`
	ReportTopDomains int               `yaml:"reportTopDomains" config_description:"Number of most-used domains included in usage reports by default." config_minimum:"1"`
	AutoReloadHours  int               `yaml:"autoReloadHours" config_description:"Remote rule reload interval in hours. Zero disables periodic reloads." config_minimum:"0"`
	Rules            []RuleConfig      `yaml:"rules" config_description:"Routing rules evaluated from top to bottom. Each rule needs proxy and at least one of hosts, ips, or externalRule." config_item_any_of:"ips,hosts,externalRule"`
	ExternalIp4      string            `yaml:"externalIp4" config_description:"Optional IPv4 source address for direct connections."`
	ExternalIp6      string            `yaml:"externalIp6" config_description:"Optional IPv6 source address for direct connections."`
	ExternalIf       string            `yaml:"externalIf" config_description:"Network interface from which source IP addresses are detected automatically."`
	ExternalDns      string            `yaml:"externalDns" config_description:"Optional DNS server for direct connections. Port 53 is used when omitted." config_examples:"8.8.8.8|1.1.1.1:53|[2606:4700:4700::1111]:53"`

	logLevelInt int
	cache       *cache.CacheManager
	configPath  string
	logger      *logging.Logger
}
