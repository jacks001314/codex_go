package appserver

import (
	"encoding/json"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/session"
	"codex_go/state"
	"codex_go/utils"
)

// sessionItemForRetainedTest builds one instruction item the way the turn runtime
// records it: a response item carrying the harness content classifications.
func sessionItemForRetainedTest(id string, text string, kinds []string, contentTypes []string) session.Item {
	content := make([]map[string]any, 0, len(contentTypes))
	for _, contentType := range contentTypes {
		entry := map[string]any{"type": contentType}
		if contentType == "input_text" {
			entry["text"] = text
		}
		content = append(content, entry)
	}
	payload := map[string]any{
		"type":    "message",
		"role":    "user",
		"content": content,
	}
	if len(kinds) > 0 {
		payload["internal_chat_message_metadata_passthrough"] = map[string]any{"content_item_kinds": kinds}
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		panic(err)
	}
	return session.Item{
		ID: id, Type: "message", Role: "user", Text: text,
		Metadata: map[string]any{"turnId": "turn-1"},
		Raw:      raw,
	}
}

// Mirrors Rust's `ContextManager::record_retained_message` for an instruction: a
// message whose classifications show contextual content is not authorization
// evidence, and an instruction is only complete when every content entry is
// classified as genuine user text.
func TestRetainedInstructionCompletenessLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name         string
		kinds        []string
		contentTypes []string
		text         string
		wantRecorded bool
		wantComplete bool
	}{
		{
			name: "classified user text", kinds: []string{"user.text"}, contentTypes: []string{"input_text"},
			text: "Keep the repository private.", wantRecorded: true, wantComplete: true,
		},
		{
			name: "legacy instruction without classifications", contentTypes: []string{"input_text"},
			text: "Keep the repository private.", wantRecorded: true,
		},
		{
			name: "misaligned classifications", kinds: []string{"user.text"}, contentTypes: []string{"input_text", "input_text"},
			text: "Keep the repository private.", wantRecorded: true,
		},
		{
			name: "omitted goal objective", kinds: []string{"user.goal.omitted"}, contentTypes: []string{"input_text"},
			text: "Keep the repository private.", wantRecorded: true,
		},
		{
			name: "contextual fragment is not authorization evidence", kinds: []string{"contextual.notes"}, contentTypes: []string{"input_text"},
			text: "Contextual notes.", wantRecorded: false,
		},
		{
			name: "media preparation failure stays authorization evidence", kinds: []string{"images.preparation_error"}, contentTypes: []string{"input_text"},
			text: "Keep the repository private.", wantRecorded: true,
		},
		{
			name: "image content cannot prove a complete instruction", kinds: []string{"user.image"}, contentTypes: []string{"input_image"},
			text: "Keep the repository private.", wantRecorded: true,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			item := sessionItemForRetainedTest("user-1", testCase.text, testCase.kinds, testCase.contentTypes)
			record, ok := retainedRecordForSessionItem(&item, 0, nil, true)
			if ok != testCase.wantRecorded {
				t.Fatalf("retainedRecordForSessionItem() recorded = %v, want %v", ok, testCase.wantRecorded)
			}
			if !ok {
				return
			}
			if record.assistant {
				t.Fatalf("instruction recorded as assistant context: %#v", record)
			}
			if record.message.Complete != testCase.wantComplete {
				t.Fatalf("completeness = %v, want %v", record.message.Complete, testCase.wantComplete)
			}
			if len(record.message.Text) > utils.ApproxBytesForTokens(900)+64 {
				t.Fatalf("instruction text = %d bytes, want the bounded budget", len(record.message.Text))
			}
		})
	}

	// An instruction longer than the budget is retained as a bounded, incomplete
	// excerpt.
	oversized := strings.Repeat("y", utils.ApproxBytesForTokens(900)+200)
	item := sessionItemForRetainedTest("user-1", oversized, []string{"user.text"}, []string{"input_text"})
	record, ok := retainedRecordForSessionItem(&item, 0, nil, true)
	if !ok {
		t.Fatal("the oversized instruction was not retained")
	}
	if record.message.Complete {
		t.Fatal("a truncated instruction was retained as complete evidence")
	}
	if len(record.message.Text) >= len(oversized) {
		t.Fatalf("instruction text = %d bytes, want a bounded excerpt", len(record.message.Text))
	}
}

// The undefined goal placeholder Rust never treats as a complete objective also
// reaches the reviewer as an omission notice rather than an instruction.
func TestRetainedOmittedObjectiveIsIncompleteLikeRust(t *testing.T) {
	item := sessionItemForRetainedTest("user-1", "Objective withheld.", []string{"user.goal.omitted"}, []string{"input_text"})
	record, ok := retainedRecordForSessionItem(&item, 0, nil, true)
	if !ok {
		t.Fatal("the omitted-objective message was not retained")
	}
	if record.message.Complete {
		t.Fatalf("omitted objective = %#v, want incomplete evidence", record.message)
	}
}

// Mirrors Rust's review prompt for unverified evidence: an instruction the harness
// never classified cannot render as authorization, so the reviewer sees the
// unavailable-instructions notice instead of its text.
func TestRuntimeRouterReportsUnverifiedInstructionsAsIncompleteLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	threadID := session.ThreadID("thread-unverified-instructions")
	if err := store.Create(&session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		Items: []session.Item{{
			ID: "user-1", Type: "message", Role: "user", Text: "Keep the repository private.",
			Metadata: map[string]any{"turnId": "turn-1"},
		}},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	context := router.retainedContextForThread(string(threadID))
	if context == nil {
		t.Fatal("retainedContextForThread() = nil")
	}
	if context.UserMessagesComplete() {
		t.Fatal("a legacy instruction without classifications was reported complete")
	}
	prompt, err := state.BuildPromptWithOptions(state.Action{Type: "command", Command: "ls", CWD: "/repo"}, nil,
		state.BuildPromptOptions{RetainedContext: context})
	if err != nil {
		t.Fatalf("BuildPromptWithOptions() error = %v", err)
	}
	if !strings.Contains(prompt, "some retained user instructions are unavailable") {
		t.Fatalf("prompt is missing the unavailable-instructions notice:\n%s", prompt)
	}
	if strings.Contains(prompt, "Keep the repository private.") {
		t.Fatalf("prompt rendered unverified evidence as authorization:\n%s", prompt)
	}
}
