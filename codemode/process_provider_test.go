package codemode

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"codex_go/tool"
)

const processHostHelperEnv = "CODEX_GO_PROCESS_HOST_TEST_HELPER"

func init() {
	var err error
	switch os.Getenv(processHostHelperEnv) {
	case "1":
		err = RunStdioHost(context.Background(), os.Stdin, os.Stdout)
	case "grpc":
		err = RunGrpcStdioHost(context.Background(), os.Stdin, os.Stdout)
	default:
		return
	}
	if err != nil {
		_, _ = io.WriteString(os.Stderr, err.Error())
		os.Exit(1)
	}
	os.Exit(0)
}

// codeModeBodyAfterHeader strips Rust's code-mode response header (status line,
// wall time, `Output:` separator) and returns the script output (#46288).
func codeModeBodyAfterHeader(t *testing.T, body string) string {
	t.Helper()
	lines := strings.SplitN(body, "\n", 3)
	if len(lines) != 3 || !strings.HasPrefix(lines[1], "Wall time ") || !strings.HasPrefix(lines[2], "Output:\n") {
		t.Fatalf("code-mode body has no header: %q", body)
	}
	return strings.TrimPrefix(lines[2], "Output:\n")
}

func TestProcessProviderExecutesNestedToolsOutsideClientProcess(t *testing.T) {
	hostProgram := copyProcessHostTestBinary(t)
	t.Setenv(processHostHelperEnv, "1")
	provider := NewProcessProvider(hostProgram)
	if err := provider.Availability(); err != nil {
		t.Fatalf("Availability() error = %v", err)
	}
	defer func() {
		if err := provider.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{
		Name: tool.PlainName("echo"), Description: "Echo a value",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}},
	}, func(_ context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		if !strings.Contains(invocation.Payload.Arguments, "cross-process") {
			t.Errorf("nested arguments = %q", invocation.Payload.Arguments)
		}
		return &tool.Output{CallID: invocation.CallID, ToolName: invocation.ToolName, Success: true, Body: "CROSS_PROCESS_OK"}, nil
	})); err != nil {
		t.Fatal(err)
	}
	execExecutor, _ := tool.NewCodeModeExecutorsWithProvider(registry, provider, false)
	notified := ""
	output, err := execExecutor.Execute(context.Background(), &tool.Invocation{
		CallID:  "process-call",
		Payload: tool.Payload{Kind: tool.PayloadCustom, Input: `const result = await tools.echo({value: "cross-process"}); notify("HOST_NOTIFY"); text(result.output);`},
		Context: map[string]any{"code_mode_notify": tool.CodeModeNotifyFunc(func(_ string, text string) { notified = text })},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output == nil || codeModeBodyAfterHeader(t, output.Body) != "CROSS_PROCESS_OK" {
		t.Fatalf("output = %#v", output)
	}
	if notified != "HOST_NOTIFY" {
		t.Fatalf("notify = %q", notified)
	}
}

// Mirrors Rust #52723: the opt-in feature runs the same code-mode host over a
// gRPC Transport stream on the child's stdio, with nested tool calls intact.
func TestGrpcProcessProviderExecutesNestedToolsOverStdio(t *testing.T) {
	hostProgram := copyProcessHostTestBinary(t)
	t.Setenv(processHostHelperEnv, "grpc")
	provider := NewGrpcProcessProvider(hostProgram)
	if err := provider.Availability(); err != nil {
		t.Fatalf("Availability() error = %v", err)
	}
	defer func() {
		if err := provider.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	}()

	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{
		Name: tool.PlainName("echo"), Description: "Echo a value",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}},
	}, func(_ context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		if !strings.Contains(invocation.Payload.Arguments, "grpc-stdio") {
			t.Errorf("nested arguments = %q", invocation.Payload.Arguments)
		}
		return &tool.Output{CallID: invocation.CallID, ToolName: invocation.ToolName, Success: true, Body: "GRPC_STDIO_OK"}, nil
	})); err != nil {
		t.Fatal(err)
	}
	execExecutor, _ := tool.NewCodeModeExecutorsWithProvider(registry, provider, false)
	notified := ""
	output, err := execExecutor.Execute(context.Background(), &tool.Invocation{
		CallID:  "grpc-process-call",
		Payload: tool.Payload{Kind: tool.PayloadCustom, Input: `const result = await tools.echo({value: "grpc-stdio"}); notify("GRPC_NOTIFY"); text(result.output);`},
		Context: map[string]any{"code_mode_notify": tool.CodeModeNotifyFunc(func(_ string, text string) { notified = text })},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output == nil || codeModeBodyAfterHeader(t, output.Body) != "GRPC_STDIO_OK" {
		t.Fatalf("output = %#v", output)
	}
	if notified != "GRPC_NOTIFY" {
		t.Fatalf("notify = %q", notified)
	}
}

// Mirrors Rust #52723: sessions share the lazy host process and gRPC channel
// while keeping their code-mode state isolated.
func TestGrpcProcessProviderKeepsSessionsIsolated(t *testing.T) {
	hostProgram := copyProcessHostTestBinary(t)
	t.Setenv(processHostHelperEnv, "grpc")
	provider := NewGrpcProcessProvider(hostProgram)
	defer provider.Close()
	runtimeA := tool.NewCodeModeRuntime(provider, false)
	defer runtimeA.Close()
	runtimeB := tool.NewCodeModeRuntime(provider, false)
	defer runtimeB.Close()
	execA, _ := runtimeA.Executors(tool.NewRegistry())
	execB, _ := runtimeB.Executors(tool.NewRegistry())
	run := func(exec interface {
		Execute(context.Context, *tool.Invocation) (*tool.Output, error)
	}, callID, script string) string {
		t.Helper()
		output, err := exec.Execute(context.Background(), &tool.Invocation{
			CallID:  callID,
			Payload: tool.Payload{Kind: tool.PayloadCustom, Input: script},
		})
		if err != nil {
			t.Fatalf("Execute(%s) error = %v", callID, err)
		}
		return codeModeBodyAfterHeader(t, output.Body)
	}
	run(execA, "store-a", `store("nb", {value: 1}); text("stored-a")`)
	run(execB, "store-b", `store("nb", {value: 2}); text("stored-b")`)
	if got := run(execA, "read-a", `text(JSON.stringify(load("nb")))`); got != `{"value":1}` {
		t.Fatalf("session A state = %q, want its own notebook", got)
	}
	if got := run(execB, "read-b", `text(JSON.stringify(load("nb")))`); got != `{"value":2}` {
		t.Fatalf("session B state = %q, want its own notebook", got)
	}
}

// Mirrors Rust #52723: a dead host is reaped and a fresh one serves the next
// connection attempt.
func TestGrpcProcessProviderRecoversAfterHostDeath(t *testing.T) {
	hostProgram := copyProcessHostTestBinary(t)
	t.Setenv(processHostHelperEnv, "grpc")
	provider := NewGrpcProcessProvider(hostProgram)
	defer provider.Close()
	runtime := tool.NewCodeModeRuntime(provider, false)
	defer runtime.Close()
	execExecutor, _ := runtime.Executors(tool.NewRegistry())
	execute := func(callID string) (string, error) {
		output, err := execExecutor.Execute(context.Background(), &tool.Invocation{
			CallID:  callID,
			Payload: tool.Payload{Kind: tool.PayloadCustom, Input: `text("alive")`},
		})
		if err != nil {
			return "", err
		}
		return codeModeBodyAfterHeader(t, output.Body), nil
	}
	if body, err := execute("before-death"); err != nil || body != "alive" {
		t.Fatalf("initial execute = %q, %v", body, err)
	}
	provider.mu.Lock()
	process := provider.process
	provider.mu.Unlock()
	if process == nil || process.Process == nil {
		t.Fatal("provider has no host process to kill")
	}
	_ = process.Process.Kill()
	var lastErr error
	for attempt := 0; attempt < 3; attempt++ {
		body, err := execute(fmt.Sprintf("after-death-%d", attempt))
		if err == nil && body == "alive" {
			return
		}
		lastErr = err
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatalf("provider did not recover after host death: %v", lastErr)
}

func TestProcessProviderRuntimePersistsStateAndCellsAcrossRegistryRebinds(t *testing.T) {
	hostProgram := copyProcessHostTestBinary(t)
	t.Setenv(processHostHelperEnv, "1")
	provider := NewProcessProvider(hostProgram)
	defer provider.Close()
	runtime := tool.NewCodeModeRuntime(provider, true)
	defer runtime.Close()

	firstRegistry := tool.NewRegistry()
	firstExec, _ := runtime.Executors(firstRegistry)
	stored, err := firstExec.Execute(context.Background(), &tool.Invocation{
		CallID: "store-first", Payload: tool.Payload{Kind: tool.PayloadCustom, Input: `store("nb", {title: "Notebook", count: 2}); text("stored")`},
	})
	if err != nil || stored == nil || codeModeBodyAfterHeader(t, stored.Body) != "stored" {
		t.Fatalf("store output = %#v, error = %v", stored, err)
	}

	secondRegistry := tool.NewRegistry()
	secondExec, secondWait := runtime.Executors(secondRegistry)
	loaded, err := secondExec.Execute(context.Background(), &tool.Invocation{
		CallID: "load-second", Payload: tool.Payload{Kind: tool.PayloadCustom, Input: `text(JSON.stringify(load("nb")))`},
	})
	if err != nil || loaded == nil || codeModeBodyAfterHeader(t, loaded.Body) != `{"count":2,"title":"Notebook"}` {
		t.Fatalf("load output = %#v, error = %v", loaded, err)
	}

	yielded, err := secondExec.Execute(context.Background(), &tool.Invocation{
		CallID: "yield-second", Payload: tool.Payload{Kind: tool.PayloadCustom, Input: "// @exec: {\"yield_time_ms\": 0}\nyield_control(); await new Promise(resolve => setTimeout(resolve, 20)); text(\"done\")"},
	})
	if err != nil || yielded == nil || yielded.Data["running"] != true {
		t.Fatalf("yield output = %#v, error = %v", yielded, err)
	}
	cellID, _ := yielded.Data["cell_id"].(string)
	thirdRegistry := tool.NewRegistry()
	_, thirdWait := runtime.Executors(thirdRegistry)
	if secondWait == thirdWait {
		// The runtime intentionally reuses one wait executor across every turn.
	} else {
		t.Fatal("runtime replaced the wait executor during registry rebind")
	}
	completed, err := thirdWait.Execute(context.Background(), &tool.Invocation{
		CallID: "wait-third", ToolName: tool.PlainName("wait"), Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: fmt.Sprintf(`{"cell_id":%q,"yield_time_ms":1000}`, cellID)},
	})
	if err != nil || completed == nil || codeModeBodyAfterHeader(t, completed.Body) != "done" {
		t.Fatalf("wait output = %#v, error = %v", completed, err)
	}
}

func TestProcessProviderAvailabilityRejectsMissingAndDirectoryPrograms(t *testing.T) {
	missing := filepath.Join(t.TempDir(), codeModeHostTestExecutableName())
	if err := NewProcessProvider(missing).Availability(); err == nil || !strings.Contains(err.Error(), "host executable was not found") {
		t.Fatalf("missing availability error = %v", err)
	}
	directory := filepath.Join(t.TempDir(), codeModeHostTestExecutableName())
	if err := os.Mkdir(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := NewProcessProvider(directory).Availability(); err == nil || !strings.Contains(err.Error(), "host executable was not found") {
		t.Fatalf("directory availability error = %v", err)
	}
}

func TestDisabledProviderWarningIsExactAndEmittedOnce(t *testing.T) {
	provider := NewDisabledProvider()
	want := "Code Mode is unavailable because code-mode host is disabled. Falling back to direct tools; enable `features.code_mode_host` and install `codex-code-mode-host`."
	if got := provider.TakeUnavailableWarning("direct"); got != want {
		t.Fatalf("warning = %q, want %q", got, want)
	}
	if got := provider.TakeUnavailableWarning("direct"); got != "" {
		t.Fatalf("second warning = %q", got)
	}
}

func copyProcessHostTestBinary(t *testing.T) string {
	t.Helper()
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), codeModeHostTestExecutableName())
	in, err := os.Open(source)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		t.Fatal(err)
	}
	if err := out.Close(); err != nil {
		t.Fatal(err)
	}
	return target
}

func codeModeHostTestExecutableName() string {
	if runtime.GOOS == "windows" {
		return "codex-code-mode-host.exe"
	}
	return "codex-code-mode-host"
}
