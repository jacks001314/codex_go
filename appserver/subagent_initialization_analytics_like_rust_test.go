package appserver

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/config"
	"codex_go/session"
	"codex_go/telemetry"
	"codex_go/turn"
)

// Mirrors Rust #51206: a thread-spawn subagent reports one initialization
// event when created and another when resumed, inheriting the parent client's
// metadata and retaining its original creation timestamp on resume.
func TestRuntimeAgentControllerEmitsSubagentInitializationAnalyticsLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := time.Now().UTC().Truncate(time.Second)
	parent := &session.Record{
		ID:        "parent-initialization-analytics",
		SessionID: "parent-initialization-analytics",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:           t.TempDir(),
			Model:         "gpt-5.4",
			ModelProvider: "openai",
			Originator:    "codex-tui",
		},
	}
	if err := store.Create(parent); err != nil {
		t.Fatal(err)
	}

	sink := newRecordingTurnEventSink()
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter:          NewRouter(store),
		Config:                config.NewConfigService(home),
		Analytics:             sink,
		AnalyticsRPCTransport: telemetry.AppServerRPCTransportStdio,
		DefaultCWD:            parent.Metadata.CWD,
	})
	connectionID := "conn-parent-initialization-analytics"
	if !router.rememberConnectionClientInfo(connectionID, ClientInfo{Name: "codex-tui", Version: "1.2.3"}) {
		t.Fatal("failed to remember parent connection client info")
	}
	if err := router.threads.RegisterTurn(string(parent.ID), "parent-turn", func() {}, now.UnixMilli(), &turn.TurnStartParams{ThreadID: string(parent.ID)}); err != nil {
		t.Fatal(err)
	}
	router.threads.UpdateTurn(string(parent.ID), "parent-turn", func(active *activeRuntimeTurn) {
		active.ConnectionID = connectionID
	})

	controller := newRuntimeAgentController(router, string(parent.ID), parent.Metadata.CWD, 4)
	spawned, err := controller.SpawnAgent(context.Background(), &agent.SpawnAgentArgs{})
	if err != nil {
		t.Fatal(err)
	}
	record, err := store.Read(session.ThreadID(spawned.AgentID), false, false)
	if err != nil {
		t.Fatal(err)
	}

	created := waitForCodexThreadInitializedEvent(t, sink, spawned.AgentID)
	assertSubagentInitializationEvent(t, created, record, "new")

	if _, err := controller.CloseAgent(context.Background(), &agent.CloseAgentArgs{Target: spawned.AgentID}); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.ResumeAgent(context.Background(), &agent.ResumeAgentArgs{ID: spawned.AgentID}); err != nil {
		t.Fatal(err)
	}
	resumed := waitForCodexThreadInitializedEvent(t, sink, spawned.AgentID)
	assertSubagentInitializationEvent(t, resumed, record, "resumed")
}

func assertSubagentInitializationEvent(t *testing.T, event telemetry.CodexThreadInitializedEventRequest, record *session.Record, wantMode string) {
	t.Helper()
	params := event.EventParams
	if params.ThreadID != string(record.ID) || params.SessionID != record.SessionID || params.InitializationMode != wantMode {
		t.Fatalf("subagent initialization ids/mode = %#v", params)
	}
	if params.SubagentSource == nil || *params.SubagentSource != "thread_spawn" {
		t.Fatalf("subagent_source = %#v", params.SubagentSource)
	}
	if params.ParentThreadID == nil || *params.ParentThreadID != string(record.ParentThreadID) {
		t.Fatalf("parent_thread_id = %#v, want %q", params.ParentThreadID, record.ParentThreadID)
	}
	if params.CreatedAt != uint64(record.CreatedAt.Unix()) {
		t.Fatalf("created_at = %d, want %d", params.CreatedAt, record.CreatedAt.Unix())
	}
	if params.Model != record.Metadata.Model {
		t.Fatalf("model = %q, want %q", params.Model, record.Metadata.Model)
	}
	if params.AppServerClient.ProductClientID != "codex-tui" ||
		params.AppServerClient.ClientName == nil || *params.AppServerClient.ClientName != "codex-tui" ||
		params.AppServerClient.ClientVersion == nil || *params.AppServerClient.ClientVersion != "1.2.3" ||
		params.AppServerClient.RPCTransport != telemetry.AppServerRPCTransportInProcess ||
		params.AppServerClient.ExperimentalAPIEnable != nil {
		t.Fatalf("app_server_client = %#v", params.AppServerClient)
	}
}
