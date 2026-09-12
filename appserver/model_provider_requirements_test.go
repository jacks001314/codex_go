package appserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/config"
	"codex_go/model"
	"codex_go/review"
	"codex_go/session"
	"codex_go/turn"
)

const managedProviderRequirements = `
model_provider = "gateway"
[model_providers.gateway]
name = "Gateway"
base_url = "https://gateway.example/v1"
[model_providers.other]
name = "Other"
base_url = "https://other.example/v1"
`

func writeManagedProviderRequirements(t *testing.T, home, contents string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(contents), 0o600); err != nil {
		t.Fatalf("write requirements.toml: %v", err)
	}
}

func newManagedProviderRouter(t *testing.T, home string) *RuntimeRouter {
	t.Helper()
	store := session.NewStore(home)
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
	})
	t.Cleanup(func() { _ = router.Close() })
	return router
}

func startManagedProviderThread(t *testing.T, router *RuntimeRouter) string {
	t.Helper()
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{}))
	if response.Error != nil {
		t.Fatalf("thread/start error = %+v", response.Error)
	}
	started, ok := response.Result.(*ThreadStartResponse)
	if !ok || started.Thread == nil {
		t.Fatalf("thread/start result = %#v", response.Result)
	}
	return started.Thread.ID
}

func TestRuntimeRouterRetainsManagedModelProviderRoute(t *testing.T) {
	home := t.TempDir()
	writeManagedProviderRequirements(t, home, managedProviderRequirements)
	router := newManagedProviderRouter(t, home)
	threadID := startManagedProviderThread(t, router)

	route, ok := router.services.ThreadRouter.threads.ModelProviderRoute(session.ThreadID(threadID))
	if !ok {
		t.Fatalf("expected a retained provider route for %s", threadID)
	}
	if route.providerID != "gateway" {
		t.Fatalf("retained provider id = %q, want gateway", route.providerID)
	}
	if route.provider.BaseURL != "https://gateway.example/v1" {
		t.Fatalf("retained provider base url = %q", route.provider.BaseURL)
	}
}

func TestManagedModelProviderSelectionChangeRejectsRetainedThread(t *testing.T) {
	home := t.TempDir()
	writeManagedProviderRequirements(t, home, managedProviderRequirements)
	router := newManagedProviderRouter(t, home)
	threadID := startManagedProviderThread(t, router)

	writeManagedProviderRequirements(t, home, strings.Replace(managedProviderRequirements, `model_provider = "gateway"`, `model_provider = "other"`, 1))

	response := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Input:    []turn.TurnUserInput{{Text: "continue"}},
	}))
	assertManagedProviderRejection(t, response)
}

func TestManagedModelProviderDefinitionChangeRejectsRetainedThread(t *testing.T) {
	home := t.TempDir()
	writeManagedProviderRequirements(t, home, managedProviderRequirements)
	router := newManagedProviderRouter(t, home)
	threadID := startManagedProviderThread(t, router)

	writeManagedProviderRequirements(t, home, strings.Replace(managedProviderRequirements, "https://gateway.example/v1", "https://other.example/v1", 1))

	response := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Input:    []turn.TurnUserInput{{Text: "continue"}},
	}))
	assertManagedProviderRejection(t, response)
}

func TestManagedModelProviderUnrelatedChangeKeepsRetainedThread(t *testing.T) {
	home := t.TempDir()
	writeManagedProviderRequirements(t, home, managedProviderRequirements)
	router := newManagedProviderRouter(t, home)
	threadID := startManagedProviderThread(t, router)

	// Changing a provider the thread does not use, or the local config, must not
	// invalidate the retained route (Rust local_config_changes_do_not_block).
	writeManagedProviderRequirements(t, home, strings.Replace(managedProviderRequirements, "https://other.example/v1", "https://elsewhere.example/v1", 1))
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("model_provider = 'openai'\n"), 0o600); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	if err := router.checkThreadModelProviderForID(threadID); err != nil {
		t.Fatalf("unrelated requirement change rejected the thread: %v", err)
	}
}

func TestManagedModelProviderInvalidRequirementsRejectRetainedThread(t *testing.T) {
	home := t.TempDir()
	writeManagedProviderRequirements(t, home, managedProviderRequirements)
	router := newManagedProviderRouter(t, home)
	threadID := startManagedProviderThread(t, router)

	writeManagedProviderRequirements(t, home, "invalid toml !!!")
	if err := router.checkThreadModelProviderForID(threadID); err == nil {
		t.Fatal("expected invalid requirements to reject the retained thread")
	}

	// A definition that cannot describe a usable provider is invalid policy.
	writeManagedProviderRequirements(t, home, "[model_providers.gateway]\nbase_url = 'https://example.test'")
	if err := router.checkThreadModelProviderForID(threadID); err == nil {
		t.Fatal("expected incomplete provider definition to reject the retained thread")
	}
}

func TestRequiredModelProviderDefinitionMergesBedrockOverrides(t *testing.T) {
	for _, providerID := range []string{model.AmazonBedrockProviderID, model.AmazonBedrockRuntimeProviderID} {
		definition := map[string]any{"aws": map[string]any{"region": "us-west-2"}}
		resolved, err := model.RequiredModelProviderDefinition(providerID, definition)
		if err != nil {
			t.Fatalf("%s resolution error = %v", providerID, err)
		}
		if resolved == nil || resolved.AWS == nil || resolved.AWS.Region != "us-west-2" {
			t.Fatalf("%s resolved = %#v", providerID, resolved)
		}
		builtIn := model.BuiltInProviders("")[providerID]
		if resolved.Name != builtIn.Name || resolved.RequiresOpenAIAuth != builtIn.RequiresOpenAIAuth {
			t.Fatalf("%s lost bundled provider traits: %#v", providerID, resolved)
		}
	}
}

func TestManagedModelProviderChangeRejectsAllRetainedThreadEntryPoints(t *testing.T) {
	home := t.TempDir()
	writeManagedProviderRequirements(t, home, managedProviderRequirements)
	router := newManagedProviderRouter(t, home)
	threadID := startManagedProviderThread(t, router)

	original := "Original goal"
	setOriginal := router.Handle(requestWithParams(t, IntID(2), MethodThreadGoalSet, &GoalSetParams{ThreadID: threadID, Objective: &original}))
	if setOriginal.Error != nil {
		t.Fatalf("goal set before policy change error = %+v", setOriginal.Error)
	}

	writeManagedProviderRequirements(t, home, strings.Replace(managedProviderRequirements, `model_provider = "gateway"`, `model_provider = "other"`, 1))

	detached := "detached"
	queuedID := "queued-1"
	blockedObjective := "Blocked objective"
	cases := []struct {
		name   string
		method Method
		params any
	}{
		{"turn/start", MethodTurnStart, turn.TurnStartParams{ThreadID: threadID, Input: []turn.TurnUserInput{{Text: "continue"}}}},
		{"turn/steer", MethodTurnSteer, turn.TurnSteerParams{ThreadID: threadID, ExpectedTurnID: "turn-1", Input: []turn.TurnUserInput{{Text: "continue"}}}},
		{"review/start inline", MethodReviewStart, &review.StartParams{ThreadID: threadID, Target: review.APITarget{Type: "custom", Instructions: "Review this change."}}},
		{"review/start detached", MethodReviewStart, &review.StartParams{ThreadID: threadID, Target: review.APITarget{Type: "custom", Instructions: "Review this change."}, Delivery: &detached}},
		{"thread/compact/start", MethodThreadCompactStart, &ThreadCompactStartParams{ThreadID: threadID}},
		{"thread/queue/start", MethodThreadQueueStart, &ThreadQueueStartParams{ThreadID: threadID, QueuedSubmissionID: &queuedID}},
		{"thread/goal/set", MethodThreadGoalSet, &GoalSetParams{ThreadID: threadID, Objective: &blockedObjective}},
	}
	for index, tc := range cases {
		response := router.Handle(requestWithParams(t, IntID(int64(100+index)), tc.method, tc.params))
		assertManagedProviderRejection(t, response)
	}

	// The existing goal is untouched, and pausing/clearing it stays available.
	getGoal := router.Handle(requestWithParams(t, IntID(200), MethodThreadGoalGet, &GoalGetParams{ThreadID: threadID}))
	if getGoal.Error != nil {
		t.Fatalf("goal get error = %+v", getGoal.Error)
	}
	fetched, ok := getGoal.Result.(*GoalGetResponse)
	if !ok || fetched.Goal == nil || fetched.Goal.Objective != original {
		t.Fatalf("goal after rejection = %#v", getGoal.Result)
	}
	paused := GoalPaused
	if response := router.Handle(requestWithParams(t, IntID(201), MethodThreadGoalSet, &GoalSetParams{ThreadID: threadID, Status: &paused})); response.Error != nil {
		t.Fatalf("goal pause error = %+v", response.Error)
	}
	if response := router.Handle(requestWithParams(t, IntID(202), MethodThreadGoalClear, &GoalClearParams{ThreadID: threadID})); response.Error != nil {
		t.Fatalf("goal clear error = %+v", response.Error)
	}
}

func assertManagedProviderRejection(t *testing.T, response *Response) {
	t.Helper()
	if response.Error == nil {
		t.Fatalf("expected managed provider rejection, got %#v", response.Result)
	}
	if response.Error.Code != -32600 {
		t.Fatalf("error code = %d, want -32600", response.Error.Code)
	}
	want := "failed to load configuration: " + errModelProviderRequirementsChanged.Error()
	if response.Error.Message != want {
		t.Fatalf("error message = %q, want %q", response.Error.Message, want)
	}
}
