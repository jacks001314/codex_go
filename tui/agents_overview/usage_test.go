package agentsoverview

import (
	"strings"
	"testing"
)

// TestRenderDetailsShowsUsageLines covers Rust #44970: the task details render
// the token totals and usage estimate after the model row.
func TestRenderDetailsShowsUsageLines(t *testing.T) {
	view := New(sampleRows(), "", false)
	view.SetUsageLines("t-1", []string{"Tokens: 13K in \u00b7 4K out", "Est. usage: 3.4 credits \u00b7 ~$0.14"})
	joined := strings.Join(view.Render(120, 24), "\n")
	for _, want := range []string{"Tokens: 13K in \u00b7 4K out", "Est. usage: 3.4 credits \u00b7 ~$0.14"} {
		if !strings.Contains(joined, want) {
			t.Errorf("details pane missing %q:\n%s", want, joined)
		}
	}
}

// TestRenderDetailsPrefersUsageOverPrompt covers Rust #44970's height
// precedence: with usage present, the original prompt is dropped rather than
// truncating the usage lines.
func TestRenderDetailsPrefersUsageOverPrompt(t *testing.T) {
	view := New(sampleRows(), "", false)
	view.SetUsageLines("t-1", []string{"Tokens: 13K in \u00b7 4K out", "Est. usage: 3.4 credits \u00b7 ~$0.14"})
	joined := strings.Join(view.Render(120, 17), "\n")
	if !strings.Contains(joined, "Est. usage: 3.4 credits") {
		t.Fatalf("usage lines were dropped:\n%s", joined)
	}
	if strings.Contains(joined, "Prompt") {
		t.Fatalf("prompt should be dropped when usage needs the space:\n%s", joined)
	}
}

// TestApplyRefreshPreservesUsageAndPrunesMissingThreads covers the refresh
// lifecycle: usage lines survive a row refresh and are dropped with the thread.
func TestApplyRefreshPreservesUsageAndPrunesMissingThreads(t *testing.T) {
	rows := sampleRows()
	view := New(rows, "", false)
	view.SetUsageLines("t-1", []string{"Tokens: 13K in \u00b7 4K out"})
	view.SetUsageLines("t-gone", []string{"Tokens: 1 in"})
	view.ApplyRefresh(rows, "")
	if got := view.usageLinesFor("t-1"); len(got) != 1 {
		t.Fatalf("usage lines after refresh = %#v", got)
	}
	if got := view.usageLinesFor("t-gone"); len(got) != 0 {
		t.Fatalf("removed thread usage = %#v, want pruned", got)
	}
}
