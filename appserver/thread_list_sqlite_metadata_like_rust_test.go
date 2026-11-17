package appserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"codex_go/rollout"
	"codex_go/session"
	"codex_go/state"
)

// writeLegacyRollout records one cheap rollout so the state listing keeps the
// row (Rust's SQLite listing drops rows whose rollout file is missing).
func writeLegacyRollout(t *testing.T, home string, threadID string, source string, now time.Time) string {
	t.Helper()
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, ThreadID: threadID, Source: source, CWD: home,
		ModelProvider: "openai", HistoryMode: "legacy", Now: now,
	})
	if err != nil {
		t.Fatalf("NewRecorder(%s): %v", threadID, err)
	}
	payload, _ := json.Marshal(map[string]any{"type": "user_message", "message": "hello " + threadID})
	if err := recorder.AppendLine(rollout.Line{Type: "event_msg", Timestamp: now.Add(time.Second).Format(time.RFC3339Nano), Payload: payload}); err != nil {
		t.Fatalf("AppendLine(%s): %v", threadID, err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close(%s): %v", threadID, err)
	}
	return recorder.Path()
}

// TestThreadListLegacyRowsUseListedThreadNameLikeRust mirrors #61866: a direct
// SQLite listing keeps each row's own values, and a legacy row falls back to
// the session index exactly like Rust's listed_thread_name.
func TestThreadListLegacyRowsUseListedThreadNameLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	cfg, err := state.NewSqliteConfig(home)
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := state.InitStateRuntime(ctx, cfg, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	router := NewRouter(session.NewStore(home))
	router.SetStateRuntime(runtime)

	const (
		indexedOnlyID  = "0198f002-0000-7000-8000-000000000001"
		derivedTitleID = "0198f002-0000-7000-8000-000000000002"
		paginatedID    = "0198f002-0000-7000-8000-000000000004"
		sectionID      = "0198f002-0000-7000-8000-000000000010"
	)
	now := time.Now().UTC().Add(-time.Hour)
	for index, threadID := range []string{indexedOnlyID, derivedTitleID, paginatedID} {
		path := writeLegacyRollout(t, home, threadID, "cli", now.Add(time.Duration(index)*time.Second))
		if err := runtime.ReconcileRollout(ctx, path, false); err != nil {
			t.Fatalf("ReconcileRollout(%s): %v", threadID, err)
		}
	}
	if _, err := runtime.StateDB().ExecContext(ctx, `INSERT INTO thread_sections (id, name) VALUES (?, 'Work')`, sectionID); err != nil {
		t.Fatal(err)
	}
	// Index-only legacy name: the derived title repeats the first user message.
	if _, err := runtime.StateDB().ExecContext(ctx,
		`UPDATE threads SET title = 'hello '+id, first_user_message = 'hello '+id, name = NULL, thread_section_id = ?, section_position = 1000000, section_entered_at_ms = 123000 WHERE id = ?`,
		sectionID, indexedOnlyID); err != nil {
		t.Fatal(err)
	}
	// A distinct rollout title wins over the index name.
	if _, err := runtime.StateDB().ExecContext(ctx,
		`UPDATE threads SET title = 'Custom title', first_user_message = 'hello', name = NULL WHERE id = ?`,
		derivedTitleID); err != nil {
		t.Fatal(err)
	}
	// Paginated rows only use their stored SQLite name.
	if _, err := runtime.StateDB().ExecContext(ctx,
		`UPDATE threads SET history_mode = 'paginated', title = 'Paginated title', name = NULL WHERE id = ?`,
		paginatedID); err != nil {
		t.Fatal(err)
	}
	for threadID, name := range map[string]string{
		indexedOnlyID:  "Indexed only",
		derivedTitleID: "Indexed loses",
		paginatedID:    "Indexed paginated",
	} {
		if err := rollout.AppendThreadName(home, threadID, name); err != nil {
			t.Fatalf("AppendThreadName(%s): %v", threadID, err)
		}
	}

	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadList, ThreadListParams{UseStateDBOnly: true}))
	if response.Error != nil {
		t.Fatalf("thread/list error: %+v", response.Error)
	}
	threads := map[string]Thread{}
	for _, thread := range response.Result.(*ThreadListResponse).Data {
		threads[thread.ID] = thread
	}
	assertName := func(threadID string, want string) {
		t.Helper()
		thread, ok := threads[threadID]
		if !ok {
			t.Fatalf("thread %s missing from the listing", threadID)
		}
		if want == "" {
			if thread.Name != nil {
				t.Fatalf("thread %s name = %q, want none", threadID, *thread.Name)
			}
			return
		}
		if thread.Name == nil || *thread.Name != want {
			t.Fatalf("thread %s name = %v, want %q", threadID, thread.Name, want)
		}
	}
	assertName(indexedOnlyID, "Indexed only")
	assertName(derivedTitleID, "Custom title")
	assertName(paginatedID, "")
	sectioned := threads[indexedOnlyID]
	if sectioned.Section == nil || sectioned.Section.ID != sectionID || sectioned.Section.Name != "Work" {
		t.Fatalf("section = %+v, want the SQLite row's section", sectioned.Section)
	}
	if sectioned.SectionEnteredAt == nil || *sectioned.SectionEnteredAt != 123 {
		t.Fatalf("sectionEnteredAt = %v, want 123", sectioned.SectionEnteredAt)
	}
}

// TestThreadListRolloutLegacyNamesUseSessionIndexLikeRust mirrors the best-effort
// name enrichment Rust applies to rollout listings (#61866).
func TestThreadListRolloutLegacyNamesUseSessionIndexLikeRust(t *testing.T) {
	home := t.TempDir()
	threadID := "0198f002-0000-7000-8000-000000000011"
	writeLegacyRollout(t, home, threadID, "cli", time.Now().UTC().Add(-time.Hour))
	if err := rollout.AppendThreadName(home, threadID, "Indexed rollout name"); err != nil {
		t.Fatal(err)
	}
	router := NewRouter(session.NewStore(home))
	response := router.Handle(requestWithParams(t, IntID(1), MethodThreadList, ThreadListParams{}))
	if response.Error != nil {
		t.Fatalf("thread/list error: %+v", response.Error)
	}
	for _, thread := range response.Result.(*ThreadListResponse).Data {
		if thread.ID != threadID {
			continue
		}
		if thread.Name == nil || *thread.Name != "Indexed rollout name" {
			t.Fatalf("thread name = %v, want the indexed name", thread.Name)
		}
		return
	}
	t.Fatalf("thread %s missing from the rollout listing", threadID)
}

// TestRepeatedLegacyRowsKeepTheirOwnMetadataLikeRust mirrors #61866's duplicate
// row case: a thread that reappears across a pagination cursor keeps each
// occurrence's timestamps and section position along with the indexed name.
func TestRepeatedLegacyRowsKeepTheirOwnMetadataLikeRust(t *testing.T) {
	row := state.ThreadListRow{ID: "duplicate-thread", HistoryMode: "legacy", Title: sql.NullString{String: "hello", Valid: true}, FirstUserMessage: sql.NullString{String: "hello", Valid: true}}
	if got := listedThreadNameFromRow(&row, "Indexed name"); got != "Indexed name" {
		t.Fatalf("listedThreadNameFromRow() = %q, want the indexed name", got)
	}
	first := session.Record{ID: "duplicate-thread", Title: listedThreadNameFromRow(&row, "Indexed name"), CreatedAt: time.Unix(1, 0), UpdatedAt: time.Unix(1, 0), RecencyAt: time.Unix(1, 0), SectionPosition: int64Ptr(1_000_000)}
	second := first
	second.UpdatedAt = time.Unix(3, 0)
	second.SectionPosition = int64Ptr(2_000_000)
	page, err := session.ListRecords([]session.Record{first, second}, session.ListOptions{PageSize: 2})
	if err != nil {
		t.Fatalf("ListRecords() error = %v", err)
	}
	if len(page.Records) != 2 {
		t.Fatalf("records = %d, want both occurrences", len(page.Records))
	}
	if page.Records[0].SectionPosition == nil || *page.Records[0].SectionPosition != 1_000_000 ||
		page.Records[1].SectionPosition == nil || *page.Records[1].SectionPosition != 2_000_000 {
		t.Fatalf("section positions = %#v, want each occurrence's own value", page.Records)
	}
	for i := range page.Records {
		if page.Records[i].Title != "Indexed name" {
			t.Fatalf("record %d title = %q, want the indexed name", i, page.Records[i].Title)
		}
	}

	// The derived Guardian label yields to the session-index name; without an
	// indexed name the distinct title is still used.
	guardian := state.ThreadListRow{
		HistoryMode:      "legacy",
		Title:            sql.NullString{String: session.GuardianThreadTitle, Valid: true},
		FirstUserMessage: sql.NullString{String: "approve this action", Valid: true},
		Source:           session.GuardianReviewSource,
	}
	if got := listedThreadNameFromRow(&guardian, "Guardian explicit"); got != "Guardian explicit" {
		t.Fatalf("guardian listed name = %q, want the indexed name", got)
	}
	if got := listedThreadNameFromRow(&guardian, ""); got != session.GuardianThreadTitle {
		t.Fatalf("guardian listed name without index = %q, want the derived title", got)
	}
}
