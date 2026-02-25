package handler

import "net"

func (s *ProxyHandler) getSourceIp(addr string, extIp4, extIp6 string) (string, error) {
	host, _, _ := net.SplitHostPort(addr)
	ips, err := s.decision.cache.ResolveHost(host)
	if err != nil {
		return "", err
	}

	for _, ip := range ips {
		isV4 := ip.To4() != nil

		if isV4 && extIp4 != "" {
			return extIp4, nil
		}
		if !isV4 && extIp6 != "" {
			return extIp6, nil
		}
	}

	return "", nil
}
