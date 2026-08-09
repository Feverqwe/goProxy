package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"goProxy/internal/cache"
	"goProxy/internal/config"
	"goProxy/internal/handler"
	"goProxy/internal/ticker"
	"goProxy/internal/tray"
)

var Version = "dev"

const (
	LatestReleaseAPI = "https://api.github.com/repos/Feverqwe/goProxy/releases/latest"
	ReleasesURL      = "https://github.com/Feverqwe/goProxy/releases"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "report" {
		if err := runReportCommand(os.Args[2:], os.Stdout, os.Stderr, time.Now()); err != nil {
			fmt.Fprintf(os.Stderr, "GoProxy report: %v\n", err)
			os.Exit(1)
		}
		return
	}

	defaultConfigPath := config.GetConfigPath()
	configPath := flag.String("config", defaultConfigPath, "Path to configuration file")
	versionFlag := flag.Bool("version", false, "Display version information")
	flag.Parse()

	if *versionFlag {
		version := fmt.Sprintf("GoProxy v%s", Version)
		fmt.Println(version)
		return
	}

	if err := runProxy(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "GoProxy: %v\n", err)
		os.Exit(1)
	}
}

func runProxy(configPath string) error {
	cacheManager := cache.NewCacheManager()

	configMutex := &sync.Mutex{}
	currentConfig, err := config.LoadConfig(configPath, cacheManager, nil, true, nil, false)
	if err != nil {
		return err
	}

	logger := currentConfig.GetLogger()
	defer logger.Close()
	proxyHandler := handler.NewProxyHandler(currentConfig, cacheManager, logger)

	var currentHTTPServer *runningHTTPServer
	var currentSOCKS5Server *runningSOCKS5Server

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGHUP, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)

	trayManager := tray.NewTrayManager()
	var reportMutex sync.Mutex
	var reportWG sync.WaitGroup

	startServer := func(addr string) error {
		if addr == "" {
			if currentHTTPServer != nil {
				logger.Info("Stopping old HTTP server...")
				if err := currentHTTPServer.Close(); err != nil {
					logger.Error("Error closing old HTTP server: %v", err)
				}
				currentHTTPServer = nil
			}
			return nil
		}

		newServer, serveErr, err := startHTTPProxyServer(addr, proxyHandler)
		if err != nil {
			return err
		}

		if currentHTTPServer != nil {
			logger.Info("Stopping old HTTP server...")
			if err := currentHTTPServer.Close(); err != nil {
				logger.Error("Error closing old HTTP server: %v", err)
			}
		}

		logger.Info("Starting proxy server on %s", addr)
		currentHTTPServer = newServer
		go func() {
			if err := <-serveErr; err != nil {
				logger.Error("Server error: %v", err)
			}
		}()
		return nil
	}

	startSocksServer := func(addr string, ph *handler.ProxyHandler) error {
		if addr == "" {
			if currentSOCKS5Server != nil {
				logger.Info("Stopping old SOCKS5 server...")
				if err := currentSOCKS5Server.Close(); err != nil {
					logger.Error("Error closing old SOCKS5 server: %v", err)
				}
				currentSOCKS5Server = nil
			}
			return nil
		}

		sh := handler.NewSocksHandler(ph, currentConfig, cacheManager, logger)
		socksServer, serveErr, err := startSOCKS5ProxyServer(addr, sh)
		if err != nil {
			return err
		}

		if currentSOCKS5Server != nil {
			logger.Info("Stopping old SOCKS5 server...")
			if err := currentSOCKS5Server.Close(); err != nil {
				logger.Error("Error closing old SOCKS5 server: %v", err)
			}
		}

		logger.Info("Starting SOCKS5 server on %s", addr)
		currentSOCKS5Server = socksServer
		go func() {
			if err := <-serveErr; err != nil {
				logger.Debug("SOCKS5 server closed: %v", err)
			}
		}()
		return nil
	}

	tickerManager := ticker.NewTickerManager()
	fatalErrChan := make(chan error, 1)
	stopWithError := func(err error) {
		logger.Error("Fatal server error: %v", err)
		select {
		case fatalErrChan <- err:
		default:
		}
		trayManager.Exit()
	}

	reloadConfiguration := func(trigger string, forceReload bool) error {
		configMutex.Lock()
		defer configMutex.Unlock()

		logger.Info("%s: reloading configuration...", trigger)

		newConfig, err := currentConfig.ReloadConfig(proxyHandler.GetHTTPClient, forceReload)
		if err != nil {
			logger.Error("Error reloading configuration: %v", err)
			return nil
		}

		prevAutoReloadHours := currentConfig.AutoReloadHours
		prevListenHttpAddr := currentConfig.ListenHttpAddr
		prevListenSocksAddr := currentConfig.ListenSocksAddr

		currentConfig = newConfig

		if newConfig.AutoReloadHours != prevAutoReloadHours {
			tickerManager.StartTicker(newConfig.AutoReloadHours)
		}

		if newConfig.ListenHttpAddr != prevListenHttpAddr {
			if err := startServer(newConfig.ListenHttpAddr); err != nil {
				return fmt.Errorf("reload HTTP listener: %w", err)
			}
		}

		if newConfig.ListenSocksAddr != prevListenSocksAddr {
			if err := startSocksServer(newConfig.ListenSocksAddr, proxyHandler); err != nil {
				return fmt.Errorf("reload SOCKS5 listener: %w", err)
			}
		}

		proxyHandler.UpdateConfig(currentConfig, cacheManager)
		return nil
	}

	if err := startServer(currentConfig.ListenHttpAddr); err != nil {
		return err
	}
	if err := startSocksServer(currentConfig.ListenSocksAddr, proxyHandler); err != nil {
		if currentHTTPServer != nil {
			_ = currentHTTPServer.Close()
		}
		return err
	}

	eventLoopDone := make(chan struct{})
	go func() {
		defer close(eventLoopDone)
		for {
			select {
			case <-trayManager.GetQuitChan():
				return
			case sig := <-sigChan:
				switch sig {
				case syscall.SIGHUP:
					if err := reloadConfiguration("Received SIGHUP signal", false); err != nil {
						stopWithError(err)
						return
					}
				case os.Interrupt, syscall.SIGTERM:
					logger.Info("Received interrupt signal, shutting down...")
					trayManager.Exit()
					return
				}
			case <-trayManager.GetReloadChan():
				if err := reloadConfiguration("Manual reload from tray", false); err != nil {
					stopWithError(err)
					return
				}
			case <-trayManager.GetReloadRulesChan():
				if err := reloadConfiguration("Force reload rules from tray", true); err != nil {
					stopWithError(err)
					return
				}
			case <-trayManager.GetOpenConfigChan():
				config.OpenConfigDirectory(configPath, logger)
			case <-trayManager.GetCheckUpdateChan():
				tray.CheckForUpdates(Version, LatestReleaseAPI, ReleasesURL, logger)
			case period := <-trayManager.GetReportChan():
				reportConfig := currentConfig
				reportWG.Add(1)
				go func() {
					defer reportWG.Done()
					reportMutex.Lock()
					defer reportMutex.Unlock()
					generateAndOpenTrayReport(reportConfig, period, time.Now(), logger)
				}()
			case <-tickerManager.GetReloadChan():
				if err := reloadConfiguration("Periodic update", false); err != nil {
					stopWithError(err)
					return
				}
			}
		}
	}()

	tickerManager.StartTicker(currentConfig.AutoReloadHours)

	trayManager.Start()
	<-eventLoopDone

	tickerManager.StopOldTicker()
	configMutex.Lock()
	logger.Info("Shutting down...")
	if currentHTTPServer != nil {
		_ = currentHTTPServer.Close()
	}
	if currentSOCKS5Server != nil {
		_ = currentSOCKS5Server.Close()
	}
	configMutex.Unlock()
	reportWG.Wait()
	logger.Info("Proxy server stopped")

	select {
	case err := <-fatalErrChan:
		return err
	default:
		return nil
	}
}
