//go:build linux

package tray

import (
	"os"
	"os/signal"
	"syscall"
)

// TrayManager is a stub implementation for Linux systems
type TrayManager struct {
	quitChan       chan struct{}
	reloadChan     chan struct{}
	openConfigChan chan struct{}
}

func NewTrayManager() *TrayManager {
	tm := &TrayManager{
		quitChan:       make(chan struct{}),
		reloadChan:     make(chan struct{}),
		openConfigChan: make(chan struct{}),
	}

	// Set up signal handling to close quitChan on interrupt
	go func() {
		sigChan := make(chan os.Signal, 1)
		signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
		<-sigChan
		close(tm.quitChan)
	}()

	return tm
}

func (tm *TrayManager) Start() {
	// No-op for Linux
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

func (tm *TrayManager) Exit() {
	// No-op for Linux
}
