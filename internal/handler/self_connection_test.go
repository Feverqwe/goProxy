package handler

import (
	"context"
	"strings"
	"testing"

	"goProxy/internal/cache"
	"goProxy/internal/config"
)

func TestIsSelfConnectionWithWildcardListeners(t *testing.T) {
	cfg := &config.ProxyConfig{
		ListenHttpAddr:  ":8080",
		ListenSocksAddr: ":1080",
	}
	guard := newSelfConnectionGuard(cfg, cache.NewCacheManager())

	tests := []struct {
		name string
		addr string
		want bool
	}{
		{name: "HTTP loopback IPv4", addr: "127.0.0.1:8080", want: true},
		{name: "HTTP loopback IPv6", addr: "[::1]:8080", want: true},
		{name: "SOCKS loopback", addr: "127.0.0.1:1080", want: true},
		{name: "equivalent numeric port", addr: "127.0.0.1:08080", want: true},
		{name: "different port", addr: "127.0.0.1:8081", want: false},
		{name: "non-local address", addr: "192.0.2.1:8080", want: false},
		{name: "localhost", addr: "localhost:8080", want: true},
		{name: "missing port", addr: "127.0.0.1", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := guard.isSelfConnection(tt.addr); got != tt.want {
				t.Fatalf("isSelfConnection(%q) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}

func TestIsSelfConnectionWithSpecificListener(t *testing.T) {
	cfg := &config.ProxyConfig{ListenHttpAddr: "localhost:8080"}
	guard := newSelfConnectionGuard(cfg, cache.NewCacheManager())

	if !guard.isSelfConnection("127.0.0.1:8080") {
		t.Fatal("localhost should match a loopback listener")
	}
	if guard.isSelfConnection("127.0.0.1:8081") {
		t.Fatal("an endpoint on a different port must not match")
	}
	if guard.isSelfConnection("192.0.2.1:8080") {
		t.Fatal("an endpoint on a different address must not match")
	}
}

func TestDialContextRejectsDirectSelfConnection(t *testing.T) {
	cfg := &config.ProxyConfig{ListenHttpAddr: ":8080"}
	handler := &ProxyHandler{
		decision: &ProxyDecision{
			config:    cfg,
			selfGuard: newSelfConnectionGuard(cfg, cache.NewCacheManager()),
		},
	}
	ctx := context.WithValue(context.Background(), proxyURLContextKey, "")

	_, err := handler.dialContext(ctx, "tcp", "localhost:8080")
	if err == nil || !strings.Contains(err.Error(), "itself") {
		t.Fatalf("dialContext() error = %v, want self-connection error", err)
	}
}

func TestDialContextRejectsSelfAsUpstream(t *testing.T) {
	cfg := &config.ProxyConfig{ListenSocksAddr: ":1080"}
	handler := &ProxyHandler{
		decision: &ProxyDecision{
			config:    cfg,
			selfGuard: newSelfConnectionGuard(cfg, cache.NewCacheManager()),
		},
	}
	ctx := context.WithValue(context.Background(), proxyURLContextKey, "socks5://localhost:1080")

	_, err := handler.dialContext(ctx, "tcp", "example.com:443")
	if err == nil || !strings.Contains(err.Error(), "itself") {
		t.Fatalf("dialContext() error = %v, want self-upstream error", err)
	}
}
