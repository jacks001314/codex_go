package tea

import (
	"bytes"
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

func TestProgramStatusClearsOnExitLikeRust(t *testing.T) {
	restore := codextui.SetTerminalProgramStatusSupportForTest(true, true)
	defer restore()
	if sequence := codextui.SetTerminalProgramStatus(codextui.ProgramStatus{State: codextui.ProgramStatusWorking}, ""); sequence == "" {
		t.Fatal("initial program status was suppressed")
	}
	var output bytes.Buffer
	clearTerminalProgramStatusOnExit(&output)
	want := "\x1b]21337;status=;indicator=;status-color=;detail=\x07\x1b]7501;state=clear\x1b\\"
	if got := output.String(); got != want {
		t.Fatalf("exit clear = %q, want %q", got, want)
	}
	output.Reset()
	clearTerminalProgramStatusOnExit(&output)
	if output.Len() != 0 {
		t.Fatalf("second exit clear emitted %q", output.String())
	}
}

func TestClearTerminalProgramStatusBeforeExternalProgramLikeRust(t *testing.T) {
	restore := codextui.SetTerminalProgramStatusSupportForTest(true, false)
	defer restore()
	var writes []string
	model := NewModel(nil, Options{
		OnWriteProgramStatus: func(sequence string) bubbletea.Cmd {
			writes = append(writes, sequence)
			return nil
		},
		OnExternalEditor: func(string) bubbletea.Cmd {
			return func() bubbletea.Msg { return ExternalEditorFinishedMsg{} }
		},
	})
	if got := codextui.SetTerminalProgramStatus(codextui.ProgramStatus{State: codextui.ProgramStatusWorking}, ""); got == "" {
		t.Fatal("initial program status was suppressed")
	}
	if cmd := model.openExternalEditor(); cmd == nil {
		t.Fatal("external editor did not produce a command")
	}
	wantClear := "\x1b]7501;state=clear\x1b\\"
	if len(writes) != 1 || writes[0] != wantClear {
		t.Fatalf("clear writes = %#v, want %q", writes, wantClear)
	}
	if got := codextui.SetTerminalProgramStatus(codextui.ProgramStatus{State: codextui.ProgramStatusWorking}, ""); got == "" {
		t.Fatal("program status was not re-emitted after the external command")
	}
}

// TestITermSessionDetailUsesForegroundActivityLikeRust mirrors #52226: only a
// foreground Working state carries the status header, and the generic header is
// omitted.
func TestITermSessionDetailUsesForegroundActivityLikeRust(t *testing.T) {
	model := NewModel(nil, Options{})
	working := codextui.ProgramStatus{State: codextui.ProgramStatusWorking}
	if got := model.itermSessionDetail(working); got != "" {
		t.Fatalf("idle detail = %q", got)
	}

	model.Update(ThreadEventMsg{Event: protocol.ThreadEvent{Type: "turn.started"}})
	if got := model.itermSessionDetail(working); got != "" {
		t.Fatalf("generic Working detail = %q", got)
	}
	model.setWorkingStatusHeader("Reading source")
	if got := model.itermSessionDetail(working); got != "Reading source" {
		t.Fatalf("working detail = %q, want Reading source", got)
	}
	if got := model.itermSessionDetail(codextui.BlockedProgramStatus(codextui.ProgramStatusKindQuestion)); got != "" {
		t.Fatalf("waiting detail = %q", got)
	}

	model.Update(TurnCompletedMsg{})
	if got := model.itermSessionDetail(working); got != "" {
		t.Fatalf("background detail = %q", got)
	}
}

func TestProgramStatusReemitsAfterFocusAndResizeLikeRust(t *testing.T) {
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
	if len(writes) != 1 {
		t.Fatalf("initial writes = %#v", writes)
	}
	_, cmd = model.Update(bubbletea.FocusMsg{})
	runTeaCmd(t, model, cmd)
	if len(writes) != 2 {
		t.Fatalf("focus did not re-emit the status: %#v", writes)
	}
	_, cmd = model.Update(bubbletea.WindowSizeMsg{Width: 90, Height: 30})
	runTeaCmd(t, model, cmd)
	if len(writes) != 3 {
		t.Fatalf("resize did not re-emit the status: %#v", writes)
	}
}

type errTestTurnFailed struct{}

func (errTestTurnFailed) Error() string { return "turn failed" }
