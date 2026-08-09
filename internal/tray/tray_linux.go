//go:build linux

package tray

import (
	"os"
	"os/signal"
	"sync"
	"syscall"
)

type TrayManager struct {
	quitChan        chan struct{}
	reloadChan      chan struct{}
	openConfigChan  chan struct{}
	checkUpdateChan chan struct{}
	reloadRulesChan chan struct{}
	reportChan      chan ReportPeriod
	exitOnce        sync.Once
}

func NewTrayManager() *TrayManager {
	tm := &TrayManager{
		quitChan:        make(chan struct{}),
		reloadChan:      make(chan struct{}),
		openConfigChan:  make(chan struct{}),
		checkUpdateChan: make(chan struct{}),
		reloadRulesChan: make(chan struct{}),
		reportChan:      make(chan ReportPeriod),
	}

	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan
		tm.Exit()
	}()

	return tm
}

func (tm *TrayManager) Start() {
	<-tm.quitChan
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

func (tm *TrayManager) Exit() {
	tm.exitOnce.Do(func() {
		close(tm.quitChan)
	})
}
