package state

import (
	"context"
	"database/sql"
	"testing"
	"time"
)

func newProjectTestRuntime(t *testing.T) *StateRuntime {
	t.Helper()
	home := t.TempDir()
	runtime := newBackfillTestRuntimeAt(t, home)
	t.Cleanup(func() { _ = runtime.Close() })
	return runtime
}

func seedProjectThread(t *testing.T, runtime *StateRuntime, threadID string, archived bool) {
	t.Helper()
	path := writeBackfillRollout(t, runtime.sqlite.Home(), threadID, time.Date(2026, 8, 17, 0, 0, 0, 0, time.UTC), archived)
	if err := runtime.ReconcileRollout(context.Background(), path, archived); err != nil {
		t.Fatal(err)
	}
}

// TestProjectListRecencySortLikeRust verifies the #41223 recency sort: ordering
// by newest non-archived assigned thread, projects with no activity last, and
// null handling in cursors.
func TestProjectListRecencySortLikeRust(t *testing.T) {
	ctx := context.Background()
	runtime := newProjectTestRuntime(t)
	for _, id := range []string{"r-t1", "r-t2", "r-t3"} {
		seedProjectThread(t, runtime, id, false)
	}
	execSetRecency := func(threadID string, recency int64) {
		if _, err := runtime.StateDB().ExecContext(ctx, `UPDATE threads SET recency_at_ms = ? WHERE id = ?`, recency, threadID); err != nil {
			t.Fatal(err)
		}
	}
	execSetRecency("r-t1", 1000)
	execSetRecency("r-t2", 3000)
	execSetRecency("r-t3", 2000)

	alpha, err := runtime.CreateProject(ctx, "alpha", nil, nil, []string{"r-t1"}, "recency-key-alpha")
	if err != nil {
		t.Fatal(err)
	}
	beta, err := runtime.CreateProject(ctx, "beta", nil, nil, []string{"r-t2"}, "recency-key-beta")
	if err != nil {
		t.Fatal(err)
	}
	gamma, err := runtime.CreateProject(ctx, "gamma", nil, nil, []string{"r-t3"}, "recency-key-gamma")
	if err != nil {
		t.Fatal(err)
	}
	empty, err := runtime.CreateProject(ctx, "empty", nil, nil, nil, "recency-key-empty")
	if err != nil {
		t.Fatal(err)
	}

	// Recency descending (default): beta (3000), gamma (2000), alpha (1000),
	// empty last.
	page, err := runtime.ListProjects(ctx, nil, 10, ProjectSortRecencyAt, "desc")
	if err != nil {
		t.Fatal(err)
	}
	wantOrder := []string{beta.Project.ID, gamma.Project.ID, alpha.Project.ID, empty.Project.ID}
	if len(page.Projects) != len(wantOrder) {
		t.Fatalf("recency desc page = %+v", page.Projects)
	}
	for i, want := range wantOrder {
		if page.Projects[i].ID != want {
			t.Fatalf("recency desc order[%d] = %q, want %q", i, page.Projects[i].ID, want)
		}
	}
	// Recency ascending puts empty projects last and the smallest recency first.
	page, err = runtime.ListProjects(ctx, nil, 10, ProjectSortRecencyAt, "asc")
	if err != nil {
		t.Fatal(err)
	}
	wantOrderAsc := []string{alpha.Project.ID, gamma.Project.ID, beta.Project.ID, empty.Project.ID}
	for i, want := range wantOrderAsc {
		if page.Projects[i].ID != want {
			t.Fatalf("recency asc order[%d] = %q, want %q", i, page.Projects[i].ID, want)
		}
	}
	// Position sorting remains unchanged and is independent of recency.
	page, err = runtime.ListProjects(ctx, nil, 10, ProjectSortPosition, "asc")
	if err != nil {
		t.Fatal(err)
	}
	if page.Projects[0].ID != alpha.Project.ID {
		t.Fatalf("position order[0] = %q, want alpha", page.Projects[0].ID)
	}
}

// TestProjectStoreLifecycleMirrorsRustProjectsStore exercises the SQLite
// project store mirroring Rust state/src/runtime/projects.rs (#38940):
// idempotent creation, ordered roots, pagination, updates, reordering, and
// deletion clearing thread assignments.
func TestProjectStoreLifecycleMirrorsRustProjectsStore(t *testing.T) {
	ctx := context.Background()
	runtime := newProjectTestRuntime(t)
	seedProjectThread(t, runtime, "project-thread-1", false)
	seedProjectThread(t, runtime, "project-thread-2", true)

	created, err := runtime.CreateProject(ctx, "alpha", []ProjectRoot{{Path: "/a"}, {Path: "/b"}}, map[string]string{"team": "core"}, []string{"project-thread-1"}, "key-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if !created.Created || created.Project.Name != "alpha" || len(created.Project.Roots) != 2 || created.Project.Position != 0 {
		t.Fatalf("created = %+v", created)
	}

	// Idempotent create resolves the same project.
	again, err := runtime.CreateProject(ctx, "alpha", nil, nil, nil, "key-alpha")
	if err != nil {
		t.Fatal(err)
	}
	if again.Created || again.Project.ID != created.Project.ID {
		t.Fatalf("idempotent create = %+v", again)
	}

	// Thread assignment from create + set_thread_project.
	threadProjectID := projectThreadID(t, runtime, "project-thread-1")
	if threadProjectID == nil || *threadProjectID != created.Project.ID {
		t.Fatalf("thread project assignment = %v", threadProjectID)
	}
	previous, found, err := runtime.SetThreadProject(ctx, "project-thread-2", nil)
	if err != nil || !found || previous != nil {
		t.Fatalf("clear thread project = %v, %v, %v", previous, found, err)
	}
	previous, found, err = runtime.SetThreadProject(ctx, "project-thread-2", &created.Project.ID)
	if err != nil || !found || previous != nil {
		t.Fatalf("assign thread project = %v, %v, %v", previous, found, err)
	}
	if _, found, err := runtime.SetThreadProject(ctx, "missing-thread", &created.Project.ID); err != nil || found {
		t.Fatalf("missing thread = %v, %v", found, err)
	}
	if _, _, err := runtime.SetThreadProject(ctx, "project-thread-1", stringPointer("missing-project")); err == nil {
		t.Fatal("assigning a missing project must fail")
	}

	// Second project appended at position 1.
	second, err := runtime.CreateProject(ctx, "beta", []ProjectRoot{{Path: "/c"}}, nil, nil, "key-beta")
	if err != nil {
		t.Fatal(err)
	}
	if second.Project.Position != 1 {
		t.Fatalf("beta position = %d, want 1", second.Project.Position)
	}

	// Pagination: limit 1 yields alpha + next cursor.
	page, err := runtime.ListProjects(ctx, nil, 1, ProjectSortPosition, "asc")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Projects) != 1 || page.Projects[0].ID != created.Project.ID || page.NextCursor == nil {
		t.Fatalf("first page = %+v", page)
	}
	next, err := runtime.ListProjects(ctx, page.NextCursor, 1, ProjectSortPosition, "asc")
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Projects) != 1 || next.Projects[0].ID != second.Project.ID || next.NextCursor != nil {
		t.Fatalf("second page = %+v", next)
	}

	// Read + update.
	read, err := runtime.GetProject(ctx, created.Project.ID)
	if err != nil || read == nil {
		t.Fatalf("read = %+v, %v", read, err)
	}
	updated, changedPtr, err := runtime.UpdateProject(ctx, created.Project.ID, stringPointer("alpha-renamed"), &[]ProjectRoot{{Path: "/x"}}, &map[string]string{"team": "platform"})
	if err != nil || updated == nil || changedPtr == nil || !*changedPtr {
		t.Fatalf("update = %+v, %v, %v", updated, changedPtr, err)
	}
	if updated.Name != "alpha-renamed" || len(updated.Roots) != 1 || updated.Roots[0].Path != "/x" || updated.Metadata["team"] != "platform" {
		t.Fatalf("updated project = %+v", updated)
	}
	noop, changedPtr, err := runtime.UpdateProject(ctx, created.Project.ID, nil, nil, nil)
	if err != nil || noop == nil || changedPtr == nil || *changedPtr {
		t.Fatalf("no-op update = %+v, %v, %v", noop, changedPtr, err)
	}

	// Move beta before alpha.
	moved, err := runtime.MoveProject(ctx, second.Project.ID, &created.Project.ID)
	if err != nil || moved == nil || !*moved {
		t.Fatalf("move = %v, %v", moved, err)
	}
	page, err = runtime.ListProjects(ctx, nil, 10, ProjectSortPosition, "asc")
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Projects) != 2 || page.Projects[0].ID != second.Project.ID || page.Projects[1].ID != created.Project.ID {
		t.Fatalf("reordered = %+v", page.Projects)
	}
	if _, err := runtime.MoveProject(ctx, second.Project.ID, &second.Project.ID); err == nil {
		t.Fatal("moving before itself must fail")
	}
	if _, err := runtime.MoveProject(ctx, second.Project.ID, stringPointer("missing-project")); err == nil {
		t.Fatal("moving before a missing project must fail")
	}

	// Delete clears assignments without deleting threads.
	active, archived, found, err := runtime.DeleteProject(ctx, created.Project.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !found {
		t.Fatal("delete reported the project as missing")
	}
	if len(active) != 1 || active[0] != "project-thread-1" || len(archived) != 1 || archived[0] != "project-thread-2" {
		t.Fatalf("deleted thread ids = active %v archived %v", active, archived)
	}
	if read, err := runtime.GetProject(ctx, created.Project.ID); err != nil || read != nil {
		t.Fatalf("deleted project still readable: %+v, %v", read, err)
	}
	threadProjectID = projectThreadID(t, runtime, "project-thread-1")
	if threadProjectID != nil {
		t.Fatalf("thread project not cleared: %v", threadProjectID)
	}
	active, archived, found, err = runtime.DeleteProject(ctx, created.Project.ID)
	if err != nil || found || active != nil || archived != nil {
		t.Fatalf("deleting a missing project = %v, %v, %v, %v", active, archived, found, err)
	}
}

// TestProjectCursorValidation mirrors Rust invalid_project_cursor (#38940).
func TestProjectCursorValidation(t *testing.T) {
	if _, err := parseProjectCursor(stringPointer("bad"), ProjectSortPosition, "asc"); err == nil {
		t.Fatal("malformed cursor must fail")
	}
	if _, err := parseProjectCursor(stringPointer("0|not-a-uuid"), ProjectSortPosition, "asc"); err == nil {
		t.Fatal("non-uuid cursor must fail")
	}
	if _, err := parseProjectCursor(stringPointer("00|00000000-0000-0000-0000-000000000000"), ProjectSortPosition, "asc"); err == nil {
		t.Fatal("canonical-form position must be enforced")
	}
	if _, err := parseProjectCursor(stringPointer("-1|00000000-0000-0000-0000-000000000000"), ProjectSortPosition, "asc"); err == nil {
		t.Fatal("negative position must fail")
	}
	if value, err := parseProjectCursor(stringPointer("3|00000000-0000-0000-0000-000000000000"), ProjectSortPosition, "asc"); err != nil || value.value == nil || *value.value != 3 || value.id != "00000000-0000-0000-0000-000000000000" {
		t.Fatalf("valid cursor = %+v, %v", value, err)
	}
}

func projectThreadID(t *testing.T, runtime *StateRuntime, threadID string) *string {
	t.Helper()
	var value sql.NullString
	if err := runtime.StateDB().QueryRowContext(context.Background(),
		`SELECT project_id FROM threads WHERE id = ?`, threadID).Scan(&value); err != nil {
		t.Fatalf("read thread project: %v", err)
	}
	if !value.Valid {
		return nil
	}
	return &value.String
}
