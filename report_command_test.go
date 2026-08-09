package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseSince(t *testing.T) {
	now := time.Date(2026, 8, 9, 12, 0, 0, 0, time.Local)
	tests := []struct {
		value string
		want  time.Time
	}{
		{value: "24h", want: now.Add(-24 * time.Hour)},
		{value: "7d", want: now.Add(-7 * 24 * time.Hour)},
		{value: "2w", want: now.Add(-14 * 24 * time.Hour)},
		{value: "all", want: time.Time{}},
		{value: "0", want: time.Time{}},
	}

	for _, tt := range tests {
		t.Run(tt.value, func(t *testing.T) {
			got, err := parseSince(tt.value, now)
			if err != nil {
				t.Fatal(err)
			}
			if !got.Equal(tt.want) {
				t.Fatalf("parseSince(%q) = %v, want %v", tt.value, got, tt.want)
			}
		})
	}
}

func TestParseSinceRejectsInvalidValue(t *testing.T) {
	if _, err := parseSince("yesterday", time.Now()); err == nil {
		t.Fatal("parseSince accepted invalid duration")
	}
}

func TestRunReportCommand(t *testing.T) {
	profileDir := t.TempDir()
	t.Setenv("PROFILE_PLACE", profileDir)
	configPath := filepath.Join(profileDir, "config.yaml")
	configContents := `logFile: access.log
rules:
  - name: unused
    proxy: direct
    hosts: unused.example
`
	if err := os.WriteFile(configPath, []byte(configContents), 0600); err != nil {
		t.Fatal(err)
	}
	logContents := "2026/08/09 12:00:00 [INFO] HTTP Direct: request to example.com (rule: 'default', proxy: 'direct')\n"
	if err := os.WriteFile(filepath.Join(profileDir, "access.log"), []byte(logContents), 0600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	now := time.Date(2026, 8, 9, 13, 0, 0, 0, time.Local)
	if err := runReportCommand([]string{"--config", configPath, "--since", "24h", "--top", "5"}, &stdout, &stderr, now); err != nil {
		t.Fatal(err)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q", stderr.String())
	}
	output := stdout.String()
	for _, expected := range []string{"Decisions: 1", "example.com", "unused"} {
		if !strings.Contains(output, expected) {
			t.Fatalf("report does not contain %q:\n%s", expected, output)
		}
	}
}

func TestRunReportCommandUsesConfiguredTopDomains(t *testing.T) {
	profileDir := t.TempDir()
	t.Setenv("PROFILE_PLACE", profileDir)
	configPath := filepath.Join(profileDir, "config.yaml")
	configContents := `logFile: access.log
reportTopDomains: 1
`
	if err := os.WriteFile(configPath, []byte(configContents), 0600); err != nil {
		t.Fatal(err)
	}
	logContents := strings.Join([]string{
		"2026/08/09 12:00:00 [INFO] HTTP Direct: request to top.example (rule: 'default', proxy: 'direct')",
		"2026/08/09 12:01:00 [INFO] HTTP Direct: request to top.example (rule: 'default', proxy: 'direct')",
		"2026/08/09 12:02:00 [INFO] HTTP Direct: request to less.example (rule: 'default', proxy: 'direct')",
	}, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(profileDir, "access.log"), []byte(logContents), 0600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	now := time.Date(2026, 8, 9, 13, 0, 0, 0, time.Local)
	if err := runReportCommand([]string{"--config", configPath, "--since", "all"}, &stdout, &stderr, now); err != nil {
		t.Fatal(err)
	}
	output := stdout.String()
	if !strings.Contains(output, "top.example") || strings.Contains(output, "less.example") {
		t.Fatalf("configured domain limit was not applied:\n%s", output)
	}
}
