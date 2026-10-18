package rollout

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/session"
)

func TestGuardianReviewHistoryPreservesCompactedToolEvidenceAndRollbackLikeRust(t *testing.T) {
	now := time.Date(2026, 10, 19, 1, 2, 3, 0, time.UTC)
	home := t.TempDir()
	recorder, err := NewRecorder(&CreateParams{
		CodexHome: home, ThreadID: "thread-review-history", SessionID: "thread-review-history",
		Source: "cli", CWD: home, ModelProvider: "openai", HistoryMode: "legacy", Now: now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	path := recorder.Path()
	items := []session.Item{
		{
			ID: "restriction", Type: "message", Role: "user", Text: "Only publish privately.",
			Metadata: map[string]any{"turnId": "turn-1"}, CreatedAt: now,
		},
		{
			ID: "call", Type: "function_call", Role: "assistant", Name: "publish",
			CallID: "call-1", Text: `{"repository":"example"}`, Metadata: map[string]any{"turnId": "turn-1"}, CreatedAt: now,
		},
		{
			ID: "result", Type: "function_call_output", CallID: "call-1",
			Text: `{"private":true}`, Metadata: map[string]any{"turnId": "turn-1"}, CreatedAt: now,
		},
		{
			ID: "second-turn", Type: "message", Role: "user", Text: "Inspect another repository.",
			Metadata: map[string]any{"turnId": "turn-2"}, CreatedAt: now.Add(time.Second),
		},
	}
	if err := AppendSessionItems(recorder, items, now); err != nil {
		t.Fatalf("AppendSessionItems() error = %v", err)
	}
	if err := recorder.AppendCompactedWithContext("Repository inspection was summarized.", []Item{{
		ID: "summary", Type: "message", Role: "assistant", Text: "Repository inspection was summarized.",
	}}, nil, now.Add(2*time.Second)); err != nil {
		t.Fatalf("AppendCompactedWithContext() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	lines, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	review := ReviewHistoryItems(lines, now)
	joined := reviewHistoryText(review)
	for _, evidence := range []string{"Only publish privately.", `{"repository":"example"}`, `{"private":true}`, "Inspect another repository."} {
		if !strings.Contains(joined, evidence) {
			t.Fatalf("review history missing %q:\n%s", evidence, joined)
		}
	}
	if strings.Contains(joined, "Repository inspection was summarized.") {
		t.Fatalf("compaction summary became review evidence:\n%s", joined)
	}

	recorder, err = Resume(path)
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if err := recorder.AppendThreadRolledBack(1, now.Add(3*time.Second)); err != nil {
		t.Fatalf("AppendThreadRolledBack() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() after rollback error = %v", err)
	}
	lines, _, err = Load(path)
	if err != nil {
		t.Fatalf("Load() after rollback error = %v", err)
	}
	joined = reviewHistoryText(ReviewHistoryItems(lines, now))
	if strings.Contains(joined, "Inspect another repository.") {
		t.Fatalf("rolled-back user evidence survived:\n%s", joined)
	}
	for _, evidence := range []string{"Only publish privately.", `{"private":true}`} {
		if !strings.Contains(joined, evidence) {
			t.Fatalf("rollback removed surviving evidence %q:\n%s", evidence, joined)
		}
	}

	recorder, err = Resume(path)
	if err != nil {
		t.Fatalf("Resume() before second rollback error = %v", err)
	}
	if err := recorder.AppendThreadRolledBack(1, now.Add(4*time.Second)); err != nil {
		t.Fatalf("AppendThreadRolledBack(second) error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() after second rollback error = %v", err)
	}
	lines, _, err = Load(path)
	if err != nil {
		t.Fatalf("Load() after second rollback error = %v", err)
	}
	if joined = reviewHistoryText(ReviewHistoryItems(lines, now)); strings.Contains(joined, "Only publish privately.") {
		t.Fatalf("rolled-back tool evidence survived:\n%s", joined)
	}
}

func TestCompactedGuardianHistoryRoundTripsLikeRust(t *testing.T) {
	now := time.Date(2026, 10, 19, 2, 3, 4, 0, time.UTC)
	home := t.TempDir()
	recorder, err := NewRecorder(&CreateParams{
		CodexHome: home, ThreadID: "thread-guardian-checkpoint", SessionID: "thread-guardian-checkpoint",
		Source: "cli", CWD: home, ModelProvider: "openai", HistoryMode: "paginated", Now: now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
	path := recorder.Path()
	items := []session.Item{
		{ID: "user", Type: "message", Role: "user", Text: "Keep the release private.", CreatedAt: now},
		{ID: "call", Type: "function_call", Name: "publish", CallID: "call-1", Text: `{"id":"release"}`, CreatedAt: now},
		{ID: "output", Type: "function_call_output", CallID: "call-1", Text: `{"private":true}`, CreatedAt: now},
	}
	rawItems := make([]json.RawMessage, 0, len(items))
	for index := range items {
		line, err := LineFromItem(ItemFromSessionItem(&items[index]), now)
		if err != nil {
			t.Fatalf("LineFromItem() error = %v", err)
		}
		rawItems = append(rawItems, line.Item)
	}
	guardianHistory, err := json.Marshal(rawItems)
	if err != nil {
		t.Fatalf("marshal guardian history: %v", err)
	}
	if err := recorder.AppendCompactedWithCheckpoint(
		"summary",
		nil,
		nil,
		nil,
		nil,
		"",
		nil,
		guardianHistory,
		now.Add(time.Second),
	); err != nil {
		t.Fatalf("AppendCompactedWithCheckpoint() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	lines, _, err := Load(path)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	restored := CompactedGuardianHistory(lines, now)
	if joined := reviewHistoryText(restored); !strings.Contains(joined, "Keep the release private.") ||
		!strings.Contains(joined, `{"private":true}`) {
		t.Fatalf("restored guardian history = %q", joined)
	}
	if legacy := CompactedGuardianHistory([]Line{{Type: "compacted", Payload: json.RawMessage(`{"message":"summary"}`)}}, now); legacy != nil {
		t.Fatalf("legacy compacted history = %#v, want nil", legacy)
	}
	recorder, err = Resume(path)
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if err := recorder.AppendThreadRolledBack(1, now.Add(2*time.Second)); err != nil {
		t.Fatalf("AppendThreadRolledBack() error = %v", err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() after rollback error = %v", err)
	}
	lines, _, err = Load(path)
	if err != nil {
		t.Fatalf("Load() after rollback error = %v", err)
	}
	if restored := CompactedGuardianHistory(lines, now); restored != nil {
		t.Fatalf("rolled-back checkpoint restored evidence: %#v", restored)
	}
}

func reviewHistoryText(items []session.Item) string {
	var builder strings.Builder
	for _, item := range items {
		builder.WriteString(item.Role)
		builder.WriteString(": ")
		builder.WriteString(item.Text)
		builder.WriteByte('\n')
	}
	return builder.String()
}
