package appserver

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/model"
	"codex_go/retainedctx"
	"codex_go/rollout"
	"codex_go/session"
	"codex_go/state"
)

// Mirrors Rust's `Session::record_retained_context` (#44893) for the
// request_user_input producer: the accepted answers are recorded into the
// thread's retained evidence under the turn's call id, ordered after the user
// messages the host already accepted, and persisted as a sparse rollout fact.
func TestRuntimeRouterRecordsVerifiedAnswersLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	threadID := session.ThreadID("thread-verified-answer")
	if err := store.Create(&session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		Items: []session.Item{
			{ID: "user-1", Type: "message", Role: "user", Text: "Keep the repository private."},
		},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})

	recorder := router.retainedVerifiedAnswerRecorder(string(threadID), "turn-1")
	if recorder == nil {
		t.Fatal("retainedVerifiedAnswerRecorder() = nil")
	}
	recorder("call-1", []retainedctx.VerifiedQuestionAnswer{
		{Question: "Pick one?\nB: Second", Answer: "B"},
	})

	context := router.retainedContextForThread(string(threadID))
	if context == nil {
		t.Fatal("retainedContextForThread() = nil")
	}
	if got := retainedOrderedTexts(context.OrderedEntries()); !reflect.DeepEqual(got, []string{"Keep the repository private.", "B"}) {
		t.Fatalf("retained evidence = %#v", got)
	}
	answers := context.VerifiedAnswers()
	if len(answers) != 1 || answers[0].TurnID != "turn-1" || answers[0].CallID != "call-1" {
		t.Fatalf("verified answers = %#v", answers)
	}

	// The sparse fact reached the thread's rollout.
	record, err := store.Read(threadID, true, true)
	if err != nil || record == nil {
		t.Fatalf("Read() = %#v/%v", record, err)
	}
	lines, _, err := rollout.Load(router.services.ThreadRouter.threadRolloutPath(record))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	events := rollout.RetainedContextEvents(lines)
	if len(events) != 1 || events[0].Answer.CallID != "call-1" || events[0].AcceptanceOrder == nil {
		t.Fatalf("rollout retained events = %#v", events)
	}
	if *events[0].AcceptanceOrder < 1 {
		t.Fatalf("acceptance order = %d, want the answer ordered after the accepted user message", *events[0].AcceptanceOrder)
	}

	// Re-recording the same acceptance is idempotent, as Rust's `record` is.
	recorder("call-1", []retainedctx.VerifiedQuestionAnswer{
		{Question: "Pick one?\nB: Second", Answer: "B"},
	})
	if got := retainedOrderedTexts(router.retainedContextForThread(string(threadID)).OrderedEntries()); !reflect.DeepEqual(got, []string{"Keep the repository private.", "B"}) {
		t.Fatalf("repeated record = %#v", got)
	}
}

// Mirrors Rust's resume rule: a restarted process restores the newest
// checkpoint's snapshot and then replays the sparse facts recorded after it, so a
// verified answer survives without a compaction.
func TestRuntimeRouterReplaysVerifiedAnswersAfterRestartLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home)
	threadID := session.ThreadID("thread-verified-replay")
	if err := store.Create(&session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		Items:     []session.Item{{ID: "user-1", Type: "message", Role: "user", Text: "Keep the repository private."}},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	router.retainedVerifiedAnswerRecorder(string(threadID), "turn-1")("call-1", []retainedctx.VerifiedQuestionAnswer{
		{Question: "Proceed?", Answer: "yes"},
	})

	// A fresh router stands in for a restarted process: its in-memory evidence is
	// gone, so the answer comes back from the rollout.
	fresh := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	restored := fresh.retainedContextForThread(string(threadID))
	if restored == nil {
		t.Fatal("retainedContextForThread() = nil after restart")
	}
	if got := retainedOrderedTexts(restored.OrderedEntries()); !reflect.DeepEqual(got, []string{"Keep the repository private.", "yes"}) {
		t.Fatalf("restored retained evidence = %#v", got)
	}
	answers := restored.VerifiedAnswers()
	if len(answers) != 1 || answers[0].Questions[0].Question != "Proceed?" || answers[0].Questions[0].Answer != "yes" {
		t.Fatalf("restored verified answers = %#v", answers)
	}
}

// Mirrors Rust's review prompt: the retained verified answers render their own
// section, so the reviewer sees the answers the host accepted for the thread.
func TestModelGuardianReviewerIncludesVerifiedAnswersLikeRust(t *testing.T) {
	retained := &retainedctx.RetainedContext{}
	retained.Record(retainedctx.RetainedContextEvent{
		Answer: retainedctx.VerifiedAnswer{
			TurnID:    "turn-1",
			CallID:    "call-1",
			Questions: []retainedctx.VerifiedQuestionAnswer{{Question: "Proceed?", Answer: "yes"}},
		},
	})

	var captured *model.AgentRequest
	reviewer := &modelGuardianReviewer{
		agent: guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
			captured = request
			return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"ok"}`}, nil
		}),
		store:           state.NewReviewStore(),
		retainedContext: func(threadID, turnID string) *retainedctx.RetainedContext { return retained },
	}
	if _, _, err := reviewer.Review(context.Background(), "thread-1", "turn-1", "call-1", state.Action{Type: "command", Command: "rm -rf /", CWD: "/repo"}); err != nil {
		t.Fatalf("Review() error = %v", err)
	}
	if captured == nil {
		t.Fatal("Review() produced no request")
	}
	for _, want := range []string{
		">>> TRUSTED USER ANSWERS START",
		"Retained source order: 0",
		"assistant: Proceed?",
		"user: yes",
		">>> TRUSTED USER ANSWERS END",
	} {
		if !strings.Contains(captured.Prompt, want) {
			t.Fatalf("prompt is missing %q:\n%s", want, captured.Prompt)
		}
	}
}

// Mirrors Rust's feature gate: the recorder is installed only when the
// guardian-approval feature is enabled (request_user_input.rs).
func TestVerifiedAnswerRecorderHonorsTheGuardianApprovalFeatureLikeRust(t *testing.T) {
	router := &RuntimeRouter{}
	if recorder := router.verifiedAnswerRecorderForTurn("thread-1", "turn-1", map[string]bool{"guardian_approval": false}); recorder != nil {
		t.Fatal("the recorder was installed with the feature disabled")
	}
	if recorder := router.verifiedAnswerRecorderForTurn("thread-1", "turn-1", map[string]bool{"guardian_approval": true}); recorder == nil {
		t.Fatal("the recorder was not installed with the feature enabled")
	}
	// The feature is stable and default-enabled, so an unset map installs it too.
	if recorder := router.verifiedAnswerRecorderForTurn("thread-1", "turn-1", nil); recorder == nil {
		t.Fatal("the recorder was not installed with the feature defaulting to enabled")
	}
}
