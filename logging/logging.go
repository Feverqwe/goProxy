package logging

import (
	"goProxy/config"
	"log"
	"strings"
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
	return l.fileLogger.Close()
}

func (l *Logger) Debug(format string, v ...interface{}) {
	if l.config.ShouldLog(config.LogLevelDebug) {
		l.Printf("[DEBUG] "+format, v...)
	}
}

func (l *Logger) Info(format string, v ...interface{}) {
	if l.config.ShouldLog(config.LogLevelInfo) {
		l.Printf("[INFO] "+format, v...)
	}
}

func (l *Logger) Warn(format string, v ...interface{}) {
	if l.config.ShouldLog(config.LogLevelWarn) {
		l.Printf("[WARN] "+format, v...)
	}
}

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

type GoproxyLoggerAdapter struct {
	logger *Logger
}

func NewGoproxyLoggerAdapter(logger *Logger) *GoproxyLoggerAdapter {
	return &GoproxyLoggerAdapter{
		logger: logger,
	}
}

func (g *GoproxyLoggerAdapter) Printf(msg string, v ...interface{}) {

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
