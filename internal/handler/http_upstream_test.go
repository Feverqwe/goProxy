package handler

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/elazarl/goproxy"
)

func TestHTTPProxyAddress(t *testing.T) {
	tests := []struct {
		proxyURL string
		want     string
	}{
		{proxyURL: "http://proxy.example", want: "proxy.example:80"},
		{proxyURL: "https://proxy.example", want: "proxy.example:443"},
		{proxyURL: "https://[2001:db8::1]:8443", want: "[2001:db8::1]:8443"},
	}
	for _, tt := range tests {
		t.Run(tt.proxyURL, func(t *testing.T) {
			parsedURL, err := url.Parse(tt.proxyURL)
			if err != nil {
				t.Fatal(err)
			}
			got, err := httpProxyAddress(parsedURL)
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Fatalf("address = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestHTTPSUpstreamProxyUsesTLS(t *testing.T) {
	target, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	targetResult := make(chan error, 1)
	go func() {
		conn, err := target.Accept()
		if err != nil {
			targetResult <- err
			return
		}
		defer conn.Close()
		request := make([]byte, 4)
		if _, err := io.ReadFull(conn, request); err != nil {
			targetResult <- err
			return
		}
		if string(request) != "ping" {
			targetResult <- fmt.Errorf("payload = %q, want ping", request)
			return
		}
		_, err = conn.Write([]byte("pong"))
		targetResult <- err
	}()

	upstreamHandler := goproxy.NewProxyHttpServer()
	upstreamTransport := http.DefaultTransport.(*http.Transport).Clone()
	upstreamTransport.Proxy = nil
	upstreamHandler.Tr = upstreamTransport
	upstreamHandler.ConnectDial = nil
	upstream := httptest.NewTLSServer(upstreamHandler)
	defer upstream.Close()

	roots := x509.NewCertPool()
	roots.AddCert(upstream.Certificate())
	proxyHandler := newSOCKSTestProxyHandler(t)
	proxyHandler.upstreamProxyTLSConfig = &tls.Config{RootCAs: roots}
	ctx := context.WithValue(context.Background(), proxyURLContextKey, upstream.URL)
	conn, err := proxyHandler.dialContext(ctx, "tcp", target.Addr().String())
	if err != nil {
		t.Fatalf("dial through HTTPS upstream proxy: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	response := make([]byte, 4)
	if _, err := io.ReadFull(conn, response); err != nil {
		t.Fatal(err)
	}
	if string(response) != "pong" {
		t.Fatalf("response = %q, want pong", response)
	}
	if err := <-targetResult; err != nil {
		t.Fatal(err)
	}
}
