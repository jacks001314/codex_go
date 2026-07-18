package app

type AgentMessageConsolidationState struct {
	LastMessage string
}

type AgentMessageConsolidationResult struct {
	Cells        []TranscriptCell
	Consolidated bool
	Start        int
	End          int
}

func ConsolidateAgentMessageRun(cells []TranscriptCell, source string, cwd string, deferred *TranscriptCell) AgentMessageConsolidationResult {
	out := append([]TranscriptCell(nil), cells...)
	if deferred != nil {
		out = append(out, *deferred)
	}

	end := len(out)
	start := TrailingRunStart(out, TranscriptCellAgentMessage)
	if start >= end {
		return AgentMessageConsolidationResult{
			Cells:        out,
			Consolidated: false,
			Start:        start,
			End:          end,
		}
	}

	consolidated := TranscriptCell{
		Kind:   TranscriptCellAgentMarkdown,
		Lines:  []string{source},
		Source: source,
		CWD:    cwd,
	}
	next := make([]TranscriptCell, 0, len(out)-(end-start)+1)
	next = append(next, out[:start]...)
	next = append(next, consolidated)
	next = append(next, out[end:]...)
	return AgentMessageConsolidationResult{
		Cells:        next,
		Consolidated: true,
		Start:        start,
		End:          end,
	}
}
