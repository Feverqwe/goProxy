package main

import (
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"goProxy/internal/cache"
	"goProxy/internal/config"
	"goProxy/internal/handler"
	"goProxy/internal/ticker"
	"goProxy/internal/tray"

	"github.com/txthinking/socks5"
)

var Version = "dev"

const (
	LatestReleaseAPI = "https://api.github.com/repos/Feverqwe/goProxy/releases/latest"
	ReleasesURL      = "https://github.com/Feverqwe/goProxy/releases"
)

func main() {
	defaultConfigPath := config.GetConfigPath()
	configPath := flag.String("config", defaultConfigPath, "Path to configuration file")
	versionFlag := flag.Bool("version", false, "Display version information")
	flag.Parse()

	if *versionFlag {
		version := fmt.Sprintf("GoProxy v%s", Version)
		fmt.Println(version)
		return
	}

	cacheManager := cache.NewCacheManager()

	configMutex := &sync.Mutex{}
	currentConfig, err := config.LoadConfig(*configPath, cacheManager, nil, true, nil, false)
	if err != nil {
		panic(err)
	}

	logger := currentConfig.GetLogger()
	proxyHandler := handler.NewProxyHandler(currentConfig, cacheManager, logger)

	var currentHttpServer *http.Server
	var currentSocksServer *socks5.Server

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGHUP, os.Interrupt, syscall.SIGTERM)

	trayManager := tray.NewTrayManager()

	startServer := func(addr string) {
		if currentHttpServer != nil {
			logger.Info("Stopping old HTTP server...")
			if err := currentHttpServer.Close(); err != nil {
				logger.Error("Error closing old server: %v", err)
			}
		}

		if addr == "" {
			return
		}

		newServer := &http.Server{
			Addr:    addr,
			Handler: proxyHandler,
		}

		go func() {
			logger.Info("Starting proxy server on %s", addr)
			if err := newServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				logger.Error("Server error: %v", err)
			}
		}()

		currentHttpServer = newServer
	}

	startSocksServer := func(addr string, ph *handler.ProxyHandler) {
		if currentSocksServer != nil {
			logger.Info("Stopping old SOCKS5 server...")
			currentSocksServer.Shutdown()
		}

		if addr == "" {
			return
		}

		sh := handler.NewSocksHandler(ph, currentConfig, cacheManager, logger)

		socksServer, err := socks5.NewClassicServer(addr, "", "", "", 30, 30)
		if err != nil {
			logger.Error("Failed to init SOCKS5 server: %v", err)
			return
		}

		go func() {
			logger.Info("Starting SOCKS5 server on %s", addr)
			if err := socksServer.ListenAndServe(sh); err != nil {
				logger.Debug("SOCKS5 server closed: %v", err)
			}
		}()

		currentSocksServer = socksServer
	}

	tickerManager := ticker.NewTickerManager()

	reloadConfiguration := func(trigger string, forceReload bool) {
		configMutex.Lock()
		defer configMutex.Unlock()

		logger.Info("%s: reloading configuration...", trigger)

		newConfig, err := currentConfig.ReloadConfig(proxyHandler.GetHTTPClient, forceReload)
		if err != nil {
			logger.Error("Error reloading configuration: %v", err)
			return
		}

		prevAutoReloadHours := currentConfig.AutoReloadHours
		prevListenHttpAddr := currentConfig.ListenHttpAddr
		prevListenSocksAddr := currentConfig.ListenSocksAddr

		currentConfig = newConfig

		if newConfig.AutoReloadHours != prevAutoReloadHours {
			tickerManager.StartTicker(newConfig.AutoReloadHours)
		}

		if newConfig.ListenHttpAddr != prevListenHttpAddr {
			startServer(newConfig.ListenHttpAddr)
		}

		if newConfig.ListenSocksAddr != prevListenSocksAddr {
			startSocksServer(newConfig.ListenSocksAddr, proxyHandler)
		}

		proxyHandler.UpdateConfig(currentConfig, cacheManager)
	}

	go func() {
		for {
			select {
			case sig := <-sigChan:
				switch sig {
				case syscall.SIGHUP:
					reloadConfiguration("Received SIGHUP signal", false)
				case os.Interrupt, syscall.SIGTERM:
					logger.Info("Received interrupt signal, shutting down...")
					trayManager.Exit()
					return
				}
			case <-trayManager.GetReloadChan():
				reloadConfiguration("Manual reload from tray", false)
			case <-trayManager.GetReloadRulesChan():
				reloadConfiguration("Force reload rules from tray", true)
			case <-trayManager.GetOpenConfigChan():
				config.OpenConfigDirectory(*configPath, logger)
			case <-trayManager.GetCheckUpdateChan():
				tray.CheckForUpdates(Version, LatestReleaseAPI, ReleasesURL, logger)
			case <-tickerManager.GetReloadChan():
				reloadConfiguration("Periodic update", false)
			}
		}
	}()

	startServer(currentConfig.ListenHttpAddr)
	startSocksServer(currentConfig.ListenSocksAddr, proxyHandler)

	tickerManager.StartTicker(currentConfig.AutoReloadHours)

	go func() {
		<-trayManager.GetQuitChan()

		tickerManager.StopOldTicker()

		logger.Info("Shutting down...")
		if currentHttpServer != nil {
			currentHttpServer.Close()
		}
		if currentSocksServer != nil {
			currentSocksServer.Shutdown()
		}
		logger.Info("Proxy server stopped")
	}()

	trayManager.Start()
}
