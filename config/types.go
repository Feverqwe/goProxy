package config

type RuleBaseConfig struct {
	Name          string `yaml:"name,omitempty"`
	Ips           string `yaml:"ips,omitempty"`
	Hosts         string `yaml:"hosts,omitempty"`
	URLs          string `yaml:"urls,omitempty"`
	ExternalIps   string `yaml:"externalIps,omitempty"`
	ExternalHosts string `yaml:"externalHosts,omitempty"`
	ExternalURLs  string `yaml:"externalURLs,omitempty"`
	ExternalRule  string `yaml:"externalRule,omitempty"`
	Not           bool   `yaml:"not,omitempty"`

	parsedIps   []string
	parsedHosts []string
	parsedURLs  []string
}

type RuleConfig struct {
	RuleBaseConfig `yaml:",inline"`
	Proxy          string `yaml:"proxy,omitempty"`
}

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

const (
	LogLevelDebug = 4
	LogLevelInfo  = 3
	LogLevelWarn  = 2
	LogLevelError = 1
	LogLevelNone  = 0
)
