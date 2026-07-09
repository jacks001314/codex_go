package chatwidget

import (
	"reflect"
	"testing"
)

func TestCommandLifecycleUnifiedExecProcessDisplayChunksAndFooterMatchRust(t *testing.T) {
	var state CommandLifecycleState

	state.TrackUnifiedExecProcessBegin("call-1", "proc-1", `bash -lc "go test ./..."`)

	if len(state.UnifiedExecProcesses) != 1 {
		t.Fatalf("processes = %#v", state.UnifiedExecProcesses)
	}
	process := state.UnifiedExecProcesses[0]
	if process.Key != "proc-1" || process.CallID != "call-1" || process.CommandDisplay != "go test ./..." {
		t.Fatalf("process = %#v", process)
	}
	if got := state.FooterCommands(); !reflect.DeepEqual(got, []string{"go test ./..."}) {
		t.Fatalf("footer commands = %#v", got)
	}

	state.TrackUnifiedExecOutputChunk("call-1", "one\n\n two \r\nthree\nfour", 3)
	if got := state.UnifiedExecProcesses[0].RecentChunks; !reflect.DeepEqual(got, []string{" two", "three", "four"}) {
		t.Fatalf("recent chunks = %#v", got)
	}

	if state.TrackUnifiedExecProcessEnd("call-1", "") {
		t.Fatalf("end without process id should not remove process keyed by process id")
	}
	if !state.TrackUnifiedExecProcessEnd("call-1", "proc-1") {
		t.Fatalf("end by process id did not remove process")
	}
	if len(state.UnifiedExecProcesses) != 0 {
		t.Fatalf("processes after end = %#v", state.UnifiedExecProcesses)
	}
}

func TestCommandLifecycleTerminalInteractionWaitStreakMatchesRust(t *testing.T) {
	var state CommandLifecycleState
	state.TrackUnifiedExecProcessBegin("call-1", "proc-1", `bash -lc "python server.py"`)

	wait := state.OnTerminalInteraction("proc-1", "", true)
	if !wait.WaitingStatus || wait.Status != "Waiting for background terminal" || wait.StatusDetails != "python server.py" || !wait.InterruptHintVisible || !wait.Redraw {
		t.Fatalf("wait result = %#v", wait)
	}
	if state.UnifiedExecWait == nil || state.UnifiedExecWait.ProcessID != "proc-1" || state.UnifiedExecWait.CommandDisplay != "python server.py" {
		t.Fatalf("wait streak = %#v", state.UnifiedExecWait)
	}

	again := state.OnTerminalInteraction("proc-1", "", true)
	if again.FlushedWait != nil || state.UnifiedExecWait == nil || state.UnifiedExecWait.ProcessID != "proc-1" {
		t.Fatalf("same-process wait should not flush: result=%#v wait=%#v", again, state.UnifiedExecWait)
	}

	interaction := state.OnTerminalInteraction("proc-1", "q", true)
	if interaction.FlushedWait == nil || interaction.FlushedWait.CommandDisplay != "python server.py" {
		t.Fatalf("stdin should flush wait first: %#v", interaction)
	}
	if interaction.InsertedInteraction == nil || interaction.InsertedInteraction.Stdin != "q" || interaction.InsertedInteraction.CommandDisplay != "python server.py" {
		t.Fatalf("inserted interaction = %#v", interaction.InsertedInteraction)
	}
	if state.UnifiedExecWait != nil {
		t.Fatalf("wait streak not cleared: %#v", state.UnifiedExecWait)
	}
}

func TestCommandLifecycleTerminalInteractionSwitchFlushesWaitMatchRust(t *testing.T) {
	var state CommandLifecycleState
	state.TrackUnifiedExecProcessBegin("call-1", "proc-1", "server one")
	state.TrackUnifiedExecProcessBegin("call-2", "proc-2", "server two")

	state.OnTerminalInteraction("proc-1", "", true)
	result := state.OnTerminalInteraction("proc-2", "", true)

	if result.FlushedWait == nil || result.FlushedWait.ProcessID != "proc-1" || result.FlushedWait.CommandDisplay != "server one" {
		t.Fatalf("switch wait flush = %#v", result.FlushedWait)
	}
	if !result.NeedsFinalMessageSeparator {
		t.Fatalf("switch wait should request final separator")
	}
	if state.UnifiedExecWait == nil || state.UnifiedExecWait.ProcessID != "proc-2" || state.UnifiedExecWait.CommandDisplay != "server two" {
		t.Fatalf("new wait streak = %#v", state.UnifiedExecWait)
	}
}

func TestCommandLifecycleTerminalInteractionWhitespaceInputMatchesRust(t *testing.T) {
	var state CommandLifecycleState
	state.TrackUnifiedExecProcessBegin("call-1", "proc-1", "server")

	result := state.OnTerminalInteraction("proc-1", "   ", true)
	if result.WaitingStatus {
		t.Fatalf("whitespace stdin should be an interaction, got wait: %#v", result)
	}
	if result.InsertedInteraction == nil || result.InsertedInteraction.Stdin != "   " {
		t.Fatalf("whitespace interaction = %#v", result.InsertedInteraction)
	}
}

func TestCommandLifecyclePreservesProcessAndCallIDTextMatchRust(t *testing.T) {
	var state CommandLifecycleState
	state.TrackUnifiedExecProcessBegin(" call-1 ", " proc-1 ", "server")
	if len(state.UnifiedExecProcesses) != 1 || state.UnifiedExecProcesses[0].Key != " proc-1 " || state.UnifiedExecProcesses[0].CallID != " call-1 " {
		t.Fatalf("process id text was not preserved: %#v", state.UnifiedExecProcesses)
	}
	if state.TrackUnifiedExecProcessEnd("call-1", "proc-1") {
		t.Fatalf("trimmed ids should not match preserved process")
	}
	if !state.TrackUnifiedExecProcessEnd(" call-1 ", " proc-1 ") {
		t.Fatalf("exact ids should remove preserved process")
	}
}

func TestCommandLifecycleDuplicateUnifiedWaitAndCompletionMatchRust(t *testing.T) {
	var state CommandLifecycleState
	first := state.RecordCommandExecutionStarted("call-1", []string{"bash", "-lc", "python server.py"}, []string{"bash"}, ExecCommandSourceUnifiedExecInteraction)
	if !first.Recorded || first.Suppressed {
		t.Fatalf("first start = %#v", first)
	}

	second := state.RecordCommandExecutionStarted("call-2", []string{"bash", "-lc", "python server.py"}, []string{"bash"}, ExecCommandSourceUnifiedExecInteraction)
	if !second.Recorded || !second.Suppressed {
		t.Fatalf("second duplicate start = %#v", second)
	}

	completed := state.RecordCommandExecutionCompleted("call-2")
	if !completed.KnownRunning || !completed.Suppressed || completed.HadWorkActivity {
		t.Fatalf("suppressed completion = %#v", completed)
	}

	userShell := state.RecordCommandExecutionStarted("shell-1", []string{"echo", "hi"}, nil, ExecCommandSourceUserShell)
	if !userShell.Recorded || userShell.Suppressed {
		t.Fatalf("user shell start = %#v", userShell)
	}
	completed = state.RecordCommandExecutionCompleted("shell-1")
	if !completed.HadWorkActivity || !completed.MaybeSendNextQueuedInput {
		t.Fatalf("user shell completion = %#v", completed)
	}
}

func TestCommandLifecycleCommandDisplayParsingMatchesRustCore(t *testing.T) {
	if got := UnifiedExecCommandDisplay(`bash -lc "go test ./internal/tui"`); got != "go test ./internal/tui" {
		t.Fatalf("bash display = %q", got)
	}
	if got := UnifiedExecCommandDisplay(`C:\tools\bash -lc "npm test"`); got != "npm test" {
		t.Fatalf("path bash display = %q", got)
	}
	if got := UnifiedExecCommandDisplay(`git status --short`); got != "git status --short" {
		t.Fatalf("plain display = %q", got)
	}
	if got := SplitCommandString(`cmd "two words" 'three words'`); !reflect.DeepEqual(got, []string{"cmd", "two words", "three words"}) {
		t.Fatalf("split = %#v", got)
	}
}
