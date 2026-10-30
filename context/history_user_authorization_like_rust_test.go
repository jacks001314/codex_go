package context

import (
	"testing"

	"codex_go/eventmap"
)

func authorizationTestMessage(role string, text string, kinds []string) HistoryItem {
	return HistoryItem{
		Kind:             eventmap.ResponseMessage,
		Role:             role,
		Content:          []eventmap.ContentItem{{Kind: eventmap.ContentInputText, Text: text}},
		ContentItemKinds: kinds,
	}
}

// Mirrors Rust #41660 `is_user_authorization_message`: host annotations decide,
// and unknown or incomplete metadata stays conservative.
func TestIsUserAuthorizationMessageLikeRust(t *testing.T) {
	cases := []struct {
		name string
		item HistoryItem
		want bool
	}{
		{"user text", authorizationTestMessage("user", "Ship it.", []string{"user.text"}), true},
		{"internal goal", authorizationTestMessage("user", "<goal>Ship it.</goal>", []string{"context.goal"}), false},
		{"no kinds", authorizationTestMessage("user", "Ship it.", nil), true},
		{"length mismatch", authorizationTestMessage("user", "Ship it.", []string{"user.text", "context.goal"}), true},
		{"empty kind", authorizationTestMessage("user", "Ship it.", []string{""}), true},
		{"unknown kind", authorizationTestMessage("user", "Ship it.", []string{"unknown"}), true},
		{"image preparation error", authorizationTestMessage("user", "", []string{"images.preparation_error"}), true},
		{"image unsupported", authorizationTestMessage("user", "", []string{"images.unsupported"}), true},
		{"audio unsupported", authorizationTestMessage("user", "", []string{"audio.unsupported"}), true},
		{"assistant message", authorizationTestMessage("assistant", "Ship it.", []string{"assistant.text"}), false},
		{"developer message", authorizationTestMessage("developer", "Ship it.", nil), false},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			item := testCase.item
			if got := IsUserAuthorizationMessage(&item); got != testCase.want {
				t.Fatalf("IsUserAuthorizationMessage(%v) = %v, want %v", testCase.name, got, testCase.want)
			}
		})
	}
	var reasoning = HistoryItem{Kind: eventmap.ResponseReasoning, Role: "user"}
	if IsUserAuthorizationMessage(&reasoning) {
		t.Fatal("non-message items must never authorize")
	}
	if IsUserAuthorizationMessage(nil) {
		t.Fatal("nil items must never authorize")
	}
}

// Mirrors Rust #41660: cached Guardian authorization survives compaction and
// internal context, but is invalidated by user input and rollback.
func TestHistoryManagerUserMessageRevisionLikeRust(t *testing.T) {
	manager := NewHistoryManager()
	if got := manager.UserMessageRevision(); got != 0 {
		t.Fatalf("initial user message revision = %d, want 0", got)
	}

	userTurn := authorizationTestMessage("user", "Inspect the deployment.", []string{"user.text"})
	manager.RecordItems(userTurn)
	if got := manager.UserMessageRevision(); got != 1 {
		t.Fatalf("revision after user turn = %d, want 1", got)
	}

	// Host-injected context must not advance the revision.
	internal := authorizationTestMessage("user", "Inspecting the deployment.", []string{"context.goal"})
	manager.RecordItems(internal)
	if got := manager.UserMessageRevision(); got != 1 {
		t.Fatalf("revision after internal context = %d, want 1", got)
	}

	// The same text submitted by the user advances even though it looks internal.
	manager.RecordItems(authorizationTestMessage("user", "Inspecting the deployment.", []string{"user.text"}))
	if got := manager.UserMessageRevision(); got != 2 {
		t.Fatalf("revision after duplicate user text = %d, want 2", got)
	}

	// Failed image preparation is still real user input.
	manager.RecordItems(authorizationTestMessage("user", ImageOmittedPlaceholder, []string{"images.preparation_error"}))
	if got := manager.UserMessageRevision(); got != 3 {
		t.Fatalf("revision after failed image preparation = %d, want 3", got)
	}

	// Compaction advances the conversation generation but preserves authorization.
	generation := manager.HistoryVersion()
	manager.ReplaceCompacted([]HistoryItem{authorizationTestMessage("user", "summary", nil)})
	if got := manager.UserMessageRevision(); got != 3 {
		t.Fatalf("revision after compaction = %d, want 3", got)
	}
	if got := manager.HistoryVersion(); got != generation+1 {
		t.Fatalf("history version after compaction = %d, want %d", got, generation+1)
	}

	// A destructive reset invalidates prior authorization.
	manager.Replace(nil)
	if got := manager.UserMessageRevision(); got != 4 {
		t.Fatalf("revision after reset = %d, want 4", got)
	}

	manager.RecordItems(authorizationTestMessage("user", "Retry.", []string{"user.text"}))
	beforeRollback := manager.UserMessageRevision()
	manager.DropLastUserTurns(1)
	if got := manager.UserMessageRevision(); got != beforeRollback+1 {
		t.Fatalf("revision after rollback = %d, want %d", got, beforeRollback+1)
	}

	empty := NewHistoryManager()
	empty.DropLastUserTurns(2)
	if got := empty.UserMessageRevision(); got != 1 {
		t.Fatalf("revision after empty rollback = %d, want 1", got)
	}
	empty.DropLastUserTurns(0)
	if got := empty.UserMessageRevision(); got != 1 {
		t.Fatalf("zero-turn rollback advanced revision to %d, want 1", got)
	}
}
