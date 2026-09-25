package appserver

import (
	"context"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/compact"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// Mirrors Rust's TurnContext construction (#44893): the per-turn selection is
// recorded as the core snake_case program only when the turn's provider is the
// OpenAI provider, and an unknown app-server value has no core program.
func TestAppCyberAccessProgramForTurnLikeRust(t *testing.T) {
	blue := turn.CyberAccessProgramDaybreakBlue
	unknown := turn.CyberAccessProgram("futureProgram")
	inherited := "daybreak_red"
	for _, testCase := range []struct {
		name       string
		params     *turn.TurnStartParams
		providerID string
		want       string
	}{
		{name: "selected", params: &turn.TurnStartParams{CyberAccessProgram: &blue}, providerID: model.OpenAIProviderID, want: "daybreak_blue"},
		{name: "unselected", params: &turn.TurnStartParams{}, providerID: model.OpenAIProviderID},
		{name: "no params", providerID: model.OpenAIProviderID},
		{name: "other provider", params: &turn.TurnStartParams{CyberAccessProgram: &blue}, providerID: "bedrock"},
		{name: "unknown selection", params: &turn.TurnStartParams{CyberAccessProgram: &unknown}, providerID: model.OpenAIProviderID},
		{name: "inherited core value", params: &turn.TurnStartParams{CoreCyberAccessProgram: inherited}, providerID: model.OpenAIProviderID, want: "daybreak_red"},
		{name: "selection wins over inherited", params: &turn.TurnStartParams{CyberAccessProgram: &blue, CoreCyberAccessProgram: inherited}, providerID: model.OpenAIProviderID, want: "daybreak_blue"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			if got := appCyberAccessProgramForTurn(testCase.params, testCase.providerID); got != testCase.want {
				t.Fatalf("appCyberAccessProgramForTurn() = %q, want %q", got, testCase.want)
			}
		})
	}
}

type compactAccessProgramAgent struct {
	requests []model.AgentRequest
}

func (a *compactAccessProgramAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.requests = append(a.requests, *request)
	return &model.AgentResponse{ResponseID: "resp-compact", Message: "summary"}, nil
}

// Mirrors Rust #48224: the compaction prompt carries the model/program pair the
// attempt was resolved with, so a remote compaction cannot pair the previous
// model with the current turn's program.
func TestAgentCompactRunnerCarriesTheModelProgramPairLikeRust(t *testing.T) {
	agent := &compactAccessProgramAgent{}
	runner := &agentCompactRunner{agent: agent, model: "gpt-previous", cyberAccessProgram: "daybreak_blue"}
	if _, err := runner.Compact(context.Background(), &compact.Request{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Trigger:  compact.TriggerManual,
		Reason:   compact.ReasonUserRequested,
		Phase:    compact.PhaseStandaloneTurn,
	}); err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if len(agent.requests) != 1 {
		t.Fatalf("agent requests = %#v", agent.requests)
	}
	if agent.requests[0].CyberAccessProgram != "daybreak_blue" {
		t.Fatalf("compaction program = %q, want daybreak_blue", agent.requests[0].CyberAccessProgram)
	}

	// An attempt resolved with no program must not invent one.
	agent = &compactAccessProgramAgent{}
	runner = &agentCompactRunner{agent: agent, model: "gpt-current"}
	if _, err := runner.Compact(context.Background(), &compact.Request{
		ThreadID: "thread-1",
		TurnID:   "turn-1",
		Trigger:  compact.TriggerManual,
		Reason:   compact.ReasonUserRequested,
		Phase:    compact.PhaseStandaloneTurn,
	}); err != nil {
		t.Fatalf("Compact() error = %v", err)
	}
	if agent.requests[0].CyberAccessProgram != "" {
		t.Fatalf("compaction program = %q, want none", agent.requests[0].CyberAccessProgram)
	}
}

// An attempt that fixed the program keeps it verbatim - including an explicitly
// absent one - so the previous-model compaction cannot inherit the current
// turn's selection (Rust #48224). An attempt without a fixed program follows the
// thread's recorded turn context.
func TestCompactProgramForAttemptUsesTheAttemptsTurnLikeRust(t *testing.T) {
	router := &RuntimeRouter{}
	previous := "daybreak_blue"
	if got := router.compactProgramForAttempt(&runtimeCompactRequest{CyberAccessProgram: &previous}, nil); got != "daybreak_blue" {
		t.Fatalf("fixed program = %q, want daybreak_blue", got)
	}
	absent := ""
	if got := router.compactProgramForAttempt(&runtimeCompactRequest{CyberAccessProgram: &absent}, nil); got != "" {
		t.Fatalf("explicitly absent program = %q, want none", got)
	}
	if got := router.compactProgramForAttempt(&runtimeCompactRequest{ThreadID: "thread-unknown"}, nil); got != "" {
		t.Fatalf("unknown thread program = %q, want none", got)
	}
}

// Mirrors Rust's multi-agent spawn/send-input (#44893): a delegated turn starts
// with the initiating turn's cyber access program, gated by the child's own
// provider, so the child keeps the same model/program pair.
func TestSpawnedAgentInheritsTheInitiatingTurnsAccessProgramLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	parent := &session.Record{ID: "parent", SessionID: "parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-5.4", ModelProvider: "openai"}}
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	controller := newRuntimeAgentControllerForTurn(router, "parent", "parent-turn", "root-turn", "delegated", "daybreak_blue", parent.Metadata.CWD, 4, agent.VersionV2, nil).(*runtimeAgentController)
	message := "do the work"
	child, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{ResolvedRole: "worker", Message: &message})
	if err != nil {
		t.Fatal(err)
	}
	active := router.threads.ActiveTurn(child.AgentID)
	if active == nil || active.Params == nil {
		t.Fatalf("child turn was not started: %#v", active)
	}
	if active.Params.CoreCyberAccessProgram != "daybreak_blue" {
		t.Fatalf("child inherited program = %q, want daybreak_blue", active.Params.CoreCyberAccessProgram)
	}
	if got := appCyberAccessProgramForTurn(active.Params, model.OpenAIProviderID); got != "daybreak_blue" {
		t.Fatalf("child OpenAI program = %q, want daybreak_blue", got)
	}
	// The child's own provider gates it: a non-OpenAI child records no program
	// (Rust sets turn_context.cyber_access_program only for the OpenAI provider).
	if got := appCyberAccessProgramForTurn(active.Params, "bedrock"); got != "" {
		t.Fatalf("child non-OpenAI program = %q, want none", got)
	}
}
