package appserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/turn"
)

// TestWorldStateFragmentHashMatchesRust pins Rust's
// WorldStateHash::from_fragment: SHA-1 over "codex-world-state-fragment-v1\0"
// plus the length-prefixed role and rendered fragment (cross-checked against an
// independent SHA-1 computation).
func TestWorldStateFragmentHashMatchesRust(t *testing.T) {
	got := sandbox.WorldStateFragmentHash("developer", "<permissions instructions>\nX\n</permissions instructions>")
	const want = "4a38bf09478cfc3500f09673f37ebc8d5b4f79d7"
	if got != want {
		t.Fatalf("WorldStateFragmentHash() = %q, want %q", got, want)
	}
	// CRLF is normalized before hashing.
	crlf := sandbox.WorldStateFragmentHash("developer", "<permissions instructions>\r\nX\r\n</permissions instructions>")
	if crlf != want {
		t.Fatalf("CRLF hash = %q, want %q", crlf, want)
	}
}

// TestPermissionsWorldStateInputItemLikeRust covers the section lifecycle: the
// fragment is emitted and persisted when the section is absent, suppressed when
// the instructions hash is unchanged, and emitted again after a change.
func TestPermissionsWorldStateInputItemLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("thread-permissions-world-state")
	now := time.Now().UTC()
	if err := store.Create(&session.Record{
		ID: threadID, SessionID: string(threadID), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{HistoryMode: string(ThreadHistoryLegacy), Extra: map[string]any{}},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	t.Cleanup(func() { _ = router.Close() })

	params := &turn.TurnStartParams{ThreadID: string(threadID), CWD: home}
	cfg := &config.Config{Values: map[string]any{}}

	item, err := router.permissionsWorldStateInputItem(string(threadID), params, cfg)
	if err != nil {
		t.Fatalf("permissionsWorldStateInputItem() error = %v", err)
	}
	text := permissionsInputItemText(t, item)
	if !strings.Contains(text, sandbox.PermissionInstructionsOpenTag) ||
		!strings.Contains(text, sandbox.PermissionInstructionsCloseTag) {
		t.Fatalf("permissions fragment = %q", text)
	}
	record, err := store.Load(threadID)
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Metadata.WorldState) == 0 {
		t.Fatal("world state snapshot was not persisted")
	}
	state, err := session.DecodeWorldState(record.Metadata.WorldState)
	if err != nil {
		t.Fatalf("DecodeWorldState() error = %v", err)
	}
	var snapshot permissionsWorldStateSnapshot
	if err := json.Unmarshal(state.PermissionInstructions, &snapshot); err != nil {
		t.Fatalf("snapshot = %s: %v", state.PermissionInstructions, err)
	}
	if len(snapshot.Instructions) != 40 {
		t.Fatalf("snapshot instructions hash = %q, want a SHA-1 hex digest", snapshot.Instructions)
	}

	// Unchanged instructions must not emit again.
	repeat, err := router.permissionsWorldStateInputItem(string(threadID), params, cfg)
	if err != nil {
		t.Fatalf("permissionsWorldStateInputItem() error = %v", err)
	}
	if repeat != nil {
		t.Fatalf("unchanged section re-emitted: %#v", repeat)
	}

	// A changed approval policy changes the hash, so the fragment is emitted.
	changed := &config.Config{Values: map[string]any{"approval_policy": "never"}}
	changedItem, err := router.permissionsWorldStateInputItem(string(threadID), params, changed)
	if err != nil {
		t.Fatalf("permissionsWorldStateInputItem() error = %v", err)
	}
	if changedItem == nil {
		t.Fatal("changed instructions were not re-emitted")
	}
	if !strings.Contains(permissionsInputItemText(t, changedItem), "Approval policy is currently never") {
		t.Fatalf("changed fragment = %q", permissionsInputItemText(t, changedItem))
	}

	// The session item keeps the fragment in the thread history.
	persisted, ok := permissionsWorldStateSessionItemForTurn("turn-1", changedItem, now)
	if !ok {
		t.Fatalf("fragment was not persistable: %#v", changedItem)
	}
	if persisted.Data["kind"] != permissionsInstructionsKind || persisted.Role != "developer" {
		t.Fatalf("session item = %#v", persisted)
	}
}

// TestPermissionsWorldStateHonorsIncludeFlagLikeRust pins that the
// include_permissions_instructions gate suppresses the section.
func TestPermissionsWorldStateHonorsIncludeFlagLikeRust(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(t.TempDir())
	threadID := session.ThreadID("thread-permissions-gate")
	now := time.Now().UTC()
	if err := store.Create(&session.Record{
		ID: threadID, SessionID: string(threadID), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{HistoryMode: string(ThreadHistoryLegacy), Extra: map[string]any{}},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Config:       config.NewConfigService(home),
	})
	t.Cleanup(func() { _ = router.Close() })

	params := &turn.TurnStartParams{ThreadID: string(threadID), CWD: home}
	cfg := &config.Config{Values: map[string]any{"include_permissions_instructions": false}}
	item, err := router.permissionsWorldStateInputItem(string(threadID), params, cfg)
	if err != nil {
		t.Fatalf("permissionsWorldStateInputItem() error = %v", err)
	}
	if item != nil {
		t.Fatalf("item = %#v, want nil when the section is disabled", item)
	}
	record, err := store.Load(threadID)
	if err != nil {
		t.Fatal(err)
	}
	if len(record.Metadata.WorldState) != 0 {
		t.Fatalf("world state persisted while disabled: %s", record.Metadata.WorldState)
	}
}

func permissionsInputItemText(t *testing.T, item any) string {
	t.Helper()
	raw, ok := item.(map[string]any)
	if !ok {
		t.Fatalf("input item = %#v", item)
	}
	return textFromInputItemContent(raw["content"])
}
