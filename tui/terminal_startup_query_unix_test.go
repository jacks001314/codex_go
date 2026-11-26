//go:build !windows

package tui

import (
	"strings"
	"testing"
)

func TestStartupProbeQueriesAlternateScrollBeforeDA1LikeRust(t *testing.T) {
	query := terminalStartupProbeQuery(false, "")
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

// Rust terminal_probe::startup sends the Kitty graphics query after the
// alternate-scroll query and before DA1, so DA1 still acts as the barrier.
func TestStartupProbeSendsKittyGraphicsBeforeDA1LikeRust(t *testing.T) {
	query := terminalStartupProbeQuery(false, kittyGraphicsQuery)
	if strings.Index(query, kittyGraphicsQuery) < strings.Index(query, "\x1b[?1007$p") {
		t.Fatalf("startup query = %q, want the Kitty query after 1007", query)
	}
	if strings.Index(query, kittyGraphicsQuery) > strings.Index(query, "\x1b[c") {
		t.Fatalf("startup query = %q, want the Kitty query before DA1", query)
	}
	if query == terminalStartupProbeQuery(false, "") {
		t.Fatal("the registry-less query did not carry the Kitty graphics query")
	}
}
