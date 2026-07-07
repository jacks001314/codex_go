package model

import "testing"

func TestSupportsResponses(t *testing.T) {
	if !OllamaSupportsResponses(OllamaVersion{}) {
		t.Fatalf("dev zero version should be supported")
	}
	if OllamaSupportsResponses(OllamaVersion{Major: 0, Minor: 13, Patch: 3}) {
		t.Fatalf("0.13.3 should not support responses")
	}
	if !OllamaSupportsResponses(OllamaVersion{Major: 0, Minor: 13, Patch: 4}) {
		t.Fatalf("0.13.4 should support responses")
	}
}

func TestParseVersion(t *testing.T) {
	got, ok := ParseOllamaVersion("0.14.1-rc1")
	if !ok || got != (OllamaVersion{Major: 0, Minor: 14, Patch: 1}) {
		t.Fatalf("ParseOllamaVersion() = %+v, %v", got, ok)
	}
}
