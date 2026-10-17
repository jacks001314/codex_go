package tea

import (
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/protocol"
	codextui "codex_go/tui"
)

// TestProgramStatusTracksLiveResultsLikeRust mirrors Rust #54114
// chatwidget/tests/program_status.rs: live turns, blocking approvals,
// completion, interruption, and failure each map to the reported state.
func TestProgramStatusTracksLiveResultsLikeRust(t *testing.T) {
	model := NewModel(nil, Options{})
	statuses := []codextui.ProgramStatus{model.desiredProgramStatus()}

	model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{Type: "turn.started"}})
	statuses = append(statuses, model.desiredProgramStatus())

	model.Update(ApprovalRequestMsg{ID: "status-approval", Command: "echo hello"})
	statuses = append(statuses, model.desiredProgramStatus())

	model.modal = nil
	statuses = append(statuses, model.desiredProgramStatus())

	model.Update(TurnCompletedMsg{})
	statuses = append(statuses, model.desiredProgramStatus())

	// A key press acknowledges a retained result.
	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune("a")})
	statuses = append(statuses, model.desiredProgramStatus())

	model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{Type: "turn.started"}})
	model.Update(TurnInterruptedMsg{})
	statuses = append(statuses, model.desiredProgramStatus())

	model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{Type: "turn.started"}})
	model.Update(TurnCompletedMsg{Err: errTestTurnFailed{}})
	statuses = append(statuses, model.desiredProgramStatus())

	model.Update(bubbletea.KeyMsg{Type: bubbletea.KeyRunes, Runes: []rune("a")})
	statuses = append(statuses, model.desiredProgramStatus())

	want := []codextui.ProgramStatus{
		{State: codextui.ProgramStatusIdle},
		{State: codextui.ProgramStatusWorking},
		codextui.BlockedProgramStatus(codextui.ProgramStatusKindPermission),
		{State: codextui.ProgramStatusWorking},
		{State: codextui.ProgramStatusDone},
		{State: codextui.ProgramStatusIdle},
		{State: codextui.ProgramStatusIdle},
		{State: codextui.ProgramStatusError},
		{State: codextui.ProgramStatusIdle},
	}
	if len(statuses) != len(want) {
		t.Fatalf("statuses = %#v", statuses)
	}
	for i := range want {
		if statuses[i] != want[i] {
			t.Fatalf("status[%d] = %#v, want %#v (all: %#v)", i, statuses[i], want[i], statuses)
		}
	}
}

// TestProgramStatusKeepsFollowUpWorkWorkingLikeRust mirrors the queued
// follow-up rule: work already submitted must not read as a finished turn.
func TestProgramStatusKeepsFollowUpWorkWorkingLikeRust(t *testing.T) {
	model := NewModel(nil, Options{})
	model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{Type: "turn.started"}})
	model.Update(TurnCompletedMsg{})
	if got := model.desiredProgramStatus(); got.State != codextui.ProgramStatusDone {
		t.Fatalf("completed status = %#v, want done", got)
	}
	model.pendingSteers = append(model.pendingSteers, pendingSteerSubmission{ID: "follow-up"})
	if got := model.desiredProgramStatus(); got.State != codextui.ProgramStatusWorking {
		t.Fatalf("pending follow-up status = %#v, want working", got)
	}
}

// TestProgramStatusAcknowledgesOnlyOnDeliberateInputLikeRust pins #54114's
// input rule: pointer movement does not clear a result.
func TestProgramStatusAcknowledgesOnlyOnDeliberateInputLikeRust(t *testing.T) {
	model := NewModel(nil, Options{})
	model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{Type: "turn.started"}})
	model.Update(TurnCompletedMsg{})
	model.Update(bubbletea.MouseMsg{Action: bubbletea.MouseActionMotion})
	if got := model.desiredProgramStatus(); got.State != codextui.ProgramStatusDone {
		t.Fatalf("motion acknowledged the result: %#v", got)
	}
	model.Update(bubbletea.MouseMsg{Action: bubbletea.MouseActionPress})
	if got := model.desiredProgramStatus(); got.State != codextui.ProgramStatusIdle {
		t.Fatalf("press did not acknowledge the result: %#v", got)
	}
}

// TestProgramStatusEmitsThroughTerminalLikeRust checks that the model writes
// the OSC 7501 sequence through the configured writer.
func TestProgramStatusEmitsThroughTerminalLikeRust(t *testing.T) {
	restore := codextui.SetTerminalProgramStatusSupportForTest(true, false)
	defer restore()
	var writes []string
	model := NewModel(nil, Options{
		OnWriteProgramStatus: func(sequence string) bubbletea.Cmd {
			return func() bubbletea.Msg {
				writes = append(writes, sequence)
				return nil
			}
		},
	})
	_, cmd := model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{Type: "turn.started"}})
	runTeaCmd(t, model, cmd)
	if len(writes) == 0 {
		t.Fatal("no program status was written")
	}
	if writes[len(writes)-1] != "\x1b]7501;state=working:app=codex\x1b\\" {
		t.Fatalf("writes = %#v", writes)
	}
}

type errTestTurnFailed struct{}

func (errTestTurnFailed) Error() string { return "turn failed" }
