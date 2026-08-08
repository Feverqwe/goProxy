package handler

import (
	"net"
	"strconv"
	"strings"

	"goProxy/internal/cache"
	"goProxy/internal/config"
)

type selfConnectionGuard struct {
	listeners []selfListener
}

type selfListener struct {
	host     string
	port     string
	wildcard bool
	ips      []net.IP
}

// newSelfConnectionGuard resolves configured listener hostnames and snapshots
// local interface addresses once. A new guard is built on every config reload.
func newSelfConnectionGuard(cfg *config.ProxyConfig, cacheManager *cache.CacheManager) *selfConnectionGuard {
	guard := &selfConnectionGuard{}
	if cfg == nil {
		return guard
	}

	localIPs := getLocalInterfaceIPs()
	for _, listenAddr := range []string{cfg.ListenHttpAddr, cfg.ListenSocksAddr} {
		if listenAddr == "" {
			continue
		}

		host, port, err := net.SplitHostPort(listenAddr)
		if err != nil {
			continue
		}
		host = normalizeEndpointHost(host)

		listener := selfListener{host: host, port: port}
		if host == "" {
			listener.wildcard = true
			listener.ips = localIPs
		} else if ip := net.ParseIP(host); ip != nil {
			listener.wildcard = ip.IsUnspecified()
			if listener.wildcard {
				listener.ips = localIPs
			} else {
				listener.ips = []net.IP{ip}
			}
		} else if cacheManager != nil {
			listener.ips, _ = cacheManager.ResolveHost(host)
		}

		guard.listeners = append(guard.listeners, listener)
	}

	return guard
}

// isSelfConnection only compares addresses. It never performs DNS resolution
// or reads network interfaces in the connection path.
func (g *selfConnectionGuard) isSelfConnection(addr string) bool {
	if g == nil {
		return false
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	host = normalizeEndpointHost(host)
	targetIP := net.ParseIP(host)

	for _, listener := range g.listeners {
		if !samePort(port, listener.port) {
			continue
		}

		if targetIP == nil {
			if host == listener.host {
				return true
			}
			if isLocalhostName(host) && listener.acceptsIP(net.ParseIP("127.0.0.1")) {
				return true
			}
			continue
		}

		if listener.acceptsIP(targetIP) {
			return true
		}
	}

	return false
}

func (l selfListener) acceptsIP(targetIP net.IP) bool {
	if l.wildcard && (targetIP.IsLoopback() || targetIP.IsUnspecified()) {
		return true
	}
	for _, listenerIP := range l.ips {
		if targetIP.Equal(listenerIP) {
			return true
		}
	}
	return false
}

func samePort(first, second string) bool {
	if first == second {
		return true
	}

	firstPort, firstErr := strconv.ParseUint(first, 10, 16)
	secondPort, secondErr := strconv.ParseUint(second, 10, 16)
	return firstErr == nil && secondErr == nil && firstPort == secondPort
}

func normalizeEndpointHost(host string) string {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	if zoneIndex := strings.LastIndex(host, "%"); zoneIndex >= 0 {
		host = host[:zoneIndex]
	}
	return host
}

func isLocalhostName(host string) bool {
	return host == "localhost" || strings.HasSuffix(host, ".localhost")
}

func getLocalInterfaceIPs() []net.IP {
	addresses, err := net.InterfaceAddrs()
	if err != nil {
		return nil
	}

	ips := make([]net.IP, 0, len(addresses))
	for _, address := range addresses {
		switch value := address.(type) {
		case *net.IPNet:
			ips = append(ips, value.IP)
		case *net.IPAddr:
			ips = append(ips, value.IP)
		}
	}
	return ips
}
