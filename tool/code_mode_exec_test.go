package tool

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestCodeModeResponseHeaderMatchesRustFormat mirrors Rust's CodeModeToolOutput
// header (#46288): the default format rounds the wall time to one decimal, the
// overhead format uses three decimals with the host measurement and the
// difference, and absent host timing (or the option off) keeps the default
// format.
func TestCodeModeResponseHeaderMatchesRustFormat(t *testing.T) {
	wall := 1*time.Second + 456*time.Millisecond
	if got := codeModeResponseHeader("Script completed", wall, nil, false); got != "Script completed\nWall time 1.5 seconds\nOutput:\n" {
		t.Fatalf("default header = %q", got)
	}
	// The option on without host timing keeps the default format.
	if got := codeModeResponseHeader("Script completed", wall, nil, true); got != "Script completed\nWall time 1.5 seconds\nOutput:\n" {
		t.Fatalf("overhead-without-host header = %q", got)
	}
	host := 1*time.Second + 456*time.Millisecond
	// 2.5s total against 1.456s host: 1.044s of harness overhead.
	total := 2*time.Second + 500*time.Millisecond
	want := "Script completed\nWall time 2.500 seconds (code-mode 1.456 seconds; overhead 1.044 seconds)\nOutput:\n"
	if got := codeModeResponseHeader("Script completed", total, &host, true); got != want {
		t.Fatalf("overhead header = %q, want %q", got, want)
	}
	// Millisecond quantization can produce a small negative difference.
	hostLater := 2*time.Second + 500*time.Millisecond
	shorterTotal := 2*time.Second + 456*time.Millisecond
	negative := codeModeResponseHeader("Script failed", shorterTotal, &hostLater, true)
	if !strings.Contains(negative, "overhead -0.044 seconds") {
		t.Fatalf("negative overhead header = %q", negative)
	}
}

// TestCodeModeScriptFailureShapeMatchesRust mirrors Rust's failed code-mode
// response (#46288): the script's partial output stays first, the `Script
// error:` block is appended after it, the header reports "Script failed", and
// the response is unsuccessful without being a tool error.
func TestCodeModeScriptFailureShapeMatchesRust(t *testing.T) {
	executor := NewCodeModeExecExecutor(NewRegistry())
	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:  "script-failure",
		Payload: Payload{Kind: PayloadCustom, Input: `text("partial output"); throw new Error("boom")`},
	})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output == nil || output.Success {
		t.Fatalf("output = %#v, want an unsuccessful response", output)
	}
	body := codeModeBodyAfterHeader(t, output.Body, "Script failed")
	if body != "partial output\nScript error:\nError: boom" {
		t.Fatalf("failure body = %q", body)
	}
	items, ok := output.Data["content_items"].([]map[string]any)
	if !ok || len(items) != 2 {
		t.Fatalf("content items = %#v", output.Data["content_items"])
	}
	if items[0]["text"] != "partial output" {
		t.Fatalf("content items[0] = %#v", items[0])
	}
	errText, _ := items[1]["text"].(string)
	if !strings.HasPrefix(errText, "Script error:\n") || !strings.Contains(errText, "boom") {
		t.Fatalf("content items[1] = %#v", items[1])
	}
}

// codeModeBodyAfterHeader strips Rust's code-mode response header (status line,
// wall time, `Output:` separator) and returns the script output, asserting the
// header shape on the way (#46288).
func codeModeBodyAfterHeader(t *testing.T, body string, wantStatus string) string {
	t.Helper()
	status, rest, ok := strings.Cut(body, "\n")
	if !ok {
		t.Fatalf("code-mode body has no header: %q", body)
	}
	if status != wantStatus {
		t.Fatalf("code-mode status = %q, want %q (body %q)", status, wantStatus, body)
	}
	wallLine, rest, ok := strings.Cut(rest, "\n")
	if !ok || !strings.HasPrefix(wallLine, "Wall time ") || !strings.HasSuffix(wallLine, " seconds") {
		t.Fatalf("code-mode wall time line = %q (body %q)", wallLine, body)
	}
	return strings.TrimPrefix(rest, "Output:\n")
}

func TestCodeModeExecUsesCustomPayloadAndNormalizesNestedOutput(t *testing.T) {
	shell := NewShellExecutor(&ShellExecutorOptions{Runner: &recordingShellRunner{output: "ALPHA\n"}, Validation: ShellValidationOptions{CWD: t.TempDir()}})
	registry := NewRegistry()
	if err := registry.Register(shell); err != nil {
		t.Fatal(err)
	}
	executor := NewCodeModeExecExecutor(registry)
	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:  "call-exec",
		Payload: Payload{Kind: PayloadCustom, Input: `const r = await tools.exec_command({"cmd":"printf 'ALPHA\\n'"}); text(r.output);`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if codeModeBodyAfterHeader(t, output.Body, "Script completed") != "ALPHA" || !output.Success {
		t.Fatalf("output = %#v", output)
	}
	commands, ok := output.Data["nested_commands"].([]string)
	if !ok || len(commands) != 1 || !strings.Contains(commands[0], "ALPHA") {
		t.Fatalf("nested commands = %#v", output.Data["nested_commands"])
	}
}

func TestCodeModeExecDescriptionUsesConfiguredDefaultYieldTime(t *testing.T) {
	runtime := NewCodeModeRuntime(nil, false)
	runtime.SetDefaultExecYieldTime(1250 * time.Millisecond)
	executor, _ := runtime.Executors(NewRegistry())
	if description := executor.Spec().Description; !strings.Contains(description, "Defaults to 1250 ms.") {
		t.Fatalf("configured default missing from description: %s", description)
	}
}

func TestCodeModeExecRunsLegacyShellCommandLikeRustWhenUnifiedExecDisabled(t *testing.T) {
	runner := &recordingShellRunner{output: "WEATHER_LEGACY_OK\n"}
	shell := NewShellExecutor(&ShellExecutorOptions{
		Runner:     runner,
		ToolName:   PlainName(DefaultShellCommandToolName),
		Validation: ShellValidationOptions{CWD: t.TempDir()},
	})
	registry := NewRegistry()
	if err := registry.Register(shell); err != nil {
		t.Fatal(err)
	}
	executor := NewCodeModeExecExecutor(registry)
	description := executor.Spec().Description
	for _, phrase := range []string{"tools.shell_command", `"required":["command"]`, `"command":"Write-Output hello"`} {
		if !strings.Contains(description, phrase) {
			t.Fatalf("description missing %q: %s", phrase, description)
		}
	}
	if strings.Contains(description, "tools.exec_command") {
		t.Fatalf("legacy description advertises exec_command: %s", description)
	}
	output, err := executor.Execute(context.Background(), &Invocation{
		CallID:  "weather-legacy",
		Payload: Payload{Kind: PayloadCustom, Input: `const r = await tools.shell_command({command: "Write-Output WEATHER_LEGACY_OK", timeout_ms: 10000, workdir: "` + strings.ReplaceAll(t.TempDir(), `\`, `\\`) + `"}); text(r.output);`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if codeModeBodyAfterHeader(t, output.Body, "Script completed") != "WEATHER_LEGACY_OK" || runner.request == nil || runner.request.HookCommand != "Write-Output WEATHER_LEGACY_OK" {
		t.Fatalf("output = %#v, request = %#v", output, runner.request)
	}
	commands, ok := output.Data["nested_commands"].([]string)
	if !ok || len(commands) != 1 || commands[0] != "Write-Output WEATHER_LEGACY_OK" {
		t.Fatalf("nested commands = %#v", output.Data["nested_commands"])
	}
}

func TestCodeModeExecDescriptionWarnsConsoleIsUnavailableLikeRust(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(NewShellExecutor(nil)); err != nil {
		t.Fatal(err)
	}
	description := NewCodeModeExecExecutor(registry).Spec().Description
	for _, phrase := range []string{"no Node", "no file system", "no network access", "no console"} {
		if !strings.Contains(description, phrase) {
			t.Fatalf("description missing %q: %s", phrase, description)
		}
	}
	for _, phrase := range []string{"tools.exec_command", "There is no nested tools.exec method", "Do not use fetch", `"required":["cmd"]`, "text(r.output)"} {
		if !strings.Contains(description, phrase) {
			t.Fatalf("description missing %q: %s", phrase, description)
		}
	}
}

func TestCodeModeExecTryCatchAndMultipleTools(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("fail_tool")}, func(context.Context, *Invocation) (*Output, error) {
		return nil, errors.New("controlled failure")
	})); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("echo")}, func(_ context.Context, invocation *Invocation) (*Output, error) {
		var args map[string]any
		if err := invocation.DecodeArguments(&args); err != nil {
			return nil, err
		}
		return &Output{Success: true, Body: args["value"].(string)}, nil
	})); err != nil {
		t.Fatal(err)
	}
	executor := NewCodeModeExecExecutor(registry)
	output, err := executor.Execute(context.Background(), &Invocation{CallID: "mixed", Payload: Payload{Kind: PayloadCustom, Input: `
		try { await tools.fail_tool({}); } catch (error) { text("recovered"); }
		const values = await Promise.all([tools.echo({value: "one"}), tools.echo({value: "two"})]);
		text(values.map(value => value.output));
	`}})
	if err != nil {
		t.Fatal(err)
	}
	if codeModeBodyAfterHeader(t, output.Body, "Script completed") != "recovered\n[\"one\",\"two\"]" {
		t.Fatalf("body = %q", output.Body)
	}
}

// Mirrors Rust #43873: an explicit JavaScript `undefined` tool argument behaves
// like an omitted argument (an empty object).
func TestCodeModeExecUndefinedToolArgumentBehavesLikeOmitted(t *testing.T) {
	registry := NewRegistry()
	var mu sync.Mutex
	received := []string{}
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("record_args")}, func(_ context.Context, invocation *Invocation) (*Output, error) {
		mu.Lock()
		received = append(received, invocation.Payload.Arguments)
		mu.Unlock()
		return &Output{Success: true, Body: "ok"}, nil
	})); err != nil {
		t.Fatal(err)
	}
	executor := NewCodeModeExecExecutor(registry)
	if _, err := executor.Execute(context.Background(), &Invocation{CallID: "undefined-args", Payload: Payload{Kind: PayloadCustom, Input: `
		await tools.record_args({});
		await tools.record_args();
		await tools.record_args(undefined);
	`}}); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(received) != 3 || received[0] != "{}" || received[1] != "{}" || received[2] != "{}" {
		t.Fatalf("received arguments = %#v", received)
	}
}

func TestCodeModeExecRejectsFailedShellOutputLikeRust(t *testing.T) {
	registry := NewRegistry()
	var calls int
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName(DefaultShellCommandToolName)}, func(context.Context, *Invocation) (*Output, error) {
		calls++
		return &Output{
			Success: true,
			Body:    "Process exited with code 1\nOutput:\ncontrolled failure",
			Data:    map[string]any{"exit_code": 1, "timed_out": false},
		}, nil
	})); err != nil {
		t.Fatal(err)
	}

	output, err := NewCodeModeExecExecutor(registry).Execute(context.Background(), &Invocation{
		CallID: "uncaught-shell-failure",
		Payload: Payload{Kind: PayloadCustom, Input: `
			await tools.shell_command({command: "fail"});
			await tools.shell_command({command: "must-not-run"});
		`},
	})
	// Rust surfaces an uncaught nested failure as a "Script failed" response
	// whose `Script error:` block carries the nested tool's message.
	if err != nil || output == nil || output.Success {
		t.Fatalf("output = %#v, error = %v", output, err)
	}
	if body := codeModeBodyAfterHeader(t, output.Body, "Script failed"); !strings.Contains(body, "controlled failure") {
		t.Fatalf("failure body = %q", body)
	}
	if calls != 1 {
		t.Fatalf("nested calls = %d, want 1", calls)
	}
}

func TestCodeModeExecPreservesNonShellBusinessFailureResult(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("business_failure")}, func(context.Context, *Invocation) (*Output, error) {
		return &Output{Success: false, Body: "business rule rejected", Error: "business rule rejected"}, nil
	})); err != nil {
		t.Fatal(err)
	}

	output, err := NewCodeModeExecExecutor(registry).Execute(context.Background(), &Invocation{
		CallID:  "business-tool-failure",
		Payload: Payload{Kind: PayloadCustom, Input: `const result = await tools.business_failure({}); text(String(result.success)); text(result.output);`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if codeModeBodyAfterHeader(t, output.Body, "Script completed") != "false\nbusiness rule rejected" {
		t.Fatalf("body = %q", output.Body)
	}
}

func TestCodeModeExecCatchesFailedShellOutputAndClosesNestedLifecycle(t *testing.T) {
	registry := NewRegistry()
	var calls int
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName(DefaultShellCommandToolName)}, func(context.Context, *Invocation) (*Output, error) {
		calls++
		if calls == 1 {
			return &Output{
				Success: true,
				Body:    "Process exited with code 1\nOutput:\ncontrolled failure",
				Data:    map[string]any{"exit_code": 1, "timed_out": false},
			}, nil
		}
		return &Output{
			Success: true,
			Body:    "Process exited with code 0\nOutput:\nRECOVERY_OK\n",
			Data:    map[string]any{"exit_code": 0, "timed_out": false},
		}, nil
	})); err != nil {
		t.Fatal(err)
	}

	type lifecycleRecord struct {
		callID   string
		exitCode int
	}
	var mu sync.Mutex
	started := []string{}
	completed := []lifecycleRecord{}
	contextValues := map[string]any{
		"code_mode_nested_tool_started": CodeModeNestedToolStartedFunc(func(_ context.Context, invocation *Invocation, _ time.Time) {
			mu.Lock()
			defer mu.Unlock()
			started = append(started, invocation.CallID)
		}),
		"code_mode_nested_tool_completed": CodeModeNestedToolCompletedFunc(func(_ context.Context, invocation *Invocation, output *Output, err error, _, _ time.Time) {
			mu.Lock()
			defer mu.Unlock()
			exitCode := -1
			if err == nil && output != nil {
				exitCode, _ = codeModeInt(output.Data["exit_code"])
			}
			completed = append(completed, lifecycleRecord{callID: invocation.CallID, exitCode: exitCode})
		}),
	}
	output, err := NewCodeModeExecExecutor(registry).Execute(context.Background(), &Invocation{
		CallID:  "caught-shell-failure",
		Context: contextValues,
		Payload: Payload{Kind: PayloadCustom, Input: `
			try {
				await tools.shell_command({command: "fail"});
			} catch (error) {
				text("CAUGHT_FAILURE");
			}
			const recovered = await tools.shell_command({command: "recover"});
			text(recovered.output);
		`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if codeModeBodyAfterHeader(t, output.Body, "Script completed") != "CAUGHT_FAILURE\nRECOVERY_OK" {
		t.Fatalf("body = %q", output.Body)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(started) != 2 || len(completed) != 2 {
		t.Fatalf("lifecycle started = %#v, completed = %#v", started, completed)
	}
	for index := range started {
		if started[index] == "" || completed[index].callID != started[index] {
			t.Fatalf("lifecycle[%d] started = %q, completed = %#v", index, started[index], completed[index])
		}
	}
	if completed[0].exitCode != 1 || completed[1].exitCode != 0 {
		t.Fatalf("completed lifecycle = %#v, want exit codes 1 then 0", completed)
	}
}

func TestCodeModeExecExceptionMatrixAndRecovery(t *testing.T) {
	executor := NewCodeModeExecExecutor(NewRegistry())
	for name, source := range map[string]string{
		"syntax":         `const = ;`,
		"throw":          `throw new Error("boom")`,
		"promise_reject": `await Promise.reject(new Error("rejected"))`,
		"timer_throw":    `await new Promise(resolve => setTimeout(() => { throw new Error("timer boom") }, 1))`,
	} {
		t.Run(name, func(t *testing.T) {
			// Rust reports a script failure as an unsuccessful response whose
			// header says "Script failed" and whose content carries a
			// `Script error:` block, not as a tool error (#46288).
			output, err := executor.Execute(context.Background(), &Invocation{CallID: name, Payload: Payload{Kind: PayloadCustom, Input: source}})
			if err != nil {
				t.Fatalf("Execute() error = %v", err)
			}
			if output == nil || output.Success {
				t.Fatalf("output = %#v, want an unsuccessful response", output)
			}
			body := codeModeBodyAfterHeader(t, output.Body, "Script failed")
			if !strings.Contains(body, "Script error:") {
				t.Fatalf("failure body = %q", body)
			}
		})
	}
	output, err := executor.Execute(context.Background(), &Invocation{CallID: "recovery", Payload: Payload{Kind: PayloadCustom, Input: `text("RECOVERED")`}})
	if err != nil || codeModeBodyAfterHeader(t, output.Body, "Script completed") != "RECOVERED" {
		t.Fatalf("recovery = %#v, %v", output, err)
	}
}

func TestCodeModeExecCancellationStopsInfiniteLoop(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err := NewCodeModeExecExecutor(NewRegistry()).Execute(ctx, &Invocation{CallID: "loop", Payload: Payload{Kind: PayloadCustom, Input: `while (true) {}`}})
	if err == nil || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v", err)
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("cancellation took %s", elapsed)
	}
}

func TestCodeModeExecRoutesFreeformApplyPatch(t *testing.T) {
	dir := t.TempDir()
	registry := NewRegistry()
	if err := registry.Register(NewApplyPatchExecutor(&ApplyPatchExecutorOptions{CWD: dir})); err != nil {
		t.Fatal(err)
	}
	executor := NewCodeModeExecExecutor(registry)
	patch := "*** Begin Patch\n*** Add File: nested.txt\n+from sobek\n*** End Patch"
	source := "const result = await tools.apply_patch(" + string(mustJSON(t, patch)) + "); text(result.success);"
	output, err := executor.Execute(context.Background(), &Invocation{CallID: "patch", Payload: Payload{Kind: PayloadCustom, Input: source}})
	if err != nil {
		t.Fatal(err)
	}
	if codeModeBodyAfterHeader(t, output.Body, "Script completed") != "true" {
		t.Fatalf("body = %q", output.Body)
	}
	data, err := os.ReadFile(filepath.Join(dir, "nested.txt"))
	if err != nil || string(data) != "from sobek\n" {
		t.Fatalf("file = %q, %v", data, err)
	}
}

func TestCodeModeExecHelpersAndSessionStore(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("echo"), Description: "Echo a value"}, func(context.Context, *Invocation) (*Output, error) {
		return &Output{Success: true}, nil
	})); err != nil {
		t.Fatal(err)
	}
	executor := NewCodeModeExecExecutor(registry)
	first, err := executor.Execute(context.Background(), &Invocation{CallID: "helpers-1", Payload: Payload{Kind: PayloadCustom, Input: `
		store("state", {count: 3});
		text(ALL_TOOLS[0].name);
		image("data:image/png;base64,AAAA", "original");
		audio({audio_url: "data:audio/wav;base64,AAAA"});
	`}})
	if err != nil {
		t.Fatal(err)
	}
	items, ok := first.Data["content_items"].([]map[string]any)
	if !ok || len(items) != 3 || items[1]["type"] != "input_image" || items[2]["type"] != "input_audio" {
		t.Fatalf("items = %#v", first.Data["content_items"])
	}
	second, err := executor.Execute(context.Background(), &Invocation{CallID: "helpers-2", Payload: Payload{Kind: PayloadCustom, Input: `text(load("state").count); exit(); text("unreachable")`}})
	if err != nil {
		t.Fatal(err)
	}
	if codeModeBodyAfterHeader(t, second.Body, "Script completed") != "3" {
		t.Fatalf("stored body = %q", second.Body)
	}
}

// Mirrors Rust #43873: storing JavaScript `undefined` reports the existing
// serializability error and preserves the previously stored value.
func TestCodeModeExecStoringUndefinedPreservesPreviousValue(t *testing.T) {
	executor := NewCodeModeExecExecutor(NewRegistry())
	output, err := executor.Execute(context.Background(), &Invocation{CallID: "store-undefined", Payload: Payload{Kind: PayloadCustom, Input: `
		store("key", null);
		try {
			store("key", undefined);
		} catch (error) {
			text(String(error));
		}
		text(load("key"));
	`}})
	if err != nil {
		t.Fatal(err)
	}
	want := "Unable to store \"key\". Only plain serializable objects can be stored.\nnull"
	if codeModeBodyAfterHeader(t, output.Body, "Script completed") != want {
		t.Fatalf("body = %q, want %q", output.Body, want)
	}
}

func TestCodeModeExecForwardsMCPStructuredImageAndAudioContent(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(NewExecutorFunc(Spec{Name: NamespacedName("mcp__demo", "inspect")}, func(context.Context, *Invocation) (*Output, error) {
		return &Output{Success: true, Data: map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": "from-mcp"},
				{"type": "image", "data": "aW1hZ2U=", "mimeType": "image/png", "_meta": map[string]any{"codex/imageDetail": "original"}},
				{"type": "audio", "data": "YXVkaW8=", "mimeType": "audio/wav"},
			},
			"structuredContent": map[string]any{"answer": 42},
			"_meta":             map[string]any{"source": "fixture"},
		}}, nil
	})); err != nil {
		t.Fatal(err)
	}
	output, err := NewCodeModeExecExecutor(registry).Execute(context.Background(), &Invocation{CallID: "mcp-content", Payload: Payload{Kind: PayloadCustom, Input: `
		const result = await tools.mcp__demo__inspect({value: 1});
		text(result.content[0].text);
		text(result.structuredContent.answer);
		image(result.content[1]);
		audio(result.content[2]);
	`}})
	if err != nil {
		t.Fatal(err)
	}
	items, ok := output.Data["content_items"].([]map[string]any)
	if !ok || len(items) != 4 {
		t.Fatalf("content_items = %#v", output.Data["content_items"])
	}
	if items[0]["text"] != "from-mcp" || items[1]["text"] != "42" {
		t.Fatalf("text items = %#v", items[:2])
	}
	if items[2]["image_url"] != "data:image/png;base64,aW1hZ2U=" || items[2]["detail"] != "original" {
		t.Fatalf("image item = %#v", items[2])
	}
	if items[3]["audio_url"] != "data:audio/wav;base64,YXVkaW8=" {
		t.Fatalf("audio item = %#v", items[3])
	}
}

func TestCodeModeExecTimers(t *testing.T) {
	executor := NewCodeModeExecExecutor(NewRegistry())
	output, err := executor.Execute(context.Background(), &Invocation{CallID: "timer", Payload: Payload{Kind: PayloadCustom, Input: `
		await new Promise(resolve => setTimeout(resolve, 5));
		text("timer-complete");
		const ignored = setTimeout(() => text("must-not-run"), 1000);
		clearTimeout(ignored);
	`}})
	if err != nil {
		t.Fatal(err)
	}
	if codeModeBodyAfterHeader(t, output.Body, "Script completed") != "timer-complete" {
		t.Fatalf("body = %q", output.Body)
	}
}

func TestCodeModeExecYieldWaitAndTerminate(t *testing.T) {
	registry := NewRegistry()
	exec, wait := NewCodeModeExecutors(registry)
	if err := registry.Register(exec); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(wait); err != nil {
		t.Fatal(err)
	}
	started, err := exec.Execute(context.Background(), &Invocation{CallID: "yield", Payload: Payload{Kind: PayloadCustom, Input: `// @exec: {"yield_time_ms": 0}
		await new Promise(resolve => setTimeout(resolve, 20)); text("done");`}})
	if err != nil {
		t.Fatal(err)
	}
	cellID, _ := started.Data["cell_id"].(string)
	if cellID == "" || !strings.Contains(started.Body, "Script running with cell ID") {
		t.Fatalf("started = %#v", started)
	}
	finished, err := wait.Execute(context.Background(), &Invocation{CallID: "wait", Payload: Payload{Kind: PayloadFunction, Arguments: `{"cell_id":"` + cellID + `","yield_time_ms":1000}`}})
	if err != nil {
		t.Fatal(err)
	}
	if codeModeBodyAfterHeader(t, finished.Body, "Script completed") != "done" {
		t.Fatalf("finished = %#v", finished)
	}

	yielded, err := exec.Execute(context.Background(), &Invocation{CallID: "control", Payload: Payload{Kind: PayloadCustom, Input: `yield_control(); await new Promise(resolve => setTimeout(resolve, 1000));`}})
	if err != nil {
		t.Fatal(err)
	}
	controlID := yielded.Data["cell_id"].(string)
	terminated, err := wait.Execute(context.Background(), &Invocation{CallID: "terminate", Payload: Payload{Kind: PayloadFunction, Arguments: `{"cell_id":"` + controlID + `","terminate":true}`}})
	if err != nil || terminated == nil || !terminated.Success || terminated.Data["terminated"] != true {
		t.Fatalf("terminate = %#v, %v", terminated, err)
	}
}

func TestCodeModeTerminatePreservesOutputAndClosesCell(t *testing.T) {
	registry := NewRegistry()
	exec, wait := NewCodeModeExecutors(registry)
	started, err := exec.Execute(context.Background(), &Invocation{CallID: "terminate-output", Payload: Payload{Kind: PayloadCustom, Input: `text("before"); yield_control(); await new Promise(resolve => setTimeout(resolve, 60000));`}})
	if err != nil {
		t.Fatal(err)
	}
	cellID := started.Data["cell_id"].(string)
	terminated, err := wait.Execute(context.Background(), &Invocation{CallID: "terminate-output-wait", Payload: Payload{Kind: PayloadFunction, Arguments: `{"cell_id":"` + cellID + `","terminate":true}`}})
	if err != nil || terminated == nil || !terminated.Success {
		t.Fatalf("terminate = %#v, %v", terminated, err)
	}
	if _, err := wait.Execute(context.Background(), &Invocation{CallID: "wait-after-terminate", Payload: Payload{Kind: PayloadFunction, Arguments: `{"cell_id":"` + cellID + `"}`}}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("wait after terminate error = %v", err)
	}
}

func TestCodeModeWaitReturnsOnlyNewOutput(t *testing.T) {
	registry := NewRegistry()
	exec, wait := NewCodeModeExecutors(registry)
	if err := registry.Register(exec); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(wait); err != nil {
		t.Fatal(err)
	}
	started, err := exec.Execute(context.Background(), &Invocation{CallID: "delta", Payload: Payload{Kind: PayloadCustom, Input: `text("first"); yield_control(); await new Promise(resolve => setTimeout(resolve, 40)); text("second"); await new Promise(resolve => setTimeout(resolve, 40)); text("third");`}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(started.Body, "first") {
		t.Fatalf("initial = %q", started.Body)
	}
	cellID := started.Data["cell_id"].(string)
	time.Sleep(55 * time.Millisecond)
	pending, err := wait.Execute(context.Background(), &Invocation{CallID: "delta-wait-1", Payload: Payload{Kind: PayloadFunction, Arguments: `{"cell_id":"` + cellID + `","yield_time_ms":1}`}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(pending.Body, "second") || strings.Contains(pending.Body, "first") {
		t.Fatalf("pending delta = %q", pending.Body)
	}
	finished, err := wait.Execute(context.Background(), &Invocation{CallID: "delta-wait-2", Payload: Payload{Kind: PayloadFunction, Arguments: `{"cell_id":"` + cellID + `","yield_time_ms":1000}`}})
	if err != nil {
		t.Fatal(err)
	}
	if codeModeBodyAfterHeader(t, finished.Body, "Script completed") != "third" {
		t.Fatalf("final delta = %q", finished.Body)
	}
}

func TestCodeModeOutputBudgetsAndPragmaValidation(t *testing.T) {
	executor := NewCodeModeExecExecutor(NewRegistry())
	output, err := executor.Execute(context.Background(), &Invocation{CallID: "truncate", Payload: Payload{Kind: PayloadCustom, Input: `// @exec: {"max_output_tokens":5}
		text("0123456789012345678901234567890123456789");`}})
	if err != nil {
		t.Fatal(err)
	}
	truncatedBody := codeModeBodyAfterHeader(t, output.Body, "Script completed")
	if !strings.HasPrefix(truncatedBody, "Warning: truncated output (original token count: 10)") || !strings.Contains(truncatedBody, "tokens truncated") {
		t.Fatalf("truncated body = %q", output.Body)
	}
	for _, source := range []string{"", "  \n", `// @exec: {"unknown":1}
text("x")`, `// @exec: {"yield_time_ms":-1}
text("x")`, `// @exec: {"max_output_tokens":-1}
text("x")`, `// @exec: {"yield_time_ms":9007199254740992}
text("x")`} {
		if _, err := executor.Execute(context.Background(), &Invocation{CallID: "invalid", Payload: Payload{Kind: PayloadCustom, Input: source}}); err == nil {
			t.Fatalf("source %q accepted", source)
		}
	}
}

func TestCodeModeRejectsTooManyPendingDelegateCalls(t *testing.T) {
	registry := NewRegistry()
	release := make(chan struct{})
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("blocked"), Parallel: true}, func(ctx context.Context, _ *Invocation) (*Output, error) {
		select {
		case <-release:
			return &Output{Success: true}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})); err != nil {
		t.Fatal(err)
	}
	defer close(release)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	source := `const calls = []; for (let i = 0; i < 1025; i++) calls.push(tools.blocked({})); await Promise.all(calls);`
	output, err := NewCodeModeExecExecutor(registry).Execute(ctx, &Invocation{CallID: "pending-limit", Payload: Payload{Kind: PayloadCustom, Input: source}})
	if err != nil || output == nil || output.Success {
		t.Fatalf("output = %#v, error = %v", output, err)
	}
	if body := codeModeBodyAfterHeader(t, output.Body, "Script failed"); !strings.Contains(body, "1024 pending delegate calls") {
		t.Fatalf("failure body = %q", body)
	}
}

func TestCodeModeNotifyInjectsSeparateOutputWithoutDuplicatingFinalBody(t *testing.T) {
	registry := NewRegistry()
	executor := NewCodeModeExecExecutor(registry)
	var gotCallID, gotText string
	output, err := executor.Execute(context.Background(), &Invocation{CallID: "notify-call", Context: map[string]any{"code_mode_notify": CodeModeNotifyFunc(func(callID, text string) { gotCallID, gotText = callID, text })}, Payload: Payload{Kind: PayloadCustom, Input: `notify("ping"); text("final")`}})
	if err != nil {
		t.Fatal(err)
	}
	if gotCallID != "notify-call" || gotText != "ping" {
		t.Fatalf("notify = %q/%q", gotCallID, gotText)
	}
	if codeModeBodyAfterHeader(t, output.Body, "Script completed") != "final" || strings.Contains(output.Body, "ping") {
		t.Fatalf("body = %q", output.Body)
	}
}

func TestCodeModeRemoteSuccessDoesNotRunInProcessFallback(t *testing.T) {
	registry := NewRegistry()
	remote := &recordingCodeModeRemoteSession{response: CodeModeRemoteResponse{
		CellID: "remote-cell",
		State:  "completed",
		ContentItems: []map[string]any{{
			"type": "input_text",
			"text": "REMOTE_OK",
		}},
	}}
	exec, _ := NewCodeModeExecutorsWithProvider(registry, &recordingCodeModeRemoteProvider{session: remote}, false)
	output, err := exec.Execute(context.Background(), &Invocation{
		CallID:  "remote-success",
		Payload: Payload{Kind: PayloadCustom, Input: `text("LOCAL_FALLBACK")`},
	})
	if err != nil {
		t.Fatal(err)
	}
	if codeModeBodyAfterHeader(t, output.Body, "Script completed") != "REMOTE_OK" || remote.executeCalls != 1 {
		t.Fatalf("output = %#v execute calls = %d", output, remote.executeCalls)
	}
	if remote.request.YieldTimeMS == nil || *remote.request.YieldTimeMS != uint64(CodeModeDefaultExecYieldTime/time.Millisecond) {
		t.Fatalf("remote default yield = %#v", remote.request.YieldTimeMS)
	}
}

func TestCodeModeRemoteFailureNeverFallsBackInProcess(t *testing.T) {
	registry := NewRegistry()
	remote := &recordingCodeModeRemoteSession{err: errors.New("remote unavailable")}
	exec, _ := NewCodeModeExecutorsWithProvider(registry, &recordingCodeModeRemoteProvider{session: remote}, false)
	output, err := exec.Execute(context.Background(), &Invocation{
		CallID:  "remote-fallback",
		Payload: Payload{Kind: PayloadCustom, Input: `text("LOCAL_OK")`},
	})
	if err == nil || output != nil || !strings.Contains(err.Error(), "code-mode remote host unavailable: remote unavailable") || remote.executeCalls != 1 {
		t.Fatalf("output = %#v error = %v execute calls = %d", output, err, remote.executeCalls)
	}
}

// TestCodeModeRemoteHostDurationFeedsTheOverheadHeader mirrors Rust #46288: the
// code-mode host's own measurement drives the overhead breakdown, and the
// default header is kept when overhead reporting is off.
func TestCodeModeRemoteHostDurationFeedsTheOverheadHeader(t *testing.T) {
	newExecutor := func(t *testing.T, hostDurationNS uint64) Executor {
		t.Helper()
		registry := NewRegistry()
		remote := &recordingCodeModeRemoteSession{response: CodeModeRemoteResponse{
			CellID:         "remote-timing-cell",
			State:          "completed",
			ContentItems:   []map[string]any{{"type": "input_text", "text": "REMOTE"}},
			HostDurationNS: hostDurationNS,
		}}
		exec, _ := NewCodeModeExecutorsWithProvider(registry, &recordingCodeModeRemoteProvider{session: remote}, false)
		return exec
	}
	run := func(t *testing.T, executor Executor, showOverhead bool) string {
		t.Helper()
		if exec, ok := executor.(*codeModeExecExecutor); ok {
			exec.bindingMu.Lock()
			exec.showCellOverhead = showOverhead
			exec.bindingMu.Unlock()
		}
		output, err := executor.Execute(context.Background(), &Invocation{
			CallID: "remote-timing", Payload: Payload{Kind: PayloadCustom, Input: `text("REMOTE")`},
		})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		return output.Body
	}

	// Overhead on with a host measurement of 2s: the breakdown appears.
	overheadBody := run(t, newExecutor(t, uint64(2*time.Second)), true)
	if !strings.Contains(overheadBody, "(code-mode 2.000 seconds; overhead ") {
		t.Fatalf("overhead body = %q", overheadBody)
	}
	if !strings.Contains(overheadBody, "Output:\nREMOTE") {
		t.Fatalf("overhead body lost the script output: %q", overheadBody)
	}

	// Overhead on without a host measurement keeps the default header.
	plainBody := run(t, newExecutor(t, 0), true)
	if strings.Contains(plainBody, "code-mode ") {
		t.Fatalf("unexpected overhead breakdown: %q", plainBody)
	}
	if !strings.HasPrefix(plainBody, "Script completed\nWall time ") || !strings.HasSuffix(plainBody, " seconds\nOutput:\nREMOTE") {
		t.Fatalf("default body = %q", plainBody)
	}
}

func TestCodeModeRemoteRuntimeErrorRespondsToModel(t *testing.T) {
	registry := NewRegistry()
	remote := &recordingCodeModeRemoteSession{response: CodeModeRemoteResponse{
		CellID:    "remote-error-cell",
		State:     "completed",
		ErrorText: "apply_patch verification failed",
	}}
	exec, _ := NewCodeModeExecutorsWithProvider(registry, &recordingCodeModeRemoteProvider{session: remote}, false)
	output, err := exec.Execute(context.Background(), &Invocation{
		CallID: "remote-runtime-error", Payload: Payload{Kind: PayloadCustom, Input: `await tools.apply_patch("broken")`},
	})
	// Rust reports the host's runtime error as a "Script failed" response
	// carrying the message in its `Script error:` block.
	if err != nil || output == nil || output.Success {
		t.Fatalf("output = %#v error = %v", output, err)
	}
	if body := codeModeBodyAfterHeader(t, output.Body, "Script failed"); !strings.Contains(body, "apply_patch verification failed") {
		t.Fatalf("failure body = %q", body)
	}
}

func TestCodeModeRemoteWaitRuntimeErrorRespondsToModel(t *testing.T) {
	registry := NewRegistry()
	remote := &recordingCodeModeRemoteSession{response: CodeModeRemoteResponse{
		CellID:    "remote-wait-error-cell",
		State:     "completed",
		ErrorText: "asynchronous execution failed",
	}}
	_, wait := NewCodeModeExecutorsWithProvider(registry, &recordingCodeModeRemoteProvider{session: remote}, false)
	output, err := wait.Execute(context.Background(), &Invocation{
		CallID: "remote-wait-runtime-error", Payload: Payload{Kind: PayloadFunction, Arguments: `{"cell_id":"remote-wait-error-cell"}`},
	})
	if err != nil || output == nil || output.Success {
		t.Fatalf("output = %#v error = %v", output, err)
	}
	if body := codeModeBodyAfterHeader(t, output.Body, "Script failed"); !strings.Contains(body, "asynchronous execution failed") {
		t.Fatalf("failure body = %q", body)
	}
}

func TestCodeModeRemoteFailureIsFatalWhenFallbackDisabled(t *testing.T) {
	registry := NewRegistry()
	remote := &recordingCodeModeRemoteSession{err: errors.New("remote unavailable")}
	exec, _ := NewCodeModeExecutorsWithProvider(registry, &recordingCodeModeRemoteProvider{session: remote}, true)
	output, err := exec.Execute(context.Background(), &Invocation{
		CallID:  "remote-no-fallback",
		Payload: Payload{Kind: PayloadCustom, Input: `text("MUST_NOT_RUN")`},
	})
	if err == nil || output != nil || !strings.Contains(err.Error(), "code-mode remote host unavailable: remote unavailable") {
		t.Fatalf("output = %#v error = %v", output, err)
	}
}

func TestCodeModeDisabledHostIsFatalWhenFallbackDisabled(t *testing.T) {
	registry := NewRegistry()
	exec, _ := NewCodeModeExecutorsWithProvider(registry, nil, true)
	output, err := exec.Execute(context.Background(), &Invocation{
		CallID: "disabled-host", Payload: Payload{Kind: PayloadCustom, Input: `text("MUST_NOT_RUN")`},
	})
	if err == nil || output != nil || !strings.Contains(err.Error(), "code-mode host is disabled and in-process fallback is disabled") {
		t.Fatalf("output = %#v error = %v", output, err)
	}
}

func TestCodeModeToolNamesMatchActualNestedTools(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(NewExecutorFunc(Spec{Name: NamespacedName("mcp__calendar", "lookup")}, func(context.Context, *Invocation) (*Output, error) {
		return &Output{Success: true}, nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName(DefaultExecCommandToolName), Exposure: ExposureHidden}, func(context.Context, *Invocation) (*Output, error) {
		return &Output{Success: true}, nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("other_hidden"), Exposure: ExposureHidden}, func(context.Context, *Invocation) (*Output, error) {
		return &Output{Success: true}, nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("request_user_input"), Exposure: ExposureDirectModelOnly}, func(context.Context, *Invocation) (*Output, error) {
		return &Output{Success: true}, nil
	})); err != nil {
		t.Fatal(err)
	}
	exec, wait := NewCodeModeExecutors(registry, PlainName(DefaultExecCommandToolName))
	if err := registry.Register(exec); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(wait); err != nil {
		t.Fatal(err)
	}

	names := NewRouter(registry).CodeModeToolNames()
	if len(names) != 2 {
		t.Fatalf("CodeModeToolNames() = %#v", names)
	}
	if got := names[DefaultExecCommandToolName]; got.Name != DefaultExecCommandToolName || got.Namespace != nil {
		t.Fatalf("exec command mapping = %#v", got)
	}
	calendar := names["mcp__calendar__lookup"]
	if calendar.Name != "lookup" || calendar.Namespace == nil || *calendar.Namespace != "mcp__calendar" {
		t.Fatalf("calendar mapping = %#v", calendar)
	}
	if _, ok := names["other_hidden"]; ok {
		t.Fatalf("hidden non-command tool leaked: %#v", names)
	}
	if _, ok := names["request_user_input"]; ok {
		t.Fatalf("direct-model-only tool leaked into code mode: %#v", names)
	}
}

func TestCodeModeNestedToolsPerSurfaceExposure(t *testing.T) {
	registry := NewRegistry()
	register := func(name string, exposure Exposure) {
		t.Helper()
		if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName(name), Exposure: exposure}, func(context.Context, *Invocation) (*Output, error) {
			return &Output{Success: true}, nil
		})); err != nil {
			t.Fatal(err)
		}
	}
	register("code_only", ExposureCodeModeOnly)
	register("deferred_only", ExposureDeferredModelOnly)
	exec, wait := NewCodeModeExecutors(registry, PlainName(DefaultExecCommandToolName))
	if err := registry.Register(exec); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(wait); err != nil {
		t.Fatal(err)
	}
	names := NewRouter(registry).CodeModeToolNames()
	if _, ok := names["code_only"]; !ok {
		t.Fatalf("code_mode_only tool missing from nested tools: %#v", names)
	}
	if _, ok := names["deferred_only"]; ok {
		t.Fatalf("deferred_model_only tool should not be nested-callable: %#v", names)
	}
}

func TestCodeModeToolResultStripsMeta(t *testing.T) {
	result := codeModeToolResult(&Output{
		Success: true,
		Body:    "hello",
		Data: map[string]any{
			"_meta":       map[string]any{"trace": "x"},
			"mcpToolCall": true,
			"content":     []any{},
		},
	})
	if _, ok := result["_meta"]; ok {
		t.Fatalf("result leaks _meta: %#v", result)
	}
	if result["mcpToolCall"] != true {
		t.Fatalf("non-meta data lost: %#v", result)
	}
}

func TestCodeModeNormalizedToolNameCollisionKeepsFirstRegisteredTool(t *testing.T) {
	registry := NewRegistry()
	for _, spec := range []Spec{
		{Name: PlainName("foo-bar"), Description: "first winner"},
		{Name: PlainName("foo_bar"), Description: "shadowed tool"},
	} {
		if err := registry.Register(NewExecutorFunc(spec, noopExecutor)); err != nil {
			t.Fatal(err)
		}
	}
	exec, wait := NewCodeModeExecutors(registry)
	if err := registry.Prepend(wait); err != nil {
		t.Fatal(err)
	}
	if err := registry.Prepend(exec); err != nil {
		t.Fatal(err)
	}

	router := NewRouter(registry)
	names := router.CodeModeToolNames()
	if len(names) != 1 || names["foo_bar"].Name != "foo-bar" {
		t.Fatalf("CodeModeToolNames() = %#v", names)
	}
	specs := router.CodeModeToolSpecs()
	if len(specs) != 1 || specs[0].Name.Key() != "foo-bar" || specs[0].Description != "first winner" {
		t.Fatalf("CodeModeToolSpecs() = %#v", specs)
	}
	if _, ok := registry.Lookup(PlainName("foo_bar")); !ok {
		t.Fatal("shadowed tool must remain directly dispatchable")
	}
}

func mustJSON(t *testing.T, value string) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

type recordingShellRunner struct {
	output  string
	request *ShellRequest
}

type recordingCodeModeRemoteProvider struct {
	session CodeModeRemoteSession
}

func (p *recordingCodeModeRemoteProvider) NewSession(CodeModeRemoteDelegate) CodeModeRemoteSession {
	return p.session
}

type recordingCodeModeRemoteSession struct {
	response     CodeModeRemoteResponse
	err          error
	executeCalls int
	request      CodeModeRemoteExecuteRequest
}

func (s *recordingCodeModeRemoteSession) Execute(_ context.Context, request CodeModeRemoteExecuteRequest) (CodeModeRemoteResponse, error) {
	s.executeCalls++
	s.request = request
	return s.response, s.err
}

func (s *recordingCodeModeRemoteSession) Wait(context.Context, string, uint64) (CodeModeRemoteResponse, error) {
	return s.response, s.err
}

func (s *recordingCodeModeRemoteSession) Terminate(context.Context, string) (CodeModeRemoteResponse, error) {
	return s.response, s.err
}

func (s *recordingCodeModeRemoteSession) Close() error { return nil }

func (r *recordingShellRunner) Run(_ context.Context, request *ShellRequest) (*ShellResult, error) {
	r.request = request
	return &ShellResult{Stdout: r.output, ExitCode: 0, HasExitCode: true}, nil
}

// yieldedCodeModeSession returns a yielded cell on execute and a completed cell
// on wait/terminate.
type yieldedCodeModeSession struct {
	executeResponse CodeModeRemoteResponse
	settleResponse  CodeModeRemoteResponse
}

func (s *yieldedCodeModeSession) Execute(context.Context, CodeModeRemoteExecuteRequest) (CodeModeRemoteResponse, error) {
	return s.executeResponse, nil
}

func (s *yieldedCodeModeSession) Wait(context.Context, string, uint64) (CodeModeRemoteResponse, error) {
	return s.settleResponse, nil
}

func (s *yieldedCodeModeSession) Terminate(context.Context, string) (CodeModeRemoteResponse, error) {
	return s.settleResponse, nil
}

func (s *yieldedCodeModeSession) Close() error { return nil }

// Mirrors Rust #44865: a yielded cell retains its callback registration (its
// owning execution's context) past the execute return, and the registration is
// released once the cell completes.
func TestCodeModeYieldedCellRetainsCallbacksUntilCompletion(t *testing.T) {
	registry := NewRegistry()
	remote := &yieldedCodeModeSession{
		executeResponse: CodeModeRemoteResponse{CellID: "yield-cell", State: "yielded"},
		settleResponse: CodeModeRemoteResponse{
			CellID:       "yield-cell",
			State:        "completed",
			ContentItems: []map[string]any{{"type": "input_text", "text": "done"}},
		},
	}
	exec, wait := NewCodeModeExecutorsWithProvider(registry, &recordingCodeModeRemoteProvider{session: remote}, false)
	inner, ok := exec.(*codeModeExecExecutor)
	if !ok {
		t.Fatalf("executor type = %T", exec)
	}
	output, err := exec.Execute(context.Background(), &Invocation{
		CallID:  "yield-call",
		Payload: Payload{Kind: PayloadCustom, Input: `text("RUN")`},
	})
	if err != nil || output == nil {
		t.Fatalf("execute output = %#v, error = %v", output, err)
	}
	delegate, _ := inner.remoteDelegate()
	if delegate.invocation("yield-call") == nil {
		t.Fatal("a yielded cell must retain its delegate callbacks past execute")
	}
	inner.remoteCellsMu.RLock()
	_, tracked := inner.remoteCells["yield-cell"]
	_, retained := inner.remoteCellReleases["yield-cell"]
	inner.remoteCellsMu.RUnlock()
	if !tracked || !retained {
		t.Fatalf("yielded cell tracking = %v release = %v", tracked, retained)
	}

	if _, err := wait.Execute(context.Background(), &Invocation{
		CallID:  "yield-wait",
		Payload: Payload{Kind: PayloadFunction, Arguments: `{"cell_id":"yield-cell"}`},
	}); err != nil {
		t.Fatalf("wait error = %v", err)
	}
	if delegate.invocation("yield-call") != nil {
		t.Fatal("callbacks must be released once the cell completes")
	}
	inner.remoteCellsMu.RLock()
	_, tracked = inner.remoteCells["yield-cell"]
	_, retained = inner.remoteCellReleases["yield-cell"]
	inner.remoteCellsMu.RUnlock()
	if tracked || retained {
		t.Fatalf("cell after completion tracking = %v release = %v", tracked, retained)
	}
}

// Mirrors Rust #44865: a host-initiated cell close releases the retained
// delegate callbacks even though the client never observed a terminal wait.
func TestCodeModeHostCellCloseReleasesRetainedCallbacks(t *testing.T) {
	registry := NewRegistry()
	remote := &yieldedCodeModeSession{
		executeResponse: CodeModeRemoteResponse{CellID: "closed-cell", State: "yielded"},
	}
	exec, _ := NewCodeModeExecutorsWithProvider(registry, &recordingCodeModeRemoteProvider{session: remote}, false)
	inner, ok := exec.(*codeModeExecExecutor)
	if !ok {
		t.Fatalf("executor type = %T", exec)
	}
	if _, err := exec.Execute(context.Background(), &Invocation{
		CallID:  "closed-call",
		Payload: Payload{Kind: PayloadCustom, Input: `text("RUN")`},
	}); err != nil {
		t.Fatalf("execute error = %v", err)
	}
	delegate, _ := inner.remoteDelegate()
	if delegate.invocation("closed-call") == nil {
		t.Fatal("a yielded cell must retain its delegate callbacks past execute")
	}
	delegate.CellClosed("closed-cell")
	if delegate.invocation("closed-call") != nil {
		t.Fatal("a host cell close must release the retained callbacks")
	}
	inner.remoteCellsMu.RLock()
	_, tracked := inner.remoteCells["closed-cell"]
	_, retained := inner.remoteCellReleases["closed-cell"]
	inner.remoteCellsMu.RUnlock()
	if tracked || retained {
		t.Fatalf("cell after host close tracking = %v release = %v", tracked, retained)
	}
}

// Mirrors Rust #45409: a Code Mode cell retains the Responses item and the
// conversation window it started in (cell_originating_call), so a nested tool
// call issued after a wait - or after a compaction moved the thread to another
// window - still reports the cell's origin.
func TestCodeModeCellRetainsItsOriginLikeRust(t *testing.T) {
	runtime := NewCodeModeRuntime(nil, false)
	defer func() { _ = runtime.Close() }()
	registry := NewRegistry()
	var mu sync.Mutex
	var nested []map[string]any
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("nested-tool")}, func(_ context.Context, invocation *Invocation) (*Output, error) {
		mu.Lock()
		nested = append(nested, cloneInvocationContext(invocation.Context))
		mu.Unlock()
		return &Output{Success: true, Body: "ok"}, nil
	})); err != nil {
		t.Fatalf("register nested tool: %v", err)
	}
	_, _ = runtime.Executors(registry)
	runtime.SetTurnWindowID("thread-1:0")
	delegate := &codeModeRemoteDelegate{exec: runtime.exec}

	// The cell starts in window 0, requested by item fc-before.
	release := delegate.begin(&Invocation{CallID: "call-exec", Context: map[string]any{OriginItemIDContextKey: "fc-before"}})
	if _, err := delegate.Invoke(context.Background(), CodeModeRemoteNestedCall{
		CellID: "origin-cell", RuntimeToolCallID: "call-exec-nested", ToolName: PlainName("nested-tool"),
		Kind: PayloadFunction, Input: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("first nested call error = %v", err)
	}
	release()

	// A compaction moves the thread to window 1 and a wait resumes the cell.
	runtime.SetTurnWindowID("thread-1:1")
	resumed := &Invocation{CallID: "call-wait", Context: map[string]any{OriginItemIDContextKey: "fc-after"}}
	release = delegate.begin(resumed)
	if _, err := delegate.Invoke(context.Background(), CodeModeRemoteNestedCall{
		CellID: "origin-cell", RuntimeToolCallID: "call-wait-nested", ToolName: PlainName("nested-tool"),
		Kind: PayloadFunction, Input: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("resumed nested call error = %v", err)
	}
	release()

	mu.Lock()
	captured := append([]map[string]any(nil), nested...)
	mu.Unlock()
	if len(captured) != 2 {
		t.Fatalf("nested invocations = %d, want 2", len(captured))
	}
	for index, context := range captured {
		if context[OriginItemIDContextKey] != "fc-before" || context[OriginWindowIDContextKey] != "thread-1:0" {
			t.Fatalf("nested call %d origin = %#v/%#v, want fc-before/thread-1:0", index, context[OriginItemIDContextKey], context[OriginWindowIDContextKey])
		}
	}

	// A different cell starts its own origin from the invocation that started it.
	release = delegate.begin(&Invocation{CallID: "call-other", Context: map[string]any{OriginItemIDContextKey: "fc-other"}})
	if _, err := delegate.Invoke(context.Background(), CodeModeRemoteNestedCall{
		CellID: "other-cell", RuntimeToolCallID: "call-other-nested", ToolName: PlainName("nested-tool"),
		Kind: PayloadFunction, Input: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("other cell nested call error = %v", err)
	}
	release()
	mu.Lock()
	other := append([]map[string]any(nil), nested...)[len(captured)]
	mu.Unlock()
	if other[OriginItemIDContextKey] != "fc-other" || other[OriginWindowIDContextKey] != "thread-1:1" {
		t.Fatalf("second cell origin = %#v/%#v", other[OriginItemIDContextKey], other[OriginWindowIDContextKey])
	}
}

// The retained origin is released with its cell.
func TestCodeModeForgottenCellDropsItsOrigin(t *testing.T) {
	runtime := NewCodeModeRuntime(nil, false)
	defer func() { _ = runtime.Close() }()
	runtime.SetTurnWindowID("thread-1:0")
	delegate := &codeModeRemoteDelegate{exec: runtime.exec}
	release := delegate.begin(&Invocation{CallID: "call-exec", Context: map[string]any{OriginItemIDContextKey: "fc-before"}})
	defer release()
	origin := runtime.exec.cellOrigin("cell-1", &Invocation{CallID: "call-exec", Context: map[string]any{OriginItemIDContextKey: "fc-before"}})
	if origin.itemID != "fc-before" || origin.windowID != "thread-1:0" {
		t.Fatalf("recorded origin = %#v", origin)
	}
	runtime.exec.forgetRemoteCell("cell-1")
	if dropped := runtime.exec.cellOrigin("cell-1", &Invocation{CallID: "call-exec", Context: map[string]any{OriginItemIDContextKey: "fc-new"}}); dropped.itemID != "fc-new" {
		t.Fatalf("origin after forget = %#v", dropped)
	}
}

// Mirrors Rust #45535's child-call facts: every nested call the code mode
// dispatches is reported with the cell it belongs to, which is the evidence the
// app-server classifies inner tool calls with.
func TestCodeModeNestedCallsAreObservedLikeRust(t *testing.T) {
	runtime := NewCodeModeRuntime(nil, false)
	defer func() { _ = runtime.Close() }()
	registry := NewRegistry()
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("nested-echo")}, func(context.Context, *Invocation) (*Output, error) {
		return &Output{Success: true, Body: "ok"}, nil
	})); err != nil {
		t.Fatalf("register nested-echo: %v", err)
	}
	_, _ = runtime.Executors(registry)
	var observed []CodeModeCallObservation
	runtime.SetCallObserver(func(observation CodeModeCallObservation) {
		observed = append(observed, observation)
	})
	delegate := &codeModeRemoteDelegate{exec: runtime.exec}
	release := delegate.begin(&Invocation{CallID: "call-exec"})
	defer release()
	if _, err := delegate.Invoke(context.Background(), CodeModeRemoteNestedCall{
		CellID: "cell-observed", RuntimeToolCallID: "nested-observed", ToolName: PlainName("nested-echo"),
		Kind: PayloadFunction, Input: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("nested call error = %v", err)
	}
	want := CodeModeCallObservation{Kind: CodeModeCallChildStarted, CellID: "cell-observed", CallID: "nested-observed"}
	if len(observed) != 1 || observed[0] != want {
		t.Fatalf("observed nested calls = %#v", observed)
	}
}

// Mirrors Rust's CodeModeToolCallFact stream: the exec call that created a cell is
// reported as the cell's parent, a dispatched nested call is reported with its
// cell, and the cell's completion is reported so its correlation state can be
// dropped.
func TestCodeModeCallObservationsFollowRustFacts(t *testing.T) {
	registry := NewRegistry()
	if err := registry.Register(NewExecutorFunc(Spec{Name: PlainName("nested-echo")}, func(context.Context, *Invocation) (*Output, error) {
		return &Output{Success: true, Body: "ok"}, nil
	})); err != nil {
		t.Fatalf("register nested-echo: %v", err)
	}
	remote := &yieldedCodeModeSession{
		executeResponse: CodeModeRemoteResponse{CellID: "observed-cell", State: "yielded"},
		settleResponse:  CodeModeRemoteResponse{CellID: "observed-cell", State: "completed"},
	}
	provider := &recordingCodeModeRemoteProvider{session: remote}
	runtime := NewCodeModeRuntime(provider, false)
	defer func() { _ = runtime.Close() }()
	exec, _ := runtime.Executors(registry)
	inner, ok := exec.(*codeModeExecExecutor)
	if !ok {
		t.Fatalf("executor type = %T", exec)
	}
	var observed []CodeModeCallObservation
	runtime.SetCallObserver(func(observation CodeModeCallObservation) {
		observed = append(observed, observation)
	})

	if _, err := exec.Execute(context.Background(), &Invocation{
		CallID:  "exec-observed",
		Payload: Payload{Kind: PayloadCustom, Input: `text("RUN")`},
	}); err != nil {
		t.Fatalf("exec error = %v", err)
	}
	delegate, _ := inner.remoteDelegate()
	if delegate == nil {
		t.Fatal("remote delegate is unavailable")
	}
	if _, err := delegate.Invoke(context.Background(), CodeModeRemoteNestedCall{
		CellID: "observed-cell", RuntimeToolCallID: "nested-observed", ToolName: PlainName("nested-echo"),
		Kind: PayloadFunction, Input: json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("nested call error = %v", err)
	}
	inner.forgetRemoteCell("observed-cell")

	want := []CodeModeCallObservation{
		{Kind: CodeModeCallCellStarted, CellID: "observed-cell", ParentCallID: "exec-observed"},
		{Kind: CodeModeCallChildStarted, CellID: "observed-cell", CallID: "nested-observed"},
		{Kind: CodeModeCallCellClosed, CellID: "observed-cell"},
	}
	if len(observed) != len(want) {
		t.Fatalf("observations = %#v, want %#v", observed, want)
	}
	for index := range want {
		if observed[index] != want[index] {
			t.Fatalf("observation %d = %#v, want %#v", index, observed[index], want[index])
		}
	}
}
