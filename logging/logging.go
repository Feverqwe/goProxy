package logging

import (
	"log"
	"strings"
	"sync"
)

type Logger struct {
	logLevelInt int
	fileLogger  *FileLogger
	mu          sync.RWMutex
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
)

func NewLogger(config ConfigProvider) *Logger {
	fileLogger, err := NewFileLogger(config)
	if err != nil {
		log.Printf("Failed to create file logger: %v, falling back to stdout", err)
		fileLogger = nil
	}

	return &Logger{
		logLevelInt: config.GetLogLevelInt(),
		fileLogger:  fileLogger,
	}
}

func (l *Logger) ShouldLog(level int) bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return level <= l.logLevelInt
}

func (l *Logger) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if l.fileLogger != nil {
		return l.fileLogger.Close()
	}
	return nil
}

func (l *Logger) Debug(format string, v ...interface{}) {
	shouldLog := l.ShouldLog(LogLevelDebug)

	if shouldLog {
		l.Printf("[DEBUG] "+format, v...)
	}
}

func (l *Logger) Info(format string, v ...interface{}) {
	shouldLog := l.ShouldLog(LogLevelInfo)

	if shouldLog {
		l.Printf("[INFO] "+format, v...)
	}
}

func (l *Logger) Warn(format string, v ...interface{}) {
	shouldLog := l.ShouldLog(LogLevelWarn)

	if shouldLog {
		l.Printf("[WARN] "+format, v...)
	}
}

func (l *Logger) Error(format string, v ...interface{}) {
	shouldLog := l.ShouldLog(LogLevelError)

	if shouldLog {
		l.Printf("[ERROR] "+format, v...)
	}
}

func (l *Logger) Printf(msg string, v ...interface{}) {
	l.mu.RLock()
	fileLogger := l.fileLogger
	l.mu.RUnlock()

	if fileLogger != nil {
		fileLogger.Printf(msg, v...)
	} else {
		log.Printf(msg, v...)
	}
}

func (l *Logger) Reconfigure(config ConfigProvider) {
	fileLogger, err := NewFileLogger(config)
	if err != nil {
		log.Printf("Failed to reconfigure file logger: %v, falling back to stdout", err)
		fileLogger = nil
	}

	if l.fileLogger != nil {
		l.fileLogger.Close()
	}

	l.mu.Lock()
	l.logLevelInt = config.GetLogLevelInt()
	l.fileLogger = fileLogger
	l.mu.Unlock()
}

type GoproxyLoggerAdapter struct {
	logger *Logger
}

func (g *GoproxyLoggerAdapter) Printf(msg string, v ...interface{}) {
	switch {
	case strings.Contains(msg, "WARN:"):
		g.logger.Warn(msg, v...)
	case strings.Contains(msg, "INFO:"):
		g.logger.Info(msg, v...)
	default:
		g.logger.Error(msg, v...)
	}
}

func NewGoproxyLoggerAdapter(logger *Logger) *GoproxyLoggerAdapter {
	return &GoproxyLoggerAdapter{
		logger: logger,
	}
}
