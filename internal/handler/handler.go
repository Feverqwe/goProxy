package handler

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/elazarl/goproxy"
	"golang.org/x/net/proxy"

	"goProxy/internal/cache"
	"goProxy/internal/config"
	"goProxy/internal/logging"
)

type contextKey string

const proxyURLContextKey contextKey = "proxyURL"

type bufferedConn struct {
	net.Conn
	reader io.Reader
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func (c *bufferedConn) CloseWrite() error {
	if conn, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return conn.CloseWrite()
	}
	return fmt.Errorf("connection type %T does not support CloseWrite", c.Conn)
}

func (c *bufferedConn) CloseRead() error {
	if conn, ok := c.Conn.(interface{ CloseRead() error }); ok {
		return conn.CloseRead()
	}
	return nil
}

// socks5DialerWithConn matches the SOCKS5 implementation returned by
// proxy.FromURL and lets the handshake run on a native TCP connection.
type socks5DialerWithConn interface {
	DialWithConn(context.Context, net.Conn, string, string) (net.Addr, error)
}

type ProxyHandler struct {
	decision               *ProxyDecision
	proxyServer            *goproxy.ProxyHttpServer
	logger                 *logging.Logger
	upstreamProxyTLSConfig *tls.Config
	mu                     sync.RWMutex
}

func NewProxyHandler(config *config.ProxyConfig, cacheManager *cache.CacheManager, logger *logging.Logger) *ProxyHandler {
	decision := NewProxyDecision(config, cacheManager, logger)

	proxyServer := goproxy.NewProxyHttpServer()
	proxyServer.Verbose = false

	goproxyLogger := logging.NewGoproxyLoggerAdapter(logger)
	proxyServer.Logger = goproxyLogger

	handler := &ProxyHandler{
		decision:    decision,
		proxyServer: proxyServer,
		logger:      logger,
	}

	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	tr.MaxIdleConns = 500
	tr.MaxIdleConnsPerHost = 100
	tr.IdleConnTimeout = 90 * time.Second
	tr.ResponseHeaderTimeout = 10 * time.Second
	tr.DialContext = handler.dialContext
	proxyServer.Tr = tr
	// NewProxyHttpServer may initialize ConnectDial from HTTPS_PROXY. Routing
	// decisions must always go through dialContext instead.
	proxyServer.ConnectDial = nil

	return handler
}

func (p *ProxyHandler) UpdateConfig(config *config.ProxyConfig, cache *cache.CacheManager) {
	goproxyLogger := logging.NewGoproxyLoggerAdapter(p.logger)
	decision := NewProxyDecision(config, cache, p.logger)

	p.mu.Lock()
	p.decision = decision
	p.proxyServer.Logger = goproxyLogger
	p.mu.Unlock()
}

func (p *ProxyHandler) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	p.mu.RLock()
	currentDecision := p.decision
	p.mu.RUnlock()

	proxyURL, ok := ctx.Value(proxyURLContextKey).(string)
	if !ok {
		return nil, fmt.Errorf("proxy URL not found in context")
	}

	if proxyURL == "#" {
		return nil, fmt.Errorf("connection blocked by proxy configuration")
	}

	if proxyURL == "" {
		return newDirectRouteDialer(currentDecision, p.logger).DialContext(ctx, network, addr)
	}

	dialer := newTCPDialer(nil, currentDecision.selfGuard)

	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("error parsing proxy URL: %w", err)
	}

	switch parsedURL.Scheme {
	case "socks5", "socks5h":
		proxyDialer, err := proxy.FromURL(parsedURL, dialer)
		if err != nil {
			return nil, fmt.Errorf("error creating SOCKS5 dialer: %w", err)
		}
		connectionDialer, ok := proxyDialer.(socks5DialerWithConn)
		if !ok {
			return nil, fmt.Errorf("SOCKS5 dialer does not support handshaking an existing connection")
		}
		proxyAddr, err := socks5ProxyAddress(parsedURL)
		if err != nil {
			return nil, err
		}

		dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		conn, err := dialer.DialContext(dialCtx, "tcp", proxyAddr)
		if err != nil {
			return nil, fmt.Errorf("error connecting to SOCKS5 proxy: %w", err)
		}
		closeConn := true
		defer func() {
			if closeConn {
				conn.Close()
			}
		}()
		if _, err := connectionDialer.DialWithConn(dialCtx, conn, network, addr); err != nil {
			return nil, err
		}
		closeConn = false
		return conn, nil
	case "http", "https":
		proxyAddr, err := httpProxyAddress(parsedURL)
		if err != nil {
			return nil, err
		}
		conn, err := dialer.DialContext(ctx, "tcp", proxyAddr)
		if err != nil {
			return nil, fmt.Errorf("error connecting to HTTP proxy: %w", err)
		}

		closeConn := true
		defer func() {
			if closeConn {
				conn.Close()
			}
		}()

		if err := conn.SetDeadline(time.Now().Add(10 * time.Second)); err != nil {
			return nil, fmt.Errorf("error setting HTTP proxy deadline: %w", err)
		}

		if parsedURL.Scheme == "https" {
			tlsConfig := &tls.Config{ServerName: parsedURL.Hostname()}
			if p.upstreamProxyTLSConfig != nil {
				tlsConfig = p.upstreamProxyTLSConfig.Clone()
				tlsConfig.ServerName = parsedURL.Hostname()
			}
			tlsConn := tls.Client(conn, tlsConfig)
			conn = tlsConn
			if err := tlsConn.HandshakeContext(ctx); err != nil {
				return nil, fmt.Errorf("error establishing TLS with HTTPS proxy: %w", err)
			}
		}

		connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", addr, addr)

		if parsedURL.User != nil {
			username := parsedURL.User.Username()
			password, _ := parsedURL.User.Password()
			credentials := base64.StdEncoding.EncodeToString([]byte(username + ":" + password))
			connectReq += fmt.Sprintf("Proxy-Authorization: Basic %s\r\n", credentials)
		}

		connectReq += "\r\n"

		if _, err := conn.Write([]byte(connectReq)); err != nil {
			return nil, fmt.Errorf("error sending CONNECT request: %w", err)
		}

		reader := bufio.NewReader(conn)
		resp, err := http.ReadResponse(reader, nil)
		if err != nil {
			return nil, fmt.Errorf("error reading proxy response: %w", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("proxy CONNECT failed with status: %d %s", resp.StatusCode, resp.Status)
		}

		if err := conn.SetDeadline(time.Time{}); err != nil {
			return nil, fmt.Errorf("error clearing HTTP proxy deadline: %w", err)
		}

		closeConn = false
		return &bufferedConn{
			Conn:   conn,
			reader: io.MultiReader(reader, conn),
		}, nil
	default:
		return nil, fmt.Errorf("unsupported proxy scheme: %s", parsedURL.Scheme)
	}
}

func (p *ProxyHandler) handleRequest(w http.ResponseWriter, r *http.Request, isHTTPS bool) {
	p.mu.RLock()
	currentDecision := p.decision
	p.mu.RUnlock()

	targetHost := r.URL.Host
	if isHTTPS {
		targetHost = r.Host
	}

	target := targetHost
	if h, _, err := net.SplitHostPort(targetHost); err == nil {
		target = h
	}

	proxyURL, decisionResult, err := currentDecision.GetProxyForHost(target)

	if err != nil {
		p.logger.Error("HTTP Error getting proxy decision: %v", err)
		http.Error(w, "Proxy configuration error", http.StatusInternalServerError)
		return
	}

	if proxyURL == "#" {
		p.logger.Info("HTTP Blocking: %s request to %s (rule: '%s', proxy: '%s')", getRequestType(isHTTPS), target, decisionResult.RuleName, decisionResult.Proxy)
		http.Error(w, "Request blocked by proxy configuration", http.StatusForbidden)
		return
	}

	if proxyURL == "" {
		p.logger.Info("HTTP Direct: %s to %s (rule: '%s', proxy: '%s')", getRequestType(isHTTPS), target, decisionResult.RuleName, decisionResult.Proxy)
	} else {
		p.logger.Info("HTTP Proxy: %s to %s via proxy %s (rule: '%s')", capitalize(getRequestType(isHTTPS)), target, decisionResult.Proxy, decisionResult.RuleName)
	}

	ctx := context.WithValue(r.Context(), proxyURLContextKey, proxyURL)
	r = r.WithContext(ctx)

	p.proxyServer.ServeHTTP(w, r)
}

func getRequestType(isHTTPS bool) string {
	if isHTTPS {
		return "HTTPS CONNECT"
	}
	return "request"
}

func capitalize(s string) string {
	if len(s) == 0 {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func (p *ProxyHandler) GetHTTPClient(targetURL string) (*http.Client, error) {
	p.mu.RLock()
	currentDecision := p.decision
	p.mu.RUnlock()

	parsedURL, err := url.Parse(targetURL)
	if err != nil {
		return nil, fmt.Errorf("error parsing URL: %w", err)
	}

	target := parsedURL.Hostname()
	isHTTPS := parsedURL.Scheme == "https"

	proxyURL, decisionResult, err := currentDecision.GetProxyForHost(target)
	if err != nil {
		return nil, fmt.Errorf("failed to get proxy decision: %v", err)
	}

	if proxyURL == "#" {
		return nil, fmt.Errorf("request blocked by proxy configuration")
	}

	transport := &roundTripperWithContext{
		base:     p.proxyServer.Tr,
		proxyURL: proxyURL,
	}

	if proxyURL == "" {
		p.logger.Info("HTTP Direct: %s to %s (rule: '%s', proxy: '%s')", getRequestType(isHTTPS), target, decisionResult.RuleName, decisionResult.Proxy)
	} else {
		p.logger.Info("HTTP Proxy: %s to %s via proxy %s (rule: '%s')", capitalize(getRequestType(isHTTPS)), target, decisionResult.Proxy, decisionResult.RuleName)
	}

	return &http.Client{
		Timeout:   10 * time.Second,
		Transport: transport,
	}, nil
}

type roundTripperWithContext struct {
	base     *http.Transport
	proxyURL string
}

func (rt *roundTripperWithContext) RoundTrip(req *http.Request) (*http.Response, error) {
	ctx := context.WithValue(req.Context(), proxyURLContextKey, rt.proxyURL)
	return rt.base.RoundTrip(req.WithContext(ctx))
}

func (p *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p.logger.Debug("HTTP %s %s %s", r.Method, r.URL.String(), r.RemoteAddr)

	isHTTPS := r.Method == http.MethodConnect

	p.handleRequest(w, r, isHTTPS)
}
