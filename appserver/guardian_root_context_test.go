package appserver

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	codexctx "codex_go/context"
	"codex_go/retainedctx"
	"codex_go/rollout"
	"codex_go/session"
	"codex_go/state"
)

// Mirrors Rust #57425's handoff regression: an ID-less checkpoint stores an
// excerpt beside the original legacy backup, and an oversized verified answer
// is excluded from the request. The worker must receive one capped copy and
// both evidence gaps.
func TestGuardianRootPreparationDeduplicatesLegacyExcerptLikeRust(t *testing.T) {
	original := strings.Repeat("Never publish project details. ", 200)
	excerpt, _ := codexctx.GuardianTruncateText(original, guardianMaxRootMessageTokens)
	answer := &retainedctx.VerifiedAnswer{
		TurnID: "large",
		CallID: "large",
		Questions: []retainedctx.VerifiedQuestionAnswer{{
			Question: "Publish?",
			Answer:   strings.Repeat("Only if conditions hold. ", 200),
		}},
	}
	prepared := prepareGuardianRootContext(guardianRootPreparationInput{
		userMessages: []guardianRootCandidate{
			{
				message:  state.RootMessage{Kind: state.RootMessageUser, Text: excerpt},
				order:    1,
				hasOrder: true,
			},
			{
				message:  state.RootMessage{Kind: state.RootMessageUserInput, Text: state.RenderOriginalVerifiedAnswer(answer)},
				order:    2,
				hasOrder: true,
			},
		},
		retainedUserSources:     []retainedctx.RetainedUserMessage{{Text: excerpt}},
		missingRootInstructions: true,
	}, []retainedctx.RetainedUserMessage{{Text: original}})

	want := []state.RootMessage{
		{Kind: state.RootMessageRetainedContextScope},
		{Kind: state.RootMessageIncompleteRootInstructions},
		{Kind: state.RootMessageIncompleteVerifiedAnswers},
		{Kind: state.RootMessageUser, Text: excerpt},
	}
	if !reflect.DeepEqual(prepared.messages, want) {
		t.Fatalf("prepared messages = %#v, want %#v", prepared.messages, want)
	}
	if !prepared.missingRootInstructions {
		t.Fatal("missing root instructions not reported")
	}
}

// Mirrors Rust #57425 through the host path: the compaction backup supplies the
// original legacy instruction, while the checkpoint retains only its excerpt.
func TestGuardianRootConversationRestoresLegacyCheckpointLikeRust(t *testing.T) {
	now := fixedTime()
	home := t.TempDir()
	store := session.NewStore(home)
	rootID := session.ThreadID(guardianRootAuthorizationRootID)
	workerID := session.ThreadID(guardianRootAuthorizationWorkerID)

	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, ThreadID: string(rootID), SessionID: string(rootID),
		Source: "cli", CWD: home, ModelProvider: "openai", HistoryMode: "paginated", Now: now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	original := strings.Repeat("Never publish project details. ", 200)
	excerpt, _ := codexctx.GuardianTruncateText(original, guardianMaxRootMessageTokens)
	legacyHistory, err := json.Marshal([]any{map[string]any{
		"type": "message",
		"role": "user",
		"content": []any{map[string]any{
			"type": "input_text",
			"text": original,
		}},
	}})
	if err != nil {
		t.Fatalf("marshal guardian history: %v", err)
	}
	retained := &retainedctx.RetainedContext{}
	retained.RecordUserMessage(retainedctx.RetainedUserMessage{Text: excerpt}, retainedctx.RetainedInputSource{})
	retained.MarkUserMessagesIncomplete()
	retained.Record(retainedctx.RetainedContextEvent{Answer: retainedctx.VerifiedAnswer{
		TurnID: "large",
		CallID: "large",
		Questions: []retainedctx.VerifiedQuestionAnswer{{
			Question: "Publish?",
			Answer:   strings.Repeat("Only if conditions hold. ", 200),
		}},
	}})
	if err := recorder.AppendCompactedWithCheckpoint(
		"Old ID-less checkpoint.", nil, retained, nil, nil, "", nil, legacyHistory, now.Add(time.Second),
	); err != nil {
		t.Fatalf("AppendCompactedWithCheckpoint() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	for _, record := range []*session.Record{
		{
			ID: rootID, SessionID: string(rootID), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
			Metadata: session.Metadata{
				Source: "cli", ModelProvider: "openai", AgentPath: "/root", MultiAgentVersion: "v2",
				Extra: map[string]any{"rollout_path": recorder.Path()},
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
		{Kind: state.RootMessageIncompleteVerifiedAnswers},
		{Kind: state.RootMessageUser, Text: excerpt},
	}
	if !reflect.DeepEqual(messages, want) {
		t.Fatalf("root conversation = %#v, want %#v", messages, want)
	}
}

// Mirrors Rust's assistant-context notice: handoff filtering that drops
// assistant evidence must say so instead of letting a reply look standalone.
func TestGuardianRootPreparationMarksFilteredAssistantContextLikeRust(t *testing.T) {
	user := guardianRootCandidate{
		message:  state.RootMessage{Kind: state.RootMessageUser, Text: "deploy"},
		order:    1,
		hasOrder: true,
	}
	assistants := make([]guardianRootCandidate, 0, 5)
	for index := 0; index < 5; index++ {
		assistants = append(assistants, guardianRootCandidate{
			message:  state.RootMessage{Kind: state.RootMessageAssistant, Text: "answer"},
			order:    uint64(index + 2),
			hasOrder: true,
		})
	}
	prepared := prepareGuardianRootContext(guardianRootPreparationInput{
		userMessages:      []guardianRootCandidate{user},
		assistantMessages: assistants,
		handoffBoundaries: []uint64{1},
	}, nil)
	found := false
	for _, message := range prepared.messages {
		if message.Kind == state.RootMessageIncompleteAssistantContext {
			found = true
		}
	}
	if !found {
		t.Fatalf("assistant omission notice missing: %#v", prepared.messages)
	}
}
