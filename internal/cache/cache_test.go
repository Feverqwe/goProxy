package cache

import (
	"context"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

func TestResolveExternalHostPreservesCachedOrder(t *testing.T) {
	manager := NewCacheManager()
	hostname := "ordered.example"
	options := ExternalDNSOptions{Server: "192.0.2.53"}
	parameters, err := makeExternalDNSParameters(hostname, options)
	if err != nil {
		t.Fatalf("make external DNS parameters: %v", err)
	}
	want := []net.IP{
		net.ParseIP("2001:db8::10"),
		net.ParseIP("192.0.2.10"),
		net.ParseIP("192.0.2.11"),
	}
	manager.dnsCache.Add(parameters.cacheKey, cloneIPs(want))

	for range 20 {
		got, err := manager.ResolveExternalHost(hostname, options)
		if err != nil {
			t.Fatalf("resolve cached host: %v", err)
		}
		if len(got) != len(want) {
			t.Fatalf("resolved IP count = %d, want %d", len(got), len(want))
		}
		for i := range want {
			if !got[i].Equal(want[i]) {
				t.Fatalf("resolved IP at index %d = %s, want %s", i, got[i], want[i])
			}
		}

		got[0][0] ^= 0xff
	}
}

func TestResolveExternalHostContextHonorsCancellation(t *testing.T) {
	dnsServer, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen DNS server: %v", err)
	}
	defer dnsServer.Close()

	manager := NewCacheManager()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	started := time.Now()
	_, err = manager.ResolveExternalHostContext(ctx, "unresponsive.example", ExternalDNSOptions{Server: dnsServer.LocalAddr().String()})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("resolve error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("canceled DNS lookup returned after %s, want less than one second", elapsed)
	}
}

func TestExternalDNSFlightKeyIncludesSourceBinding(t *testing.T) {
	oldParameters, err := makeExternalDNSParameters("reload.example", ExternalDNSOptions{
		Server:     "192.0.2.53",
		SourceIPv4: "192.0.2.10",
	})
	if err != nil {
		t.Fatalf("make old parameters: %v", err)
	}
	newParameters, err := makeExternalDNSParameters("reload.example", ExternalDNSOptions{
		Server:     "192.0.2.53",
		SourceIPv4: "192.0.2.20",
	})
	if err != nil {
		t.Fatalf("make new parameters: %v", err)
	}

	if oldParameters.cacheKey != newParameters.cacheKey {
		t.Fatal("source binding unexpectedly changed the DNS result cache key")
	}
	if oldParameters.flightKey == newParameters.flightKey {
		t.Fatal("source binding did not change the in-flight lookup key")
	}
}

func TestResolveCachedKeepsDifferentSourceFlightsSeparate(t *testing.T) {
	manager := NewCacheManager()
	started := make(chan string, 2)
	release := make(chan struct{})
	var releaseOnce sync.Once
	releaseAll := func() {
		releaseOnce.Do(func() { close(release) })
	}
	defer releaseAll()

	lookup := func(name, address string) func(context.Context) ([]net.IP, error) {
		return func(ctx context.Context) ([]net.IP, error) {
			started <- name
			select {
			case <-release:
				return []net.IP{net.ParseIP(address)}, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
	}

	type result struct {
		ips []net.IP
		err error
	}
	results := make(chan result, 2)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	go func() {
		ips, err := manager.resolveCached(ctx, "shared-cache", "old-source", lookup("old", "192.0.2.10"))
		results <- result{ips: ips, err: err}
	}()
	if name := <-started; name != "old" {
		t.Fatalf("first lookup = %q, want old", name)
	}

	go func() {
		ips, err := manager.resolveCached(ctx, "shared-cache", "new-source", lookup("new", "192.0.2.20"))
		results <- result{ips: ips, err: err}
	}()
	select {
	case name := <-started:
		if name != "new" {
			t.Fatalf("second lookup = %q, want new", name)
		}
	case <-time.After(250 * time.Millisecond):
		t.Fatal("different source flight was incorrectly coalesced")
	}

	releaseAll()
	for range 2 {
		result := <-results
		if result.err != nil {
			t.Fatalf("resolve cached: %v", result.err)
		}
		if len(result.ips) != 1 {
			t.Fatalf("resolved IP count = %d, want 1", len(result.ips))
		}
	}
}
