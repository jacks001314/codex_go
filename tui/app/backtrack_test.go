package app

import (
	"testing"

	codextui "codex_go/tui"
)

func backtrackMessages(roles ...codextui.MessageRole) []codextui.Message {
	texts := map[codextui.MessageRole]string{
		codextui.RoleUser:      "user",
		codextui.RoleAssistant: "assistant",
		codextui.RoleHistory:   "history",
	}
	messages := make([]codextui.Message, 0, len(roles))
	for _, role := range roles {
		messages = append(messages, codextui.Message{Role: role, Text: texts[role]})
	}
	return messages
}

func TestBacktrackTargetRequiresUserMessage(t *testing.T) {
	cells := backtrackMessages(codextui.RoleAssistant, codextui.RoleHistory)
	if HasBacktrackTarget(cells) {
		t.Fatal("assistant/history transcript should have no backtrack target")
	}
	if UserCount(cells) != 0 {
		t.Fatalf("UserCount = %d, want 0", UserCount(cells))
	}

	cells = append(cells, codextui.Message{Role: codextui.RoleUser, Text: "hello"})
	if !HasBacktrackTarget(cells) {
		t.Fatal("a user message should provide a backtrack target")
	}
	if UserCount(cells) != 1 {
		t.Fatalf("UserCount = %d, want 1", UserCount(cells))
	}
}

func TestNthUserPositionSkipsNonUserMessages(t *testing.T) {
	messages := backtrackMessages(codextui.RoleAssistant, codextui.RoleUser, codextui.RoleHistory, codextui.RoleUser)
	if position, ok := NthUserPosition(messages, 0); !ok || position != 1 {
		t.Fatalf("NthUserPosition(0) = %d,%v want 1,true", position, ok)
	}
	if position, ok := NthUserPosition(messages, 1); !ok || position != 3 {
		t.Fatalf("NthUserPosition(1) = %d,%v want 3,true", position, ok)
	}
	if _, ok := NthUserPosition(messages, 2); ok {
		t.Fatal("NthUserPosition(2) should be missing")
	}
	if _, ok := NthUserPosition(messages, BacktrackNoSelection); ok {
		t.Fatal("NthUserPosition(-1) should be missing")
	}
}

func TestBacktrackStepSelectionMathMatchesRust(t *testing.T) {
	// Step backward: no selection -> newest, oldest stays, otherwise one older.
	if got := StepBackwardBacktrack(BacktrackNoSelection, 3); got != 2 {
		t.Fatalf("backward from none = %d, want 2", got)
	}
	if got := StepBackwardBacktrack(0, 3); got != 0 {
		t.Fatalf("backward from oldest = %d, want 0", got)
	}
	if got := StepBackwardBacktrack(2, 3); got != 1 {
		t.Fatalf("backward from newest = %d, want 1", got)
	}
	// Step forward: no selection -> newest, otherwise one newer clamped.
	if got := StepForwardBacktrack(BacktrackNoSelection, 3); got != 2 {
		t.Fatalf("forward from none = %d, want 2", got)
	}
	if got := StepForwardBacktrack(1, 3); got != 2 {
		t.Fatalf("forward from 1 = %d, want 2", got)
	}
	if got := StepForwardBacktrack(2, 3); got != 2 {
		t.Fatalf("forward from newest = %d, want 2", got)
	}
	// A stale ordinal beyond the count clamps to the newest message.
	if got := StepBackwardBacktrack(9, 2); got != 1 {
		t.Fatalf("backward stale = %d, want 1", got)
	}
	// An empty transcript can never select anything.
	if got := StepBackwardBacktrack(BacktrackNoSelection, 0); got != BacktrackNoSelection {
		t.Fatalf("backward empty = %d, want no selection", got)
	}
	if got := StepForwardBacktrack(0, 0); got != BacktrackNoSelection {
		t.Fatalf("forward empty = %d, want no selection", got)
	}
}

func TestBacktrackStatePrimeResetAndSelection(t *testing.T) {
	var state BacktrackState
	state.Reset()
	if state.Primed || state.HasSelection() || state.NthUserMessage != BacktrackNoSelection {
		t.Fatalf("reset state = %#v", state)
	}

	state.Prime(" thread-1 ")
	if !state.Primed || state.BaseThreadID != "thread-1" || state.HasSelection() {
		t.Fatalf("primed state = %#v", state)
	}

	messages := backtrackMessages(codextui.RoleAssistant, codextui.RoleUser, codextui.RoleUser)
	state.NthUserMessage = 1
	selection, ok := state.BacktrackSelection("thread-1", messages)
	if !ok || selection.ThreadID != "thread-1" || selection.UserOrdinal != 1 || selection.Prompt.Text != "user" {
		t.Fatalf("selection = %#v ok=%v", selection, ok)
	}

	// A different active thread invalidates the selection (Rust base_id check).
	if _, ok := state.BacktrackSelection("thread-2", messages); ok {
		t.Fatal("a stale base thread should not resolve a selection")
	}

	// No selection yields no result even on the matching thread.
	state.NthUserMessage = BacktrackNoSelection
	if _, ok := state.BacktrackSelection("thread-1", messages); ok {
		t.Fatal("an unselected state should not resolve a selection")
	}

	state.Reset()
	if state.Primed || state.BaseThreadID != "" || state.HasSelection() {
		t.Fatalf("reset state = %#v", state)
	}
}

func TestBacktrackSelectionForPromptRejectsMissingOrdinal(t *testing.T) {
	messages := backtrackMessages(codextui.RoleUser)
	if _, ok := BacktrackSelectionForPrompt("thread-1", messages, 1); ok {
		t.Fatal("an out-of-range ordinal should not resolve")
	}
}

func TestBacktrackSelectionRestoresPromptAttachmentsAndElements(t *testing.T) {
	state := &codextui.State{}
	state.SetThreadID("thread-1")
	state.AddUserPromptMessage(
		"describe this\n\nAttachments:\n- image: /tmp/chart.png",
		"describe this",
		[]string{"/tmp/chart.png"},
		[]string{"https://example.test/remote.png"},
		[]codextui.MessageTextElement{{Start: 0, End: 13, Placeholder: "describe this"}},
	)
	state.AddMessage(codextui.RoleAssistant, "ok")

	var backtrack BacktrackState
	backtrack.Prime("thread-1")
	backtrack.NthUserMessage = 0
	selection, ok := backtrack.BacktrackSelection("thread-1", state.Messages)
	if !ok {
		t.Fatal("the user prompt should resolve")
	}
	// The composer restores the prompt, not the rendered attachment listing.
	if selection.Prompt.Text != "describe this" {
		t.Fatalf("prompt text = %q, want the original prompt", selection.Prompt.Text)
	}
	if len(selection.Prompt.LocalImages) != 1 || selection.Prompt.LocalImages[0] != "/tmp/chart.png" {
		t.Fatalf("local images = %#v", selection.Prompt.LocalImages)
	}
	if len(selection.Prompt.RemoteImageURLs) != 1 || selection.Prompt.RemoteImageURLs[0] != "https://example.test/remote.png" {
		t.Fatalf("remote images = %#v", selection.Prompt.RemoteImageURLs)
	}
	if len(selection.Prompt.TextElements) != 1 {
		t.Fatalf("text elements = %#v", selection.Prompt.TextElements)
	}
	element := selection.Prompt.TextElements[0]
	if element.ByteRange.Start != 0 || element.ByteRange.End != 13 {
		t.Fatalf("element range = %#v", element.ByteRange)
	}
	if element.Placeholder == nil || *element.Placeholder != "describe this" {
		t.Fatalf("element placeholder = %#v", element.Placeholder)
	}
}
