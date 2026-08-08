package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"testing"
	"time"

	"goProxy/internal/cache"
	"goProxy/internal/config"
	"goProxy/internal/logging"
)

func TestSOCKS5ProxyAddress(t *testing.T) {
	tests := []struct {
		name     string
		proxyURL string
		want     string
	}{
		{name: "default port", proxyURL: "socks5://proxy.example", want: "proxy.example:1080"},
		{name: "explicit port", proxyURL: "socks5://proxy.example:1081", want: "proxy.example:1081"},
		{name: "IPv6", proxyURL: "socks5://[2001:db8::1]:1081", want: "[2001:db8::1]:1081"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsedURL, err := url.Parse(tt.proxyURL)
			if err != nil {
				t.Fatalf("parse URL: %v", err)
			}
			got, err := socks5ProxyAddress(parsedURL)
			if err != nil {
				t.Fatalf("socks5ProxyAddress: %v", err)
			}
			if got != tt.want {
				t.Fatalf("address = %q, want %q", got, tt.want)
			}
		})
	}
}

func newSOCKSTestProxyHandler(t *testing.T) *ProxyHandler {
	t.Helper()
	logger := logging.NewLogger(environmentTestLoggerConfig{})
	t.Cleanup(func() {
		if err := logger.Close(); err != nil {
			t.Errorf("close logger: %v", err)
		}
	})
	cfg := &config.ProxyConfig{
		DefaultProxy: "direct",
		Proxies:      map[string]string{"direct": ""},
	}
	return NewProxyHandler(cfg, cache.NewCacheManager(), logger)
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

	handler := newSOCKSTestProxyHandler(t)
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

	handler := newSOCKSTestProxyHandler(t)
	ctx := context.WithValue(context.Background(), proxyURLContextKey, "socks5://"+listener.Addr().String())
	conn, err := handler.dialContext(ctx, "tcp", "target.example:443")
	if err != nil {
		t.Fatalf("dial through SOCKS5: %v", err)
	}
	defer conn.Close()
	if _, ok := conn.(*net.TCPConn); !ok {
		t.Fatalf("connection type = %T, want native *net.TCPConn", conn)
	}
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
