//go:build !linux

package tray

import (
	"goProxy/assets"

	"github.com/getlantern/systray"
)

type TrayManager struct {
	quitChan       chan struct{}
	reloadChan     chan struct{}
	openConfigChan chan struct{}
}

func NewTrayManager() *TrayManager {
	return &TrayManager{
		quitChan:       make(chan struct{}, 1),
		reloadChan:     make(chan struct{}, 1),
		openConfigChan: make(chan struct{}, 1),
	}
}

func (tm *TrayManager) Start() {
	systray.Run(tm.onReady, tm.onExit)
}

func (tm *TrayManager) GetQuitChan() <-chan struct{} {
	return tm.quitChan
}

func (tm *TrayManager) GetReloadChan() <-chan struct{} {
	return tm.reloadChan
}

func (tm *TrayManager) GetOpenConfigChan() <-chan struct{} {
	return tm.openConfigChan
}

func (tm *TrayManager) onReady() {
	systray.SetTemplateIcon(assets.IconSVGData, assets.IconIcoData)
	systray.SetTooltip("GoProxy - HTTP Proxy Server")

	tm.createMenu()
}

func (tm *TrayManager) Exit() {
	systray.Quit()
}

func (tm *TrayManager) onExit() {
	close(tm.quitChan)
}

func (tm *TrayManager) createMenu() {

	systray.AddSeparator()
	reloadItem := systray.AddMenuItem("Reload config", "Reload configuration file")
	openConfigItem := systray.AddMenuItem("Open config directory", "Open directory containing config file")
	systray.AddSeparator()

	quitItem := systray.AddMenuItem("Quit", "Close app")

	go func() {
		for range reloadItem.ClickedCh {

			select {
			case tm.reloadChan <- struct{}{}:
			default:

			}
		}
	}()

	go func() {
		for range openConfigItem.ClickedCh {

			select {
			case tm.openConfigChan <- struct{}{}:
			default:

			}
		}
	}()

	go func() {
		for range quitItem.ClickedCh {
			systray.Quit()
		}
	}()
}
