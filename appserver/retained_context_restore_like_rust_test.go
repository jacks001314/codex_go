package appserver

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/retainedctx"
	"codex_go/rollout"
	"codex_go/session"
)

func writeRetainedRestoreRollout(t *testing.T, home string, threadID session.ThreadID, compactedPayload string) {
	t.Helper()
	now := time.Now().UTC()
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:     home,
		ThreadID:      string(threadID),
		Source:        "cli",
		CWD:           home,
		ModelProvider: "openai",
		Now:           now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.AppendLine(rollout.Line{
		Type:      "compacted",
		Timestamp: now.Format(time.RFC3339Nano),
		Payload:   json.RawMessage(compactedPayload),
	}); err != nil {
		t.Fatalf("AppendLine(compacted) error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
}

// Mirrors Rust #42293's legacy checkpoint rule: a checkpoint written before
// retained context existed replaced the model window, so a resumed thread cannot
// prove its earlier user instructions are complete.
func TestRuntimeRouterMarksLegacyCheckpointWithoutRetainedContextIncompleteLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	threadID := session.ThreadID("thread-legacy-checkpoint")
	if err := store.Create(&session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		Items: []session.Item{{
			ID: "user-after-checkpoint", Type: "message", Role: "user", Text: "Keep the repository private.",
		}},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeRetainedRestoreRollout(t, home, threadID, `{"message":"compacted","replacement_history":[]}`)

	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	context := router.retainedContextForThread(string(threadID))
	if context == nil {
		t.Fatal("retainedContextForThread() = nil, want the visible post-checkpoint instruction")
	}
	if !context.HasMissingUserMessages() {
		t.Fatal("a legacy checkpoint without retained context must leave earlier instructions incomplete")
	}
}

// Mirrors Rust's `restore` repair for checkpoints written before `next_order`
// was persisted: a newly accepted answer must sort after the checkpoint's facts.
func TestRuntimeRouterRestoresRetainedCheckpointOrderLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	threadID := session.ThreadID("thread-checkpoint-order")
	if err := store.Create(&session.Record{ID: threadID, SessionID: string(threadID)}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	writeRetainedRestoreRollout(t, home, threadID, `{
		"message":"compacted",
		"replacement_history":[],
		"retained_context":{
			"verified_answers":[
				{"turn_id":"turn-1","call_id":"call-1","questions":[{"question":"Publish?","answer":"Only privately."}],"order":5}
			],
			"incomplete":false,
			"user_messages":[],
			"user_messages_incomplete":false,
			"assistant_messages":[],
			"assistant_messages_incomplete":false
		}
	}`)

	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	if !router.recordRetainedContextEvent(string(threadID), retainedctx.RetainedContextEvent{Answer: retainedctx.VerifiedAnswer{
		TurnID:    "turn-1",
		CallID:    "call-2",
		Questions: []retainedctx.VerifiedQuestionAnswer{{Question: "Publish the README?", Answer: "Do not publish it."}},
	}}) {
		t.Fatal("recordRetainedContextEvent() = false")
	}
	context := router.retainedContextForThread(string(threadID))
	if context == nil {
		t.Fatal("retainedContextForThread() = nil")
	}
	entries := context.OrderedEntries()
	answers := make([]string, 0, len(entries))
	orders := make([]uint64, 0, len(entries))
	for _, entry := range entries {
		if entry.Entry.VerifiedAnswer == nil {
			continue
		}
		answers = append(answers, entry.Entry.VerifiedAnswer.CallID)
		orders = append(orders, entry.Order.Order)
	}
	if want := []string{"call-1", "call-2"}; !reflect.DeepEqual(answers, want) {
		t.Fatalf("restored answer order = %v, want %v", answers, want)
	}
	if want := []uint64{5, 6}; !reflect.DeepEqual(orders, want) {
		t.Fatalf("restored answer orders = %v, want %v", orders, want)
	}
}

// Restoring must also reapply the live record bound instead of trusting an
// oversized answer stored by an older checkpoint.
func TestRuntimeRouterBoundsOversizedCheckpointAnswerLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	threadID := session.ThreadID("thread-checkpoint-bound")
	if err := store.Create(&session.Record{ID: threadID, SessionID: string(threadID)}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	oversized := strings.Repeat("a", retainedctx.MaxRecordBytes+1)
	payload := map[string]any{
		"message":             "compacted",
		"replacement_history": []any{},
		"retained_context": map[string]any{
			"verified_answers": []any{map[string]any{
				"turn_id": "turn-1", "call_id": "call-1", "order": 1,
				"questions": []any{map[string]any{"question": "Publish?", "answer": oversized}},
			}},
			"incomplete":                    false,
			"user_messages":                 []any{},
			"user_messages_incomplete":      false,
			"assistant_messages":            []any{},
			"assistant_messages_incomplete": false,
		},
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal checkpoint: %v", err)
	}
	writeRetainedRestoreRollout(t, home, threadID, string(encoded))

	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	context := router.retainedContextForThread(string(threadID))
	if context == nil {
		t.Fatal("retainedContextForThread() = nil")
	}
	if context.VerifiedAnswersComplete() {
		t.Fatal("an oversized checkpoint answer must be bounded and marked incomplete")
	}
	answers := context.VerifiedAnswers()
	if len(answers) != 1 || len(answers[0].Questions) != 0 {
		t.Fatalf("bounded checkpoint answers = %#v, want one entry with no questions", answers)
	}
}
