package logging

import (
	"io"
	"log"
	"os"
	"runtime"

	"gopkg.in/natefinch/lumberjack.v2"
)

type FileLogger struct {
	config     ConfigProvider
	fileLogger *log.Logger
	lumberjack *lumberjack.Logger
}

func NewFileLogger(config ConfigProvider) (*FileLogger, error) {
	fl := &FileLogger{
		config: config,
	}

	logFile := config.(interface{ GetAccessLogPath() string }).GetAccessLogPath()
	maxSize := config.(interface{ GetMaxLogSize() int }).GetMaxLogSize()
	maxFiles := config.(interface{ GetMaxLogFiles() int }).GetMaxLogFiles()

	if logFile == "" {
		fl.fileLogger = log.New(os.Stdout, "", log.LstdFlags)
		return fl, nil
	}

	fl.lumberjack = &lumberjack.Logger{
		Filename:   logFile,
		MaxSize:    maxSize,
		MaxBackups: maxFiles,
		MaxAge:     28,
		Compress:   true,
	}

	var writer io.Writer
	if runtime.GOOS == "windows" {
		writer = fl.lumberjack
	} else {

		writer = io.MultiWriter(os.Stdout, fl.lumberjack)
	}
	fl.fileLogger = log.New(writer, "", log.LstdFlags)

	return fl, nil
}

func (fl *FileLogger) Printf(format string, v ...interface{}) {
	fl.fileLogger.Printf(format, v...)
}

func (fl *FileLogger) Close() error {
	if fl.lumberjack != nil {
		return fl.lumberjack.Close()
	}
	return nil
}
