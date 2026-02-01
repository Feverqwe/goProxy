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

	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.DialContext = handler.dialContext

	proxyServer.Tr = tr

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

// dialContext реализует DialContext для http.Transport, используя собственную реализацию без зацикливания
func (p *ProxyHandler) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	// Получаем proxyURL из контекста
	proxyURL, ok := ctx.Value(proxyURLContextKey).(string)
	if !ok {
		return nil, fmt.Errorf("proxy URL not found in context")
	}

	// Если URL прокси равен "#" - блокируем соединение
	if proxyURL == "#" {
		return nil, fmt.Errorf("connection blocked by proxy configuration")
	}

	// Если URL прокси пустой - используем прямое соединение
	if proxyURL == "" {
		return net.Dial(network, addr)
	}

	// Парсим URL прокси
	parsedURL, err := url.Parse(proxyURL)
	if err != nil {
		return nil, fmt.Errorf("error parsing proxy URL: %w", err)
	}

	// Создаем dialer в зависимости от типа прокси
	switch parsedURL.Scheme {
	case "socks5", "socks5h":
		// SOCKS5 прокси - используем стандартную реализацию
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
		// HTTP/HTTPS прокси - реализуем HTTP CONNECT для туннелирования
		conn, err := net.Dial("tcp", parsedURL.Host)
		if err != nil {
			return nil, fmt.Errorf("error connecting to HTTP proxy: %w", err)
		}

		// Отправляем CONNECT команду для создания туннеля
		connectReq := fmt.Sprintf("CONNECT %s HTTP/1.1\r\nHost: %s\r\n", addr, addr)

		// Добавляем аутентификацию, если указана в URL прокси
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

		// Читаем ответ прокси с использованием bufio для корректного парсинга HTTP ответа
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
