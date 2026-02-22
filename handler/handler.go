package handler

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/elazarl/goproxy"
	"golang.org/x/net/proxy"

	"goProxy/cache"
	"goProxy/config"
	"goProxy/logging"
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

type ProxyHandler struct {
	decision    *ProxyDecision
	proxyServer *goproxy.ProxyHttpServer
	logger      *logging.Logger
	mu          sync.RWMutex
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
	tr.DialContext = handler.dialContext
	proxyServer.Tr = tr

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
	proxyURL, ok := ctx.Value(proxyURLContextKey).(string)
	if !ok {
		return nil, fmt.Errorf("proxy URL not found in context")
	}

	if proxyURL == "#" {
		return nil, fmt.Errorf("connection blocked by proxy configuration")
	}

	dialer := &net.Dialer{
		Timeout:   30 * time.Second,
		KeepAlive: 30 * time.Second,
	}

	if proxyURL == "" {
		p.mu.RLock()
		bindExternalIf := p.decision.config.BindExternalIf
		extIf := p.decision.config.ExternalIf
		extIp4 := p.decision.config.ExternalIp4
		extIp6 := p.decision.config.ExternalIp6
		p.mu.RUnlock()

		if extIp4 != "" || extIp6 != "" {
			host, _, _ := net.SplitHostPort(addr)
			// Используем ваш кеш, чтобы не делать лишних DNS-запросов
			ips, err := p.decision.cache.ResolveHost(host)

			if err == nil && len(ips) > 0 {
				targetIP := ips[0] // Берем первый отрезолвленный IP

				var sourceIP string
				// Определяем, какой внешний IP использовать
				if targetIP.To4() != nil {
					sourceIP = extIp4
				} else {
					sourceIP = extIp6
				}

				if sourceIP != "" {
					// Привязываемся к конкретному IP, порт оставляем 0 (выберет ОС)
					localAddr, err := net.ResolveTCPAddr("tcp", net.JoinHostPort(sourceIP, "0"))
					if err == nil {
						dialer.LocalAddr = localAddr
						p.logger.Debug("Direct connection to %s bound to source IP: %s", addr, sourceIP)
					} else {
						p.logger.Error("Failed to resolve LocalAddr %s: %v", sourceIP, err)
					}
				}
			}
		}

		if bindExternalIf && extIf != "" {
			dialer.Control = func(network, address string, c syscall.RawConn) error {
				return c.Control(func(fd uintptr) {
					err := BindToInterface(fd, extIf)
					if err != nil {
						p.logger.Error("Failed to bind to interface %s: %v", extIf, err)
					} else {
						p.logger.Debug("Dialer bound to interface: %s", extIf)
					}
				})
			}
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
		proxyDialer, err := proxy.SOCKS5("tcp", parsedURL.Host, auth, dialer)
		if err != nil {
			return nil, fmt.Errorf("error creating SOCKS5 dialer: %w", err)
		}
		return proxyDialer.Dial(network, addr)
	case "http", "https":
		conn, err := dialer.DialContext(ctx, "tcp", parsedURL.Host)
		if err != nil {
			return nil, fmt.Errorf("error connecting to HTTP proxy: %w", err)
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

	var transport http.RoundTripper

	if proxyURL == "" {
		transport = http.DefaultTransport
		p.logger.Info("HTTP Direct: %s to %s (rule: '%s', proxy: '%s')", getRequestType(isHTTPS), target, decisionResult.RuleName, decisionResult.Proxy)
	} else {
		transport = &roundTripperWithContext{
			base:     p.proxyServer.Tr,
			proxyURL: proxyURL,
		}
		p.logger.Info("HTTP Proxy: %s to %s via proxy %s (rule: '%s')", capitalize(getRequestType(isHTTPS)), target, decisionResult.Proxy, decisionResult.RuleName)
	}

	return &http.Client{
		Timeout:   30 * time.Second,
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
