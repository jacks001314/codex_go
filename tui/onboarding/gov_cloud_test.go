package onboarding

import (
	"strings"
	"testing"

	"codex_go/tui/keymsg"
)

func TestGovCloudGuidanceAcknowledgeAndCancel(t *testing.T) {
	model := &govCloudGuidanceModel{width: 80, height: 24}
	if view := model.viewString(); !strings.Contains(view, "Using Codex with AWS GovCloud") ||
		!strings.Contains(view, GovCloudGuidanceURL) || !strings.Contains(view, "> Acknowledge") {
		t.Fatalf("guidance view:\n%s", view)
	}
	updated, command := model.Update(keymsg.KeyMsg{Type: keymsg.KeyEnter})
	finished := updated.(*govCloudGuidanceModel)
	if command == nil || !finished.accepted || !finished.done {
		t.Fatalf("acknowledge state = %#v cmd=%v", finished, command)
	}

	cancelled := &govCloudGuidanceModel{width: 80, height: 24}
	updated, command = cancelled.Update(keymsg.KeyMsg{Type: keymsg.KeyEsc})
	finished = updated.(*govCloudGuidanceModel)
	if command == nil || finished.accepted || !finished.done {
		t.Fatalf("cancel state = %#v cmd=%v", finished, command)
	}
}

func TestGovCloudGuidanceScrollsWithoutLosingAcknowledge(t *testing.T) {
	model := &govCloudGuidanceModel{width: 40, height: 6}
	for range 5 {
		model.Update(keymsg.KeyMsg{Type: keymsg.KeyDown})
	}
	_ = model.viewString()
	if model.offset == 0 {
		t.Fatal("down did not scroll the guidance")
	}
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyUp})
	if model.offset != 2 {
		t.Fatalf("offset after up = %d, want 2", model.offset)
	}
	if !strings.Contains(model.viewString(), "> Acknowledge") {
		t.Fatalf("footer missing after scrolling:\n%s", model.viewString())
	}
}
