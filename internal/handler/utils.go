package handler

import (
	"fmt"
	"net"
	"net/url"
)

func socks5ProxyAddress(proxyURL *url.URL) (string, error) {
	host := proxyURL.Hostname()
	if host == "" {
		return "", fmt.Errorf("SOCKS5 proxy URL has no host")
	}
	port := proxyURL.Port()
	if port == "" {
		port = "1080"
	}
	return net.JoinHostPort(host, port), nil
}

func getSourceIpByIps(ips []net.IP, extIp4, extIp6 string) string {
	for _, ip := range ips {
		isV4 := ip.To4() != nil

		if isV4 && extIp4 != "" {
			return extIp4
		}
		if !isV4 && extIp6 != "" {
			return extIp6
		}
	}

	return ""
}

func (s *ProxyHandler) getSourceIp(addr string, extDns, extIp4, extIp6 string) (string, error) {
	host, _, _ := net.SplitHostPort(addr)
	ips, err := s.decision.cache.ResolveExternalHost(host, extDns, func(ips []net.IP) string {
		return getSourceIpByIps(ips, extIp4, extIp6)
	})
	if err != nil {
		return "", err
	}

	return getSourceIpByIps(ips, extIp4, extIp6), nil
}
