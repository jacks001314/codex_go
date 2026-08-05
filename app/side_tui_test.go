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
		Personality:     "pragmatic",
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
