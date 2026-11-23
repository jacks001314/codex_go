package appserver

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"codex_go/compact"
	"codex_go/config"
	"codex_go/model"
	"codex_go/session"
)

// TestFeedbackTagsUseLatestSamplingCOTRemovalLikeRust mirrors Rust #62883
// (`619d36f810`): feedback/save reports carry `cot_removed` from the latest
// completed sampling request on the live thread. A reported removal is "true",
// an explicitly empty report is "false", an unreported response omits the tag,
// and any caller-supplied value is discarded.
func TestFeedbackTagsUseLatestSamplingCOTRemovalLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	t.Cleanup(func() { _ = router.Close() })
	const threadID = "thread-feedback-cot-removed"
	clientTags := map[string]string{"cot_removed": "untrusted"}

	assertTag := func(t *testing.T, want string, wantPresent bool) {
		t.Helper()
		snapshot := router.feedbackSnapshotForRequest(threadID)
		tags := snapshot.UploadTags("bad_result", nil, clientTags, nil)
		got, present := tags["cot_removed"]
		if present != wantPresent || (wantPresent && got != want) {
			t.Fatalf("cot_removed = %q (present %v), want %q (present %v); tags=%#v", got, present, want, wantPresent, tags)
		}
	}

	assertTag(t, "", false)

	router.recordSamplingCOTRemoved(threadID, &model.ReasoningRemovals{Counts: map[model.ReasoningRemovalReason]int64{}})
	assertTag(t, "false", true)

	reported := &model.ReasoningRemovals{Counts: map[model.ReasoningRemovalReason]int64{model.ReasoningRemovalPrefixMismatch: 1}}
	router.recordSamplingCOTRemoved(threadID, reported)
	assertTag(t, "true", true)

	// A completed response without a report clears the stored value.
	router.recordSamplingCOTRemoved(threadID, nil)
	assertTag(t, "", false)

	// The status is per live thread: another thread has no report.
	other := router.feedbackSnapshotForRequest("thread-other")
	if _, present := other.UploadTags("bad_result", nil, clientTags, nil)["cot_removed"]; present {
		t.Fatalf("unrelated thread inherited cot_removed: %#v", other.Tags)
	}
}

// TestSessionTelemetryRecordsLatestCOTRemovalLikeRust checks the app server's
// session telemetry forwards every completed sampling request's reasoning
// report to the per-thread state.
func TestSessionTelemetryRecordsLatestCOTRemovalLikeRust(t *testing.T) {
	router := NewRuntimeRouter(RuntimeServices{})
	t.Cleanup(func() { _ = router.Close() })
	const threadID = "thread-telemetry-cot-removed"
	agent := &model.ResponsesAgentRunner{}
	router.installSessionTelemetry(agent, threadID)
	if agent.Telemetry == nil {
		t.Fatal("the model runner has no session telemetry sink")
	}

	agent.Telemetry.RecordSSEEventCompleted(context.Background(), model.SSECompletedRecord{
		ReasoningRemovals: &model.ReasoningRemovals{Counts: map[model.ReasoningRemovalReason]int64{model.ReasoningRemovalPrefixMismatch: 2}},
	})
	if removed, ok := router.lastSamplingCOTRemoved(threadID); !ok || !removed {
		t.Fatalf("reported removals = (%v, %v), want (true, true)", removed, ok)
	}

	agent.Telemetry.RecordSSEEventCompleted(context.Background(), model.SSECompletedRecord{
		ReasoningRemovals: &model.ReasoningRemovals{Counts: map[model.ReasoningRemovalReason]int64{}},
	})
	if removed, ok := router.lastSamplingCOTRemoved(threadID); !ok || removed {
		t.Fatalf("empty report = (%v, %v), want (false, true)", removed, ok)
	}

	agent.Telemetry.RecordSSEEventCompleted(context.Background(), model.SSECompletedRecord{})
	if _, ok := router.lastSamplingCOTRemoved(threadID); ok {
		t.Fatal("an unreported response kept a stored status")
	}
}

type failingCompactRunner struct {
	err error
}

func (r *failingCompactRunner) Compact(context.Context, *compact.Request) (*compact.Result, error) {
	return nil, r.err
}

func newCOTRemovalCompactionContext(t *testing.T, runner compact.RemoteRunner) (*RuntimeRouter, string) {
	t.Helper()
	home := t.TempDir()
	store := session.NewStore(filepath.Join(home, "sessions"))
	now := time.Now().UTC()
	const threadID = "thread-cot-removal-compaction"
	if err := store.Create(&session.Record{
		ID: threadID, SessionID: threadID,
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{Model: "gpt-5.4", CWD: home, Extra: map[string]any{}},
		Items: []session.Item{
			{ID: "u1", Type: "message", Role: "user", Text: "first request", CreatedAt: now},
			{ID: "a1", Type: "agent_message", Role: "assistant", Text: "first answer", CreatedAt: now},
		},
	}); err != nil {
		t.Fatalf("Create record error = %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter:  NewRouter(store),
		Config:        config.NewConfigService(home),
		CompactRunner: runner,
	})
	t.Cleanup(func() { _ = router.Close() })
	router.SetNotificationSink(NewNotificationBuffer())
	return router, threadID
}

// TestCompactionClearsOrPreservesLatestCOTRemovalLikeRust covers Rust #62883's
// history-replacement rule: a successful compaction clears the stored report
// (the next feedback omits the tag), while a failed compaction keeps the last
// sampling result.
func TestCompactionClearsOrPreservesLatestCOTRemovalLikeRust(t *testing.T) {
	reported := &model.ReasoningRemovals{Counts: map[model.ReasoningRemovalReason]int64{model.ReasoningRemovalPrefixMismatch: 1}}

	t.Run("failed compaction preserves the report", func(t *testing.T) {
		router, threadID := newCOTRemovalCompactionContext(t, &failingCompactRunner{err: errors.New("replacement failed validation")})
		router.recordSamplingCOTRemoved(threadID, reported)
		if _, err := router.compactThread(context.Background(), &runtimeCompactRequest{
			ThreadID:   threadID,
			TurnID:     "turn-compact-fails",
			Trigger:    compact.TriggerManual,
			Reason:     compact.ReasonUserRequested,
			Phase:      compact.PhaseStandaloneTurn,
			RemoteOnly: true,
		}); err == nil {
			t.Fatal("compactThread() error = nil, want the replacement failure")
		}
		if removed, ok := router.lastSamplingCOTRemoved(threadID); !ok || !removed {
			t.Fatalf("failed compaction status = (%v, %v), want (true, true)", removed, ok)
		}
	})

	t.Run("successful compaction clears the report", func(t *testing.T) {
		router, threadID := newCOTRemovalCompactionContext(t, &recordingCompactRunner{summary: "compaction summary"})
		router.recordSamplingCOTRemoved(threadID, reported)
		if _, err := router.compactThread(context.Background(), &runtimeCompactRequest{
			ThreadID: threadID,
			TurnID:   "turn-compact-succeeds",
			Trigger:  compact.TriggerManual,
			Reason:   compact.ReasonUserRequested,
			Phase:    compact.PhaseStandaloneTurn,
		}); err != nil {
			t.Fatalf("compactThread() error = %v", err)
		}
		if removed, ok := router.lastSamplingCOTRemoved(threadID); ok {
			t.Fatalf("successful compaction status = (%v, %v), want no stored report", removed, ok)
		}
		snapshot := router.feedbackSnapshotForRequest(threadID)
		if _, present := snapshot.UploadTags("bad_result", nil, nil, nil)["cot_removed"]; present {
			t.Fatal("feedback after compaction kept the cot_removed tag")
		}
	})
}
