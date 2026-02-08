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
	"time"

	"goProxy/cache"
	"goProxy/config"
	"goProxy/handler"
	"goProxy/logger"
	"goProxy/tray"

	"github.com/skratchdot/open-golang/open"
)

func main() {
	logger.InitDefaultLogger()

	defaultConfigPath := config.GetConfigPath()
	configPath := flag.String("config", defaultConfigPath, "Path to configuration file")
	versionFlag := flag.Bool("version", false, "Display version information")
	flag.Parse()

	if *versionFlag {
		fmt.Println(GetVersion())
		fmt.Println(GetBuildInfo())
		return
	}

	cacheManager := cache.NewCacheManager()

	currentConfig, err := config.LoadConfig(*configPath, cacheManager, true)
	if err != nil {
		panic(err)
	}

	proxyHandler := handler.NewProxyHandler(currentConfig, cacheManager)
	currentListenAddr := currentConfig.ListenAddr

	server := &http.Server{
		Addr:    currentListenAddr,
		Handler: proxyHandler,
	}

	serverMutex := &sync.Mutex{}
	currentServer := server

	logger.Info("Starting proxy server on %s", currentListenAddr)

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGHUP, os.Interrupt, syscall.SIGTERM)

	trayManager := tray.NewTrayManager()

	restartServerIfAddressChanged := func(newConfig *config.ProxyConfig) {
		serverMutex.Lock()
		defer serverMutex.Unlock()

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

	reloadTickerChan := make(chan time.Time)

	var reloadTicker *time.Ticker
	startTicker := func(hours int) {
		if reloadTicker != nil {
			reloadTicker.Stop()
		}
		if hours > 0 {
			reloadTicker = time.NewTicker(time.Duration(hours) * time.Hour)
			go func() {
				for t := range reloadTicker.C {
					reloadTickerChan <- t
				}
			}()
		} else {
			reloadTicker = nil
		}
	}

	reloadConfiguration := func(trigger string) {
		logger.Info("%s: reloading configuration...", trigger)
		newConfig, err := currentConfig.ReloadConfig()
		if err != nil {
			logger.Error("Error reloading configuration: %v", err)
			return
		}

		if newConfig.AutoReloadHours != currentConfig.AutoReloadHours {
			startTicker(newConfig.AutoReloadHours)
		}
		currentConfig = newConfig

		proxyHandler.UpdateConfig(currentConfig)

		restartServerIfAddressChanged(currentConfig)
	}

	go func() {
		for {
			select {
			case sig := <-sigChan:
				switch sig {
				case syscall.SIGHUP:
					reloadConfiguration("Received SIGHUP signal")
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
				reloadConfiguration("Manual reload from tray")
			case <-trayManager.GetOpenConfigChan():
				openConfigDirectory(*configPath)
			case <-reloadTickerChan:
				reloadConfiguration("Periodic update")
			}
		}
	}()

	go func() {
		if err := currentServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("Server error: %v", err)
			panic(err)
		}
	}()

	startTicker(currentConfig.AutoReloadHours)
	defer func() {
		if reloadTicker != nil {
			reloadTicker.Stop()
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

func openConfigDirectory(configPath string) {
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
