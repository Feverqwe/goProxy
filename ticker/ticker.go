package ticker

import (
	"sync"
	"time"
)

type TickerManager struct {
	ticker     *time.Ticker
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

	if tm.ticker != nil {
		tm.ticker.Stop()
		tm.ticker = nil
	}
}

func (tm *TickerManager) StartTicker(hours int) {
	tm.StopOldTicker()

	if hours <= 0 {
		return
	}

	tm.mutex.Lock()
	tm.stopChan = make(chan struct{})
	tm.ticker = time.NewTicker(time.Duration(hours) * time.Hour)
	currentStopChan := tm.stopChan
	currentTicker := tm.ticker
	tm.mutex.Unlock()

	go func() {
		for {
			select {
			case <-currentTicker.C:
				select {
				case tm.reloadChan <- struct{}{}:
				default:
					// Если релоад уже в очереди, пропускаем
				}
			case <-currentStopChan:
				return // Выход из горутины при остановке тикера
			}
		}
	}()
}

func (tm *TickerManager) GetReloadChan() <-chan struct{} {
	return tm.reloadChan
}
