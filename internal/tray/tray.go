//go:build !linux

package tray

import (
	"goProxy/assets"

	"github.com/getlantern/systray"
)

type TrayManager struct {
	quitChan        chan struct{}
	reloadChan      chan struct{}
	openConfigChan  chan struct{}
	checkUpdateChan chan struct{}
	reloadRulesChan chan struct{}
	reportChan      chan ReportPeriod
}

func NewTrayManager() *TrayManager {
	return &TrayManager{
		quitChan:        make(chan struct{}, 1),
		reloadChan:      make(chan struct{}, 1),
		openConfigChan:  make(chan struct{}, 1),
		checkUpdateChan: make(chan struct{}, 1),
		reloadRulesChan: make(chan struct{}, 1),
		reportChan:      make(chan ReportPeriod, 1),
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

func (tm *TrayManager) GetCheckUpdateChan() <-chan struct{} {
	return tm.checkUpdateChan
}

func (tm *TrayManager) GetReloadRulesChan() <-chan struct{} {
	return tm.reloadRulesChan
}

func (tm *TrayManager) GetReportChan() <-chan ReportPeriod {
	return tm.reportChan
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
	reloadRulesItem := systray.AddMenuItem("Reload rules", "Force reload external rules")
	openConfigItem := systray.AddMenuItem("Open config directory", "Open directory containing config file")
	reportItem := systray.AddMenuItem("Report", "Create and open a usage report")
	reportDayItem := reportItem.AddSubMenuItem("Last 24 hours", "Create a report for the last 24 hours")
	reportWeekItem := reportItem.AddSubMenuItem("Last 7 days", "Create a report for the last 7 days")
	reportAllItem := reportItem.AddSubMenuItem("All time", "Create a report from all available logs")
	checkUpdateItem := systray.AddMenuItem("Check updates", "Check for new version")
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
		for range checkUpdateItem.ClickedCh {
			select {
			case tm.checkUpdateChan <- struct{}{}:
			default:
			}
		}
	}()

	go func() {
		for range reloadRulesItem.ClickedCh {
			select {
			case tm.reloadRulesChan <- struct{}{}:
			default:
			}
		}
	}()

	forwardReport := func(item *systray.MenuItem, period ReportPeriod) {
		go func() {
			for range item.ClickedCh {
				select {
				case tm.reportChan <- period:
				default:
				}
			}
		}()
	}
	forwardReport(reportDayItem, ReportLastDay)
	forwardReport(reportWeekItem, ReportLastSevenDays)
	forwardReport(reportAllItem, ReportAllTime)

	go func() {
		for range quitItem.ClickedCh {
			systray.Quit()
		}
	}()
}
