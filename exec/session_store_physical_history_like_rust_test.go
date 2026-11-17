package exec

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/rollout"
	"codex_go/session"
)

// Rust materializes a paginated fork's inherited prefix from the parent
// rollout (RolloutReference), and the app server installs that resolver when it
// builds its store. `codex exec` used to open a bare store, so resuming a
// paginated fork failed with "physical history resolver is not configured" and
// dropped the inherited conversation.
func TestRunnerSessionStoreResolvesPaginatedHistoryLikeRust(t *testing.T) {
	home := t.TempDir()
	parentID := "0192f0aa-0000-7000-8000-0000000000a1"
	position := writeExecPaginatedRolloutForTest(t, home, parentID)
	childID := session.ThreadID("0192f0aa-0000-7000-8000-0000000000a2")

	store := (&Runner{CodexHome: home}).sessionStore()
	if err := store.Save(&session.Record{
		ID: childID,
		HistoryBase: &session.HistoryPosition{
			ThreadID:            session.ThreadID(parentID),
			EndOrdinalExclusive: position.EndOrdinalExclusive,
			EndByteOffset:       position.EndByteOffset,
			ItemEnd:             1,
			TurnEnd:             1,
		},
		Items: []session.Item{{ID: "child-1", Type: "message", Role: "user"}},
	}); err != nil {
		t.Fatalf("Save(child) error = %v", err)
	}

	bare := session.NewStore(filepath.Join(home, "sessions"))
	if _, err := bare.Read(childID, true, true); err == nil || !strings.Contains(err.Error(), "physical history resolver is not configured") {
		t.Fatalf("bare store Read() error = %v, want resolver-not-configured", err)
	}

	record, err := store.Read(childID, true, true)
	if err != nil {
		t.Fatalf("Read(child) error = %v", err)
	}
	if record.InheritedItems == 0 || len(record.Items) != record.InheritedItems+1 {
		t.Fatalf("inherited = %d, items = %d; want the parent prefix plus one local item", record.InheritedItems, len(record.Items))
	}
	if len(record.Items[0].Content) == 0 || record.Items[0].Content[0].Text != "inherited" {
		t.Fatalf("inherited item = %#v, want the parent rollout message", record.Items[0])
	}
	if record.Items[len(record.Items)-1].ID != "child-1" {
		t.Fatalf("local item = %#v, want the fork's own message last", record.Items[len(record.Items)-1])
	}
}

// writeExecPaginatedRolloutForTest writes one pageable parent rollout and
// returns the position covering everything it wrote.
func writeExecPaginatedRolloutForTest(t *testing.T, home string, threadID string) rollout.HistoryPosition {
	t.Helper()
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:     home,
		ThreadID:      threadID,
		Source:        "cli",
		CWD:           "/repo",
		ModelProvider: "openai",
		HistoryMode:   "paginated",
		Now:           time.Date(2026, 11, 18, 12, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NewRecorder(%s) error = %v", threadID, err)
	}
	if err := recorder.AppendItem(rollout.Item{ID: "parent-1", Type: "user_message", Data: map[string]any{"text": "inherited", "turn_id": "turn-1"}}); err != nil {
		t.Fatalf("AppendItem() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	path, err := rollout.FindThreadPath(home, threadID, false)
	if err != nil {
		t.Fatalf("FindThreadPath(%s) error = %v", threadID, err)
	}
	steps, _, err := rollout.ReadProjectionSteps(path, 0, 0)
	if err != nil {
		t.Fatalf("ReadProjectionSteps(%s) error = %v", path, err)
	}
	if len(steps) == 0 {
		t.Fatalf("ReadProjectionSteps(%s) returned no steps", path)
	}
	last := steps[len(steps)-1]
	return rollout.HistoryPosition{
		ThreadID:            threadID,
		EndOrdinalExclusive: last.Ordinal + 1,
		EndByteOffset:       last.EndByteOffset,
	}
}
