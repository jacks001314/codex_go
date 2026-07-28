package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"time"

	"codex_go/tool"
)

const (
	MultiAgentV2DefaultWait = 30 * time.Second
	MultiAgentV2MinWait     = 10 * time.Second
	MultiAgentV2MaxWait     = time.Hour
)

var multiAgentV2TaskNamePattern = regexp.MustCompile(`^[a-z0-9_]+$`)

type V2ToolController interface {
	ToolController
	SendMessage(context.Context, *SendMessageArgs) error
	FollowupTask(context.Context, *FollowupTaskArgs) error
	WaitForActivity(context.Context, *WaitForActivityArgs) (*WaitForActivityResult, error)
	InterruptAgent(context.Context, *InterruptAgentArgs) (*InterruptAgentResult, error)
	ListAgents(context.Context, *ListAgentsArgs) (*ListAgentsResult, error)
}

type SendMessageArgs struct {
	Target  string `json:"target"`
	Message string `json:"message"`
}

type FollowupTaskArgs struct {
	Target  string `json:"target"`
	Message string `json:"message"`
}

type WaitForActivityArgs struct {
	TimeoutMS *int64 `json:"timeout_ms,omitempty"`
}

type WaitForActivityResult struct {
	Message  string `json:"message"`
	TimedOut bool   `json:"timed_out"`
}

type InterruptAgentArgs struct {
	Target string `json:"target"`
}

type InterruptAgentResult struct {
	PreviousStatus any `json:"previous_status"`
}

type ListAgentsArgs struct {
	PathPrefix *string `json:"path_prefix,omitempty"`
}

type ListedAgent struct {
	AgentName   string `json:"agent_name"`
	AgentStatus any    `json:"agent_status"`
}

type ListAgentsResult struct {
	Agents []ListedAgent `json:"agents"`
}

type multiAgentV2ToolKind string

const (
	multiAgentV2Spawn     multiAgentV2ToolKind = "spawn_agent"
	multiAgentV2Send      multiAgentV2ToolKind = "send_message"
	multiAgentV2Followup  multiAgentV2ToolKind = "followup_task"
	multiAgentV2Wait      multiAgentV2ToolKind = "wait_agent"
	multiAgentV2Interrupt multiAgentV2ToolKind = "interrupt_agent"
	multiAgentV2List      multiAgentV2ToolKind = "list_agents"
)

type multiAgentV2ToolExecutor struct {
	kind                      multiAgentV2ToolKind
	controller                V2ToolController
	exposure                  tool.Exposure
	namespace                 string
	waitMin                   time.Duration
	waitMax                   time.Duration
	waitDefault               time.Duration
	hideSpawnMetadata         bool
	exposeSpawnModelOverrides bool
}

func registerMultiAgentV2Handlers(registry *tool.Registry, options *MultiAgentHandlerOptions) error {
	controller := options.Controller
	if len(options.Roles) > 0 || options.Defaults != (SpawnDefaults{}) {
		controller = NewRoleAwareToolController(controller, options.Roles, options.Defaults)
	}
	v2, ok := controller.(V2ToolController)
	if !ok {
		return fmt.Errorf("%w: multi-agent v2 controller is unavailable", tool.ErrToolInvalidCall)
	}
	namespace := strings.TrimSpace(options.Namespace)
	if namespace == "" {
		namespace = MultiAgentV2Namespace
	}
	waitMin, waitMax, waitDefault := options.WaitMin, options.WaitMax, options.WaitDefault
	if !options.WaitConfigured && waitMin <= 0 {
		waitMin = MultiAgentV2MinWait
	}
	if !options.WaitConfigured && waitMax <= 0 {
		waitMax = MultiAgentV2MaxWait
	}
	if !options.WaitConfigured && waitDefault <= 0 {
		waitDefault = MultiAgentV2DefaultWait
	}
	kinds := []multiAgentV2ToolKind{multiAgentV2Spawn, multiAgentV2Send, multiAgentV2Followup}
	if !options.DisableWaitAgent {
		kinds = append(kinds, multiAgentV2Wait)
	}
	kinds = append(kinds, multiAgentV2Interrupt, multiAgentV2List)
	for _, kind := range kinds {
		if err := registry.Register(&multiAgentV2ToolExecutor{
			kind: kind, controller: v2, exposure: options.Exposure, namespace: namespace,
			waitMin: waitMin, waitMax: waitMax, waitDefault: waitDefault, hideSpawnMetadata: options.HideSpawnMetadata,
			exposeSpawnModelOverrides: options.ExposeSpawnModelOverrides,
		}); err != nil {
			return err
		}
	}
	return nil
}

func (e *multiAgentV2ToolExecutor) Spec() tool.Spec {
	namespace := e.namespace
	if namespace == "" {
		namespace = MultiAgentV2Namespace
	}
	spec := tool.Spec{
		Name:                 tool.NamespacedName(namespace, string(e.kind)),
		Exposure:             e.exposure,
		NamespaceDescription: "Tools for spawning and managing sub-agents.",
	}
	if spec.Exposure == "" {
		spec.Exposure = tool.ExposureModelVisible
	}
	switch e.kind {
	case multiAgentV2Spawn:
		spec.Description = "Spawns an agent to work on the specified task. The new agent's canonical task name will be provided to it along with the message."
		properties := map[string]any{
			"task_name":        map[string]any{"type": "string", "description": "Task name for the new agent. Use lowercase letters, digits, and underscores."},
			"message":          map[string]any{"type": "string", "description": "Initial plain-text task for the new agent.", "encrypted": true},
			"agent_type":       map[string]any{"type": "string", "description": "Agent type override for the new agent. Omit unless explicitly asked. Set `fork_turns` to `none` or a positive integer when an explicit override is needed."},
			"model":            map[string]any{"type": "string", "description": "Model override for the new agent. Omit to inherit the parent model."},
			"reasoning_effort": map[string]any{"type": "string", "description": "Reasoning effort override for the new agent. Omit to inherit the parent effort."},
			"service_tier":     map[string]any{"type": "string", "description": "Service tier override for the new agent."},
			"fork_turns":       map[string]any{"type": "string", "description": "Optional number of turns to fork. Defaults to `all`. Use `none`, `all`, or a positive integer string such as `3` to fork only the most recent turns."},
		}
		if e.hideSpawnMetadata {
			delete(properties, "service_tier")
		}
		if !e.exposeSpawnModelOverrides {
			delete(properties, "model")
			delete(properties, "reasoning_effort")
		}
		spec.InputSchema = multiAgentObjectSchema(properties, []string{"task_name", "message"})
		outputProperties := map[string]any{"task_name": map[string]any{"type": "string", "description": "Canonical task name for the spawned agent."}}
		required := []string{"task_name"}
		if !e.hideSpawnMetadata {
			outputProperties["nickname"] = map[string]any{"type": []any{"string", "null"}, "description": "User-facing nickname for the spawned agent when available."}
			required = append(required, "nickname")
		}
		spec.OutputSchema = multiAgentObjectSchema(outputProperties, required)
		spec.Parallel = true
	case multiAgentV2Send:
		spec.Description = "Send a message to an existing agent. The message will be delivered promptly. Does not trigger a new turn."
		spec.InputSchema = targetMessageSchema("Relative or canonical task name to message (from spawn_agent).", "Message text to queue on the target agent.")
	case multiAgentV2Followup:
		spec.Description = "Send a follow-up task to an existing non-root target agent and trigger a turn if it is idle. If the target is already running, deliver the task promptly at message boundaries while sampling, or after the pending tool call completes."
		spec.InputSchema = targetMessageSchema("Agent id or canonical task name to send a follow-up task to (from spawn_agent).", "Message text to send to the target agent.")
	case multiAgentV2Wait:
		spec.Description = "Wait for a mailbox update from any live agent, including queued messages and final-status notifications. The wait also ends early when new user input is steered into the active turn. Does not return the content; returns either a summary of which agents have updates (if any), an interruption summary for steered input, or a timeout summary if no activity arrives before the deadline."
		spec.InputSchema = multiAgentObjectSchema(map[string]any{
			"timeout_ms": map[string]any{"type": "number", "description": fmt.Sprintf("Timeout in milliseconds. Defaults to %d, min %d, max %d. Prefer longer waits (minutes) to avoid busy polling.", e.waitDefault.Milliseconds(), e.waitMin.Milliseconds(), e.waitMax.Milliseconds())},
		}, nil)
		spec.OutputSchema = multiAgentObjectSchema(map[string]any{
			"message":   map[string]any{"type": "string", "description": "Brief wait summary without the agent's final content."},
			"timed_out": map[string]any{"type": "boolean", "description": "Whether the wait call returned because no mailbox update arrived before the timeout."},
		}, []string{"message", "timed_out"})
	case multiAgentV2Interrupt:
		spec.Description = "Interrupt an agent's current turn, if any, and return its previous status. The agent remains available for messages and follow-up tasks."
		spec.InputSchema = targetSchema("Agent id or canonical task name to interrupt (from spawn_agent).")
		spec.OutputSchema = previousStatusSchema("The agent status observed before the interrupt request was handled.")
	case multiAgentV2List:
		spec.Description = "List live agents in the current root thread tree. Optionally filter by task-path prefix."
		spec.InputSchema = multiAgentObjectSchema(map[string]any{"path_prefix": map[string]any{"type": "string", "description": "Task-path prefix filter without a trailing slash. Omit to list all live agents."}}, nil)
		spec.OutputSchema = multiAgentObjectSchema(map[string]any{"agents": map[string]any{
			"description": "Live agents visible in the current root thread tree.",
			"type":        "array", "items": multiAgentObjectSchema(map[string]any{
				"agent_name":   map[string]any{"type": "string", "description": "Canonical task name for the agent when available, otherwise the agent id."},
				"agent_status": map[string]any{"description": "Last known status of the agent.", "allOf": []any{agentStatusSchema()}},
			}, []string{"agent_name", "agent_status"}),
		}}, []string{"agents"})
	}
	return spec
}

func (e *multiAgentV2ToolExecutor) Execute(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
	if invocation == nil || invocation.Payload.Kind != tool.PayloadFunction {
		return nil, tool.RespondToModel("multi-agent v2 handler received unsupported payload")
	}
	var result any
	var err error
	switch e.kind {
	case multiAgentV2Spawn:
		var args SpawnAgentArgs
		if err = invocation.DecodeArguments(&args); err == nil {
			if !multiAgentV2TaskNamePattern.MatchString(args.TaskName) {
				return nil, tool.RespondToModel("task_name must match ^[a-z0-9_]+$")
			}
			if strings.TrimSpace(execString(args.Message)) == "" {
				return nil, tool.RespondToModel("message is required")
			}
			if args.ForkContext {
				return nil, tool.RespondToModel("fork_context is not supported in MultiAgentV2; use fork_turns instead")
			}
			if args.ForkTurns == nil {
				value := "all"
				args.ForkTurns = &value
			}
			if err = validateForkTurns(args.ForkTurns); err != nil {
				return nil, tool.RespondToModel(err.Error())
			}
			result, err = e.controller.SpawnAgent(ctx, &args)
			if spawned, ok := result.(*SpawnAgentResult); ok && spawned != nil && err == nil {
				output := map[string]any{"task_name": firstNonEmptyAgentString(spawned.TaskName, args.TaskName)}
				if !e.hideSpawnMetadata {
					output["nickname"] = spawned.Nickname
				}
				result = output
			}
		}
	case multiAgentV2Send:
		var args SendMessageArgs
		if err = invocation.DecodeArguments(&args); err == nil {
			err = validateTargetMessage(args.Target, args.Message)
		}
		if err == nil {
			err = e.controller.SendMessage(ctx, &args)
			result = nil
		}
	case multiAgentV2Followup:
		var args FollowupTaskArgs
		if err = invocation.DecodeArguments(&args); err == nil {
			err = validateTargetMessage(args.Target, args.Message)
		}
		if err == nil {
			err = e.controller.FollowupTask(ctx, &args)
			result = nil
		}
	case multiAgentV2Wait:
		var args WaitForActivityArgs
		if err = invocation.DecodeArguments(&args); err == nil {
			if args.TimeoutMS != nil && (*args.TimeoutMS < e.waitMin.Milliseconds() || *args.TimeoutMS > e.waitMax.Milliseconds()) {
				return nil, tool.RespondToModel(fmt.Sprintf("timeout_ms must be between %d and %d", e.waitMin.Milliseconds(), e.waitMax.Milliseconds()))
			}
			result, err = e.controller.WaitForActivity(ctx, &args)
		}
	case multiAgentV2Interrupt:
		var args InterruptAgentArgs
		if err = invocation.DecodeArguments(&args); err == nil && strings.TrimSpace(args.Target) == "" {
			err = fmt.Errorf("target is required")
		}
		if err == nil {
			result, err = e.controller.InterruptAgent(ctx, &args)
		}
	case multiAgentV2List:
		var args ListAgentsArgs
		if err = invocation.DecodeArguments(&args); err == nil {
			result, err = e.controller.ListAgents(ctx, &args)
		}
	default:
		err = fmt.Errorf("unknown multi-agent v2 tool %q", e.kind)
	}
	if err != nil {
		return nil, tool.RespondToModel(err.Error())
	}
	body := ""
	if result != nil {
		data, marshalErr := json.Marshal(result)
		if marshalErr != nil {
			return nil, marshalErr
		}
		body = string(data)
	}
	return &tool.Output{Success: true, Body: body, Data: map[string]any{"result": result}, LogPreview: body}, nil
}

func targetSchema(description string) map[string]any {
	return multiAgentObjectSchema(map[string]any{"target": map[string]any{"type": "string", "description": description}}, []string{"target"})
}

func targetMessageSchema(targetDescription, messageDescription string) map[string]any {
	return multiAgentObjectSchema(map[string]any{
		"target":  map[string]any{"type": "string", "description": targetDescription},
		"message": map[string]any{"type": "string", "description": messageDescription, "encrypted": true},
	}, []string{"target", "message"})
}

func agentStatusSchema() map[string]any {
	return map[string]any{"oneOf": []any{
		map[string]any{"type": "string", "enum": []any{"pending_init", "running", "interrupted", "shutdown", "not_found"}},
		multiAgentObjectSchema(map[string]any{"completed": map[string]any{"type": []any{"string", "null"}}}, []string{"completed"}),
		multiAgentObjectSchema(map[string]any{"errored": map[string]any{"type": "string"}}, []string{"errored"}),
	}}
}

func previousStatusSchema(description string) map[string]any {
	return multiAgentObjectSchema(map[string]any{
		"previous_status": map[string]any{"description": description, "allOf": []any{agentStatusSchema()}},
	}, []string{"previous_status"})
}

func V2AgentStatusValue(status AgentMessageStatus) any {
	switch status.Kind {
	case AgentMessageStatusCompleted:
		if status.Message == "" {
			return map[string]any{"completed": nil}
		}
		return map[string]any{"completed": status.Message}
	case AgentMessageStatusErrored:
		return map[string]any{"errored": status.Message}
	default:
		return string(status.Kind)
	}
}

func validateTargetMessage(target, message string) error {
	if strings.TrimSpace(target) == "" {
		return fmt.Errorf("target is required")
	}
	if strings.TrimSpace(message) == "" {
		return fmt.Errorf("message is required")
	}
	return nil
}

func validateForkTurns(value *string) error {
	if value == nil {
		return nil
	}
	normalized := strings.TrimSpace(*value)
	if strings.EqualFold(normalized, "none") || strings.EqualFold(normalized, "all") {
		return nil
	}
	if normalized == "" || normalized[0] == '0' {
		return fmt.Errorf("fork_turns must be `none`, `all`, or a positive integer string")
	}
	for _, char := range normalized {
		if char < '0' || char > '9' {
			return fmt.Errorf("fork_turns must be `none`, `all`, or a positive integer string")
		}
	}
	return nil
}

func execString(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}

var _ tool.Executor = (*multiAgentV2ToolExecutor)(nil)

func (c *MemoryToolController) SendMessage(ctx context.Context, args *SendMessageArgs) error {
	_ = ctx
	if args == nil || validateTargetMessage(args.Target, args.Message) != nil {
		return fmt.Errorf("target and message are required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensure()
	target := normalizeAgentTarget(args.Target)
	if _, ok := c.statuses[target]; !ok {
		return fmt.Errorf("agent %s not found", args.Target)
	}
	c.inputs[target] = append(c.inputs[target], args.Message)
	return nil
}

func (c *MemoryToolController) FollowupTask(ctx context.Context, args *FollowupTaskArgs) error {
	if args == nil {
		return fmt.Errorf("target and message are required")
	}
	if err := c.SendMessage(ctx, &SendMessageArgs{Target: args.Target, Message: args.Message}); err != nil {
		return err
	}
	c.SetStatus(args.Target, AgentMessageStatus{Kind: AgentMessageStatusRunning})
	return nil
}

func (c *MemoryToolController) WaitForActivity(ctx context.Context, args *WaitForActivityArgs) (*WaitForActivityResult, error) {
	timeout := MultiAgentV2DefaultWait
	if args != nil && args.TimeoutMS != nil {
		timeout = time.Duration(*args.TimeoutMS) * time.Millisecond
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-timer.C:
		return &WaitForActivityResult{TimedOut: true}, nil
	}
}

func (c *MemoryToolController) InterruptAgent(_ context.Context, args *InterruptAgentArgs) (*InterruptAgentResult, error) {
	if args == nil || strings.TrimSpace(args.Target) == "" {
		return nil, fmt.Errorf("target is required")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensure()
	target := normalizeAgentTarget(args.Target)
	previous, ok := c.statuses[target]
	if !ok {
		previous = AgentMessageStatus{Kind: AgentMessageStatusNotFound}
	} else {
		c.statuses[target] = AgentMessageStatus{Kind: AgentMessageStatusInterrupted}
	}
	return &InterruptAgentResult{PreviousStatus: V2AgentStatusValue(previous)}, nil
}

func (c *MemoryToolController) ListAgents(_ context.Context, args *ListAgentsArgs) (*ListAgentsResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensure()
	prefix := ""
	if args != nil && args.PathPrefix != nil {
		prefix = strings.TrimSpace(*args.PathPrefix)
	}
	result := &ListAgentsResult{Agents: []ListedAgent{{AgentName: "/root", AgentStatus: "running"}}}
	for id, status := range c.statuses {
		name := "/root/" + id
		if prefix == "" || strings.HasPrefix(name, prefix) {
			result.Agents = append(result.Agents, ListedAgent{AgentName: name, AgentStatus: V2AgentStatusValue(status)})
		}
	}
	return result, nil
}

var _ V2ToolController = (*MemoryToolController)(nil)
