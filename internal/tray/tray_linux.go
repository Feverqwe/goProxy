//go:build linux

package tray

import (
	"os"
	"os/signal"
	"syscall"
)

type TrayManager struct {
	quitChan        chan struct{}
	reloadChan      chan struct{}
	openConfigChan  chan struct{}
	checkUpdateChan chan struct{}
	reloadRulesChan chan struct{}
}

func NewTrayManager() *TrayManager {
	tm := &TrayManager{
		quitChan:        make(chan struct{}),
		reloadChan:      make(chan struct{}),
		openConfigChan:  make(chan struct{}),
		checkUpdateChan: make(chan struct{}),
		reloadRulesChan: make(chan struct{}),
	}

	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan
		close(tm.quitChan)
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

func (tm *TrayManager) Exit() {

}
