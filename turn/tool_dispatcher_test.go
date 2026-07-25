package turn

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/tool"
)

func TestToolDispatcherExecutesFunctionAndCustomCalls(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		var args struct {
			Text string `json:"text"`
		}
		if err := invocation.DecodeArguments(&args); err != nil {
			return nil, err
		}
		return &tool.Output{Success: true, Body: args.Text}, nil
	})); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName(tool.DefaultApplyPatchToolName)}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		if invocation.Payload.Kind != tool.PayloadCustom || invocation.Payload.Input != "patch" {
			t.Fatalf("custom payload = %#v", invocation.Payload)
		}
		return &tool.Output{Success: true, Body: "patched"}, nil
	})); err != nil {
		t.Fatalf("register custom: %v", err)
	}
	dispatcher := NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)})

	results, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{
		{Type: "agent_message", Text: "ignore"},
		{ID: "call-1", Type: "function_call", Name: "echo", CallID: "call-1", Arguments: `{"text":"hi"}`},
		{ID: "call-2", Type: "custom_tool_call", Name: tool.DefaultApplyPatchToolName, CallID: "call-2", Input: "patch"},
	})
	if err != nil {
		t.Fatalf("ExecuteToolItems() error = %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("results = %#v", results)
	}
	if results[0].Response.Type != "function_call_output" || results[0].Response.Output.Text() != "hi" {
		t.Fatalf("function response = %#v", results[0].Response)
	}
	if results[1].Response.Type != "custom_tool_call_output" || results[1].Response.Output.Text() != "patched" {
		t.Fatalf("custom response = %#v", results[1].Response)
	}
}

func TestToolDispatcherAddsThreadAndTurnContextToInvocations(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("inspect")}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		if invocation.Context["thread_id"] != "thread-parent" || invocation.Context["threadId"] != "thread-parent" {
			t.Fatalf("thread context = %#v", invocation.Context)
		}
		if invocation.Context["turn_id"] != "turn-1" || invocation.Context["turnId"] != "turn-1" {
			t.Fatalf("turn context = %#v", invocation.Context)
		}
		return &tool.Output{Success: true, Body: "ok"}, nil
	})); err != nil {
		t.Fatalf("register inspect: %v", err)
	}
	dispatcher := NewToolDispatcher(&ToolDispatcherOptions{
		Router:   tool.NewRouter(registry),
		ThreadID: "thread-parent",
		TurnID:   "turn-1",
	})

	results, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{{
		ID:        "call-1",
		Type:      "function_call",
		Name:      "inspect",
		CallID:    "call-1",
		Arguments: `{}`,
	}})
	if err != nil {
		t.Fatalf("ExecuteToolItems() error = %v", err)
	}
	if len(results) != 1 || results[0].Output.Body != "ok" {
		t.Fatalf("results = %#v", results)
	}
}

func TestToolDispatcherToolSearchOutput(t *testing.T) {
	registry := tool.NewRegistry()
	if err := tool.RegisterToolSearchHandler(registry, []tool.Spec{{
		Name:                 tool.NamespacedName("drive", "create_doc"),
		Description:          "Create Google Docs",
		NamespaceDescription: "Drive tools",
		Exposure:             tool.ExposureDiscoverable,
		Search:               &tool.SearchInfo{Text: "google docs create"},
	}}); err != nil {
		t.Fatalf("RegisterToolSearchHandler() error = %v", err)
	}
	dispatcher := NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)})

	results, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{{
		ID:        "search-1",
		Type:      "tool_search_call",
		CallID:    "search-1",
		Execution: "client",
		Search:    map[string]any{"query": "google docs"},
	}})
	if err != nil {
		t.Fatalf("ExecuteToolItems() error = %v", err)
	}
	if len(results) != 1 || results[0].Response.Type != "tool_search_output" {
		t.Fatalf("results = %#v", results)
	}
	if len(results[0].Response.Tools) != 1 {
		t.Fatalf("tools = %#v", results[0].Response.Tools)
	}
	data, err := json.Marshal(results[0].Response)
	if err != nil {
		t.Fatalf("Marshal response: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal response: %v", err)
	}
	if payload["type"] != "tool_search_output" || payload["call_id"] != "search-1" || payload["execution"] != "client" {
		t.Fatalf("response payload = %#v", payload)
	}
	tools := payload["tools"].([]any)
	if len(tools) != 1 {
		t.Fatalf("response tools = %#v", payload["tools"])
	}
	namespace, ok := tools[0].(map[string]any)
	if !ok || namespace["type"] != "namespace" || namespace["name"] != "drive" {
		t.Fatalf("response namespace = %#v", tools[0])
	}
	children, ok := namespace["tools"].([]any)
	if !ok || len(children) != 1 {
		t.Fatalf("response namespace tools = %#v", namespace["tools"])
	}
	child, ok := children[0].(map[string]any)
	if !ok || child["type"] != "function" || child["name"] != "create_doc" || child["defer_loading"] != true {
		t.Fatalf("response namespace child = %#v", children[0])
	}
}

func TestToolResponseItemMarshalRustRequiredFields(t *testing.T) {
	data, err := json.Marshal(&ToolResponseItem{Type: "tool_search_output"})
	if err != nil {
		t.Fatalf("Marshal tool search response: %v", err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal tool search response: %v", err)
	}
	if payload["call_id"] != nil || payload["status"] != "completed" || payload["execution"] != "client" {
		t.Fatalf("tool search response payload = %#v", payload)
	}
	if tools, ok := payload["tools"].([]any); !ok || len(tools) != 0 {
		t.Fatalf("tools = %#v", payload["tools"])
	}

	data, err = json.Marshal(&ToolResponseItem{Type: "function_call_output", Output: NewFunctionCallOutputPayload("ok", nil)})
	if err != nil {
		t.Fatalf("Marshal function response: %v", err)
	}
	payload = map[string]any{}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal function response: %v", err)
	}
	if _, ok := payload["call_id"]; !ok || payload["output"] != "ok" {
		t.Fatalf("function response payload = %#v", payload)
	}
}

func TestToolResponseFromOutputPrefersFormattedBodyLikeRust(t *testing.T) {
	invocation := &tool.Invocation{
		CallID:   "call-shell",
		ToolName: tool.PlainName(tool.DefaultExecCommandToolName),
		Payload:  tool.Payload{Kind: tool.PayloadFunction},
	}
	response := ToolResponseFromOutput(invocation, &tool.Output{
		Success: true,
		Body:    "Exit code: 0\nWall time: 0.1 seconds\nOutput:\nhello",
		Data:    map[string]any{"output": "hello"},
	})
	if response == nil || response.Output == nil || response.Output.Text() != "Exit code: 0\nWall time: 0.1 seconds\nOutput:\nhello" {
		t.Fatalf("response = %#v", response)
	}
	data, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatal(err)
	}
	items, ok := payload["output"].([]any)
	if !ok || len(items) != 1 || items[0].(map[string]any)["type"] != "input_text" {
		t.Fatalf("wire payload = %#v", payload)
	}
}

func TestToolResponseFromOutputStillPrefersContentItems(t *testing.T) {
	items := []FunctionCallOutputContentItem{{Type: "input_text", Text: "structured"}}
	invocation := &tool.Invocation{CallID: "call-media", Payload: tool.Payload{Kind: tool.PayloadFunction}}
	response := ToolResponseFromOutput(invocation, &tool.Output{
		Success: true,
		Body:    "fallback",
		Data:    map[string]any{"content_items": items},
	})
	if response == nil || response.Output == nil || response.Output.Text() != "structured" {
		t.Fatalf("response = %#v", response)
	}
}

func TestToolDispatcherRespondToModelBecomesFailedOutput(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("reject")}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		return nil, tool.RespondToModel("bad args")
	})); err != nil {
		t.Fatalf("register reject: %v", err)
	}
	dispatcher := NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)})

	results, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{{
		ID:        "call-1",
		Type:      "function_call",
		Name:      "reject",
		Arguments: `{}`,
	}})
	if err != nil {
		t.Fatalf("ExecuteToolItems() error = %v", err)
	}
	if len(results) != 1 || results[0].Output.Success {
		t.Fatalf("result = %#v", results)
	}
	if results[0].Response.Output.Text() != "bad args" || results[0].Response.Output.Success == nil || *results[0].Response.Output.Success {
		t.Fatalf("response = %#v", results[0].Response.Output)
	}
}

func TestToolDispatcherApplyPatchVerificationFailureIsNotFileChange(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName(tool.DefaultApplyPatchToolName)}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		return nil, tool.RespondToModel("apply_patch verification failed: missing context")
	})); err != nil {
		t.Fatalf("register apply_patch: %v", err)
	}
	dispatcher := NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)})

	results, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{{
		ID: "patch-1", Type: "custom_tool_call", Name: tool.DefaultApplyPatchToolName,
		CallID: "patch-1", Input: "*** Begin Patch\n*** Update File: state.txt\n@@\n-MISSING\n+VALUE\n*** End Patch",
	}})
	if err != nil {
		t.Fatalf("ExecuteToolItems() error = %v", err)
	}
	if len(results) != 1 || results[0].Output.Success {
		t.Fatalf("results = %#v", results)
	}
	if results[0].Output.Data != nil {
		t.Fatalf("verification failure data = %#v, want nil so no file_change event is emitted", results[0].Output.Data)
	}
	if got := results[0].Response.Output.Text(); !strings.Contains(got, "missing context") {
		t.Fatalf("response output = %q", got)
	}
}

func TestToolDispatcherMissingToolBecomesFailedOutput(t *testing.T) {
	dispatcher := NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(tool.NewRegistry())})

	results, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{{
		ID:        "call-1",
		Type:      "function_call",
		Name:      "missing",
		CallID:    "call-1",
		Arguments: `{}`,
	}})
	if err != nil {
		t.Fatalf("ExecuteToolItems() error = %v", err)
	}
	if len(results) != 1 || results[0].Output.Success {
		t.Fatalf("results = %#v", results)
	}
	if !strings.Contains(results[0].Response.Output.Text(), "tool not found: missing") {
		t.Fatalf("response = %#v", results[0].Response.Output)
	}
}

func TestToolDispatcherInvalidJSONArgumentsBecomeFailedOutput(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("decode")}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		var args struct {
			Text string `json:"text"`
		}
		if err := invocation.DecodeArguments(&args); err != nil {
			return nil, err
		}
		return &tool.Output{Success: true, Body: args.Text}, nil
	})); err != nil {
		t.Fatalf("register decode: %v", err)
	}
	dispatcher := NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)})

	results, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{{
		ID:        "call-1",
		Type:      "function_call",
		Name:      "decode",
		CallID:    "call-1",
		Arguments: `{`,
	}})
	if err != nil {
		t.Fatalf("ExecuteToolItems() error = %v", err)
	}
	if len(results) != 1 || results[0].Output.Success {
		t.Fatalf("results = %#v", results)
	}
	if !strings.Contains(results[0].Response.Output.Text(), "unexpected end of JSON input") {
		t.Fatalf("response = %#v", results[0].Response.Output)
	}
}

func TestToolDispatcherFatalErrorReturnsError(t *testing.T) {
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("fatal")}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		return nil, tool.Fatal("disk failed")
	})); err != nil {
		t.Fatalf("register fatal: %v", err)
	}
	dispatcher := NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)})

	_, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{{
		ID:        "call-1",
		Type:      "function_call",
		Name:      "fatal",
		Arguments: `{}`,
	}})
	if err == nil || IsRespondToModelError(err) || !errors.Is(err, tool.Fatal("disk failed")) {
		t.Fatalf("error = %v", err)
	}
}

func TestToolDispatcherRunsParallelSupportedCallsConcurrently(t *testing.T) {
	registry := tool.NewRegistry()
	ready := make(chan string, 2)
	release := make(chan struct{})
	executor := tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("parallel"), Parallel: true}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		ready <- invocation.CallID
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &tool.Output{Success: true, Body: invocation.CallID}, nil
	})
	if err := registry.Register(executor); err != nil {
		t.Fatalf("register parallel: %v", err)
	}
	dispatcher := NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)})
	done := make(chan []ToolExecutionResult, 1)
	errs := make(chan error, 1)

	go func() {
		results, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{
			{ID: "call-1", Type: "function_call", Name: "parallel", CallID: "call-1", Arguments: `{}`},
			{ID: "call-2", Type: "function_call", Name: "parallel", CallID: "call-2", Arguments: `{}`},
		})
		if err != nil {
			errs <- err
			return
		}
		done <- results
	}()

	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case callID := <-ready:
			seen[callID] = true
		case err := <-errs:
			t.Fatalf("ExecuteToolItems() error before release = %v", err)
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("parallel calls did not both start: %#v", seen)
		}
	}
	close(release)
	select {
	case err := <-errs:
		t.Fatalf("ExecuteToolItems() error = %v", err)
	case results := <-done:
		if len(results) != 2 || results[0].Invocation.CallID != "call-1" || results[1].Invocation.CallID != "call-2" {
			t.Fatalf("results = %#v", results)
		}
	case <-time.After(time.Second):
		t.Fatalf("ExecuteToolItems() did not finish")
	}
}

func TestToolDispatcherTurnsAttributedCancelCauseIntoModelVisibleToolError(t *testing.T) {
	registry := tool.NewRegistry()
	executor := tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("exec_command")}, func(ctx context.Context, _ *tool.Invocation) (*tool.Output, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	})
	if err := registry.Register(executor); err != nil {
		t.Fatal(err)
	}
	dispatcher := NewToolDispatcher(&ToolDispatcherOptions{
		Router: tool.NewRouter(registry),
		OnToolStarted: func(_ context.Context, invocation *tool.Invocation, _ time.Time) {
			invocation.Cancel(tool.RespondToModel("Network access was blocked by policy."))
		},
	})
	results, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{{
		Type:      "function_call",
		CallID:    "call-network",
		Name:      "exec_command",
		Arguments: `{}`,
	}})
	if err != nil {
		t.Fatalf("ExecuteToolItems() error = %v", err)
	}
	if len(results) != 1 || results[0].Output.Success || results[0].Output.Body != "Network access was blocked by policy." {
		t.Fatalf("results = %#v", results)
	}
}

func TestToolDispatcherSerializesNonParallelCalls(t *testing.T) {
	registry := tool.NewRegistry()
	firstStarted := make(chan struct{})
	secondStarted := make(chan struct{})
	releaseFirst := make(chan struct{})
	var active int32
	var overlapped int32
	executor := tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("serial")}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		if atomic.AddInt32(&active, 1) > 1 {
			atomic.StoreInt32(&overlapped, 1)
		}
		defer atomic.AddInt32(&active, -1)
		if invocation.CallID == "call-1" {
			close(firstStarted)
			select {
			case <-releaseFirst:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		} else {
			close(secondStarted)
		}
		return &tool.Output{Success: true, Body: invocation.CallID}, nil
	})
	if err := registry.Register(executor); err != nil {
		t.Fatalf("register serial: %v", err)
	}
	dispatcher := NewToolDispatcher(&ToolDispatcherOptions{Router: tool.NewRouter(registry)})
	done := make(chan []ToolExecutionResult, 1)
	errs := make(chan error, 1)

	go func() {
		results, err := dispatcher.ExecuteToolItems(context.Background(), []model.AgentItem{
			{ID: "call-1", Type: "function_call", Name: "serial", CallID: "call-1", Arguments: `{}`},
			{ID: "call-2", Type: "function_call", Name: "serial", CallID: "call-2", Arguments: `{}`},
		})
		if err != nil {
			errs <- err
			return
		}
		done <- results
	}()

	select {
	case <-firstStarted:
	case err := <-errs:
		t.Fatalf("ExecuteToolItems() error before first start = %v", err)
	case <-time.After(500 * time.Millisecond):
		t.Fatalf("first call did not start")
	}
	select {
	case <-secondStarted:
		t.Fatalf("second non-parallel call started while first was still running")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirst)
	select {
	case err := <-errs:
		t.Fatalf("ExecuteToolItems() error = %v", err)
	case results := <-done:
		if atomic.LoadInt32(&overlapped) != 0 {
			t.Fatalf("non-parallel tool calls overlapped")
		}
		if len(results) != 2 || results[0].Invocation.CallID != "call-1" || results[1].Invocation.CallID != "call-2" {
			t.Fatalf("results = %#v", results)
		}
	case <-time.After(time.Second):
		t.Fatalf("ExecuteToolItems() did not finish")
	}
}

func TestFunctionCallOutputPayloadSerializesAsWireBody(t *testing.T) {
	ok := true
	payload := NewFunctionCallOutputPayload([]FunctionCallOutputContentItem{{Type: "input_text", Text: "hello"}}, &ok)
	data, err := json.Marshal(struct {
		Output *FunctionCallOutputPayload `json:"output"`
	}{Output: payload})
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if string(data) != `{"output":[{"type":"input_text","text":"hello"}]}` {
		t.Fatalf("json = %s", data)
	}
	if payload.Text() != "hello" {
		t.Fatalf("Text() = %q", payload.Text())
	}
}

func TestMCPEncryptedContentSerializesAsFunctionOutputItemsLikeRust(t *testing.T) {
	invocation := &tool.Invocation{
		CallID:   "encrypted-1",
		ToolName: tool.NamespacedName("rmcp", "encrypted_output"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: "{}"},
	}
	response := ToolResponseFromOutput(invocation, &tool.Output{
		Success: true,
		Data: map[string]any{"content_items": []any{
			map[string]any{"type": "input_text", "text": "Lookup completed"},
			map[string]any{"type": "encrypted_content", "encrypted_content": "gAAAA-test"},
		}},
	})
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var wire map[string]any
	if err := json.Unmarshal(encoded, &wire); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	items, ok := wire["output"].([]any)
	if !ok || len(items) != 2 {
		t.Fatalf("output = %#v", wire["output"])
	}
	encrypted, _ := items[1].(map[string]any)
	if encrypted["type"] != "encrypted_content" || encrypted["encrypted_content"] != "gAAAA-test" {
		t.Fatalf("encrypted output = %#v", encrypted)
	}
}
