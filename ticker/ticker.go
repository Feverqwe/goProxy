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

	stopChan := tm.stopChan
	interval := tm.interval
	tm.mutex.Unlock()

	go func() {
		// Используем интервал напрямую вместо проверки каждые 10 минут
		ticker := time.NewTicker(interval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				select {
				case tm.reloadChan <- struct{}{}:
				default:
					// канал полон, пропускаем
				}
				tm.mutex.Lock()
				tm.lastTick = time.Now().Unix()
				tm.mutex.Unlock()
			case <-stopChan:
				return
			}
		}
	}()
}

func (tm *TickerManager) GetReloadChan() <-chan struct{} {
	return tm.reloadChan
}
