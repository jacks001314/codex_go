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
