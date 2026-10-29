package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/utils"
)

// Mirrors Rust #45980: a read-only snapshot recovered from turn summaries
// shows the intermediate-activity notice in the transcript on both the resume
// and the agent-switch paths, while a complete snapshot stays quiet.
func TestReadOnlySummaryHistoryNoticeLikeRust(t *testing.T) {
	const notice = "Showing up to 100 recent prompts and final replies. Intermediate messages and tool activity are unavailable."
	const marker = "Showing up to 100 recent prompts"

	resumed := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	resumed.applyResumeResponse("thread-a", SessionResumeResponse{
		Summary: &codextui.SessionSummary{ThreadID: "thread-a"},
		Messages: []codextui.Message{
			{Role: codextui.RoleUser, Text: "recent prompt"},
			{Role: codextui.RoleAssistant, Text: "final reply"},
		},
		ReadOnly:      true,
		HistoryNotice: notice,
	})
	if view := utils.StripANSI(resumed.View()); !strings.Contains(view, marker) {
		t.Fatalf("resume view missing summary history notice:\n%s", view)
	}

	switched := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	switched.applyAgentSwitchResult(AgentSwitchResultMsg{
		ThreadID: "thread-b",
		Response: AgentThreadSwitchResponse{
			Entry:         codextui.AgentThreadEntry{ThreadID: "thread-b"},
			Status:        "idle",
			ReadOnly:      true,
			HistoryNotice: notice,
		},
	})
	if view := utils.StripANSI(switched.View()); !strings.Contains(view, marker) {
		t.Fatalf("agent switch view missing summary history notice:\n%s", view)
	}

	complete := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 40})
	complete.applyResumeResponse("thread-c", SessionResumeResponse{
		Summary:  &codextui.SessionSummary{ThreadID: "thread-c"},
		ReadOnly: true,
	})
	if view := utils.StripANSI(complete.View()); strings.Contains(view, marker) {
		t.Fatalf("complete snapshot must not show the summary notice:\n%s", view)
	}
}
