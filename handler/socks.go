package handler

import (
	"context"
	"io"
	"net"
	"net/url"
	"time"

	"goProxy/cache"
	"goProxy/config"
	"goProxy/logging"

	"github.com/txthinking/socks5"
)

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
		udpManager:     NewUDPSessionManager(5 * time.Minute),
		defaultHandler: &socks5.DefaultHandle{},
	}
}

// TCPHandle handles SOCKS5 CONNECT requests
func (s *SocksHandler) TCPHandle(server *socks5.Server, conn *net.TCPConn, r *socks5.Request) error {
	localAddr := conn.LocalAddr().(*net.TCPAddr)

	// 3. Подготавливаем IP (v4 или v6)
	var atyp byte
	var ip []byte

	if localAddr.IP.To4() != nil {
		atyp = socks5.ATYPIPv4
		ip = localAddr.IP.To4()
	} else {
		atyp = socks5.ATYPIPv6
		ip = localAddr.IP.To16()
	}

	// If it's a UDP Associate request, we don't dial anything yet.
	// We just tell the client we are ready to relay.
	if r.Cmd == socks5.CmdUDP {
		// 1. Get the actual UDP address the server is listening on
		uaddr, err := net.ResolveUDPAddr("udp", server.UDPConn.LocalAddr().String())
		if err != nil {
			return err
		}

		port := []byte{byte(uaddr.Port >> 8), byte(uaddr.Port & 0xff)}

		rep := socks5.NewReply(socks5.RepSuccess, atyp, ip, port)

		if _, err := rep.WriteTo(conn); err != nil {
			return err
		}

		// 3. Keep TCP alive (as before)
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

	proxyURL, decision, err := s.ph.decision.GetProxyForHost(target)
	if err != nil {
		s.logger.Error("SOCKS5 Decision Error: %v", err)
		rep := socks5.NewReply(socks5.RepHostUnreachable, atyp, ip, []byte{0, 0})
		rep.WriteTo(conn)
		return err
	}

	// 1. Rule-based blocking
	if proxyURL == "#" {
		s.logger.Info("SOCKS5 Blocked: %s (rule: %s)", target, decision.RuleName)
		rep := socks5.NewReply(socks5.RepConnectionRefused, atyp, ip, []byte{0, 0})
		rep.WriteTo(conn)
		return nil
	}

	// 2. Logging connection attempt
	if proxyURL == "" {
		s.logger.Info("SOCKS5 Direct: %s (rule: %s)", target, decision.RuleName)
	} else {
		s.logger.Info("SOCKS5 Proxy: %s via %s (rule: %s)", target, decision.Proxy, decision.RuleName)
	}

	// 3. Dialing target (Direct or via Upstream Proxy)
	ctx := context.WithValue(context.Background(), proxyURLContextKey, proxyURL)
	remote, err := s.ph.dialContext(ctx, "tcp", targetHost)
	if err != nil {
		s.logger.Error("SOCKS5 Dial Error (%s): %v", target, err)
		rep := socks5.NewReply(socks5.RepServerFailure, atyp, ip, []byte{0, 0})
		rep.WriteTo(conn)
		return err
	}
	defer remote.Close()

	// 4. Success reply to client
	rep := socks5.NewReply(socks5.RepSuccess, atyp, ip, []byte{0, 0})
	if _, err := rep.WriteTo(conn); err != nil {
		s.logger.Error("SOCKS5: Error writing reply: %v", err)
		return err
	}

	// 5. Bidirectional data copy
	errCh := make(chan error, 2)
	go func() { _, err := io.Copy(remote, conn); errCh <- err }()
	go func() { _, err := io.Copy(conn, remote); errCh <- err }()

	return <-errCh
}

// UDPHandle handles SOCKS5 UDP ASSOCIATE data packets
func (s *SocksHandler) UDPHandle(server *socks5.Server, addr *net.UDPAddr, d *socks5.Datagram) error {
	targetHost := d.Address()
	target, _, _ := net.SplitHostPort(targetHost)
	clientKey := addr.String() + "->" + targetHost

	proxyURL, decision, err := s.ph.decision.GetProxyForHost(target)
	if err != nil {
		s.logger.Error("SOCKS5 UDP Decision Error: %v", err)
		return nil
	}

	// 1. Blocking
	if proxyURL == "#" {
		s.logger.Info("SOCKS5 UDP Blocked: %s", target) // Use debug for UDP to avoid log flooding
		return nil
	}

	// 2. Check for existing session in UDPSessionManager
	upstreamConn, exists := s.udpManager.Get(clientKey)

	if !exists {
		// Logic for new UDP session
		if proxyURL == "" {
			s.logger.Info("SOCKS5 UDP Direct: %s (rule: %s)", target, decision.RuleName)

			s.ph.mu.RLock()
			extIf := s.ph.decision.config.ExternalIf
			extIp4 := s.ph.decision.config.ExternalIp4
			extIp6 := s.ph.decision.config.ExternalIp6
			s.ph.mu.RUnlock()

			if extIf == "" && extIp4 == "" && extIp6 == "" {
				return s.defaultHandler.UDPHandle(server, addr, d)
			}

			dialer := net.Dialer{
				Timeout: 10 * time.Second,
			}

			if extIp4 != "" || extIp6 != "" {
				dialer.LocalAddr = s.getLocalUDPAddr(extIp4, extIp6, targetHost)
			}

			upstreamConn, err = dialer.Dial("udp", targetHost)
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

	// 3. Send packet to upstream
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
		// If no data for 6 minutes, the goroutine dies and the manager will eventually cleanup
		upstream.SetReadDeadline(time.Now().Add(6 * time.Minute))

		n, err := upstream.Read(buf)
		if err != nil {
			// Expected when connection is closed or timed out
			return
		}

		// Re-package the response from the target into a SOCKS5 Datagram
		resp := socks5.NewDatagram(atyp, dstIP, dstPort, buf[:n])
		_, err = server.UDPConn.WriteToUDP(resp.Bytes(), clientAddr)
		if err != nil {
			s.logger.Debug("UDP Client Write Error: %v", err)
			return
		}
	}
}

func (s *SocksHandler) getLocalUDPAddr(extIp4 string, extIp6 string, target string) *net.UDPAddr {
	host, _, _ := net.SplitHostPort(target)
	ips, err := s.ph.decision.cache.ResolveHost(host)

	if err == nil && len(ips) > 0 {
		targetIP := ips[0]
		var sourceIP string
		if targetIP.To4() != nil {
			sourceIP = extIp4
		} else {
			sourceIP = extIp6
		}

		if sourceIP != "" {
			return &net.UDPAddr{IP: net.ParseIP(sourceIP), Port: 0}
		}
	}

	return nil
}
