package appserver

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/retainedctx"
	"codex_go/rollout"
	"codex_go/session"
	"codex_go/state"
)

func retainedOrderedTexts(entries []retainedctx.OrderedEntry) []string {
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		switch {
		case entry.Entry.UserMessage != nil:
			out = append(out, entry.Entry.UserMessage.Text)
		case entry.Entry.AssistantMessage != nil:
			out = append(out, entry.Entry.AssistantMessage.Text)
		case entry.Entry.VerifiedAnswer != nil:
			out = append(out, entry.Entry.VerifiedAnswer.Questions[0].Answer)
		}
	}
	return out
}

// Mirrors Rust's session-owned retained context: the thread's accepted user
// messages become the reviewer's retained evidence in acceptance order, repeated
// resolution dedupes by message id, and a thread without instructions has none.
func TestRuntimeRouterRetainedContextRecordsThreadInstructionsLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	threadID := session.ThreadID("thread-retained-context")
	if err := store.Create(&session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		Items: []session.Item{
			{ID: "user-1", Type: "message", Role: "user", Text: "Keep the repository private."},
			{ID: "assistant-1", Type: "message", Role: "assistant", Text: "Understood."},
			{ID: "user-2", Type: "message", Role: "user", Text: "Never publish it."},
		},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	want := []string{"Keep the repository private.", "Never publish it."}
	context := router.retainedContextForThread(string(threadID))
	if context == nil {
		t.Fatal("retainedContextForThread() = nil, want the thread's instructions")
	}
	if got := retainedOrderedTexts(context.OrderedEntries()); !reflect.DeepEqual(got, want) {
		t.Fatalf("retained instructions = %#v, want %#v", got, want)
	}
	// The assistant message is not user authorization evidence.
	for _, entry := range context.OrderedEntries() {
		if entry.Entry.AssistantMessage != nil {
			t.Fatalf("assistant text became retained user evidence: %#v", entry)
		}
	}
	// Resolving again dedupes by message id instead of duplicating.
	if got := retainedOrderedTexts(router.retainedContextForThread(string(threadID)).OrderedEntries()); !reflect.DeepEqual(got, want) {
		t.Fatalf("second resolution = %#v, want %#v", got, want)
	}
	if got := router.retainedContextForThread("thread-unknown"); got != nil {
		t.Fatalf("unknown thread context = %#v, want nil", got)
	}
}

// Mirrors Rust's checkpoint rule: compaction persists the retained snapshot, so a
// restarted process still shows the original instructions even though the
// model's history no longer holds them.
func TestRuntimeRouterRetainedContextSurvivesCompactionLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	threadID := session.ThreadID("thread-retained-compaction")
	now := fixedTime()
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, ThreadID: string(threadID), SessionID: string(threadID),
		Source: "cli", CWD: home, ModelProvider: "openai", HistoryMode: "paginated", Now: now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if err := store.Create(&session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		Items: []session.Item{
			{ID: "user-1", Type: "message", Role: "user", Text: "Keep the repository private."},
		},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	if context := router.retainedContextForThread(string(threadID)); context == nil {
		t.Fatal("retainedContextForThread() = nil before compaction")
	}
	// The compaction replaces the model-facing history and writes the checkpoint.
	record, err := store.Read(threadID, true, true)
	if err != nil || record == nil {
		t.Fatalf("Read() = %#v/%v", record, err)
	}
	record.Items = []session.Item{{ID: "compacted-1", Type: "message", Role: "assistant", Text: "summary"}}
	if err := store.Save(record); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	if err := router.appendRuntimeCompacted(string(threadID), "summary", record.Items, now.Add(time.Second)); err != nil {
		t.Fatalf("appendRuntimeCompacted() error = %v", err)
	}
	lines, _, err := rollout.Load(filepath.Join(home, rollout.SessionsSubdir, now.Format("2006"), now.Format("01"), now.Format("02"), filepath.Base(recorder.Path())))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if checkpoint := rollout.CompactedRetainedContext(lines); checkpoint == nil {
		t.Fatal("the checkpoint carries no retained snapshot")
	}

	// A fresh router (a restarted process) rebuilds the evidence from the
	// checkpoint even though the history no longer holds the instruction.
	fresh := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	restored := fresh.retainedContextForThread(string(threadID))
	if restored == nil {
		t.Fatal("retainedContextForThread() = nil after compaction")
	}
	if got := retainedOrderedTexts(restored.OrderedEntries()); !reflect.DeepEqual(got, []string{"Keep the repository private."}) {
		t.Fatalf("restored retained instructions = %#v", got)
	}
}

// Mirrors Rust's review prompt: the retained user-instruction section renders the
// thread's original instructions, and a thread without evidence adds no section.
func TestModelGuardianReviewerIncludesRetainedInstructionsLikeRust(t *testing.T) {
	messageID := "user-1"
	retained := &retainedctx.RetainedContext{}
	retained.RecordUserMessage(retainedctx.RetainedUserMessage{
		TurnID:    "turn-1",
		MessageID: &messageID,
		Text:      "Keep the repository private.",
		Complete:  true,
	}, retainedctx.LocalInputSource(nil))

	var captured *model.AgentRequest
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			captured = request
			return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
		}),
		store:           state.NewReviewStore(),
		retainedContext: func(threadID, turnID string) *retainedctx.RetainedContext { return retained },
	}
	if _, _, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{Type: "command", Command: "ls", CWD: "/repo"}); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if captured == nil {
		t.Fatal("Review() produced no request")
	}
	for _, want := range []string{
		">>> RETAINED USER INSTRUCTIONS START",
		"Retained source order: 0\nuser: Keep the repository private.\n",
		">>> RETAINED USER INSTRUCTIONS END",
	} {
		if !strings.Contains(captured.Prompt, want) {
			t.Fatalf("prompt is missing %q:\n%s", want, captured.Prompt)
		}
	}

	open := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			captured = request
			return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
		}),
		store:           state.NewReviewStore(),
		retainedContext: func(threadID, turnID string) *retainedctx.RetainedContext { return nil },
	}
	if _, _, err := open.Review(context.Background(), "thread-1", "turn-1", "call-2", state.Action{Type: "command", Command: "ls", CWD: "/repo"}); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if strings.Contains(captured.Prompt, "RETAINED USER INSTRUCTIONS") {
		t.Fatalf("a thread without retained evidence rendered a section:\n%s", captured.Prompt)
	}
}
