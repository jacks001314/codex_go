package tool

import (
	"context"
	"strings"
	"testing"
)

func TestCodeModeExecUsesCustomPayloadAndNormalizesNestedOutput(t *testing.T) {
	shell := NewShellExecutor(&ShellExecutorOptions{Runner: &recordingShellRunner{output: "ALPHA\n"}, Validation: ShellValidationOptions{CWD: t.TempDir()}})
	executor := NewCodeModeExecExecutor(shell)
	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:  "call-exec",
		Payload: Payload{Kind: PayloadCustom, Input: `const r = await tools.exec_command({"cmd":"printf 'ALPHA\\n'"}); text(r.output);`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if output.Body != "ALPHA" || !output.Success {
		t.Fatalf("output = %#v", output)
	}
	commands, ok := output.Data["nested_commands"].([]string)
	if !ok || len(commands) != 1 || !strings.Contains(commands[0], "ALPHA") {
		t.Fatalf("nested commands = %#v", output.Data["nested_commands"])
	}
}

type recordingShellRunner struct{ output string }

func (r *recordingShellRunner) Run(context.Context, *ShellRequest) (*ShellResult, error) {
	return &ShellResult{Stdout: r.output, ExitCode: 0, HasExitCode: true}, nil
}
