package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codex_go/model"
	"codex_go/session"
	"codex_go/state"
)

func encryptedAgentMessageRaw(author, recipient, ciphertext string) json.RawMessage {
	payload := map[string]any{
		"type":      "agent_message",
		"author":    author,
		"recipient": recipient,
		"content": []any{
			map[string]any{"type": "input_text", "text": "Message Type: MESSAGE\nPayload:\n"},
			map[string]any{"type": "encrypted_content", "encrypted_content": ciphertext},
		},
	}
	data, _ := json.Marshal(payload)
	return data
}

// Mirrors Rust #49038: the complete native agent message is recovered from the
// stored item, preserving author, recipient and ciphertext.
func TestGuardianNativeAgentMessagePreservesCiphertextLikeRust(t *testing.T) {
	raw := encryptedAgentMessageRaw("/root", "/root/worker", "sealed-reply")
	item := session.Item{
		ID:   "message-1",
		Type: "agent_message",
		Metadata: map[string]any{
			"rawResponseItem": json.RawMessage(raw),
		},
	}
	native, ok := guardianNativeAgentMessage(&item)
	if !ok {
		t.Fatal("guardianNativeAgentMessage() did not recognize the encrypted agent message")
	}
	var payload struct {
		Type      string `json:"type"`
		Author    string `json:"author"`
		Recipient string `json:"recipient"`
		Content   []struct {
			Type             string `json:"type"`
			EncryptedContent string `json:"encrypted_content"`
		} `json:"content"`
	}
	if err := json.Unmarshal(native, &payload); err != nil {
		t.Fatalf("native agent message did not parse: %v", err)
	}
	if payload.Type != "agent_message" || payload.Author != "/root" || payload.Recipient != "/root/worker" {
		t.Fatalf("native metadata = %#v, want the original author and recipient", payload)
	}
	if len(payload.Content) != 2 || payload.Content[1].Type != "encrypted_content" || payload.Content[1].EncryptedContent != "sealed-reply" {
		t.Fatalf("native content = %#v, want the ciphertext part preserved", payload.Content)
	}
	// A plaintext message is not a native encrypted item.
	if _, ok := guardianNativeAgentMessage(&session.Item{Type: "agent_message", Text: "plain"}); ok {
		t.Fatal("plaintext agent message was treated as a native encrypted item")
	}
}

// Rust #49038: an oversized encrypted item is omitted whole with the explicit
// notice at its own position, and a fitting item stays native.
func TestGuardianTranscriptCarriesEncryptedAgentMessagesLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	threadID := "123e4567-e89b-42d3-a456-426614174203"
	now := fixedTime()
	small := encryptedAgentMessageRaw("/root", "/root/worker", "sealed-reply")
	huge := encryptedAgentMessageRaw("/root", "/root/worker", strings.Repeat("c", guardianNativeAgentMessageMaxBytes))
	items := []session.Item{
		{ID: "user-1", Type: "message", Role: "user", Text: "Please deploy.", CreatedAt: now},
		{ID: "agent-small", Type: "agent_message", CreatedAt: now, Metadata: map[string]any{"rawResponseItem": json.RawMessage(small)}},
		{ID: "agent-huge", Type: "agent_message", CreatedAt: now, Metadata: map[string]any{"rawResponseItem": json.RawMessage(huge)}},
	}
	if err := store.Create(&session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{Source: "cli", ModelProvider: "openai"},
		Items:     items,
	}); err != nil {
		t.Fatalf("store.Create: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	entries := router.guardianReviewTranscriptEntries(threadID)
	if len(entries) != 3 {
		t.Fatalf("entries = %#v, want user, native, omission", entries)
	}
	if entries[0].Author != "user" || !strings.Contains(entries[0].Text, "Please deploy.") {
		t.Fatalf("user entry = %#v", entries[0])
	}
	if len(entries[1].Native) == 0 || !strings.Contains(string(entries[1].Native), "sealed-reply") {
		t.Fatalf("fitting encrypted message = %#v, want the native item", entries[1])
	}
	if len(entries[2].Native) != 0 || entries[2].Text != guardianTranscriptOmissionNotice || entries[2].Author != "assistant" {
		t.Fatalf("oversized encrypted message = %#v, want the host omission notice", entries[2])
	}
	rendered := router.guardianReviewTranscript(threadID)
	for _, line := range rendered {
		if strings.Contains(line, "sealed-reply") {
			t.Fatalf("native ciphertext reached the text transcript: %q", line)
		}
	}
}

// Mirrors Rust's incremental review contract: an encrypted parent reply appears
// once, in order, as a native input item, and the prompt continues after it.
func TestGuardianReviewDeliversEncryptedAgentMessageLikeRust(t *testing.T) {
	native := encryptedAgentMessageRaw("/root", "/root/worker", "sealed-reply")
	var captured *model.AgentRequest
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			captured = request
			return &model.AgentResponse{Message: guardianDecisionDurationAssessment}, nil
		}),
		store: state.NewReviewStore(),
		transcriptEntries: func(string) []state.GuardianTranscriptEntry {
			return []state.GuardianTranscriptEntry{
				{Author: "user", LineRole: "user", Text: "Please deploy."},
				{Author: "assistant", Native: native},
				{Author: "user", LineRole: "user", Text: "Only staging."},
			}
		},
		transcriptFormat: func(string) state.GuardianTranscriptFormat {
			return state.GuardianTranscriptFormatJSON
		},
	}
	decision, _, err := reviewer.Review(context.Background(), "thread-native", "turn-native", "call-native", state.Action{Type: "command", Command: "ls", CWD: t.TempDir()})
	if err != nil {
		t.Fatalf("review error = %v", err)
	}
	if decision != state.DecisionApproved {
		t.Fatalf("decision = %q, want approved", decision)
	}
	if captured == nil {
		t.Fatal("reviewer model did not receive a request")
	}
	if strings.Contains(captured.Prompt, "sealed-reply") {
		t.Fatalf("ciphertext was flattened into the prompt: %q", captured.Prompt)
	}
	if !strings.Contains(captured.Prompt, `"author":"user"`) || !strings.Contains(captured.Prompt, "Please deploy.") {
		t.Fatalf("prompt = %q, want the JSON user record before the native message", captured.Prompt)
	}
	nativeIndex, trailingIndex := -1, -1
	for index, item := range captured.InputItems {
		switch typed := item.(type) {
		case json.RawMessage:
			if strings.Contains(string(typed), "sealed-reply") {
				nativeIndex = index
			}
		case map[string]any:
			if content, ok := typed["content"].([]any); ok && len(content) == 1 {
				if part, ok := content[0].(map[string]any); ok {
					if text, _ := part["text"].(string); strings.Contains(text, "Only staging.") && strings.Contains(text, "Planned action JSON:") {
						trailingIndex = index
					}
				}
			}
		}
	}
	if nativeIndex < 0 {
		t.Fatalf("input items = %#v, want the native encrypted agent message", captured.InputItems)
	}
	if trailingIndex < 0 || trailingIndex <= nativeIndex {
		t.Fatalf("trailing transcript/action item index = %d, native index = %d; want the prompt to continue after the native message", trailingIndex, nativeIndex)
	}
}
