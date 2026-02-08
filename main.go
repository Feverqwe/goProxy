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

	configMutex := &sync.Mutex{}
	currentConfig, err := config.LoadConfig(*configPath, cacheManager, nil, true)
	if err != nil {
		panic(err)
	}

	proxyHandler := handler.NewProxyHandler(currentConfig, cacheManager)

	var currentServer *http.Server

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGHUP, os.Interrupt, syscall.SIGTERM)

	trayManager := tray.NewTrayManager()

	startServer := func(listenAddr string) {
		if currentServer != nil {
			if err := currentServer.Close(); err != nil {
				logger.Error("Error closing old server: %v", err)
			}
		}

		newServer := &http.Server{
			Addr:    listenAddr,
			Handler: proxyHandler,
		}

		go func() {
			logger.Info("Starting proxy server on %s", listenAddr)
			if err := newServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Error("Server error: %v", err)
			}
		}()

		currentServer = newServer
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
		configMutex.Lock()
		defer configMutex.Unlock()

		logger.Info("%s: reloading configuration...", trigger)
		newConfig, err := currentConfig.ReloadConfig(proxyHandler.GetHTTPClient)
		if err != nil {
			logger.Error("Error reloading configuration: %v", err)
			return
		}

		prevAutoReloadHours := currentConfig.AutoReloadHours
		prevListenAddr := currentConfig.ListenAddr

		currentConfig = newConfig

		if newConfig.AutoReloadHours != prevAutoReloadHours {
			startTicker(newConfig.AutoReloadHours)
		}

		if newConfig.ListenAddr != prevListenAddr {
			startServer(newConfig.ListenAddr)
		}

		proxyHandler.UpdateConfig(currentConfig, cacheManager)
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
		startServer(currentConfig.ListenAddr)
	}()

	startTicker(currentConfig.AutoReloadHours)

	go func() {
		<-trayManager.GetQuitChan()

		if reloadTicker != nil {
			reloadTicker.Stop()
		}

		logger.Info("Shutting down proxy server...")
		if err := currentServer.Close(); err != nil {
			logger.Error("Error closing server: %v", err)
		}
		logger.Info("Proxy server stopped")
	}()

	trayManager.Start()
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
