package main

import (
	"flag"
	"fmt"
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

func main() {
	// Парсим аргументы командной строки
	defaultConfigPath := config.GetConfigPath()
	configPath := flag.String("config", defaultConfigPath, "Path to configuration file")
	versionFlag := flag.Bool("version", false, "Display version information")
	flag.Parse()

	// Если запрошена версия, показываем и выходим
	if *versionFlag {
		fmt.Println(GetVersion())
		fmt.Println(GetBuildInfo())
		return
	}

	// Создаем менеджер конфигурации
	configManager, err := config.NewConfigManager(*configPath)
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

	// Переменные для управления сервером
	serverMutex := &sync.Mutex{}
	currentServer := server
	currentListenAddr := currentConfig.ListenAddr

	// Логируем информацию о запуске
	logger.Info("Starting proxy server on %s", currentConfig.ListenAddr)
	logger.Info("Default proxy: %s", currentConfig.GetProxyURL())
	logger.Info("Available proxies: %v", currentConfig.Proxies)
	logger.Info("Configuration reload signal: SIGHUP (kill -HUP %d)", os.Getpid())

	// Настраиваем обработку сигналов для перезагрузки конфигурации и завершения
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGHUP, os.Interrupt, syscall.SIGTERM)

	// Создаем трей менеджер
	trayManager := tray.NewTrayManager()

	// Функция для перезапуска сервера при изменении адреса
	restartServerIfAddressChanged := func() {
		serverMutex.Lock()
		defer serverMutex.Unlock()

		newConfig := configManager.GetConfig()
		newListenAddr := newConfig.ListenAddr

		// Проверяем, изменился ли адрес прослушивания
		if newListenAddr != currentListenAddr {
			logger.Info("Listen address changed from '%s' to '%s', restarting server...", currentListenAddr, newListenAddr)

			// Останавливаем текущий сервер
			if err := currentServer.Close(); err != nil {
				logger.Error("Error closing old server: %v", err)
			}

			// Создаем новый сервер с новым адресом
			newServer := &http.Server{
				Addr:    newListenAddr,
				Handler: proxyHandler,
			}

			// Запускаем новый сервер в горутине
			go func() {
				logger.Info("Starting new server on %s", newListenAddr)
				if err := newServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					logger.Error("New server error: %v", err)
					// Use panic for fatal errors during server startup
					panic(err)
				}
			}()

			// Обновляем текущие переменные
			currentServer = newServer
			currentListenAddr = newListenAddr
		} else {
			logger.Debug("Listen address unchanged (%s), no server restart needed", currentListenAddr)
		}
	}

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

						// Проверяем и перезапускаем сервер при изменении адреса
						restartServerIfAddressChanged()
					}
				case os.Interrupt, syscall.SIGTERM:
					logger.Info("Received interrupt signal, shutting down...")
					trayManager.Exit()
					return
				}
			case <-trayManager.GetQuitChan():
				serverMutex.Lock()
				if err := currentServer.Close(); err != nil {
					logger.Error("Error closing server: %v", err)
				}
				serverMutex.Unlock()
				return
			case <-trayManager.GetReloadChan():
				if err := configManager.ReloadConfig(); err != nil {
					logger.Error("Error reloading configuration: %v", err)
				} else {
					logger.Info("Configuration reloaded successfully")

					// Обновляем конфигурацию в обработчике
					proxyHandler.UpdateConfig(configManager)

					// Проверяем и перезапускаем сервер при изменении адреса
					restartServerIfAddressChanged()
				}
			case <-trayManager.GetOpenConfigChan():
				openConfigDirectory(*configPath, logger)
			}
		}
	}()

	// Запускаем сервер в горутине
	go func() {
		if err := currentServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server error: %v", err)
			// Use panic for fatal errors during server startup
			panic(err)
		}
	}()

	// Запускаем трей менеджер (это блокирующий вызов)
	trayManager.Start()

	// Когда трей завершится, корректно закрываем сервер
	logger.Info("Shutting down proxy server...")
	serverMutex.Lock()
	if err := currentServer.Close(); err != nil {
		logger.Error("Error closing server: %v", err)
	}
	serverMutex.Unlock()
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

	// Ensure the directory exists with secure permissions
	if err := os.MkdirAll(configDir, 0700); err != nil {
		logger.Error("Error creating config directory: %v", err)
		return
	}

	// Open directory using cross-platform open package
	if err := open.Run(configDir); err != nil {
		logger.Error("Error opening config directory: %v", err)
	}
}
