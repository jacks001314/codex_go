package appserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/compact"
	"codex_go/model"
	"codex_go/protocol"
	"codex_go/session"
	"codex_go/state"
	"codex_go/turn"
)

func mustRequestCountry(t *testing.T, code string) protocol.RequestCountry {
	t.Helper()
	country, err := protocol.ParseRequestCountry(code)
	if err != nil {
		t.Fatalf("ParseRequestCountry(%q) error = %v", code, err)
	}
	return country
}

// Mirrors Rust #61535: the thread's trusted host country flows from thread
// start into the sampling request body.
func TestThreadStartRequestCountryReachesSamplingRequestLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	sink := NewNotificationBuffer()
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	router.SetNotificationSink(sink)
	defer router.Close()

	country := "de"
	started := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD: t.TempDir(), Model: "gpt-test", RequestCountry: &country,
	}))
	if started.Error != nil {
		t.Fatalf("thread/start error: %+v", started.Error)
	}
	threadID := started.Result.(*ThreadStartResponse).Thread.ID

	turnStart := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID, Prompt: "hello",
	}))
	if turnStart.Error != nil {
		t.Fatalf("turn/start error: %+v", turnStart.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	if code, ok := request.RequestCountry.KnownCountry(); !ok || code != "DE" {
		t.Fatalf("sampling request country = %#v, want DE", request.RequestCountry)
	}
	waitForTurnCompletedStatus(t, sink, turnStart.Result.(*turn.TurnStartResponse).Turn.ID, TurnStatusCompleted)
}

// Mirrors Rust #61535: a live turn settings update applies to the next step and
// leaves the thread's saved default untouched.
func TestTurnSettingsUpdateRequestCountryReachesNextStepWithoutChangingThreadDefaultLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	defer router.Close()
	const (
		threadID = "thread-country-step"
		turnID   = "turn-country-step"
	)
	country := "DE"
	if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{
		ThreadID: threadID, RequestCountry: &country,
	}); err != nil {
		t.Fatalf("register active turn: %v", err)
	}
	router.updateActiveRuntimeTurnAnalytics(threadID, turnID, "", &appTurnRunConfig{
		RequestCountry: mustRequestCountry(t, "DE"),
	})
	update := "US"
	if !router.updateActiveTurnRuntimeSettings(&turn.TurnSettingsUpdateParams{
		ThreadID: threadID, TurnID: turnID, RequestCountry: &update,
	}) {
		t.Fatal("turn settings update was not applied")
	}
	settings := router.turnStepSettingsProvider(threadID, turnID)()
	if settings == nil || settings.RequestCountry == nil {
		t.Fatalf("next step settings = %#v, want a request country", settings)
	}
	if code, ok := settings.RequestCountry.KnownCountry(); !ok || code != "US" {
		t.Fatalf("next step country = %#v, want US", settings.RequestCountry)
	}
	if got := router.requestCountryForThread(threadID); !got.IsOmitted() {
		t.Fatalf("thread default country = %#v, want it unchanged (omitted)", got)
	}
}

// Mirrors Rust #61535: a spawned agent inherits the issuing step's captured
// request country as its own thread default.
func TestSpawnedAgentInheritsParentRequestCountryLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	parent := &session.Record{ID: "parent-country", SessionID: "parent-country", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-5.4", ModelProvider: "openai"}}
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	defer router.Close()

	if err := router.registerActiveRuntimeTurn(string(parent.ID), "parent-turn", func() {}, now.UnixMilli(), &turn.TurnStartParams{
		ThreadID: string(parent.ID), Model: "gpt-5.4",
	}); err != nil {
		t.Fatalf("register parent turn: %v", err)
	}
	router.updateActiveRuntimeTurnAnalytics(string(parent.ID), "parent-turn", "", &appTurnRunConfig{
		Model:          "gpt-5.4",
		RequestCountry: mustRequestCountry(t, "DE"),
	})
	controller := newRuntimeAgentControllerForTurn(router, string(parent.ID), "parent-turn", "root-turn", "delegated", "", parent.Metadata.CWD, 4, agent.VersionV1, nil).(*runtimeAgentController)
	message := "do the work"
	child, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{ResolvedRole: "worker", Message: &message})
	if err != nil {
		t.Fatalf("SpawnAgent() error = %v", err)
	}
	childRecord, err := store.Read(session.ThreadID(child.AgentID), true, false)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(stringFromMap(childRecord.Metadata.Extra, requestCountryExtraKey)); got != "DE" {
		t.Fatalf("child record country = %q, want DE", got)
	}
	if code, ok := router.requestCountryForThread(child.AgentID).KnownCountry(); !ok || code != "DE" {
		t.Fatalf("child inherited country = %#v, want DE", router.requestCountryForThread(child.AgentID))
	}
}

// Mirrors Rust #61535: remote compaction issues its request with the step's
// captured country.
func TestRemoteCompactionCarriesCapturedRequestCountryLikeRust(t *testing.T) {
	var captured *model.AgentRequest
	agent := guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		captured = request
		return &model.AgentResponse{Message: "summary"}, nil
	})
	runner := &agentCompactRunner{agent: agent, model: "gpt-test", requestCountry: mustRequestCountry(t, "DE")}
	result, err := runner.Compact(context.Background(), &compact.Request{
		ThreadID: "thread-compact-country", TurnID: "turn-compact-country",
		Trigger: compact.TriggerAuto, Reason: compact.ReasonContextWindowExceeded, Phase: compact.PhaseMidTurn,
		Prompt: "summarize",
	})
	if err != nil || result == nil {
		t.Fatalf("Compact() result=%#v err=%v", result, err)
	}
	if captured == nil {
		t.Fatal("compaction request was not issued")
	}
	if code, ok := captured.RequestCountry.KnownCountry(); !ok || code != "DE" {
		t.Fatalf("compaction request country = %#v, want DE", captured.RequestCountry)
	}
}

// Mirrors Rust #61535: a Guardian review forwards the captured parent country.
func TestGuardianReviewRequestCarriesCapturedRequestCountryLikeRust(t *testing.T) {
	const (
		threadID    = "thread-country-review"
		turnID      = "turn-country-review"
		parentModel = "gpt-parent"
	)
	var captured *model.AgentRequest
	agent := guardianAgentFunc(func(_ context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
		copyRequest := *request
		captured = &copyRequest
		return &model.AgentResponse{Message: `{"riskLevel":"low","userAuthorization":"high","outcome":"allow","rationale":"country"}`}, nil
	})
	router := NewRuntimeRouter(RuntimeServices{
		Agent: agent,
		Models: model.NewModelService(model.NewStaticModelsManagerWithOrigin(
			model.ModelsResponse{Models: []model.ModelInfo{{
				Slug: parentModel, Visibility: "list", SupportedInAPI: true,
				DefaultReasoningLevel: "medium", SupportedReasoningLevels: []string{"low", "medium", "high"},
			}}}, model.ModelOriginProvider,
		)),
		ThreadStatus: NewThreadStatusManager(),
	})
	if err := router.registerActiveRuntimeTurn(threadID, turnID, func() {}, time.Now().UnixMilli(), &turn.TurnStartParams{
		ThreadID: threadID, Model: parentModel,
	}); err != nil {
		t.Fatalf("register active turn: %v", err)
	}
	router.updateActiveRuntimeTurnAnalytics(threadID, turnID, "", &appTurnRunConfig{
		Model:          parentModel,
		RequestCountry: mustRequestCountry(t, "DE"),
	})
	reviewer := router.ensureGuardianReviewer(agent)
	decision, _, err := reviewer.Review(context.Background(), threadID, turnID, "call-country", state.Action{
		Type: "mcp_tool_call", Server: "apps", ToolName: "calendar",
	})
	if err != nil || decision != state.DecisionApproved {
		t.Fatalf("Guardian review decision=%s err=%v", decision, err)
	}
	if captured == nil {
		t.Fatal("guardian review request was not issued")
	}
	if code, ok := captured.RequestCountry.KnownCountry(); !ok || code != "DE" {
		t.Fatalf("guardian review request country = %#v, want DE", captured.RequestCountry)
	}
}
