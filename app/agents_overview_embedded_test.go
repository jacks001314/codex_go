package app

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"codex_go/session"
	agentsoverview "codex_go/tui/agents_overview"
	codextea "codex_go/tui/tea"
)

func newEmbeddedDashboardTestStore(t *testing.T) *session.Store {
	t.Helper()
	return session.NewStore(filepath.Join(t.TempDir(), "sessions"))
}

func createEmbeddedDashboardTestThread(t *testing.T, store *session.Store, id, parent string, archived bool) {
	t.Helper()
	now := time.Now().UTC()
	record := &session.Record{
		ID:             session.ThreadID(id),
		SessionID:      id,
		ParentThreadID: session.ThreadID(parent),
		Archived:       archived,
		CreatedAt:      now,
		UpdatedAt:      now,
		RecencyAt:      now,
		Metadata:       session.Metadata{CWD: t.TempDir()},
		Items: []session.Item{
			{ID: "item-" + id, Type: "message", Role: "user", Text: "parent question", CreatedAt: now},
		},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create(%s) error = %v", id, err)
	}
}

func dashboardRowsContain(rows []agentsoverview.Row, threadID string) bool {
	for _, row := range rows {
		if row.ThreadID == threadID {
			return true
		}
	}
	return false
}

func dashboardRowsAll(rows []agentsoverview.Row) []string {
	ids := make([]string, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ThreadID)
	}
	return ids
}

// Rust #58238: embedded sessions run the command center against their own app
// server, so listing, starting, renaming, archiving, and deleting work without
// a shared background server.
func TestEmbeddedAgentsDashboardSourceLifecycleLikeRust(t *testing.T) {
	ctx := context.Background()
	store := newEmbeddedDashboardTestStore(t)
	source := newEmbeddedAgentsDashboardSource(store, nil)

	startedThreadID, err := source.NewSession(ctx, t.TempDir())
	if err != nil {
		t.Fatalf("NewSession() error = %v", err)
	}
	if startedThreadID == "" {
		t.Fatal("NewSession() returned no thread id")
	}

	rows, err := source.List(ctx)
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if !dashboardRowsContain(rows, startedThreadID) {
		t.Fatalf("List() = %v, want the new session %s", dashboardRowsAll(rows), startedThreadID)
	}

	if err := source.Rename(ctx, startedThreadID, "renamed task"); err != nil {
		t.Fatalf("Rename() error = %v", err)
	}
	record, err := store.Read(session.ThreadID(startedThreadID), true, false)
	if err != nil {
		t.Fatalf("Read(renamed) error = %v", err)
	}
	if record.Title != "renamed task" {
		t.Fatalf("renamed title = %q, want %q", record.Title, "renamed task")
	}

	if err := source.Archive(ctx, startedThreadID); err != nil {
		t.Fatalf("Archive() error = %v", err)
	}
	rows, err = source.List(ctx)
	if err != nil {
		t.Fatalf("List() after archive error = %v", err)
	}
	if dashboardRowsContain(rows, startedThreadID) {
		t.Fatalf("archived session %s is still listed", startedThreadID)
	}

	if err := source.Delete(ctx, startedThreadID); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	if _, err := store.Read(session.ThreadID(startedThreadID), true, false); !errors.Is(err, session.ErrThreadNotFound) {
		t.Fatalf("deleted session still exists: %v", err)
	}
}

// Stopping an idle task has nothing to interrupt (Rust's
// stop_agents_overview_thread only issues turn/interrupt for an in-progress
// turn), and an empty thread id is rejected before any RPC.
func TestEmbeddedAgentsDashboardSourceStopIdleThreadLikeRust(t *testing.T) {
	store := newEmbeddedDashboardTestStore(t)
	createEmbeddedDashboardTestThread(t, store, "thread-idle", "", false)
	source := newEmbeddedAgentsDashboardSource(store, nil)
	if err := source.Stop(context.Background(), "thread-idle"); err != nil {
		t.Fatalf("Stop(idle) error = %v", err)
	}
	if err := source.Stop(context.Background(), "  "); err == nil {
		t.Fatal("Stop() accepted an empty thread id")
	}
}

// knownLocalThreadSubtree walks the persisted parent graph so lifecycle actions
// cover every descendant the dashboard folds under the root (Rust #58237).
func TestKnownLocalThreadSubtreeCoversDescendantsLikeRust(t *testing.T) {
	store := newEmbeddedDashboardTestStore(t)
	createEmbeddedDashboardTestThread(t, store, "thread-root", "", false)
	createEmbeddedDashboardTestThread(t, store, "thread-child", "thread-root", false)
	createEmbeddedDashboardTestThread(t, store, "thread-grandchild", "thread-child", true)
	createEmbeddedDashboardTestThread(t, store, "thread-other", "", false)

	subtree := knownLocalThreadSubtree(store, "thread-root")
	for _, want := range []string{"thread-root", "thread-child", "thread-grandchild"} {
		if _, ok := subtree[want]; !ok {
			t.Fatalf("subtree %v is missing %s", subtree, want)
		}
	}
	if _, ok := subtree["thread-other"]; ok {
		t.Fatalf("subtree %v includes an unrelated thread", subtree)
	}
	if got := knownLocalThreadSubtree(store, "thread-other"); len(got) != 1 {
		t.Fatalf("unrelated subtree = %v, want only the root", got)
	}
}

// Rust #58237: archiving or deleting one task closes only the side
// conversations under its subtree; other tasks keep theirs.
func TestEmbeddedAgentsDashboardArchiveKeepsUnrelatedSidesLikeRust(t *testing.T) {
	ctx := context.Background()
	store := newEmbeddedDashboardTestStore(t)
	createEmbeddedDashboardTestThread(t, store, "thread-a", "", false)
	createEmbeddedDashboardTestThread(t, store, "thread-b", "", false)
	coordinator := newInteractiveLocalSideCoordinator(store)
	sideA, err := coordinator.Start(codextea.SideStartParams{ParentThreadID: "thread-a"})
	if err != nil {
		t.Fatalf("Start(side for a) error = %v", err)
	}
	sideB, err := coordinator.Start(codextea.SideStartParams{ParentThreadID: "thread-b"})
	if err != nil {
		t.Fatalf("Start(side for b) error = %v", err)
	}

	source := newEmbeddedAgentsDashboardSource(store, coordinator.CloseRelated)
	if err := source.Archive(ctx, "thread-a"); err != nil {
		t.Fatalf("Archive(thread-a) error = %v", err)
	}
	if _, err := store.Read(session.ThreadID(sideA.SideThreadID), true, false); !errors.Is(err, session.ErrThreadNotFound) {
		t.Fatalf("side conversation of the archived task still exists: %v", err)
	}
	if _, err := store.Read(session.ThreadID(sideB.SideThreadID), true, false); err != nil {
		t.Fatalf("unrelated side conversation was closed: %v", err)
	}
	if _, ok := coordinator.Instructions(sideB.SideThreadID); !ok {
		t.Fatal("unrelated side conversation was forgotten")
	}
}

// CloseRelated reports the affected subtree and leaves every other parent's
// side conversations running.
func TestInteractiveLocalSideCoordinatorCloseRelatedScopesParents(t *testing.T) {
	store := newEmbeddedDashboardTestStore(t)
	createEmbeddedDashboardTestThread(t, store, "thread-root", "", false)
	createEmbeddedDashboardTestThread(t, store, "thread-child", "thread-root", false)
	createEmbeddedDashboardTestThread(t, store, "thread-other", "", false)
	coordinator := newInteractiveLocalSideCoordinator(store)
	sideRoot, err := coordinator.Start(codextea.SideStartParams{ParentThreadID: "thread-root"})
	if err != nil {
		t.Fatalf("Start(root side) error = %v", err)
	}
	sideChild, err := coordinator.Start(codextea.SideStartParams{ParentThreadID: "thread-child"})
	if err != nil {
		t.Fatalf("Start(child side) error = %v", err)
	}
	sideOther, err := coordinator.Start(codextea.SideStartParams{ParentThreadID: "thread-other"})
	if err != nil {
		t.Fatalf("Start(other side) error = %v", err)
	}

	if !coordinator.CloseRelated(knownLocalThreadSubtree(store, "thread-root")) {
		t.Fatal("CloseRelated() reported a failure")
	}
	for _, side := range []string{sideRoot.SideThreadID, sideChild.SideThreadID} {
		if _, err := store.Read(session.ThreadID(side), true, false); !errors.Is(err, session.ErrThreadNotFound) {
			t.Fatalf("subtree side %s was not closed: %v", side, err)
		}
	}
	if _, err := store.Read(session.ThreadID(sideOther.SideThreadID), true, false); err != nil {
		t.Fatalf("unrelated side was closed: %v", err)
	}
}
