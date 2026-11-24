package app

import (
	"path/filepath"
	"testing"
	"time"

	"codex_go/session"
	codextea "codex_go/tui/tea"
)

// Rust #59767: a side conversation forks the parent's named permission profile
// and keeps it after the parent changes selection. The profile replaces the
// sandbox override because thread/fork rejects combining the two.
func TestInteractiveLocalSideCoordinatorInheritsPermissionProfileLikeRust(t *testing.T) {
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
			CWD:                     `D:\repo`,
			Model:                   "gpt-parent",
			ActivePermissionProfile: "parent-profile",
		},
		Items: []session.Item{
			{ID: "item-user", Type: "message", Role: "user", Text: "parent question", CreatedAt: now},
		},
	}
	if err := store.Create(parent); err != nil {
		t.Fatalf("Create(parent) error = %v", err)
	}
	coordinator := newInteractiveLocalSideCoordinator(store)
	response, err := coordinator.Start(codextea.SideStartParams{
		ParentThreadID:    "thread-parent",
		CWD:               `D:\side`,
		ApprovalPolicy:    "on-request",
		Sandbox:           "workspace-write",
		PermissionProfile: "side-reader",
	})
	if err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	side, err := store.Read(session.ThreadID(response.SideThreadID), true, true)
	if err != nil {
		t.Fatalf("Read(side) error = %v", err)
	}
	if side.Metadata.ActivePermissionProfile != "side-reader" {
		t.Fatalf("side active permission profile = %q, want side-reader", side.Metadata.ActivePermissionProfile)
	}
	if permissions, _ := side.Metadata.Extra["permissions"].(string); permissions != "side-reader" {
		t.Fatalf("side permissions snapshot = %#v, want side-reader", side.Metadata.Extra["permissions"])
	}

	// The side owns the profile it started with; selecting another one on the
	// parent does not rewrite the child.
	other := "full-access"
	if _, err := store.UpdateMetadata("thread-parent", &session.MetadataPatch{ActivePermissionProfile: &other}, true); err != nil {
		t.Fatalf("UpdateMetadata(parent) error = %v", err)
	}
	reloaded, err := store.Read(session.ThreadID(response.SideThreadID), true, true)
	if err != nil {
		t.Fatalf("Read(side again) error = %v", err)
	}
	if reloaded.Metadata.ActivePermissionProfile != "side-reader" {
		t.Fatalf("side profile after parent change = %q, want side-reader", reloaded.Metadata.ActivePermissionProfile)
	}
}
