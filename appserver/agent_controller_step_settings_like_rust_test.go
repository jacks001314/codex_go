package appserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// Mirrors Rust #46075 for the V1 backend: a spawn builds the child from the
// invoking step's captured settings (model, effective reasoning effort, reasoning
// summary) rather than the parent thread record's older values.
func TestRuntimeAgentControllerV1SpawnUsesCapturedStepSettingsLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	parent := &session.Record{ID: "parent", SessionID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-5.4-old", ModelProvider: "openai"}}
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	defer router.Close()

	effort, summary := "high", "concise"
	captured := &turn.TurnStartParams{ThreadID: "parent", Model: "gpt-5.4", Effort: &effort, Summary: &summary}
	if err := router.threads.RegisterTurn("parent", "parent-turn", func() {}, now.UnixMilli(), captured); err != nil {
		t.Fatal(err)
	}
	controller := newRuntimeAgentControllerForTurn(router, "parent", "parent-turn", "root-turn", "delegated", "", parent.Metadata.CWD, 4, agent.VersionV1, nil).(*runtimeAgentController)
	message := "do the work"
	child, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{ResolvedRole: "worker", Message: &message})
	if err != nil {
		t.Fatal(err)
	}
	childRecord, err := store.Read(session.ThreadID(child.AgentID), true, false)
	if err != nil {
		t.Fatal(err)
	}
	if childRecord.Metadata.Model != "gpt-5.4" {
		t.Fatalf("child model = %q, want the captured step model", childRecord.Metadata.Model)
	}
	active := router.threads.ActiveTurn(child.AgentID)
	if active == nil || active.Params == nil {
		t.Fatalf("child turn was not started: %#v", active)
	}
	if stringPtrValue(active.Params.Effort) != "high" || stringPtrValue(active.Params.Summary) != "concise" {
		t.Fatalf("child settings = effort %q summary %q, want the captured step settings",
			stringPtrValue(active.Params.Effort), stringPtrValue(active.Params.Summary))
	}
}

// Mirrors Rust #46075's effort-only case: after a mid-turn settings update the
// requested effort is validated against the captured model's supported levels,
// not the model the turn started with.
func TestRuntimeAgentControllerSpawnValidatesEffortAgainstCapturedModelLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	// The thread record still holds the turn's initial model, which accepts "low".
	parent := &session.Record{ID: "parent", SessionID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), Model: "model-a", ModelProvider: "openai"}}
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	manager := model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{
		{Slug: "model-a", DisplayName: "model-a", Visibility: model.VisibilityList, SupportedInAPI: true, SupportedReasoningLevels: []string{"low"}},
		{Slug: "model-b", DisplayName: "model-b", Visibility: model.VisibilityList, SupportedInAPI: true, SupportedReasoningLevels: []string{"medium", "high"}},
	}})
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Agent:        &model.ResponsesAgentRunner{ModelsManager: manager},
	})
	defer router.Close()

	// The settings update during the active turn switched the step to model-b.
	captured := &turn.TurnStartParams{ThreadID: "parent", Model: "model-b"}
	if err := router.threads.RegisterTurn("parent", "parent-turn", func() {}, now.UnixMilli(), captured); err != nil {
		t.Fatal(err)
	}
	controller := newRuntimeAgentControllerForTurn(router, "parent", "parent-turn", "root-turn", "delegated", "", parent.Metadata.CWD, 4, agent.VersionV2, nil).(*runtimeAgentController)

	// "medium" is supported by the captured model only.
	args := &agent.SpawnAgentArgs{ReasoningEffort: stringPtr("medium")}
	if err := controller.resolveSpawnModelOverrides(args); err != nil {
		t.Fatalf("effort supported by the captured model was rejected: %v", err)
	}
	if args.Model != nil {
		t.Fatalf("effort-only override resolved a model: %#v", args.Model)
	}
	if args.ReasoningEffort == nil || *args.ReasoningEffort != "medium" {
		t.Fatalf("resolved effort = %#v, want medium", args.ReasoningEffort)
	}

	// "low" is supported by the turn's initial model only, so validating against
	// the stale model would wrongly accept it.
	err := controller.resolveSpawnModelOverrides(&agent.SpawnAgentArgs{ReasoningEffort: stringPtr("low")})
	if err == nil {
		t.Fatal("effort unsupported by the captured model was accepted")
	}
	if want := "is not supported for model `model-b`"; !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %v, want it to name the captured model (%s)", err, want)
	}
}
