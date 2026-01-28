package handler

import (
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

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

	var client *http.Client

	// Создаем HTTP клиент с транспортом
	transport := &http.Transport{
		Dial:                  dialer.Dial,
		MaxConnsPerHost:       100,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	client = &http.Client{
		Transport: transport,
		Timeout:   30 * time.Second,
	}

	// Логируем тип соединения
	if dialer == proxy.Direct {
		p.logger.Info("Direct request to %s (proxy '%s')", r.URL.Host, proxyKey)
	} else {
		p.logger.Info("Request to %s via proxy %s", r.URL.Host, proxyKey)
	}

	// Копируем запрос, но изменяем URL для абсолютного пути
	outReq := r.Clone(r.Context())
	outReq.URL.Scheme = "http"
	if r.TLS != nil {
		outReq.URL.Scheme = "https"
	}
	outReq.URL.Host = r.Host

	// Удаляем заголовок Hop-by-hop
	outReq.Header.Del("Proxy-Connection")
	outReq.Header.Del("Proxy-Authenticate")
	outReq.Header.Del("Proxy-Authorization")
	outReq.Header.Del("TE")
	outReq.Header.Del("Trailers")
	outReq.Header.Del("Transfer-Encoding")
	outReq.Header.Del("Upgrade")

	// Отправляем запрос
	resp, err := client.Do(outReq)
	if err != nil {
		http.Error(w, "Error forwarding request", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Копируем заголовки ответа
	for key, values := range resp.Header {
		for _, value := range values {
			w.Header().Add(key, value)
		}
	}

	// Устанавливаем статус код
	w.WriteHeader(resp.StatusCode)

	// Копируем тело ответа
	_, err = io.Copy(w, resp.Body)
	if err != nil {
		p.logger.Error("Error copying response body: %v", err)
	}
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

	// Устанавливаем соединение через dialer
	targetConn, err := dialer.Dial("tcp", r.Host)
	if err != nil {
		p.logger.Error("Error connecting: %v", err)
		http.Error(w, "Error connecting", http.StatusBadGateway)
		return
	}
	defer targetConn.Close()

	// Отправляем клиенту успешный ответ
	w.WriteHeader(http.StatusOK)

	// Получаем соединение с клиентом
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		p.logger.Error("Hijacking not supported")
		http.Error(w, "Hijacking not supported", http.StatusInternalServerError)
		return
	}

	clientConn, _, err := hijacker.Hijack()
	if err != nil {
		p.logger.Error("Error hijacking connection: %v", err)
		http.Error(w, "Error hijacking connection", http.StatusInternalServerError)
		return
	}
	defer clientConn.Close()

	// Начинаем двунаправленную передачу данных
	go func() {
		io.Copy(targetConn, clientConn)
	}()
	io.Copy(clientConn, targetConn)
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
