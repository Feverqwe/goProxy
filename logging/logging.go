package logging

import (
	"goProxy/config"
	"log"
	"strings"
)

// Logger предоставляет методы для логирования с различными уровнями
type Logger struct {
	config     ConfigProvider
	fileLogger *FileLogger
}

// ConfigProvider интерфейс для получения конфигурации логирования
type ConfigProvider interface {
	ShouldLog(level int) bool
	GetAccessLogPath() string
	GetMaxLogSize() int
	GetMaxLogFiles() int
}

// NewLogger создает новый экземпляр логгера
func NewLogger(config ConfigProvider) *Logger {
	fileLogger, err := NewFileLogger(config)
	if err != nil {
		// Fallback to stdout if file logger fails
		log.Printf("Failed to create file logger: %v, falling back to stdout", err)
		fileLogger = nil
	}

	return &Logger{
		config:     config,
		fileLogger: fileLogger,
	}
}

func (l *Logger) Close() error {
	return l.fileLogger.Close()
}

// Debug логирует отладочное сообщение, если включен соответствующий уровень
func (l *Logger) Debug(format string, v ...interface{}) {
	if l.config.ShouldLog(config.LogLevelDebug) {
		l.Printf("[DEBUG] "+format, v...)
	}
}

// Info логирует информационное сообщение
func (l *Logger) Info(format string, v ...interface{}) {
	if l.config.ShouldLog(config.LogLevelInfo) {
		l.Printf("[INFO] "+format, v...)
	}
}

// Warn логирует предупреждение
func (l *Logger) Warn(format string, v ...interface{}) {
	if l.config.ShouldLog(config.LogLevelWarn) {
		l.Printf("[WARN] "+format, v...)
	}
}

// Error логирует ошибку
func (l *Logger) Error(format string, v ...interface{}) {
	if l.config.ShouldLog(config.LogLevelError) {
		l.Printf("[ERROR] "+format, v...)
	}
}

func (l *Logger) Printf(msg string, v ...interface{}) {
	if l.fileLogger != nil {
		l.fileLogger.Printf(msg, v...)
	} else {
		log.Printf(msg, v...)
	}
}

// GoproxyLoggerAdapter адаптирует наш логгер для использования в goproxy
type GoproxyLoggerAdapter struct {
	logger *Logger
}

// NewGoproxyLoggerAdapter создает новый адаптер логгера для goproxy
func NewGoproxyLoggerAdapter(logger *Logger) *GoproxyLoggerAdapter {
	return &GoproxyLoggerAdapter{
		logger: logger,
	}
}

// Printf реализует интерфейс goproxy.Logger
func (g *GoproxyLoggerAdapter) Printf(msg string, v ...interface{}) {
	// Определяем уровень логирования на основе содержимого сообщения
	switch {
	case strings.Contains(msg, "WARN:"):
		if g.logger.config.ShouldLog(config.LogLevelWarn) {
			g.logger.Printf(msg, v...)
		}
	case strings.Contains(msg, "INFO:"):
		if g.logger.config.ShouldLog(config.LogLevelInfo) {
			g.logger.Printf(msg, v...)
		}
	default:
		if g.logger.config.ShouldLog(config.LogLevelError) {
			g.logger.Printf(msg, v...)
		}
	}
}
