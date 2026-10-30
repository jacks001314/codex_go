package tool

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"codex_go/sandbox"
)

// Rust #47568 carries the executor-observed backend from the shell result into
// the tool output so app-server analytics can report it. The value is internal
// and must not be serialized on the model-visible output.
func TestShellExecutorCarriesObservedSandboxBackendLikeRust(t *testing.T) {
	seccomp := sandbox.SandboxTypeLinuxSeccomp
	runner := &fakeShellRunner{result: &ShellResult{ExitCode: 0, Stdout: "ok\n", SandboxType: &seccomp}}
	executor := NewShellExecutor(&ShellExecutorOptions{
		Runner: runner,
		Shell:  &Shell{Type: ShellBash, Path: "/bin/sh"},
		Validation: ShellValidationOptions{
			CWD: t.TempDir(),
		},
	})
	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:   "call-sandbox-backend",
		ToolName: PlainName(DefaultExecCommandToolName),
		Payload:  Payload{Kind: PayloadFunction, Arguments: `{"cmd":"true"}`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output.SandboxType == nil || *output.SandboxType != sandbox.SandboxTypeLinuxSeccomp {
		t.Fatalf("Output.SandboxType = %#v, want linuxSeccomp", output.SandboxType)
	}
	payload, err := json.Marshal(output)
	if err != nil {
		t.Fatalf("Marshal(output) error = %v", err)
	}
	for _, forbidden := range []string{"SandboxType", "sandboxType", "sandbox_type"} {
		if strings.Contains(string(payload), forbidden) {
			t.Fatalf("serialized tool output leaked %q: %s", forbidden, payload)
		}
	}
}

func TestPrepareUnifiedExecShellRequestMarksExplicitNoneLikeRust(t *testing.T) {
	prepared, err := prepareUnifiedExecShellRequest(&ShellRequest{
		Command: []string{"sh", "-c", "true"},
		CWD:     t.TempDir(),
	})
	if err != nil {
		t.Fatalf("prepareUnifiedExecShellRequest() error = %v", err)
	}
	if prepared.ProcessSandboxType == nil || *prepared.ProcessSandboxType != sandbox.SandboxTypeNone {
		t.Fatalf("ProcessSandboxType = %#v, want explicit none", prepared.ProcessSandboxType)
	}
}

func TestPrepareUnifiedExecShellRequestKeepsRemoteBackendUnknownLikeRust(t *testing.T) {
	prepared, err := prepareUnifiedExecShellRequest(&ShellRequest{
		Command:              []string{"sh", "-c", "true"},
		CWD:                  t.TempDir(),
		UnifiedExecRemoteURL: "ws://executor.invalid",
	})
	if err != nil {
		t.Fatalf("prepareUnifiedExecShellRequest() error = %v", err)
	}
	if prepared.ProcessSandboxType != nil {
		t.Fatalf("remote ProcessSandboxType = %#v, want nil until the executor reports it", prepared.ProcessSandboxType)
	}
}

func TestUnifiedExecProcessReportsSandboxBackendOnCompletionLikeRust(t *testing.T) {
	manager := NewUnifiedExecManager()
	defer manager.Close()
	seccomp := sandbox.SandboxTypeLinuxSeccomp
	var events []UnifiedExecEvent
	result, err := manager.Exec(context.Background(), &ShellRequest{
		Command:            unifiedExecHelperCommand("immediate"),
		CWD:                t.TempDir(),
		YieldTimeMS:        1_000,
		ProcessSandboxType: &seccomp,
		UnifiedExecEventSink: func(event UnifiedExecEvent) {
			events = append(events, event)
		},
	}, "sandbox-backend-call")
	if err != nil {
		t.Fatalf("Exec() error = %v", err)
	}
	if result.SandboxType == nil || *result.SandboxType != sandbox.SandboxTypeLinuxSeccomp {
		t.Fatalf("result.SandboxType = %#v, want linuxSeccomp", result.SandboxType)
	}
	for _, event := range events {
		if event.Kind != UnifiedExecEventEnd {
			continue
		}
		if event.SandboxType == nil || *event.SandboxType != sandbox.SandboxTypeLinuxSeccomp {
			t.Fatalf("end event SandboxType = %#v, want linuxSeccomp", event.SandboxType)
		}
		return
	}
	t.Fatalf("no terminal unified-exec event in %#v", events)
}
