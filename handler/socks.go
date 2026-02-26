package handler

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"sync"
	"time"

	"goProxy/cache"
	"goProxy/config"
	"goProxy/logging"

	"github.com/txthinking/socks5"
)

var bufferPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 128*1024)
	},
}

type SocksHandler struct {
	ph             *ProxyHandler
	logger         *logging.Logger
	udpManager     *UDPSessionManager
	defaultHandler *socks5.DefaultHandle
}

func NewSocksHandler(ph *ProxyHandler, config *config.ProxyConfig, cacheManager *cache.CacheManager, logger *logging.Logger) *SocksHandler {
	return &SocksHandler{
		ph:             ph,
		logger:         logger,
		udpManager:     NewUDPSessionManager(1 * time.Minute),
		defaultHandler: &socks5.DefaultHandle{},
	}
}

func (s *SocksHandler) TCPHandle(server *socks5.Server, conn *net.TCPConn, r *socks5.Request) error {
	s.ph.mu.RLock()
	currentDecision := s.ph.decision
	s.ph.mu.RUnlock()

	localAddr := conn.LocalAddr().(*net.TCPAddr)

	var atyp byte
	var ip []byte

	if localAddr.IP.To4() != nil {
		atyp = socks5.ATYPIPv4
		ip = localAddr.IP.To4()
	} else {
		atyp = socks5.ATYPIPv6
		ip = localAddr.IP.To16()
	}

	if r.Cmd == socks5.CmdUDP {
		uaddr, err := net.ResolveUDPAddr("udp", server.UDPConn.LocalAddr().String())
		if err != nil {
			return err
		}

		port := []byte{byte(uaddr.Port >> 8), byte(uaddr.Port & 0xff)}

		rep := socks5.NewReply(socks5.RepSuccess, atyp, ip, port)

		if _, err := rep.WriteTo(conn); err != nil {
			return err
		}

		b := make([]byte, 1)
		for {
			_, err := conn.Read(b)
			if err != nil {
				return nil
			}
		}
	}

	targetHost := r.Address()
	target, _, _ := net.SplitHostPort(targetHost)

	proxyURL, decision, err := currentDecision.GetProxyForHost(target)
	if err != nil {
		s.logger.Error("SOCKS5 Decision Error: %v", err)
		rep := socks5.NewReply(socks5.RepHostUnreachable, atyp, ip, []byte{0, 0})
		rep.WriteTo(conn)
		return err
	}

	if proxyURL == "#" {
		s.logger.Info("SOCKS5 Blocked: %s (rule: %s)", target, decision.RuleName)
		rep := socks5.NewReply(socks5.RepConnectionRefused, atyp, ip, []byte{0, 0})
		rep.WriteTo(conn)
		return nil
	}

	if proxyURL == "" {
		s.logger.Info("SOCKS5 Direct: %s (rule: %s)", target, decision.RuleName)
	} else {
		s.logger.Info("SOCKS5 Proxy: %s via %s (rule: %s)", target, decision.Proxy, decision.RuleName)
	}

	ctx := context.WithValue(context.Background(), proxyURLContextKey, proxyURL)
	remote, err := s.ph.dialContext(ctx, "tcp", targetHost)
	if err != nil {
		s.logger.Error("SOCKS5 Dial Error (%s): %v", target, err)
		rep := socks5.NewReply(socks5.RepServerFailure, atyp, ip, []byte{0, 0})
		rep.WriteTo(conn)
		return err
	}
	defer remote.Close()

	rep := socks5.NewReply(socks5.RepSuccess, atyp, ip, []byte{0, 0})
	if _, err := rep.WriteTo(conn); err != nil {
		s.logger.Error("SOCKS5: Error writing reply: %v", err)
		return err
	}

	errCh := make(chan error, 2)

	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			remote.Close()
			conn.Close()
		})
	}

	copyDir := func(dst io.Writer, src io.Reader) {
		buf := bufferPool.Get().([]byte)
		defer bufferPool.Put(buf)

		_, err := io.CopyBuffer(dst, src, buf)
		errCh <- err
		closeBoth()
	}

	go copyDir(remote, conn)
	go copyDir(conn, remote)

	return <-errCh
}

func (s *SocksHandler) UDPHandle(server *socks5.Server, addr *net.UDPAddr, d *socks5.Datagram) error {
	s.ph.mu.RLock()
	currentDecision := s.ph.decision
	s.ph.mu.RUnlock()

	targetHost := d.Address()
	target, port, _ := net.SplitHostPort(targetHost)
	clientKey := addr.String() + "->" + targetHost

	proxyURL, decision, err := currentDecision.GetProxyForHost(target)
	if err != nil {
		s.logger.Error("SOCKS5 UDP Decision Error: %v", err)
		return nil
	}

	if proxyURL == "#" {
		s.logger.Info("SOCKS5 UDP Blocked: %s", target)
		return nil
	}

	upstreamConn, exists := s.udpManager.Get(clientKey)

	if !exists {
		if proxyURL == "" {
			s.logger.Info("SOCKS5 UDP Direct: %s (rule: %s)", target, decision.RuleName)

			extIp4 := currentDecision.config.ExternalIp4
			extIp6 := currentDecision.config.ExternalIp6
			extDns := currentDecision.config.ExternalDns

			if extIp4 == "" && extIp6 == "" {
				return s.defaultHandler.UDPHandle(server, addr, d)
			}

			dialer := net.Dialer{
				Timeout: 10 * time.Second,
			}

			ips, err := currentDecision.cache.ResolveExternalHost(target, extDns, func(ips []net.IP) string {
				return getSourceIpByIps(ips, extIp4, extIp6)
			})
			if err != nil {
				s.logger.Error("SOCKS5 UDP DNS Resolve Error for %s: %v", target, err)
				return err
			}

			if len(ips) == 0 {
				return fmt.Errorf("no IP addresses found for host: %s", target)
			}

			sourceIP := getSourceIpByIps(ips, extIp4, extIp6)
			if sourceIP != "" {
				dialer.LocalAddr = &net.UDPAddr{IP: net.ParseIP(sourceIP), Port: 0}
			}

			targetIPAddr := net.JoinHostPort(ips[0].String(), port)
			upstreamConn, err = dialer.Dial("udp", targetIPAddr)
			if err != nil {
				s.logger.Error("SOCKS5 UDP Direct Dial Error: %v", err)
				return err
			}
		} else {
			s.logger.Info("SOCKS5 UDP Proxy: %s via %s (rule: %s)", target, decision.Proxy, decision.RuleName)

			parsedURL, err := url.Parse(proxyURL)
			if err != nil || (parsedURL.Scheme != "socks5" && parsedURL.Scheme != "socks5h") {
				s.logger.Warn("UDP Associate only supports SOCKS5 upstream. Falling back to direct for %s", target)
				return s.defaultHandler.UDPHandle(server, addr, d)
			}

			user, pass := "", ""
			if parsedURL.User != nil {
				user = parsedURL.User.Username()
				pass, _ = parsedURL.User.Password()
			}

			client, err := socks5.NewClient(parsedURL.Host, user, pass, 30, 30)
			if err != nil {
				s.logger.Error("SOCKS5 UDP Client Error: %v", err)
				return err
			}

			upstreamConn, err = client.Dial("udp", targetHost)
			if err != nil {
				s.logger.Error("SOCKS5 UDP Upstream Dial Error: %v", err)
				return err
			}
		}

		s.udpManager.Set(clientKey, upstreamConn)
		go s.listenUpstreamUDP(server, addr, targetHost, upstreamConn, clientKey)
	}

	_, err = upstreamConn.Write(d.Data)
	if err != nil {
		s.logger.Debug("SOCKS5 UDP Write Error: %v", err)
		s.udpManager.sessions.Delete(clientKey)
		upstreamConn.Close()
	}
	return err
}

func (s *SocksHandler) listenUpstreamUDP(server *socks5.Server, clientAddr *net.UDPAddr, target string, upstream net.Conn, clientKey string) {
	defer func() {
		upstream.Close()
		s.udpManager.sessions.Delete(clientKey)
	}()

	buf := make([]byte, 65507)
	atyp, dstIP, dstPort, err := socks5.ParseAddress(target)
	if err != nil {
		s.logger.Error("UDP Parse Address Error: %v", err)
		return
	}

	for {
		upstream.SetReadDeadline(time.Now().Add(2 * time.Minute))

		n, err := upstream.Read(buf)
		if err != nil {
			return
		}

		resp := socks5.NewDatagram(atyp, dstIP, dstPort, buf[:n])
		_, err = server.UDPConn.WriteToUDP(resp.Bytes(), clientAddr)
		if err != nil {
			s.logger.Debug("UDP Client Write Error: %v", err)
			return
		}
	}
}
