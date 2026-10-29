package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

// Rust parity: codex-rs/tui/src/app_server_session.rs (#45519). A resume
// response carries the thread's restored collaboration mode, and the TUI must
// use it for the session, including the first prompt after the resume.
func TestResumeRestoresCollaborationModeLikeRust(t *testing.T) {
	effort := "high"
	state := codextui.NewState(&codextui.Options{Model: "gpt-5.4", ReasoningEffort: "medium"})
	state.SetThreadID("thread-plan")
	model := NewModel(state, Options{
		Width:           100,
		Height:          24,
		FeatureSettings: map[string]bool{"collaboration_modes": true},
	})

	model.applyResumeResponse("thread-plan", SessionResumeResponse{
		Summary: &codextui.SessionSummary{ThreadID: "thread-plan"},
		ThreadSettings: &appserver.Settings{
			Model:  "gpt-5.4",
			Effort: &effort,
			CollaborationMode: map[string]any{
				"mode": "plan",
				"settings": map[string]any{
					"model":                  "gpt-5.4",
					"reasoning_effort":       "high",
					"developer_instructions": "Keep planning",
				},
			},
		},
	})

	if !state.PlanMode {
		t.Fatal("PlanMode = false, want the resumed plan mode")
	}
	if got := state.EffectiveReasoningEffort(); got != "high" {
		t.Fatalf("plan reasoning effort = %q, want the resumed effort", got)
	}

	typeText(t, model, "Continue planning")
	model.Update(key(bubbletea.KeyEnter))
	requests := model.SubmittedRequests()
	if len(requests) != 1 || requests[0].CollaborationMode == nil {
		t.Fatalf("resumed first prompt requests = %#v", requests)
	}
	if requests[0].CollaborationMode.Mode != chatwidget.CollaborationModeKindPlan {
		t.Fatalf("first prompt mode = %#v, want plan", requests[0].CollaborationMode)
	}
}

// Rust preserves the local selection for older servers that omit the mode.
func TestResumeWithoutReportedModeKeepsLocalSelectionLikeRust(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5.4"})
	state.SetThreadID("thread-local")
	state.PlanMode = true
	model := NewModel(state, Options{Width: 100, Height: 24})

	model.applyResumeResponse("thread-local", SessionResumeResponse{
		Summary:        &codextui.SessionSummary{ThreadID: "thread-local"},
		ThreadSettings: &appserver.Settings{Model: "gpt-5.4"},
	})
	if !state.PlanMode {
		t.Fatal("an older server without a mode cleared the local plan selection")
	}

	plainState := codextui.NewState(&codextui.Options{Model: "gpt-5.4"})
	plainState.SetThreadID("thread-plain")
	plainModel := NewModel(plainState, Options{Width: 100, Height: 24})
	plainModel.applyResumeResponse("thread-plain", SessionResumeResponse{
		Summary:        &codextui.SessionSummary{ThreadID: "thread-plain"},
		ThreadSettings: &appserver.Settings{Model: "gpt-5.4"},
	})
	if plainState.PlanMode {
		t.Fatal("an older server without a mode enabled plan mode")
	}
}
