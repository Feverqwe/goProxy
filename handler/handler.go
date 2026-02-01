package handler

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"

	"github.com/elazarl/goproxy"
	"golang.org/x/net/proxy"

	"goProxy/cache"
	"goProxy/config"
	"goProxy/logging"
)

type contextKey string

const proxyURLContextKey contextKey = "proxyURL"

type ProxyHandler struct {
	configManager *config.ConfigManager
	logger        *logging.Logger
	cache         *cache.CacheManager
	decision      *ProxyDecision
	proxyServer   *goproxy.ProxyHttpServer
	mu            sync.RWMutex
}

func NewProxyHandler(configManager *config.ConfigManager) *ProxyHandler {
	config := configManager.GetConfig()
	logger := logging.NewLogger(config)

	cacheManager := cache.NewCacheManager()

	cacheManager.PrecompilePatterns(config.GetAllHosts(), config.GetAllURLs(), config.GetAllIps())

	decision := NewProxyDecision(config, cacheManager)

	proxyServer := goproxy.NewProxyHttpServer()
	proxyServer.Verbose = false

	goproxyLogger := logging.NewGoproxyLoggerAdapter(logger)
	proxyServer.Logger = goproxyLogger

	handler := &ProxyHandler{
		configManager: configManager,
		logger:        logger,
		cache:         cacheManager,
		decision:      decision,
		proxyServer:   proxyServer,
	}

	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = handler.dialContext

	proxyServer.Tr = tr

	return handler
}

func (p *ProxyHandler) UpdateConfig(configManager *config.ConfigManager) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.configManager = configManager
	config := configManager.GetConfig()

	if err := p.logger.Close(); err != nil {
		p.logger.Error("Close logger error", err)
	} else {
		p.logger = logging.NewLogger(config)
	}

	p.cache.PrecompilePatterns(config.GetAllHosts(), config.GetAllURLs(), config.GetAllIps())

	p.decision = NewProxyDecision(config, p.cache)

	goproxyLogger := logging.NewGoproxyLoggerAdapter(p.logger)
	p.proxyServer.Logger = goproxyLogger
}

func (p *ProxyHandler) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {

	proxyURL, ok := ctx.Value(proxyURLContextKey).(string)
	if !ok {
		return nil, fmt.Errorf("proxy URL not found in context")
	}

	if proxyURL == "#" {
		return nil, fmt.Errorf("connection blocked by proxy configuration")
	}

	if proxyURL == "" {
		return net.Dial(network, addr)
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
		dialer, err := proxy.SOCKS5("tcp", parsedURL.Host, auth, proxy.Direct)
		if err != nil {
			return nil, fmt.Errorf("error creating SOCKS5 dialer: %w", err)
		}
		return dialer.Dial(network, addr)
	case "http", "https":

		conn, err := net.Dial("tcp", parsedURL.Host)
		if err != nil {
			return nil, fmt.Errorf("error connecting to HTTP proxy: %w", err)
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
			conn.Close()
			return nil, fmt.Errorf("error sending CONNECT request: %w", err)
		}

		reader := bufio.NewReader(conn)
		resp, err := http.ReadResponse(reader, nil)
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("error reading proxy response: %w", err)
		}
		resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			conn.Close()
			return nil, fmt.Errorf("proxy CONNECT failed with status: %d %s", resp.StatusCode, resp.Status)
		}

		return conn, nil
	default:
		return nil, fmt.Errorf("unsupported proxy scheme: %s", parsedURL.Scheme)
	}
}

func (p *ProxyHandler) handleRequest(w http.ResponseWriter, r *http.Request, isHTTPS bool) {

	p.mu.RLock()
	config := p.configManager.GetConfig()
	proxyKey := p.decision.GetProxyForRequest(r)
	p.mu.RUnlock()

	proxyURL, exists := config.Proxies[proxyKey]
	if !exists {
		p.logger.Error("Proxy key '%s' not found in proxies map", proxyKey)
		http.Error(w, "Proxy configuration error", http.StatusInternalServerError)
		return
	}

	if proxyURL == "#" {
		target := r.URL.Host
		if isHTTPS {
			target = r.Host
		}
		p.logger.Info("Blocking %s request to %s (proxy '%s')", getRequestType(isHTTPS), target, proxyKey)
		http.Error(w, "Request blocked by proxy configuration", http.StatusForbidden)
		return
	}

	target := r.URL.Host
	if isHTTPS {
		target = r.Host
	}
	if proxyURL == "" {
		p.logger.Info("Direct %s to %s (proxy '%s')", getRequestType(isHTTPS), target, proxyKey)
	} else {
		p.logger.Info("%s to %s via proxy %s", capitalize(getRequestType(isHTTPS)), target, proxyKey)
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

func (p *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {

	p.logger.Debug("%s %s %s", r.Method, r.URL.String(), r.RemoteAddr)

	isHTTPS := r.Method == http.MethodConnect

	p.handleRequest(w, r, isHTTPS)
}
