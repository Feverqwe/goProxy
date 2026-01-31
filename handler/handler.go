package handler

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/elazarl/goproxy"
	"golang.org/x/net/proxy"

	"goProxy/cache"
	"goProxy/config"
	"goProxy/logging"
)

// ProxyHandler handles HTTP and HTTPS requests through proxy
type ProxyHandler struct {
	configManager *config.ConfigManager
	logger        *logging.Logger
	cache         *cache.CacheManager
	decision      *ProxyDecision
	mu            sync.RWMutex
}

// NewProxyHandler создает новый обработчик прокси
func NewProxyHandler(configManager *config.ConfigManager) *ProxyHandler {
	config := configManager.GetConfig()
	logger := logging.NewLogger(config)

	cacheManager := cache.NewCacheManager()

	// Предварительно компилируем паттерны
	cacheManager.PrecompilePatterns(config.GetAllHosts(), config.GetAllURLs(), config.GetAllIps())

	// Создаем компонент для принятия решений о маршрутизации
	decision := NewProxyDecision(config, cacheManager)

	return &ProxyHandler{
		configManager: configManager,
		logger:        logger,
		cache:         cacheManager,
		decision:      decision,
	}
}

// UpdateConfig обновляет конфигурацию обработчика
func (p *ProxyHandler) UpdateConfig(configManager *config.ConfigManager) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.configManager = configManager
	config := configManager.GetConfig()

	// Обновляем логгер с новой конфигурацией
	if err := p.logger.Close(); err != nil {
		p.logger.Error("Close logger error", err)
	} else {
		p.logger = logging.NewLogger(config)
	}

	// Перекомпилируем паттерны с новой конфигурацией (используем существующий кэш)
	p.cache.PrecompilePatterns(config.GetAllHosts(), config.GetAllURLs(), config.GetAllIps())

	// Обновляем компонент принятия решений
	p.decision = NewProxyDecision(config, p.cache)
}

// getProxyDialer возвращает dialer для указанного запроса
func (p *ProxyHandler) getProxyDialer(r *http.Request) (proxy.Dialer, string, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	config := p.configManager.GetConfig()

	// Определяем, какой прокси использовать для запроса
	proxyKey := p.decision.GetProxyForRequest(r)

	// Получаем URL прокси по ключу
	proxyURL, exists := config.Proxies[proxyKey]
	if !exists {
		return nil, proxyKey, fmt.Errorf("proxy key '%s' not found in proxies map", proxyKey)
	}

	// Если URL прокси равен "#" - это специальный блокирующий прокси
	if proxyURL == "#" {
		return nil, proxyKey, nil // Возвращаем nil dialer для блокировки
	}

	// Если URL прокси пустой - используем прямое соединение
	if proxyURL == "" {
		return proxy.Direct, proxyKey, nil
	}

	// Определяем тип прокси на основе URL
	var dialer proxy.Dialer

	if strings.Contains(proxyURL, "://") {
		parsed, err := url.Parse(proxyURL)
		if err != nil {
			return nil, proxyKey, fmt.Errorf("error parsing proxy URL: %w", err)
		}

		switch parsed.Scheme {
		case "socks5", "socks5h":
			// Создаем SOCKS5 прокси
			dialer, err = proxy.SOCKS5("tcp", parsed.Host, nil, proxy.Direct)
			if err != nil {
				return nil, proxyKey, fmt.Errorf("error creating SOCKS5 proxy: %w", err)
			}
		case "http", "https":
			// Создаем HTTP прокси
			dialer, err = proxy.FromURL(parsed, proxy.Direct)
			if err != nil {
				return nil, proxyKey, fmt.Errorf("error creating HTTP proxy: %w", err)
			}
		default:
			return nil, proxyKey, fmt.Errorf("unsupported proxy type: %s", parsed.Scheme)
		}
	} else {
		return nil, proxyKey, fmt.Errorf("proxy URL must include scheme (socks5://, http://, https://) for proxy '%s'", proxyKey)
	}

	return dialer, proxyKey, nil
}

// handleRequestWithGoproxy обрабатывает запрос через goproxy с указанным dialer
func (p *ProxyHandler) handleRequestWithGoproxy(w http.ResponseWriter, r *http.Request, dialer proxy.Dialer) {
	// Создаем goproxy с нашим dialer
	proxyServer := goproxy.NewProxyHttpServer()
	proxyServer.Verbose = false

	// Настраиваем наш логгер для goproxy
	goproxyLogger := logging.NewGoproxyLoggerAdapter(p.logger)
	proxyServer.Logger = goproxyLogger

	// Настраиваем dialer для goproxy с использованием DialContext
	proxyServer.Tr = &http.Transport{
		DialContext:           p.createDialContext(dialer),
		MaxConnsPerHost:       100,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	// Обрабатываем запрос через goproxy
	proxyServer.ServeHTTP(w, r)
}

// createDialContext создает функцию DialContext на основе proxy.Dialer
func (p *ProxyHandler) createDialContext(dialer proxy.Dialer) func(context.Context, string, string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		// Проверяем, поддерживает ли dialer DialContext
		if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
			return contextDialer.DialContext(ctx, network, addr)
		}

		p.logger.Debug("Dialer does not support DialContext")

		// Если dialer не поддерживает DialContext, используем обычный Dial
		// и создаем канал для отмены операции
		type dialResult struct {
			conn net.Conn
			err  error
		}

		resultChan := make(chan dialResult, 1)

		go func() {
			conn, err := dialer.Dial(network, addr)
			resultChan <- dialResult{conn, err}
		}()

		select {
		case <-ctx.Done():
			// Контекст отменен, возвращаем ошибку
			return nil, ctx.Err()
		case result := <-resultChan:
			return result.conn, result.err
		}
	}
}

func (p *ProxyHandler) handleRequest(w http.ResponseWriter, r *http.Request, isHTTPS bool) {
	// Получаем dialer для запроса
	dialer, proxyKey, err := p.getProxyDialer(r)
	if err != nil {
		p.logger.Error("Error getting proxy dialer: %v", err)
		http.Error(w, "Proxy configuration error", http.StatusInternalServerError)
		return
	}

	// Проверяем, является ли прокси блокирующим (значение "#")
	config := p.configManager.GetConfig()
	proxyValue, exists := config.Proxies[proxyKey]
	if exists && proxyValue == "#" {
		target := r.URL.Host
		if isHTTPS {
			target = r.Host
		}
		p.logger.Info("Blocking %s request to %s (proxy '%s')", getRequestType(isHTTPS), target, proxyKey)
		http.Error(w, "Request blocked by proxy configuration", http.StatusForbidden)
		return
	}

	// Проверяем, что proxyKey существует (обработка случая, когда ключ не найден)
	if !exists {
		p.logger.Error("Proxy key '%s' not found in proxies map", proxyKey)
		http.Error(w, "Proxy configuration error", http.StatusInternalServerError)
		return
	}

	// Проверяем, что dialer не nil (для не-блокирующих прокси)
	if dialer == nil {
		p.logger.Error("Invalid proxy configuration for key '%s'", proxyKey)
		http.Error(w, "Proxy configuration error", http.StatusInternalServerError)
		return
	}

	// Логируем тип соединения
	target := r.URL.Host
	if isHTTPS {
		target = r.Host
	}
	if dialer == proxy.Direct {
		p.logger.Info("Direct %s to %s (proxy '%s')", getRequestType(isHTTPS), target, proxyKey)
	} else {
		p.logger.Info("%s to %s via proxy %s", capitalize(getRequestType(isHTTPS)), target, proxyKey)
	}

	// Обрабатываем запрос через goproxy
	p.handleRequestWithGoproxy(w, r, dialer)
}

// getRequestType возвращает строковое представление типа запроса
func getRequestType(isHTTPS bool) string {
	if isHTTPS {
		return "HTTPS CONNECT"
	}
	return "request"
}

// capitalize возвращает строку с первой заглавной буквой
func capitalize(s string) string {
	if len(s) == 0 {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// ServeHTTP обрабатывает HTTP запросы
func (p *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Логируем запрос
	p.logger.Debug("%s %s %s", r.Method, r.URL.String(), r.RemoteAddr)

	// Обрабатываем CONNECT метод для HTTPS
	if r.Method == http.MethodConnect {
		p.handleRequest(w, r, true)
		return
	}

	// Обрабатываем обычные HTTP запросы
	p.handleRequest(w, r, false)
}
