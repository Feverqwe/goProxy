package main

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/txthinking/socks5"
)

func writeRuntimeConfig(t *testing.T, httpAddr, socksAddr string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	contents := fmt.Sprintf(`defaultProxy: direct
proxies:
  direct: ""
listenHttpAddr: %q
listenSocksAddr: %q
logLevel: none
logFile: ""
maxLogSize: 1
maxLogFiles: 1
`, httpAddr, socksAddr)
	if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestRunProxyReturnsHTTPListenerError(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	err = runProxy(writeRuntimeConfig(t, occupied.Addr().String(), ""))
	if err == nil || !strings.Contains(err.Error(), "listen HTTP proxy") {
		t.Fatalf("runProxy error = %v, want HTTP listener error", err)
	}
}

func TestRunProxyReturnsSOCKS5ListenerError(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	err = runProxy(writeRuntimeConfig(t, "", occupied.Addr().String()))
	if err == nil || !strings.Contains(err.Error(), "listen SOCKS5 TCP") {
		t.Fatalf("runProxy error = %v, want SOCKS5 listener error", err)
	}
}

func TestStartHTTPProxyServerReturnsListenError(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	server, _, err := startHTTPProxyServer(occupied.Addr().String(), http.NotFoundHandler())
	if err == nil {
		if server != nil {
			_ = server.Close()
		}
		t.Fatal("expected HTTP listener error for an occupied port")
	}
	if server != nil {
		t.Fatal("HTTP server returned after listener failure")
	}
}

func TestStartSOCKS5ProxyServerReturnsTCPListenError(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()

	server, _, err := startSOCKS5ProxyServer(occupied.Addr().String(), nil)
	if err == nil {
		if server != nil {
			_ = server.Close()
		}
		t.Fatal("expected SOCKS5 TCP listener error for an occupied port")
	}
	if server != nil {
		t.Fatal("SOCKS5 server returned after TCP listener failure")
	}
}

func TestStartSOCKS5ProxyServerClosesTCPWhenUDPListenFails(t *testing.T) {
	udpAddr, err := net.ResolveUDPAddr("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	occupiedUDP, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		t.Fatal(err)
	}
	defer occupiedUDP.Close()
	addr := occupiedUDP.LocalAddr().String()

	server, _, err := startSOCKS5ProxyServer(addr, nil)
	if err == nil {
		if server != nil {
			_ = server.Close()
		}
		t.Fatal("expected SOCKS5 UDP listener error for an occupied port")
	}
	if server != nil {
		t.Fatal("SOCKS5 server returned after UDP listener failure")
	}

	tcpListener, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("TCP listener was not released after UDP failure: %v", err)
	}
	_ = tcpListener.Close()
}

func TestStartedSOCKS5ProxyAcceptsTCPConnections(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	go func() {
		conn, acceptErr := target.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		request := make([]byte, 4)
		if _, readErr := io.ReadFull(conn, request); readErr == nil && string(request) == "ping" {
			_, _ = conn.Write([]byte("pong"))
		}
	}()

	reserved, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	proxyAddr := reserved.Addr().String()
	_ = reserved.Close()

	server, serveErr, err := startSOCKS5ProxyServer(proxyAddr, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()

	client, err := socks5.NewClient(proxyAddr, "", "", 3, 3)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := client.Dial("tcp", target.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(3 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 4)
	if _, err := io.ReadFull(conn, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != "pong" {
		t.Fatalf("unexpected SOCKS5 response %q", response)
	}

	if err := server.Close(); err != nil {
		t.Fatal(err)
	}
	if err := <-serveErr; err != nil {
		t.Fatalf("SOCKS5 server returned an error after normal close: %v", err)
	}
}
