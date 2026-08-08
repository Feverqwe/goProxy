package handler

import (
	"fmt"
	"net"
	"net/url"
)

func getSourceIpByIps(ips []net.IP, extIp4, extIp6 string) string {
	_, sourceIP := getTargetAndSourceIp(ips, extIp4, extIp6)
	return sourceIP
}

func getTargetAndSourceIp(ips []net.IP, extIp4, extIp6 string) (net.IP, string) {
	for _, ip := range ips {
		if ip == nil {
			continue
		}
		isV4 := ip.To4() != nil

		if isV4 && extIp4 != "" {
			return ip, extIp4
		}
		if !isV4 && extIp6 != "" {
			return ip, extIp6
		}
	}

	if extIp4 == "" && extIp6 == "" {
		for _, ip := range ips {
			if ip != nil {
				return ip, ""
			}
		}
	}

	return nil, ""
}

func getProxyAddress(proxyURL *url.URL) (string, error) {
	host := proxyURL.Hostname()
	if host == "" {
		return "", fmt.Errorf("proxy URL has no host")
	}

	port := proxyURL.Port()
	if port == "" {
		switch proxyURL.Scheme {
		case "http":
			port = "80"
		case "https":
			port = "443"
		default:
			return "", fmt.Errorf("unsupported proxy scheme: %s", proxyURL.Scheme)
		}
	}

	return net.JoinHostPort(host, port), nil
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
