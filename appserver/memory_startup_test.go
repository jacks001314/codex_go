package appserver

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/codexapi"
	"codex_go/config"
	"codex_go/memories"
	"codex_go/model"
	"codex_go/rollout"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/state"
	"codex_go/turn"
)

type memoryTestAgent struct {
	response string
	requests chan model.AgentRequest
}

type memoryPipelineTestAgent struct {
	requests chan model.AgentRequest
}

func (a *memoryPipelineTestAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.requests <- *request
	message := "done"
	if request.OutputSchema != nil {
		message = `{"raw_memory":"startup raw","rollout_summary":"startup summary","rollout_slug":"startup-slug"}`
	}
	return &model.AgentResponse{
		ResponseID: "memory-pipeline-response", Message: message,
		Items: []model.AgentItem{{ID: "memory-pipeline-message", Type: "agent_message", Text: message}},
		Model: request.Model, ProviderID: request.ProviderID,
	}, nil
}

func (a *memoryTestAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	a.requests <- *request
	return &model.AgentResponse{
		ResponseID: "memory-response",
		Message:    a.response,
		Items:      []model.AgentItem{{ID: "memory-message", Type: "agent_message", Text: a.response}},
		Model:      request.Model,
		ProviderID: request.ProviderID,
	}, nil
}

func TestMemoryStageOneUsesDetachedResponsesRequestLikeRust(t *testing.T) {
	home := t.TempDir()
	cwd := filepath.Join(home, "workspace")
	initMemoryTestGitRepo(t, cwd)
	agent := &memoryTestAgent{
		response: `{"raw_memory":"raw","rollout_summary":"summary","rollout_slug":"slug"}`,
		requests: make(chan model.AgentRequest, 1),
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(filepath.Join(home, "sessions"))),
		Config:       config.NewConfigService(home),
		Agent:        agent,
		DefaultCWD:   cwd,
	})
	t.Cleanup(func() { _ = router.Close() })
	extractor := &appServerMemoryStageOne{
		router: router, parentThreadID: "thread-parent", parentCWD: cwd,
		providerID: "openai", originator: "codex_vscode", reasoningSummary: "auto", serviceTier: "default",
	}
	result, err := extractor.ExtractMemory(context.Background(), memories.StageOneExtractionRequest{
		Model: "gpt-memory", Instructions: "extract", Input: "rollout", OutputSchema: memories.StageOneOutputSchema(),
	})
	if err != nil {
		t.Fatalf("ExtractMemory() error = %v", err)
	}
	if result.RawMemory != "raw" || result.RolloutSummary != "summary" || result.RolloutSlug == nil || *result.RolloutSlug != "slug" {
		t.Fatalf("ExtractMemory() = %+v", result)
	}
	request := <-agent.requests
	if request.Model != "gpt-memory" || request.ProviderID != "openai" || request.ReasoningEffort != "low" || request.ReasoningSummary != "auto" || request.ServiceTier != "default" {
		t.Fatalf("agent request model fields = %+v", request)
	}
	if request.Instructions != "extract" || request.Prompt != "rollout" || request.OutputSchema == nil {
		t.Fatalf("agent request prompt fields = %+v", request)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(request.ClientMetadata[codexapi.ClientCodexTurnMetadataHeader]), &metadata); err != nil {
		t.Fatalf("turn metadata error = %v, metadata = %#v", err, request.ClientMetadata)
	}
	if metadata["request_kind"] != "memory" || metadata["session_id"] != nil || metadata["thread_id"] != nil || metadata["turn_id"] != nil || metadata["window_id"] != nil {
		t.Fatalf("detached memory metadata = %#v", metadata)
	}
	if workspaces, ok := metadata["workspaces"].(map[string]any); !ok || len(workspaces) != 1 {
		t.Fatalf("detached memory workspaces = %#v", metadata["workspaces"])
	}
}

func TestMemoryConsolidatorRunsInternalEphemeralTurnAndCleansItUp(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "memories")
	if err := memories.PrepareWorkspace(context.Background(), root); err != nil {
		t.Fatalf("PrepareWorkspace() error = %v", err)
	}
	agent := &memoryTestAgent{response: "done", requests: make(chan model.AgentRequest, 2)}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(filepath.Join(home, "sessions"))),
		Config:       config.NewConfigService(home),
		Models:       model.NewModelService(nil),
		Turns:        turn.NewTurnService(),
		ThreadStatus: NewThreadStatusManager(),
		Agent:        agent,
		DefaultCWD:   root,
	})
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	t.Cleanup(func() { _ = router.Close() })
	profile := sandbox.WorkspaceWritePermissionProfile()
	consolidator := &appServerMemoryConsolidator{
		router: router, providerID: "openai", originator: "codex_vscode", parentProfile: &profile,
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := consolidator.ConsolidateMemory(ctx, memories.ConsolidationRequest{
		Root: root, Prompt: "consolidate", Model: "gpt-consolidate", ReasoningEffort: "medium",
	}); err != nil {
		t.Fatalf("ConsolidateMemory() error = %v", err)
	}
	request := <-agent.requests
	if request.Model != "gpt-consolidate" || request.ReasoningEffort != "medium" || request.Prompt != "consolidate" {
		t.Fatalf("consolidation request = %+v", request)
	}
	if request.ClientMetadata[codexapi.ClientOpenAISubagentHeader] != "memory_consolidation" {
		t.Fatalf("consolidation metadata = %#v", request.ClientMetadata)
	}
	var metadata map[string]any
	if err := json.Unmarshal([]byte(request.ClientMetadata[codexapi.ClientCodexTurnMetadataHeader]), &metadata); err != nil {
		t.Fatalf("consolidation turn metadata error = %v", err)
	}
	if metadata["thread_source"] != "memory_consolidation" {
		t.Fatalf("consolidation turn metadata = %#v", metadata)
	}
	threadID := request.ThreadID
	if _, ok := router.threads.EphemeralRecord(session.ThreadID(threadID), true); ok {
		t.Fatalf("ephemeral consolidation thread %s was not removed", threadID)
	}
	if status := router.requireThreadStatus().LoadedStatusForThread(threadID); status.Type != "notLoaded" {
		t.Fatalf("consolidation status after cleanup = %+v", status)
	}
	for _, notification := range sink.List() {
		if got := notificationThreadID(notification.Params); got == threadID {
			t.Fatalf("internal consolidation notification leaked: method=%s params=%+v", notification.Method, notification.Params)
		}
	}
}

func TestMemoryConsolidationConfigAndSandboxMatchRust(t *testing.T) {
	overrides := memoryConsolidationConfigOverrides()
	featuresMap := overrides["features"].(map[string]any)
	for _, key := range []string{"memories", "multi_agent", "multi_agent_v2", "apps", "enable_mcp_apps", "plugins", "skill_mcp_dependency_install"} {
		if value, ok := featuresMap[key].(bool); !ok || value {
			t.Fatalf("feature %s = %#v, want false", key, featuresMap[key])
		}
	}
	managed := memoryConsolidationSandbox(nil, `C:\memories`)
	if managed.Kind != sandbox.SandboxWorkspaceWrite || managed.NetworkAccess || !managed.ExcludeTmpdirEnvVar || !managed.ExcludeSlashTmp || len(managed.WritableRoots) != 1 {
		t.Fatalf("managed consolidation sandbox = %+v", managed)
	}
	disabledProfile := sandbox.FullAccessPermissionProfile()
	if got := memoryConsolidationSandbox(&disabledProfile, `C:\memories`); got.Kind != sandbox.SandboxDangerFullAccess {
		t.Fatalf("disabled consolidation sandbox = %+v", got)
	}
	externalProfile := sandbox.PermissionProfile{SandboxPolicy: sandbox.NewExternalSandboxPolicy(sandbox.NetworkEnabled)}
	if got := memoryConsolidationSandbox(&externalProfile, `C:\memories`); got.Kind != "external-sandbox" || got.ExternalNetwork != sandbox.NetworkEnabled {
		t.Fatalf("external consolidation sandbox = %+v", got)
	}
}

func TestThreadStartAutomaticallyRunsMemoryStartupPipeline(t *testing.T) {
	home := t.TempDir()
	configText := "[features]\nmemories = true\n\n[memories]\nmax_rollouts_per_startup = 1\nmin_rollout_idle_hours = 1\nmax_rollout_age_days = 10\nmin_rate_limit_remaining_percent = 0\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(configText), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	sqliteConfig, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	stateRuntime, err := state.InitStateRuntime(context.Background(), sqliteConfig, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer stateRuntime.Close()
	updated := time.Now().UTC().Add(-2 * time.Hour).Truncate(time.Second)
	rolloutPath := writeMemoryStartupTestRollout(t, home, "old-memory-thread", updated)
	if err := stateRuntime.ReconcileRollout(context.Background(), rolloutPath, false); err != nil {
		t.Fatal(err)
	}
	if _, err := stateRuntime.StateDB().Exec(`
UPDATE threads SET memory_mode = 'enabled', preview = 'remember startup integration', updated_at = ?, updated_at_ms = ? WHERE id = 'old-memory-thread'`, updated.Unix(), updated.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	agent := &memoryPipelineTestAgent{requests: make(chan model.AgentRequest, 4)}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(filepath.Join(home, "sessions"))),
		StateRuntime: stateRuntime,
		Config:       config.NewConfigService(home),
		Models:       model.NewModelService(nil),
		Turns:        turn.NewTurnService(),
		ThreadStatus: NewThreadStatusManager(),
		Agent:        agent,
		DefaultCWD:   home,
	})
	defer router.Close()
	started := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD: home, Model: "parent-model", ModelProvider: "openai",
	}))
	if started.Error != nil {
		t.Fatalf("thread/start error = %+v", started.Error)
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var raw, summary, slug string
		err := stateRuntime.MemoriesDB().QueryRow(`
SELECT raw_memory, rollout_summary, rollout_slug FROM stage1_outputs WHERE thread_id = 'old-memory-thread'`).Scan(&raw, &summary, &slug)
		if err == nil {
			if raw != "startup raw" || summary != "startup summary" || slug != "startup-slug" {
				t.Fatalf("stage-one output = %q/%q/%q", raw, summary, slug)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("memory startup did not persist stage-one output: %v", err)
		}
		time.Sleep(25 * time.Millisecond)
	}
	select {
	case request := <-agent.requests:
		if request.OutputSchema == nil || request.Model != model.DefaultMemoryExtractionPreferredModel {
			t.Fatalf("startup phase-one request = %+v", request)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("memory startup did not call phase-one model")
	}
}

func TestMemoryRateLimitGuardMatchesRustThreshold(t *testing.T) {
	reached := auth.RateLimitReached
	if memoryRateLimitAllows(auth.RateLimitSnapshot{RateLimitReachedType: &reached}, 25) {
		t.Fatal("reached rate limit allowed memory startup")
	}
	if !memoryRateLimitAllows(auth.RateLimitSnapshot{Primary: &auth.RateLimitWindow{UsedPercent: 75}}, 25) {
		t.Fatal("exact threshold rejected memory startup")
	}
	if memoryRateLimitAllows(auth.RateLimitSnapshot{Secondary: &auth.RateLimitWindow{UsedPercent: 76}}, 25) {
		t.Fatal("secondary rate limit above threshold allowed startup")
	}
}

func initMemoryTestGitRepo(t *testing.T, root string) {
	t.Helper()
	for _, args := range [][]string{
		{"init", root},
		{"-C", root, "config", "user.email", "codex@example.com"},
		{"-C", root, "config", "user.name", "Codex"},
		{"-C", root, "commit", "--allow-empty", "-m", "baseline"},
	} {
		if output, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %s error = %v: %s", strings.Join(args, " "), err, output)
		}
	}
}

func writeMemoryStartupTestRollout(t *testing.T, home, threadID string, now time.Time) string {
	t.Helper()
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, ThreadID: threadID, SessionID: threadID,
		Source: "cli", ThreadSource: "user", CWD: home,
		Model: "gpt-test", ModelProvider: "openai", HistoryMode: "legacy", MemoryMode: "enabled", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(map[string]any{
		"type": "message", "role": "user",
		"content": []any{map[string]any{"type": "input_text", "text": "remember startup integration"}},
	})
	if err := recorder.AppendLine(rollout.Line{Type: "item", Timestamp: now.Format(time.RFC3339Nano), Item: raw}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	return recorder.Path()
}
