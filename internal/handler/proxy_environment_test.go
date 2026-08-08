package handler

import (
	"testing"

	"goProxy/internal/cache"
	"goProxy/internal/config"
	"goProxy/internal/logging"
)

type environmentTestLoggerConfig struct{}

func (environmentTestLoggerConfig) GetAccessLogPath() string { return "" }
func (environmentTestLoggerConfig) GetMaxLogSize() int       { return 1 }
func (environmentTestLoggerConfig) GetMaxLogFiles() int      { return 1 }
func (environmentTestLoggerConfig) GetLogLevelInt() int      { return logging.LogLevelNone }

func TestProxyEnvironmentIsIgnored(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("http_proxy", "http://127.0.0.1:1")
	t.Setenv("https_proxy", "http://127.0.0.1:1")

	logger := logging.NewLogger(environmentTestLoggerConfig{})
	t.Cleanup(func() {
		if err := logger.Close(); err != nil {
			t.Errorf("close logger: %v", err)
		}
	})

	cfg := &config.ProxyConfig{
		DefaultProxy: "direct",
		Proxies:      map[string]string{"direct": ""},
	}
	handler := NewProxyHandler(cfg, cache.NewCacheManager(), logger)

	if handler.proxyServer.Tr.Proxy != nil {
		t.Fatal("transport still uses an environment proxy")
	}
	if handler.proxyServer.ConnectDial != nil {
		t.Fatal("CONNECT still uses the goproxy environment dialer")
	}

	client, err := handler.GetHTTPClient("https://rules.example/list.txt")
	if err != nil {
		t.Fatalf("create direct HTTP client: %v", err)
	}
	transport, ok := client.Transport.(*roundTripperWithContext)
	if !ok {
		t.Fatalf("direct client transport type = %T, want *roundTripperWithContext", client.Transport)
	}
	if transport.base.Proxy != nil {
		t.Fatal("direct HTTP client still uses an environment proxy")
	}
}
