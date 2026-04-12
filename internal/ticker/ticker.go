package ticker

import (
	"log"
	"sync"
	"time"
)

type TickerManager struct {
	stopChan   chan struct{}
	mutex      sync.Mutex
	reloadChan chan struct{}
}

func NewTickerManager() *TickerManager {
	return &TickerManager{
		reloadChan: make(chan struct{}, 1),
	}
}

func (tm *TickerManager) StopOldTicker() {
	tm.mutex.Lock()
	defer tm.mutex.Unlock()

	if tm.stopChan != nil {
		close(tm.stopChan)
		tm.stopChan = nil
	}
}

func (tm *TickerManager) StartTicker(hours int) {
	tm.StopOldTicker()

	if hours <= 0 {
		return
	}

	currentStopChan := make(chan struct{})
	lastTick := time.Now().Unix()
	currentInterval := time.Duration(hours) * time.Hour

	tm.mutex.Lock()
	tm.stopChan = currentStopChan
	tm.mutex.Unlock()

	go func() {
		checkTicker := time.NewTicker(10 * time.Minute)
		defer checkTicker.Stop()

		for {
			select {
			case <-checkTicker.C:
				log.Println("tick")
				currentTime := time.Now().Unix()
				elapsed := time.Duration(currentTime-lastTick) * time.Second
				if elapsed >= currentInterval {
					lastTick = currentTime
					select {
					case tm.reloadChan <- struct{}{}:
					default:
					}
				}
			case <-currentStopChan:
				return
			}
		}
	}()
}

func (tm *TickerManager) GetReloadChan() <-chan struct{} {
	return tm.reloadChan
}
