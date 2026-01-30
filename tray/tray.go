//go:build !linux

package tray

import (
	"goProxy/assets"

	"github.com/getlantern/systray"
)

// TrayManager manages the system tray on all platforms except Linux
type TrayManager struct {
	quitChan       chan struct{}
	reloadChan     chan struct{}
	openConfigChan chan struct{}
}

// NewTrayManager creates a new tray manager for all platforms except Linux
func NewTrayManager() *TrayManager {
	return &TrayManager{
		quitChan:       make(chan struct{}, 1),
		reloadChan:     make(chan struct{}, 1),
		openConfigChan: make(chan struct{}, 1),
	}
}

// Start initializes and starts the enhanced system tray
func (tm *TrayManager) Start() {
	systray.Run(tm.onReady, tm.onExit)
}

// GetQuitChan returns the quit channel for external monitoring
func (tm *TrayManager) GetQuitChan() <-chan struct{} {
	return tm.quitChan
}

// GetReloadChan returns the reload config channel
func (tm *TrayManager) GetReloadChan() <-chan struct{} {
	return tm.reloadChan
}

// GetOpenConfigChan returns the open config directory channel
func (tm *TrayManager) GetOpenConfigChan() <-chan struct{} {
	return tm.openConfigChan
}

// onReady is called when systray is ready
func (tm *TrayManager) onReady() {
	systray.SetTemplateIcon(assets.IconSVGData, assets.IconIcoData)
	systray.SetTooltip("GoProxy - HTTP Proxy Server")

	tm.createMenu()
}

func (tm *TrayManager) Exit() {
	systray.Quit()
}

// onExit is called when systray exits
func (tm *TrayManager) onExit() {
	close(tm.quitChan)
}

// createMenu creates the enhanced tray menu with config options
func (tm *TrayManager) createMenu() {
	// Add separator and config options
	systray.AddSeparator()
	reloadItem := systray.AddMenuItem("Reload config", "Reload configuration file")
	openConfigItem := systray.AddMenuItem("Open config directory", "Open directory containing config file")
	systray.AddSeparator()

	// Add the exit button
	quitItem := systray.AddMenuItem("Quit", "Close app")

	// Handle each menu item in separate goroutines
	go func() {
		for range reloadItem.ClickedCh {
			// Signal to reload configuration
			select {
			case tm.reloadChan <- struct{}{}:
			default:
				// Channel is full, ignore
			}
		}
	}()

	go func() {
		for range openConfigItem.ClickedCh {
			// Signal to open config directory
			select {
			case tm.openConfigChan <- struct{}{}:
			default:
				// Channel is full, ignore
			}
		}
	}()

	go func() {
		for range quitItem.ClickedCh {
			systray.Quit()
		}
	}()
}
