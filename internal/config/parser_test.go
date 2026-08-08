package config

import (
	"reflect"
	"testing"
)

func TestParseStringToListStripsInlineComments(t *testing.T) {
	input := `
example.com # hash comment
*.example.org // slash comment
# full hash comment
// full slash comment
example.net
`

	want := []string{
		"example.com",
		"*.example.org",
		"example.org",
		"example.net",
	}

	if got := parseStringToList(input, true); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseStringToList() = %#v, want %#v", got, want)
	}
}

func TestParseStringToListPreservesURLMarkers(t *testing.T) {
	input := `
https://example.com/hosts.txt # external list
http://example.org/path//segment // external list
https://example.net/list#section
`

	want := []string{
		"https://example.com/hosts.txt",
		"http://example.org/path//segment",
		"https://example.net/list#section",
	}

	if got := parseStringToList(input, false); !reflect.DeepEqual(got, want) {
		t.Fatalf("parseStringToList() = %#v, want %#v", got, want)
	}
}
