package rollout

import (
	"path/filepath"
	"testing"
)

func TestMigrateRolloutsProgressMatchesRust(t *testing.T) {
	// Mirrors Rust LocalThreadStore::migrate_rollouts_with_progress: one update
	// per discovered path, newest first inside each root, the active tree before
	// the archived one, and a nil status for paths the thread selection prunes.
	home := t.TempDir()
	olderID := "123e4567-e89b-42d3-a456-426614174010"
	newerID := "123e4567-e89b-42d3-a456-426614174011"
	archivedID := "123e4567-e89b-42d3-a456-426614174012"
	paginatedID := "123e4567-e89b-42d3-a456-426614174013"

	writeTestRollout(t, filepath.Join(home, SessionsSubdir, "rollout-2025-01-01T00-00-00-"+olderID+".jsonl"), olderID, "")
	writeTestRollout(t, filepath.Join(home, SessionsSubdir, "rollout-2025-01-02T00-00-00-"+paginatedID+".jsonl"), paginatedID, "paginated")
	writeTestRollout(t, filepath.Join(home, SessionsSubdir, "rollout-2025-01-03T00-00-00-"+newerID+".jsonl"), newerID, "")
	writeTestRollout(t, filepath.Join(home, ArchivedSessionsSubdir, "rollout-2025-01-04T00-00-00-"+archivedID+".jsonl"), archivedID, "")

	var updates []MigrationProgress
	report, err := MigrateRollouts(home, MigrationOptions{Progress: func(update MigrationProgress) {
		updates = append(updates, update)
	}})
	if err != nil {
		t.Fatalf("MigrateRollouts: %v", err)
	}
	if len(updates) != 4 {
		t.Fatalf("progress updates = %d, want 4: %#v", len(updates), updates)
	}
	wantIDs := []string{newerID, paginatedID, olderID, archivedID}
	for index, update := range updates {
		if update.ProcessedPaths != index+1 || update.TotalPaths != 4 {
			t.Fatalf("update %d = %#v, want %d/4", index, update, index+1)
		}
		if update.OutcomeStatus == nil {
			t.Fatalf("update %d has no status: %#v", index, update)
		}
		if len(report.Outcomes) <= index || report.Outcomes[index].ThreadID == nil || *report.Outcomes[index].ThreadID != wantIDs[index] {
			t.Fatalf("outcome order %d = %#v, want thread %s", index, report.Outcomes, wantIDs[index])
		}
	}
	if status := *updates[1].OutcomeStatus; status != MigrationStatusAlreadyPaginated {
		t.Fatalf("paginated update status = %q", status)
	}

	updates = nil
	report, err = MigrateRollouts(home, MigrationOptions{
		ThreadIDs: []string{olderID},
		Progress: func(update MigrationProgress) {
			updates = append(updates, update)
		},
	})
	if err != nil {
		t.Fatalf("MigrateRollouts with selection: %v", err)
	}
	if len(updates) != 4 {
		t.Fatalf("selected progress updates = %d, want 4: %#v", len(updates), updates)
	}
	withStatus := 0
	for index, update := range updates {
		if index != 2 && update.OutcomeStatus != nil {
			t.Fatalf("update %d kept an unselected status: %#v", index, update)
		}
		if update.OutcomeStatus != nil {
			withStatus++
		}
	}
	if withStatus != 1 {
		t.Fatalf("updates with status = %d, want 1", withStatus)
	}
	if len(report.Outcomes) != 1 || report.Outcomes[0].ThreadID == nil || *report.Outcomes[0].ThreadID != olderID {
		t.Fatalf("selected outcomes = %#v", report.Outcomes)
	}
}
