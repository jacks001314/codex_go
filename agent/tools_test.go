package agent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/tool"
)

func TestMultiAgentV2ToolContractMatchesRust(t *testing.T) {
	registry := tool.NewRegistry()
	controller := NewMemoryToolController()
	if err := RegisterMultiAgentHandlersWithOptions(registry, &MultiAgentHandlerOptions{
		Controller: controller, Exposure: tool.ExposureModelVisible, Version: VersionV2,
		Namespace: MultiAgentV2Namespace, WaitConfigured: true, WaitMin: 0, WaitMax: 100 * time.Millisecond,
		HideSpawnMetadata: true, ExposeSpawnModelOverrides: true,
	}); err != nil {
		t.Fatal(err)
	}
	want := []string{"spawn_agent", "send_message", "followup_task", "wait_agent", "interrupt_agent", "list_agents"}
	for _, name := range want {
		executor, ok := registry.Lookup(tool.NamespacedName(MultiAgentV2Namespace, name))
		if !ok {
			t.Fatalf("missing collaboration.%s", name)
		}
		spec := executor.Spec()
		if spec.NamespaceDescription != "Tools for spawning and managing sub-agents." || spec.Exposure != tool.ExposureModelVisible {
			t.Fatalf("spec %s = %#v", name, spec)
		}
		encoded, err := json.Marshal(spec.InputSchema)
		if err != nil || strings.Contains(string(encoded), `"required":null`) {
			t.Fatalf("invalid input schema for %s: %s, %v", name, encoded, err)
		}
	}
	spawn, _ := registry.Lookup(tool.NamespacedName(MultiAgentV2Namespace, "spawn_agent"))
	properties := spawn.Spec().InputSchema["properties"].(map[string]any)
	if _, ok := properties["service_tier"]; ok {
		t.Fatal("hidden spawn metadata must omit service_tier")
	}
	if _, ok := properties["model"]; !ok {
		t.Fatal("model override must be exposed by default config")
	}
	output, err := spawn.Execute(context.Background(), &tool.Invocation{
		ToolName: tool.NamespacedName(MultiAgentV2Namespace, "spawn_agent"),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"task_name":"factorial_part","message":"compute"}`},
	})
	if err != nil || output.Body != `{"task_name":"/root/factorial_part"}` {
		t.Fatalf("spawn output = %q, %v", output.Body, err)
	}
	if _, err := spawn.Execute(context.Background(), &tool.Invocation{Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"task_name":"Bad-Name","message":"compute"}`}}); err == nil {
		t.Fatal("invalid task_name accepted")
	}
	if _, err := spawn.Execute(context.Background(), &tool.Invocation{Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"task_name":"bad_fork","message":"compute","fork_turns":"0"}`}}); err == nil {
		t.Fatal("invalid fork_turns accepted")
	}
}

func TestV2AgentStatusValueUsesRustWireUnion(t *testing.T) {
	cases := []struct {
		status AgentMessageStatus
		want   string
	}{
		{AgentMessageStatus{Kind: AgentMessageStatusRunning}, `"running"`},
		{AgentMessageStatus{Kind: AgentMessageStatusCompleted}, `{"completed":null}`},
		{AgentMessageStatus{Kind: AgentMessageStatusCompleted, Message: "done"}, `{"completed":"done"}`},
		{AgentMessageStatus{Kind: AgentMessageStatusErrored, Message: "boom"}, `{"errored":"boom"}`},
	}
	for _, tc := range cases {
		got, err := json.Marshal(V2AgentStatusValue(tc.status))
		if err != nil || string(got) != tc.want {
			t.Fatalf("V2AgentStatusValue(%#v) = %s, %v", tc.status, got, err)
		}
	}
}

func TestMultiAgentV2ControllerErrorsRespondToModel(t *testing.T) {
	registry := tool.NewRegistry()
	controller := &limitV2Controller{MemoryToolController: NewMemoryToolController()}
	if err := RegisterMultiAgentHandlersWithOptions(registry, &MultiAgentHandlerOptions{
		Controller: controller, Version: VersionV2, Exposure: tool.ExposureModelVisible,
		HideSpawnMetadata: true, ExposeSpawnModelOverrides: true,
	}); err != nil {
		t.Fatal(err)
	}
	executor, _ := registry.Lookup(tool.NamespacedName(MultiAgentV2Namespace, "spawn_agent"))
	_, err := executor.Execute(context.Background(), &tool.Invocation{Payload: tool.Payload{
		Kind: tool.PayloadFunction, Arguments: `{"task_name":"worker","message":"compute","fork_turns":"none"}`,
	}})
	var callErr *tool.FunctionCallError
	if !tool.AsFunctionCallError(err, &callErr) || !callErr.RespondsToModel() || !strings.Contains(callErr.ModelMessage(), ErrAgentLimitReached.Error()) {
		t.Fatalf("error = %#v", err)
	}
}

func TestMultiAgentV1ControllerErrorsRespondToModel(t *testing.T) {
	registry := tool.NewRegistry()
	controller := &limitV1Controller{MemoryToolController: NewMemoryToolController()}
	if err := RegisterMultiAgentHandlersWithOptions(registry, &MultiAgentHandlerOptions{
		Controller: controller, Version: VersionV1, Exposure: tool.ExposureModelVisible,
	}); err != nil {
		t.Fatal(err)
	}
	executor, _ := registry.Lookup(tool.NamespacedName(MultiAgentV1Namespace, "spawn_agent"))
	_, err := executor.Execute(context.Background(), &tool.Invocation{Payload: tool.Payload{
		Kind: tool.PayloadFunction, Arguments: `{"message":"compute"}`,
	}})
	var callErr *tool.FunctionCallError
	if !tool.AsFunctionCallError(err, &callErr) || !callErr.RespondsToModel() || !strings.Contains(callErr.ModelMessage(), ErrAgentLimitReached.Error()) {
		t.Fatalf("error = %#v", err)
	}
}

type limitV2Controller struct{ *MemoryToolController }

func (c *limitV2Controller) SpawnAgent(context.Context, *SpawnAgentArgs) (*SpawnAgentResult, error) {
	return nil, ErrAgentLimitReached
}

type limitV1Controller struct{ *MemoryToolController }

func (c *limitV1Controller) SpawnAgent(context.Context, *SpawnAgentArgs) (*SpawnAgentResult, error) {
	return nil, ErrAgentLimitReached
}

type plaintextCaptureV2Controller struct {
	*MemoryToolController
	spawn    *SpawnAgentArgs
	send     *SendMessageArgs
	followup *FollowupTaskArgs
}

func (c *plaintextCaptureV2Controller) SpawnAgent(_ context.Context, args *SpawnAgentArgs) (*SpawnAgentResult, error) {
	copy := *args
	c.spawn = &copy
	return &SpawnAgentResult{TaskName: "/root/worker"}, nil
}

func (c *plaintextCaptureV2Controller) SendMessage(_ context.Context, args *SendMessageArgs) error {
	copy := *args
	c.send = &copy
	return nil
}

func (c *plaintextCaptureV2Controller) FollowupTask(_ context.Context, args *FollowupTaskArgs) error {
	copy := *args
	c.followup = &copy
	return nil
}

func TestMultiAgentV2ToolsPropagatePlaintextMessageSource(t *testing.T) {
	controller := &plaintextCaptureV2Controller{MemoryToolController: NewMemoryToolController()}
	registry := tool.NewRegistry()
	if err := RegisterMultiAgentHandlersWithOptions(registry, &MultiAgentHandlerOptions{
		Controller: controller, Version: VersionV2, Namespace: MultiAgentV2Namespace,
	}); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name      string
		arguments string
	}{
		{name: "spawn_agent", arguments: `{"task_name":"worker","message":"start","fork_turns":"none"}`},
		{name: "send_message", arguments: `{"target":"worker","message":"context"}`},
		{name: "followup_task", arguments: `{"target":"worker","message":"continue"}`},
	}
	for _, test := range cases {
		executor, ok := registry.Lookup(tool.NamespacedName(MultiAgentV2Namespace, test.name))
		if !ok {
			t.Fatalf("missing %s", test.name)
		}
		if _, err := executor.Execute(context.Background(), &tool.Invocation{
			Source: "direct_plaintext_message", Payload: tool.Payload{Kind: tool.PayloadFunction, Arguments: test.arguments},
		}); err != nil {
			t.Fatalf("Execute(%s) error = %v", test.name, err)
		}
	}
	if controller.spawn == nil || !controller.spawn.Plaintext || controller.send == nil || !controller.send.Plaintext || controller.followup == nil || !controller.followup.Plaintext {
		t.Fatalf("plaintext args = spawn %#v send %#v followup %#v", controller.spawn, controller.send, controller.followup)
	}
}

func TestMultiAgentToolsLifecycle(t *testing.T) {
	controller := NewMemoryToolController()
	spawn := NewMultiAgentToolExecutor(MultiAgentToolSpawn, controller)
	output, err := spawn.Execute(context.Background(), &tool.Invocation{
		CallID:   "spawn",
		ToolName: tool.NamespacedName(MultiAgentV1Namespace, string(MultiAgentToolSpawn)),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"message":"research this"}`},
	})
	if err != nil {
		t.Fatalf("spawn Execute() error = %v", err)
	}
	var spawned SpawnAgentResult
	if err := json.Unmarshal([]byte(output.Body), &spawned); err != nil {
		t.Fatalf("Unmarshal spawn = %v", err)
	}
	if spawned.AgentID == "" || spawned.Nickname == nil {
		t.Fatalf("spawned = %#v", spawned)
	}

	send := NewMultiAgentToolExecutor(MultiAgentToolSend, controller)
	if _, err := send.Execute(context.Background(), &tool.Invocation{
		CallID:   "send",
		ToolName: tool.NamespacedName(MultiAgentV1Namespace, string(MultiAgentToolSend)),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"target":"` + spawned.AgentID + `","message":"continue"}`},
	}); err != nil {
		t.Fatalf("send Execute() error = %v", err)
	}

	controller.SetStatus(spawned.AgentID, AgentMessageStatus{Kind: AgentMessageStatusCompleted, Message: "done"})
	wait := NewMultiAgentToolExecutor(MultiAgentToolWait, controller)
	waitOutput, err := wait.Execute(context.Background(), &tool.Invocation{
		CallID:   "wait",
		ToolName: tool.NamespacedName(MultiAgentV1Namespace, string(MultiAgentToolWait)),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"targets":["` + spawned.AgentID + `"]}`},
	})
	if err != nil {
		t.Fatalf("wait Execute() error = %v", err)
	}
	if !strings.Contains(waitOutput.Body, `"completed"`) || !strings.Contains(waitOutput.Body, `"done"`) {
		t.Fatalf("wait output = %q", waitOutput.Body)
	}

	closeTool := NewMultiAgentToolExecutor(MultiAgentToolClose, controller)
	closeOutput, err := closeTool.Execute(context.Background(), &tool.Invocation{
		CallID:   "close",
		ToolName: tool.NamespacedName(MultiAgentV1Namespace, string(MultiAgentToolClose)),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"target":"` + spawned.AgentID + `"}`},
	})
	if err != nil {
		t.Fatalf("close Execute() error = %v", err)
	}
	if !strings.Contains(closeOutput.Body, `"completed"`) {
		t.Fatalf("close output = %q", closeOutput.Body)
	}
}

func TestMultiAgentToolHookPayloads(t *testing.T) {
	executor := NewMultiAgentToolExecutor(MultiAgentToolSpawn, NewMemoryToolController())
	invocation := &tool.Invocation{
		CallID:   "call-spawn",
		ToolName: tool.NamespacedName(MultiAgentV1Namespace, string(MultiAgentToolSpawn)),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"message":"split work"}`},
	}
	pre, ok := executor.PreToolUsePayload(invocation)
	if !ok {
		t.Fatal("PreToolUsePayload() ok = false")
	}
	if pre.ToolName == nil || pre.ToolName.Name != "spawn_agent" || len(pre.ToolName.MatcherAliases) != 1 || pre.ToolName.MatcherAliases[0] != "Agent" {
		t.Fatalf("pre.ToolName = %#v", pre.ToolName)
	}
	if input, ok := pre.ToolInput.(map[string]any); !ok || input["message"] != "split work" {
		t.Fatalf("pre.ToolInput = %#v", pre.ToolInput)
	}

	post, ok := executor.PostToolUsePayload(invocation, &tool.Output{
		CallID: "call-spawn",
		Body:   `{"agent_id":"agent-1"}`,
		Data:   map[string]any{"result": &SpawnAgentResult{AgentID: "agent-1"}},
	})
	if !ok {
		t.Fatal("PostToolUsePayload() ok = false")
	}
	if post.ToolName == nil || post.ToolName.Name != "spawn_agent" || post.ToolUseID != "call-spawn" {
		t.Fatalf("post = %#v", post)
	}
	if _, ok := post.ToolResponse.(*SpawnAgentResult); !ok {
		t.Fatalf("ToolResponse = %#v", post.ToolResponse)
	}
}

func TestMultiAgentToolUpdatedHookInput(t *testing.T) {
	executor := NewMultiAgentToolExecutor(MultiAgentToolSend, NewMemoryToolController())
	updated, err := executor.WithUpdatedHookInput(&tool.Invocation{
		ToolName: tool.NamespacedName(MultiAgentV1Namespace, string(MultiAgentToolSend)),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"target":"agent-1","message":"old"}`},
	}, map[string]any{"target": "agent-1", "message": "new"})
	if err != nil {
		t.Fatalf("WithUpdatedHookInput() error = %v", err)
	}
	if !strings.Contains(updated.Payload.Arguments, `"new"`) {
		t.Fatalf("updated = %#v", updated.Payload)
	}
}

func TestRegisterMultiAgentHandlersDiscoverable(t *testing.T) {
	registry := tool.NewRegistry()
	if err := RegisterMultiAgentHandlers(registry, NewMemoryToolController(), tool.ExposureDiscoverable); err != nil {
		t.Fatalf("RegisterMultiAgentHandlers() error = %v", err)
	}
	specs := registry.NamesAsSpecs()
	if len(specs) != 5 {
		t.Fatalf("specs = %#v", specs)
	}
	for _, spec := range specs {
		if spec.Exposure != tool.ExposureDiscoverable || spec.Search == nil {
			t.Fatalf("spec = %#v", spec)
		}
		if spec.InputSchema["type"] != "object" {
			t.Fatalf("invalid input schema for %s: %#v", spec.Name.Key(), spec.InputSchema)
		}
		properties, ok := spec.InputSchema["properties"].(map[string]any)
		if !ok {
			t.Fatalf("invalid properties for %s: %#v", spec.Name.Key(), spec.InputSchema)
		}
		for property, raw := range properties {
			if _, ok := raw.(map[string]any); !ok {
				t.Fatalf("invalid property %s for %s: %#v", property, spec.Name.Key(), raw)
			}
		}
		encoded, err := json.Marshal(spec.InputSchema)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), `"required":null`) {
			t.Fatalf("required must serialize as an array for %s: %s", spec.Name.Key(), encoded)
		}
	}
	if err := tool.RegisterToolSearchFromRegistry(registry); err != nil {
		t.Fatalf("RegisterToolSearchFromRegistry() error = %v", err)
	}
	output, err := tool.NewRouter(registry).Dispatch(context.Background(), &tool.Invocation{
		CallID:   "search",
		ToolName: tool.PlainName(tool.ToolSearchName),
		Payload:  tool.Payload{Kind: tool.PayloadFunction, Arguments: `{"query":"delegate parallel worker"}`},
	})
	if err != nil {
		t.Fatalf("tool_search Dispatch() error = %v", err)
	}
	if !strings.Contains(output.Body, "spawn_agent") {
		t.Fatalf("tool_search output = %q", output.Body)
	}
}

func TestRegisterMultiAgentHandlersWithRolesDescribesConfiguredTypes(t *testing.T) {
	registry := tool.NewRegistry()
	err := RegisterMultiAgentHandlersWithOptions(registry, &MultiAgentHandlerOptions{
		Controller: &captureToolController{}, Exposure: tool.ExposureModelVisible,
		Roles: map[string]RoleConfig{"reviewer": {Description: "Reviews changes.", Settings: map[string]string{"model_reasoning_effort": "high"}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	executor, ok := registry.Lookup(tool.NamespacedName(MultiAgentV1Namespace, string(MultiAgentToolSpawn)))
	if !ok {
		t.Fatal("spawn_agent missing")
	}
	description := executor.Spec().Description
	if !strings.Contains(description, "`reviewer`: Reviews changes.") || !strings.Contains(description, "reasoning effort is set to `high`") || strings.Contains(description, "`explorer`") {
		t.Fatalf("description = %q", description)
	}
}
