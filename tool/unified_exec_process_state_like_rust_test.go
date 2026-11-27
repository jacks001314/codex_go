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

// Rust #63896: termination can close stdin before the final read publishes the
// exit code, but hasExited stays false until status and code are available.
func TestUnifiedExecTerminatePublishesExitStatusWithCodeLikeRust(t *testing.T) {
	process := newStateTestProcess()
	if process.hasExited() {
		t.Fatal("fresh process reported exited")
	}
	if err := process.terminate(); err != nil {
		t.Fatalf("terminate() error = %v", err)
	}
	if process.hasExited() {
		t.Fatal("terminated process reported exited before its exit code was known")
	}
	if err := process.writeStdin("input"); !errors.Is(err, ErrUnifiedExecStdinClosed) {
		t.Fatalf("writeStdin() error = %v, want stdin closed", err)
	}
	exitCode := 7
	process.finishWithExit(nil, &exitCode)
	if !process.hasExited() || process.exitCode != 7 {
		t.Fatalf("final state = exited:%t code:%d", process.hasExited(), process.exitCode)
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
	if process.hasExited() {
		t.Fatal("rejected stdin reported exited before its exit code was known")
	}
	exitCode := 3
	process.finishWithExit(nil, &exitCode)
	if !process.hasExited() || process.exitCode != 3 {
		t.Fatalf("state after rejected stdin = exited:%t code:%d, want true/3", process.hasExited(), process.exitCode)
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
