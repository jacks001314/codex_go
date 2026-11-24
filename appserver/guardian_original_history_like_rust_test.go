package appserver

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/agentboard"
	"codex_go/session"
)

// Rust thread-store original_history: a tail read reports the durable end, an
// after read returns every record past the cursor, and a range read returns
// exactly the captured cutoff while refusing a cutoff the store no longer
// covers.
func TestGuardianOriginalHistoryReadsTailAfterAndRangeLikeRust(t *testing.T) {
	store, root := guardianOriginalHistoryFixture(t)

	tail, err := ReadGuardianOriginalHistory(store, string(root), GuardianOriginalHistoryTail(string(root)))
	if err != nil {
		t.Fatalf("tail read error = %v", err)
	}
	if len(tail.Items) != 0 || tail.End != (agentboard.GuardianHistoryPosition{ThreadID: string(root), EndOrdinalExclusive: 3}) {
		t.Fatalf("tail = %#v, want no items ending at record 3", tail)
	}

	after, err := ReadGuardianOriginalHistory(store, string(root), GuardianOriginalHistoryAfter(agentboard.GuardianHistoryPosition{ThreadID: string(root)}))
	if err != nil {
		t.Fatalf("after read error = %v", err)
	}
	if len(after.Items) != 3 || after.End.EndOrdinalExclusive != 3 {
		t.Fatalf("after = %d items ending at %d, want 3 records", len(after.Items), after.End.EndOrdinalExclusive)
	}
	for index, item := range after.Items {
		if want := uint64(index + 1); item.Position.EndOrdinalExclusive != want {
			t.Fatalf("item %d position = %d, want %d", index, item.Position.EndOrdinalExclusive, want)
		}
	}

	fromSecond, err := ReadGuardianOriginalHistory(store, string(root), GuardianOriginalHistoryAfter(agentboard.GuardianHistoryPosition{ThreadID: string(root), EndOrdinalExclusive: 2}))
	if err != nil {
		t.Fatalf("after read from the second record error = %v", err)
	}
	if len(fromSecond.Items) != 1 || fromSecond.Items[0].Item.ID != "root-3" {
		t.Fatalf("after(2) = %#v, want only the third record", fromSecond.Items)
	}

	cutoff := agentboard.GuardianHistoryPosition{ThreadID: string(root), EndOrdinalExclusive: 2}
	ranged, err := ReadGuardianOriginalHistory(store, string(root), GuardianOriginalHistoryRange(agentboard.GuardianHistoryPosition{ThreadID: string(root)}, cutoff))
	if err != nil {
		t.Fatalf("range read error = %v", err)
	}
	if len(ranged.Items) != 2 || ranged.End != cutoff {
		t.Fatalf("range = %d items ending at %#v, want 2 items ending at the cutoff", len(ranged.Items), ranged.End)
	}

	if _, err := ReadGuardianOriginalHistory(store, string(root), GuardianOriginalHistoryRange(agentboard.GuardianHistoryPosition{ThreadID: string(root)}, agentboard.GuardianHistoryPosition{ThreadID: string(root), EndOrdinalExclusive: 9})); err == nil || !strings.Contains(err.Error(), "cutoff no longer applies") {
		t.Fatalf("future cutoff error = %v, want the cutoff rejection", err)
	}
	if _, err := ReadGuardianOriginalHistory(store, string(root), GuardianOriginalHistoryAfter(agentboard.GuardianHistoryPosition{ThreadID: "other-root"})); err == nil || !strings.Contains(err.Error(), "another root") {
		t.Fatalf("foreign start error = %v, want a root mismatch", err)
	}
}

// A stored record whose history sequence skips a value is corrupt for the
// intake: Rust refuses an ordinal that changed under the cursor.
func TestGuardianOriginalHistoryRejectsSkippedOrdinalLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	root := session.ThreadID("guardian-gap")
	first := guardianOriginalItem("gap-1", "user", "first request", now)
	second := guardianOriginalItem("gap-2", "user", "second request", now)
	session.MergeItemHarnessMetadata(&second, map[string]any{"history_sequence": 5})
	if err := store.Create(&session.Record{
		ID: root, SessionID: string(root), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-5"},
		Items:    []session.Item{first, second},
	}); err != nil {
		t.Fatal(err)
	}
	_, err := ReadGuardianOriginalHistory(store, string(root), GuardianOriginalHistoryAfter(agentboard.GuardianHistoryPosition{ThreadID: string(root)}))
	if err == nil || !strings.Contains(err.Error(), "ordinal changed") {
		t.Fatalf("skipped ordinal error = %v, want the ordinal rejection", err)
	}
}

// A fork or attached thread has no original history of its own.
func TestGuardianOriginalHistoryRequiresRootLikeRust(t *testing.T) {
	store, root := guardianOriginalHistoryFixture(t)
	now := time.Now().UTC()
	child := &session.Record{
		ID: "child", SessionID: "child", ForkedFromID: root, CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-5"},
	}
	if err := store.Create(child); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadGuardianOriginalHistory(store, "child", GuardianOriginalHistoryTail("child")); err == nil || !strings.Contains(err.Error(), "paginated root") {
		t.Fatalf("fork history error = %v, want the root requirement", err)
	}
	attached := &session.Record{
		ID: "attached", SessionID: "attached", AttachedThreadParentID: root, CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-5"},
	}
	if err := store.Create(attached); err != nil {
		t.Fatal(err)
	}
	if _, err := ReadGuardianOriginalHistory(store, "attached", GuardianOriginalHistoryTail("attached")); err == nil || !strings.Contains(err.Error(), "paginated root") {
		t.Fatalf("attached history error = %v, want the root requirement", err)
	}
}

// The intake adapter reports the cursor, the durable end, and the stored
// original payloads of every record in the read.
func TestGuardianStoreEvidenceReaderAdaptsTheThreadStoreLikeRust(t *testing.T) {
	store, root := guardianOriginalHistoryFixture(t)
	reader := GuardianStoreEvidenceReader(store)
	ctx := context.Background()

	start := agentboard.GuardianHistoryPosition{ThreadID: string(root)}
	snapshot, err := reader(ctx, start, nil)
	if err != nil {
		t.Fatalf("reader error = %v", err)
	}
	if snapshot.Start != start || snapshot.End.EndOrdinalExclusive != 3 {
		t.Fatalf("snapshot = start %#v end %#v", snapshot.Start, snapshot.End)
	}
	if len(snapshot.Sources) != 3 {
		t.Fatalf("sources = %d, want 3", len(snapshot.Sources))
	}
	encoded, err := snapshot.EncodedSources()
	if err != nil {
		t.Fatalf("EncodedSources() error = %v", err)
	}
	if !strings.Contains(encoded[0], "first request") || !strings.Contains(encoded[2], "third request") {
		t.Fatalf("encoded sources = %#v", encoded)
	}

	cutoff := agentboard.GuardianHistoryPosition{ThreadID: string(root), EndOrdinalExclusive: 1}
	ranged, err := reader(ctx, start, &cutoff)
	if err != nil {
		t.Fatalf("range reader error = %v", err)
	}
	if len(ranged.Sources) != 1 || ranged.End != cutoff {
		t.Fatalf("range snapshot = %d sources ending at %#v, want the single captured cutoff", len(ranged.Sources), ranged.End)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := reader(cancelled, start, nil); err == nil {
		t.Fatal("cancelled read returned evidence")
	}
}

// guardianOriginalHistoryFixture creates a root thread with three committed
// records carrying stored response payloads.
func guardianOriginalHistoryFixture(t *testing.T) (*session.Store, session.ThreadID) {
	t.Helper()
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	root := session.ThreadID("guardian-root")
	items := []session.Item{
		guardianOriginalItem("root-1", "user", "first request", now),
		guardianOriginalItem("root-2", "assistant", "first answer", now),
		guardianOriginalItem("root-3", "user", "third request", now),
	}
	if err := store.Create(&session.Record{
		ID: root, SessionID: string(root), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{CWD: t.TempDir(), Model: "gpt-5"},
		Items:    items,
	}); err != nil {
		t.Fatalf("Create(root) error = %v", err)
	}
	return store, root
}

func guardianOriginalItem(id string, role string, text string, now time.Time) session.Item {
	payload, _ := json.Marshal(map[string]any{
		"type": "message",
		"role": role,
		"content": []map[string]any{
			{"type": "input_text", "text": text},
		},
	})
	return session.Item{ID: id, Type: "message", Role: role, Text: text, Raw: payload, CreatedAt: now}
}
