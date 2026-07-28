package agent

import (
	"context"
	"fmt"
	"strings"
)

// RoleAwareToolController applies spawn defaults and configured role settings
// before delegating lifecycle operations to the runtime controller.
type RoleAwareToolController struct {
	delegate ToolController
	resolver *RoleResolver
	roles    map[string]RoleConfig
	defaults SpawnDefaults
}

func NewRoleAwareToolController(delegate ToolController, roles map[string]RoleConfig, defaults SpawnDefaults) *RoleAwareToolController {
	if delegate == nil {
		delegate = NewMemoryToolController()
	}
	return &RoleAwareToolController{delegate: delegate, resolver: NewRoleResolver(map[string]RoleConfig{}), roles: cloneRoles(roles), defaults: defaults}
}

func (c *RoleAwareToolController) SpawnAgent(ctx context.Context, args *SpawnAgentArgs) (*SpawnAgentResult, error) {
	if args == nil {
		args = &SpawnAgentArgs{}
	}
	resolved := cloneSpawnAgentArgs(args)
	if resolved.ForkContext && resolved.AgentType != nil && strings.TrimSpace(*resolved.AgentType) != "" {
		return nil, fmt.Errorf("agent_type is not supported when fork_context is true")
	}
	fullHistory := resolved.TaskName != "" && (resolved.ForkTurns == nil || strings.EqualFold(strings.TrimSpace(*resolved.ForkTurns), "all") || strings.TrimSpace(*resolved.ForkTurns) == "")
	if fullHistory && resolved.AgentType != nil && strings.TrimSpace(*resolved.AgentType) != "" {
		return nil, fmt.Errorf("full-history forked agents inherit the parent agent type; omit agent_type, or spawn without a full-history fork")
	}
	setSpawnDefault(&resolved.Model, c.defaults.Model)
	setSpawnDefault(&resolved.ReasoningEffort, c.defaults.ReasoningEffort)
	setSpawnDefault(&resolved.ServiceTier, c.defaults.ServiceTier)
	roleName := DefaultRoleName
	if resolved.AgentType != nil && strings.TrimSpace(*resolved.AgentType) != "" {
		roleName = strings.TrimSpace(*resolved.AgentType)
	}
	if fullHistory {
		resolved.ResolvedRole = ""
	} else if role, ok := c.resolver.Resolve(&RuntimeConfig{AgentRoles: c.roles}, roleName); ok {
		resolved.ResolvedRole = roleName
		resolved.NicknameCandidates = append([]string(nil), role.NicknameCandidates...)
		applyRoleSpawnSettings(&resolved, role.Settings)
	} else if roleName != DefaultRoleName {
		return nil, fmt.Errorf("unknown agent_type %q", roleName)
	}
	return c.delegate.SpawnAgent(ctx, &resolved)
}

func (c *RoleAwareToolController) SendInput(ctx context.Context, args *SendInputArgs) (*SendInputResult, error) {
	return c.delegate.SendInput(ctx, args)
}
func (c *RoleAwareToolController) WaitAgent(ctx context.Context, args *WaitAgentArgs) (*WaitAgentResult, error) {
	return c.delegate.WaitAgent(ctx, args)
}
func (c *RoleAwareToolController) ResumeAgent(ctx context.Context, args *ResumeAgentArgs) (*ResumeAgentResult, error) {
	return c.delegate.ResumeAgent(ctx, args)
}
func (c *RoleAwareToolController) CloseAgent(ctx context.Context, args *CloseAgentArgs) (*CloseAgentResult, error) {
	return c.delegate.CloseAgent(ctx, args)
}

func (c *RoleAwareToolController) SendMessage(ctx context.Context, args *SendMessageArgs) error {
	delegate, ok := c.delegate.(V2ToolController)
	if !ok {
		return fmt.Errorf("multi-agent v2 controller is unavailable")
	}
	return delegate.SendMessage(ctx, args)
}

func (c *RoleAwareToolController) FollowupTask(ctx context.Context, args *FollowupTaskArgs) error {
	delegate, ok := c.delegate.(V2ToolController)
	if !ok {
		return fmt.Errorf("multi-agent v2 controller is unavailable")
	}
	return delegate.FollowupTask(ctx, args)
}

func (c *RoleAwareToolController) WaitForActivity(ctx context.Context, args *WaitForActivityArgs) (*WaitForActivityResult, error) {
	delegate, ok := c.delegate.(V2ToolController)
	if !ok {
		return nil, fmt.Errorf("multi-agent v2 controller is unavailable")
	}
	return delegate.WaitForActivity(ctx, args)
}

func (c *RoleAwareToolController) InterruptAgent(ctx context.Context, args *InterruptAgentArgs) (*InterruptAgentResult, error) {
	delegate, ok := c.delegate.(V2ToolController)
	if !ok {
		return nil, fmt.Errorf("multi-agent v2 controller is unavailable")
	}
	return delegate.InterruptAgent(ctx, args)
}

func (c *RoleAwareToolController) ListAgents(ctx context.Context, args *ListAgentsArgs) (*ListAgentsResult, error) {
	delegate, ok := c.delegate.(V2ToolController)
	if !ok {
		return nil, fmt.Errorf("multi-agent v2 controller is unavailable")
	}
	return delegate.ListAgents(ctx, args)
}

func cloneSpawnAgentArgs(args *SpawnAgentArgs) SpawnAgentArgs {
	cloned := *args
	cloned.Items = append([]any(nil), args.Items...)
	cloned.NicknameCandidates = append([]string(nil), args.NicknameCandidates...)
	return cloned
}

func setSpawnDefault(target **string, value string) {
	if *target == nil && strings.TrimSpace(value) != "" {
		trimmed := strings.TrimSpace(value)
		*target = &trimmed
	}
}

func applyRoleSpawnSettings(args *SpawnAgentArgs, settings map[string]string) {
	for key, value := range settings {
		value := strings.TrimSpace(value)
		if value == "" {
			continue
		}
		switch key {
		case "model":
			args.Model = &value
		case "model_reasoning_effort":
			args.ReasoningEffort = &value
		case "service_tier":
			args.ServiceTier = &value
		}
	}
}

var _ ToolController = (*RoleAwareToolController)(nil)
var _ V2ToolController = (*RoleAwareToolController)(nil)
