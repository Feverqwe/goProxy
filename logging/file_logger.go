package logging

import (
	"io"
	"log"
	"os"
	"runtime"

	"gopkg.in/natefinch/lumberjack.v2"
)

// FileLogger provides file-based logging with rotation support using lumberjack
type FileLogger struct {
	config     ConfigProvider
	fileLogger *log.Logger
	lumberjack *lumberjack.Logger
}

// NewFileLogger creates a new file logger with rotation support
func NewFileLogger(config ConfigProvider) (*FileLogger, error) {
	fl := &FileLogger{
		config: config,
	}

	// Get configuration values
	logFile := config.(interface{ GetAccessLogPath() string }).GetAccessLogPath()
	maxSize := config.(interface{ GetMaxLogSize() int }).GetMaxLogSize()
	maxFiles := config.(interface{ GetMaxLogFiles() int }).GetMaxLogFiles()

	// If no log file specified, use stdout only
	if logFile == "" {
		fl.fileLogger = log.New(os.Stdout, "", log.LstdFlags)
		return fl, nil
	}

	// Configure lumberjack for log rotation
	fl.lumberjack = &lumberjack.Logger{
		Filename:   logFile,
		MaxSize:    maxSize,  // megabytes
		MaxBackups: maxFiles, // number of backups
		MaxAge:     28,       // days
		Compress:   true,     // compress rotated files
	}

	var writer io.Writer
	if runtime.GOOS == "windows" {
		writer = fl.lumberjack
	} else {
		// Create multi-writer that writes to both stdout and file
		writer = io.MultiWriter(os.Stdout, fl.lumberjack)
	}
	fl.fileLogger = log.New(writer, "", log.LstdFlags)

	return fl, nil
}

// Printf writes formatted log message to file and stdout
func (fl *FileLogger) Printf(format string, v ...interface{}) {
	fl.fileLogger.Printf(format, v...)
}

// Close closes the log file
func (fl *FileLogger) Close() error {
	if fl.lumberjack != nil {
		return fl.lumberjack.Close()
	}
	return nil
}
