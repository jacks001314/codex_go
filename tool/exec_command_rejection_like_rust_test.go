package tool

import (
	"context"
	"errors"
	"strings"
	"testing"

	"codex_go/sandbox"
)

// TestExecCommandFailureMessageIsBoundedLikeRust mirrors Rust #42113: a failed
// exec_command reports a bounded model-facing message and does not prepend the
// rejected command.
func TestExecCommandFailureMessageIsBoundedLikeRust(t *testing.T) {
	runner := &fakeShellRunner{err: errors.New(strings.Repeat("remote rejection ", 400))}
	executor := NewShellExecutor(&ShellExecutorOptions{
		Runner:   runner,
		Shell:    &Shell{Type: ShellBash, Path: "/bin/sh"},
		OneShot:  true,
		ToolName: PlainName(DefaultExecCommandToolName),
		Validation: ShellValidationOptions{
			ApprovalPolicy: sandbox.ApprovalOnRequest,
			CWD:            t.TempDir(),
		},
	})

	const command = "echo this-command-must-not-be-echoed"
	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-bounded-rejection",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"` + command + `"}`},
	})
	if output != nil {
		t.Fatalf("output = %#v, want nil on launch failure", output)
	}
	var callErr *FunctionCallError
	if !errors.As(err, &callErr) || !callErr.RespondsToModel() {
		t.Fatalf("error = %v, want a respond-to-model error", err)
	}
	message := callErr.ModelMessage()
	if !strings.HasPrefix(message, "exec_command failed: ") {
		t.Fatalf("message = %q, want exec_command prefix", message)
	}
	if !strings.Contains(message, "chars truncated") {
		t.Fatalf("message = %q, want a middle-truncation marker", message)
	}
	if len(message) > execCommandRejectionMaxBytes+64 {
		t.Fatalf("message length = %d, want bounded near %d bytes", len(message), execCommandRejectionMaxBytes)
	}
	if strings.Contains(message, command) {
		t.Fatalf("rejected command was echoed into the message: %q", message)
	}
}

// TestUnifiedExecProcessIDsAdvanceAfterReleaseLikeRust mirrors Rust #42113's
// deterministic-process-id regression: releasing an id must not make the next
// allocation reuse it.
func TestUnifiedExecProcessIDsAdvanceAfterReleaseLikeRust(t *testing.T) {
	manager := NewUnifiedExecManager()
	defer manager.Close()

	first, err := manager.allocateProcessID()
	if err != nil {
		t.Fatalf("allocate first id: %v", err)
	}
	manager.releaseProcessID(first)
	second, err := manager.allocateProcessID()
	if err != nil {
		t.Fatalf("allocate second id: %v", err)
	}
	manager.releaseProcessID(second)

	if second != first+1 {
		t.Fatalf("process ids = (%d, %d), want the released id to stay reserved", first, second)
	}
}
