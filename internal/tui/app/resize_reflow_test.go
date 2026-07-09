package app

import (
	"reflect"
	"testing"
)

func TestTrailingRunStartMatchesRustStreamingRun(t *testing.T) {
	cells := []TranscriptCell{
		{Kind: TranscriptCellUserMessage, Lines: []string{"user"}},
		{Kind: TranscriptCellAgentMessage, Lines: []string{"a"}},
		{Kind: TranscriptCellAgentMessage, Lines: []string{"b"}, StreamContinuation: true},
		{Kind: TranscriptCellAgentMessage, Lines: []string{"c"}, StreamContinuation: true},
	}
	if got := TrailingRunStart(cells, TranscriptCellAgentMessage); got != 1 {
		t.Fatalf("TrailingRunStart = %d, want 1", got)
	}

	orphanContinuation := []TranscriptCell{
		{Kind: TranscriptCellUserMessage, Lines: []string{"user"}},
		{Kind: TranscriptCellAgentMessage, Lines: []string{"tail"}, StreamContinuation: true},
	}
	if got := TrailingRunStart(orphanContinuation, TranscriptCellAgentMessage); got != 1 {
		t.Fatalf("orphan continuation start = %d, want 1", got)
	}

	noRun := []TranscriptCell{{Kind: TranscriptCellUserMessage, Lines: []string{"user"}}}
	if got := TrailingRunStart(noRun, TranscriptCellAgentMessage); got != len(noRun) {
		t.Fatalf("no-run start = %d, want %d", got, len(noRun))
	}
}

func TestBufferInitialHistoryReplayDisplayLinesKeepsTailRows(t *testing.T) {
	buffer := InitialHistoryReplayBuffer{}
	BufferInitialHistoryReplayDisplayLines(&buffer, []string{"1", "2", "3"}, 4)
	BufferInitialHistoryReplayDisplayLines(&buffer, []string{"4", "5"}, 4)

	want := []string{"2", "3", "4", "5"}
	if !reflect.DeepEqual(buffer.RetainedLines, want) {
		t.Fatalf("retained = %#v, want %#v", buffer.RetainedLines, want)
	}
}

func TestHistoryInsertAndInitialReplayBufferDecisionsMatchRust(t *testing.T) {
	first := DisplayLinesForHistoryInsert(TranscriptCell{Kind: TranscriptCellUserMessage, Lines: []string{"user"}}, false, false)
	if first.Defer || !first.HasEmittedHistoryLines || !reflect.DeepEqual(first.Lines, []string{"user"}) {
		t.Fatalf("first insert = %#v", first)
	}
	second := DisplayLinesForHistoryInsert(TranscriptCell{Kind: TranscriptCellAgentMarkdown, Lines: []string{"agent"}}, first.HasEmittedHistoryLines, true)
	if !second.Defer || !reflect.DeepEqual(second.Lines, []string{"", "agent"}) {
		t.Fatalf("second insert = %#v", second)
	}
	continuation := DisplayLinesForHistoryInsert(TranscriptCell{Kind: TranscriptCellAgentMessage, Lines: []string{"stream"}, StreamContinuation: true}, second.HasEmittedHistoryLines, false)
	if !reflect.DeepEqual(continuation.Lines, []string{"stream"}) {
		t.Fatalf("continuation insert = %#v", continuation)
	}

	if !BeginInitialHistoryReplayBufferDecision(false) || BeginInitialHistoryReplayBufferDecision(true) {
		t.Fatal("initial replay buffer overlay decision mismatch")
	}
	buffer, ok := BeginThreadSwitchHistoryReplayBufferDecision(10, false)
	if !ok || !buffer.RenderFromTranscriptTail {
		t.Fatalf("thread switch buffer = %#v ok=%v", buffer, ok)
	}
	if _, ok := BeginThreadSwitchHistoryReplayBufferDecision(0, false); ok {
		t.Fatal("thread switch buffer should require a row cap")
	}
	if _, ok := BeginThreadSwitchHistoryReplayBufferDecision(10, true); ok {
		t.Fatal("thread switch buffer should not start under overlay")
	}

	finishTail := FinishInitialHistoryReplayBufferDecision(&InitialHistoryReplayBuffer{RenderFromTranscriptTail: true})
	if !finishTail.RenderFromTranscriptTail || len(finishTail.InsertLines) != 0 {
		t.Fatalf("finish tail = %#v", finishTail)
	}
	finishLines := FinishInitialHistoryReplayBufferDecision(&InitialHistoryReplayBuffer{RetainedLines: []string{"a", "b"}})
	if finishLines.RenderFromTranscriptTail || !reflect.DeepEqual(finishLines.InsertLines, []string{"a", "b"}) {
		t.Fatalf("finish lines = %#v", finishLines)
	}
}

func TestRenderTranscriptLinesForReflowRestoresSeparatorsAndRowCap(t *testing.T) {
	cells := []TranscriptCell{
		{Kind: TranscriptCellUserMessage, Lines: []string{"user 1"}},
		{Kind: TranscriptCellAgentMarkdown, Lines: []string{"agent 1a", "agent 1b"}},
		{Kind: TranscriptCellUserMessage, Lines: []string{"user 2"}},
		{Kind: TranscriptCellAgentMessage, Lines: []string{"stream head"}},
		{Kind: TranscriptCellAgentMessage, Lines: []string{"stream tail"}, StreamContinuation: true},
	}

	got := RenderTranscriptLinesForReflow(cells, 4)
	want := []string{"", "user 2", "", "stream head", "stream tail"}
	if !reflect.DeepEqual(got.Lines, want[len(want)-4:]) {
		t.Fatalf("capped lines = %#v, want %#v", got.Lines, want[len(want)-4:])
	}
	if !got.HasEmittedHistoryLines {
		t.Fatalf("HasEmittedHistoryLines = false, want true")
	}

	uncapped := RenderTranscriptLinesForReflow(cells[:3], 0)
	wantUncapped := []string{"user 1", "", "agent 1a", "agent 1b", "", "user 2"}
	if !reflect.DeepEqual(uncapped.Lines, wantUncapped) {
		t.Fatalf("uncapped lines = %#v, want %#v", uncapped.Lines, wantUncapped)
	}
}

func TestRenderTranscriptLinesForReflowExtendsRetainedSuffixToRunHead(t *testing.T) {
	cells := []TranscriptCell{
		{Kind: TranscriptCellUserMessage, Lines: []string{"old"}},
		{Kind: TranscriptCellAgentMessage, Lines: []string{"head"}},
		{Kind: TranscriptCellAgentMessage, Lines: []string{"cont 1"}, StreamContinuation: true},
		{Kind: TranscriptCellAgentMessage, Lines: []string{"cont 2"}, StreamContinuation: true},
	}

	got := RenderTranscriptLinesForReflow(cells, 1)
	want := []string{"cont 2"}
	if !reflect.DeepEqual(got.Lines, want) {
		t.Fatalf("capped continuation lines = %#v, want %#v", got.Lines, want)
	}

	withoutFinalTrim := RenderTranscriptLinesForReflow(cells, 10)
	wantFullRun := []string{"old", "", "head", "cont 1", "cont 2"}
	if !reflect.DeepEqual(withoutFinalTrim.Lines, wantFullRun) {
		t.Fatalf("full run lines = %#v, want %#v", withoutFinalTrim.Lines, wantFullRun)
	}
}

func TestShouldMarkReflowAsStreamTime(t *testing.T) {
	if !ShouldMarkReflowAsStreamTime(true, false, nil) {
		t.Fatalf("active agent stream should mark stream-time")
	}
	if !ShouldMarkReflowAsStreamTime(false, true, nil) {
		t.Fatalf("active plan stream should mark stream-time")
	}
	cells := []TranscriptCell{
		{Kind: TranscriptCellUserMessage, Lines: []string{"user"}},
		{Kind: TranscriptCellAgentMessage, Lines: []string{"tail"}},
	}
	if !ShouldMarkReflowAsStreamTime(false, false, cells) {
		t.Fatalf("trailing agent message should mark stream-time")
	}
	if ShouldMarkReflowAsStreamTime(false, false, cells[:1]) {
		t.Fatalf("no active stream and no trailing stream cells should not mark stream-time")
	}
}

func TestResizeReflowSizeChangeDecisionMatchesRust(t *testing.T) {
	var tracker ResizeReflowTracker
	first := tracker.HandleDrawSizeChange(ResizeReflowTerminalSize{Width: 80, Height: 24}, ResizeReflowTerminalSize{Width: 0, Height: 24}, false)
	if !first.WidthInitialized || first.WidthChanged || first.ReflowNeeded || first.ScheduleResizeReflow || !first.NotifyChatWidgetResize {
		t.Fatalf("first size decision = %#v", first)
	}

	height := tracker.HandleDrawSizeChange(ResizeReflowTerminalSize{Width: 80, Height: 30}, ResizeReflowTerminalSize{Width: 80, Height: 24}, false)
	if !height.HeightChanged || !height.ShouldRebuildTranscript || height.ResizeReflowTargetWidth != nil || !height.ScheduleResizeReflow || !height.RequestFrame {
		t.Fatalf("height size decision = %#v", height)
	}

	width := tracker.HandleDrawSizeChange(ResizeReflowTerminalSize{Width: 100, Height: 30}, ResizeReflowTerminalSize{Width: 80, Height: 30}, true)
	if !width.WidthChanged || !width.ReflowNeeded || !width.ShouldRebuildTranscript || !width.MarkResizeRequestedDuringStream || width.ResizeReflowTargetWidth == nil || *width.ResizeReflowTargetWidth != 100 {
		t.Fatalf("width size decision = %#v", width)
	}
}

func TestClearAndRunResizeReflowDecisionsMatchRust(t *testing.T) {
	alt := ClearTerminalForResizeReplayDecisionForState(true, 4)
	if !alt.ClearVisibleScreenOnly || alt.ClearScrollbackAndVisibleScreen || !alt.ResetViewportY {
		t.Fatalf("alt clear decision = %#v", alt)
	}
	normal := ClearTerminalForResizeReplayDecisionForState(false, 0)
	if normal.ClearVisibleScreenOnly || !normal.ClearScrollbackAndVisibleScreen || normal.ResetViewportY {
		t.Fatalf("normal clear decision = %#v", normal)
	}

	wait := MaybeRunResizeReflowDecision(true, false, nil, false, 0)
	if wait.RunNow || !wait.RequestFrame {
		t.Fatalf("wait decision = %#v", wait)
	}
	run := MaybeRunResizeReflowDecision(true, true, []TranscriptCell{{Kind: TranscriptCellAgentMessage, Lines: []string{"stream"}}}, true, 1)
	if !run.RunNow || !run.ClearPendingReflow || !run.MarkReflowedWidth || !run.MarkRanDuringStream || !run.ClearPendingHistory || run.ResetEmissionState || !run.ClearTerminalHistory || !run.InsertReflowedHistory {
		t.Fatalf("run decision = %#v", run)
	}
	empty := MaybeRunResizeReflowDecision(true, true, nil, false, 0)
	if !empty.RunNow || !empty.ResetEmissionState || empty.ClearTerminalHistory || empty.InsertReflowedHistory {
		t.Fatalf("empty run decision = %#v", empty)
	}
}

func TestConsolidateAgentMessageRunMatchesRustReplacement(t *testing.T) {
	cells := []TranscriptCell{
		{Kind: TranscriptCellUserMessage, Lines: []string{"user"}},
		{Kind: TranscriptCellAgentMessage, Lines: []string{"a"}},
		{Kind: TranscriptCellAgentMessage, Lines: []string{"b"}, StreamContinuation: true},
	}
	result := ConsolidateAgentMessageRun(cells, "final **markdown**", "/work", nil)
	if !result.Consolidated || result.Start != 1 || result.End != 3 {
		t.Fatalf("result metadata = %#v", result)
	}
	if len(result.Cells) != 2 || result.Cells[1].Kind != TranscriptCellAgentMarkdown || result.Cells[1].Source != "final **markdown**" || result.Cells[1].CWD != "/work" {
		t.Fatalf("consolidated cells = %#v", result.Cells)
	}
}

func TestConsolidateAgentMessageRunIncludesDeferredCell(t *testing.T) {
	deferred := TranscriptCell{Kind: TranscriptCellAgentMessage, Lines: []string{"deferred"}}
	result := ConsolidateAgentMessageRun(nil, "final", "", &deferred)
	if !result.Consolidated || len(result.Cells) != 1 || result.Cells[0].Kind != TranscriptCellAgentMarkdown {
		t.Fatalf("deferred consolidation result = %#v", result)
	}
}

func TestConsolidateAgentMessageRunNoopsWithoutTrailingAgentRun(t *testing.T) {
	cells := []TranscriptCell{{Kind: TranscriptCellUserMessage, Lines: []string{"user"}}}
	result := ConsolidateAgentMessageRun(cells, "final", "", nil)
	if result.Consolidated {
		t.Fatalf("unexpected consolidation = %#v", result)
	}
	if !reflect.DeepEqual(result.Cells, cells) {
		t.Fatalf("cells changed = %#v", result.Cells)
	}
}
