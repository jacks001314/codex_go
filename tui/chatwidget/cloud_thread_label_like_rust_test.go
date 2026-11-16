package chatwidget

import (
	"strings"
	"testing"

	"github.com/rivo/uniseg"

	bottompane "codex_go/tui/bottom_pane"
)

// Rust #61742: status-line and terminal-title thread items carry the Cloud
// marker. The title fallback to the thread id is decorated before truncation.
func TestStatusSurfaceCloudThreadLabelsLikeRust(t *testing.T) {
	state := NewStatusControlsState(StatusControlsRuntime{
		ThreadID:    "thread-cloud",
		ThreadName:  "Fix cache",
		ThreadTitle: "Fix cache",
		ThreadCloud: true,
	})
	if value, ok := state.StatusLineValueForItem(bottompane.StatusLineThreadName); !ok || value != "☁︎ Fix cache" {
		t.Fatalf("cloud thread name = %q ok=%v", value, ok)
	}
	if value, ok := state.StatusLineValueForItem(bottompane.StatusLineThreadTitle); !ok || value != "☁︎ Fix cache" {
		t.Fatalf("cloud thread title = %q ok=%v", value, ok)
	}
	if value, ok := state.TerminalTitleValueForItem(TerminalTitleThread); !ok || value != "☁︎ Fix cache" {
		t.Fatalf("cloud terminal thread = %q ok=%v", value, ok)
	}
	if value, ok := state.TerminalTitleValueForItem(TerminalTitleThreadName); !ok || value != "☁︎ Fix cache" {
		t.Fatalf("cloud terminal thread name = %q ok=%v", value, ok)
	}

	unnamed := NewStatusControlsState(StatusControlsRuntime{ThreadID: "thread-cloud", ThreadCloud: true})
	if value, ok := unnamed.StatusLineValueForItem(bottompane.StatusLineThreadTitle); !ok || value != "☁︎ thread-cloud" {
		t.Fatalf("cloud thread-title fallback = %q ok=%v", value, ok)
	}
	if _, ok := unnamed.StatusLineValueForItem(bottompane.StatusLineThreadName); ok {
		t.Fatal("unnamed thread-name item must stay absent")
	}

	long := NewStatusControlsState(StatusControlsRuntime{
		ThreadID:    "thread-cloud",
		ThreadName:  strings.Repeat("x", 60),
		ThreadTitle: strings.Repeat("x", 60),
		ThreadCloud: true,
	})
	value, ok := long.TerminalTitleValueForItem(TerminalTitleThread)
	if !ok || !strings.HasPrefix(value, "☁︎ ") || !strings.HasSuffix(value, "...") {
		t.Fatalf("truncated cloud title = %q ok=%v", value, ok)
	}
	if graphemes := uniseg.GraphemeClusterCount(value); graphemes > 48 {
		t.Fatalf("cloud terminal title graphemes = %d, want <= 48", graphemes)
	}

	local := NewStatusControlsState(StatusControlsRuntime{ThreadID: "thread-local", ThreadName: "Fix cache", ThreadTitle: "Fix cache"})
	if value, _ := local.StatusLineValueForItem(bottompane.StatusLineThreadTitle); value != "Fix cache" {
		t.Fatalf("local thread title = %q", value)
	}
}
