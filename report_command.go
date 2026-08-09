package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"time"

	"goProxy/internal/config"
	"goProxy/internal/report"

	"gopkg.in/yaml.v3"
)

func runReportCommand(args []string, stdout, stderr io.Writer, now time.Time) error {
	flags := flag.NewFlagSet("report", flag.ContinueOnError)
	flags.SetOutput(stderr)
	configPath := flags.String("config", config.GetConfigPath(), "Path to configuration file")
	sinceValue := flags.String("since", "7d", "Report period (for example 24h, 7d, 2w, or all)")
	top := flags.Int("top", 0, "Number of most-used domains to show (defaults to reportTopDomains; 0 disables the table)")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected report argument: %s", flags.Arg(0))
	}
	topWasSet := false
	flags.Visit(func(f *flag.Flag) {
		if f.Name == "top" {
			topWasSet = true
		}
	})
	if topWasSet && *top < 0 {
		return fmt.Errorf("top must not be negative")
	}

	since, err := parseSince(*sinceValue, now)
	if err != nil {
		return err
	}

	cfg, err := readReportConfig(*configPath)
	if err != nil {
		return err
	}
	logPath := cfg.GetAccessLogPath()
	if logPath == "" {
		return fmt.Errorf("file logging is disabled by logFile in %s", *configPath)
	}

	stats, err := report.Collect(logPath, since)
	if err != nil {
		return err
	}
	addKnownRules(stats, cfg)

	reportTop := cfg.GetReportTopDomains()
	if topWasSet {
		reportTop = *top
	}
	return report.Write(stdout, stats, reportTop)
}

func addKnownRules(stats *report.Stats, cfg *config.ProxyConfig) {
	knownRules := []string{"default"}
	for _, rule := range cfg.Rules {
		knownRules = append(knownRules, rule.Name)
	}
	stats.AddKnownRules(knownRules)
}

func readReportConfig(path string) (*config.ProxyConfig, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open configuration %s: %w", path, err)
	}
	defer file.Close()

	cfg := &config.ProxyConfig{}
	if err := yaml.NewDecoder(file).Decode(cfg); err != nil {
		return nil, fmt.Errorf("parse configuration %s: %w", path, err)
	}
	return cfg, nil
}

func parseSince(value string, now time.Time) (time.Time, error) {
	value = strings.TrimSpace(strings.ToLower(value))
	if value == "all" || value == "0" {
		return time.Time{}, nil
	}

	duration, err := parseReportDuration(value)
	if err != nil {
		return time.Time{}, fmt.Errorf("invalid since value %q: use a duration such as 24h, 7d, 2w, or all", value)
	}
	if duration < 0 {
		return time.Time{}, fmt.Errorf("since duration must not be negative")
	}
	return now.Add(-duration).Truncate(time.Second), nil
}

func parseReportDuration(value string) (time.Duration, error) {
	if len(value) > 1 {
		unit := value[len(value)-1]
		if unit == 'd' || unit == 'w' {
			amount, err := strconv.ParseUint(value[:len(value)-1], 10, 32)
			if err != nil {
				return 0, err
			}
			hours := amount * 24
			if unit == 'w' {
				hours *= 7
			}
			return time.ParseDuration(strconv.FormatUint(hours, 10) + "h")
		}
	}
	return time.ParseDuration(value)
}
