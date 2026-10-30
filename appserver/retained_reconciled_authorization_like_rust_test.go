package appserver

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/retainedctx"
	"codex_go/rollout"
	"codex_go/session"
	"codex_go/state"
)

func retainedOrderPtr(value uint64) *uint64 {
	return &value
}

func writeRetainedCheckpointRollout(t *testing.T, home string, threadID session.ThreadID, retained *retainedctx.RetainedContext) string {
	t.Helper()
	now := fixedTime()
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, ThreadID: string(threadID), SessionID: string(threadID),
		Source: "cli", CWD: home, ModelProvider: "openai", HistoryMode: "paginated", Now: now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	if err := recorder.AppendCompactedWithCheckpoint(
		"Compacted.", nil, retained, nil, nil, "", nil, nil, now.Add(time.Second),
	); err != nil {
		t.Fatalf("AppendCompactedWithCheckpoint() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	return recorder.Path()
}

func harnessOrderedUserItem(id string, order uint64, text string) session.Item {
	return session.Item{
		ID: id, Type: "message", Role: "user", Text: text,
		Data: map[string]any{"harness_metadata": map[string]any{"user_input_order": order}},
	}
}

// Mirrors Rust #43472: an incomplete checkpoint omitted a surviving root
// instruction, and a queued approval reached model history after a later
// restrictive instruction. Both are recovered in persisted acceptance order.
func TestGuardianRootConversationOrdersQueuedApprovalByAcceptanceLikeRust(t *testing.T) {
	now := fixedTime()
	home := t.TempDir()
	store := session.NewStore(home)
	rootID := session.ThreadID(guardianRootAuthorizationRootID)
	workerID := session.ThreadID(guardianRootAuthorizationWorkerID)

	initial := "Inspect the deployment. Do not delete production data."
	queued := "I approve making the reviewed deployment public."
	restrictive := "Do not publish the deployment."
	answer := retainedctx.VerifiedAnswer{
		TurnID: "turn-answer", CallID: "publish",
		Questions: []retainedctx.VerifiedQuestionAnswer{{Question: "May I publish?", Answer: "Yes, publish it."}},
	}
	retained := &retainedctx.RetainedContext{}
	retained.RecordUserMessage(retainedctx.RetainedUserMessage{
		TurnID: "turn-initial", MessageID: stringPtr("initial"), Text: initial,
	}, retainedctx.LocalInputSource(retainedOrderPtr(0)))
	retained.Record(retainedctx.RetainedContextEvent{Answer: answer, AcceptanceOrder: retainedOrderPtr(2)})
	retained.MarkUserMessagesIncomplete()
	rolloutPath := writeRetainedCheckpointRollout(t, home, rootID, retained)

	for _, record := range []*session.Record{
		{
			ID: rootID, SessionID: string(rootID), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
			Metadata: session.Metadata{
				Source: "cli", ModelProvider: "openai", AgentPath: "/root", MultiAgentVersion: "v2",
				Extra: map[string]any{"rollout_path": rolloutPath},
			},
			Items: []session.Item{
				harnessOrderedUserItem("initial", 0, initial),
				harnessOrderedUserItem("restrict", 3, restrictive),
				harnessOrderedUserItem("queued", 1, queued),
			},
		},
		{
			ID: workerID, SessionID: string(workerID), ParentThreadID: rootID,
			CreatedAt: now, UpdatedAt: now, RecencyAt: now,
			Metadata: session.Metadata{
				Source: "subagent", ModelProvider: "openai", AgentPath: "/root/worker",
				MultiAgentVersion: "v2", Originator: "subagent",
			},
		},
	} {
		if err := store.Create(record); err != nil {
			t.Fatalf("store.Create(%s): %v", record.ID, err)
		}
	}

	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	messages := router.guardianRootConversation(string(workerID))
	want := []state.RootMessage{
		{Kind: state.RootMessageRetainedContextScope},
		{Kind: state.RootMessageIncompleteRootInstructions},
		{Kind: state.RootMessageUser, Text: initial},
		{Kind: state.RootMessageUser, Text: queued},
		{Kind: state.RootMessageUserInput, Text: state.RenderOriginalVerifiedAnswer(&answer)},
		{Kind: state.RootMessageUser, Text: restrictive},
	}
	if !reflect.DeepEqual(messages, want) {
		t.Fatalf("root conversation = %#v, want %#v", messages, want)
	}
}

// Mirrors Rust #43472: an older checkpoint cleared an oversized instruction,
// but the exact surviving source still supplies a bounded excerpt.
func TestRuntimeRouterRecoversOmittedInstructionExcerptOnResumeLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	threadID := session.ThreadID("thread-omitted-excerpt")
	original := strings.Repeat("Never publish project details. ", 120)
	messageID := "omitted"
	retained := &retainedctx.RetainedContext{}
	retained.RecordUserMessage(retainedctx.RetainedUserMessage{
		TurnID: "turn-1", MessageID: &messageID, Text: "", Complete: false,
	}, retainedctx.LocalInputSource(retainedOrderPtr(1)))
	retained.MarkUserMessagesIncomplete()
	rolloutPath := writeRetainedCheckpointRollout(t, home, threadID, retained)
	if err := store.Create(&session.Record{
		ID: threadID, SessionID: string(threadID),
		Metadata: session.Metadata{Extra: map[string]any{"rollout_path": rolloutPath}},
		Items:    []session.Item{{ID: messageID, Type: "message", Role: "user", Text: original}},
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
	recovered := ""
	for _, entry := range context.OrderedEntries() {
		if entry.Entry.UserMessage != nil && entry.Entry.UserMessage.MessageID != nil && *entry.Entry.UserMessage.MessageID == messageID {
			recovered = entry.Entry.UserMessage.Text
		}
	}
	if recovered == "" || !strings.HasPrefix(recovered, "Never publish project details.") {
		t.Fatalf("recovered excerpt = %q, want the surviving bounded instruction", recovered)
	}
	if !context.HasMissingUserMessages() {
		t.Fatal("recovering an excerpt must not clear the checkpoint's evidence gap")
	}
}

// Mirrors Rust #43472: surviving local metadata advances the acceptance
// counter even when its retained record is gone (a legacy checkpoint without a
// retained snapshot), so a new answer sorts after that accepted input.
func TestRuntimeRouterRestoresInputOrderFromSurvivingMetadataLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	threadID := session.ThreadID("thread-order-restore")
	writeRetainedRestoreRollout(t, home, threadID, `{"message":"compacted","replacement_history":[]}`)
	if err := store.Create(&session.Record{
		ID: threadID, SessionID: string(threadID),
		Items: []session.Item{{
			ID: "call-5", Type: "function_call", Name: "shell",
			Data: map[string]any{"harness_metadata": map[string]any{"user_input_order": 5}},
		}},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	if !router.recordRetainedContextEvent(string(threadID), retainedctx.RetainedContextEvent{Answer: retainedctx.VerifiedAnswer{
		TurnID: "turn-1", CallID: "call-1",
		Questions: []retainedctx.VerifiedQuestionAnswer{{Question: "Publish?", Answer: "Only privately."}},
	}}) {
		t.Fatal("recordRetainedContextEvent() = false")
	}
	context := router.retainedContextForThread(string(threadID))
	if context == nil {
		t.Fatal("retainedContextForThread() = nil")
	}
	entries := context.OrderedEntries()
	if len(entries) != 1 || entries[0].Entry.VerifiedAnswer == nil {
		t.Fatalf("ordered entries = %#v, want the accepted answer", entries)
	}
	if got := entries[0].Order.Order; got != 6 {
		t.Fatalf("answer order = %d, want 6 (after the surviving order 5)", got)
	}
}

// Mirrors Rust #42844: an oversized instruction is omitted atomically from the
// retained-instructions section with an explicit host notice. The resumed
// excerpt recovery must not turn it back into a partial permission.
func TestRetainedInstructionsOmitOversizedCheckpointInstructionLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	threadID := session.ThreadID("thread-oversized-instruction")
	original := strings.Repeat("Permission is conditional. ", 800)
	messageID := "oversized"
	retained := &retainedctx.RetainedContext{}
	retained.RecordUserMessage(retainedctx.RetainedUserMessage{
		TurnID: "turn-1", MessageID: &messageID, Text: original, Complete: true,
	}, retainedctx.LocalInputSource(retainedOrderPtr(1)))
	rolloutPath := writeRetainedCheckpointRollout(t, home, threadID, retained)
	if err := store.Create(&session.Record{
		ID: threadID, SessionID: string(threadID),
		Metadata: session.Metadata{Extra: map[string]any{"rollout_path": rolloutPath}},
		Items:    []session.Item{{ID: messageID, Type: "message", Role: "user", Text: original}},
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
		t.Fatal("an oversized checkpoint instruction must stay incomplete after resume")
	}
	fragments := state.RenderRetainedInstructions(context)
	if len(fragments) == 0 ||
		!strings.HasPrefix(fragments[0].Content, "Host notice: some retained user instructions are unavailable") {
		t.Fatalf("retained instruction fragments = %#v, want the omission notice first", fragments)
	}
	for _, fragment := range fragments {
		if strings.Contains(fragment.Content, "Permission is conditional") {
			t.Fatalf("an over-budget instruction must be omitted whole: %q", fragment.Content)
		}
	}
}
