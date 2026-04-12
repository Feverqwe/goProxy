package handler

import (
	"net"
)

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
