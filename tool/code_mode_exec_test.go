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
	if output.Body != "ALPHA" || !output.Success {
		t.Fatalf("output = %#v", output)
	}
	commands, ok := output.Data["nested_commands"].([]string)
	if !ok || len(commands) != 1 || !strings.Contains(commands[0], "ALPHA") {
		t.Fatalf("nested commands = %#v", output.Data["nested_commands"])
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
	if output.Body != "WEATHER_LEGACY_OK" || runner.request == nil || runner.request.HookCommand != "Write-Output WEATHER_LEGACY_OK" {
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
	if output.Body != "recovered\n[\"one\",\"two\"]" {
		t.Fatalf("body = %q", output.Body)
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

	_, err := NewCodeModeExecExecutor(registry).Execute(context.Background(), &Invocation{
		CallID: "uncaught-shell-failure",
		Payload: Payload{Kind: PayloadCustom, Input: `
			await tools.shell_command({command: "fail"});
			await tools.shell_command({command: "must-not-run"});
		`},
	})
	if err == nil || !strings.Contains(err.Error(), "controlled failure") {
		t.Fatalf("error = %v", err)
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
	if output.Body != "false\nbusiness rule rejected" {
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
	if output.Body != "CAUGHT_FAILURE\nRECOVERY_OK" {
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
			if _, err := executor.Execute(context.Background(), &Invocation{CallID: name, Payload: Payload{Kind: PayloadCustom, Input: source}}); err == nil {
				t.Fatal("expected JavaScript error")
			}
		})
	}
	output, err := executor.Execute(context.Background(), &Invocation{CallID: "recovery", Payload: Payload{Kind: PayloadCustom, Input: `text("RECOVERED")`}})
	if err != nil || output.Body != "RECOVERED" {
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
	if output.Body != "true" {
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
	if second.Body != "3" {
		t.Fatalf("stored body = %q", second.Body)
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
	if output.Body != "timer-complete" {
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
	if finished.Body != "done" {
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
	if finished.Body != "third" {
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
	if !strings.HasPrefix(output.Body, "Warning: truncated output (original token count: 10)") || !strings.Contains(output.Body, "tokens truncated") {
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
	_, err := NewCodeModeExecExecutor(registry).Execute(ctx, &Invocation{CallID: "pending-limit", Payload: Payload{Kind: PayloadCustom, Input: source}})
	if err == nil || !strings.Contains(err.Error(), "1024 pending delegate calls") {
		t.Fatalf("error = %v", err)
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
	if output.Body != "final" || strings.Contains(output.Body, "ping") {
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
	if output.Body != "REMOTE_OK" || remote.executeCalls != 1 {
		t.Fatalf("output = %#v execute calls = %d", output, remote.executeCalls)
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
	var callErr *FunctionCallError
	if output != nil || !AsFunctionCallError(err, &callErr) || callErr.IsFatal() || !strings.Contains(callErr.ModelMessage(), "apply_patch verification failed") {
		t.Fatalf("output = %#v error = %v call error = %#v", output, err, callErr)
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
	var callErr *FunctionCallError
	if output != nil || !AsFunctionCallError(err, &callErr) || callErr.IsFatal() || !strings.Contains(callErr.ModelMessage(), "asynchronous execution failed") {
		t.Fatalf("output = %#v error = %v call error = %#v", output, err, callErr)
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
}

func (s *recordingCodeModeRemoteSession) Execute(context.Context, CodeModeRemoteExecuteRequest) (CodeModeRemoteResponse, error) {
	s.executeCalls++
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
