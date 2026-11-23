package tool

import (
	"errors"
	"testing"
)

func newStateTestProcess() *unifiedExecProcess {
	return &unifiedExecProcess{
		done:      make(chan struct{}),
		eventDone: make(chan struct{}),
	}
}

// Rust #62557: a confirmed termination marks the process exited without
// replacing the exit code the output collector later publishes.
func TestUnifiedExecTerminateMarksExitedAndPreservesExitCodeLikeRust(t *testing.T) {
	process := newStateTestProcess()
	if process.hasExited() {
		t.Fatal("fresh process reported exited")
	}
	if err := process.terminate(); err != nil {
		t.Fatalf("terminate() error = %v", err)
	}
	if !process.hasExited() {
		t.Fatal("terminated process did not report exited")
	}
	if err := process.writeStdin("input"); !errors.Is(err, ErrUnifiedExecStdinClosed) {
		t.Fatalf("writeStdin() error = %v, want stdin closed", err)
	}
	exitCode := 7
	process.finishWithExit(nil, &exitCode)
	if process.exitCode != 7 || !process.exited {
		t.Fatalf("final state = exited:%t code:%d", process.exited, process.exitCode)
	}
}

// Rust #62557: rejected stdin marks the process exited and keeps the exit code
// that the final read later records.
func TestUnifiedExecRejectedStdinKeepsReportedExitCodeLikeRust(t *testing.T) {
	process := newStateTestProcess()
	process.markProcessExited()
	if err := process.writeStdin("input"); !errors.Is(err, ErrUnifiedExecStdinClosed) {
		t.Fatalf("writeStdin() error = %v, want stdin closed", err)
	}
	if !process.hasExited() {
		t.Fatal("process with a rejected stdin did not report exited")
	}
	exitCode := 3
	process.finishWithExit(nil, &exitCode)
	if process.exitCode != 3 {
		t.Fatalf("exit code after rejected stdin = %d, want 3", process.exitCode)
	}
}

// Rust #62557: repeated failure paths keep the first failure message.
func TestUnifiedExecRepeatedFailuresKeepFirstMessageLikeRust(t *testing.T) {
	process := newStateTestProcess()
	first := errors.New("first failure")
	process.finishWithExit(first, nil)
	process.finishWithExit(errors.New("second failure"), nil)
	if process.waitErr != first {
		t.Fatalf("waitErr = %v, want the first failure", process.waitErr)
	}
	if process.exitCode != -1 {
		t.Fatalf("exitCode = %d, want the first failure's -1", process.exitCode)
	}
}
