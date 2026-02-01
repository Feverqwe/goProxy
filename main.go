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

	defaultConfigPath := config.GetConfigPath()
	configPath := flag.String("config", defaultConfigPath, "Path to configuration file")
	versionFlag := flag.Bool("version", false, "Display version information")
	flag.Parse()

	if *versionFlag {
		fmt.Println(GetVersion())
		fmt.Println(GetBuildInfo())
		return
	}

	configManager, err := config.NewConfigManager(*configPath)
	if err != nil {

		panic(err)
	}

	currentConfig := configManager.GetConfig()
	logger := logging.NewLogger(currentConfig)

	proxyHandler := handler.NewProxyHandler(configManager)

	server := &http.Server{
		Addr:    currentConfig.ListenAddr,
		Handler: proxyHandler,
	}

	serverMutex := &sync.Mutex{}
	currentServer := server
	currentListenAddr := currentConfig.ListenAddr

	logger.Info("Starting proxy server on %s", currentConfig.ListenAddr)
	logger.Info("Default proxy: %s", currentConfig.GetProxyURL())
	logger.Info("Available proxies: %v", currentConfig.Proxies)
	logger.Info("Configuration reload signal: SIGHUP (kill -HUP %d)", os.Getpid())

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGHUP, os.Interrupt, syscall.SIGTERM)

	trayManager := tray.NewTrayManager()

	restartServerIfAddressChanged := func() {
		serverMutex.Lock()
		defer serverMutex.Unlock()

		newConfig := configManager.GetConfig()
		newListenAddr := newConfig.ListenAddr

		if newListenAddr != currentListenAddr {
			logger.Info("Listen address changed from '%s' to '%s', restarting server...", currentListenAddr, newListenAddr)

			if err := currentServer.Close(); err != nil {
				logger.Error("Error closing old server: %v", err)
			}

			newServer := &http.Server{
				Addr:    newListenAddr,
				Handler: proxyHandler,
			}

			go func() {
				logger.Info("Starting new server on %s", newListenAddr)
				if err := newServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
					logger.Error("New server error: %v", err)

					panic(err)
				}
			}()

			currentServer = newServer
			currentListenAddr = newListenAddr
		} else {
			logger.Debug("Listen address unchanged (%s), no server restart needed", currentListenAddr)
		}
	}

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

						proxyHandler.UpdateConfig(configManager)

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

					proxyHandler.UpdateConfig(configManager)

					restartServerIfAddressChanged()
				}
			case <-trayManager.GetOpenConfigChan():
				openConfigDirectory(*configPath, logger)
			}
		}
	}()

	go func() {
		if err := currentServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server error: %v", err)

			panic(err)
		}
	}()

	trayManager.Start()

	logger.Info("Shutting down proxy server...")
	serverMutex.Lock()
	if err := currentServer.Close(); err != nil {
		logger.Error("Error closing server: %v", err)
	}
	serverMutex.Unlock()
	logger.Info("Proxy server stopped")
}

func openConfigDirectory(configPath string, logger *logging.Logger) {
	configDir := filepath.Dir(configPath)

	if !filepath.IsAbs(configDir) {
		absPath, err := filepath.Abs(configDir)
		if err != nil {
			logger.Error("Error getting absolute path for config directory: %v", err)
			return
		}
		configDir = absPath
	}

	if err := os.MkdirAll(configDir, 0700); err != nil {
		logger.Error("Error creating config directory: %v", err)
		return
	}

	if err := open.Run(configDir); err != nil {
		logger.Error("Error opening config directory: %v", err)
	}
}
