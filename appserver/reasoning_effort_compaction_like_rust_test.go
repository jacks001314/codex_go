package appserver

import (
	"context"
	"errors"
	"testing"
	"time"

	"codex_go/compact"
	"codex_go/model"
	"codex_go/session"
)

// Rust #43796 (`35d9e4bc4d`) separates request-effort lookups by usage:
// sampling pins the selected effort, while compaction reuses the pin when it
// matches and never mutates live state. A fallback compaction model resolves
// its own effort so a failed attempt keeps the original pin.
func TestReasoningEffortCompactionKeepsFallbackLookupSideEffectFreeLikeRust(t *testing.T) {
	router := &RuntimeRouter{}
	const threadID = "thread-effort-compaction-fallback"
	router.setReasoningEffortPinState(threadID, reasoningEffortPin{
		Kind: reasoningEffortPinActive, Model: "original", Effort: "low",
	})

	// Compaction against a different (fallback) model resolves that model's own
	// effort and leaves the live pin for the original model untouched.
	if got := router.reasoningEffortForRequest(threadID, "fallback", "low", true, "medium", true, requestEffortCompaction); got != "medium" {
		t.Fatalf("fallback compaction effort = %q, want medium", got)
	}
	if got, ok := router.reasoningEffortPinState(threadID).Get("original"); !ok || got != "low" {
		t.Fatalf("fallback compaction pin = %q,%v, want low,true", got, ok)
	}

	// A failed compaction must not retire or replace the pin: the next sampling
	// request still reuses the original baseline.
	if got := router.reasoningEffortForRequest(threadID, "original", "high", true, "high", true, requestEffortSampling); got != "low" {
		t.Fatalf("sampling after failed compaction = %q, want pinned low", got)
	}

	// Compaction on the pinned model reuses the pin even when the selected and
	// update efforts differ.
	if got := router.reasoningEffortForRequest(threadID, "original", "high", true, "high", true, requestEffortCompaction); got != "low" {
		t.Fatalf("pinned compaction effort = %q, want low", got)
	}
	if got, ok := router.reasoningEffortPinState(threadID).Get("original"); !ok || got != "low" {
		t.Fatalf("compaction mutated pin to %q,%v, want low,true", got, ok)
	}

	// With overrides disabled (for example after resuming with the feature off)
	// compaction uses the selected effort and never establishes a pin.
	router.clearReasoningEffortPin(threadID)
	if got := router.reasoningEffortForRequest(threadID, "original", "high", false, "high", true, requestEffortCompaction); got != "high" {
		t.Fatalf("disabled compaction effort = %q, want selected high", got)
	}
	if _, ok := router.reasoningEffortPinState(threadID).Get("original"); ok {
		t.Fatal("disabled compaction must not pin an effort")
	}
}

// The compaction runner is built from the record's model, so it must carry the
// pinned request effort instead of the newest selected effort. Resolving a
// fallback model's effort is a read-only look at the live pin.
func TestCompactRunnerResolvesEffortFromPinWithoutMutatingItLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	const threadID = "thread-compact-effort"
	record := &session.Record{
		ID: session.ThreadID(threadID), SessionID: threadID,
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{Model: "gpt-5.4", ModelProvider: "openai", CWD: t.TempDir()},
	}
	if err := store.Create(record); err != nil {
		t.Fatalf("Create record error = %v", err)
	}
	manager := model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{
		{
			Slug: "gpt-5.4", DisplayName: "gpt-5.4", Visibility: model.VisibilityList, SupportedInAPI: true,
			SupportsReasoningEffortUpdates: true, DefaultReasoningLevel: "high",
		},
		{
			Slug: "gpt-5.2", DisplayName: "gpt-5.2", Visibility: model.VisibilityList, SupportedInAPI: true,
			SupportsReasoningEffortUpdates: true, DefaultReasoningLevel: "low",
		},
	}})
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(store),
		Agent:        &model.ResponsesAgentRunner{ModelsManager: manager},
		Models:       model.NewModelService(manager),
	})
	defer router.Close()

	router.setReasoningEffortPinState(threadID, reasoningEffortPin{
		Kind: reasoningEffortPinActive, Model: "gpt-5.4", Effort: "medium",
	})
	request := &compact.Request{
		ThreadID: threadID,
		TurnID:   "turn-compact-effort",
		Trigger:  compact.TriggerManual,
		Reason:   compact.ReasonUserRequested,
		Phase:    compact.PhaseStandaloneTurn,
	}

	runner, ok := router.compactRunnerForRecord(record, request, "", "").(*agentCompactRunner)
	if !ok || runner == nil {
		t.Fatalf("compactRunnerForRecord() = %#v, want an agent compaction runner", runner)
	}
	if runner.effort != "medium" {
		t.Fatalf("compaction effort = %q, want the pinned medium", runner.effort)
	}

	fallback, ok := router.compactRunnerForRecord(record, request, "gpt-5.2", "").(*agentCompactRunner)
	if !ok || fallback == nil {
		t.Fatalf("fallback compactRunnerForRecord() = %#v, want an agent compaction runner", fallback)
	}
	if fallback.effort != "low" {
		t.Fatalf("fallback compaction effort = %q, want gpt-5.2's low", fallback.effort)
	}
	if got, ok := router.reasoningEffortPinState(threadID).Get("gpt-5.4"); !ok || got != "medium" {
		t.Fatalf("fallback lookup pin = %q,%v, want medium,true", got, ok)
	}
}

type pinTransitionCompactRunner struct {
	fail bool
}

func (r *pinTransitionCompactRunner) Compact(_ context.Context, request *compact.Request) (*compact.Result, error) {
	if r.fail {
		return nil, errors.New("compaction failed")
	}
	return &compact.Result{
		Status:      compact.StatusCompleted,
		Request:     *request,
		Summary:     "compaction summary",
		NewHistory:  compact.BuildCompactedHistory(nil, nil, "compaction summary"),
		CompletedAt: time.Now().UTC(),
		Source:      compact.SourceRemote,
	}, nil
}

// Mirrors Rust's success/failure compaction arms: only a successful compaction
// retires the pin, and the next sampling request re-establishes the selected
// effort without a redundant configuration_update.
func TestCompactionFailureKeepsPinAndSuccessRetiresItLikeRust(t *testing.T) {
	const threadID = "thread-compaction-pin"
	router, _ := compactionAccountingThread(t, threadID)
	router.setReasoningEffortPinState(threadID, reasoningEffortPin{
		Kind: reasoningEffortPinActive, Model: "gpt-test", Effort: "medium",
	})

	failed := &runtimeCompactRequest{
		ThreadID:   threadID,
		TurnID:     "turn-compact-fail",
		Trigger:    compact.TriggerManual,
		Reason:     compact.ReasonUserRequested,
		Phase:      compact.PhaseStandaloneTurn,
		RemoteOnly: true,
	}
	router.services.CompactRunner = &pinTransitionCompactRunner{fail: true}
	if _, err := router.compactThread(context.Background(), failed); err == nil {
		t.Fatal("compactThread() error = nil, want the failing compaction error")
	}
	if got, ok := router.reasoningEffortPinState(threadID).Get("gpt-test"); !ok || got != "medium" {
		t.Fatalf("pin after failed compaction = %q,%v, want medium,true", got, ok)
	}
	if got := router.reasoningEffortForRequest(threadID, "gpt-test", "high", true, "high", true, requestEffortSampling); got != "medium" {
		t.Fatalf("sampling after failed compaction = %q, want the pinned medium", got)
	}

	succeeded := *failed
	succeeded.TurnID = "turn-compact-success"
	router.services.CompactRunner = &pinTransitionCompactRunner{}
	if _, err := router.compactThread(context.Background(), &succeeded); err != nil {
		t.Fatalf("compactThread() error = %v", err)
	}
	if pin := router.reasoningEffortPinState(threadID); pin.Kind != reasoningEffortPinCompacted {
		t.Fatalf("pin after successful compaction = %#v, want Compacted", pin)
	}
	if items := router.reasoningEffortOverrideInputItems(threadID, "gpt-test", "high", true, nil); len(items) != 0 {
		t.Fatalf("post-compaction updates = %#v, want none", items)
	}
	if got := router.reasoningEffortForRequest(threadID, "gpt-test", "high", true, "high", true, requestEffortSampling); got != "high" {
		t.Fatalf("sampling after successful compaction = %q, want the fresh baseline high", got)
	}

	// A later selection change still records exactly one update and keeps the
	// baseline for the request, matching Rust's post-compaction turn behavior.
	if items := router.reasoningEffortOverrideInputItems(threadID, "gpt-test", "low", true, nil); len(items) != 1 {
		t.Fatalf("changed selection updates = %#v, want one update", items)
	}
	if got := router.reasoningEffortForRequest(threadID, "gpt-test", "low", true, "low", true, requestEffortSampling); got != "high" {
		t.Fatalf("sampling after selection change = %q, want the baseline high", got)
	}
}
