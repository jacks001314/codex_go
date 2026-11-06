package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/utils"
)

func composerHintTestModel(t *testing.T, width int, show bool) *Model {
	t.Helper()
	model := NewModel(codextui.NewState(nil), Options{
		Width:        width,
		Height:       24,
		ShowTooltips: &show,
	})
	model.activityFollow = true
	return model
}

func composerGapTipText(t *testing.T, model *Model) string {
	t.Helper()
	view := utils.StripANSI(model.viewString())
	for _, line := range strings.Split(view, "\n") {
		if strings.Contains(line, "Tip: ") {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

// containsNonComposerTip reports a startup or turn tip while ignoring the
// contextual discovery tips added in #46739.
func containsNonComposerTip(view string) bool {
	for _, line := range strings.Split(view, "\n") {
		if !strings.Contains(line, "Tip: ") {
			continue
		}
		switch {
		case strings.Contains(line, "@ mentions files, skills, and plugins"):
		case strings.Contains(line, "searches this conversation"):
		case strings.Contains(line, "/copy copies the last response"):
		case strings.Contains(line, "drag to select text"):
		default:
			return true
		}
	}
	return false
}

// TestComposerHintRotationLikeRust covers #46739's contextual composer tips.
func TestComposerHintRotationLikeRust(t *testing.T) {
	model := composerHintTestModel(t, 80, true)
	if got := composerGapTipText(t, model); got != "Tip: @ mentions files, skills, and plugins" {
		t.Fatalf("fresh tip = %q", got)
	}

	model.State.AddMessage(codextui.RoleUser, "one")
	if got := composerGapTipText(t, model); got != "Tip: f3 searches this conversation" {
		t.Fatalf("one-prompt tip = %q", got)
	}
	model.State.AddMessage(codextui.RoleUser, "two")
	if got := composerGapTipText(t, model); got != "Tip: /copy copies the last response" {
		t.Fatalf("two-prompt tip = %q", got)
	}
	model.State.AddMessage(codextui.RoleUser, "three")
	if got := composerGapTipText(t, model); got != "Tip: drag to select text · ctrl+c copies" {
		t.Fatalf("three-prompt tip = %q", got)
	}

	// The tip is painted in the right side of the unused gap row.
	view := utils.StripANSI(model.viewString())
	line := ""
	for _, candidate := range strings.Split(view, "\n") {
		if strings.Contains(candidate, "Tip: ") {
			line = candidate
			break
		}
	}
	if !strings.HasPrefix(line, " ") {
		t.Fatalf("tip is not right-aligned in the gap row: %q", line)
	}
}

// TestComposerHintYieldRulesLikeRust keeps tips out of drafts, narrow screens,
// disabled settings, and active work.
func TestComposerHintYieldRulesLikeRust(t *testing.T) {
	narrow := composerHintTestModel(t, 32, true)
	if got := composerGapTipText(t, narrow); got != "" {
		t.Fatalf("narrow tip = %q, want none", got)
	}
	disabled := composerHintTestModel(t, 80, false)
	if got := composerGapTipText(t, disabled); got != "" {
		t.Fatalf("disabled tip = %q, want none", got)
	}
	draft := composerHintTestModel(t, 80, true)
	draft.setComposerPrompt("draft")
	if got := composerGapTipText(t, draft); got != "" {
		t.Fatalf("draft tip = %q, want none", got)
	}
	running := composerHintTestModel(t, 80, true)
	running.setStatus("running")
	if got := composerGapTipText(t, running); got != "" {
		t.Fatalf("running tip = %q, want none", got)
	}
}
