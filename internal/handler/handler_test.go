package handler

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"goProxy/internal/cache"
	"goProxy/internal/config"
	"goProxy/internal/logging"
)

type testLoggerConfig struct{}

func (testLoggerConfig) GetAccessLogPath() string { return "" }
func (testLoggerConfig) GetMaxLogSize() int       { return 1 }
func (testLoggerConfig) GetMaxLogFiles() int      { return 1 }
func (testLoggerConfig) GetLogLevelInt() int      { return logging.LogLevelNone }

func newTestProxyHandler(t *testing.T, cfg *config.ProxyConfig) *ProxyHandler {
	t.Helper()

	logger := logging.NewLogger(testLoggerConfig{})
	t.Cleanup(func() {
		if err := logger.Close(); err != nil {
			t.Errorf("close logger: %v", err)
		}
	})

	return NewProxyHandler(cfg, cache.NewCacheManager(), logger)
}

func TestHTTPSUpstreamUsesTLS(t *testing.T) {
	requestTarget := make(chan string, 1)
	proxyServer := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodConnect {
			t.Errorf("method = %s, want CONNECT", r.Method)
		}
		requestTarget <- r.Host
		w.Header().Set("Content-Length", "0")
		w.WriteHeader(http.StatusOK)
	}))
	defer proxyServer.Close()

	cfg := &config.ProxyConfig{
		DefaultProxy: "direct",
		Proxies:      map[string]string{"direct": ""},
	}
	handler := newTestProxyHandler(t, cfg)

	rootCAs := x509.NewCertPool()
	rootCAs.AddCert(proxyServer.Certificate())
	handler.proxyTLSConfig = &tls.Config{RootCAs: rootCAs}

	ctx := context.WithValue(context.Background(), proxyURLContextKey, proxyServer.URL)
	conn, err := handler.dialContext(ctx, "tcp", "target.example:443")
	if err != nil {
		t.Fatalf("dial through HTTPS proxy: %v", err)
	}
	defer conn.Close()

	buffered, ok := conn.(*bufferedConn)
	if !ok {
		t.Fatalf("connection type = %T, want *bufferedConn", conn)
	}
	if _, ok := buffered.Conn.(*tls.Conn); !ok {
		t.Fatalf("underlying connection type = %T, want *tls.Conn", buffered.Conn)
	}

	select {
	case target := <-requestTarget:
		if target != "target.example:443" {
			t.Fatalf("CONNECT target = %q, want target.example:443", target)
		}
	case <-time.After(time.Second):
		t.Fatal("HTTPS proxy did not receive CONNECT")
	}
}

func TestProxyEnvironmentIsIgnored(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("http_proxy", "http://127.0.0.1:1")
	t.Setenv("https_proxy", "http://127.0.0.1:1")

	cfg := &config.ProxyConfig{
		DefaultProxy: "direct",
		Proxies:      map[string]string{"direct": ""},
	}
	handler := newTestProxyHandler(t, cfg)

	if handler.proxyServer.Tr.Proxy != nil {
		t.Fatal("transport still uses an environment proxy")
	}
	if handler.proxyServer.ConnectDial != nil {
		t.Fatal("CONNECT still uses the goproxy environment dialer")
	}

	client, err := handler.GetHTTPClient("https://rules.example/list.txt")
	if err != nil {
		t.Fatalf("create direct HTTP client: %v", err)
	}
	transport, ok := client.Transport.(*roundTripperWithContext)
	if !ok {
		t.Fatalf("direct client transport type = %T, want *roundTripperWithContext", client.Transport)
	}
	if transport.base.Proxy != nil {
		t.Fatal("direct HTTP client still uses an environment proxy")
	}
}

func TestDomainNormalizationInRules(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	configYAML := []byte(`
defaultProxy: direct
proxies:
  direct: ""
  block: "#"
logLevel: none
logFile: ""
rules:
  - name: normalized
    proxy: block
    hosts: "*.EXAMPLE.COM."
`)
	if err := os.WriteFile(configPath, configYAML, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cacheManager := cache.NewCacheManager()
	cfg, err := config.LoadConfig(configPath, cacheManager, nil, true, nil, false)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	t.Cleanup(func() {
		if err := cfg.GetLogger().Close(); err != nil {
			t.Errorf("close logger: %v", err)
		}
	})

	decision := NewProxyDecision(cfg, cacheManager, cfg.GetLogger())
	proxyURL, result, err := decision.GetProxyForHost("API.Example.Com.")
	if err != nil {
		t.Fatalf("get proxy decision: %v", err)
	}
	if proxyURL != "#" || result.RuleName != "normalized" {
		t.Fatalf("decision = (%q, %q), want blocked by normalized rule", proxyURL, result.RuleName)
	}
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

func TestProxyAddressDefaults(t *testing.T) {
	tests := map[string]string{
		"http://proxy.example":       "proxy.example:80",
		"https://proxy.example":      "proxy.example:443",
		"https://[2001:db8::1]:8443": "[2001:db8::1]:8443",
	}

	for rawURL, want := range tests {
		t.Run(rawURL, func(t *testing.T) {
			parsed, err := url.Parse(rawURL)
			if err != nil {
				t.Fatalf("parse URL: %v", err)
			}
			got, err := getProxyAddress(parsed)
			if err != nil {
				t.Fatalf("get proxy address: %v", err)
			}
			if got != want {
				t.Fatalf("address = %q, want %q", got, want)
			}
		})
	}
}

func TestUDPSessionActivityIsConcurrentSafe(t *testing.T) {
	conn, peer := net.Pipe()
	defer peer.Close()
	session := newUDPSession(conn)

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 1_000 {
				if !session.touch() {
					return
				}
				_, _ = session.conn()
			}
		}()
	}
	wg.Wait()

	if session.closeIfExpired(time.Now(), time.Minute) {
		t.Fatal("freshly touched session was closed as expired")
	}
	session.mu.Lock()
	session.lastActive = time.Now().Add(-2 * time.Minute)
	session.mu.Unlock()
	if !session.closeIfExpired(time.Now(), time.Minute) {
		t.Fatal("old session was not closed")
	}
	if session.touch() {
		t.Fatal("closed session became active again")
	}
}

func TestUDPSessionManagerGetOrCreateIsAtomic(t *testing.T) {
	manager := &UDPSessionManager{ttl: time.Minute}

	const workers = 16
	results := make(chan *UDPSession, workers)
	start := make(chan struct{})
	var createCount atomic.Int32
	var peersMu sync.Mutex
	var peers []net.Conn
	createConn := func() (net.Conn, error) {
		createCount.Add(1)
		conn, peer := net.Pipe()
		peersMu.Lock()
		peers = append(peers, peer)
		peersMu.Unlock()
		return conn, nil
	}

	var wg sync.WaitGroup
	for range workers {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			session, err := manager.GetOrCreate("client->target", createConn)
			if err != nil {
				t.Errorf("GetOrCreate: %v", err)
				return
			}
			results <- session
		}()
	}
	close(start)
	wg.Wait()
	close(results)
	defer func() {
		for _, peer := range peers {
			peer.Close()
		}
	}()

	if got := createCount.Load(); got != 1 {
		t.Fatalf("connection creations = %d, want 1", got)
	}
	var winner *UDPSession
	for session := range results {
		if winner == nil {
			winner = session
		}
		if session != winner {
			t.Fatal("concurrent GetOrCreate returned different sessions")
		}
	}
	current, ok := manager.Get("client->target")
	if !ok || current != winner {
		t.Fatal("manager did not retain the single winning session")
	}
	manager.Delete("client->target", winner)
	if _, ok := manager.Get("client->target"); ok {
		t.Fatal("deleted session is still available")
	}

	newConn, newPeer := net.Pipe()
	defer newPeer.Close()
	replacement, loaded := manager.LoadOrStore("client->target", newConn)
	if loaded {
		t.Fatal("replacement session unexpectedly reused an old session")
	}
	manager.Delete("client->target", winner)
	current, ok = manager.Get("client->target")
	if !ok || current != replacement {
		t.Fatal("deleting an old session removed its replacement")
	}
	manager.Delete("client->target", replacement)
}

func TestExpiredLRUReevaluatesProxyDecision(t *testing.T) {
	cfg := &config.ProxyConfig{
		DefaultProxy: "direct",
		Proxies: map[string]string{
			"direct": "",
			"block":  "#",
		},
	}
	logger := logging.NewLogger(testLoggerConfig{})
	t.Cleanup(func() { _ = logger.Close() })
	decision := newProxyDecision(cfg, cache.NewCacheManager(), logger, 20*time.Millisecond)

	proxyURL, _, err := decision.GetProxyForHost("cache.example")
	if err != nil || proxyURL != "" {
		t.Fatalf("initial decision = %q, %v; want direct", proxyURL, err)
	}
	cfg.DefaultProxy = "block"
	proxyURL, _, err = decision.GetProxyForHost("cache.example")
	if err != nil || proxyURL != "" {
		t.Fatalf("cached decision = %q, %v; want direct", proxyURL, err)
	}
	time.Sleep(30 * time.Millisecond)
	proxyURL, _, err = decision.GetProxyForHost("cache.example")
	if err != nil || proxyURL != "#" {
		t.Fatalf("expired decision = %q, %v; want block", proxyURL, err)
	}
}

func TestSOCKS5HandshakeHonorsContext(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = io.Copy(io.Discard, conn)
	}()

	cfg := &config.ProxyConfig{
		DefaultProxy: "direct",
		Proxies:      map[string]string{"direct": ""},
	}
	handler := newTestProxyHandler(t, cfg)
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	ctx = context.WithValue(ctx, proxyURLContextKey, "socks5://"+listener.Addr().String())

	started := time.Now()
	_, err = handler.dialContext(ctx, "tcp", "target.example:443")
	var netErr net.Error
	if !errors.Is(err, context.DeadlineExceeded) && (!errors.As(err, &netErr) || !netErr.Timeout()) {
		t.Fatalf("dial error = %v, want a context-related timeout", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("SOCKS5 handshake cancellation took %s", elapsed)
	}
}

func TestSOCKS5UpstreamConnectionSupportsHalfClose(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()
	serverResult := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			serverResult <- err
			return
		}
		defer conn.Close()

		negotiation := make([]byte, 3)
		if _, err := io.ReadFull(conn, negotiation); err != nil {
			serverResult <- err
			return
		}
		if _, err := conn.Write([]byte{0x05, 0x00}); err != nil {
			serverResult <- err
			return
		}

		requestHeader := make([]byte, 4)
		if _, err := io.ReadFull(conn, requestHeader); err != nil {
			serverResult <- err
			return
		}
		addressLength := 0
		switch requestHeader[3] {
		case 0x01:
			addressLength = net.IPv4len
		case 0x04:
			addressLength = net.IPv6len
		case 0x03:
			length := make([]byte, 1)
			if _, err := io.ReadFull(conn, length); err != nil {
				serverResult <- err
				return
			}
			addressLength = int(length[0])
		default:
			serverResult <- fmt.Errorf("unexpected SOCKS address type %d", requestHeader[3])
			return
		}
		if _, err := io.ReadFull(conn, make([]byte, addressLength+2)); err != nil {
			serverResult <- err
			return
		}
		if _, err := conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0, 0, 0, 0, 0, 0}); err != nil {
			serverResult <- err
			return
		}

		payload, err := io.ReadAll(conn)
		if err != nil {
			serverResult <- err
			return
		}
		if string(payload) != "request" {
			serverResult <- fmt.Errorf("payload = %q, want request", payload)
			return
		}
		if _, err := conn.Write([]byte("response")); err != nil {
			serverResult <- err
			return
		}
		serverResult <- conn.(*net.TCPConn).CloseWrite()
	}()

	cfg := &config.ProxyConfig{
		DefaultProxy: "direct",
		Proxies:      map[string]string{"direct": ""},
	}
	handler := newTestProxyHandler(t, cfg)
	ctx := context.WithValue(context.Background(), proxyURLContextKey, "socks5://"+listener.Addr().String())
	conn, err := handler.dialContext(ctx, "tcp", "target.example:443")
	if err != nil {
		t.Fatalf("dial through SOCKS5: %v", err)
	}
	defer conn.Close()
	closeWriter, ok := conn.(interface{ CloseWrite() error })
	if !ok {
		t.Fatalf("connection type %T does not support CloseWrite", conn)
	}
	if _, err := conn.Write([]byte("request")); err != nil {
		t.Fatalf("write request: %v", err)
	}
	if err := closeWriter.CloseWrite(); err != nil {
		t.Fatalf("half-close SOCKS5 tunnel: %v", err)
	}
	response, err := io.ReadAll(conn)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if string(response) != "response" {
		t.Fatalf("response = %q, want response", response)
	}
	if err := <-serverResult; err != nil {
		t.Fatalf("SOCKS5 server: %v", err)
	}
}

func TestProxyTCPConnectionsPreservesHalfClose(t *testing.T) {
	leftPeer, leftProxy := tcpConnectionPair(t)
	rightProxy, rightPeer := tcpConnectionPair(t)
	defer leftPeer.Close()
	defer leftProxy.Close()
	defer rightProxy.Close()
	defer rightPeer.Close()

	proxyResult := make(chan error, 1)
	go func() {
		proxyResult <- proxyTCPConnections(leftProxy, rightProxy)
	}()

	rightResult := make(chan error, 1)
	go func() {
		request, err := io.ReadAll(rightPeer)
		if err != nil {
			rightResult <- err
			return
		}
		if string(request) != "request" {
			rightResult <- fmt.Errorf("request = %q, want request", request)
			return
		}
		if _, err := rightPeer.Write([]byte("response")); err != nil {
			rightResult <- err
			return
		}
		rightResult <- rightPeer.(*net.TCPConn).CloseWrite()
	}()

	if _, err := leftPeer.Write([]byte("request")); err != nil {
		t.Fatalf("write request: %v", err)
	}
	if err := leftPeer.(*net.TCPConn).CloseWrite(); err != nil {
		t.Fatalf("half-close request: %v", err)
	}
	response, err := io.ReadAll(leftPeer)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	if string(response) != "response" {
		t.Fatalf("response = %q, want response", response)
	}
	if err := <-rightResult; err != nil {
		t.Fatalf("right peer: %v", err)
	}
	if err := <-proxyResult; err != nil {
		t.Fatalf("proxy: %v", err)
	}
}

func tcpConnectionPair(t *testing.T) (net.Conn, net.Conn) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	accepted := make(chan net.Conn, 1)
	acceptErr := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			acceptErr <- err
			return
		}
		accepted <- conn
	}()

	client, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	select {
	case server := <-accepted:
		return client, server
	case err := <-acceptErr:
		client.Close()
		t.Fatalf("accept: %v", err)
		return nil, nil
	}
}
