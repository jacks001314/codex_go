package app

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/rollout"
	"codex_go/session"
	codextea "codex_go/tui/tea"
)

func TestInteractiveLocalSideCoordinatorForksInjectsAndDeletes(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := time.Now().UTC()
	parent := &session.Record{
		ID:        "thread-parent",
		SessionID: "thread-parent",
		Preview:   "parent question",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:          `D:\repo`,
			Model:        "gpt-parent",
			HistoryMode:  string(session.ForkAll),
			Instructions: "Keep the parent convention.",
			// Rust #46494: the active session's server-resolved roots are the
			// authoritative selection a side-conversation fork must preserve.
			Extra: map[string]any{"runtime_workspace_roots": []any{"D:/repo", "D:/repo/lib"}},
		},
		Items: []session.Item{
			{ID: "item-user", Type: "message", Role: "user", Text: "parent question", CreatedAt: now, Metadata: map[string]any{"turnId": "turn-parent"}},
			{ID: "item-agent", Type: "message", Role: "assistant", Text: "parent answer", CreatedAt: now.Add(time.Second), Metadata: map[string]any{"turnId": "turn-parent"}},
		},
	}
	if err := store.Create(parent); err != nil {
		t.Fatalf("Create(parent) error = %v", err)
	}

	coordinator := newInteractiveLocalSideCoordinator(store)
	response, err := coordinator.Start(codextea.SideStartParams{
		ParentThreadID:  "thread-parent",
		CWD:             `D:\side`,
		Model:           "gpt-side",
		ReasoningEffort: "high",
		ApprovalPolicy:  "on-request",
		Sandbox:         "workspace-write",
		ServiceTier:     "priority",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if response.ParentThreadID != "thread-parent" || response.SideThreadID == "" || response.SideThreadID == "thread-parent" {
		t.Fatalf("Start() response = %#v", response)
	}

	side, err := store.Read(session.ThreadID(response.SideThreadID), true, true)
	if err != nil {
		t.Fatalf("Read(side) error = %v", err)
	}
	if side.ForkedFromID != "thread-parent" || side.ParentThreadID != "" {
		t.Fatalf("side ancestry = forked:%q parent:%q", side.ForkedFromID, side.ParentThreadID)
	}
	if side.Metadata.CWD != `D:\side` || side.Metadata.Model != "gpt-side" || side.Metadata.ServiceTier != "priority" {
		t.Fatalf("side metadata = %#v", side.Metadata)
	}
	if !strings.Contains(side.Metadata.Instructions, "Keep the parent convention.") || !strings.Contains(side.Metadata.Instructions, codextea.SideDeveloperInstructionText) {
		t.Fatalf("side instructions = %q", side.Metadata.Instructions)
	}
	if ephemeral, _ := side.Metadata.Extra["ephemeral"].(bool); !ephemeral {
		t.Fatalf("side ephemeral metadata = %#v", side.Metadata.Extra)
	}
	if len(side.Items) != 3 || side.Items[2].Role != "user" || side.Items[2].Text != codextea.SideBoundaryPrompt {
		t.Fatalf("side items = %#v", side.Items)
	}
	// The CWD override must not drop the parent's runtime workspace roots
	// (Rust #46494 ForkConfigSource::Session).
	if roots := stringSliceFromAny(side.Metadata.Extra["runtime_workspace_roots"]); len(roots) != 2 {
		t.Fatalf("side runtime workspace roots = %#v, want the parent's two roots", roots)
	}
	if instructions, ok := coordinator.Instructions(response.SideThreadID); !ok || instructions != side.Metadata.Instructions {
		t.Fatalf("Instructions() = %q, %v", instructions, ok)
	}
	if _, err := rollout.FindThreadPath(home, response.SideThreadID, false); err != nil {
		t.Fatalf("side rollout missing: %v", err)
	}

	if _, err := coordinator.Close(codextea.SideCloseParams{ParentThreadID: "thread-parent", SideThreadID: response.SideThreadID}); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if _, err := store.Read(session.ThreadID(response.SideThreadID), true, true); !errors.Is(err, session.ErrThreadNotFound) {
		t.Fatalf("side store record still exists: %v", err)
	}
	if _, err := rollout.FindThreadPath(home, response.SideThreadID, false); err == nil {
		t.Fatal("side rollout still exists after close")
	}
	if _, err := store.Read("thread-parent", true, true); err != nil {
		t.Fatalf("parent removed with side: %v", err)
	}
}

func TestInteractiveLocalSideCoordinatorCloseAll(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := time.Now().UTC()
	if err := store.Create(&session.Record{
		ID: "thread-parent", SessionID: "thread-parent", CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Items: []session.Item{{ID: "item-user", Type: "message", Role: "user", Text: "parent question", CreatedAt: now}},
	}); err != nil {
		t.Fatalf("Create(parent) error = %v", err)
	}
	coordinator := newInteractiveLocalSideCoordinator(store)
	response, err := coordinator.Start(codextea.SideStartParams{ParentThreadID: "thread-parent"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	if err := coordinator.CloseAll(); err != nil {
		t.Fatalf("CloseAll() error = %v", err)
	}
	if _, err := store.Read(session.ThreadID(response.SideThreadID), true, true); !errors.Is(err, session.ErrThreadNotFound) {
		t.Fatalf("CloseAll left side record: %v", err)
	}
}

func TestInteractiveLocalSideCoordinatorForkIncludesLiveParentItems(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := time.Now().UTC()
	parent := &session.Record{
		ID:        "thread-parent",
		SessionID: "thread-parent",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			HistoryMode: "paginated",
			RolloutTurns: []session.TurnSnapshot{
				{ID: "turn-1", Status: "completed"},
				{ID: "turn-2", Status: "running"},
			},
		},
		Items: []session.Item{
			{ID: "user-1", Type: "message", Role: "user", Text: "parent question", CreatedAt: now, Metadata: map[string]any{"turnId": "turn-1"}},
		},
	}
	if err := store.Create(parent); err != nil {
		t.Fatalf("Create(parent) error = %v", err)
	}
	live := session.OpenLiveThreadItems(store, "thread-parent", "turn-2")
	defer live.Close()
	live.Append(
		session.Item{ID: "assistant-2", Type: "agent_message", Role: "assistant", Text: "Inspecting the repo.", CreatedAt: now.Add(time.Second), Metadata: map[string]any{"turnId": "turn-2"}},
		session.Item{ID: "tool-call-2", Type: "function_call", Name: "shell", CallID: "call-2", Text: "git status", CreatedAt: now.Add(2 * time.Second), Metadata: map[string]any{"turnId": "turn-2"}},
		session.Item{ID: "output-2", Type: "tool_output", CallID: "call-2", Text: "clean", CreatedAt: now.Add(3 * time.Second), Metadata: map[string]any{"turnId": "turn-2"}},
	)

	coordinator := newInteractiveLocalSideCoordinator(store)
	response, err := coordinator.Start(codextea.SideStartParams{ParentThreadID: "thread-parent"})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	side, err := store.Read(session.ThreadID(response.SideThreadID), true, true)
	if err != nil {
		t.Fatalf("Read(side) error = %v", err)
	}
	if len(side.Items) != 5 {
		t.Fatalf("side items = %#v, want inherited user, live suffix, and boundary", side.Items)
	}
	if side.Items[0].ID != "user-1" || side.Items[1].ID != "assistant-2" || side.Items[2].CallID != "call-2" || side.Items[3].CallID != "call-2" {
		t.Fatalf("side live item order = %#v", side.Items)
	}
	if side.Items[4].Text != codextea.SideBoundaryPrompt {
		t.Fatalf("side boundary item = %#v", side.Items[4])
	}
}

func TestInteractiveLocalSideCoordinatorRequiresStartedConversation(t *testing.T) {
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := time.Now().UTC()
	rolloutPath := filepath.Join(home, "missing-rollout.jsonl")
	if err := store.Create(&session.Record{
		ID:        "thread-parent",
		SessionID: "thread-parent",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{Extra: map[string]any{"rollout_path": rolloutPath}},
	}); err != nil {
		t.Fatalf("Create(parent) error = %v", err)
	}
	_, err := newInteractiveLocalSideCoordinator(store).Start(codextea.SideStartParams{ParentThreadID: "thread-parent"})
	if err == nil || !strings.Contains(err.Error(), "no rollout found for thread id") {
		t.Fatalf("Start() error = %v", err)
	}
}
