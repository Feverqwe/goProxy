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

type capturingDialer struct {
	dialer *net.Dialer
	mu     sync.Mutex
	conn   net.Conn
}

func (d *capturingDialer) Dial(network, address string) (net.Conn, error) {
	return d.DialContext(context.Background(), network, address)
}

func (d *capturingDialer) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	conn, err := d.dialer.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	d.mu.Lock()
	d.conn = conn
	d.mu.Unlock()
	return conn, nil
}

func (d *capturingDialer) connection() net.Conn {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.conn
}

type halfCloseConn struct {
	net.Conn
	halfCloser net.Conn
}

func (c *halfCloseConn) CloseWrite() error {
	conn, ok := c.halfCloser.(interface{ CloseWrite() error })
	if !ok {
		return fmt.Errorf("connection type %T does not support CloseWrite", c.halfCloser)
	}
	return conn.CloseWrite()
}

func (c *halfCloseConn) CloseRead() error {
	conn, ok := c.halfCloser.(interface{ CloseRead() error })
	if !ok {
		return nil
	}
	return conn.CloseRead()
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	return c.reader.Read(p)
}

func (c *bufferedConn) CloseWrite() error {
	conn, ok := c.Conn.(interface{ CloseWrite() error })
	if !ok {
		return fmt.Errorf("connection type %T does not support CloseWrite", c.Conn)
	}
	return conn.CloseWrite()
}

func (c *bufferedConn) CloseRead() error {
	conn, ok := c.Conn.(interface{ CloseRead() error })
	if !ok {
		return nil
	}
	return conn.CloseRead()
}

type ProxyHandler struct {
	decision       *ProxyDecision
	proxyServer    *goproxy.ProxyHttpServer
	proxyTLSConfig *tls.Config
	logger         *logging.Logger
	mu             sync.RWMutex
}

func NewProxyHandler(config *config.ProxyConfig, cacheManager *cache.CacheManager, logger *logging.Logger) *ProxyHandler {
	decision := NewProxyDecision(config, cacheManager, logger)

	proxyServer := goproxy.NewProxyHttpServer()
	proxyServer.Verbose = false

	goproxyLogger := logging.NewGoproxyLoggerAdapter(logger)
	proxyServer.Logger = goproxyLogger

	handler := &ProxyHandler{
		decision:       decision,
		proxyServer:    proxyServer,
		proxyTLSConfig: &tls.Config{},
		logger:         logger,
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

	dialer := &net.Dialer{
		Timeout:   10 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	if proxyURL == "" {
		extIp4 := currentDecision.config.ExternalIp4
		extIp6 := currentDecision.config.ExternalIp6
		extDns := currentDecision.config.ExternalDns

		if extIp4 != "" || extIp6 != "" || extDns != "" {
			host, port, err := net.SplitHostPort(addr)
			if err != nil {
				return nil, fmt.Errorf("invalid address format %s: %w", addr, err)
			}

			ips, err := currentDecision.cache.ResolveExternalHost(host, extDns, func(ips []net.IP) string {
				return getSourceIpByIps(ips, extIp4, extIp6)
			})
			if err != nil {
				p.logger.Error("DNS Resolve Error for %s: %v", host, err)
				return nil, err
			}

			if len(ips) == 0 {
				return nil, fmt.Errorf("no IP addresses found for host: %s", host)
			}

			targetIP, sourceIP := getTargetAndSourceIp(ips, extIp4, extIp6)
			if targetIP == nil {
				return nil, fmt.Errorf("no IP addresses for %s are compatible with the configured source IPs", host)
			}
			if sourceIP != "" {
				localAddr, err := net.ResolveTCPAddr("tcp", net.JoinHostPort(sourceIP, "0"))
				if err != nil {
					return nil, fmt.Errorf("invalid TCP source IP %s: %w", sourceIP, err)
				}
				dialer.LocalAddr = localAddr
				p.logger.Debug("Direct connection to %s bound to source IP: %s", addr, sourceIP)
			}

			targetAddr := net.JoinHostPort(targetIP.String(), port)
			return dialer.DialContext(ctx, network, targetAddr)
		}

		return dialer.DialContext(ctx, network, addr)
	}

	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("error parsing proxy URL: %w", err)
	}

	switch parsedURL.Scheme {
	case "socks5", "socks5h":
		var auth *proxy.Auth
		if parsedURL.User != nil {
			auth = &proxy.Auth{
				User: parsedURL.User.Username(),
			}
			if password, ok := parsedURL.User.Password(); ok {
				auth.Password = password
			}
		}
		capturingDialer := &capturingDialer{dialer: dialer}
		proxyDialer, err := proxy.SOCKS5("tcp", parsedURL.Host, auth, capturingDialer)
		if err != nil {
			return nil, fmt.Errorf("error creating SOCKS5 dialer: %w", err)
		}
		contextDialer, ok := proxyDialer.(proxy.ContextDialer)
		if !ok {
			return nil, fmt.Errorf("SOCKS5 dialer does not support context cancellation")
		}
		dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		conn, err := contextDialer.DialContext(dialCtx, network, addr)
		if err != nil {
			return nil, err
		}
		rawConn := capturingDialer.connection()
		if rawConn == nil {
			conn.Close()
			return nil, fmt.Errorf("SOCKS5 dialer did not expose its transport connection")
		}
		return &halfCloseConn{Conn: conn, halfCloser: rawConn}, nil
	case "http", "https":
		proxyAddr, err := getProxyAddress(parsedURL)
		if err != nil {
			return nil, err
		}

		var conn net.Conn
		if parsedURL.Scheme == "https" {
			tlsConfig := p.proxyTLSConfig.Clone()
			tlsConfig.ServerName = parsedURL.Hostname()
			tlsDialer := &tls.Dialer{
				NetDialer: dialer,
				Config:    tlsConfig,
			}
			conn, err = tlsDialer.DialContext(ctx, "tcp", proxyAddr)
		} else {
			conn, err = dialer.DialContext(ctx, "tcp", proxyAddr)
		}
		if err != nil {
			return nil, fmt.Errorf("error connecting to %s proxy: %w", strings.ToUpper(parsedURL.Scheme), err)
		}

		closeConn := true
		defer func() {
			if closeConn {
				conn.Close()
			}
		}()

		conn.SetDeadline(time.Now().Add(10 * time.Second))

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

		conn.SetDeadline(time.Time{})

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
