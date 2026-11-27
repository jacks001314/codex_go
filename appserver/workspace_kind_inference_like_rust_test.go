package appserver

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/session"
	"codex_go/state"
	"codex_go/turn"
)

// Mirrors Rust #63545: only a TUI composer prompt (a client message id on a
// codex-tui connection) infers the thread's project assignment, in which case
// an assigned thread reports project and everything else projectless.
func TestInferredWorkspaceKindLikeRust(t *testing.T) {
	router, runtime := newProjectTestRuntimeRouter(t)
	if !router.rememberConnectionClientInfo("conn-tui", ClientInfo{Name: "codex-tui", Version: "1.0"}) {
		t.Fatal("client info was not recorded")
	}
	if !router.rememberConnectionClientInfo("conn-other", ClientInfo{Name: "other-client", Version: "1.0"}) {
		t.Fatal("client info was not recorded")
	}

	if got := router.inferredWorkspaceKind("conn-tui", &turn.TurnStartParams{ThreadID: "unknown-thread", ClientUserMessageID: "msg-1"}); got != "projectless" {
		t.Fatalf("unassigned thread = %q, want projectless", got)
	}
	if got := router.inferredWorkspaceKind("conn-tui", &turn.TurnStartParams{ThreadID: "unknown-thread"}); got != "" {
		t.Fatalf("helper request = %q, want no inferred kind", got)
	}
	if got := router.inferredWorkspaceKind("conn-other", &turn.TurnStartParams{ThreadID: "unknown-thread", ClientUserMessageID: "msg-1"}); got != "" {
		t.Fatalf("non-TUI connection = %q, want no inferred kind", got)
	}
	if got := router.inferredWorkspaceKind("missing", &turn.TurnStartParams{ThreadID: "unknown-thread", ClientUserMessageID: "msg-1"}); got != "" {
		t.Fatalf("unknown connection = %q, want no inferred kind", got)
	}

	threadID := "00000000-0000-0000-0000-0000000000aa"
	if _, err := runtime.StateDB().ExecContext(context.Background(),
		`INSERT INTO threads (id, rollout_path, created_at, updated_at, source, model_provider, cwd, title, sandbox_policy, approval_mode, archived)
		 VALUES (?, '', 1, 1, 'cli', 'openai', '', '', 'read-only', 'never', 0)`,
		threadID); err != nil {
		t.Fatal(err)
	}
	created, err := runtime.CreateProject(context.Background(), "alpha", nil, nil, nil, "key-workspace-kind")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := runtime.SetThreadProject(context.Background(), threadID, &created.Project.ID); err != nil {
		t.Fatal(err)
	}
	if got := router.inferredWorkspaceKind("conn-tui", &turn.TurnStartParams{ThreadID: threadID, ClientUserMessageID: "msg-2"}); got != "project" {
		t.Fatalf("assigned thread = %q, want project", got)
	}
}

// A TUI composer turn without workspace_kind reports the inferred value on the
// model request while a helper request keeps none (Rust #63545).
func TestTurnStartReportsInferredWorkspaceKindLikeRust(t *testing.T) {
	home := t.TempDir()
	sqlite, err := state.NewSqliteConfig(filepath.Join(home, "state"))
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := state.InitStateRuntime(context.Background(), sqlite, "test-provider")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	agent := newRecordingRuntimeAgent("ok")
	router := NewRuntimeRouter(RuntimeServices{
		StateRuntime: runtime,
		ThreadRouter: NewRouter(session.NewStore(filepath.Join(home, "sessions"))),
		Config:       config.NewConfigService(home),
		Turns:        turn.NewTurnService(),
		Agent:        agent,
		ThreadStatus: NewThreadStatusManager(),
	})
	t.Cleanup(func() { _ = router.Close() })
	router.SetNotificationSink(NewNotificationBuffer())
	if !router.rememberConnectionClientInfo("", ClientInfo{Name: "codex-tui", Version: "1.0"}) {
		t.Fatal("client info was not recorded")
	}

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:    t.TempDir(),
		Prompt: "hello workspace kind",
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	composer := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID:            threadID,
		Prompt:              "hello workspace kind",
		ClientUserMessageID: "msg-tui-1",
	}))
	if composer.Error != nil {
		t.Fatalf("composer turn start error: %+v", composer.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	var metadata map[string]any
	if err := json.Unmarshal([]byte(request.ClientMetadata["x-codex-turn-metadata"]), &metadata); err != nil {
		t.Fatalf("turn metadata json error = %v", err)
	}
	if metadata["workspace_kind"] != "projectless" {
		t.Fatalf("workspace_kind = %#v, want projectless", metadata["workspace_kind"])
	}

	helper := router.Handle(requestWithParams(t, IntID(3), MethodTurnStart, turn.TurnStartParams{
		ThreadID: threadID,
		Prompt:   "helper request",
	}))
	if helper.Error != nil {
		t.Fatalf("helper turn start error: %+v", helper.Error)
	}
	helperRequest := waitForRuntimeAgentRequest(t, agent)
	var helperMetadata map[string]any
	if err := json.Unmarshal([]byte(helperRequest.ClientMetadata["x-codex-turn-metadata"]), &helperMetadata); err != nil {
		t.Fatalf("helper turn metadata json error = %v", err)
	}
	if _, ok := helperMetadata["workspace_kind"]; ok {
		t.Fatalf("helper request carried an inferred workspace_kind: %#v", helperMetadata["workspace_kind"])
	}
}
