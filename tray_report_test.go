package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"goProxy/internal/config"
	"goProxy/internal/tray"
)

func TestTrayReportRange(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 123, time.Local)
	tests := []struct {
		name       string
		period     tray.ReportPeriod
		wantSince  time.Time
		wantPeriod string
	}{
		{name: "day", period: tray.ReportLastDay, wantSince: now.Add(-24 * time.Hour).Truncate(time.Second), wantPeriod: "24h"},
		{name: "week", period: tray.ReportLastSevenDays, wantSince: now.Add(-7 * 24 * time.Hour).Truncate(time.Second), wantPeriod: "7d"},
		{name: "all", period: tray.ReportAllTime, wantSince: time.Time{}, wantPeriod: "all"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotSince, gotPeriod, err := trayReportRange(tt.period, now)
			if err != nil {
				t.Fatal(err)
			}
			if !gotSince.Equal(tt.wantSince) || gotPeriod != tt.wantPeriod {
				t.Fatalf("trayReportRange() = (%v, %q), want (%v, %q)", gotSince, gotPeriod, tt.wantSince, tt.wantPeriod)
			}
		})
	}
}

func TestCreateTrayReportFile(t *testing.T) {
	profileDir := t.TempDir()
	t.Setenv("PROFILE_PLACE", profileDir)
	cfg := &config.ProxyConfig{
		LogFile:          "goProxy.log",
		ReportTopDomains: 1,
		Rules: []config.RuleConfig{
			{RuleBaseConfig: config.RuleBaseConfig{Name: "unused"}, Proxy: "direct"},
		},
	}
	logContents := strings.Join([]string{
		"2026/08/09 12:00:00 [INFO] HTTP Direct: request to example.com (rule: 'default', proxy: 'direct')",
		"2026/08/09 12:01:00 [INFO] HTTP Direct: request to example.com (rule: 'default', proxy: 'direct')",
		"2026/08/09 12:02:00 [INFO] HTTP Direct: request to less.example (rule: 'default', proxy: 'direct')",
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(profileDir, cfg.LogFile), []byte(logContents), 0600); err != nil {
		t.Fatal(err)
	}

	now := time.Date(2026, 8, 9, 13, 0, 0, 0, time.Local)
	firstPath, err := createTrayReportFile(cfg, tray.ReportLastDay, now)
	if err != nil {
		t.Fatal(err)
	}
	secondPath, err := createTrayReportFile(cfg, tray.ReportLastDay, now)
	if err != nil {
		t.Fatal(err)
	}
	if firstPath == secondPath {
		t.Fatalf("duplicate report path %s", firstPath)
	}
	if filepath.Dir(firstPath) != filepath.Join(profileDir, "report") {
		t.Fatalf("report directory = %s", filepath.Dir(firstPath))
	}
	info, err := os.Stat(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0600 {
		t.Fatalf("report permissions = %o, want 600", info.Mode().Perm())
	}
	contents, err := os.ReadFile(firstPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"Decisions: 3", "example.com", "unused"} {
		if !strings.Contains(string(contents), expected) {
			t.Fatalf("report does not contain %q:\n%s", expected, contents)
		}
	}
	if strings.Contains(string(contents), "less.example") {
		t.Fatalf("tray report ignored reportTopDomains:\n%s", contents)
	}
}

func TestTrayReportRangeRejectsUnknownPeriod(t *testing.T) {
	if _, _, err := trayReportRange(tray.ReportPeriod(99), time.Now()); err == nil {
		t.Fatal("trayReportRange accepted an unknown period")
	}
}
