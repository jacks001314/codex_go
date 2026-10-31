package state

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/rollout"
	"codex_go/session"
)

// writeGuardianRollout writes a review thread whose only user-visible content
// is a synthetic approval prompt, mirroring the Guardian rollouts Rust #47302
// makes compact in thread listings.
func writeGuardianRollout(t *testing.T, home, threadID string, now time.Time) string {
	t.Helper()
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, ThreadID: threadID, Source: session.GuardianReviewSource,
		ThreadSource: "subagent", CWD: "/workspace", ModelProvider: "openai",
		HistoryMode: "paginated", MemoryMode: "disabled", CLIVersion: "1.2.3", Now: now,
	})
	if err != nil {
		t.Fatal(err)
	}
	prompt := strings.Repeat("large synthetic guardian prompt ", 64)
	userPayload, _ := json.Marshal(map[string]any{"type": "user_message", "message": prompt})
	if err := recorder.AppendLine(rollout.Line{Type: "event_msg", Timestamp: now.Add(time.Second).Format(time.RFC3339Nano), Payload: userPayload}); err != nil {
		t.Fatal(err)
	}
	itemPayload, _ := json.Marshal(map[string]any{
		"type": "item_completed",
		"item": map[string]any{"type": "user_message", "message": prompt},
	})
	if err := recorder.AppendLine(rollout.Line{Type: "event_msg", Timestamp: now.Add(2 * time.Second).Format(time.RFC3339Nano), Payload: itemPayload}); err != nil {
		t.Fatal(err)
	}
	goalPayload, _ := json.Marshal(map[string]any{
		"type": "thread_goal_updated",
		"goal": map[string]any{"objective": "synthetic guardian objective"},
	})
	if err := recorder.AppendLine(rollout.Line{Type: "event_msg", Timestamp: now.Add(3 * time.Second).Format(time.RFC3339Nano), Payload: goalPayload}); err != nil {
		t.Fatal(err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatal(err)
	}
	return recorder.Path()
}

func TestGuardianReviewThreadKeepsCompactDisplayMetadataLikeRust(t *testing.T) {
	home := t.TempDir()
	path := writeGuardianRollout(t, home, "guardian-thread", time.Date(2026, 11, 1, 8, 0, 0, 0, time.UTC))

	metadata, err := extractRolloutThreadMetadata(path, false, "openai")
	if err != nil {
		t.Fatal(err)
	}
	if metadata.title != session.GuardianThreadTitle {
		t.Fatalf("title = %q, want %q", metadata.title, session.GuardianThreadTitle)
	}
	if metadata.preview != session.GuardianThreadPreview {
		t.Fatalf("preview = %q, want %q", metadata.preview, session.GuardianThreadPreview)
	}
	if metadata.firstUserMessage != "" {
		t.Fatalf("first_user_message = %q, want empty", metadata.firstUserMessage)
	}

	item, ok := rollout.BuildThreadItem(path, false)
	if !ok {
		t.Fatal("BuildThreadItem did not return a thread item")
	}
	if item.Preview != session.GuardianThreadPreview {
		t.Fatalf("summary preview = %q, want %q", item.Preview, session.GuardianThreadPreview)
	}
	if item.FirstUserMessage != "" {
		t.Fatalf("summary first_user_message = %q, want empty", item.FirstUserMessage)
	}
}

func TestGuardianReviewBackfillPreservesCustomNameLikeRust(t *testing.T) {
	home := t.TempDir()
	runtime := newBackfillTestRuntimeAt(t, home)
	ctx := context.Background()
	path := writeGuardianRollout(t, home, "guardian-named", time.Date(2026, 11, 1, 9, 0, 0, 0, time.UTC))

	metadata, err := extractRolloutThreadMetadata(path, false, "openai")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.upsertRolloutThread(ctx, metadata); err != nil {
		t.Fatal(err)
	}
	if _, err := runtime.StateDB().ExecContext(ctx,
		"UPDATE threads SET name = ?, title = ? WHERE id = ?",
		"Named Guardian review", "Named Guardian review", metadata.id); err != nil {
		t.Fatal(err)
	}

	refreshed, err := extractRolloutThreadMetadata(path, false, "openai")
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.upsertRolloutThread(ctx, refreshed); err != nil {
		t.Fatal(err)
	}

	var title, name, preview, firstUserMessage string
	if err := runtime.StateDB().QueryRowContext(ctx,
		"SELECT title, COALESCE(name, ''), COALESCE(preview, ''), COALESCE(first_user_message, '') FROM threads WHERE id = ?",
		metadata.id).Scan(&title, &name, &preview, &firstUserMessage); err != nil {
		t.Fatal(err)
	}
	if title != "Named Guardian review" || name != "Named Guardian review" {
		t.Fatalf("title/name = %q/%q, want the explicit custom name", title, name)
	}
	if preview != session.GuardianThreadPreview {
		t.Fatalf("preview = %q, want %q", preview, session.GuardianThreadPreview)
	}
	if firstUserMessage != "" {
		t.Fatalf("first_user_message = %q, want empty", firstUserMessage)
	}
}
