package handler

import (
	"os"
	"path/filepath"
	"testing"

	"goProxy/internal/cache"
	"goProxy/internal/config"
)

func TestProxyDecisionNormalizesDNSCaseAndTrailingDot(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	contents := []byte(`defaultProxy: direct
proxies:
  direct: ""
  block: "#"
listenHttpAddr: ""
listenSocksAddr: ""
logLevel: none
logFile: ""
maxLogSize: 1
maxLogFiles: 1
rules:
  - name: blocked
    proxy: block
    hosts: "*.Example.COM."
`)
	if err := os.WriteFile(configPath, contents, 0600); err != nil {
		t.Fatal(err)
	}
	cacheManager := cache.NewCacheManager()
	cfg, err := config.LoadConfig(configPath, cacheManager, nil, true, nil, false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cfg.GetLogger().Close() })

	decision := NewProxyDecision(cfg, cacheManager, cfg.GetLogger())
	for _, host := range []string{"EXAMPLE.COM", "WWW.EXAMPLE.COM", "www.example.com."} {
		proxyURL, result, err := decision.GetProxyForHost(host)
		if err != nil {
			t.Fatalf("GetProxyForHost(%q): %v", host, err)
		}
		if proxyURL != "#" || result.RuleName != "blocked" {
			t.Errorf("GetProxyForHost(%q) = (%q, %q), want blocked route", host, proxyURL, result.RuleName)
		}
	}
}
