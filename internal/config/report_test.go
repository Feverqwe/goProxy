package config

import "testing"

func TestReportTopDomainsDefault(t *testing.T) {
	tests := []struct {
		name  string
		value int
		want  int
	}{
		{name: "missing from older config", value: 0, want: 20},
		{name: "configured", value: 50, want: 50},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &ProxyConfig{ReportTopDomains: tt.value}
			if got := cfg.GetReportTopDomains(); got != tt.want {
				t.Fatalf("GetReportTopDomains() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestDefaultConfigIncludesReportTopDomains(t *testing.T) {
	if got := defaultConfig().ReportTopDomains; got != 20 {
		t.Fatalf("default ReportTopDomains = %d, want 20", got)
	}
}
