package tea

import (
	"bytes"
	"strings"
	"testing"
)

// TestResolveProgramSizeExplicitPreserved guards against the startup size
// probe overriding sizes that callers explicitly configured.
func TestResolveProgramSizeExplicitPreserved(t *testing.T) {
	options := Options{Width: 72, Height: 18}
	resolved := resolveProgramSize(options, strings.NewReader("input"), &bytes.Buffer{})
	if resolved.Width != 72 || resolved.Height != 18 {
		t.Fatalf("resolveProgramSize changed explicit size to %dx%d", resolved.Width, resolved.Height)
	}
}

// TestResolveProgramSizeNoTerminal verifies that without a terminal the size
// probe falls back to the defaults instead of crashing.
func TestResolveProgramSizeNoTerminal(t *testing.T) {
	options := resolveProgramSize(Options{}, strings.NewReader("input"), &bytes.Buffer{})
	if options.Width < 0 || options.Height < 0 {
		t.Fatalf("resolveProgramSize produced negative size %dx%d", options.Width, options.Height)
	}
	if options.Width == 0 && options.Height == 0 {
		return // no terminal detected; caller keeps defaults
	}
	if options.Width <= 0 || options.Height <= 0 {
		t.Fatalf("resolveProgramSize produced partial size %dx%d", options.Width, options.Height)
	}
}
