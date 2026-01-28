package main

import (
	"flag"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"goProxy/config"
	"goProxy/handler"
	"goProxy/logging"
	"goProxy/tray"

	"github.com/skratchdot/open-golang/open"
)

// ConfigManager manages configuration with reload capability
type ConfigManager struct {
	config     *config.ProxyConfig
	configPath string
	mu         sync.RWMutex
}

// NewConfigManager creates a new configuration manager
func NewConfigManager(configPath string) (*ConfigManager, error) {
	config, err := config.LoadConfig(configPath)
	if err != nil {
		return nil, err
	}
	return &ConfigManager{
		config:     config,
		configPath: configPath,
	}, nil
}

// GetConfig returns a thread-safe copy of the configuration
func (cm *ConfigManager) GetConfig() *config.ProxyConfig {
	cm.mu.RLock()
	defer cm.mu.RUnlock()
	return cm.config
}

// ReloadConfig reloads the configuration from file
func (cm *ConfigManager) ReloadConfig() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	newConfig, err := config.LoadConfig(cm.configPath)
	if err != nil {
		return err
	}

	cm.config = newConfig
	return nil
}

// ShouldLog проверяет, нужно ли логировать сообщение в зависимости от уровня
func (cm *ConfigManager) ShouldLog(level string) bool {
	return cm.GetConfig().ShouldLog(level)
}

func main() {
	// Парсим аргументы командной строки
	defaultConfigPath := config.GetConfigPath()
	configPath := flag.String("config", defaultConfigPath, "Path to configuration file")
	flag.Parse()

	// Создаем менеджер конфигурации
	configManager, err := NewConfigManager(*configPath)
	if err != nil {
		// Use standard log for fatal errors before logger is initialized
		panic(err)
	}

	// Создаем логгер
	currentConfig := configManager.GetConfig()
	logger := logging.NewLogger(currentConfig)

	// Создаем обработчик прокси
	proxyHandler := handler.NewProxyHandler(configManager)

	// Создаем HTTP сервер
	server := &http.Server{
		Addr:    currentConfig.ListenAddr,
		Handler: proxyHandler,
	}

	// Логируем информацию о запуске
	logger.Info("Starting proxy server on %s", currentConfig.ListenAddr)
	logger.Info("Default proxy: %s (%s)", currentConfig.GetProxyURL(), currentConfig.GetProxyType())
	logger.Info("Available proxies: %v", currentConfig.Proxies)
	logger.Info("Configuration reload signal: SIGHUP (kill -HUP %d)", os.Getpid())

	// Настраиваем обработку сигналов для перезагрузки конфигурации и завершения
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGHUP, os.Interrupt, syscall.SIGTERM)

	// Создаем трей менеджер
	trayManager := tray.NewTrayManager()

	// Запускаем горутину для обработки сигналов
	go func() {
		for {
			select {
			case sig := <-sigChan:
				switch sig {
				case syscall.SIGHUP:
					logger.Info("Received SIGHUP signal, reloading configuration...")
					if err := configManager.ReloadConfig(); err != nil {
						logger.Error("Error reloading configuration: %v", err)
					} else {
						logger.Info("Configuration reloaded successfully")

						// Обновляем конфигурацию в обработчике
						proxyHandler.UpdateConfig(configManager)
					}
				case os.Interrupt, syscall.SIGTERM:
					logger.Info("Received interrupt signal, shutting down...")
					trayManager.Exit()
					return
				}
			case <-trayManager.GetQuitChan():
				logger.Info("Received quit signal from tray, shutting down...")
				server.Close()
				return
			case <-trayManager.GetReloadChan():
				logger.Info("Received reload config signal from tray, reloading configuration...")
				if err := configManager.ReloadConfig(); err != nil {
					logger.Error("Error reloading configuration: %v", err)
				} else {
					logger.Info("Configuration reloaded successfully")

					// Обновляем конфигурацию в обработчике
					proxyHandler.UpdateConfig(configManager)
				}
			case <-trayManager.GetOpenConfigChan():
				logger.Info("Received open config directory signal from tray")
				openConfigDirectory(*configPath, logger)
			}
		}
	}()

	// Запускаем сервер в горутине
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server error: %v", err)
			// Use panic for fatal errors during server startup
			panic(err)
		}
	}()

	// Запускаем трей менеджер (это блокирующий вызов)
	trayManager.Start()

	// Когда трей завершится, закрываем сервер
	logger.Info("Shutting down proxy server...")
	server.Close()
	logger.Info("Proxy server stopped")
}

// openConfigDirectory opens the directory containing the config file
func openConfigDirectory(configPath string, logger *logging.Logger) {
	configDir := filepath.Dir(configPath)

	// If config path is relative, make it absolute relative to current directory
	if !filepath.IsAbs(configDir) {
		absPath, err := filepath.Abs(configDir)
		if err != nil {
			logger.Error("Error getting absolute path for config directory: %v", err)
			return
		}
		configDir = absPath
	}

	// Ensure the directory exists
	if err := os.MkdirAll(configDir, 0755); err != nil {
		logger.Error("Error creating config directory: %v", err)
		return
	}

	// Open directory using cross-platform open package
	if err := open.Run(configDir); err != nil {
		logger.Error("Error opening config directory: %v", err)
	}
}
