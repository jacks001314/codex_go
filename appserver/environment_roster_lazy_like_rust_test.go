package appserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"codex_go/agent"
	"codex_go/config"
	"codex_go/turn"
)

// Mirrors Rust's lazy host-roster read (core/src/context/world_state/mod.rs
// resolve_host_roster): the world-state diff selects its environment update
// first, and the agent reader is only consulted when that update is actually
// rendered. A turn whose environment context did not change must not read the
// roster at all.
func TestTurnEnvironmentContextReadsHostRosterOnlyWhenRenderingLikeRust(t *testing.T) {
	router, _ := newEnvironmentSubagentTestRouter(t, string(agent.VersionV2))
	reader := &environmentTestAgentReader{children: []AgentRef{
		{ThreadID: "child-b", Path: "/root/beta"},
		{ThreadID: "child-a", Path: "/root/alpha"},
	}}
	router.services.AgentSessionReader = reader
	day := time.Date(2026, 8, 1, 9, 0, 0, 0, time.UTC)
	router.services.ThreadRouter.SetClock(func() time.Time { return day })
	params := &turn.TurnStartParams{ThreadID: "root", CWD: router.services.DefaultCWD}
	cfg := &config.Config{Values: map[string]any{}}

	first, _, err := router.turnEnvironmentContextInputItemForTurn(context.Background(), params.ThreadID, "turn-1", params, cfg)
	if err != nil {
		t.Fatal(err)
	}
	firstText := strings.Join(messageInputTextsForRole(first, "user"), "\n")
	if !strings.Contains(firstText, "<subagents>") || !strings.Contains(firstText, "/root/beta") {
		t.Fatalf("first turn environment context missing roster: %q", firstText)
	}
	if reader.reads != 1 {
		t.Fatalf("first turn roster reads = %d, want 1", reader.reads)
	}

	second, _, err := router.turnEnvironmentContextInputItemForTurn(context.Background(), params.ThreadID, "turn-2", params, cfg)
	if err != nil {
		t.Fatal(err)
	}
	if texts := messageInputTextsForRole(second, "user"); len(texts) != 0 {
		t.Fatalf("unchanged turn rendered %#v, want no environment fragment", texts)
	}
	if reader.reads != 1 {
		t.Fatalf("unchanged turn read the roster again: reads = %d, want 1", reader.reads)
	}

	day = day.Add(24 * time.Hour)
	third, _, err := router.turnEnvironmentContextInputItemForTurn(context.Background(), params.ThreadID, "turn-3", params, cfg)
	if err != nil {
		t.Fatal(err)
	}
	thirdText := strings.Join(messageInputTextsForRole(third, "user"), "\n")
	if !strings.Contains(thirdText, "<subagents>") || !strings.Contains(thirdText, "/root/beta") {
		t.Fatalf("changed turn environment context missing roster: %q", thirdText)
	}
	if reader.reads != 2 {
		t.Fatalf("changed turn roster reads = %d, want 2", reader.reads)
	}
}
