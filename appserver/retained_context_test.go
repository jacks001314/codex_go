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
	"codex_go/utils"
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

	// The assistant message is retained as assistant context between the two
	// instructions, in the acceptance order history records.
	want := []string{"Keep the repository private.", "Understood.", "Never publish it."}
	context := router.retainedContextForThread(string(threadID))
	if context == nil {
		t.Fatal("retainedContextForThread() = nil, want the thread's instructions")
	}
	if got := retainedOrderedTexts(context.OrderedEntries()); !reflect.DeepEqual(got, want) {
		t.Fatalf("retained instructions = %#v, want %#v", got, want)
	}
	// The assistant message is assistant context, never user authorization evidence.
	sawAssistant := false
	for _, entry := range context.OrderedEntries() {
		if entry.Entry.AssistantMessage != nil {
			sawAssistant = true
			if entry.Entry.AssistantMessage.Text != "Understood." {
				t.Fatalf("assistant context = %#v", entry.Entry.AssistantMessage)
			}
			continue
		}
		if entry.Entry.UserMessage != nil && entry.Entry.UserMessage.Text == "Understood." {
			t.Fatalf("assistant text became retained user evidence: %#v", entry)
		}
	}
	if !sawAssistant {
		t.Fatal("the assistant message was not retained as assistant context")
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
	// The compacted history marks its summary the way sessionItemsFromCompactItems
	// does, so the derivation recognises it as compaction output.
	record.Items = []session.Item{{
		ID:       "compacted-1",
		Type:     "message",
		Role:     "assistant",
		Text:     "summary",
		Metadata: map[string]any{"compact": true, "kind": "compaction_summary"},
	}}
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

// Mirrors Rust's ContextManager::record_retained_message assistant arm: assistant
// output is retained as bounded context in history order, is marked incomplete
// when the evidence bound had to shorten it, is not duplicated by a repeated
// resolution, and never includes a compaction summary.
func TestRuntimeRouterRetainsAssistantContextLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	threadID := session.ThreadID("thread-assistant-context")
	oversized := strings.Repeat("x", utils.ApproxBytesForTokens(900)+200)
	if err := store.Create(&session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		Items: []session.Item{
			{ID: "user-1", Type: "message", Role: "user", Text: "Keep the repository private."},
			{ID: "assistant-1", Type: "message", Role: "assistant", Text: "Understood."},
			{ID: "assistant-oversized", Type: "message", Role: "assistant", Text: oversized},
			{
				ID: "compacted-1", Type: "message", Role: "assistant", Text: "conversation digest",
				Metadata: map[string]any{"compact": true, "kind": "compaction_summary"},
			},
		},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	context := router.retainedContextForThread(string(threadID))
	if context == nil {
		t.Fatal("retainedContextForThread() = nil")
	}
	entries := context.OrderedEntries()
	if len(entries) != 3 {
		t.Fatalf("retained entries = %#v, want the instruction and two assistant messages", entries)
	}
	orders := make([]retainedctx.RetainedContextOrder, 0, len(entries))
	for _, entry := range entries {
		orders = append(orders, entry.Order)
		if entry.Entry.UserMessage != nil || entry.Entry.VerifiedAnswer != nil {
			if entry.Entry.UserMessage != nil && entry.Entry.UserMessage.Text != "Keep the repository private." {
				t.Fatalf("unexpected retained user evidence = %#v", entry.Entry.UserMessage)
			}
		}
	}
	assistant := map[string]*retainedctx.RetainedUserMessage{}
	for _, entry := range entries {
		if entry.Entry.AssistantMessage != nil {
			assistant[entry.Entry.AssistantMessage.Text] = entry.Entry.AssistantMessage
		}
	}
	if assistant["Understood."] == nil || !assistant["Understood."].Complete {
		t.Fatalf("bounded assistant context = %#v", assistant["Understood."])
	}
	oversizedEntry := context.OrderedEntries()[2].Entry.AssistantMessage
	if oversizedEntry == nil {
		t.Fatalf("the oversized assistant message was not retained: %#v", entries)
	}
	if oversizedEntry.Complete {
		t.Fatal("a truncated assistant message was retained as complete evidence")
	}
	// Rust's truncation is token-estimated, so the retained text is bounded but
	// need not fit the byte estimate exactly; the incomplete flag is the contract.
	if len(oversizedEntry.Text) >= len(oversized) {
		t.Fatalf("assistant text = %d bytes, want a bounded excerpt", len(oversizedEntry.Text))
	}
	if strings.Contains(oversizedEntry.Text, "conversation digest") {
		t.Fatalf("the compaction summary was retained as assistant evidence")
	}

	// Resolving again neither duplicates the evidence nor advances its order.
	again := router.retainedContextForThread(string(threadID))
	if again == nil || len(again.OrderedEntries()) != len(entries) {
		t.Fatalf("second resolution = %#v", again)
	}
	for index, entry := range again.OrderedEntries() {
		if entry.Order != orders[index] {
			t.Fatalf("entry %d order = %#v, want %#v", index, entry.Order, orders[index])
		}
	}

	// The reviewer prompt renders the bounded assistant context and the omission
	// notice for the message the bound had to shorten.
	prompt, err := state.BuildPromptWithOptions(state.Action{Type: "command", Command: "ls", CWD: "/repo"}, nil,
		state.BuildPromptOptions{RetainedContext: again})
	if err != nil {
		t.Fatalf("BuildPromptWithOptions() error = %v", err)
	}
	if !strings.Contains(prompt, "assistant: Understood.") {
		t.Fatalf("prompt is missing the assistant context:\n%s", prompt)
	}
	if !strings.Contains(prompt, state.RootMessage{Kind: state.RootMessageIncompleteAssistantContext}.Render()) {
		t.Fatalf("prompt is missing the omitted-assistant-context notice:\n%s", prompt)
	}
	if strings.Contains(prompt, strings.Repeat("x", 40)) {
		t.Fatalf("prompt rendered the truncated assistant text:\n%s", prompt)
	}
}
