package handler

import (
	"context"
	"net"
	"time"

	"golang.org/x/sync/singleflight"
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
	ips, err := resolveExternalHost(host, extDns, extIp4, extIp6)
	if err != nil {
		s.logger.Warn("External DNS failed for %s: %v. Falling back to internal cache.", host, err)

		ips, err = s.decision.cache.ResolveHost(host)
		if err != nil {
			return "", err
		}
	}

	return getSourceIpByIps(ips, extIp4, extIp6), nil
}

var externalDnsGroup singleflight.Group

func resolveExternalHost(hostname, extDns, extIp4, extIp6 string) ([]net.IP, error) {
	if ip := net.ParseIP(hostname); ip != nil {
		return []net.IP{ip}, nil
	}

	ipsInterface, err, _ := externalDnsGroup.Do(hostname, func() (interface{}, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		var resolver *net.Resolver
		if extDns != "" {
			dnsAddr := extDns
			dnsHost, _, err := net.SplitHostPort(extDns)
			if err != nil {
				dnsHost = extDns
				dnsAddr = net.JoinHostPort(extDns, "53")
			}

			resolver = &net.Resolver{
				PreferGo: true,
				Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
					dnsIp := net.ParseIP(dnsHost)
					sourceIP := getSourceIpByIps([]net.IP{dnsIp}, extIp4, extIp6)
					d := net.Dialer{Timeout: time.Second * 5}
					if sourceIP != "" {
						d.LocalAddr = &net.UDPAddr{IP: net.ParseIP(sourceIP)}
					}
					return d.DialContext(ctx, "udp", dnsAddr)
				},
			}
		} else {
			resolver = net.DefaultResolver
		}

		ips, err := resolver.LookupIP(ctx, "ip", hostname)
		if err != nil {
			return nil, err
		}

		return ips, nil
	})

	if err != nil {
		return nil, err
	}

	return ipsInterface.([]net.IP), nil
}
