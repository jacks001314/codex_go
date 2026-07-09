package bottompane

import (
	"strings"
	"testing"

	"codex_go/internal/tui"
)

func TestFooterModeTransitionsMatchRust(t *testing.T) {
	if got := ToggleShortcutMode(FooterModeComposerEmpty, false, true); got != FooterModeShortcutOverlay {
		t.Fatalf("toggle empty = %s", got)
	}
	if got := ToggleShortcutMode(FooterModeShortcutOverlay, false, false); got != FooterModeComposerHasDraft {
		t.Fatalf("toggle overlay = %s", got)
	}
	if got := ToggleShortcutMode(FooterModeQuitShortcutReminder, true, true); got != FooterModeQuitShortcutReminder {
		t.Fatalf("quit reminder should be sticky while ctrl-c hint is active, got %s", got)
	}
	if got := EscHintMode(FooterModeComposerEmpty, true); got != FooterModeComposerEmpty {
		t.Fatalf("esc while running = %s", got)
	}
	if got := EscHintMode(FooterModeComposerEmpty, false); got != FooterModeEscHint {
		t.Fatalf("esc idle = %s", got)
	}
	if got := ResetFooterModeAfterActivity(FooterModeShortcutOverlay); got != FooterModeComposerEmpty {
		t.Fatalf("reset shortcut overlay = %s", got)
	}
}

func TestFooterLinesStatusAgentAndShortcutOverlayMatchRustCore(t *testing.T) {
	props := FooterProps{
		Mode:              FooterModeComposerEmpty,
		StatusLineEnabled: true,
		StatusLineValue:   "model: gpt-5",
		ActiveAgentLabel:  "Robie [explorer]",
	}
	lines := FooterLines(props)
	if len(lines) != 1 || lines[0] != "model: gpt-5"+FooterContextJoiner+"Robie [explorer]" {
		t.Fatalf("status/agent footer = %#v", lines)
	}
	props.StatusLineEnabled = false
	props.StatusLineValue = ""
	lines = FooterLines(props)
	if len(lines) != 1 || lines[0] != "Robie [explorer]" {
		t.Fatalf("active agent footer = %#v", lines)
	}
	props.StatusLineEnabled = true
	props.StatusLineValue = "model: gpt-5"
	props.Mode = FooterModeShortcutOverlay
	props.CollaborationModesEnabled = true
	props.UseShiftEnterHint = true
	lines = FooterLines(props)
	for _, want := range []string{
		"/ for commands",
		"! for shell commands",
		"Shift+Enter for newline",
		"Tab to submit message",
		"@ for file paths",
		"Ctrl+V to paste images",
		"Ctrl+R search history",
		"Ctrl+T to view transcript",
		"Ctrl+G to edit in external editor",
		"Shift+Tab to change mode",
		"Ctrl+C to exit",
		"customize shortcuts with /keymap",
	} {
		if !footerContainsLine(lines, want) {
			t.Fatalf("shortcut overlay missing %q: %#v", want, lines)
		}
	}
}

func TestFooterDraftQueueEscQuitAndHeight(t *testing.T) {
	queue := FooterProps{Mode: FooterModeComposerHasDraft, IsTaskRunning: true}
	if lines := FooterLines(queue); len(lines) != 1 || lines[0] != "Tab to queue message" {
		t.Fatalf("queue lines = %#v", lines)
	}
	draft := FooterProps{Mode: FooterModeComposerHasDraft, UseShiftEnterHint: true}
	if lines := FooterLines(draft); len(lines) != 1 || lines[0] != "" {
		t.Fatalf("draft lines = %#v", lines)
	}
	esc := FooterProps{Mode: FooterModeEscHint, EscBacktrackHint: true}
	if lines := FooterLines(esc); lines[0] != "Esc again to edit previous message" {
		t.Fatalf("esc lines = %#v", lines)
	}
	quit := FooterProps{Mode: FooterModeQuitShortcutReminder, IsTaskRunning: true}
	if lines := FooterLines(quit); lines[0] != "Ctrl+C again to quit" {
		t.Fatalf("quit lines = %#v", lines)
	}
	history := FooterProps{Mode: FooterModeHistorySearch}
	if lines := FooterLines(history); lines[0] != "reverse-i-search: " {
		t.Fatalf("history lines = %#v", lines)
	}
	if FooterHeight(FooterProps{Mode: FooterModeShortcutOverlay}) <= 1 {
		t.Fatal("shortcut overlay should reserve multiple footer rows")
	}
}

func TestSingleLineFooterLayoutCollapseAndRender(t *testing.T) {
	props := FooterProps{
		Mode:                       FooterModeComposerEmpty,
		CollaborationModeIndicator: CollaborationModePlan,
		ShowCycleHint:              true,
		StatusLineEnabled:          true,
		StatusLineValue:            "ctx 50%",
	}
	layout := ComputeSingleLineFooterLayout(120, len("ctx 50%"), props, true, false)
	if layout.LeftKind != SummaryLeftDefault || !layout.ShowContext {
		t.Fatalf("wide layout = %#v", layout)
	}
	line := RenderSingleLineFooter(120, props)
	for _, want := range []string{"? for shortcuts", "Plan mode", FooterModeCycleHint, "ctx 50%"} {
		if !strings.Contains(line, want) {
			t.Fatalf("wide footer missing %q: %q", want, line)
		}
	}
	narrow := RenderSingleLineFooter(30, props)
	if tui.DisplayWidth(narrow) > 30 {
		t.Fatalf("narrow footer too wide: %q width=%d", narrow, tui.DisplayWidth(narrow))
	}
	if !strings.Contains(narrow, "Plan mode") && !strings.Contains(narrow, "ctx 50%") {
		t.Fatalf("narrow footer dropped both mode and context: %q", narrow)
	}

	queue := FooterProps{Mode: FooterModeComposerHasDraft, IsTaskRunning: true, StatusLineEnabled: true, StatusLineValue: "ctx"}
	queueLayout := ComputeSingleLineFooterLayout(16, len("ctx"), queue, false, true)
	if queueLayout.Left != "Tab to queue" || queueLayout.ShowContext {
		t.Fatalf("queue fallback = %#v", queueLayout)
	}
}

func TestFooterStatusIndicatorsMatchRustText(t *testing.T) {
	usage := "10K / 20K"
	active := &FooterGoalStatusIndicator{Kind: GoalStatusActive, Usage: usage, HasUsage: true}
	if got, ok := GoalStatusIndicatorLine(active); !ok || got != "Pursuing goal (10K / 20K)" {
		t.Fatalf("active goal = %q ok=%v", got, ok)
	}
	if got, ok := GoalStatusIndicatorLine(&FooterGoalStatusIndicator{Kind: GoalStatusBudgetLimited}); !ok || got != "Goal abandoned" {
		t.Fatalf("budget limited = %q ok=%v", got, ok)
	}
	if got := StatusLineRightIndicatorLine(CollaborationModePlan, active, true, true); got != "Plan mode (shift+tab to cycle)"+FooterContextJoiner+"IDE context" {
		t.Fatalf("mode right indicator = %q", got)
	}
	if got := StatusLineRightIndicatorLine("", active, true, false); got != "Pursuing goal (10K / 20K)"+FooterContextJoiner+"IDE context" {
		t.Fatalf("goal right indicator = %q", got)
	}
}

func TestContextWindowLineMatchesRust(t *testing.T) {
	over := int64(120)
	if got := ContextWindowLine(&over, nil); got != "100% context left" {
		t.Fatalf("clamped percent = %q", got)
	}
	under := int64(-1)
	if got := ContextWindowLine(&under, nil); got != "0% context left" {
		t.Fatalf("negative percent = %q", got)
	}
	used := int64(15320)
	if got := ContextWindowLine(nil, &used); got != "15.3K used" {
		t.Fatalf("used tokens = %q", got)
	}
	if got := ContextWindowLine(nil, nil); got != "100% context left" {
		t.Fatalf("default context = %q", got)
	}
}

func footerContainsLine(lines []string, want string) bool {
	for _, line := range lines {
		if line == want {
			return true
		}
	}
	return false
}
