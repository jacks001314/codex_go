package tool

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
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

func TestCodeModeExecDescriptionWarnsConsoleIsUnavailableLikeRust(t *testing.T) {
	description := NewCodeModeExecExecutor(NewRegistry()).Spec().Description
	for _, phrase := range []string{"no Node", "no file system", "no network access", "no console"} {
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

func mustJSON(t *testing.T, value string) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

type recordingShellRunner struct{ output string }

func (r *recordingShellRunner) Run(context.Context, *ShellRequest) (*ShellResult, error) {
	return &ShellResult{Stdout: r.output, ExitCode: 0, HasExitCode: true}, nil
}
