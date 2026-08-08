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

func parseSourceIP(sourceIP string, wantIPv4 bool) (net.IP, error) {
	ip := net.ParseIP(sourceIP)
	if ip == nil {
		return nil, fmt.Errorf("invalid source IP %s", sourceIP)
	}
	if wantIPv4 != (ip.To4() != nil) {
		return nil, fmt.Errorf("source IP %s has the wrong address family", sourceIP)
	}
	return ip, nil
}
