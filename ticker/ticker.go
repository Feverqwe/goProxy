package ticker

import (
	"sync"
	"time"
)

type TickerManager struct {
	stopChan   chan struct{}
	mutex      sync.Mutex
	reloadChan chan struct{}
	interval   time.Duration
	lastTick   int64
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

	tm.mutex.Lock()
	tm.stopChan = make(chan struct{})
	tm.interval = time.Duration(hours) * time.Hour
	tm.lastTick = time.Now().Unix()
	currentStopChan := tm.stopChan
	currentInterval := tm.interval
	tm.mutex.Unlock()

	checkTicker := time.NewTicker(10 * time.Minute)
	defer checkTicker.Stop()

	go func() {
		for {
			select {
			case <-checkTicker.C:
				tm.mutex.Lock()
				currentTime := time.Now().Unix()
				elapsed := time.Duration(currentTime-tm.lastTick) * time.Second
				if elapsed >= currentInterval {
					select {
					case tm.reloadChan <- struct{}{}:
					default:

					}
					tm.lastTick = currentTime
				}
				tm.mutex.Unlock()
			case <-currentStopChan:
				return
			}
		}
	}()
}

func (tm *TickerManager) GetReloadChan() <-chan struct{} {
	return tm.reloadChan
}
