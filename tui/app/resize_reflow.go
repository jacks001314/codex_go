package app

const (
	TranscriptCellAgentMessage        = "agent_message"
	TranscriptCellAgentMarkdown       = "agent_markdown"
	TranscriptCellProposedPlanStream  = "proposed_plan_stream"
	TranscriptCellProposedPlanFinal   = "proposed_plan_final"
	TranscriptCellUserMessage         = "user_message"
	TranscriptCellGeneric             = "generic"
	defaultResizeReflowUnlimitedRows  = 0
	defaultTranscriptSeparatorLine    = ""
	defaultTranscriptReflowNoRowLimit = 0
)

type ResizeReflowRequest struct {
	Width  int
	Height int
}

type TranscriptCell struct {
	Kind               string
	Lines              []string
	StreamContinuation bool
	Source             string
	CWD                string
}

type InitialHistoryReplayBuffer struct {
	RetainedLines            []string
	RenderFromTranscriptTail bool
}

type HistoryInsertDecision struct {
	Lines                  []string
	Defer                  bool
	HasEmittedHistoryLines bool
}

type ReflowRenderResult struct {
	Lines                  []string
	HasEmittedHistoryLines bool
}

type InitialHistoryReplayFinishDecision struct {
	InsertLines              []string
	RenderFromTranscriptTail bool
}

type ResizeReflowTerminalSize struct {
	Width  int
	Height int
}

type ResizeReflowSizeChangeDecision struct {
	WidthInitialized                   bool
	WidthChanged                       bool
	HeightChanged                      bool
	ReflowNeeded                       bool
	ShouldRebuildTranscript            bool
	NotifyChatWidgetResize             bool
	MarkResizeRequestedDuringStream    bool
	ScheduleResizeReflow               bool
	ResizeReflowTargetWidth            *int
	RequestFrame                       bool
	ClearPendingHistoryBeforePreRender bool
}

type ResizeReflowTracker struct {
	LastWidth int
	HasWidth  bool
}

type ClearTerminalForResizeReplayDecision struct {
	ClearVisibleScreenOnly          bool
	ClearScrollbackAndVisibleScreen bool
	ResetViewportY                  bool
}

type ResizeReflowRunDecision struct {
	RunNow                bool
	RequestFrame          bool
	ClearPendingReflow    bool
	MarkReflowedWidth     bool
	MarkRanDuringStream   bool
	ClearPendingHistory   bool
	ResetEmissionState    bool
	ClearTerminalHistory  bool
	InsertReflowedHistory bool
}

func TrailingRunStart(cells []TranscriptCell, kind string) int {
	end := len(cells)
	start := end
	for start > 0 && cells[start-1].StreamContinuation && cells[start-1].Kind == kind {
		start--
	}
	if start > 0 && cells[start-1].Kind == kind && !cells[start-1].StreamContinuation {
		start--
	}
	return start
}

func DisplayLinesForHistoryInsert(cell TranscriptCell, hasEmittedHistoryLines bool, overlayActive bool) HistoryInsertDecision {
	lines := append([]string(nil), cell.Lines...)
	if len(lines) == 0 {
		return HistoryInsertDecision{HasEmittedHistoryLines: hasEmittedHistoryLines}
	}
	if !cell.StreamContinuation {
		if hasEmittedHistoryLines {
			lines = append([]string{defaultTranscriptSeparatorLine}, lines...)
		} else {
			hasEmittedHistoryLines = true
		}
	}
	return HistoryInsertDecision{
		Lines:                  lines,
		Defer:                  overlayActive,
		HasEmittedHistoryLines: hasEmittedHistoryLines,
	}
}

func BeginInitialHistoryReplayBufferDecision(overlayActive bool) bool {
	return !overlayActive
}

func BeginThreadSwitchHistoryReplayBufferDecision(maxRows int, overlayActive bool) (InitialHistoryReplayBuffer, bool) {
	if overlayActive || maxRows <= 0 {
		return InitialHistoryReplayBuffer{}, false
	}
	return InitialHistoryReplayBuffer{RenderFromTranscriptTail: true}, true
}

func FinishInitialHistoryReplayBufferDecision(buffer *InitialHistoryReplayBuffer) InitialHistoryReplayFinishDecision {
	if buffer == nil {
		return InitialHistoryReplayFinishDecision{}
	}
	if len(buffer.RetainedLines) == 0 && buffer.RenderFromTranscriptTail {
		return InitialHistoryReplayFinishDecision{RenderFromTranscriptTail: true}
	}
	return InitialHistoryReplayFinishDecision{InsertLines: append([]string(nil), buffer.RetainedLines...)}
}

func BufferInitialHistoryReplayDisplayLines(buffer *InitialHistoryReplayBuffer, display []string, maxRows int) {
	if buffer == nil || len(display) == 0 {
		return
	}
	buffer.RetainedLines = append(buffer.RetainedLines, display...)
	if maxRows <= defaultResizeReflowUnlimitedRows {
		return
	}
	if len(buffer.RetainedLines) > maxRows {
		buffer.RetainedLines = append([]string(nil), buffer.RetainedLines[len(buffer.RetainedLines)-maxRows:]...)
	}
}

func RenderTranscriptLinesForReflow(cells []TranscriptCell, maxRows int) ReflowRenderResult {
	cellDisplays := []TranscriptCell{}
	renderedRows := 0
	start := len(cells)

	for start > 0 {
		start--
		cell := cells[start]
		renderedRows += len(cell.Lines)
		cellDisplays = append([]TranscriptCell{cell}, cellDisplays...)
		if maxRows > defaultTranscriptReflowNoRowLimit && renderedRows > maxRows {
			break
		}
	}

	for start > 0 && len(cellDisplays) > 0 && cellDisplays[0].StreamContinuation {
		start--
		cellDisplays = append([]TranscriptCell{cells[start]}, cellDisplays...)
	}

	hasEmitted := false
	lines := []string{}
	for _, cell := range cellDisplays {
		if len(cell.Lines) > 0 && !cell.StreamContinuation {
			if hasEmitted {
				lines = append(lines, defaultTranscriptSeparatorLine)
			} else {
				hasEmitted = true
			}
		}
		lines = append(lines, cell.Lines...)
	}
	if maxRows > defaultTranscriptReflowNoRowLimit && len(lines) > maxRows {
		lines = append([]string(nil), lines[len(lines)-maxRows:]...)
	}
	return ReflowRenderResult{
		Lines:                  lines,
		HasEmittedHistoryLines: len(lines) > 0,
	}
}

func ShouldMarkReflowAsStreamTime(activeAgentStream bool, activePlanStream bool, cells []TranscriptCell) bool {
	if activeAgentStream || activePlanStream {
		return true
	}
	return TrailingRunStart(cells, TranscriptCellAgentMessage) < len(cells) ||
		TrailingRunStart(cells, TranscriptCellProposedPlanStream) < len(cells)
}

func (t *ResizeReflowTracker) HandleDrawSizeChange(size ResizeReflowTerminalSize, lastKnown ResizeReflowTerminalSize, streamTime bool) ResizeReflowSizeChangeDecision {
	if t == nil {
		t = &ResizeReflowTracker{}
	}
	initialized := false
	widthChanged := false
	reflowNeeded := false
	if !t.HasWidth {
		initialized = true
		t.HasWidth = true
		t.LastWidth = size.Width
	} else if t.LastWidth != size.Width {
		widthChanged = true
		reflowNeeded = true
		t.LastWidth = size.Width
	}
	heightChanged := size.Height != lastKnown.Height
	shouldRebuild := reflowNeeded || heightChanged
	decision := ResizeReflowSizeChangeDecision{
		WidthInitialized:                   initialized,
		WidthChanged:                       widthChanged,
		HeightChanged:                      heightChanged,
		ReflowNeeded:                       reflowNeeded,
		ShouldRebuildTranscript:            shouldRebuild,
		NotifyChatWidgetResize:             initialized || widthChanged,
		MarkResizeRequestedDuringStream:    reflowNeeded && streamTime,
		ScheduleResizeReflow:               shouldRebuild,
		RequestFrame:                       shouldRebuild,
		ClearPendingHistoryBeforePreRender: shouldRebuild,
	}
	if reflowNeeded {
		target := size.Width
		decision.ResizeReflowTargetWidth = &target
	}
	return decision
}

func ClearTerminalForResizeReplayDecisionForState(altScreenActive bool, viewportY int) ClearTerminalForResizeReplayDecision {
	return ClearTerminalForResizeReplayDecision{
		ClearVisibleScreenOnly:          altScreenActive,
		ClearScrollbackAndVisibleScreen: !altScreenActive,
		ResetViewportY:                  viewportY > 0,
	}
}

func MaybeRunResizeReflowDecision(hasPending bool, due bool, transcriptCells []TranscriptCell, streamTime bool, reflowedLineCount int) ResizeReflowRunDecision {
	if !hasPending {
		return ResizeReflowRunDecision{}
	}
	if !due {
		return ResizeReflowRunDecision{RequestFrame: true}
	}
	ranDuringStream := len(transcriptCells) > 0 && streamTime
	return ResizeReflowRunDecision{
		RunNow:                true,
		ClearPendingReflow:    true,
		MarkReflowedWidth:     true,
		MarkRanDuringStream:   ranDuringStream,
		ClearPendingHistory:   true,
		ResetEmissionState:    len(transcriptCells) == 0,
		ClearTerminalHistory:  len(transcriptCells) > 0,
		InsertReflowedHistory: reflowedLineCount > 0,
	}
}
