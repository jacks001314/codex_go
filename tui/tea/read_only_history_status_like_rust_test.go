package tea

import (
	"strings"
	"testing"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

// TestReadOnlyHistoryHidesStatusLineUntilInteractiveResumeLikeRust covers the
// Go TUI boundary of Rust #61219: saved history and unavailable conversations
// hide the status line/context usage, token-usage notifications do not bring it
// back, and an interactive resume restores it even with read-only permissions.
func TestReadOnlyHistoryHidesStatusLineUntilInteractiveResumeLikeRust(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{
		Width:           100,
		Height:          30,
		StatusLineItems: []string{"session-id"},
	})

	model.applyResumeResponse("thread-history", SessionResumeResponse{Status: "idle"})
	if !strings.Contains(model.statusLineContent(), "thread-history") {
		t.Fatalf("live session status line missing thread id: %q", model.statusLineContent())
	}

	window := int64(100000)
	usage := &protocol.ThreadTokenUsage{
		Total:              protocol.Usage{InputTokens: 10, TotalTokens: 10},
		Last:               protocol.Usage{InputTokens: 10, TotalTokens: 10},
		ModelContextWindow: &window,
	}

	model.applyResumeResponse("thread-history", SessionResumeResponse{Status: "idle", ReadOnly: true})
	if got := model.statusLineContent(); got != "" {
		t.Fatalf("read-only history status line = %q, want hidden", got)
	}
	model.applyTokenUsage(usage)
	if got := model.statusLineContent(); got != "" {
		t.Fatalf("token usage revived read-only history status line: %q", got)
	}

	// A live resume with a read-only permission profile is still interactive:
	// the status line must return because the permission profile is not a
	// history view.
	model.State.Sandbox = "read-only"
	model.applyResumeResponse("thread-history", SessionResumeResponse{Status: "idle"})
	if !strings.Contains(model.statusLineContent(), "thread-history") {
		t.Fatalf("interactive read-only resume status line missing thread id: %q", model.statusLineContent())
	}

	model.setThreadUnavailable(true)
	if got := model.statusLineContent(); got != "" {
		t.Fatalf("unavailable conversation status line = %q, want hidden", got)
	}
	model.applyTokenUsage(usage)
	if got := model.statusLineContent(); got != "" {
		t.Fatalf("token usage revived unavailable status line: %q", got)
	}
	model.setThreadUnavailable(false)
	if !strings.Contains(model.statusLineContent(), "thread-history") {
		t.Fatalf("recovered conversation status line missing thread id: %q", model.statusLineContent())
	}
}
