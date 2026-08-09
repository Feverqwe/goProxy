package report

import (
	"bytes"
	"compress/gzip"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseEvent(t *testing.T) {
	tests := []struct {
		name     string
		message  string
		expected event
	}{
		{
			name:    "http blocked",
			message: "HTTP Blocking: request to ads.example (rule: 'blocked hosts', proxy: 'block')",
			expected: event{
				Action:   ActionBlocked,
				Protocol: "HTTP",
				Domain:   "ads.example",
				Rule:     "blocked hosts",
			},
		},
		{
			name:    "https direct",
			message: "HTTP Direct: HTTPS CONNECT to secure.example (rule: 'default', proxy: 'direct')",
			expected: event{
				Action:   ActionDirect,
				Protocol: "HTTPS CONNECT",
				Domain:   "secure.example",
				Rule:     "default",
			},
		},
		{
			name:    "http proxy",
			message: "HTTP Proxy: Request to proxy.example via proxy office (rule: 'work')",
			expected: event{
				Action:   ActionProxy,
				Protocol: "HTTP",
				Domain:   "proxy.example",
				Rule:     "work",
			},
		},
		{
			name:    "socks tcp blocked",
			message: "SOCKS5 Blocked: tcp.example (rule: blocked)",
			expected: event{
				Action:   ActionBlocked,
				Protocol: "SOCKS5 TCP",
				Domain:   "tcp.example",
				Rule:     "blocked",
			},
		},
		{
			name:    "socks tcp proxy",
			message: "SOCKS5 Proxy: tcp.example via office (rule: work)",
			expected: event{
				Action:   ActionProxy,
				Protocol: "SOCKS5 TCP",
				Domain:   "tcp.example",
				Rule:     "work",
			},
		},
		{
			name:    "socks udp blocked without rule",
			message: "SOCKS5 UDP Blocked: udp.example",
			expected: event{
				Action:   ActionBlocked,
				Protocol: "SOCKS5 UDP",
				Domain:   "udp.example",
				Rule:     "(unknown)",
			},
		},
		{
			name:    "socks udp direct",
			message: "SOCKS5 UDP Direct: udp.example (rule: local)",
			expected: event{
				Action:   ActionDirect,
				Protocol: "SOCKS5 UDP",
				Domain:   "udp.example",
				Rule:     "local",
			},
		},
		{
			name:    "socks udp proxy",
			message: "SOCKS5 UDP Proxy: udp.example via office (rule: work)",
			expected: event{
				Action:   ActionProxy,
				Protocol: "SOCKS5 UDP",
				Domain:   "udp.example",
				Rule:     "work",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			actual, ok := parseEvent(tt.message)
			if !ok {
				t.Fatal("parseEvent did not recognize message")
			}
			if actual != tt.expected {
				t.Fatalf("parseEvent() = %#v, want %#v", actual, tt.expected)
			}
		})
	}
}

func TestCollectReadsCurrentAndCompressedRotatedLogs(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "goProxy.log")
	current := strings.Join([]string{
		"2026/08/09 11:59:59 [INFO] HTTP Direct: request to old.example (rule: 'default', proxy: 'direct')",
		"2026/08/09 12:00:00 [INFO] HTTP Blocking: request to ads.example (rule: 'blocked', proxy: 'block')",
		"2026/08/09 12:01:00 [INFO] unrelated message",
	}, "\n") + "\n"
	if err := os.WriteFile(logPath, []byte(current), 0600); err != nil {
		t.Fatal(err)
	}

	rotatedPath := filepath.Join(dir, "goProxy-2026-08-09T12-02-00.000.log.gz")
	writeGzipFile(t, rotatedPath, "2026/08/09 12:02:00 [INFO] SOCKS5 Proxy: api.example via office (rule: work)\n")
	if err := os.WriteFile(filepath.Join(dir, "other.log"), []byte("not part of the report\n"), 0600); err != nil {
		t.Fatal(err)
	}

	since := time.Date(2026, 8, 9, 12, 0, 0, 0, time.Local)
	stats, err := Collect(logPath, since)
	if err != nil {
		t.Fatal(err)
	}

	if len(stats.Files) != 2 {
		t.Fatalf("len(Files) = %d, want 2: %v", len(stats.Files), stats.Files)
	}
	if stats.Decisions != (Counts{Total: 2, Blocked: 1, Proxy: 1}) {
		t.Fatalf("Decisions = %#v", stats.Decisions)
	}
	if _, exists := stats.Domains["old.example"]; exists {
		t.Fatal("event older than cutoff was included")
	}
	if stats.Rules["blocked"].Blocked != 1 || stats.Rules["work"].Proxy != 1 {
		t.Fatalf("Rules = %#v", stats.Rules)
	}
}

func TestCollectIgnoresUnrelatedFileWithMatchingPrefix(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "goProxy.log")
	if err := os.WriteFile(logPath, []byte(""), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "goProxy-not-a-rotation.log"), []byte(""), 0600); err != nil {
		t.Fatal(err)
	}

	stats, err := Collect(logPath, time.Time{})
	if err != nil {
		t.Fatal(err)
	}
	if len(stats.Files) != 1 || stats.Files[0] != logPath {
		t.Fatalf("Files = %v, want only %s", stats.Files, logPath)
	}
}

func TestRotatedLogNames(t *testing.T) {
	tests := []struct {
		name     string
		stem     string
		ext      string
		want     bool
		compress bool
	}{
		{name: "goProxy-2026-08-09T12-02-00.000.log", stem: "goProxy", ext: ".log", want: true},
		{name: "goProxy-2026-08-09T12-02-00.000.log.gz", stem: "goProxy", ext: ".log", want: true, compress: true},
		{name: "goProxy.log-2026-08-09T12-02-00.000.gz", stem: "goProxy.log", ext: ".gz", want: true},
		{name: "goProxy.log-2026-08-09T12-02-00.000.gz.gz", stem: "goProxy.log", ext: ".gz", want: true, compress: true},
		{name: "goProxy-not-a-rotation.log", stem: "goProxy", ext: ".log", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isRotatedLogName(tt.name, tt.stem, tt.ext); got != tt.want {
				t.Fatalf("isRotatedLogName() = %v, want %v", got, tt.want)
			}
			logPath := filepath.Join("logs", tt.stem+tt.ext)
			path := filepath.Join("logs", tt.name)
			if got := isCompressedRotation(path, logPath); got != tt.compress {
				t.Fatalf("isCompressedRotation() = %v, want %v", got, tt.compress)
			}
		})
	}
}

func TestWriteSortsDomainsAndIncludesUnusedRule(t *testing.T) {
	stats := &Stats{
		Files:     []string{"goProxy.log"},
		Protocols: map[string]Counts{"HTTP": {Total: 3, Blocked: 1, Direct: 2}},
		Rules:     map[string]Counts{"used": {Total: 3, Blocked: 1, Direct: 2}},
		Domains: map[string]Counts{
			"less.example": {Total: 1, Direct: 1},
			"top.example":  {Total: 2, Blocked: 1, Direct: 1},
		},
		Decisions: Counts{Total: 3, Blocked: 1, Direct: 2},
	}
	stats.AddKnownRules([]string{"used", "unused"})

	var output bytes.Buffer
	if err := Write(&output, stats, 1); err != nil {
		t.Fatal(err)
	}
	text := output.String()
	if !strings.Contains(text, "unused") {
		t.Fatalf("report does not include unused rule:\n%s", text)
	}
	if !strings.Contains(text, "top.example") || strings.Contains(text, "less.example") {
		t.Fatalf("domain top limit was not applied:\n%s", text)
	}
	if !strings.Contains(text, "Blocked  1  (33.3%)") {
		t.Fatalf("summary percentage is missing:\n%s", text)
	}
}

func TestWriteReturnsWriterError(t *testing.T) {
	stats := &Stats{
		Protocols: make(map[string]Counts),
		Rules:     make(map[string]Counts),
		Domains:   make(map[string]Counts),
	}
	wantErr := errors.New("write failed")
	if err := Write(errorWriter{err: wantErr}, stats, 20); !errors.Is(err, wantErr) {
		t.Fatalf("Write() error = %v, want %v", err, wantErr)
	}
}

type errorWriter struct {
	err error
}

func (w errorWriter) Write([]byte) (int, error) {
	return 0, w.err
}

func writeGzipFile(t *testing.T, path, contents string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	gzWriter := gzip.NewWriter(file)
	if _, err := gzWriter.Write([]byte(contents)); err != nil {
		t.Fatal(err)
	}
	if err := gzWriter.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}
