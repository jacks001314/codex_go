package app

import (
	"testing"

	"codex_go/appserver"
	"codex_go/session"
)

// TestResumeHistoryCarriesClientMessageIDLikeRust covers Rust #59817: the
// transcript rebuilt for a resume keeps each user message's client id so the
// TUI can reconcile uncertain submissions by identity.
func TestResumeHistoryCarriesClientMessageIDLikeRust(t *testing.T) {
	thread := &appserver.Thread{Turns: []appserver.Turn{{Items: []appserver.ThreadItem{
		{ID: "item-1", Type: "userMessage", Role: "user", Text: "hello", Data: map[string]any{"clientId": "client-remote"}},
		{ID: "item-2", Type: "agentMessage", Role: "assistant", Text: "hi"},
	}}}}
	remote := remoteTUIThreadMessagesFromThread(thread, reasoningProjectionChatWidget, false)
	if len(remote) != 2 {
		t.Fatalf("remote messages = %d, want 2", len(remote))
	}
	if remote[0].ClientID != "client-remote" {
		t.Fatalf("remote user client id = %q", remote[0].ClientID)
	}
	if remote[1].ClientID != "" {
		t.Fatalf("assistant client id = %q, want empty", remote[1].ClientID)
	}

	record := &session.Record{Items: []session.Item{{
		ID:       "local-1",
		Type:     "message",
		Role:     "user",
		Text:     "hello",
		Metadata: map[string]any{"clientId": "client-local"},
	}}}
	local := interactiveSessionMessagesFromRecord(record, reasoningProjectionChatWidget, false)
	if len(local) != 1 || local[0].ClientID != "client-local" {
		t.Fatalf("local messages = %#v", local)
	}
}
