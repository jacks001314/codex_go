package state

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

// TestThreadTokenUpdateKeepsConcurrentMetadataLikeRust pins Rust #60006(a): an
// observation that only advanced the recorded usage writes three columns, so a
// metadata change made between the rebuild's read and its write survives, and a
// genuine metadata difference still takes the full repair path.
func TestThreadTokenUpdateKeepsConcurrentMetadataLikeRust(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	runtime := newBackfillTestRuntimeAt(t, home)
	threadID := "0199aaaa-2222-7000-8000-000000000a01"
	now := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)

	metadata := rolloutThreadMetadata{
		id: threadID, path: filepath.Join(home, "sessions", "tokens.jsonl"),
		source: "cli", historyMode: "paginated", modelProvider: "openai",
		cwd: "/workspace", cliVersion: "1.2.3", title: "Token Thread",
		firstUserMessage: "tokens request",
		preview:          "preview text", sandboxPolicy: "read-only", approvalMode: "on-request",
		memoryMode: "disabled", createdAt: now, updatedAt: now, recencyAt: now,
		tokensUsed: 7,
	}
	if err := runtime.upsertRolloutThread(ctx, metadata); err != nil {
		t.Fatalf("upsertRolloutThread() error = %v", err)
	}
	before := readTimestampProbeRow(t, runtime, threadID)
	if before.tokensUsed != 7 {
		t.Fatalf("fixture tokens_used = %d, want 7", before.tokensUsed)
	}
	for _, stmt := range []string{
		`CREATE TABLE token_writes(kind TEXT)`,
		`CREATE TRIGGER count_metadata_write AFTER UPDATE OF title, model, cwd, preview, first_user_message ON threads BEGIN INSERT INTO token_writes VALUES ('metadata'); END`,
	} {
		if _, err := runtime.StateDB().ExecContext(ctx, stmt); err != nil {
			t.Fatalf("install write counter: %v", err)
		}
	}

	// Usage-only observation: the narrow branch keeps every other column.
	metadata.tokensUsed = 99
	metadata.updatedAt = now.Add(time.Hour)
	if err := runtime.upsertRolloutThread(ctx, metadata); err != nil {
		t.Fatalf("usage-only upsertRolloutThread() error = %v", err)
	}
	after := readTimestampProbeRow(t, runtime, threadID)
	if after.tokensUsed != 99 {
		t.Fatalf("tokens_used = %d after a usage-only observation, want 99", after.tokensUsed)
	}
	before.tokensUsed = after.tokensUsed
	if after != before {
		t.Fatalf("usage-only write changed metadata: %+v, want %+v", after, before)
	}
	var metadataWrites int
	if err := runtime.StateDB().QueryRowContext(ctx, `SELECT COUNT(*) FROM token_writes`).Scan(&metadataWrites); err != nil {
		t.Fatalf("read write counter: %v", err)
	}
	if metadataWrites != 0 {
		t.Fatalf("usage-only write touched metadata columns %d times, want 0", metadataWrites)
	}

	// A metadata change applied between the read and the write survives, because
	// the narrow statement names only tokens_used, updated_at and updated_at_ms.
	if _, err := runtime.StateDB().ExecContext(ctx, `UPDATE threads SET title = 'Concurrent' WHERE id = ?`, threadID); err != nil {
		t.Fatalf("concurrent metadata update: %v", err)
	}
	if err := runtime.UpdateThreadTokenUsage(ctx, threadID, 123, metadata.updatedAt.Unix(), metadata.updatedAt.UnixMilli()); err != nil {
		t.Fatalf("UpdateThreadTokenUsage() error = %v", err)
	}
	final := readTimestampProbeRow(t, runtime, threadID)
	if final.title != "Concurrent" {
		t.Fatalf("title = %q after a token write, want the concurrent update kept", final.title)
	}
	if final.tokensUsed != 123 {
		t.Fatalf("tokens_used = %d after UpdateThreadTokenUsage, want 123", final.tokensUsed)
	}
}
