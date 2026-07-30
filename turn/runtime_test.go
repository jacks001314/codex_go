package turn

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"codex_go/codexapi"
	"codex_go/model"
	"codex_go/tool"
)

func TestRuntimeExecutesRecoveredCustomToolCallOnceAndReturnsSingleFinal(t *testing.T) {
	const javascript = `text("RECOVERED")`
	var requestMu sync.Mutex
	requestCount := 0
	var secondRequest map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("Decode request body: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		requestMu.Lock()
		requestCount++
		current := requestCount
		if current == 2 {
			secondRequest = body
		}
		requestMu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		if current == 1 {
			_, _ = w.Write([]byte(strings.Join([]string{
				`data: {"type":"response.created","response":{"id":"resp-1"}}`,
				`data: {"type":"response.output_item.added","item":{"id":"ctc_1","type":"function_call","call_id":"call-1","name":"exec","arguments":""}}`,
				`data: {"type":"response.custom_tool_call_input.delta","item_id":"ctc_1","call_id":"call-1","delta":"text(\"RECOVERED\")"}`,
				`data: {"type":"response.output_item.done","item":{"id":"ctc_1","type":"function_call","call_id":"call-1","name":"exec","arguments":""}}`,
				`data: {"type":"response.completed","response":{"id":"resp-1","usage":{"input_tokens":1,"output_tokens":1,"total_tokens":2}}}`,
			}, "\n\n") + "\n\n"))
			return
		}
		_, _ = w.Write([]byte(strings.Join([]string{
			`data: {"type":"response.created","response":{"id":"resp-2"}}`,
			`data: {"type":"response.output_item.done","item":{"id":"msg-1","type":"message","role":"assistant","content":[{"type":"output_text","text":"DONE"}]}}`,
			`data: {"type":"response.completed","response":{"id":"resp-2","usage":{"input_tokens":2,"output_tokens":1,"total_tokens":3}}}`,
		}, "\n\n") + "\n\n"))
	}))
	defer server.Close()

	var execMu sync.Mutex
	execCalls := 0
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{
		Name:     tool.PlainName(tool.CodeModeExecToolName),
		Freeform: &tool.FreeformSpec{Syntax: "lark", Definition: "start: /[\\s\\S]+/"},
	}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		execMu.Lock()
		defer execMu.Unlock()
		execCalls++
		if invocation.Payload.Kind != tool.PayloadCustom || invocation.Payload.Input != javascript {
			t.Fatalf("exec invocation = %#v", invocation)
		}
		return &tool.Output{Success: true, Body: "RECOVERED"}, nil
	})); err != nil {
		t.Fatalf("register exec: %v", err)
	}

	runner := model.NewResponsesAgentRunner(&model.ResponsesAgentOptions{
		Provider: &model.APIProvider{BaseURL: server.URL},
		Stream:   true,
		ModelsManager: model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{{
			Slug:             "gpt-test",
			UseResponsesLite: true,
		}}}),
	})
	runtime := NewRuntime(&RuntimeOptions{Agent: runner, Router: tool.NewRouter(registry), MaxTurns: 3})
	result, err := runtime.Run(context.Background(), &AgentLoopRequest{Prompt: "run exec", Model: "gpt-test"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	requestMu.Lock()
	gotRequestCount := requestCount
	second := secondRequest
	requestMu.Unlock()
	execMu.Lock()
	gotExecCalls := execCalls
	execMu.Unlock()
	if gotRequestCount != 2 || gotExecCalls != 1 {
		t.Fatalf("requests=%d execCalls=%d result=%#v", gotRequestCount, gotExecCalls, result)
	}
	if result.Iterations != 2 || len(result.ToolExecutions) != 1 || result.Response.Message != "DONE" || len(ResponseAssistantMessages(result.Response)) != 1 {
		t.Fatalf("result = %#v", result)
	}
	encodedSecond, err := json.Marshal(second["input"])
	if err != nil {
		t.Fatalf("Marshal second input: %v", err)
	}
	inputJSON := string(encodedSecond)
	if !strings.Contains(inputJSON, `"type":"custom_tool_call"`) || !strings.Contains(inputJSON, `"input":"text(\"RECOVERED\")"`) || !strings.Contains(inputJSON, `"type":"custom_tool_call_output"`) {
		t.Fatalf("second request input = %s", inputJSON)
	}
	if strings.Contains(inputJSON, `"type":"function_call","name":"exec"`) {
		t.Fatalf("second request retained malformed exec call = %s", inputJSON)
	}
}

func TestRuntimeInjectsToolsAndRunsLoop(t *testing.T) {
	agent := &runtimeRecordingAgent{}
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{
		Name:        tool.PlainName("echo"),
		Description: "Echo text",
		InputSchema: map[string]any{
			"type": "object",
		},
	}, func(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{Success: true, Body: "ok"}, nil
	})); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	runtime := NewRuntime(&RuntimeOptions{
		Agent:  agent,
		Router: tool.NewRouter(registry),
	})
	attestationProvider := codexapi.NewStaticAttestationProvider("attest")

	result, err := runtime.Run(context.Background(), &AgentLoopRequest{
		Prompt:              "use tool",
		Instructions:        "runtime instructions",
		Originator:          "codex_vscode",
		AttestationProvider: attestationProvider,
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Response.Message != "done" || len(result.ToolExecutions) != 1 {
		t.Fatalf("result = %#v", result)
	}
	if len(agent.requests) != 2 || len(agent.requests[0].Tools) != 1 || len(agent.requests[1].Tools) != 1 {
		t.Fatalf("requests = %#v", agent.requests)
	}
	if agent.requests[0].Instructions != "runtime instructions" || agent.requests[1].Instructions != "runtime instructions" {
		t.Fatalf("instructions = %q/%q", agent.requests[0].Instructions, agent.requests[1].Instructions)
	}
	if agent.requests[0].Originator != "codex_vscode" || agent.requests[1].Originator != "codex_vscode" {
		t.Fatalf("originator = %q/%q", agent.requests[0].Originator, agent.requests[1].Originator)
	}
	if agent.requests[0].AttestationProvider != attestationProvider || agent.requests[1].AttestationProvider != attestationProvider {
		t.Fatalf("attestation providers not preserved")
	}
}

func TestRuntimeAugmentsWebRunDescriptionOnlyWithCodeMode(t *testing.T) {
	for _, testCase := range []struct {
		name     string
		codeMode bool
	}{
		{name: "code_mode", codeMode: true},
		{name: "direct_tools"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			agent := &singleTurnAgent{response: &model.AgentResponse{
				Message: "done",
				Items:   []model.AgentItem{{ID: "msg-1", Type: "agent_message", Text: "done"}},
			}}
			registry := tool.NewRegistry()
			if err := registry.Register(tool.NewExecutorFunc(tool.Spec{
				Name:                 tool.NamespacedName(WebSearchNamespace, WebSearchRunTool),
				Description:          "Search the web",
				NamespaceDescription: "Tool for accessing the internet.",
				InputSchema: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"weather": map[string]any{
							"type": "array",
							"items": map[string]any{
								"type":       "object",
								"properties": map[string]any{"location": map[string]any{"type": "string"}},
								"required":   []string{"location"},
							},
						},
					},
				},
			}, nil)); err != nil {
				t.Fatal(err)
			}
			if testCase.codeMode {
				exec, wait := tool.NewCodeModeExecutors(registry)
				if err := registry.Register(exec); err != nil {
					t.Fatal(err)
				}
				if err := registry.Register(wait); err != nil {
					t.Fatal(err)
				}
			}
			runtime := NewRuntime(&RuntimeOptions{Agent: agent, Router: tool.NewRouter(registry)})
			if _, err := runtime.Run(context.Background(), &AgentLoopRequest{Prompt: "weather"}); err != nil {
				t.Fatal(err)
			}
			description := runtimeNamespacedToolDescription(agent.requests[0].Tools, WebSearchNamespace, WebSearchRunTool)
			if testCase.codeMode && !strings.Contains(description, "declare const tools: { web__run(args:") {
				t.Fatalf("code-mode description = %q", description)
			}
			if !testCase.codeMode && description != "Search the web" {
				t.Fatalf("direct description = %q", description)
			}
		})
	}
}

func TestRuntimeStandaloneWebSearchRegisteredFollowsWinningExecutor(t *testing.T) {
	name := tool.NamespacedName(WebSearchNamespace, WebSearchRunTool)
	for _, testCase := range []struct {
		name       string
		first      tool.Executor
		standalone bool
	}{
		{name: "standalone wins", first: NewWebSearchHandler(&WebSearchOptions{}), standalone: true},
		{name: "external collision wins", first: tool.NewExecutorFunc(tool.Spec{Name: name, Description: "MCP winner"}, nil)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			registry := tool.NewRegistry()
			if registered, err := registry.RegisterExternal(testCase.first); err != nil || !registered {
				t.Fatalf("register first = %t, %v", registered, err)
			}
			if _, err := registry.RegisterExternal(NewWebSearchHandler(&WebSearchOptions{})); err != nil {
				t.Fatal(err)
			}
			runtime := NewRuntime(&RuntimeOptions{Router: tool.NewRouter(registry)})
			if got := runtime.StandaloneWebSearchRegistered(); got != testCase.standalone {
				t.Fatalf("StandaloneWebSearchRegistered() = %t, want %t", got, testCase.standalone)
			}
		})
	}
}

func TestRuntimeCodeModeOnlyKeepsOnlyExecWaitAndDirectModelToolsVisible(t *testing.T) {
	agent := &singleTurnAgent{response: &model.AgentResponse{
		Message: "done",
		Items:   []model.AgentItem{{ID: "msg-1", Type: "agent_message", Text: "done"}},
	}}
	registry := tool.NewRegistry()
	for _, spec := range []tool.Spec{
		{Name: tool.PlainName("apply_patch")},
		{Name: tool.PlainName("update_plan")},
		{Name: tool.PlainName("request_user_input"), Exposure: tool.ExposureDirectModelOnly},
		{Name: tool.NamespacedName("collaboration", "spawn_agent"), Exposure: tool.ExposureDirectModelOnly, NamespaceDescription: "Agent tools"},
	} {
		if err := registry.Register(tool.NewExecutorFunc(spec, func(context.Context, *tool.Invocation) (*tool.Output, error) {
			return &tool.Output{Success: true}, nil
		})); err != nil {
			t.Fatal(err)
		}
	}
	execTool, waitTool := tool.NewCodeModeExecutors(registry)
	if err := registry.Register(execTool); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(waitTool); err != nil {
		t.Fatal(err)
	}

	runtime := NewRuntime(&RuntimeOptions{Agent: agent, Router: tool.NewRouter(registry)})
	if _, err := runtime.Run(context.Background(), &AgentLoopRequest{Prompt: "edit", ToolMode: model.ToolModeCodeModeOnly}); err != nil {
		t.Fatal(err)
	}
	visible := runtimeRequestToolNames(agent.requests[0].Tools)
	for _, want := range []string{"exec", "wait", "request_user_input", "collaboration.spawn_agent"} {
		if !visible[want] {
			t.Fatalf("visible tools = %#v, missing %q", visible, want)
		}
	}
	for _, hidden := range []string{"apply_patch", "update_plan"} {
		if visible[hidden] {
			t.Fatalf("visible tools = %#v, unexpectedly contains %q", visible, hidden)
		}
	}
	nested := tool.NewRouter(registry).CodeModeToolNames()
	if _, ok := nested["apply_patch"]; !ok {
		t.Fatalf("nested tools = %#v, missing apply_patch", nested)
	}
	for _, directOnly := range []string{"request_user_input", "collaboration__spawn_agent"} {
		if _, ok := nested[directOnly]; ok {
			t.Fatalf("nested tools = %#v, contains direct-only %q", nested, directOnly)
		}
	}
	execDescription := runtimeToolDescription(agent.requests[0].Tools, "exec")
	if !strings.Contains(execDescription, "### `apply_patch`") || !strings.Contains(execDescription, "declare const tools: { apply_patch(args: unknown): Promise<unknown>; };") {
		t.Fatalf("exec description = %q", execDescription)
	}
	for _, directOnly := range []string{"request_user_input", "collaboration__spawn_agent"} {
		if strings.Contains(execDescription, "declare const tools: { "+directOnly+"(") {
			t.Fatalf("exec description contains direct-only tool %q: %q", directOnly, execDescription)
		}
	}
}

func TestRuntimeWithoutRouterPreservesPromptAndResponseInputItems(t *testing.T) {
	agent := &singleTurnAgent{response: &model.AgentResponse{
		Message: "assistant answer",
		Usage:   model.AgentUsage{InputTokens: 2, OutputTokens: 3},
		Items: []model.AgentItem{{
			ID:   "msg-1",
			Type: "agent_message",
			Text: "assistant answer",
		}},
	}}
	runtime := NewRuntime(&RuntimeOptions{Agent: agent})

	result, err := runtime.Run(context.Background(), &AgentLoopRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !resultInputItemsHaveText(result.InputItems, "hello") || !runtimeResultInputItemsHaveAgentText(result.InputItems, "assistant answer") {
		t.Fatalf("result input items = %#v", result.InputItems)
	}
	if result.Iterations != 1 || result.Usage.InputTokens != 2 || result.Usage.OutputTokens != 3 {
		t.Fatalf("result = %#v", result)
	}
	if result.TimingProfile == nil || result.TimingProfile.SamplingRequestCount != 1 {
		t.Fatalf("timing profile = %#v", result.TimingProfile)
	}
	if len(agent.requests) != 1 || agent.requests[0].Prompt != "hello" {
		t.Fatalf("agent requests = %#v", agent.requests)
	}
}

func TestRuntimeWithoutRouterRejectsToolCalls(t *testing.T) {
	agent := &singleTurnAgent{response: &model.AgentResponse{Items: []model.AgentItem{{
		ID:        "call-1",
		Type:      "function_call",
		Name:      "echo",
		CallID:    "call-1",
		Arguments: `{}`,
	}}}}
	runtime := NewRuntime(&RuntimeOptions{Agent: agent})

	_, err := runtime.Run(context.Background(), &AgentLoopRequest{Prompt: "call tool"})
	if err == nil || !strings.Contains(err.Error(), "tool dispatcher is nil") {
		t.Fatalf("Run() error = %v, want dispatcher failure", err)
	}
}

func TestRuntimeMergesHostedToolsBeforeAgentRequest(t *testing.T) {
	agent := &singleTurnAgent{response: &model.AgentResponse{
		Message: "ok",
		Items:   []model.AgentItem{{ID: "msg-1", Type: "agent_message", Text: "ok"}},
	}}
	runtime := NewRuntime(&RuntimeOptions{
		Agent:       agent,
		Router:      tool.NewRouter(tool.NewRegistry()),
		HostedTools: []any{HostedImageGenerationTool("png")},
	})

	if _, err := runtime.Run(context.Background(), &AgentLoopRequest{Prompt: "draw"}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(agent.requests) != 1 || !runtimeRequestToolsContainType(agent.requests[0].Tools, HostedImageGenerationToolType) {
		t.Fatalf("agent request tools = %#v", agent.requests)
	}
}

func TestRuntimeMergesPerRequestHostedToolsBeforeAgentRequest(t *testing.T) {
	agent := &singleTurnAgent{response: &model.AgentResponse{
		Message: "ok",
		Items:   []model.AgentItem{{ID: "msg-1", Type: "agent_message", Text: "ok"}},
	}}
	runtime := NewRuntime(&RuntimeOptions{
		Agent:  agent,
		Router: tool.NewRouter(tool.NewRegistry()),
	})

	if _, err := runtime.Run(context.Background(), &AgentLoopRequest{
		Prompt:      "draw",
		HostedTools: []any{HostedImageGenerationTool("png")},
	}); err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(agent.requests) != 1 || !runtimeRequestToolsContainType(agent.requests[0].Tools, HostedImageGenerationToolType) {
		t.Fatalf("agent request tools = %#v", agent.requests)
	}
}

func TestRuntimeAddsCodeModeToolNamesOnlyForResponsesLite(t *testing.T) {
	for _, tc := range []struct {
		name string
		lite bool
	}{
		{name: "lite", lite: true},
		{name: "non-lite"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			agent := &singleTurnAgent{response: &model.AgentResponse{Message: "ok"}}
			registry := tool.NewRegistry()
			if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("view_image")}, func(context.Context, *tool.Invocation) (*tool.Output, error) {
				return &tool.Output{Success: true}, nil
			})); err != nil {
				t.Fatal(err)
			}
			exec, wait := tool.NewCodeModeExecutors(registry)
			if err := registry.Register(exec); err != nil {
				t.Fatal(err)
			}
			if err := registry.Register(wait); err != nil {
				t.Fatal(err)
			}
			metadata := map[string]string{codexapi.ClientCodexTurnMetadataHeader: `{"thread_id":"thread-1","request_kind":"turn"}`}
			if tc.lite {
				metadata["ws_request_header_x_openai_internal_codex_responses_lite"] = "true"
			}
			runtime := NewRuntime(&RuntimeOptions{Agent: agent, Router: tool.NewRouter(registry)})
			if _, err := runtime.Run(context.Background(), &AgentLoopRequest{Prompt: "run", ClientMetadata: metadata}); err != nil {
				t.Fatal(err)
			}
			var turnMetadata map[string]any
			if err := json.Unmarshal([]byte(agent.requests[0].ClientMetadata[codexapi.ClientCodexTurnMetadataHeader]), &turnMetadata); err != nil {
				t.Fatal(err)
			}
			toolNames, present := turnMetadata[codexapi.CodeModeToolNamesKey].(map[string]any)
			if !tc.lite {
				if present {
					t.Fatalf("non-lite metadata = %#v", turnMetadata)
				}
				return
			}
			viewImage, ok := toolNames["view_image"].(map[string]any)
			if !present || !ok || viewImage["name"] != "view_image" || viewImage["namespace"] != nil {
				t.Fatalf("lite metadata = %#v", turnMetadata)
			}
			if _, legacy := agent.requests[0].ClientMetadata["x-codex-code-mode-tool-names"]; legacy {
				t.Fatalf("legacy code-mode metadata leaked: %#v", agent.requests[0].ClientMetadata)
			}
		})
	}
}

func TestRuntimePreservesCodeModeToolNamesAfterSteerMetadataUpdate(t *testing.T) {
	mailbox := NewSteerMailbox()
	agent := &fakeLoopAgent{enqueueSteerAfterFirstCall: true, steerMailbox: mailbox}
	registry := tool.NewRegistry()
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("view_image")}, func(context.Context, *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{Success: true}, nil
	})); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(tool.NewExecutorFunc(tool.Spec{Name: tool.PlainName("echo")}, func(context.Context, *tool.Invocation) (*tool.Output, error) {
		return &tool.Output{Success: true, Body: "ok"}, nil
	})); err != nil {
		t.Fatal(err)
	}
	exec, wait := tool.NewCodeModeExecutors(registry)
	if err := registry.Register(exec); err != nil {
		t.Fatal(err)
	}
	if err := registry.Register(wait); err != nil {
		t.Fatal(err)
	}
	runtime := NewRuntime(&RuntimeOptions{Agent: agent, Router: tool.NewRouter(registry), SteerMailbox: mailbox, MaxTurns: 3})
	baseMetadata := map[string]string{
		codexapi.ClientCodexTurnMetadataHeader:                     `{"thread_id":"thread-1","request_kind":"turn"}`,
		"ws_request_header_x_openai_internal_codex_responses_lite": "true",
	}
	if _, err := runtime.Run(context.Background(), &AgentLoopRequest{
		Prompt: "run", ThreadID: "thread-1", TurnID: "turn-1", SteerMailbox: mailbox, ClientMetadata: baseMetadata,
	}); err != nil {
		t.Fatal(err)
	}
	if len(agent.requests) != 2 {
		t.Fatalf("requests = %#v", agent.requests)
	}
	for i := range agent.requests {
		var metadata map[string]any
		if err := json.Unmarshal([]byte(agent.requests[i].ClientMetadata[codexapi.ClientCodexTurnMetadataHeader]), &metadata); err != nil {
			t.Fatalf("request %d metadata error = %v", i, err)
		}
		if _, ok := metadata[codexapi.CodeModeToolNamesKey]; !ok {
			t.Fatalf("request %d lost code-mode names: %#v", i, metadata)
		}
	}
}

type runtimeRecordingAgent struct {
	requests []model.AgentRequest
}

func (a *runtimeRecordingAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	a.requests = append(a.requests, *request)
	if len(a.requests) == 1 {
		return &model.AgentResponse{Items: []model.AgentItem{{
			ID:        "call-1",
			Type:      "function_call",
			Name:      "echo",
			CallID:    "call-1",
			Arguments: `{}`,
		}}}, nil
	}
	return &model.AgentResponse{
		Message: "done",
		Items:   []model.AgentItem{{ID: "msg-1", Type: "agent_message", Text: "done"}},
	}, nil
}

type singleTurnAgent struct {
	requests []model.AgentRequest
	response *model.AgentResponse
	err      error
}

func (a *singleTurnAgent) Run(ctx context.Context, request *model.AgentRequest) (*model.AgentResponse, error) {
	_ = ctx
	a.requests = append(a.requests, *request)
	if a.err != nil {
		return nil, a.err
	}
	if a.response != nil {
		return a.response, nil
	}
	return &model.AgentResponse{}, nil
}

func runtimeResultInputItemsHaveAgentText(items []any, want string) bool {
	for _, raw := range items {
		item, ok := raw.(*model.AgentItem)
		if ok && item.Text == want {
			return true
		}
	}
	return false
}

func runtimeRequestToolsContainType(tools []any, toolType string) bool {
	for _, toolValue := range tools {
		toolMap, ok := toolValue.(map[string]any)
		if ok && toolMap["type"] == toolType {
			return true
		}
	}
	return false
}

func runtimeRequestToolNames(tools []any) map[string]bool {
	out := map[string]bool{}
	for _, value := range tools {
		item, ok := value.(map[string]any)
		if !ok {
			continue
		}
		name, _ := item["name"].(string)
		if item["type"] != "namespace" {
			out[name] = true
			continue
		}
		children, _ := item["tools"].([]map[string]any)
		for _, child := range children {
			childName, _ := child["name"].(string)
			out[name+"."+childName] = true
		}
	}
	return out
}

func runtimeNamespacedToolDescription(tools []any, namespace string, name string) string {
	for _, value := range tools {
		namespaceTool, ok := value.(map[string]any)
		if !ok || namespaceTool["type"] != "namespace" || namespaceTool["name"] != namespace {
			continue
		}
		children, _ := namespaceTool["tools"].([]map[string]any)
		for _, child := range children {
			if child["name"] == name {
				description, _ := child["description"].(string)
				return description
			}
		}
	}
	return ""
}

func runtimeToolDescription(tools []any, name string) string {
	for _, value := range tools {
		item, ok := value.(map[string]any)
		if !ok || item["name"] != name {
			continue
		}
		description, _ := item["description"].(string)
		return description
	}
	return ""
}
