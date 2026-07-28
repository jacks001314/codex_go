package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"codex_go/tool"
)

const (
	MultiAgentV1Namespace = "agent"
	MultiAgentV2Namespace = "collaboration"
)

type ToolController interface {
	SpawnAgent(ctx context.Context, args *SpawnAgentArgs) (*SpawnAgentResult, error)
	SendInput(ctx context.Context, args *SendInputArgs) (*SendInputResult, error)
	WaitAgent(ctx context.Context, args *WaitAgentArgs) (*WaitAgentResult, error)
	ResumeAgent(ctx context.Context, args *ResumeAgentArgs) (*ResumeAgentResult, error)
	CloseAgent(ctx context.Context, args *CloseAgentArgs) (*CloseAgentResult, error)
}

type MemoryToolController struct {
	mu       sync.Mutex
	nextID   int
	statuses map[string]AgentMessageStatus
	inputs   map[string][]string
}

func NewMemoryToolController() *MemoryToolController {
	return &MemoryToolController{
		statuses: map[string]AgentMessageStatus{},
		inputs:   map[string][]string{},
	}
}

type SpawnAgentArgs struct {
	TaskName        string  `json:"task_name,omitempty"`
	Message         *string `json:"message,omitempty"`
	Items           []any   `json:"items,omitempty"`
	AgentType       *string `json:"agent_type,omitempty"`
	Model           *string `json:"model,omitempty"`
	ReasoningEffort *string `json:"reasoning_effort,omitempty"`
	ServiceTier     *string `json:"service_tier,omitempty"`
	ForkContext     bool    `json:"fork_context,omitempty"`
	ForkTurns       *string `json:"fork_turns,omitempty"`

	ResolvedRole       string   `json:"-"`
	NicknameCandidates []string `json:"-"`
}

type SpawnAgentResult struct {
	AgentID  string  `json:"agent_id"`
	TaskName string  `json:"task_name,omitempty"`
	Nickname *string `json:"nickname,omitempty"`
}

type SendInputArgs struct {
	Target    string  `json:"target"`
	Message   *string `json:"message,omitempty"`
	Items     []any   `json:"items,omitempty"`
	Interrupt bool    `json:"interrupt,omitempty"`
}

type SendInputResult struct {
	SubmissionID string `json:"submission_id"`
}

type WaitAgentArgs struct {
	Targets   []string `json:"targets,omitempty"`
	TimeoutMS *int64   `json:"timeout_ms,omitempty"`
}

type WaitAgentResult struct {
	Status   map[string]AgentMessageStatus `json:"status"`
	TimedOut bool                          `json:"timed_out"`
}

type ResumeAgentArgs struct {
	ID string `json:"id"`
}

type ResumeAgentResult struct {
	Status AgentMessageStatus `json:"status"`
}

type CloseAgentArgs struct {
	Target string `json:"target"`
}

type CloseAgentResult struct {
	PreviousStatus AgentMessageStatus `json:"previous_status"`
}

func (c *MemoryToolController) SpawnAgent(ctx context.Context, args *SpawnAgentArgs) (*SpawnAgentResult, error) {
	_ = ctx
	if c == nil {
		c = NewMemoryToolController()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensure()
	c.nextID++
	id := fmt.Sprintf("agent-%d", c.nextID)
	nickname := fmt.Sprintf("Agent %d", c.nextID)
	c.statuses[id] = AgentMessageStatus{Kind: AgentMessageStatusRunning}
	if args != nil && args.Message != nil {
		c.inputs[id] = append(c.inputs[id], *args.Message)
	}
	taskName := ""
	if args != nil && strings.TrimSpace(args.TaskName) != "" {
		taskName = "/root/" + strings.TrimSpace(args.TaskName)
	}
	return &SpawnAgentResult{AgentID: id, TaskName: taskName, Nickname: &nickname}, nil
}

func (c *MemoryToolController) SendInput(ctx context.Context, args *SendInputArgs) (*SendInputResult, error) {
	_ = ctx
	if args == nil || strings.TrimSpace(args.Target) == "" {
		return nil, fmt.Errorf("target is required")
	}
	if c == nil {
		c = NewMemoryToolController()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensure()
	target := normalizeAgentTarget(args.Target)
	if _, ok := c.statuses[target]; !ok {
		c.statuses[target] = AgentMessageStatus{Kind: AgentMessageStatusNotFound}
	}
	if args.Message != nil {
		c.inputs[target] = append(c.inputs[target], *args.Message)
	}
	return &SendInputResult{SubmissionID: "submission-" + target}, nil
}

func (c *MemoryToolController) WaitAgent(ctx context.Context, args *WaitAgentArgs) (*WaitAgentResult, error) {
	if args == nil {
		args = &WaitAgentArgs{}
	}
	if args.TimeoutMS != nil && *args.TimeoutMS > 0 {
		timer := time.NewTimer(time.Duration(*args.TimeoutMS) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	if c == nil {
		c = NewMemoryToolController()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensure()
	status := map[string]AgentMessageStatus{}
	targets := args.Targets
	if len(targets) == 0 {
		for id := range c.statuses {
			targets = append(targets, id)
		}
	}
	for _, target := range targets {
		id := normalizeAgentTarget(target)
		value, ok := c.statuses[id]
		if !ok {
			value = AgentMessageStatus{Kind: AgentMessageStatusNotFound}
		}
		status[id] = value
	}
	return &WaitAgentResult{Status: status, TimedOut: false}, nil
}

func (c *MemoryToolController) ResumeAgent(ctx context.Context, args *ResumeAgentArgs) (*ResumeAgentResult, error) {
	_ = ctx
	if args == nil || strings.TrimSpace(args.ID) == "" {
		return nil, fmt.Errorf("id is required")
	}
	if c == nil {
		c = NewMemoryToolController()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensure()
	id := normalizeAgentTarget(args.ID)
	status, ok := c.statuses[id]
	if !ok {
		status = AgentMessageStatus{Kind: AgentMessageStatusNotFound}
	} else if status.IsFinal() {
		status = AgentMessageStatus{Kind: AgentMessageStatusRunning}
		c.statuses[id] = status
	}
	return &ResumeAgentResult{Status: status}, nil
}

func (c *MemoryToolController) CloseAgent(ctx context.Context, args *CloseAgentArgs) (*CloseAgentResult, error) {
	_ = ctx
	if args == nil || strings.TrimSpace(args.Target) == "" {
		return nil, fmt.Errorf("target is required")
	}
	if c == nil {
		c = NewMemoryToolController()
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensure()
	id := normalizeAgentTarget(args.Target)
	previous, ok := c.statuses[id]
	if !ok {
		previous = AgentMessageStatus{Kind: AgentMessageStatusNotFound}
	} else {
		c.statuses[id] = AgentMessageStatus{Kind: AgentMessageStatusShutdown}
	}
	return &CloseAgentResult{PreviousStatus: previous}, nil
}

func (c *MemoryToolController) SetStatus(agentID string, status AgentMessageStatus) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.ensure()
	c.statuses[normalizeAgentTarget(agentID)] = status
}

func (c *MemoryToolController) ensure() {
	if c.statuses == nil {
		c.statuses = map[string]AgentMessageStatus{}
	}
	if c.inputs == nil {
		c.inputs = map[string][]string{}
	}
}

type MultiAgentToolKind string

const (
	MultiAgentToolSpawn  MultiAgentToolKind = "spawn_agent"
	MultiAgentToolSend   MultiAgentToolKind = "send_input"
	MultiAgentToolWait   MultiAgentToolKind = "wait_agent"
	MultiAgentToolResume MultiAgentToolKind = "resume_agent"
	MultiAgentToolClose  MultiAgentToolKind = "close_agent"
)

type MultiAgentToolExecutor struct {
	kind             MultiAgentToolKind
	controller       ToolController
	exposure         tool.Exposure
	spawnDescription string
}

func NewMultiAgentToolExecutor(kind MultiAgentToolKind, controller ToolController) *MultiAgentToolExecutor {
	if controller == nil {
		controller = NewMemoryToolController()
	}
	return &MultiAgentToolExecutor{kind: kind, controller: controller}
}

func RegisterMultiAgentHandlers(registry *tool.Registry, controller ToolController, exposure tool.Exposure) error {
	return RegisterMultiAgentHandlersWithOptions(registry, &MultiAgentHandlerOptions{Controller: controller, Exposure: exposure})
}

type MultiAgentHandlerOptions struct {
	Controller                ToolController
	Exposure                  tool.Exposure
	Version                   MultiAgentVersion
	Namespace                 string
	Roles                     map[string]RoleConfig
	Defaults                  SpawnDefaults
	DisableWaitAgent          bool
	WaitDefault               time.Duration
	WaitMin                   time.Duration
	WaitMax                   time.Duration
	WaitConfigured            bool
	HideSpawnMetadata         bool
	ExposeSpawnModelOverrides bool
}

type SpawnDefaults struct {
	Model           string
	ReasoningEffort string
	ServiceTier     string
}

func RegisterMultiAgentHandlersWithOptions(registry *tool.Registry, options *MultiAgentHandlerOptions) error {
	if registry == nil {
		return fmt.Errorf("%w: registry is nil", tool.ErrToolInvalidCall)
	}
	if options == nil {
		options = &MultiAgentHandlerOptions{}
	}
	if options.Version == VersionV2 {
		return registerMultiAgentV2Handlers(registry, options)
	}
	controller := options.Controller
	if len(options.Roles) > 0 || options.Defaults != (SpawnDefaults{}) {
		controller = NewRoleAwareToolController(controller, options.Roles, options.Defaults)
	}
	for _, kind := range []MultiAgentToolKind{MultiAgentToolSpawn, MultiAgentToolSend, MultiAgentToolWait, MultiAgentToolResume, MultiAgentToolClose} {
		if kind == MultiAgentToolWait && options.DisableWaitAgent {
			continue
		}
		executor := NewMultiAgentToolExecutor(kind, controller)
		executor.exposure = options.Exposure
		if kind == MultiAgentToolSpawn && len(options.Roles) > 0 {
			executor.spawnDescription = NewRoleResolver(map[string]RoleConfig{}).SpawnToolDescription(options.Roles)
		}
		if err := registry.Register(executor); err != nil {
			return err
		}
	}
	return nil
}

func (e *MultiAgentToolExecutor) Spec() tool.Spec {
	spec := multiAgentToolSpec(e.kind)
	if e.kind == MultiAgentToolSpawn && strings.TrimSpace(e.spawnDescription) != "" {
		spec.Description += "\n\n" + e.spawnDescription
	}
	spec.Exposure = e.exposure
	if spec.Exposure == tool.ExposureDiscoverable {
		spec.Search = &tool.SearchInfo{
			Text:   multiAgentSearchText(e.kind),
			Source: &tool.SearchSourceInfo{Name: "Multi-agent tools", Description: "Spawn and manage sub-agents."},
		}
	}
	return spec
}

func (e *MultiAgentToolExecutor) Execute(ctx context.Context, invocation *tool.Invocation) (*tool.Output, error) {
	if e == nil || e.controller == nil {
		return nil, fmt.Errorf("%w: multi-agent controller is nil", tool.ErrToolInvalidCall)
	}
	if invocation == nil || invocation.Payload.Kind != tool.PayloadFunction {
		return nil, tool.RespondToModel("multi-agent handler received unsupported payload")
	}
	var result any
	var err error
	switch e.kind {
	case MultiAgentToolSpawn:
		var args SpawnAgentArgs
		if err := invocation.DecodeArguments(&args); err != nil {
			return nil, err
		}
		result, err = e.controller.SpawnAgent(ctx, &args)
	case MultiAgentToolSend:
		var args SendInputArgs
		if err := invocation.DecodeArguments(&args); err != nil {
			return nil, err
		}
		result, err = e.controller.SendInput(ctx, &args)
	case MultiAgentToolWait:
		var args WaitAgentArgs
		if err := invocation.DecodeArguments(&args); err != nil {
			return nil, err
		}
		result, err = e.controller.WaitAgent(ctx, &args)
	case MultiAgentToolResume:
		var args ResumeAgentArgs
		if err := invocation.DecodeArguments(&args); err != nil {
			return nil, err
		}
		result, err = e.controller.ResumeAgent(ctx, &args)
	case MultiAgentToolClose:
		var args CloseAgentArgs
		if err := invocation.DecodeArguments(&args); err != nil {
			return nil, err
		}
		result, err = e.controller.CloseAgent(ctx, &args)
	default:
		err = fmt.Errorf("unknown multi-agent tool %q", e.kind)
	}
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return &tool.Output{Success: true, Body: string(body), Data: map[string]any{"result": result}, LogPreview: string(body)}, nil
}

func (e *MultiAgentToolExecutor) PreToolUsePayload(invocation *tool.Invocation) (*tool.PreToolUsePayload, bool) {
	if invocation == nil || invocation.Payload.Kind != tool.PayloadFunction {
		return nil, false
	}
	return &tool.PreToolUsePayload{
		ToolName:  multiAgentHookToolName(e.kind),
		ToolInput: multiAgentHookInput(invocation.Payload.Arguments),
	}, true
}

func (e *MultiAgentToolExecutor) PostToolUsePayload(invocation *tool.Invocation, output *tool.Output) (*tool.PostToolUsePayload, bool) {
	if invocation == nil || output == nil || invocation.Payload.Kind != tool.PayloadFunction {
		return nil, false
	}
	return &tool.PostToolUsePayload{
		ToolName:     multiAgentHookToolName(e.kind),
		ToolUseID:    firstNonEmptyAgentString(output.CallID, invocation.CallID),
		ToolInput:    multiAgentHookInput(invocation.Payload.Arguments),
		ToolResponse: outputHookResponse(output),
	}, true
}

func (e *MultiAgentToolExecutor) WithUpdatedHookInput(invocation *tool.Invocation, updatedInput any) (*tool.Invocation, error) {
	if invocation == nil || invocation.Payload.Kind != tool.PayloadFunction {
		return nil, tool.RespondToModel("multi-agent hook input rewrite received unsupported payload")
	}
	data, err := json.Marshal(updatedInput)
	if err != nil {
		return nil, tool.RespondToModel(fmt.Sprintf("failed to serialize rewritten multi-agent arguments: %v", err))
	}
	updated := *invocation
	updated.Payload.Arguments = string(data)
	return &updated, nil
}

func multiAgentToolSpec(kind MultiAgentToolKind) tool.Spec {
	name := tool.NamespacedName(MultiAgentV1Namespace, string(kind))
	switch kind {
	case MultiAgentToolSpawn:
		return tool.Spec{Name: name, Description: "Spawns a sub-agent to work on a task.", InputSchema: multiAgentObjectSchema(map[string]any{
			"message":          map[string]any{"type": "string", "description": "Initial task message."},
			"items":            map[string]any{"type": "array", "description": "Optional structured input items.", "items": map[string]any{}},
			"agent_type":       map[string]any{"type": "string", "description": "Optional configured agent role."},
			"model":            map[string]any{"type": "string", "description": "Optional model override."},
			"reasoning_effort": map[string]any{"type": "string", "description": "Optional reasoning effort override."},
			"service_tier":     map[string]any{"type": "string", "description": "Optional service tier override."},
			"fork_context":     map[string]any{"type": "boolean", "description": "Whether to include the parent context."},
		}, nil), Parallel: true}
	case MultiAgentToolSend:
		return tool.Spec{Name: name, Description: "Sends input to an existing sub-agent.", InputSchema: multiAgentObjectSchema(map[string]any{
			"target":    map[string]any{"type": "string", "description": "Agent id."},
			"message":   map[string]any{"type": "string", "description": "Message to send."},
			"items":     map[string]any{"type": "array", "description": "Optional structured input items.", "items": map[string]any{}},
			"interrupt": map[string]any{"type": "boolean", "description": "Interrupt the target first."},
		}, []string{"target"})}
	case MultiAgentToolWait:
		return tool.Spec{Name: name, Description: "Waits for one or more sub-agents and reports status.", InputSchema: multiAgentObjectSchema(map[string]any{
			"targets":    map[string]any{"type": "array", "description": "Agent ids.", "items": map[string]any{"type": "string"}},
			"timeout_ms": map[string]any{"type": "integer", "minimum": 0, "description": "Optional timeout in milliseconds."},
		}, nil)}
	case MultiAgentToolResume:
		return tool.Spec{Name: name, Description: "Resumes a closed sub-agent.", InputSchema: multiAgentObjectSchema(map[string]any{
			"id": map[string]any{"type": "string", "description": "Agent id."},
		}, []string{"id"})}
	case MultiAgentToolClose:
		return tool.Spec{Name: name, Description: "Closes a sub-agent.", InputSchema: multiAgentObjectSchema(map[string]any{
			"target": map[string]any{"type": "string", "description": "Agent id."},
		}, []string{"target"})}
	default:
		return tool.Spec{Name: name}
	}
}

func multiAgentObjectSchema(properties map[string]any, required []string) map[string]any {
	schema := map[string]any{
		"type":                 "object",
		"properties":           properties,
		"additionalProperties": false,
	}
	if required != nil {
		requiredValues := make([]any, len(required))
		for i := range required {
			requiredValues[i] = required[i]
		}
		schema["required"] = requiredValues
	}
	return schema
}

func multiAgentHookToolName(kind MultiAgentToolKind) *tool.HookToolName {
	if kind == MultiAgentToolSpawn {
		return &tool.HookToolName{Name: string(kind), MatcherAliases: []string{"Agent"}}
	}
	return &tool.HookToolName{Name: MultiAgentV1Namespace + "." + string(kind)}
}

func multiAgentSearchText(kind MultiAgentToolKind) string {
	switch kind {
	case MultiAgentToolSpawn:
		return "spawn_agent spawn agent subagent sub-agent delegate delegation parallel work worker explorer fork model reasoning"
	case MultiAgentToolSend:
		return "send_input send message existing agent subagent follow up interrupt redirect queue target"
	case MultiAgentToolWait:
		return "wait_agent wait agent subagent status final result complete timeout targets"
	case MultiAgentToolResume:
		return "resume_agent resume reopen closed agent subagent thread id target"
	case MultiAgentToolClose:
		return "close_agent close shutdown stop agent subagent thread status target"
	default:
		return string(kind)
	}
}

func multiAgentHookInput(arguments string) any {
	if strings.TrimSpace(arguments) == "" {
		return map[string]any{}
	}
	var value any
	if err := json.Unmarshal([]byte(arguments), &value); err != nil {
		return arguments
	}
	return value
}

func outputHookResponse(output *tool.Output) any {
	if output == nil {
		return nil
	}
	if output.Data != nil {
		if value, ok := output.Data["result"]; ok {
			return value
		}
		return output.Data
	}
	return output.Body
}

func normalizeAgentTarget(target string) string {
	return strings.TrimPrefix(strings.TrimSpace(target), "/")
}

func firstNonEmptyAgentString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

var _ tool.Executor = (*MultiAgentToolExecutor)(nil)
var _ tool.PreToolUsePayloadProvider = (*MultiAgentToolExecutor)(nil)
var _ tool.PostToolUsePayloadProvider = (*MultiAgentToolExecutor)(nil)
var _ tool.HookInputUpdater = (*MultiAgentToolExecutor)(nil)
