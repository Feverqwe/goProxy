package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/elazarl/goproxy"

	"goProxy/cache"
	"goProxy/config"
	"goProxy/logging"
)

// contextKey is a type for context keys to avoid collisions
type contextKey string

// proxyURLContextKey is the key used to store proxyURL in the request context
const proxyURLContextKey contextKey = "proxyURL"

// ProxyHandler handles HTTP and HTTPS requests through proxy
type ProxyHandler struct {
	configManager *config.ConfigManager
	logger        *logging.Logger
	cache         *cache.CacheManager
	decision      *ProxyDecision
	proxyServer   *goproxy.ProxyHttpServer
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

	// Создаем единственный экземпляр goproxy
	proxyServer := goproxy.NewProxyHttpServer()
	proxyServer.Verbose = false

	// Настраиваем наш логгер для goproxy
	goproxyLogger := logging.NewGoproxyLoggerAdapter(logger)
	proxyServer.Logger = goproxyLogger

	handler := &ProxyHandler{
		configManager: configManager,
		logger:        logger,
		cache:         cacheManager,
		decision:      decision,
		proxyServer:   proxyServer,
	}

	// Настраиваем Transport с Proxy функцией
	proxyServer.Tr = &http.Transport{
		Proxy:                 handler.getProxyURL,
		MaxConnsPerHost:       100,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}

	return handler
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

	// Обновляем логгер для goproxy
	goproxyLogger := logging.NewGoproxyLoggerAdapter(p.logger)
	p.proxyServer.Logger = goproxyLogger
}

// getProxyURL возвращает URL прокси для указанного запроса
func (p *ProxyHandler) getProxyURL(r *http.Request) (*url.URL, error) {
	p.mu.RLock()
	defer p.mu.RUnlock()

	// Получаем proxyURL из контекста запроса
	proxyURL, ok := r.Context().Value(proxyURLContextKey).(string)
	if !ok {
		return nil, fmt.Errorf("proxy URL not found in request context")
	}

	// Если URL прокси равен "#" - это специальный блокирующий прокси
	if proxyURL == "#" {
		return nil, nil // Возвращаем nil для блокировки
	}

	// Если URL прокси пустой - используем прямое соединение (nil означает прямой доступ)
	if proxyURL == "" {
		return nil, nil
	}

	// Парсим URL прокси
	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("error parsing proxy URL: %w", err)
	}

	return parsedURL, nil
}

func (p *ProxyHandler) handleRequest(w http.ResponseWriter, r *http.Request, isHTTPS bool) {
	// Получаем proxyKey для запроса
	p.mu.RLock()
	config := p.configManager.GetConfig()
	proxyKey := p.decision.GetProxyForRequest(r)
	p.mu.RUnlock()

	// Получаем proxyURL по ключу
	proxyURL, exists := config.Proxies[proxyKey]
	if !exists {
		p.logger.Error("Proxy key '%s' not found in proxies map", proxyKey)
		http.Error(w, "Proxy configuration error", http.StatusInternalServerError)
		return
	}

	// Проверяем, является ли прокси блокирующим (значение "#")
	if proxyURL == "#" {
		target := r.URL.Host
		if isHTTPS {
			target = r.Host
		}
		p.logger.Info("Blocking %s request to %s (proxy '%s')", getRequestType(isHTTPS), target, proxyKey)
		http.Error(w, "Request blocked by proxy configuration", http.StatusForbidden)
		return
	}

	// Логируем тип соединения
	target := r.URL.Host
	if isHTTPS {
		target = r.Host
	}
	if proxyURL == "" {
		p.logger.Info("Direct %s to %s (proxy '%s')", getRequestType(isHTTPS), target, proxyKey)
	} else {
		p.logger.Info("%s to %s via proxy %s", capitalize(getRequestType(isHTTPS)), target, proxyKey)
	}

	// Создаем новый контекст с proxyURL и обновляем запрос
	ctx := context.WithValue(r.Context(), proxyURLContextKey, proxyURL)
	r = r.WithContext(ctx)

	p.proxyServer.ServeHTTP(w, r)
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

	isHTTPS := r.Method == http.MethodConnect

	p.handleRequest(w, r, isHTTPS)
}
