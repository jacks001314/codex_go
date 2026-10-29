package tea

import (
	"strings"
	"testing"
	"time"

	codextui "codex_go/tui"
)

func hookStatusTestModel(now *time.Time) (*Model, *codextui.State) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 100, Height: 30})
	model.now = func() time.Time { return *now }
	return model, state
}

// TestFastHookStaysHiddenLikeRust keeps hooks shorter than the 300ms reveal
// delay out of every TUI surface.
func TestFastHookStaysHiddenLikeRust(t *testing.T) {
	now := time.Unix(100, 0)
	model, state := hookStatusTestModel(&now)
	model.Update(HookRunMsg{ID: "fast", EventName: "preToolUse", StatusMessage: "checking", Running: true})
	model.Update(HookRunMsg{ID: "fast", EventName: "preToolUse", Status: "completed"})
	if view := strings.ToLower(model.View()); strings.Contains(view, "checking") || strings.Contains(view, "running hook") {
		t.Fatalf("fast hook was revealed:\n%s", model.View())
	}
	if got := countRole(state.Messages, codextui.RoleHistory); got != 0 {
		t.Fatalf("quiet successful hook entered history: %#v", state.Messages)
	}
}

// TestRunningHookIsIndependentOfTurnLikeRust shows activity outside a turn.
func TestRunningHookIsIndependentOfTurnLikeRust(t *testing.T) {
	now := time.Unix(200, 0)
	model, state := hookStatusTestModel(&now)
	model.Update(HookRunMsg{ID: "run", EventName: "preToolUse", StatusMessage: "checking policy", Running: true})
	now = now.Add(hookStatusRevealDelay)
	view := strings.ToLower(model.View())
	if !strings.Contains(view, "checking policy") {
		t.Fatalf("idle turn did not render hook activity:\n%s", model.View())
	}
	if got := countRole(state.Messages, codextui.RoleHistory); got != 0 {
		t.Fatalf("running hook entered history: %#v", state.Messages)
	}
}

// TestRunningHookFoldsIntoWorkingRowLikeRust uses the working row while a turn
// is active, avoiding a duplicate standalone activity line.
func TestRunningHookFoldsIntoWorkingRowLikeRust(t *testing.T) {
	now := time.Unix(250, 0)
	model, _ := hookStatusTestModel(&now)
	model.State.SetStatus("running")
	model.syncTaskRunningTimer()
	model.Update(HookRunMsg{ID: "run", EventName: "preToolUse", StatusMessage: "checking policy", Running: true})
	now = now.Add(hookStatusRevealDelay)
	view := strings.ToLower(model.View())
	if !strings.Contains(view, "working") || !strings.Contains(view, "checking policy") {
		t.Fatalf("working row did not include hook activity:\n%s", model.View())
	}
	if got := strings.Count(view, "checking policy"); got != 1 {
		t.Fatalf("hook activity rendered %d times, want one folded line:\n%s", got, model.View())
	}
}

// TestLongRunningHookReflowsBelowWorkingRowLikeRust keeps a too-wide summary
// on its own `\u2514` activity line, truncated to the row width like Rust.
func TestLongRunningHookReflowsBelowWorkingRowLikeRust(t *testing.T) {
	now := time.Unix(260, 0)
	state := codextui.NewState(nil)
	model := NewModel(state, Options{Width: 24, Height: 20})
	model.now = func() time.Time { return now }
	state.SetStatus("running")
	model.syncTaskRunningTimer()
	model.Update(HookRunMsg{
		ID:            "run",
		EventName:     "preToolUse",
		StatusMessage: "checking a deliberately long policy description",
		Running:       true,
	})
	now = now.Add(hookStatusRevealDelay)
	view := strings.ToLower(model.View())
	if !strings.Contains(view, "working") || !strings.Contains(view, "\u2514") || !strings.Contains(view, "checking a delib") {
		t.Fatalf("reflowed hook activity missing:\n%s", model.View())
	}
	if strings.Contains(view, "checking a deliberately long") {
		t.Fatalf("over-wide hook summary was not truncated:\n%s", model.View())
	}
}

// TestConcurrentHooksCollapseToSummaryLikeRust uses a shared label for mixed
// revealed messages and one message when all hooks agree.
func TestConcurrentHooksCollapseToSummaryLikeRust(t *testing.T) {
	now := time.Unix(300, 0)
	model, _ := hookStatusTestModel(&now)
	model.Update(HookRunMsg{ID: "a", EventName: "preToolUse", StatusMessage: "checking policy", Running: true})
	model.Update(HookRunMsg{ID: "b", EventName: "preToolUse", StatusMessage: "scanning secrets", Running: true})
	now = now.Add(hookStatusRevealDelay)
	if view := model.View(); !strings.Contains(view, "Running hooks") {
		t.Fatalf("mixed hook summary missing:\n%s", view)
	}

	model.Update(HookRunMsg{ID: "b", EventName: "preToolUse", Status: "completed"})
	model.Update(HookRunMsg{ID: "c", EventName: "preToolUse", StatusMessage: "checking policy", Running: true})
	now = now.Add(hookStatusRevealDelay)
	if view := model.View(); !strings.Contains(view, "Checking policy") {
		t.Fatalf("identical hook summary missing:\n%s", view)
	}
}

// TestHookContextIsSuppressedLikeRust removes model-facing context entries.
func TestHookContextIsSuppressedLikeRust(t *testing.T) {
	now := time.Unix(400, 0)
	model, state := hookStatusTestModel(&now)
	model.Update(HookRunMsg{
		ID:        "failed",
		EventName: "postToolUse",
		Status:    "failed",
		Entries: []HookOutputEntry{
			{Kind: "context", Text: "model-only context"},
			{Kind: "error", Text: "hook failed"},
		},
	})
	view := model.View()
	if strings.Contains(view, "model-only context") {
		t.Fatalf("hook context leaked into TUI:\n%s", view)
	}
	if !strings.Contains(view, "hook failed") {
		t.Fatalf("actionable hook failure missing:\n%s", view)
	}
	if got := countRole(state.Messages, codextui.RoleHistory); got != 1 {
		t.Fatalf("failure history count = %d", got)
	}
}
