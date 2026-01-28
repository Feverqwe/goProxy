package logging

import (
	"log"
)

// Logger предоставляет методы для логирования с различными уровнями
type Logger struct {
	config ConfigProvider
}

// ConfigProvider интерфейс для получения конфигурации логирования
type ConfigProvider interface {
	ShouldLog(level string) bool
}

// NewLogger создает новый экземпляр логгера
func NewLogger(config ConfigProvider) *Logger {
	return &Logger{
		config: config,
	}
}

// Debug логирует отладочное сообщение, если включен соответствующий уровень
func (l *Logger) Debug(format string, v ...interface{}) {
	if l.config.ShouldLog("debug") {
		log.Printf("[DEBUG] "+format, v...)
	}
}

// Info логирует информационное сообщение
func (l *Logger) Info(format string, v ...interface{}) {
	if l.config.ShouldLog("info") {
		log.Printf("[INFO] "+format, v...)
	}
}

// Warn логирует предупреждение
func (l *Logger) Warn(format string, v ...interface{}) {
	if l.config.ShouldLog("warn") {
		log.Printf("[WARN] "+format, v...)
	}
}

// Error логирует ошибку
func (l *Logger) Error(format string, v ...interface{}) {
	if l.config.ShouldLog("error") {
		log.Printf("[ERROR] "+format, v...)
	}
}
