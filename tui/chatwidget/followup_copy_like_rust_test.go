package chatwidget

import (
	"testing"

	codextui "codex_go/tui"
)

// TestFollowupDirectivesCopyAsLabelsLikeRust pins #49089's copy half: whole
// response copy exposes the followed-up action label and never the private
// prompt attribute.
func TestFollowupDirectivesCopyAsLabelsLikeRust(t *testing.T) {
	source := "- :codex-followup[Inspect **items**]{prompt=\"private\"}\n"
	messages := []codextui.Message{{Role: codextui.RoleAssistant, Text: source}}
	text, ok := LastAssistantMarkdown(messages)
	if !ok {
		t.Fatal("assistant response was not copyable")
	}
	want := "- Inspect **items**"
	if text != want {
		t.Fatalf("LastAssistantMarkdown() = %q, want %q", text, want)
	}
	targets := CopyTargetsFromMarkdown(text)
	if len(targets) == 0 || targets[0].Text != want {
		t.Fatalf("copy targets = %#v, want whole response %q", targets, want)
	}
}
