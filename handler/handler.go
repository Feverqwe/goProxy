package handler

import (
	"fmt"
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

// ProxyHandler обрабатывает HTTP и HTTPS запросы через прокси
type ProxyHandler struct {
	configManager interface {
		GetConfig() *config.ProxyConfig
	}
	logger   *logging.Logger
	cache    *cache.CacheManager
	decision *ProxyDecision
	mu       sync.RWMutex
}

// NewProxyHandler создает новый обработчик прокси
func NewProxyHandler(configManager interface {
	GetConfig() *config.ProxyConfig
}) *ProxyHandler {
	config := configManager.GetConfig()
	logger := logging.NewLogger(config)

	cacheManager, err := cache.NewCacheManager(config.IPCacheSize)
	if err != nil {
		// Use panic for fatal errors during initialization
		panic(err)
	}

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
func (p *ProxyHandler) UpdateConfig(configManager interface {
	GetConfig() *config.ProxyConfig
}) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.configManager = configManager
	config := configManager.GetConfig()

	// Обновляем логгер с новой конфигурацией
	p.logger = logging.NewLogger(config)

	// Создаем новый кэш с новыми настройками
	cacheManager, err := cache.NewCacheManager(config.IPCacheSize)
	if err != nil {
		p.logger.Error("Error creating new cache manager: %v", err)
		return
	}
	p.cache = cacheManager

	// Перекомпилируем паттерны с новой конфигурацией
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
		p.logger.Error("Proxy key '%s' not found in proxies map", proxyKey)
		return nil, proxyKey, nil
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
	proxyType := ""
	if strings.Contains(proxyURL, "://") {
		parsed, err := url.Parse(proxyURL)
		if err == nil {
			proxyType = parsed.Scheme
		}
	}

	// Проверяем, что тип прокси указан
	if proxyType == "" {
		p.logger.Error("Proxy type is required for proxy '%s'. URL must include scheme (socks5://, http://, https://)", proxyKey)
		return nil, proxyKey, fmt.Errorf("proxy type is required for proxy '%s'", proxyKey)
	}

	var dialer proxy.Dialer
	var err error

	switch proxyType {
	case "socks5", "socks5h":
		// Создаем SOCKS5 прокси
		proxyAddr := proxyURL
		if strings.Contains(proxyURL, "://") {
			parsed, err := url.Parse(proxyURL)
			if err == nil {
				proxyAddr = parsed.Host
			}
		}
		dialer, err = proxy.SOCKS5("tcp", proxyAddr, nil, proxy.Direct)
		if err != nil {
			p.logger.Error("Error creating SOCKS5 proxy: %v", err)
			return nil, proxyKey, err
		}
	case "http", "https":
		// Создаем HTTP прокси
		proxyURL, err := url.Parse(proxyURL)
		if err != nil {
			p.logger.Error("Error parsing HTTP proxy URL: %v", err)
			return nil, proxyKey, err
		}
		dialer, err = proxy.FromURL(proxyURL, proxy.Direct)
		if err != nil {
			p.logger.Error("Error creating HTTP proxy: %v", err)
			return nil, proxyKey, err
		}
	default:
		p.logger.Error("Unsupported proxy type: %s", proxyType)
		return nil, proxyKey, nil
	}

	return dialer, proxyKey, nil
}

// handleRequestWithGoproxy обрабатывает запрос через goproxy с указанным dialer
func (p *ProxyHandler) handleRequestWithGoproxy(w http.ResponseWriter, r *http.Request, dialer proxy.Dialer, proxyKey string) {
	// Создаем goproxy с нашим dialer
	proxyServer := goproxy.NewProxyHttpServer()
	proxyServer.Verbose = false

	// Настраиваем dialer для goproxy
	proxyServer.Tr = &http.Transport{
		Dial:                  dialer.Dial,
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

func (p *ProxyHandler) handleHTTP(w http.ResponseWriter, r *http.Request) {
	// Получаем dialer для запроса
	dialer, proxyKey, err := p.getProxyDialer(r)
	if err != nil {
		http.Error(w, "Proxy configuration error", http.StatusInternalServerError)
		return
	}

	// Проверяем, является ли прокси блокирующим (значение "#")
	config := p.configManager.GetConfig()
	proxyValue, exists := config.Proxies[proxyKey]
	if exists && proxyValue == "#" {
		p.logger.Info("Blocking request to %s (proxy '%s')", r.URL.Host, proxyKey)
		http.Error(w, "Request blocked by proxy configuration", http.StatusForbidden)
		return
	}

	// Проверяем, что proxyKey существует (обработка случая, когда ключ не найден)
	if dialer == nil {
		p.logger.Error("Proxy key '%s' not found in proxies map", proxyKey)
		http.Error(w, "Proxy configuration error", http.StatusInternalServerError)
		return
	}

	// Логируем тип соединения
	if dialer == proxy.Direct {
		p.logger.Info("Direct request to %s (proxy '%s')", r.URL.Host, proxyKey)
	} else {
		p.logger.Info("Request to %s via proxy %s", r.URL.Host, proxyKey)
	}

	// Обрабатываем запрос через goproxy
	p.handleRequestWithGoproxy(w, r, dialer, proxyKey)
}

func (p *ProxyHandler) handleHTTPS(w http.ResponseWriter, r *http.Request) {
	// Получаем dialer для запроса
	dialer, proxyKey, err := p.getProxyDialer(r)
	if err != nil {
		http.Error(w, "Proxy configuration error", http.StatusInternalServerError)
		return
	}

	// Проверяем, является ли прокси блокирующим (значение "#")
	config := p.configManager.GetConfig()
	proxyValue, exists := config.Proxies[proxyKey]
	if exists && proxyValue == "#" {
		p.logger.Info("Blocking HTTPS CONNECT to %s (proxy '%s')", r.Host, proxyKey)
		http.Error(w, "Request blocked by proxy configuration", http.StatusForbidden)
		return
	}

	// Проверяем, что proxyKey существует (обработка случая, когда ключ не найден)
	if dialer == nil {
		p.logger.Error("Proxy key '%s' not found in proxies map", proxyKey)
		http.Error(w, "Proxy configuration error", http.StatusInternalServerError)
		return
	}

	// Логируем тип соединения
	if dialer == proxy.Direct {
		p.logger.Info("Direct HTTPS CONNECT to %s (proxy '%s')", r.Host, proxyKey)
	} else {
		p.logger.Info("HTTPS CONNECT to %s via proxy %s", r.Host, proxyKey)
	}

	// Обрабатываем запрос через goproxy (он автоматически обработает CONNECT)
	p.handleRequestWithGoproxy(w, r, dialer, proxyKey)
}

// ServeHTTP обрабатывает HTTP запросы
func (p *ProxyHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Логируем запрос
	p.logger.Debug("%s %s %s", r.Method, r.URL.String(), r.RemoteAddr)

	// Обрабатываем CONNECT метод для HTTPS
	if r.Method == http.MethodConnect {
		p.handleHTTPS(w, r)
		return
	}

	// Обрабатываем обычные HTTP запросы
	p.handleHTTP(w, r)
}
