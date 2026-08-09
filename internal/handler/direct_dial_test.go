package handler

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"goProxy/internal/cache"
	"goProxy/internal/config"
	"goProxy/internal/logging"

	"golang.org/x/net/dns/dnsmessage"
)

func newDirectTestProxyHandler(t *testing.T, cfg *config.ProxyConfig) *ProxyHandler {
	t.Helper()

	logger := logging.NewLogger(environmentTestLoggerConfig{})
	t.Cleanup(func() {
		if err := logger.Close(); err != nil {
			t.Errorf("close logger: %v", err)
		}
	})

	return NewProxyHandler(cfg, cache.NewCacheManager(), logger)
}

func TestGetTargetAndSourceIPUsesCompatibleFamily(t *testing.T) {
	ips := []net.IP{
		net.ParseIP("2001:db8::10"),
		net.ParseIP("192.0.2.10"),
	}

	target, source := getTargetAndSourceIp(ips, "192.0.2.20", "")
	if !target.Equal(net.ParseIP("192.0.2.10")) || source != "192.0.2.20" {
		t.Fatalf("IPv4 selection = (%v, %q), want (192.0.2.10, 192.0.2.20)", target, source)
	}

	target, source = getTargetAndSourceIp(ips, "", "2001:db8::20")
	if !target.Equal(net.ParseIP("2001:db8::10")) || source != "2001:db8::20" {
		t.Fatalf("IPv6 selection = (%v, %q), want (2001:db8::10, 2001:db8::20)", target, source)
	}

	target, source = getTargetAndSourceIp(ips, "", "")
	if !target.Equal(ips[0]) || source != "" {
		t.Fatalf("unbound selection = (%v, %q), want first address without source", target, source)
	}
}

func TestDirectDialDelegatesResolutionAndPreservesSourceBinding(t *testing.T) {
	targetListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen target: %v", err)
	}
	defer targetListener.Close()

	dnsAddr := startTestDNSServer(t, []dnsmessage.AResource{
		{A: [4]byte{127, 0, 0, 2}},
		{A: [4]byte{127, 0, 0, 1}},
	})

	accepted := make(chan net.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := targetListener.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- conn
	}()

	cfg := &config.ProxyConfig{
		DefaultProxy: "direct",
		Proxies:      map[string]string{"direct": ""},
		ExternalIp4:  "127.0.0.1",
		ExternalIp6:  "::1",
		ExternalDns:  dnsAddr,
	}
	handler := newDirectTestProxyHandler(t, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, proxyURLContextKey, "")

	_, port, err := net.SplitHostPort(targetListener.Addr().String())
	if err != nil {
		t.Fatalf("split target address: %v", err)
	}
	conn, err := handler.dialContext(ctx, "tcp", net.JoinHostPort("fallback.test", port))
	if err != nil {
		t.Fatalf("dial target through DNS fallback: %v", err)
	}
	defer conn.Close()

	select {
	case serverConn := <-accepted:
		defer serverConn.Close()
		remote, ok := serverConn.RemoteAddr().(*net.TCPAddr)
		if !ok {
			t.Fatalf("remote address type = %T, want *net.TCPAddr", serverConn.RemoteAddr())
		}
		if !remote.IP.Equal(net.ParseIP("127.0.0.1")) {
			t.Fatalf("source IP = %s, want 127.0.0.1", remote.IP)
		}
	case err := <-acceptErr:
		t.Fatalf("accept target connection: %v", err)
	case <-ctx.Done():
		t.Fatalf("accept target connection: %v", ctx.Err())
	}
}

func TestDirectDialUsesExternalDNSWithoutSourceBinding(t *testing.T) {
	targetListener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen target: %v", err)
	}
	defer targetListener.Close()

	dnsAddr := startTestDNSServer(t, []dnsmessage.AResource{
		{A: [4]byte{127, 0, 0, 1}},
	})
	cfg := &config.ProxyConfig{
		DefaultProxy: "direct",
		Proxies:      map[string]string{"direct": ""},
		ExternalDns:  dnsAddr,
	}
	handler := newDirectTestProxyHandler(t, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, proxyURLContextKey, "")

	_, port, err := net.SplitHostPort(targetListener.Addr().String())
	if err != nil {
		t.Fatalf("split target address: %v", err)
	}
	conn, err := handler.dialContext(ctx, "tcp", net.JoinHostPort("external-dns-only.test", port))
	if err != nil {
		t.Fatalf("dial target using external DNS only: %v", err)
	}
	conn.Close()
}

func TestDirectDialAllowsConfiguredListenerDestination(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen target: %v", err)
	}
	defer listener.Close()

	cfg := &config.ProxyConfig{
		DefaultProxy:   "direct",
		Proxies:        map[string]string{"direct": ""},
		ListenHttpAddr: listener.Addr().String(),
	}
	handler := newDirectTestProxyHandler(t, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, proxyURLContextKey, "")

	conn, err := handler.dialContext(ctx, "tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial configured listener destination: %v", err)
	}
	conn.Close()
}

func TestDirectDialTimeoutIncludesDNSResolution(t *testing.T) {
	dnsServer, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen DNS server: %v", err)
	}
	defer dnsServer.Close()

	cfg := &config.ProxyConfig{
		DefaultProxy: "direct",
		Proxies:      map[string]string{"direct": ""},
		ExternalDns:  dnsServer.LocalAddr().String(),
	}
	handler := newDirectTestProxyHandler(t, cfg)
	dialer := newDirectRouteDialer(handler.decision, handler.logger)
	dialer.timeout = 50 * time.Millisecond

	started := time.Now()
	_, err = dialer.DialContext(context.Background(), "tcp", "unresponsive.example:443")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("dial error = %v, want context deadline exceeded", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("direct dial returned after %s, want less than one second", elapsed)
	}
}

func TestDialTCPSerialTriesNextAddress(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen target: %v", err)
	}
	defer listener.Close()

	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split target address: %v", err)
	}
	candidates := tcpDialCandidates{
		network: "tcp4",
		ips: []net.IP{
			net.ParseIP("127.0.0.2"),
			net.ParseIP("127.0.0.1"),
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := dialTCPSerial(ctx, port, candidates)
	if err != nil {
		t.Fatalf("dial second target address: %v", err)
	}
	conn.Close()
}

func TestDialTCPHappyEyeballsFallsBackBetweenFamilies(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen target: %v", err)
	}
	defer listener.Close()

	_, port, err := net.SplitHostPort(listener.Addr().String())
	if err != nil {
		t.Fatalf("split target address: %v", err)
	}
	primary := tcpDialCandidates{
		network: "tcp6",
		ips:     []net.IP{net.ParseIP("::1")},
	}
	fallback := tcpDialCandidates{
		network: "tcp4",
		ips:     []net.IP{net.ParseIP("127.0.0.1")},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	conn, err := dialTCPHappyEyeballs(ctx, port, primary, fallback)
	if err != nil {
		t.Fatalf("dial fallback address family: %v", err)
	}
	conn.Close()
}

func startTestDNSServer(t *testing.T, answers []dnsmessage.AResource) string {
	t.Helper()

	server, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen DNS: %v", err)
	}
	t.Cleanup(func() { _ = server.Close() })

	go func() {
		buffer := make([]byte, 1500)
		for {
			n, clientAddr, err := server.ReadFrom(buffer)
			if err != nil {
				return
			}

			var parser dnsmessage.Parser
			header, err := parser.Start(buffer[:n])
			if err != nil {
				t.Errorf("parse DNS header: %v", err)
				return
			}
			question, err := parser.Question()
			if err != nil {
				t.Errorf("parse DNS question: %v", err)
				return
			}

			builder := dnsmessage.NewBuilder(nil, dnsmessage.Header{
				ID:                 header.ID,
				Response:           true,
				RecursionAvailable: true,
			})
			builder.EnableCompression()
			if err := builder.StartQuestions(); err != nil {
				t.Errorf("start DNS questions: %v", err)
				return
			}
			if err := builder.Question(question); err != nil {
				t.Errorf("add DNS question: %v", err)
				return
			}
			if err := builder.StartAnswers(); err != nil {
				t.Errorf("start DNS answers: %v", err)
				return
			}
			if question.Type == dnsmessage.TypeA {
				for _, answer := range answers {
					header := dnsmessage.ResourceHeader{
						Name:  question.Name,
						Type:  dnsmessage.TypeA,
						Class: dnsmessage.ClassINET,
						TTL:   60,
					}
					if err := builder.AResource(header, answer); err != nil {
						t.Errorf("add DNS answer: %v", err)
						return
					}
				}
			}
			response, err := builder.Finish()
			if err != nil {
				t.Errorf("finish DNS response: %v", err)
				return
			}
			if _, err := server.WriteTo(response, clientAddr); err != nil {
				return
			}
		}
	}()

	return server.LocalAddr().String()
}
