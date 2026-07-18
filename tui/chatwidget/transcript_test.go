package chatwidget

import "testing"

func TestTranscriptStateActiveCellRevisionWraps(t *testing.T) {
	state := TranscriptState{ActiveCellRevision: ^uint64(0)}

	state.BumpActiveCellRevision()

	if state.ActiveCellRevision != 0 {
		t.Fatalf("ActiveCellRevision = %d, want wrapped 0", state.ActiveCellRevision)
	}
}

func TestTranscriptStateCopyHistoryTracksLatestVisibleTurnAndRollback(t *testing.T) {
	var state TranscriptState
	state.RecordVisibleUserTurn()
	state.RecordAgentMarkdown("first")
	state.RecordAgentMarkdown("first updated")
	state.RecordVisibleUserTurn()
	state.RecordAgentMarkdown("second")

	if len(state.AgentTurnMarkdowns) != 2 {
		t.Fatalf("AgentTurnMarkdowns len = %d, want 2", len(state.AgentTurnMarkdowns))
	}
	if state.AgentTurnMarkdowns[0].Markdown != "first updated" {
		t.Fatalf("same-turn markdown was not replaced: %#v", state.AgentTurnMarkdowns[0])
	}

	state.TruncateCopyHistoryToUserTurnCount(1)

	if state.VisibleUserTurnCount != 1 {
		t.Fatalf("VisibleUserTurnCount = %d, want 1", state.VisibleUserTurnCount)
	}
	if state.LastAssistantMarkdown != "first updated" {
		t.Fatalf("LastAssistantMarkdown = %q, want first updated", state.LastAssistantMarkdown)
	}
	if state.CopyHistoryEvictedByRollback {
		t.Fatalf("CopyHistoryEvictedByRollback = true, want false while retained history exists")
	}
	if state.SawCopySourceThisTurn {
		t.Fatalf("SawCopySourceThisTurn = true after rollback, want false")
	}
}

func TestTranscriptStateCopyHistoryCapsAndEviction(t *testing.T) {
	var state TranscriptState
	for i := 1; i <= 35; i++ {
		state.RecordVisibleUserTurn()
		state.RecordAgentMarkdown(string(rune('a' + i%26)))
	}

	if len(state.AgentTurnMarkdowns) != MaxAgentCopyHistory {
		t.Fatalf("AgentTurnMarkdowns len = %d, want %d", len(state.AgentTurnMarkdowns), MaxAgentCopyHistory)
	}
	if got := state.AgentTurnMarkdowns[0].UserTurnCount; got != 4 {
		t.Fatalf("first retained user turn = %d, want 4", got)
	}
	if got := state.AgentTurnMarkdowns[len(state.AgentTurnMarkdowns)-1].UserTurnCount; got != 35 {
		t.Fatalf("last retained user turn = %d, want 35", got)
	}

	state.TruncateCopyHistoryToUserTurnCount(0)

	if state.LastAssistantMarkdown != "" {
		t.Fatalf("LastAssistantMarkdown = %q, want empty", state.LastAssistantMarkdown)
	}
	if len(state.AgentTurnMarkdowns) != 0 {
		t.Fatalf("AgentTurnMarkdowns len = %d, want 0", len(state.AgentTurnMarkdowns))
	}
	if !state.CopyHistoryEvictedByRollback {
		t.Fatalf("CopyHistoryEvictedByRollback = false, want true")
	}
}

func TestTranscriptStateResetTurnFlagsPreservesSeparatorAndPlanProgress(t *testing.T) {
	progress := &PlanProgress{Completed: 1, Total: 3}
	state := TranscriptState{
		SawCopySourceThisTurn:      true,
		NeedsFinalMessageSeparator: true,
		HadWorkActivity:            true,
		SawPlanUpdateThisTurn:      true,
		SawPlanItemThisTurn:        true,
		LatestProposedPlanMarkdown: "plan",
		LastPlanProgress:           progress,
		PlanDeltaBuffer:            "delta",
		PlanItemActive:             true,
	}

	state.ResetTurnFlags()

	if state.SawCopySourceThisTurn || state.HadWorkActivity || state.SawPlanUpdateThisTurn || state.SawPlanItemThisTurn || state.PlanItemActive {
		t.Fatalf("turn-scoped booleans were not reset: %#v", state)
	}
	if state.LatestProposedPlanMarkdown != "" || state.PlanDeltaBuffer != "" {
		t.Fatalf("turn-scoped plan text was not reset: %#v", state)
	}
	if !state.NeedsFinalMessageSeparator {
		t.Fatalf("NeedsFinalMessageSeparator was cleared, want preserved")
	}
	if state.LastPlanProgress != progress {
		t.Fatalf("LastPlanProgress was not preserved")
	}
}

func TestTranscriptStateRecordPlanProgressClampAndClear(t *testing.T) {
	var state TranscriptState

	state.RecordPlanProgress(5, 3)

	if state.LastPlanProgress == nil {
		t.Fatalf("LastPlanProgress nil, want value")
	}
	if state.LastPlanProgress.Completed != 3 || state.LastPlanProgress.Total != 3 {
		t.Fatalf("LastPlanProgress = %#v, want 3/3", state.LastPlanProgress)
	}
	if !state.SawPlanUpdateThisTurn {
		t.Fatalf("SawPlanUpdateThisTurn = false, want true")
	}

	state.RecordPlanProgress(-1, 0)

	if state.LastPlanProgress != nil {
		t.Fatalf("LastPlanProgress = %#v, want nil after clear", state.LastPlanProgress)
	}
}

func TestTranscriptStateLastAssistantMarkdownForCopyTrims(t *testing.T) {
	state := TranscriptState{LastAssistantMarkdown: "  answer\n"}

	got, ok := state.LastAssistantMarkdownForCopy()

	if !ok || got != "answer" {
		t.Fatalf("LastAssistantMarkdownForCopy = %q ok=%v, want answer true", got, ok)
	}
}
