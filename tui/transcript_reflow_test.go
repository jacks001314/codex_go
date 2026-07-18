package tui

import (
	"testing"
	"time"
)

func TestTranscriptReflowScheduleDebouncedPostponesExistingReflow(t *testing.T) {
	var state TranscriptReflowState
	if state.ScheduleDebounced(nil) {
		t.Fatal("ScheduleDebounced returned true, want false")
	}
	first := state.PendingUntil()
	if first == nil {
		t.Fatal("missing first pending deadline")
	}
	time.Sleep(time.Millisecond)
	if state.ScheduleDebounced(nil) {
		t.Fatal("second ScheduleDebounced returned true, want false")
	}
	second := state.PendingUntil()
	if second == nil || !second.After(*first) {
		t.Fatalf("second deadline = %v, first = %v", second, first)
	}
}

func TestTranscriptReflowScheduleDebouncedPostponesDueExistingReflow(t *testing.T) {
	var state TranscriptReflowState
	state.SetDueForTest()
	before := time.Now()
	if state.ScheduleDebounced(nil) {
		t.Fatal("ScheduleDebounced returned true, want false")
	}
	deadline := state.PendingUntil()
	if deadline == nil || !deadline.After(before) {
		t.Fatalf("deadline = %v, before = %v", deadline, before)
	}
}

func TestTranscriptReflowFirstObservedWidthMarksBaseline(t *testing.T) {
	var state TranscriptReflowState
	change := state.NoteWidth(80)
	if !change.Initialized || change.Changed {
		t.Fatalf("change = %#v", change)
	}
	if state.lastObservedWidth == nil || *state.lastObservedWidth != 80 || state.lastReflowWidth == nil || *state.lastReflowWidth != 80 {
		t.Fatalf("state = %#v", state)
	}
	if state.ReflowNeededForWidth(80) {
		t.Fatal("width 80 should not need reflow after baseline")
	}
}

func TestTranscriptReflowRecordsActualRebuildWidth(t *testing.T) {
	var state TranscriptReflowState
	state.NoteWidth(80)
	if !state.MarkReflowedWidth(100) {
		t.Fatal("first mark should report changed")
	}
	if state.lastObservedWidth == nil || *state.lastObservedWidth != 80 || state.lastReflowWidth == nil || *state.lastReflowWidth != 100 {
		t.Fatalf("state = %#v", state)
	}
	if state.MarkReflowedWidth(100) {
		t.Fatal("same width should report unchanged")
	}
}

func TestTranscriptReflowNeededComparesActualRebuildWidth(t *testing.T) {
	var state TranscriptReflowState
	state.NoteWidth(80)
	state.MarkReflowedWidth(90)
	state.NoteWidth(100)
	if !state.ReflowNeededForWidth(100) {
		t.Fatal("width 100 should need reflow because last actual rebuild was 90")
	}
	target := 100
	state.ScheduleDebounced(&target)
	if state.ReflowNeededForWidth(100) {
		t.Fatal("pending target should prevent repeated reschedule")
	}
	state.ClearPendingReflow()
	if !state.ReflowNeededForWidth(100) {
		t.Fatal("clearing pending target should allow reschedule")
	}
}

func TestTranscriptReflowStreamFinishFlagsDrain(t *testing.T) {
	var state TranscriptReflowState
	state.MarkResizeRequestedDuringStream()
	if !state.TakeStreamFinishReflowNeeded() {
		t.Fatal("resize during stream should require finish reflow")
	}
	if state.TakeStreamFinishReflowNeeded() {
		t.Fatal("finish reflow flag should drain")
	}
	state.MarkRanDuringStream()
	if !state.TakeStreamFinishReflowNeeded() {
		t.Fatal("ran during stream should require finish reflow")
	}
	state.MarkRanDuringStream()
	state.MarkResizeRequestedDuringStream()
	state.ClearStreamFlags()
	if state.TakeStreamFinishReflowNeeded() {
		t.Fatal("ClearStreamFlags should clear both flags")
	}
}

func TestTranscriptReflowClearResetsStreamFlags(t *testing.T) {
	var state TranscriptReflowState
	state.NoteWidth(80)
	state.ScheduleImmediate()
	state.MarkRanDuringStream()
	state.MarkResizeRequestedDuringStream()
	state.Clear()
	if state.HasPendingReflow() || state.TakeStreamFinishReflowNeeded() || state.lastObservedWidth != nil {
		t.Fatalf("state after clear = %#v", state)
	}
}
