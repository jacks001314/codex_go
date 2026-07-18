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
	setSpawnDefault(&resolved.Model, c.defaults.Model)
	setSpawnDefault(&resolved.ReasoningEffort, c.defaults.ReasoningEffort)
	setSpawnDefault(&resolved.ServiceTier, c.defaults.ServiceTier)
	roleName := DefaultRoleName
	if resolved.AgentType != nil && strings.TrimSpace(*resolved.AgentType) != "" {
		roleName = strings.TrimSpace(*resolved.AgentType)
	}
	if role, ok := c.resolver.Resolve(&RuntimeConfig{AgentRoles: c.roles}, roleName); ok {
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
