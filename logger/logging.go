package logger

import (
	"log"
	"strings"
	"sync"
)

const (
	LogLevelDebug = 4
	LogLevelInfo  = 3
	LogLevelWarn  = 2
	LogLevelError = 1
	LogLevelNone  = 0
)

var (
	globalLogger *Logger
	loggerMutex  sync.RWMutex
)

type Logger struct {
	config     ConfigProvider
	fileLogger *FileLogger
}

type ConfigProvider interface {
	ShouldLog(level int) bool
	GetAccessLogPath() string
	GetMaxLogSize() int
	GetMaxLogFiles() int
}

// DefaultConfigProvider provides default logging configuration
type DefaultConfigProvider struct{}

func (d *DefaultConfigProvider) ShouldLog(level int) bool {
	return level <= LogLevelInfo // Default to info level
}

func (d *DefaultConfigProvider) GetAccessLogPath() string {
	return "" // Default to stdout
}

func (d *DefaultConfigProvider) GetMaxLogSize() int {
	return 10 // Default 10MB
}

func (d *DefaultConfigProvider) GetMaxLogFiles() int {
	return 5 // Default 5 files
}

func NewLogger(config ConfigProvider) *Logger {
	fileLogger, err := NewFileLogger(config)
	if err != nil {
		log.Printf("Failed to create file logger: %v, falling back to stdout", err)
		fileLogger = nil
	}

	return &Logger{
		config:     config,
		fileLogger: fileLogger,
	}
}

func (l *Logger) Close() error {
	if l.fileLogger != nil {
		return l.fileLogger.Close()
	}
	return nil
}

func (l *Logger) Debug(format string, v ...interface{}) {
	if l.config.ShouldLog(LogLevelDebug) {
		l.Printf("[DEBUG] "+format, v...)
	}
}

func (l *Logger) Info(format string, v ...interface{}) {
	if l.config.ShouldLog(LogLevelInfo) {
		l.Printf("[INFO] "+format, v...)
	}
}

func (l *Logger) Warn(format string, v ...interface{}) {
	if l.config.ShouldLog(LogLevelWarn) {
		l.Printf("[WARN] "+format, v...)
	}
}

func (l *Logger) Error(format string, v ...interface{}) {
	if l.config.ShouldLog(LogLevelError) {
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

// Global logger functions
func GetLogger() *Logger {
	loggerMutex.RLock()
	defer loggerMutex.RUnlock()
	return globalLogger
}

// InitDefaultLogger initializes the global logger with default configuration
func InitDefaultLogger() {
	loggerMutex.Lock()
	defer loggerMutex.Unlock()

	// Close the existing logger if it exists
	if globalLogger != nil {
		globalLogger.Close()
	}

	globalLogger = NewLogger(&DefaultConfigProvider{})
}

func ReconfigureGlobalLogger(config ConfigProvider) {
	loggerMutex.Lock()
	defer loggerMutex.Unlock()

	if globalLogger != nil {
		// Close the old file logger
		globalLogger.Close()

		// Create a new logger with the updated configuration
		fileLogger, err := NewFileLogger(config)
		if err != nil {
			log.Printf("Failed to reconfigure file logger: %v, falling back to stdout", err)
			fileLogger = nil
		}

		// Update the logger with new configuration
		globalLogger.config = config
		globalLogger.fileLogger = fileLogger
	} else {
		// If no logger exists, initialize it
		globalLogger = NewLogger(config)
	}
}

func Debug(format string, v ...interface{}) {
	if logger := GetLogger(); logger != nil {
		logger.Debug(format, v...)
	}
}

func Info(format string, v ...interface{}) {
	if logger := GetLogger(); logger != nil {
		logger.Info(format, v...)
	}
}

func Warn(format string, v ...interface{}) {
	if logger := GetLogger(); logger != nil {
		logger.Warn(format, v...)
	}
}

func Error(format string, v ...interface{}) {
	if logger := GetLogger(); logger != nil {
		logger.Error(format, v...)
	}
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
