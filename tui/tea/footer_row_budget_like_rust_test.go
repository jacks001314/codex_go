package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// TestStatusLineWrapsWithinRowBudgetLikeRust covers Rust #60680/#60681: the
// status surface owns rows separate from composer hints, wraps at the full
// available width, and yields rows to the draft on short terminals.
func TestStatusLineWrapsWithinRowBudgetLikeRust(t *testing.T) {
	inline := NewModel(codextui.NewState(nil), Options{Width: 30, Height: 24})
	inline.width, inline.height, inline.ownedScreen = 30, 24, false
	if got := inline.statusLineRowBudget(); got != 2 {
		t.Fatalf("inline status budget = %d, want 2", got)
	}
	fullscreen := NewModel(codextui.NewState(nil), Options{Width: 30, Height: 40})
	fullscreen.width, fullscreen.height, fullscreen.ownedScreen = 30, 40, true
	if got := fullscreen.statusLineRowBudget(); got != 4 {
		t.Fatalf("fullscreen status budget = %d, want 4", got)
	}
	short := NewModel(codextui.NewState(nil), Options{Width: 30, Height: 8})
	short.width, short.height, short.ownedScreen = 30, 8, true
	if got := short.statusLineRowBudget(); got != 1 {
		t.Fatalf("short status budget = %d, want 1", got)
	}

	status := "gpt-5 · context 92% left · feature/very-long-branch-name · sandbox workspace-write"
	section := inline.renderStatusLineSection(status)
	rows := strings.Split(section, "\n")
	if len(rows) < 2 || len(rows) > 2 {
		t.Fatalf("inline status rows = %d, want 2:\n%s", len(rows), section)
	}
	if got := len(wrapStatusLineRows(status, 12, 2)); got != 2 {
		t.Fatalf("wrapped rows = %d, want 2", got)
	}
	if clipped := wrapStatusLineRows(status, 12, 1); len(clipped) != 1 || !strings.HasSuffix(clipped[0], "…") {
		t.Fatalf("clipped status = %#v", clipped)
	}
}

// TestFooterHintsFitWholeHintsLikeRust covers Rust #60683: hints wrap as whole
// shortcut/label pairs and overflow hints are dropped rather than split.
func TestFooterHintsFitWholeHintsLikeRust(t *testing.T) {
	segments := []string{"← for agents", "? for shortcuts"}
	wide := renderFooterHintRows(segments, 80, 1)
	if len(wide) != 1 || !strings.Contains(wide[0], "for agents") || !strings.Contains(wide[0], "for shortcuts") {
		t.Fatalf("wide hint row = %#v", wide)
	}
	narrow := renderFooterHintRows(segments, 14, 1)
	if len(narrow) != 1 || narrow[0] != "← for agents" {
		t.Fatalf("narrow hint row = %#v", narrow)
	}
	full := renderFooterHintRows(segments, 14, 2)
	if len(full) != 2 || full[0] != "← for agents" || full[1] != "? for shortcuts" {
		t.Fatalf("full hint rows = %#v", full)
	}
}

// TestEmptyFooterHintRowYieldsToStatusLikeRust covers Rust #60681: a status
// line already occupies the footer, so no extra blank hint row is reserved.
func TestEmptyFooterHintRowYieldsToStatusLikeRust(t *testing.T) {
	if shouldRenderFooterHintRow("gpt-5", "") {
		t.Fatal("empty hint row rendered beside status content")
	}
	if !shouldRenderFooterHintRow("", "") {
		t.Fatal("empty hint row must remain when no status line is enabled")
	}
	if !shouldRenderFooterHintRow("gpt-5", "? for shortcuts") {
		t.Fatal("non-empty hint row was dropped")
	}
}
