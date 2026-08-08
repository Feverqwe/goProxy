package cache

import (
	"fmt"
	"net"
	"testing"
)

func TestResolveExternalHostPreservesCachedOrder(t *testing.T) {
	manager := NewCacheManager()
	hostname := "ordered.example"
	extDNS := "192.0.2.53"
	cacheKey := fmt.Sprintf("ext:%s:%s", extDNS, hostname)
	want := []net.IP{
		net.ParseIP("2001:db8::10"),
		net.ParseIP("192.0.2.10"),
		net.ParseIP("192.0.2.11"),
	}
	manager.dnsCache.Add(cacheKey, cloneIPs(want))

	for range 20 {
		got, err := manager.ResolveExternalHost(hostname, extDNS, func([]net.IP) string { return "" })
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
