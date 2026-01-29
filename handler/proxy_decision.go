package handler

import (
	"net"
	"net/http"
	"strings"

	"goProxy/cache"
	"goProxy/config"
	"goProxy/logging"
)

// ProxyDecision обрабатывает логику принятия решения о маршрутизации запросов
type ProxyDecision struct {
	config *config.ProxyConfig
	cache  *cache.CacheManager
	logger *logging.Logger
}

// NewProxyDecision создает новый экземпляр для принятия решений о маршрутизации
func NewProxyDecision(config *config.ProxyConfig, cache *cache.CacheManager) *ProxyDecision {
	logger := logging.NewLogger(config)
	return &ProxyDecision{
		config: config,
		cache:  cache,
		logger: logger,
	}
}

// matchesGlob проверяет, соответствует ли строка glob-паттерну с кешированием
func (d *ProxyDecision) matchesGlob(pattern, s string) bool {
	// Удаляем порт из хоста для сравнения
	hostWithoutPort := s
	if strings.Contains(s, ":") {
		hostParts := strings.Split(s, ":")
		if len(hostParts) == 2 {
			hostWithoutPort = hostParts[0]
		}
	}

	// Проверяем точное совпадение
	if pattern == hostWithoutPort {
		return true
	}

	// Используем кеш для скомпилированных glob-паттернов
	g, err := d.cache.GetGlob(pattern)
	if err != nil {
		return false
	}

	return g.Match(hostWithoutPort)
}

// matchesURLPattern проверяет, соответствует ли полный URL паттерну с кешированием
func (d *ProxyDecision) matchesURLPattern(pattern, url string) bool {
	g, err := d.cache.GetGlob(pattern)
	if err != nil {
		return false
	}

	return g.Match(url)
}

// GetProxyForRequest определяет, какой прокси использовать для данного запроса
func (d *ProxyDecision) GetProxyForRequest(r *http.Request) string {
	host := r.URL.Hostname()

	// Обрабатываем правила сверху вниз
	for _, rule := range d.config.Rules {
		matchesRule := false

		// Парсим строки в списки
		urlRules := config.ParseStringToList(rule.URLs)
		ipRules := config.ParseStringToList(rule.Ips)
		hostRules := config.ParseStringToList(rule.Hosts)

		// Сначала проверяем полные URL паттерны в текущем правиле
		for _, urlRule := range urlRules {
			fullURL := r.URL.String()
			if d.matchesURLPattern(urlRule, fullURL) {
				matchesRule = true
				break
			}
		}

		// Проверяем IP-диапазоны обхода в текущем правиле
		if !matchesRule && len(ipRules) > 0 {
			// Сначала проверяем, является ли хост IP-адресом
			ip := net.ParseIP(host)
			if ip != nil {
				for _, ipRule := range ipRules {
					ipNet, err := d.cache.GetCIDRNet(ipRule)
					if err == nil && ipNet.Contains(ip) {
						matchesRule = true
						break
					}
				}
			} else {
				// Если хост - это доменное имя, разрешаем его в IP-адреса
				ips, err := d.cache.ResolveHost(host)
				if err == nil {
					if d.config.ShouldLog("debug") {
						d.logger.Debug("Resolved host %s to IPs: %v", host, ips)
					}
					for _, resolvedIP := range ips {
						for _, ipRule := range ipRules {
							ipNet, err := d.cache.GetCIDRNet(ipRule)
							if err == nil && ipNet.Contains(resolvedIP) {
								if d.config.ShouldLog("debug") {
									d.logger.Debug("Host %s IP %s matches IP rule %s", host, resolvedIP.String(), ipRule)
								}
								matchesRule = true
								break
							}
						}
						if matchesRule {
							break
						}
					}
				} else {
					if d.config.ShouldLog("debug") {
						d.logger.Debug("Failed to resolve host %s: %v", host, err)
					}
				}
			}
		}

		// Проверяем домены обхода в текущем правиле
		if !matchesRule {
			for _, hostRule := range hostRules {
				if d.matchesGlob(hostRule, host) {
					matchesRule = true
					break
				}
			}
		}

		// Если правило инвертированное, применяем обратную логику
		if rule.Not {
			// Для инвертированного правила: если запрос НЕ соответствует паттернам,
			// то используем прокси из этого правила
			if !matchesRule {
				return rule.Proxy
			}
		} else {
			// Для обычного правила: если запрос соответствует паттернам,
			// то используем прокси из этого правила
			if matchesRule {
				return rule.Proxy
			}
		}
	}

	// Если ни одно правило не сработало, используем глобальный прокси
	return d.config.DefaultProxy
}
