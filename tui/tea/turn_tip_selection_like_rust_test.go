package tea

import (
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/protocol"
	codextui "codex_go/tui"
	"codex_go/utils"
)

func newWorkingTipSelectionModel(t *testing.T) (*Model, *string) {
	t.Helper()
	enabled := true
	base := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	var copied string
	model := NewModel(codextui.NewState(nil), Options{
		Width:        120,
		Height:       30,
		ShowTooltips: &enabled,
		CopyOnSelect: "always",
		OnClipboardWrite: func(text string) error {
			copied = text
			return nil
		},
	})
	model.now = func() time.Time { return base }
	model.Update(ThreadEventMsg{Event: protocol.TurnStarted()})
	model.now = func() time.Time { return base.Add(turnTipWorkingDelay + time.Second) }
	model.View()
	return model, &copied
}

func newCompletionTipSelectionModel(t *testing.T) (*Model, *string) {
	t.Helper()
	enabled := true
	var copied string
	model := NewModel(codextui.NewState(nil), Options{
		Width:        120,
		Height:       30,
		ShowTooltips: &enabled,
		CopyOnSelect: "always",
		OnClipboardWrite: func(text string) error {
			copied = text
			return nil
		},
	})
	for index := 0; index < turnTipCompletionInterval; index++ {
		turnID := turnIDForTest(index)
		model.Update(ThreadEventMsg{Event: protocol.TurnStartedWithID(turnID)})
		model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{
			Type: "item.completed",
			Item: &protocol.ThreadItem{ID: "final-" + turnID, Type: "agent_message", Text: "done", Phase: "final_answer"},
		}})
		model.Update(ThreadEventMsg{Event: protocol.TurnCompletedWithID(protocol.Usage{}, turnID)})
	}
	model.View()
	return model, &copied
}

func dragTurnTipSelection(t *testing.T, model *Model, kind renderedRegionKind) bubbletea.Cmd {
	t.Helper()
	region := model.renderedSelection.regions[kind]
	if region.w <= 0 || len(region.text) == 0 {
		t.Fatalf("tip region %d not registered: %#v", kind, region)
	}
	line := region.text[0]
	start := findDisplayColumn(line, "Tip")
	if start < 0 {
		t.Fatalf("tip row missing its label: %q", line)
	}
	end := start + len("Tip") - 1
	_, _ = model.Update(bubbletea.MouseMsg{
		X: start, Y: region.y, Action: bubbletea.MouseActionPress, Button: bubbletea.MouseButtonLeft,
	})
	_, cmd := model.Update(bubbletea.MouseMsg{
		X: end, Y: region.y, Action: bubbletea.MouseActionRelease, Button: bubbletea.MouseButtonLeft,
	})
	return cmd
}

// TestWorkingTurnTipSelectionCopiesLikeRust mirrors Rust #53152: a painted
// working tip row participates in copy-on-select and keeps its highlight while
// the editable composer is untouched.
func TestWorkingTurnTipSelectionCopiesLikeRust(t *testing.T) {
	model, copied := newWorkingTipSelectionModel(t)
	cmd := dragTurnTipSelection(t, model, renderedRegionWorkingTip)
	runTeaCmd(t, model, cmd)
	if *copied != "Tip" {
		t.Fatalf("copied working tip text = %q, want %q", *copied, "Tip")
	}
	if !model.renderedSelection.active || model.renderedSelectionText() != "Tip" {
		t.Fatalf("working tip selection = %#v/%q", model.renderedSelection, model.renderedSelectionText())
	}
	if got := model.composer.Value(); got != "" {
		t.Fatalf("composer draft = %q, want untouched", got)
	}
	view := model.View()
	if !strings.Contains(view, "\x1b[7m") {
		t.Fatal("selected working tip was not highlighted")
	}
	if !strings.Contains(utils.StripANSI(view), "Selecting text \u00b7 esc cancel") {
		t.Fatalf("selection hint missing:\n%s", utils.StripANSI(view))
	}
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyEsc})
	if model.renderedSelection.active {
		t.Fatal("esc did not cancel the tip selection")
	}
}

// TestWorkingTurnTipSelectionClearsWhenTipDisappearsLikeRust invalidates the
// selection when the painted working row is removed.
func TestWorkingTurnTipSelectionClearsWhenTipDisappearsLikeRust(t *testing.T) {
	model, _ := newWorkingTipSelectionModel(t)
	cmd := dragTurnTipSelection(t, model, renderedRegionWorkingTip)
	runTeaCmd(t, model, cmd)
	if !model.renderedSelection.active {
		t.Fatal("working tip selection was not active")
	}
	model.Update(ThreadEventMsg{Event: protocol.TurnCompleted(protocol.Usage{})})
	model.View()
	if model.renderedSelection.active {
		t.Fatalf("selection survived the working tip disappearing: %#v", model.renderedSelection)
	}
	if model.renderedSelection.regions[renderedRegionWorkingTip].w != 0 {
		t.Fatalf("working tip region = %#v, want cleared", model.renderedSelection.regions[renderedRegionWorkingTip])
	}
}

// TestCompletionTurnTipSelectionCopiesLikeRust covers the transcript-adjacent
// completion tip surface.
func TestCompletionTurnTipSelectionCopiesLikeRust(t *testing.T) {
	model, copied := newCompletionTipSelectionModel(t)
	if view := utils.StripANSI(model.View()); !strings.Contains(view, "\u2514 ") || !strings.Contains(view, "Tip:") {
		t.Fatalf("completion tip missing:\n%s", view)
	}
	cmd := dragTurnTipSelection(t, model, renderedRegionCompletionTip)
	runTeaCmd(t, model, cmd)
	if *copied != "Tip" {
		t.Fatalf("copied completion tip text = %q, want %q", *copied, "Tip")
	}
	if got := model.renderedSelectionText(); got != "Tip" {
		t.Fatalf("completion tip selection = %q, want %q", got, "Tip")
	}
}
