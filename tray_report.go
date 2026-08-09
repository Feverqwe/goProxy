package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"goProxy/internal/config"
	"goProxy/internal/logging"
	"goProxy/internal/report"
	"goProxy/internal/tray"

	"github.com/skratchdot/open-golang/open"
)

func generateAndOpenTrayReport(cfg *config.ProxyConfig, period tray.ReportPeriod, now time.Time, logger *logging.Logger) {
	path, err := createTrayReportFile(cfg, period, now)
	if err != nil {
		logger.Error("Failed to create usage report: %v", err)
		return
	}

	logger.Info("Usage report saved to %s", path)
	if err := open.Run(path); err != nil {
		logger.Error("Failed to open usage report %s: %v", path, err)
	}
}

func createTrayReportFile(cfg *config.ProxyConfig, period tray.ReportPeriod, now time.Time) (string, error) {
	since, periodName, err := trayReportRange(period, now)
	if err != nil {
		return "", err
	}
	logPath := cfg.GetAccessLogPath()
	if logPath == "" {
		return "", errors.New("file logging is disabled")
	}

	stats, err := report.Collect(logPath, since)
	if err != nil {
		return "", err
	}
	addKnownRules(stats, cfg)

	dir := config.GetReportDirectory()
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", fmt.Errorf("create report directory %s: %w", dir, err)
	}

	baseName := fmt.Sprintf("goproxy-report-%s-%s", periodName, now.Format("20060102-150405.000"))
	path, file, err := createUniqueReportFile(dir, baseName)
	if err != nil {
		return "", err
	}

	complete := false
	defer func() {
		if !complete {
			_ = os.Remove(path)
		}
	}()
	if err := report.Write(file, stats, cfg.GetReportTopDomains()); err != nil {
		_ = file.Close()
		return "", fmt.Errorf("write report %s: %w", path, err)
	}
	if err := file.Close(); err != nil {
		return "", fmt.Errorf("close report %s: %w", path, err)
	}
	complete = true
	return path, nil
}

func trayReportRange(period tray.ReportPeriod, now time.Time) (time.Time, string, error) {
	switch period {
	case tray.ReportLastDay:
		return now.Add(-24 * time.Hour).Truncate(time.Second), "24h", nil
	case tray.ReportLastSevenDays:
		return now.Add(-7 * 24 * time.Hour).Truncate(time.Second), "7d", nil
	case tray.ReportAllTime:
		return time.Time{}, "all", nil
	default:
		return time.Time{}, "", fmt.Errorf("unknown report period: %d", period)
	}
}

func createUniqueReportFile(dir, baseName string) (string, *os.File, error) {
	for suffix := 0; ; suffix++ {
		name := baseName + ".txt"
		if suffix > 0 {
			name = fmt.Sprintf("%s-%d.txt", baseName, suffix+1)
		}
		path := filepath.Join(dir, name)
		file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
		if err == nil {
			return path, file, nil
		}
		if !errors.Is(err, os.ErrExist) {
			return "", nil, fmt.Errorf("create report %s: %w", path, err)
		}
	}
}
