package app

import (
	"strings"
	"testing"
)

func TestInteractiveAutoThreadTitle(t *testing.T) {
	cases := map[string]string{
		"Release triage for the API": "Release triage for the API",
		"   fix   spacing   here   ": "fix spacing here",
		"":                           "",
		strings.Repeat("long", 20):   strings.Repeat("long", 14) + "lon\u2026",
	}
	for input, want := range cases {
		got := interactiveAutoThreadTitle(input)
		if got != want {
			t.Fatalf("interactiveAutoThreadTitle(%q) = %q, want %q", input, got, want)
		}
		if len([]rune(got)) > 60 {
			t.Fatalf("title too long: %q", got)
		}
	}
}
