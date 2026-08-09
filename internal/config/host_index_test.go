package config

import (
	"bytes"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"
)

func TestCompactStringSetGrowsAndDeduplicates(t *testing.T) {
	var set compactStringSet
	for i := range 10_000 {
		value := fmt.Appendf(nil, "domain-%05d.example", i)
		if !set.add(value) {
			t.Fatalf("add(%q) failed", value)
		}
	}
	if !set.add([]byte("domain-00042.example")) {
		t.Fatal("adding duplicate failed")
	}
	if set.count != 10_000 {
		t.Fatalf("set count = %d, want 10000", set.count)
	}
	for _, value := range []string{"domain-00000.example", "domain-00042.example", "domain-09999.example"} {
		if !set.has(value) {
			t.Errorf("set does not contain %q", value)
		}
	}
	if set.has("missing.example") {
		t.Fatal("set contains a value that was not added")
	}
}

func TestHostRuleBuilderIndexesSimpleRules(t *testing.T) {
	var builder hostRuleBuilder
	for _, token := range []string{
		"*.example.com",
		"exact.example.net",
		"!*.allowed.example.com",
		"!exact.allowed.example.net",
		"api.*.example.org",
		"*.prefix*.example.org",
	} {
		builder.addString(token, false)
	}

	for _, host := range []string{"example.com", "www.example.com", "deep.www.example.com", "exact.example.net"} {
		positive, negative := builder.index.match(host)
		if !positive || negative {
			t.Errorf("match(%q) = (%v, %v), want (true, false)", host, positive, negative)
		}
	}
	for _, host := range []string{"allowed.example.com", "www.allowed.example.com", "exact.allowed.example.net"} {
		_, negative := builder.index.match(host)
		if !negative {
			t.Errorf("match(%q) did not find negative rule", host)
		}
	}
	if positive, negative := builder.index.match("notexample.com"); positive || negative {
		t.Fatalf("match(notexample.com) = (%v, %v), want no match", positive, negative)
	}
	wantPatterns := []string{"api.*.example.org", "*.prefix*.example.org", "prefix*.example.org"}
	if !reflect.DeepEqual(builder.patterns, wantPatterns) {
		t.Fatalf("fallback patterns = %q, want %q", builder.patterns, wantPatterns)
	}
}

func TestHostRuleBuilderNormalizesDNSCaseAndTrailingDot(t *testing.T) {
	var builder hostRuleBuilder
	builder.addString("*.Example.COM.", false)
	builder.addString("!Allowed.Example.COM.", false)

	for _, host := range []string{"example.com", "www.example.com"} {
		positive, negative := builder.index.match(host)
		if !positive || negative {
			t.Errorf("match(%q) = (%v, %v), want (true, false)", host, positive, negative)
		}
	}
	if positive, negative := builder.index.match("allowed.example.com"); !positive || !negative {
		t.Fatalf("match(allowed.example.com) = (%v, %v), want (true, true)", positive, negative)
	}
}

func TestScanRuleTokensStreamsLongListsAndComments(t *testing.T) {
	input := "  # comment\n// another comment\n*.one.example,*.two.example\nhttps://rules.example/list\n" +
		strings.Repeat("x", 70*1024) + "\n"
	var got []string
	if err := scanRuleTokens(strings.NewReader(input), func(token []byte) {
		got = append(got, string(token))
	}); err != nil {
		t.Fatalf("scanRuleTokens() error: %v", err)
	}

	wantPrefix := []string{"*.one.example", "*.two.example", "https://rules.example/list"}
	if !reflect.DeepEqual(got[:3], wantPrefix) {
		t.Fatalf("tokens prefix = %q, want %q", got[:3], wantPrefix)
	}
	if len(got) != 4 || len(got[3]) != 70*1024 {
		t.Fatalf("long token was not preserved: count=%d length=%d", len(got), len(got[3]))
	}
}

func BenchmarkBuildTwoMillionWildcardHostIndex(b *testing.B) {
	var input bytes.Buffer
	input.Grow(48_000_000)
	for i := range 2_000_000 {
		fmt.Fprintf(&input, "*.domain-%07d.example\n", i)
	}
	data := input.Bytes()

	b.ReportAllocs()
	b.SetBytes(int64(len(data)))
	b.ResetTimer()
	for range b.N {
		var capacity hostRuleCapacity
		if err := scanRuleTokens(bytes.NewReader(data), func(token []byte) {
			capacity.observe(token, false)
		}); err != nil {
			b.Fatal(err)
		}
		var builder hostRuleBuilder
		builder.reserve(capacity)
		if err := scanRuleTokens(bytes.NewReader(data), func(token []byte) {
			builder.add(token, false)
		}); err != nil {
			b.Fatal(err)
		}
		if builder.index.positiveSuffix.count != 2_000_000 {
			b.Fatalf("indexed %d domains", builder.index.positiveSuffix.count)
		}
	}
}

func BenchmarkBuildHostIndexFromFile(b *testing.B) {
	path := os.Getenv("GOPROXY_RULE_BENCH_FILE")
	if path == "" {
		b.Skip("set GOPROXY_RULE_BENCH_FILE to a host rule list")
	}
	info, err := os.Stat(path)
	if err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.SetBytes(info.Size())
	var indexedRules uint32
	var indexBytes int
	b.ResetTimer()
	for range b.N {
		file, err := os.Open(path)
		if err != nil {
			b.Fatal(err)
		}

		var capacity hostRuleCapacity
		if err := scanRuleTokens(file, func(token []byte) {
			capacity.observe(token, false)
		}); err != nil {
			file.Close()
			b.Fatal(err)
		}
		var builder hostRuleBuilder
		builder.reserve(capacity)
		if _, err := file.Seek(0, 0); err != nil {
			file.Close()
			b.Fatal(err)
		}
		if err := scanRuleTokens(file, func(token []byte) {
			builder.add(token, false)
		}); err != nil {
			file.Close()
			b.Fatal(err)
		}
		if err := file.Close(); err != nil {
			b.Fatal(err)
		}

		indexedRules = builder.index.positiveSuffix.count
		indexBytes = len(builder.index.positiveSuffix.data) + len(builder.index.positiveSuffix.table)*4
	}
	b.ReportMetric(float64(indexedRules), "rules")
	b.ReportMetric(float64(indexBytes), "index-B")
}
