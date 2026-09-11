package appserver

import (
	"strings"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/session"
	"codex_go/turn"
)

func newEnvironmentSubagentTestRouter(t *testing.T, multiAgentVersion string) (*RuntimeRouter, *session.Store) {
	t.Helper()
	cwd := t.TempDir()
	store := session.NewStore(t.TempDir())
	now := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	records := []*session.Record{
		{ID: "root", SessionID: "root", CreatedAt: now, UpdatedAt: now, Metadata: session.Metadata{
			CWD: cwd, AgentPath: "/root", MultiAgentVersion: multiAgentVersion,
		}},
		{ID: "child-a", SessionID: "child-a", ParentThreadID: "root", CreatedAt: now, UpdatedAt: now, Metadata: session.Metadata{
			CWD: cwd, AgentPath: "/root/alpha", AgentNickname: "Alpha",
		}},
		{ID: "child-b", SessionID: "child-b", ParentThreadID: "root", CreatedAt: now, UpdatedAt: now, Metadata: session.Metadata{
			CWD: cwd, AgentPath: "/root/beta",
		}},
		{ID: "grand", SessionID: "grand", ParentThreadID: "child-a", CreatedAt: now, UpdatedAt: now, Metadata: session.Metadata{
			CWD: cwd, AgentPath: "/root/alpha/grand",
		}},
	}
	for _, record := range records {
		if err := store.Save(record); err != nil {
			t.Fatal(err)
		}
	}
	graph := agent.NewMemoryStore()
	for _, edge := range [][2]string{{"root", "child-a"}, {"root", "child-b"}, {"child-a", "grand"}} {
		if err := graph.UpsertThreadSpawnEdge(edge[0], edge[1], agent.ThreadSpawnEdgeOpen); err != nil {
			t.Fatal(err)
		}
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		SpawnGraph:   graph,
		Environment:  NewEnvironmentManager(EnvironmentShellInfo{Name: "sh"}, cwd),
		DefaultCWD:   cwd,
	})
	return router, store
}

// Mirrors Rust #43491: the multi-agent v2 roster lists registered direct
// children (including unloaded ones), excludes grandchildren, and renders the
// full agent path.
func TestTurnEnvironmentContextRendersMultiAgentV2SubagentsLikeRust(t *testing.T) {
	router, _ := newEnvironmentSubagentTestRouter(t, string(agent.VersionV2))
	now := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	text := router.turnEnvironmentContextTextAt(&turn.TurnStartParams{ThreadID: "root"}, now, "UTC", "")
	want := "  <subagents>\n    <agent name=\"/root/alpha\" />\n    <agent name=\"/root/beta\" />\n  </subagents>\n"
	if !strings.Contains(text, want) {
		t.Fatalf("environment context missing v2 subagents roster: %q", text)
	}
	if strings.Contains(text, "grand") {
		t.Fatalf("environment context must exclude grandchildren: %q", text)
	}

	// A loaded child is listed before an alphabetically earlier unloaded sibling.
	router.threads.liveThreads[session.ThreadID("child-b")] = &managedLiveThread{}
	text = router.turnEnvironmentContextTextAt(&turn.TurnStartParams{ThreadID: "root"}, now, "UTC", "")
	want = "  <subagents>\n    <agent name=\"/root/beta\" />\n    <agent name=\"/root/alpha\" />\n  </subagents>\n"
	if !strings.Contains(text, want) {
		t.Fatalf("environment context did not prioritize loaded child: %q", text)
	}
}

// Mirrors Rust's non-v2 behavior: only loaded children are listed, using the
// short agent path name and nickname.
func TestTurnEnvironmentContextRendersMultiAgentV1SubagentsLikeRust(t *testing.T) {
	router, _ := newEnvironmentSubagentTestRouter(t, "")
	now := time.Date(2026, 8, 1, 8, 0, 0, 0, time.UTC)
	router.threads.liveThreads[session.ThreadID("child-a")] = &managedLiveThread{}
	text := router.turnEnvironmentContextTextAt(&turn.TurnStartParams{ThreadID: "root"}, now, "UTC", "")
	want := "  <subagents>\n    - alpha: Alpha\n  </subagents>\n"
	if !strings.Contains(text, want) {
		t.Fatalf("environment context missing v1 subagents roster: %q", text)
	}
	if strings.Contains(text, "beta") {
		t.Fatalf("environment context must omit unloaded children for v1: %q", text)
	}
}
