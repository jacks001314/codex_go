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

// Mirrors Rust #63545's staged pending thread metadata: a project supplied to
// thread/start decides the first composer turn's workspace kind before the
// thread has a state row, and the persisted row wins afterwards.
func TestInferredWorkspaceKindUsesStagedThreadStartProjectLikeRust(t *testing.T) {
	router, runtime := newProjectTestRuntimeRouter(t)
	if !router.rememberConnectionClientInfo("conn-tui", ClientInfo{Name: "codex-tui", Version: "1.0"}) {
		t.Fatal("client info was not recorded")
	}
	created, err := runtime.CreateProject(context.Background(), "alpha", nil, nil, nil, "key-staged-workspace-kind")
	if err != nil {
		t.Fatal(err)
	}
	threadID := "00000000-0000-0000-0000-0000000000bb"
	router.recordPendingThreadProject(threadID, &created.Project.ID)
	composer := &turn.TurnStartParams{ThreadID: threadID, ClientUserMessageID: "msg-1"}
	if got := router.inferredWorkspaceKind("conn-tui", composer); got != "project" {
		t.Fatalf("staged project = %q, want project", got)
	}

	// The row wins once it exists: a projectless row reports projectless even
	// while a staged assignment is still recorded, and the staged value is
	// dropped.
	if _, err := runtime.StateDB().ExecContext(context.Background(),
		`INSERT INTO threads (id, rollout_path, created_at, updated_at, source, model_provider, cwd, title, sandbox_policy, approval_mode, archived)
		 VALUES (?, '', 1, 1, 'cli', 'openai', '', '', 'read-only', 'never', 0)`, threadID); err != nil {
		t.Fatal(err)
	}
	router.recordPendingThreadProject(threadID, &created.Project.ID)
	if got := router.inferredWorkspaceKind("conn-tui", composer); got != "projectless" {
		t.Fatalf("projectless row = %q, want projectless", got)
	}
	if staged := router.pendingThreadProject(threadID); staged != nil {
		t.Fatalf("staged project = %q, want it dropped once the row exists", *staged)
	}

	// A persisted assignment reports project without any staged value.
	if _, _, err := runtime.SetThreadProject(context.Background(), threadID, &created.Project.ID); err != nil {
		t.Fatal(err)
	}
	if got := router.inferredWorkspaceKind("conn-tui", composer); got != "project" {
		t.Fatalf("assigned row = %q, want project", got)
	}
}

// The thread/start project reaches the first composer turn through the model
// request metadata when the client sends no workspace kind.
func TestTurnStartReportsStagedThreadStartProjectLikeRust(t *testing.T) {
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
	created, err := runtime.CreateProject(context.Background(), "alpha", nil, nil, nil, "key-staged-turn")
	if err != nil {
		t.Fatal(err)
	}
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
	sink := NewNotificationBuffer()
	router.SetNotificationSink(sink)
	if !router.rememberConnectionClientInfo("", ClientInfo{Name: "codex-tui", Version: "1.0"}) {
		t.Fatal("client info was not recorded")
	}

	threadStart := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{
		CWD:       t.TempDir(),
		Prompt:    "hello staged project",
		ProjectID: &created.Project.ID,
	}))
	if threadStart.Error != nil {
		t.Fatalf("thread start error: %+v", threadStart.Error)
	}
	threadID := threadStart.Result.(*ThreadStartResponse).Thread.ID
	composer := router.Handle(requestWithParams(t, IntID(2), MethodTurnStart, turn.TurnStartParams{
		ThreadID:            threadID,
		Prompt:              "hello staged project",
		ClientUserMessageID: "msg-tui-staged",
	}))
	if composer.Error != nil {
		t.Fatalf("composer turn start error: %+v", composer.Error)
	}
	request := waitForRuntimeAgentRequest(t, agent)
	var metadata map[string]any
	if err := json.Unmarshal([]byte(request.ClientMetadata["x-codex-turn-metadata"]), &metadata); err != nil {
		t.Fatalf("turn metadata json error = %v", err)
	}
	if metadata["workspace_kind"] != "project" {
		t.Fatalf("workspace_kind = %#v, want project", metadata["workspace_kind"])
	}
	waitForNotificationMethod(t, sink, NotificationTurnCompleted)
}
