package appserver

import (
	"strings"
	"testing"

	"codex_go/session"
)

// Mirrors Rust #41879: the independent review transcript is captured before
// compaction replaces the model window, so summarized-away user and tool
// evidence remains available, while thread/revert removes it with its turn.
func TestGuardianReviewHistorySurvivesCompactionAndRollbackLikeRust(t *testing.T) {
	now := fixedTime()
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
			ID: "follow-up", Type: "message", Role: "user", Text: "Inspect another repository.",
			Metadata: map[string]any{"turnId": "turn-2"}, CreatedAt: now.Add(1),
		},
	}
	router := guardianProfileTestRouter(t, items)
	record, err := router.threadRecord(guardianProfileTestThreadID, true, true)
	if err != nil || record == nil {
		t.Fatalf("threadRecord() = %#v, %v", record, err)
	}

	router.captureGuardianReviewHistory(guardianProfileTestThreadID, record.Items)
	record.Items = []session.Item{{
		ID: "summary", Type: "message", Role: "assistant", Text: "Repository inspection was summarized.",
		Metadata: map[string]any{"compact": true, "kind": "compaction_summary"},
	}}
	if err := router.services.ThreadRouter.store.Save(record); err != nil {
		t.Fatalf("Save() after compaction: %v", err)
	}

	selection := router.guardianReviewTranscriptSelection(guardianProfileTestThreadID)
	joined := guardianTranscriptText(selection)
	for _, evidence := range []string{
		"user: Only publish privately.",
		`{"repository":"example"}`,
		"tool publish result:",
		`{"private":true}`,
		"Inspect another repository.",
	} {
		if !strings.Contains(joined, evidence) {
			t.Fatalf("review transcript missing %q:\n%s", evidence, joined)
		}
	}
	if strings.Contains(joined, "Repository inspection was summarized.") {
		t.Fatalf("compaction summary became review evidence:\n%s", joined)
	}

	router.truncateGuardianReviewHistoryBefore(guardianProfileTestThreadID, "turn-2")
	selection = router.guardianReviewTranscriptSelection(guardianProfileTestThreadID)
	joined = guardianTranscriptText(selection)
	if strings.Contains(joined, "Inspect another repository.") {
		t.Fatalf("reverted user evidence survived:\n%s", joined)
	}
	if !strings.Contains(joined, "Only publish privately.") || !strings.Contains(joined, `{"private":true}`) {
		t.Fatalf("revert removed surviving evidence:\n%s", joined)
	}
}

func guardianTranscriptText(selection guardianTranscriptSelection) string {
	var builder strings.Builder
	for _, entry := range selection.Entries {
		builder.WriteString(entry.LineRole)
		builder.WriteString(": ")
		builder.WriteString(entry.Text)
		builder.WriteByte('\n')
	}
	return builder.String()
}
