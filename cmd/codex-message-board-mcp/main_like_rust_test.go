package main

import (
	"bytes"
	"context"
	"path/filepath"
	"testing"

	"codex_go/agent"
	"codex_go/state"
)

// Rust #58904: caller metadata is validated against persisted agent
// membership, and an agent in a child thread belongs to its session tree.
func TestMessageBoardMCPRegistryMembershipLikeRust(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	db, err := state.OpenSQLite(ctx, "file:"+path)
	if err != nil {
		t.Fatalf("OpenSQLite() error = %v", err)
	}
	for _, statement := range []string{
		`CREATE TABLE threads (id TEXT PRIMARY KEY, agent_path TEXT, updated_at INTEGER)`,
		`CREATE TABLE thread_spawn_edges (parent_thread_id TEXT NOT NULL, child_thread_id TEXT PRIMARY KEY)`,
		`INSERT INTO threads (id, agent_path, updated_at) VALUES ('session-1', '/root', 1)`,
		`INSERT INTO threads (id, agent_path, updated_at) VALUES ('child-1', '/root/worker', 2)`,
		`INSERT INTO thread_spawn_edges (parent_thread_id, child_thread_id) VALUES ('session-1', 'child-1')`,
	} {
		if _, err := db.ExecContext(ctx, statement); err != nil {
			t.Fatalf("exec %q: %v", statement, err)
		}
	}
	db.Close()

	registry, err := openMessageBoardMCPRegistry(ctx, path)
	if err != nil {
		t.Fatalf("openMessageBoardMCPRegistry() error = %v", err)
	}
	defer registry.Close()
	for _, testCase := range []struct {
		threadID  string
		sessionID string
		want      bool
	}{
		{threadID: "session-1", sessionID: "session-1", want: true},
		{threadID: "child-1", sessionID: "session-1", want: true},
		{threadID: "child-1", sessionID: "child-1", want: true},
		{threadID: "child-1", sessionID: "other-session", want: false},
		{threadID: "unknown", sessionID: "session-1", want: false},
	} {
		got, err := registry.Member(ctx, testCase.threadID, testCase.sessionID)
		if err != nil {
			t.Fatalf("Member(%q, %q) error = %v", testCase.threadID, testCase.sessionID, err)
		}
		if got != testCase.want {
			t.Fatalf("Member(%q, %q) = %v, want %v", testCase.threadID, testCase.sessionID, got, testCase.want)
		}
	}
	pathValue, ok, err := registry.AgentPath(ctx, "child-1")
	if err != nil || !ok || pathValue != agent.AgentPath("/root/worker") {
		t.Fatalf("AgentPath(child-1) = %q, %v, %v", pathValue, ok, err)
	}
	resolved, ok, err := registry.ResolveAgent(ctx, agent.AgentPath("/root/worker"))
	if err != nil || !ok || resolved != "child-1" {
		t.Fatalf("ResolveAgent(/root/worker) = %q, %v, %v", resolved, ok, err)
	}
}

// Rust #58904: startup without an existing registry still serves, but rejects
// every caller.
func TestMessageBoardMCPRegistryMissingDatabaseLikeRust(t *testing.T) {
	ctx := context.Background()
	registry, err := openMessageBoardMCPRegistry(ctx, filepath.Join(t.TempDir(), "missing.db"))
	if err != nil {
		t.Fatalf("openMessageBoardMCPRegistry() error = %v", err)
	}
	defer registry.Close()
	if member, err := registry.Member(ctx, "thread", "thread"); err != nil || member {
		t.Fatalf("Member() = %v, %v", member, err)
	}
}

func TestMessageBoardMCPRunRequiresTokenLikeRust(t *testing.T) {
	t.Setenv("CODEX_MESSAGE_BOARD_TOKEN", "")
	var stdout, stderr bytes.Buffer
	if code := run(nil, &stdout, &stderr); code != 2 {
		t.Fatalf("run() code = %d, stderr = %s", code, stderr.String())
	}
}
