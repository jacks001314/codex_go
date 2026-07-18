package chatwidget

import "testing"

func TestHookLifecycleStartFlushesCompletedAndStartsActiveMatchRust(t *testing.T) {
	state := HookLifecycleState{
		CompletedPersistentRuns: []HookRun{{ID: "old", Name: "Old", Status: HookStatusCompleted}},
	}

	result := state.OnHookStarted(HookRun{ID: "new", Name: "New", Command: "echo new"})

	if !result.RecordedVisibleTurnActivity || !result.FlushAnswerStream || !result.RequestRedraw {
		t.Fatalf("start side effects = %#v", result)
	}
	if len(result.InsertedCompletedRuns) != 1 || result.InsertedCompletedRuns[0].ID != "old" {
		t.Fatalf("flushed completed runs = %#v", result.InsertedCompletedRuns)
	}
	if len(state.ActiveRuns) != 1 || state.ActiveRuns[0].ID != "new" || state.ActiveRuns[0].Status != HookStatusStarted {
		t.Fatalf("active runs = %#v", state.ActiveRuns)
	}
	if !state.NeedsFinalMessageSeparator || state.UsageInsertionRequests == 0 || state.ActiveCellRevision == 0 {
		t.Fatalf("state flags = %#v", state)
	}
}

func TestHookLifecycleCompleteExistingFlushesAndClearsIdleMatchRust(t *testing.T) {
	state := HookLifecycleState{}
	state.OnHookStarted(HookRun{ID: "run-1", Name: "Lint"})

	result := state.OnHookCompleted(HookRun{ID: "run-1", Name: "Lint", Output: "ok"})

	if len(result.InsertedCompletedRuns) != 1 || result.InsertedCompletedRuns[0].ID != "run-1" {
		t.Fatalf("completed output = %#v", result.InsertedCompletedRuns)
	}
	if len(state.ActiveRuns) != 0 || len(state.CompletedPersistentRuns) != 0 {
		t.Fatalf("runs should be flushed and idle: %#v", state)
	}
	if !result.NeedsFinalMessageSeparator || !result.RequestedUsageInsertion || !result.ActiveCellCleared {
		t.Fatalf("completion flags = %#v", result)
	}
}

func TestHookLifecycleClearActiveHookCellDropsTransientStateMatchRust(t *testing.T) {
	state := HookLifecycleState{
		ActiveRuns:              []HookRun{{ID: "run-1", Status: HookStatusStarted}},
		CompletedPersistentRuns: []HookRun{{ID: "done", Status: HookStatusCompleted}},
	}

	result := state.ClearActiveHookCell()

	if !result.ActiveCellCleared || !result.BumpedActiveCellRevision || !result.RequestedUsageInsertion {
		t.Fatalf("clear result = %#v", result)
	}
	if len(state.ActiveRuns) != 0 || len(state.CompletedPersistentRuns) != 0 {
		t.Fatalf("active state should be dropped: %#v", state)
	}
}
