package app

import (
	"strings"
	"testing"

	"codex_go/appserver"
)

// TestRemoteTUIThreadMessagesHideDelegatedVoiceContentLikeRust drives the
// persisted-thread load path (resume / switch into another agent's thread) and
// checks Rust hide_private_items_after_voice_handoff: a delegated turn keeps its
// public answer and spoken prompt but never replays its reasoning or commentary.
func TestRemoteTUIThreadMessagesHideDelegatedVoiceContentLikeRust(t *testing.T) {
	thread := &appserver.Thread{Turns: []appserver.Turn{{
		ID: "voice-turn",
		Items: []appserver.ThreadItem{
			{ID: "user-1", Type: "userMessage", Text: "<realtime_delegation><input>inspect &lt;the&gt; workspace</input></realtime_delegation>"},
			{ID: "reasoning-1", Type: "reasoning", Data: map[string]any{"summary": []any{"private reasoning"}}},
			{ID: "commentary-1", Type: "agentMessage", Text: "private commentary", Data: map[string]any{"phase": "commentary"}},
			{ID: "agent-1", Type: "agentMessage", Text: "the public answer", Data: map[string]any{"phase": "final_answer"}},
		},
	}, {
		ID: "typed-turn",
		Items: []appserver.ThreadItem{
			{ID: "user-2", Type: "userMessage", Text: "typed question"},
			{ID: "reasoning-2", Type: "reasoning", Data: map[string]any{"summary": []any{"typed reasoning"}}},
			{ID: "commentary-2", Type: "agentMessage", Text: "typed commentary", Data: map[string]any{"phase": "commentary"}},
		},
	}}}
	messages := remoteTUIThreadMessagesFromThread(thread, reasoningProjectionChatWidget, false)
	var joined strings.Builder
	for _, message := range messages {
		joined.WriteString(message.Text)
		joined.WriteString("\n")
	}
	rendered := joined.String()
	if !strings.Contains(rendered, "the public answer") {
		t.Fatalf("delegated final answer missing: %q", rendered)
	}
	if !strings.Contains(rendered, "inspect <the> workspace") {
		t.Fatalf("spoken prompt not rendered from the delegation envelope: %q", rendered)
	}
	if strings.Contains(rendered, "<realtime_delegation>") {
		t.Fatalf("delegation envelope leaked into history: %q", rendered)
	}
	if strings.Contains(rendered, "private reasoning") || strings.Contains(rendered, "private commentary") {
		t.Fatalf("delegated private content leaked into history: %q", rendered)
	}
	// The non-delegated turn is untouched.
	if !strings.Contains(rendered, "typed reasoning") || !strings.Contains(rendered, "typed commentary") || !strings.Contains(rendered, "typed question") {
		t.Fatalf("typed turn content was suppressed: %q", rendered)
	}
}

// TestRemoteTUIThreadActiveReasoningIgnoresDelegatedTurnLikeRust pins the
// heading gate: a delegated voice turn never restores its reasoning heading.
func TestRemoteTUIThreadActiveReasoningIgnoresDelegatedTurnLikeRust(t *testing.T) {
	thread := &appserver.Thread{Turns: []appserver.Turn{{
		ID:     "voice-turn",
		Status: appserver.TurnStatusInProgress,
		Items: []appserver.ThreadItem{
			{ID: "user-1", Type: "userMessage", Text: "<realtime_delegation><input>spoken</input></realtime_delegation>"},
			{ID: "reasoning-1", Type: "reasoning", Data: map[string]any{"summary": []any{"## Private step"}}},
		},
	}}}
	if turnID, itemID, heading, ok := remoteTUIThreadActiveReasoning(thread); ok {
		t.Fatalf("delegated turn restored a reasoning heading: %q/%q/%q", turnID, itemID, heading)
	}
}
