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

	"codex_go/tool"
)

const processHostHelperEnv = "CODEX_GO_PROCESS_HOST_TEST_HELPER"

func init() {
	if os.Getenv(processHostHelperEnv) != "1" {
		return
	}
	if err := RunStdioHost(context.Background(), os.Stdin, os.Stdout); err != nil {
		_, _ = io.WriteString(os.Stderr, err.Error())
		os.Exit(1)
	}
	os.Exit(0)
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
	if output == nil || output.Body != "CROSS_PROCESS_OK" {
		t.Fatalf("output = %#v", output)
	}
	if notified != "HOST_NOTIFY" {
		t.Fatalf("notify = %q", notified)
	}
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
	if err != nil || stored == nil || stored.Body != "stored" {
		t.Fatalf("store output = %#v, error = %v", stored, err)
	}

	secondRegistry := tool.NewRegistry()
	secondExec, secondWait := runtime.Executors(secondRegistry)
	loaded, err := secondExec.Execute(context.Background(), &tool.Invocation{
		CallID: "load-second", Payload: tool.Payload{Kind: tool.PayloadCustom, Input: `text(JSON.stringify(load("nb")))`},
	})
	if err != nil || loaded == nil || loaded.Body != `{"count":2,"title":"Notebook"}` {
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
	if err != nil || completed == nil || completed.Body != "done" {
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
