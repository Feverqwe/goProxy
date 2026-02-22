package handler

import "net"

func (s *ProxyHandler) getSourceIp(addr string, extIp4, extIp6 string) (string, error) {
	host, _, _ := net.SplitHostPort(addr)
	ips, err := s.decision.cache.ResolveHost(host)
	if err != nil {
		return "", err
	}

	var sourceIP string
	if len(ips) > 0 {
		var hasV4, hasV6 bool
		for _, ip := range ips {
			if ip.To4() != nil {
				hasV4 = true
			} else {
				hasV6 = true
			}
		}

		if hasV6 && extIp6 != "" {
			sourceIP = extIp6
		} else if hasV4 && extIp4 != "" {
			sourceIP = extIp4
		}
	}

	return sourceIP, nil
}
