package appserver

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
)

const prewarmEffortTestModel = "gpt-5.4"

func prewarmEffortModels() *model.StaticModelsManager {
	return model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{{
		Slug: prewarmEffortTestModel, DisplayName: prewarmEffortTestModel,
		Visibility: model.VisibilityList, SupportedInAPI: true,
		SupportsReasoningEffortUpdates: true, DefaultReasoningLevel: "medium",
	}}})
}

func newPrewarmEffortAgent(responseID string) *historyPrewarmAgent {
	return &historyPrewarmAgent{prewarmingRuntimeAgent: &prewarmingRuntimeAgent{
		recordingRuntimeAgent: newRecordingRuntimeAgent("ok"),
		prewarmedID:           responseID,
	}}
}

func waitForPrewarmRequest(t *testing.T, agent *historyPrewarmAgent, previous int) *model.AgentRequest {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		agent.mu.Lock()
		var request *model.AgentRequest
		if len(agent.requests) > previous {
			request = agent.requests[len(agent.requests)-1]
		}
		agent.mu.Unlock()
		if request != nil {
			return request
		}
		if time.Now().After(deadline) {
			t.Fatal("prewarm request was not issued")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func writeReasoningEffortConfig(t *testing.T, home, featureValue string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(config.ConfigPath(home)), 0o700); err != nil {
		t.Fatal(err)
	}
	body := "[features]\nreasoning_effort_override = " + featureValue + "\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// Mirrors Rust #43795: the startup WebSocket warmup establishes the request
// baseline before the first turn, so a later effort change only adds a trusted
// configuration update while the request keeps the pinned effort.
func TestStartupPrewarmPinsRequestEffortLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(t.TempDir())
	agent := newPrewarmEffortAgent("prewarm-effort-resp")
	manager := prewarmEffortModels()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Agent:        agent,
		Models:       model.NewModelService(manager),
		DefaultCWD:   home,
	})
	defer router.Close()

	const threadID = "thread-prewarm-effort"
	effort := "medium"
	router.scheduleStartupPrewarm(&ThreadStartResponse{
		Thread:          &Thread{ID: threadID, CWD: home},
		CWD:             home,
		Model:           prewarmEffortTestModel,
		ReasoningEffort: &effort,
	})
	request := waitForPrewarmRequest(t, agent, 0)
	if request.ReasoningEffort != "medium" {
		t.Fatalf("prewarm reasoning effort = %q, want medium", request.ReasoningEffort)
	}
	if pinned, ok := router.reasoningEffortPinState(threadID).Get(prewarmEffortTestModel); !ok || pinned != "medium" {
		t.Fatalf("prewarm pin = %q,%v, want medium,true", pinned, ok)
	}

	// The next turn changes the selection to high: it records one update but the
	// sampling request keeps the prewarmed baseline.
	if items := router.reasoningEffortOverrideInputItems(threadID, prewarmEffortTestModel, "high", true, nil); len(items) != 1 {
		t.Fatalf("post-prewarm updates = %#v, want one update", items)
	}
	if got := router.reasoningEffortForRequest(threadID, prewarmEffortTestModel, "high", true, "high", true, requestEffortSampling); got != "medium" {
		t.Fatalf("first turn effort = %q, want the prewarmed medium", got)
	}
}

// Rust #43795 keeps the disabled-feature behavior: the warmup sends the
// selected effort and does not establish a pin, so the first turn follows the
// current selection without a configuration update.
func TestStartupPrewarmWithoutOverrideUsesSelectedEffortLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(t.TempDir())
	writeReasoningEffortConfig(t, home, "false")
	agent := newPrewarmEffortAgent("prewarm-effort-resp")
	manager := prewarmEffortModels()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Agent:        agent,
		Models:       model.NewModelService(manager),
		Config:       config.NewConfigService(home),
		DefaultCWD:   home,
	})
	defer router.Close()

	const threadID = "thread-prewarm-effort-disabled"
	effort := "medium"
	router.scheduleStartupPrewarm(&ThreadStartResponse{
		Thread:          &Thread{ID: threadID, CWD: home},
		CWD:             home,
		Model:           prewarmEffortTestModel,
		ReasoningEffort: &effort,
	})
	request := waitForPrewarmRequest(t, agent, 0)
	if request.ReasoningEffort != "medium" {
		t.Fatalf("prewarm reasoning effort = %q, want the selected medium", request.ReasoningEffort)
	}
	if _, ok := router.reasoningEffortPinState(threadID).Get(prewarmEffortTestModel); ok {
		t.Fatal("disabled overrides must not pin a prewarm effort")
	}
	if got := router.reasoningEffortForRequest(threadID, prewarmEffortTestModel, "high", false, "high", true, requestEffortSampling); got != "high" {
		t.Fatalf("first turn effort = %q, want the selected high", got)
	}
	if items := router.reasoningEffortOverrideInputItems(threadID, prewarmEffortTestModel, "high", false, nil); len(items) != 0 {
		t.Fatalf("disabled override updates = %#v, want none", items)
	}
}

// The idle-history warmup uses the same pinning path as startup prewarm, so a
// reconnecting WebSocket replays the pinned effort instead of the newest
// selection.
func TestHistoryPrewarmUsesPinnedRequestEffortLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	const threadID = "thread-history-prewarm-effort"
	if err := store.Save(&session.Record{
		ID: session.ThreadID(threadID), SessionID: threadID, ParentThreadID: "root",
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: home, Model: prewarmEffortTestModel, ModelProvider: "openai"},
		Items: []session.Item{
			{ID: "user-1", Type: "message", Role: "user", Text: "first task", CreatedAt: now},
			{ID: "assistant-1", Type: "message", Role: "assistant", Text: "first result", CreatedAt: now},
		},
	}); err != nil {
		t.Fatal(err)
	}
	agent := newPrewarmEffortAgent("history-prewarm-effort-resp")
	manager := prewarmEffortModels()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Agent:        agent,
		Models:       model.NewModelService(manager),
		DefaultCWD:   home,
	})
	defer router.Close()
	retainIdleLifecycleThread(t, router, threadID)

	router.scheduleHistoryPrewarm(threadID)
	request := waitForPrewarmRequest(t, agent, 0)
	if request.Originator != "session_startup" || request.PrewarmInputMode != "history" {
		t.Fatalf("history prewarm request = %#v", request)
	}
	if request.ReasoningEffort != "medium" {
		t.Fatalf("history prewarm reasoning effort = %q, want the model's medium", request.ReasoningEffort)
	}
	if pinned, ok := router.reasoningEffortPinState(threadID).Get(prewarmEffortTestModel); !ok || pinned != "medium" {
		t.Fatalf("history prewarm pin = %q,%v, want medium,true", pinned, ok)
	}
}

// A model switch is a new baseline: the new model's selection is recorded and
// pinned, and switching back re-establishes that model's selection even if the
// old pin had the same effort.
func TestReasoningEffortModelSwitchReestablishesBaselineLikeRust(t *testing.T) {
	router := &RuntimeRouter{}
	const threadID = "thread-effort-model-switch"
	router.setReasoningEffortPinState(threadID, reasoningEffortPin{
		Kind: reasoningEffortPinActive, Model: "gpt-5.4", Effort: "medium",
	})

	if items := router.reasoningEffortOverrideInputItems(threadID, "gpt-5.2", "high", true, nil); len(items) != 1 {
		t.Fatalf("model switch updates = %#v, want one update", items)
	}
	if got := router.reasoningEffortForRequest(threadID, "gpt-5.2", "high", true, "high", true, requestEffortSampling); got != "high" {
		t.Fatalf("switched model effort = %q, want high", got)
	}
	if pinned, ok := router.reasoningEffortPinState(threadID).Get("gpt-5.2"); !ok || pinned != "high" {
		t.Fatalf("switched model pin = %q,%v, want high,true", pinned, ok)
	}

	if items := router.reasoningEffortOverrideInputItems(threadID, "gpt-5.4", "medium", true, nil); len(items) != 1 {
		t.Fatalf("switch-back updates = %#v, want one update", items)
	}
	if got := router.reasoningEffortForRequest(threadID, "gpt-5.4", "medium", true, "medium", true, requestEffortSampling); got != "medium" {
		t.Fatalf("switch-back effort = %q, want medium", got)
	}
}

// Rust #43795 clears the runtime pin on resume; the first request afterwards
// re-establishes the selected effort.
func TestThreadResumeClearsReasoningEffortPinLikeRust(t *testing.T) {
	home := t.TempDir()
	now := fixedTime()
	threadID := session.ThreadID("thread-effort-resume")
	store := session.NewStore(filepath.Join(home, "sessions"))
	threadRouter := NewRouter(store)
	record := &session.Record{
		ID: threadID, SessionID: string(threadID), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), SessionPrefix: session.PrefixForSessionID(string(threadID))},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	if err := threadRouter.createThreadRollout(record, now); err != nil {
		t.Fatalf("createThreadRollout() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: threadRouter,
		Config:       config.NewConfigService(home),
	})
	router.setReasoningEffortPinState(string(threadID), reasoningEffortPin{
		Kind: reasoningEffortPinActive, Model: "gpt-5.4", Effort: "medium",
	})
	resumed := router.Handle(requestWithParams(t, IntID(1), MethodThreadResume, ThreadResumeParams{ThreadID: string(threadID)}))
	if resumed.Error != nil {
		t.Fatalf("resume error = %+v", resumed.Error)
	}
	if _, ok := router.reasoningEffortPinState(string(threadID)).Get("gpt-5.4"); ok {
		t.Fatal("resume must clear the request-effort baseline")
	}
	if got := router.reasoningEffortForRequest(string(threadID), "gpt-5.4", "high", true, "high", true, requestEffortSampling); got != "high" {
		t.Fatalf("post-resume effort = %q, want the re-established high", got)
	}
}
