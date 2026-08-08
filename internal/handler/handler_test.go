package handler

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"goProxy/internal/cache"
	"goProxy/internal/config"
	"goProxy/internal/logging"
)

type testLoggerConfig struct{}

func (testLoggerConfig) GetAccessLogPath() string { return "" }
func (testLoggerConfig) GetMaxLogSize() int       { return 1 }
func (testLoggerConfig) GetMaxLogFiles() int      { return 1 }
func (testLoggerConfig) GetLogLevelInt() int      { return logging.LogLevelNone }

func newTestProxyHandler(t *testing.T, cfg *config.ProxyConfig) *ProxyHandler {
	t.Helper()

	logger := logging.NewLogger(testLoggerConfig{})
	t.Cleanup(func() {
		if err := logger.Close(); err != nil {
			t.Errorf("close logger: %v", err)
		}
	})

	return NewProxyHandler(cfg, cache.NewCacheManager(), logger)
}

func TestHTTPSUpstreamUsesTLS(t *testing.T) {
	requestTarget := make(chan string, 1)
	proxyServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			t.Errorf("method = %s, want CONNECT", r.Method)
		}
		requestTarget <- r.Host
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusOK)
	}))
	defer proxyServer.Close()

	cfg := &config.ProxyConfig{
		DefaultProxy: "direct",
		Proxies:      map[string]string{"direct": ""},
	}
	handler := newTestProxyHandler(t, cfg)

	rootCAs := x509.NewCertPool()
	rootCAs.AddCert(proxyServer.Certificate())
	handler.proxyTLSConfig = &tls.Config{RootCAs: rootCAs}

	ctx := context.WithValue(context.Background(), proxyURLContextKey, proxyServer.URL)
	conn, err := handler.dialContext(ctx, "tcp", "target.example:443")
	if err != nil {
		t.Fatalf("dial through HTTPS proxy: %v", err)
	}
	defer conn.Close()

	buffered, ok := conn.(*bufferedConn)
	if !ok {
		t.Fatalf("connection type = %T, want *bufferedConn", conn)
	}
	if _, ok := buffered.Conn.(*tls.Conn); !ok {
		t.Fatalf("underlying connection type = %T, want *tls.Conn", buffered.Conn)
	}

	select {
	case target := <-requestTarget:
		if target != "target.example:443" {
			t.Fatalf("CONNECT target = %q, want target.example:443", target)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTPS proxy did not receive CONNECT")
	}
}

func TestProxyEnvironmentIsIgnored(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("http_proxy", "http://127.0.0.1:1")
	t.Setenv("https_proxy", "http://127.0.0.1:1")

	cfg := &config.ProxyConfig{
		DefaultProxy: "direct",
		Proxies:      map[string]string{"direct": ""},
	}
	handler := newTestProxyHandler(t, cfg)

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

func TestDomainNormalizationInRules(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	configYAML := []byte(`
defaultProxy: direct
proxies:
  direct: ""
  block: "#"
logLevel: none
logFile: ""
rules:
  - name: normalized
    proxy: block
    hosts: "*.EXAMPLE.COM."
`)
	if err := os.WriteFile(configPath, configYAML, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cacheManager := cache.NewCacheManager()
	cfg, err := config.LoadConfig(configPath, cacheManager, nil, true, nil, false)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	t.Cleanup(func() {
		if err := cfg.GetLogger().Close(); err != nil {
			t.Errorf("close logger: %v", err)
		}
	})

	decision := NewProxyDecision(cfg, cacheManager, cfg.GetLogger())
	proxyURL, result, err := decision.GetProxyForHost("API.Example.Com.")
	if err != nil {
		t.Fatalf("get proxy decision: %v", err)
	}
	if proxyURL != "#" || result.RuleName != "normalized" {
		t.Fatalf("decision = (%q, %q), want blocked by normalized rule", proxyURL, result.RuleName)
	}
}

func TestGetTargetAndSourceIPUsesCompatibleFamily(t *testing.T) {
	ips := []net.IP{
		net.ParseIP("2001:db8::10"),
		net.ParseIP("192.0.2.10"),
	}

	target, source := getTargetAndSourceIp(ips, "192.0.2.20", "")
	if !target.Equal(net.ParseIP("192.0.2.10")) || source != "192.0.2.20" {
		t.Fatalf("IPv4 selection = (%v, %q), want (192.0.2.10, 192.0.2.20)", target, source)
	}

	target, source = getTargetAndSourceIp(ips, "", "2001:db8::20")
	if !target.Equal(net.ParseIP("2001:db8::10")) || source != "2001:db8::20" {
		t.Fatalf("IPv6 selection = (%v, %q), want (2001:db8::10, 2001:db8::20)", target, source)
	}

	target, source = getTargetAndSourceIp(ips, "", "")
	if !target.Equal(ips[0]) || source != "" {
		t.Fatalf("unbound selection = (%v, %q), want first address without source", target, source)
	}
}

func TestProxyAddressDefaults(t *testing.T) {
	tests := map[string]string{
		"http://proxy.example":       "proxy.example:80",
		"https://proxy.example":      "proxy.example:443",
		"https://[2001:db8::1]:8443": "[2001:db8::1]:8443",
	}

	for rawURL, want := range tests {
		t.Run(rawURL, func(t *testing.T) {
			parsed, err := url.Parse(rawURL)
			if err != nil {
				t.Fatalf("parse URL: %v", err)
			}
			got, err := getProxyAddress(parsed)
			if err != nil {
				t.Fatalf("get proxy address: %v", err)
			}
			if got != want {
				t.Fatalf("address = %q, want %q", got, want)
			}
		})
	}
}

func TestUDPSessionActivityIsConcurrentSafe(t *testing.T) {
	session := &UDPSession{}
	session.touch()

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 1_000 {
				session.touch()
				_ = session.expired(time.Now(), time.Minute)
			}
		}()
	}
	wg.Wait()

	if session.expired(time.Now(), time.Minute) {
		t.Fatal("freshly touched session is expired")
	}
	session.lastActive.Store(time.Now().Add(-2 * time.Minute).UnixNano())
	if !session.expired(time.Now(), time.Minute) {
		t.Fatal("old session is not expired")
	}
}
