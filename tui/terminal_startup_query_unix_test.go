//go:build !windows

package tui

import (
	"strings"
	"testing"
)

func TestStartupProbeQueriesAlternateScrollBeforeDA1LikeRust(t *testing.T) {
	query := terminalStartupProbeQuery(false)
	if !strings.HasPrefix(query, "\x1b[?1007$p") {
		t.Fatalf("startup query = %q, want 1007 DECRQM first", query)
	}
	if strings.Index(query, "\x1b[?1007$p") > strings.Index(query, "\x1b[c") {
		t.Fatalf("startup query = %q, want 1007 before DA1", query)
	}
	if !strings.Contains(query, terminalColorProbeQuery) {
		t.Fatalf("startup query = %q, want the color query", query)
	}
}
