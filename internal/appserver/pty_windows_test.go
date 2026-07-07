//go:build windows

package appserver

import "testing"

func TestWindowsTTYInputNormalizerMatchesRustSemantics(t *testing.T) {
	normalizer := &windowsTTYInputNormalizer{}
	if got := string(normalizer.Normalize([]byte("line\n"))); got != "line\r" {
		t.Fatalf("Normalize LF = %q, want CR", got)
	}
	if got := string(normalizer.Normalize([]byte("next\r"))); got != "next\r" {
		t.Fatalf("Normalize trailing CR = %q", got)
	}
	if got := string(normalizer.Normalize([]byte("\n"))); got != "" {
		t.Fatalf("Normalize split CRLF LF = %q, want empty", got)
	}
	if got := normalizer.Normalize([]byte{'X', '\b', '\n'}); string(got) != "X\x7f\r" {
		t.Fatalf("Normalize backspace/LF = %#v, want X DEL CR", got)
	}
}
