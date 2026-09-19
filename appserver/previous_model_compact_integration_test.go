package appserver

import (
	"context"
	"os"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// recordingTurnAgent answers every sampling request with a completed turn.
type recordingTurnAgent struct{}

func (a *recordingTurnAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	return &model.AgentResponse{
		ResponseID: "resp-" + request.TurnID,
		Message:    "done",
		Items:      []model.AgentItem{{ID: "msg-" + request.TurnID, Type: "agent_message", Text: "done"}},
	}, nil
}

// End to end for the previous-model compaction foundation (#46324): a completed
// turn persists a `turn_context` record carrying the turn's model and compaction
// hash, a switched model whose hash differs compacts with the previous model
// before sampling, and the resumed thread compares against the recorded model.
func TestRuntimeTurnStartRunsPreviousModelCompactionLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(home + "/sessions")
	sink := NewNotificationBuffer()
	runner := &recordingCompactRunner{summary: "previous model summary"}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter:  NewRouter(store),
		Turns:         turn.NewTurnService(),
		Agent:         &recordingTurnAgent{},
		ThreadStatus:  NewThreadStatusManager(),
		CompactRunner: runner,
		Config:        config.NewConfigService(home),
		Models: model.NewModelService(model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{
			{Slug: "gpt-previous", ContextWindow: 200000, CompHash: "hash-previous"},
			{Slug: "gpt-current", ContextWindow: 200000, CompHash: "hash-current"},
		}})),
	})
	router.SetNotificationSink(sink)

	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:   home,
		Model: "gpt-previous",
	}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID

	first := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "first turn",
		Model:    "gpt-previous",
	}))
	if first.Error != nil {
		t.Fatalf("first turn start error: %+v", first.Error)
	}
	waitForTurnCompletedStatus(t, sink, first.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)

	previousModel, previousHash, ok := router.runtimePreviousTurnSettings(threadID, nil)
	if !ok || previousModel != "gpt-previous" || previousHash != "hash-previous" {
		t.Fatalf("recorded previous settings = %q, %q, %v; want gpt-previous, hash-previous, true", previousModel, previousHash, ok)
	}
	record, err := store.Read(session.ThreadID(threadID), true, false)
	if err != nil {
		t.Fatalf("read thread error: %v", err)
	}
	raw, err := os.ReadFile(router.services.ThreadRouter.threadRolloutPath(record))
	if err != nil {
		t.Fatalf("ReadFile(rollout) error = %v", err)
	}
	if !strings.Contains(string(raw), `"turn_context"`) || !strings.Contains(string(raw), `"comp_hash":"hash-previous"`) {
		t.Fatalf("rollout is missing the turn-context record:\n%s", raw)
	}

	// Switching to a model whose compaction hash differs compacts with the
	// previous model before the new turn samples.
	second := router.Handle(requestWithParams(t, IntID(3), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "second turn",
		Model:    "gpt-current",
	}))
	if second.Error != nil {
		t.Fatalf("second turn start error: %+v", second.Error)
	}
	waitForTurnCompletedStatus(t, sink, second.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)
	if runner.request == nil {
		t.Fatal("previous-model compaction did not run for a changed compaction hash")
	}
}
