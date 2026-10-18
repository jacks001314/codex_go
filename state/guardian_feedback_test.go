package state

import (
	"context"
	"path/filepath"
	"testing"
)

func seedGuardianFeedbackThread(t *testing.T, runtime *StateRuntime, id string) {
	t.Helper()
	_, err := runtime.StateDB().ExecContext(context.Background(), `INSERT INTO threads
		(id, rollout_path, created_at, updated_at, created_at_ms, updated_at_ms, recency_at_ms, source, model_provider, cwd, title, sandbox_policy, approval_mode)
		VALUES (?, ?, 1, 1, 1, 1, 1, 'cli', 'openai', '/workspace', 'thread', '', '')`,
		id, filepath.Join("sessions", id+".jsonl"))
	if err != nil {
		t.Fatalf("insert thread %s: %v", id, err)
	}
}

func TestGuardianReviewFeedbackPersistsAndCascadesLikeRust(t *testing.T) {
	ctx := context.Background()
	config, err := NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	seedGuardianFeedbackThread(t, runtime, "thread-a")
	seedGuardianFeedbackThread(t, runtime, "thread-b")
	for i := 0; i < MaxGuardianReviewRecordsPerThread+2; i++ {
		record := NewGuardianReviewRecord("thread-a", []byte(`{"thread":"a"}`))
		if err := runtime.RecordGuardianReviewFailure(ctx, &record); err != nil {
			t.Fatal(err)
		}
	}
	record := NewGuardianReviewRecord("thread-b", []byte(`{"thread":"b"}`))
	if err := runtime.RecordGuardianReviewFailure(ctx, &record); err != nil {
		t.Fatal(err)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	reopened, err := InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	records, err := reopened.ListGuardianReviewRecords(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != MaxGuardianReviewRecordsPerThread+1 {
		t.Fatalf("records after reopen = %d, want %d", len(records), MaxGuardianReviewRecordsPerThread+1)
	}
	countA := 0
	for _, record := range records {
		if record.ThreadID == "thread-a" {
			countA++
		}
	}
	if countA != MaxGuardianReviewRecordsPerThread {
		t.Fatalf("thread-a records = %d, want %d", countA, MaxGuardianReviewRecordsPerThread)
	}
	if _, err := reopened.DeleteThreadsStrict(ctx, []string{"thread-a"}); err != nil {
		t.Fatal(err)
	}
	records, err = reopened.ListGuardianReviewRecords(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != 1 || records[0].ThreadID != "thread-b" {
		t.Fatalf("records after delete = %#v", records)
	}
}

func TestGuardianReviewFeedbackGlobalCountBudgetLikeRust(t *testing.T) {
	ctx := context.Background()
	config, err := NewSqliteConfig(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	runtime, err := InitStateRuntime(ctx, config, "openai")
	if err != nil {
		t.Fatal(err)
	}
	defer runtime.Close()
	for i := 0; i < 10; i++ {
		seedGuardianFeedbackThread(t, runtime, "thread-"+string(rune('a'+i)))
	}
	for i := 0; i < MaxGuardianReviewRecords; i++ {
		record := NewGuardianReviewRecord("thread-"+string(rune('a'+i%10)), []byte(`{"record":true}`))
		if err := runtime.RecordGuardianReviewFailure(ctx, &record); err != nil {
			t.Fatal(err)
		}
	}
	records, err := runtime.ListGuardianReviewRecords(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(records) != MaxGuardianReviewRecords {
		t.Fatalf("global records = %d, want %d", len(records), MaxGuardianReviewRecords)
	}
}
