package appserver

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
	"codex_go/turn"
)

// TestRuntimeRouterRefreshesGlobalInstructionsAtTurnBoundary covers Rust #44675:
// edits to the global AGENTS.md take effect during an active session, and
// removing the source clears the applied instructions.
func TestRuntimeRouterRefreshesGlobalInstructionsAtTurnBoundary(t *testing.T) {
	codexHome := t.TempDir()
	store := session.NewStore(filepath.Join(codexHome, "sessions"))
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		ThreadExtras: NewThreadExtraService(),
		Turns:        turn.NewTurnService(),
		ThreadStatus: NewThreadStatusManager(),
		Models:       model.NewModelService(nil),
	})
	agentsPath := filepath.Join(codexHome, config.DefaultAgentsMDFilename)
	if err := os.WriteFile(agentsPath, []byte("global v1"), 0o600); err != nil {
		t.Fatalf("write AGENTS.md: %v", err)
	}
	start := router.Handle(requestWithParams(t, IntID(1), MethodThreadStart, ThreadStartParams{}))
	if start.Error != nil {
		t.Fatalf("thread start error: %+v", start.Error)
	}
	threadID := start.Result.(*ThreadStartResponse).Thread.ID
	record, err := router.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil {
		t.Fatalf("thread record: %v", err)
	}
	if record.Metadata.BaseInstructions != "global v1" {
		t.Fatalf("start base instructions = %q, want global v1", record.Metadata.BaseInstructions)
	}
	if !boolFromMap(record.Metadata.Extra, "instructions_from_agents_md") {
		t.Fatalf("AGENTS.md provenance missing from %#v", record.Metadata.Extra)
	}

	// Editing the global file is picked up at the next turn boundary.
	if err := os.WriteFile(agentsPath, []byte("global v2"), 0o600); err != nil {
		t.Fatalf("rewrite AGENTS.md: %v", err)
	}
	params := &turn.TurnStartParams{ThreadID: threadID}
	if err := router.prepareTurnStartParams(params); err != nil {
		t.Fatalf("prepareTurnStartParams: %v", err)
	}
	if params.BaseInstructions == nil || *params.BaseInstructions != "global v2" {
		t.Fatalf("refreshed base instructions = %#v, want global v2", params.BaseInstructions)
	}
	updated, err := router.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || updated == nil {
		t.Fatalf("thread record: %v", err)
	}
	if updated.Metadata.BaseInstructions != "global v2" {
		t.Fatalf("persisted base instructions = %q, want global v2", updated.Metadata.BaseInstructions)
	}

	// An unchanged source does not rewrite the applied instructions.
	beforeSave := updated
	if err := router.prepareTurnStartParams(&turn.TurnStartParams{ThreadID: threadID}); err != nil {
		t.Fatalf("prepareTurnStartParams: %v", err)
	}
	if after, _ := router.threadRecord(session.ThreadID(threadID), true, false); after.Metadata.BaseInstructions != beforeSave.Metadata.BaseInstructions {
		t.Fatalf("unchanged source altered instructions: %q", after.Metadata.BaseInstructions)
	}

	// Removing the source clears the applied instructions so model/config
	// instructions take over.
	if err := os.Remove(agentsPath); err != nil {
		t.Fatalf("remove AGENTS.md: %v", err)
	}
	cleared := &turn.TurnStartParams{ThreadID: threadID}
	if err := router.prepareTurnStartParams(cleared); err != nil {
		t.Fatalf("prepareTurnStartParams: %v", err)
	}
	if cleared.BaseInstructions != nil {
		t.Fatalf("removed source left override %q", *cleared.BaseInstructions)
	}
	if after, _ := router.threadRecord(session.ThreadID(threadID), true, false); after.Metadata.BaseInstructions != "" {
		t.Fatalf("removed source kept %q", after.Metadata.BaseInstructions)
	}
}
