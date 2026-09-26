package tea

import (
	"strings"
	"testing"
	"time"

	"codex_go/protocol"
	codextui "codex_go/tui"
	"codex_go/utils"
)

// Mirrors Rust's turn-tips lifecycle: the working tip appears only after the
// working delay, stays for the rest of the turn, and exposure is counted once.
func TestTurnTipsWorkingTipAppearsAfterDelayLikeRust(t *testing.T) {
	enabled := true
	base := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 30, ShowTooltips: &enabled})
	model.now = func() time.Time { return base }
	model.Update(ThreadEventMsg{Event: protocol.TurnStarted()})

	if view := utils.StripANSI(model.View()); strings.Contains(view, "\u2514 ") {
		t.Fatalf("tip rendered before the working delay:\n%s", view)
	}
	model.now = func() time.Time { return base.Add(turnTipWorkingDelay + time.Second) }
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "\u2514 ") {
		t.Fatalf("working tip missing after the delay:\n%s", view)
	}
	tip := strings.TrimSpace(view[strings.Index(view, "\u2514 ")+len("\u2514 "):])
	tip = strings.TrimSpace(strings.SplitN(tip, "\n", 2)[0])
	if tip == "" || strings.ContainsAny(tip, "\r\n") {
		t.Fatalf("working tip = %q", tip)
	}
	// The tip stays for the rest of the turn and its template does not change.
	model.now = func() time.Time { return base.Add(2 * time.Minute) }
	again := utils.StripANSI(model.View())
	if !strings.Contains(again, tip) {
		t.Fatalf("working tip did not persist:\n%s", again)
	}
}

// Mirrors Rust's `render_tooltip_lines`: the resolved tip is prefixed with
// Markdown bold `Tip:` and rendered through the Markdown renderer, and a
// template whose rendering is not a single fitting line is skipped.
func TestRenderTurnTipLineUsesMarkdownTipLinesLikeRust(t *testing.T) {
	line, ok := renderTurnTipLine(
		"Start a fresh idea with **/new**; the previous session stays in history.",
		100, nil, "", "")
	if !ok {
		t.Fatal("key-free tip did not render")
	}
	if !strings.Contains(utils.StripANSI(line), "Tip: Start a fresh idea") {
		t.Fatalf("tip line = %q, want the Markdown Tip: prefix", utils.StripANSI(line))
	}
	if !strings.Contains(line, "\x1b[1m") {
		t.Fatalf("tip line = %q, want the bold Tip: styling", line)
	}
	// Rust rejects a template that wraps at the available width (its `[line]`
	// single-line match), so the picker tries the next one.
	if _, ok := renderTurnTipLine("Start a fresh idea with **/new**.", 8, nil, "", ""); ok {
		t.Fatal("a wrapping tip still produced a single line")
	}
	// A placeholder with no keymap is skipped before rendering.
	if _, ok := renderTurnTipLine("Press {key:composer.queue} to queue a message.", 100, nil, "", ""); ok {
		t.Fatal("an unbound placeholder produced a tip line")
	}
}

// Mirrors Rust's gates: the preference, an empty composer, no modal and a
// running turn are all required before a tip may be shown.
func TestTurnTipsHonorShowTooltipsAndGatesLikeRust(t *testing.T) {
	disabled := false
	base := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 30, ShowTooltips: &disabled})
	model.now = func() time.Time { return base.Add(time.Minute) }
	model.Update(ThreadEventMsg{Event: protocol.TurnStarted()})
	if view := utils.StripANSI(model.View()); strings.Contains(view, "\u2514 ") {
		t.Fatalf("disabled tooltips rendered a tip:\n%s", view)
	}

	enabled := true
	model = NewModel(codextui.NewState(nil), Options{Width: 120, Height: 30, ShowTooltips: &enabled})
	model.now = func() time.Time { return base.Add(time.Minute) }
	model.Update(ThreadEventMsg{Event: protocol.TurnStarted()})
	model.composer.InsertString("draft")
	if view := utils.StripANSI(model.View()); strings.Contains(view, "\u2514 ") {
		t.Fatalf("a non-empty composer rendered a tip:\n%s", view)
	}
}

// Mirrors Rust's completion cadence constants: completion tips start at the
// third turn start, are spaced by three turn starts, and at most two are shown.
func TestTurnTipsCompletionCadenceLikeRust(t *testing.T) {
	var tips turnTips
	base := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	for index := 0; index < 3; index++ {
		tips.observeTurnStarted("thread-1", turnIDForTest(index), base.Add(time.Duration(index)*time.Minute))
	}
	if tips.starts != 3 {
		t.Fatalf("starts = %d, want 3", tips.starts)
	}
	// A repeated start for the same turn does not count again.
	tips.observeTurnStarted("thread-1", turnIDForTest(2), base.Add(4*time.Minute))
	if tips.starts != 3 {
		t.Fatalf("repeated start counted: %d", tips.starts)
	}
	tips.observeRetainedItem("thread-1", turnIDForTest(2), true)
	tips.observeTurnCompleted("thread-1", turnIDForTest(2), true, false)
	tips.acknowledge(turnTipSurfaceCompletion)
	if tips.completionsShown != 1 || tips.nextCompletion != tips.starts+turnTipCompletionInterval {
		t.Fatalf("completion accounting = %d/%d", tips.completionsShown, tips.nextCompletion)
	}
	// The interval gates the next completion until three more turns start.
	tips.observeTurnStarted("thread-1", turnIDForTest(3), base.Add(5*time.Minute))
	if tips.starts >= tips.nextCompletion {
		t.Fatalf("cadence not enforced: starts=%d next=%d", tips.starts, tips.nextCompletion)
	}
	// A failed turn with a final answer still does not become eligible.
	tips.observeTurnCompleted("thread-1", turnIDForTest(3), false, true)
	if tips.completionsShown != 1 {
		t.Fatalf("a failed turn counted a completion: %d", tips.completionsShown)
	}
}

func turnIDForTest(index int) string {
	return "turn-" + string(rune('a'+index))
}

// Mirrors Rust's completion surface: a finished turn with a final answer whose
// cadence allows it renders one Markdown tip line, counted once and stable for
// the rest of the finished turn.
func TestTurnTipsCompletionTipRendersLikeRust(t *testing.T) {
	var tips turnTips
	base := time.Date(2026, 9, 26, 12, 0, 0, 0, time.UTC)
	for index := 0; index < turnTipCompletionInterval; index++ {
		tips.observeTurnStarted("thread-1", turnIDForTest(index), base)
	}
	tips.observeTurnCompleted("thread-1", turnIDForTest(2), true, true)
	if tips.current == nil || !tips.current.completionEligible {
		t.Fatal("finished turn did not become eligible for a completion tip")
	}
	line, ok := tips.completionTip(80, nil, "", "")
	if !ok || !strings.Contains(utils.StripANSI(line), "Tip: ") {
		t.Fatalf("completion tip = %q ok=%v", line, ok)
	}
	tips.acknowledge(turnTipSurfaceCompletion)
	again, ok := tips.completionTip(80, nil, "", "")
	if !ok || again != line {
		t.Fatalf("completion tip did not persist: %q ok=%v", again, ok)
	}
	if tips.completionsShown != 1 || tips.nextCompletion != tips.starts+turnTipCompletionInterval {
		t.Fatalf("completion accounting = %d/%d", tips.completionsShown, tips.nextCompletion)
	}
	// A new turn inside the spacing window is not eligible again.
	tips.observeTurnStarted("thread-1", turnIDForTest(3), base)
	tips.observeTurnCompleted("thread-1", turnIDForTest(3), true, true)
	if tips.current == nil || tips.current.completionEligible {
		t.Fatal("spacing window still allowed a completion tip")
	}
	// Once the window elapses the next finished turn is eligible again.
	tips.observeTurnStarted("thread-1", turnIDForTest(4), base)
	tips.observeTurnStarted("thread-1", turnIDForTest(5), base)
	tips.observeTurnCompleted("thread-1", turnIDForTest(5), true, true)
	if tips.current == nil || !tips.current.completionEligible {
		t.Fatal("third turn in the window did not restore eligibility")
	}
}

// A completed turn with a final answer renders the completion tip in the
// transcript (Rust's `Phase::Complete` surface), gated on the tooltip
// preference and the finished-turn state.
func TestTurnTipsCompletionTipRendersInTranscriptLikeRust(t *testing.T) {
	enabled := true
	model := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 30, ShowTooltips: &enabled})
	for index := 0; index < turnTipCompletionInterval; index++ {
		turnID := turnIDForTest(index)
		model.Update(ThreadEventMsg{Event: protocol.TurnStartedWithID(turnID)})
		model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{
			Type: "item.completed",
			Item: &protocol.ThreadItem{ID: "final-" + turnID, Type: "agent_message", Text: "done", Phase: "final_answer"},
		}})
		model.Update(ThreadEventMsg{Event: protocol.TurnCompletedWithID(protocol.Usage{}, turnID)})
	}
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "\u2514 ") || !strings.Contains(view, "Tip: ") {
		t.Fatalf("completion tip missing from the transcript:\n%s", view)
	}

	// The preference gate suppresses the completion tip.
	disabled := false
	off := NewModel(codextui.NewState(nil), Options{Width: 120, Height: 30, ShowTooltips: &disabled})
	for index := 0; index < turnTipCompletionInterval; index++ {
		turnID := turnIDForTest(index)
		off.Update(ThreadEventMsg{Event: protocol.TurnStartedWithID(turnID)})
		off.Update(ThreadEventMsg{Event: protocol.ThreadEvent{
			Type: "item.completed",
			Item: &protocol.ThreadItem{ID: "final-" + turnID, Type: "agent_message", Text: "done", Phase: "final_answer"},
		}})
		off.Update(ThreadEventMsg{Event: protocol.TurnCompletedWithID(protocol.Usage{}, turnID)})
	}
	if view := utils.StripANSI(off.View()); strings.Contains(view, "\u2514 ") {
		t.Fatalf("disabled tooltips rendered a completion tip:\n%s", view)
	}
}
