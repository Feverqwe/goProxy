package logging

import (
	"fmt"
	"log"
	"strings"
	"sync"
)

type Logger struct {
	logLevelInt int
	fileLogger  *FileLogger
	mu          sync.RWMutex
	logChan     chan string
	done        chan struct{}
	wg          sync.WaitGroup
}

type ConfigProvider interface {
	GetAccessLogPath() string
	GetMaxLogSize() int
	GetMaxLogFiles() int
	GetLogLevelInt() int
}

const (
	LogLevelDebug = 4
	LogLevelInfo  = 3
	LogLevelWarn  = 2
	LogLevelError = 1
	LogLevelNone  = 0
	LogBufferSize = 100000
)

func NewLogger(config ConfigProvider) *Logger {
	fileLogger, err := NewFileLogger(config)
	if err != nil {
		log.Printf("Failed to create file logger: %v, falling back to stdout", err)
		fileLogger = nil
	}

	l := &Logger{
		logLevelInt: config.GetLogLevelInt(),
		fileLogger:  fileLogger,
		logChan:     make(chan string, LogBufferSize),
		done:        make(chan struct{}),
	}

	l.wg.Add(1)
	go l.startWorker()

	return l
}

func (l *Logger) startWorker() {
	defer l.wg.Done()
	for {
		select {
		case msg, ok := <-l.logChan:
			if !ok {
				return
			}
			l.mu.RLock()
			fLogger := l.fileLogger
			l.mu.RUnlock()

			if fLogger != nil {
				fLogger.Printf("%s", msg)
			} else {
				log.Printf("%s", msg)
			}
		case <-l.done:
			close(l.logChan)
			for msg := range l.logChan {
				if l.fileLogger != nil {
					l.fileLogger.Printf("%s", msg)
				} else {
					log.Printf("%s", msg)
				}
			}
			return
		}
	}
}

func (l *Logger) Printf(format string, v ...interface{}) {
	msg := fmt.Sprintf(format, v...)

	select {
	case l.logChan <- msg:
	default:
		// pass
	}
}

func (l *Logger) ShouldLog(level int) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return level <= l.logLevelInt
}

func (l *Logger) Close() error {
	close(l.done)
	l.wg.Wait()

	l.mu.Lock()
	defer l.mu.Unlock()

	if l.fileLogger != nil {
		return l.fileLogger.Close()
	}
	return nil
}

func (l *Logger) Debug(format string, v ...interface{}) {
	if l.ShouldLog(LogLevelDebug) {
		l.Printf("[DEBUG] "+format, v...)
	}
}

func (l *Logger) Info(format string, v ...interface{}) {
	if l.ShouldLog(LogLevelInfo) {
		l.Printf("[INFO] "+format, v...)
	}
}

func (l *Logger) Warn(format string, v ...interface{}) {
	if l.ShouldLog(LogLevelWarn) {
		l.Printf("[WARN] "+format, v...)
	}
}

func (l *Logger) Error(format string, v ...interface{}) {
	if l.ShouldLog(LogLevelError) {
		l.Printf("[ERROR] "+format, v...)
	}
}

func (l *Logger) Reconfigure(config ConfigProvider) {
	newFileLogger, err := NewFileLogger(config)
	if err != nil {
		log.Printf("Failed to reconfigure file logger: %v", err)
		return
	}

	l.mu.Lock()
	oldFileLogger := l.fileLogger
	l.logLevelInt = config.GetLogLevelInt()
	l.fileLogger = newFileLogger
	l.mu.Unlock()

	if oldFileLogger != nil {
		oldFileLogger.Close()
	}
}

type GoproxyLoggerAdapter struct {
	logger *Logger
}

func (g *GoproxyLoggerAdapter) Printf(msg string, v ...interface{}) {
	cleanMsg := strings.TrimSpace(msg)
	switch {
	case strings.Contains(cleanMsg, "WARN:"):
		g.logger.Warn(cleanMsg, v...)
	case strings.Contains(cleanMsg, "INFO:"):
		g.logger.Info(cleanMsg, v...)
	default:
		g.logger.Error(cleanMsg, v...)
	}
}

func NewGoproxyLoggerAdapter(logger *Logger) *GoproxyLoggerAdapter {
	return &GoproxyLoggerAdapter{logger: logger}
}
