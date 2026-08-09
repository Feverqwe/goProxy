package report

import (
	"bufio"
	"compress/gzip"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"text/tabwriter"
	"time"
)

const (
	logTimestampLayout    = "2006/01/02 15:04:05"
	backupTimestampLayout = "2006-01-02T15-04-05.000"
)

type Action string

const (
	ActionBlocked Action = "blocked"
	ActionDirect  Action = "direct"
	ActionProxy   Action = "proxy"
)

type Counts struct {
	Total   int
	Blocked int
	Direct  int
	Proxy   int
}

func (c *Counts) add(action Action) {
	c.Total++
	switch action {
	case ActionBlocked:
		c.Blocked++
	case ActionDirect:
		c.Direct++
	case ActionProxy:
		c.Proxy++
	}
}

type Stats struct {
	Since         time.Time
	Files         []string
	Decisions     Counts
	Protocols     map[string]Counts
	Rules         map[string]Counts
	Domains       map[string]Counts
	UnparsedLines int
	Warnings      []error
}

type event struct {
	Action   Action
	Protocol string
	Domain   string
	Rule     string
}

var (
	httpRoutePattern     = regexp.MustCompile(`^HTTP (Blocking|Direct): (request|HTTPS CONNECT) to (.+) \(rule: '(.*)', proxy: '(.*)'\)$`)
	httpProxyPattern     = regexp.MustCompile(`^HTTP Proxy: (Request|HTTPS CONNECT) to (.+) via proxy (.+) \(rule: '(.*)'\)$`)
	socksRoutePattern    = regexp.MustCompile(`^SOCKS5 (Blocked|Direct): (.+) \(rule: (.*)\)$`)
	socksProxyPattern    = regexp.MustCompile(`^SOCKS5 Proxy: (.+) via (.+) \(rule: (.*)\)$`)
	socksUDPRoutePattern = regexp.MustCompile(`^SOCKS5 UDP (Blocked|Direct): (.+?)(?: \(rule: (.*)\))?$`)
	socksUDPProxyPattern = regexp.MustCompile(`^SOCKS5 UDP Proxy: (.+) via (.+) \(rule: (.*)\)$`)
)

func Collect(logPath string, since time.Time) (*Stats, error) {
	files, err := discoverLogFiles(logPath)
	if err != nil {
		return nil, err
	}

	stats := &Stats{
		Since:     since,
		Files:     files,
		Protocols: make(map[string]Counts),
		Rules:     make(map[string]Counts),
		Domains:   make(map[string]Counts),
	}

	for _, path := range files {
		compressed := filepath.Clean(path) != filepath.Clean(logPath) && isCompressedRotation(path, logPath)
		if err := scanFile(path, compressed, since, stats); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				stats.Warnings = append(stats.Warnings, fmt.Errorf("%s disappeared during log rotation", path))
				continue
			}
			stats.Warnings = append(stats.Warnings, fmt.Errorf("read %s: %w", path, err))
		}
	}

	return stats, nil
}

func discoverLogFiles(logPath string) ([]string, error) {
	dir := filepath.Dir(logPath)
	base := filepath.Base(logPath)
	ext := filepath.Ext(base)
	stem := strings.TrimSuffix(base, ext)

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read log directory %s: %w", dir, err)
	}

	var current string
	var rotations []string
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		path := filepath.Join(dir, name)
		if name == base {
			current = path
		} else if isRotatedLogName(name, stem, ext) {
			rotations = append(rotations, path)
		}
	}

	if current == "" && len(rotations) == 0 {
		return nil, fmt.Errorf("no log files found for %s", logPath)
	}

	sort.Strings(rotations)
	files := make([]string, 0, len(rotations)+1)
	if current != "" {
		// Read the active file first so a concurrent rotation cannot move its
		// contents into a backup that was not present in the directory snapshot.
		files = append(files, current)
	}
	files = append(files, rotations...)
	return files, nil
}

func isRotatedLogName(name, stem, ext string) bool {
	if !strings.HasPrefix(name, stem+"-") {
		return false
	}
	suffix := strings.TrimPrefix(name, stem+"-")
	if isBackupTimestamp(suffix, ext) {
		return true
	}
	if !strings.HasSuffix(suffix, ".gz") {
		return false
	}
	return isBackupTimestamp(strings.TrimSuffix(suffix, ".gz"), ext)
}

func isBackupTimestamp(suffix, ext string) bool {
	if !strings.HasSuffix(suffix, ext) {
		return false
	}
	suffix = strings.TrimSuffix(suffix, ext)
	_, err := time.Parse(backupTimestampLayout, suffix)
	return err == nil
}

func isCompressedRotation(path, logPath string) bool {
	ext := filepath.Ext(filepath.Base(logPath))
	return strings.HasSuffix(filepath.Base(path), ext+".gz")
}

func scanFile(path string, compressed bool, since time.Time, stats *Stats) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()

	var reader io.Reader = file
	if compressed {
		gzReader, err := gzip.NewReader(file)
		if err != nil {
			return err
		}
		defer gzReader.Close()
		reader = gzReader
	}

	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		timestamp, message, ok := splitLogLine(line)
		if !ok {
			if looksLikeAccessLine(line) {
				stats.UnparsedLines++
			}
			continue
		}
		if !since.IsZero() && timestamp.Before(since) {
			continue
		}

		event, ok := parseEvent(message)
		if !ok {
			if looksLikeAccessLine(message) {
				stats.UnparsedLines++
			}
			continue
		}
		stats.add(event)
	}
	return scanner.Err()
}

func splitLogLine(line string) (time.Time, string, bool) {
	if len(line) < len(logTimestampLayout)+1 {
		return time.Time{}, "", false
	}
	timestamp, err := time.ParseInLocation(logTimestampLayout, line[:len(logTimestampLayout)], time.Local)
	if err != nil {
		return time.Time{}, "", false
	}
	message := strings.TrimSpace(line[len(logTimestampLayout):])
	message = strings.TrimPrefix(message, "[INFO] ")
	return timestamp, message, true
}

func parseEvent(message string) (event, bool) {
	if matches := httpRoutePattern.FindStringSubmatch(message); matches != nil {
		return event{
			Action:   routeAction(matches[1]),
			Protocol: httpProtocol(matches[2]),
			Domain:   matches[3],
			Rule:     matches[4],
		}, true
	}
	if matches := httpProxyPattern.FindStringSubmatch(message); matches != nil {
		return event{
			Action:   ActionProxy,
			Protocol: httpProtocol(matches[1]),
			Domain:   matches[2],
			Rule:     matches[4],
		}, true
	}
	if matches := socksRoutePattern.FindStringSubmatch(message); matches != nil {
		return event{
			Action:   routeAction(matches[1]),
			Protocol: "SOCKS5 TCP",
			Domain:   matches[2],
			Rule:     matches[3],
		}, true
	}
	if matches := socksProxyPattern.FindStringSubmatch(message); matches != nil {
		return event{
			Action:   ActionProxy,
			Protocol: "SOCKS5 TCP",
			Domain:   matches[1],
			Rule:     matches[3],
		}, true
	}
	if matches := socksUDPRoutePattern.FindStringSubmatch(message); matches != nil {
		rule := matches[3]
		if rule == "" {
			rule = "(unknown)"
		}
		return event{
			Action:   routeAction(matches[1]),
			Protocol: "SOCKS5 UDP",
			Domain:   matches[2],
			Rule:     rule,
		}, true
	}
	if matches := socksUDPProxyPattern.FindStringSubmatch(message); matches != nil {
		return event{
			Action:   ActionProxy,
			Protocol: "SOCKS5 UDP",
			Domain:   matches[1],
			Rule:     matches[3],
		}, true
	}
	return event{}, false
}

func routeAction(value string) Action {
	switch value {
	case "Blocking", "Blocked":
		return ActionBlocked
	case "Direct":
		return ActionDirect
	default:
		return ActionProxy
	}
}

func httpProtocol(value string) string {
	if value == "HTTPS CONNECT" {
		return "HTTPS CONNECT"
	}
	return "HTTP"
}

func looksLikeAccessLine(line string) bool {
	return strings.Contains(line, "HTTP Blocking:") ||
		strings.Contains(line, "HTTP Direct:") ||
		strings.Contains(line, "HTTP Proxy:") ||
		strings.Contains(line, "SOCKS5 Blocked:") ||
		strings.Contains(line, "SOCKS5 Direct:") ||
		strings.Contains(line, "SOCKS5 Proxy:") ||
		strings.Contains(line, "SOCKS5 UDP Blocked:") ||
		strings.Contains(line, "SOCKS5 UDP Direct:") ||
		strings.Contains(line, "SOCKS5 UDP Proxy:")
}

func (s *Stats) add(event event) {
	s.Decisions.add(event.Action)
	addToMap(s.Protocols, event.Protocol, event.Action)
	addToMap(s.Rules, event.Rule, event.Action)
	addToMap(s.Domains, event.Domain, event.Action)
}

func addToMap(values map[string]Counts, key string, action Action) {
	counts := values[key]
	counts.add(action)
	values[key] = counts
}

func (s *Stats) AddKnownRules(rules []string) {
	for _, rule := range rules {
		if rule == "" {
			continue
		}
		if _, exists := s.Rules[rule]; !exists {
			s.Rules[rule] = Counts{}
		}
	}
}

func Write(w io.Writer, stats *Stats, top int) error {
	if top < 0 {
		return fmt.Errorf("top must not be negative")
	}
	var output strings.Builder
	writeReport(&output, stats, top)
	_, err := io.WriteString(w, output.String())
	return err
}

func writeReport(w io.Writer, stats *Stats, top int) {
	fmt.Fprintln(w, "goProxy usage report")
	if stats.Since.IsZero() {
		fmt.Fprintln(w, "Period: all available logs")
	} else {
		fmt.Fprintf(w, "Period: since %s\n", stats.Since.Format("2006-01-02 15:04:05 MST"))
	}
	fmt.Fprintf(w, "Log files: %d\n\n", len(stats.Files))

	writeSummary(w, stats.Decisions)
	writeCountsTable(w, "Protocols", stats.Protocols, 0)
	writeCountsTable(w, "Rules", stats.Rules, 0)
	if top > 0 {
		writeCountsTable(w, fmt.Sprintf("Domains (top %d)", top), stats.Domains, top)
	}

	if stats.UnparsedLines > 0 {
		fmt.Fprintf(w, "Warning: %d access-like log lines could not be parsed.\n", stats.UnparsedLines)
	}
	for _, warning := range stats.Warnings {
		fmt.Fprintf(w, "Warning: %v\n", warning)
	}
}

func writeSummary(w io.Writer, counts Counts) {
	fmt.Fprintf(w, "Decisions: %d\n", counts.Total)
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintf(tw, "Blocked\t%d\t%s\n", counts.Blocked, percentage(counts.Blocked, counts.Total))
	fmt.Fprintf(tw, "Direct\t%d\t%s\n", counts.Direct, percentage(counts.Direct, counts.Total))
	fmt.Fprintf(tw, "Proxy\t%d\t%s\n", counts.Proxy, percentage(counts.Proxy, counts.Total))
	tw.Flush()
	fmt.Fprintln(w)
}

func writeCountsTable(w io.Writer, title string, values map[string]Counts, limit int) {
	type row struct {
		name   string
		counts Counts
	}
	rows := make([]row, 0, len(values))
	for name, counts := range values {
		rows = append(rows, row{name: name, counts: counts})
	}
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].counts.Total == rows[j].counts.Total {
			return rows[i].name < rows[j].name
		}
		return rows[i].counts.Total > rows[j].counts.Total
	})
	if limit > 0 && len(rows) > limit {
		rows = rows[:limit]
	}

	fmt.Fprintln(w, title+":")
	tw := tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "Name\tTotal\tBlocked\tDirect\tProxy")
	for _, row := range rows {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\n", row.name, row.counts.Total, row.counts.Blocked, row.counts.Direct, row.counts.Proxy)
	}
	tw.Flush()
	fmt.Fprintln(w)
}

func percentage(value, total int) string {
	if total == 0 {
		return "(0.0%)"
	}
	return fmt.Sprintf("(%.1f%%)", float64(value)*100/float64(total))
}
