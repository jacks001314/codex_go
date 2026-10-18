package appserver

import (
	"strings"
	"testing"
	"time"

	"codex_go/rollout"
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

// Mirrors Rust #42065's restart boundary: a paginated rollout restores the
// bounded Guardian transcript from the compacted checkpoint rather than from
// the summarized model window.
func TestGuardianReviewHistoryRestoresFromCompactionCheckpoint(t *testing.T) {
	now := fixedTime()
	home := t.TempDir()
	store := session.NewStore(home)
	threadID := session.ThreadID("123e4567-e89b-42d3-a456-426614174121")
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome: home, ThreadID: string(threadID), SessionID: string(threadID),
		Source: "cli", CWD: home, ModelProvider: "openai", HistoryMode: "paginated", Now: now,
	})
	if err != nil {
		t.Fatalf("NewRecorder() error = %v", err)
	}
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
	}
	if err := store.Create(&session.Record{
		ID: threadID, SessionID: string(threadID), CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{
			Source: "cli", ModelProvider: "openai",
			Extra: map[string]any{"rollout_path": recorder.Path()},
		},
		Items: items,
	}); err != nil {
		t.Fatalf("store.Create() error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	record, err := router.threadRecord(threadID, true, true)
	if err != nil || record == nil {
		t.Fatalf("threadRecord() = %#v, %v", record, err)
	}
	router.captureGuardianReviewHistory(string(threadID), record.Items)
	guardianHistory := router.guardianReviewHistoryCheckpointWire(string(threadID), now)
	if len(guardianHistory) == 0 {
		t.Fatal("guardian checkpoint wire is empty")
	}
	if err := recorder.AppendCompactedWithCheckpoint(
		"Repository inspection was summarized.",
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
	record.Items = []session.Item{{
		ID: "summary", Type: "message", Role: "assistant", Text: "Repository inspection was summarized.",
		Metadata: map[string]any{"compact": true, "kind": "compaction_summary"},
	}}
	if err := store.Save(record); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	restarted := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)})
	selection := restarted.guardianReviewTranscriptSelection(string(threadID))
	joined := guardianTranscriptText(selection)
	for _, evidence := range []string{
		"Only publish privately.",
		`{"repository":"example"}`,
		"tool publish result:",
		`{"private":true}`,
	} {
		if !strings.Contains(joined, evidence) {
			t.Fatalf("restored transcript missing %q:\n%s", evidence, joined)
		}
	}
}

func TestGuardianReviewHistoryForkAndSubagentIsolationLikeRust(t *testing.T) {
	now := fixedTime()
	source := &session.Record{
		ID: "source", SessionID: "source", CreatedAt: now,
		Items: []session.Item{
			{
				ID: "restriction", Type: "message", Role: "user", Text: "Only publish privately.",
				Metadata: map[string]any{"turnId": "turn-1"}, CreatedAt: now,
			},
			{
				ID: "follow-up", Type: "message", Role: "user", Text: "Inspect the release.",
				Metadata: map[string]any{"turnId": "turn-2"}, CreatedAt: now.Add(1),
			},
		},
	}
	router := NewRuntimeRouter(RuntimeServices{})
	router.forkGuardianReviewHistory(source, "user-fork", "", "", 0)
	forked := router.guardianReviewHistoryItems("user-fork", &session.Record{ID: "user-fork", CreatedAt: now})
	if len(forked) != 2 || forked[0].Text != "Only publish privately." {
		t.Fatalf("user fork review history = %#v", forked)
	}
	router.forkGuardianReviewHistory(source, "user-fork-last", "", "", 1)
	forked = router.guardianReviewHistoryItems("user-fork-last", &session.Record{ID: "user-fork-last", CreatedAt: now})
	if len(forked) != 1 || forked[0].Text != "Inspect the release." {
		t.Fatalf("last-turn user fork review history = %#v", forked)
	}
	router.forkGuardianReviewHistory(source, "user-fork-through", "", "turn-1", 0)
	forked = router.guardianReviewHistoryItems("user-fork-through", &session.Record{ID: "user-fork-through", CreatedAt: now})
	if len(forked) != 1 || forked[0].Text != "Only publish privately." {
		t.Fatalf("through-turn user fork review history = %#v", forked)
	}

	router.captureGuardianReviewHistory("subagent", nil)
	inherited := router.guardianReviewHistoryItems("subagent", source)
	if len(inherited) != 0 {
		t.Fatalf("subagent inherited parent review evidence: %#v", inherited)
	}
	router.recordGuardianReviewHistoryAppend("subagent", []session.Item{{
		ID: "child-evidence", Type: "message", Role: "user", Text: "Inspect the child workspace.",
		Metadata: map[string]any{"turnId": "child-turn"}, CreatedAt: now,
	}})
	inherited = router.guardianReviewHistoryItems("subagent", source)
	if len(inherited) != 1 || inherited[0].Text != "Inspect the child workspace." {
		t.Fatalf("subagent-local review history = %#v", inherited)
	}
}
