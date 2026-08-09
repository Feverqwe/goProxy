package handler

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"sync"
	"time"

	"goProxy/internal/cache"
	"goProxy/internal/config"
	"goProxy/internal/logging"

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

	return proxyTCPConnections(conn, remote)
}

func proxyTCPConnections(left, right net.Conn) error {
	errCh := make(chan error, 2)

	var once sync.Once
	closeBoth := func() {
		once.Do(func() {
			right.Close()
			left.Close()
		})
	}

	halfClose := func(dst, src net.Conn) bool {
		closeWriter, ok := dst.(interface{ CloseWrite() error })
		if !ok || closeWriter.CloseWrite() != nil {
			return false
		}
		if closeReader, ok := src.(interface{ CloseRead() error }); ok {
			_ = closeReader.CloseRead()
		}
		return true
	}

	copyDir := func(dst, src net.Conn) {
		buf := bufferPool.Get().([]byte)
		defer bufferPool.Put(buf)

		_, err := io.CopyBuffer(dst, src, buf)
		if err != nil || !halfClose(dst, src) {
			closeBoth()
		}
		errCh <- err
	}

	go copyDir(right, left)
	go copyDir(left, right)

	var firstErr error
	for range 2 {
		if err := <-errCh; err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
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

	extIp4 := currentDecision.config.ExternalIp4
	extIp6 := currentDecision.config.ExternalIp6
	extDns := currentDecision.config.ExternalDns
	dnsOptions := cache.ExternalDNSOptions{
		Server:     extDns,
		SourceIPv4: extIp4,
		SourceIPv6: extIp6,
	}

	var parsedProxyURL *url.URL
	if proxyURL == "" {
		if extIp4 == "" && extIp6 == "" && extDns == "" {
			return s.defaultHandler.UDPHandle(server, addr, d)
		}
	} else {
		parsedProxyURL, err = url.Parse(proxyURL)
		if err != nil || (parsedProxyURL.Scheme != "socks5" && parsedProxyURL.Scheme != "socks5h") {
			s.logger.Warn("UDP Associate only supports SOCKS5 upstream. Falling back to direct for %s", target)
			return s.defaultHandler.UDPHandle(server, addr, d)
		}
	}

	session, err := s.udpManager.GetOrCreate(clientKey, func() (net.Conn, error) {
		if proxyURL == "" {
			s.logger.Info("SOCKS5 UDP Direct: %s (rule: %s)", target, decision.RuleName)
			dialer := net.Dialer{
				Timeout: 10 * time.Second,
			}

			ips, err := currentDecision.cache.ResolveExternalHost(target, dnsOptions)
			if err != nil {
				s.logger.Error("SOCKS5 UDP DNS Resolve Error for %s: %v", target, err)
				return nil, err
			}

			if len(ips) == 0 {
				return nil, fmt.Errorf("no IP addresses found for host: %s", target)
			}

			targetIP, sourceIP := getTargetAndSourceIp(ips, extIp4, extIp6)
			if targetIP == nil {
				return nil, fmt.Errorf("no IP addresses for %s are compatible with the configured source IPs", target)
			}
			if sourceIP != "" {
				source, err := parseSourceIP(sourceIP, targetIP.To4() != nil)
				if err != nil {
					return nil, err
				}
				dialer.LocalAddr = &net.UDPAddr{IP: source}
			}

			targetIPAddr := net.JoinHostPort(targetIP.String(), port)
			upstreamConn, err := dialer.Dial("udp", targetIPAddr)
			if err != nil {
				s.logger.Error("SOCKS5 UDP Direct Dial Error: %v", err)
				return nil, err
			}
			return upstreamConn, nil
		}

		s.logger.Info("SOCKS5 UDP Proxy: %s via %s (rule: %s)", target, decision.Proxy, decision.RuleName)
		user, pass := "", ""
		if parsedProxyURL.User != nil {
			user = parsedProxyURL.User.Username()
			pass, _ = parsedProxyURL.User.Password()
		}

		client, err := socks5.NewClient(parsedProxyURL.Host, user, pass, 30, 0)
		if err != nil {
			s.logger.Error("SOCKS5 UDP Client Error: %v", err)
			return nil, err
		}

		upstreamConn, err := client.Dial("udp", targetHost)
		if err != nil {
			s.logger.Error("SOCKS5 UDP Upstream Dial Error: %v", err)
			return nil, err
		}
		return upstreamConn, nil
	})
	if err != nil {
		return err
	}

	upstreamConn, sessionOpen := session.conn()
	if !sessionOpen {
		return fmt.Errorf("SOCKS5 UDP session closed during use")
	}
	session.startListener(func() {
		go s.listenUpstreamUDP(server, addr, targetHost, session, clientKey)
	})

	if err := upstreamConn.SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil {
		s.udpManager.Delete(clientKey, session)
		return err
	}
	_, err = upstreamConn.Write(d.Data)
	if err != nil {
		s.logger.Debug("SOCKS5 UDP Write Error: %v", err)
		s.udpManager.Delete(clientKey, session)
	}
	return err
}

func (s *SocksHandler) listenUpstreamUDP(server *socks5.Server, clientAddr *net.UDPAddr, target string, session *UDPSession, clientKey string) {
	defer s.udpManager.Delete(clientKey, session)

	upstream, ok := session.conn()
	if !ok {
		return
	}

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
