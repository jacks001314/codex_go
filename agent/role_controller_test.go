package agent

import (
	"context"
	"strings"
	"testing"
)

type captureToolController struct{ spawned *SpawnAgentArgs }

func (c *captureToolController) SpawnAgent(_ context.Context, args *SpawnAgentArgs) (*SpawnAgentResult, error) {
	copy := cloneSpawnAgentArgs(args)
	c.spawned = &copy
	return &SpawnAgentResult{AgentID: "child"}, nil
}
func (*captureToolController) SendInput(context.Context, *SendInputArgs) (*SendInputResult, error) {
	return &SendInputResult{}, nil
}
func (*captureToolController) WaitAgent(context.Context, *WaitAgentArgs) (*WaitAgentResult, error) {
	return &WaitAgentResult{}, nil
}
func (*captureToolController) ResumeAgent(context.Context, *ResumeAgentArgs) (*ResumeAgentResult, error) {
	return &ResumeAgentResult{}, nil
}
func (*captureToolController) CloseAgent(context.Context, *CloseAgentArgs) (*CloseAgentResult, error) {
	return &CloseAgentResult{}, nil
}

func TestRoleAwareToolControllerAppliesDefaultsAndLockedRoleSettings(t *testing.T) {
	delegate := &captureToolController{}
	controller := NewRoleAwareToolController(delegate, map[string]RoleConfig{
		"reviewer": {Description: "Reviews.", NicknameCandidates: []string{"Sage", "Scout"}, Settings: map[string]string{"model": "gpt-review", "model_reasoning_effort": "high", "service_tier": "fast"}},
	}, SpawnDefaults{Model: "gpt-default", ReasoningEffort: "medium", ServiceTier: "auto"})
	role := " reviewer "
	requestedModel := "gpt-requested"
	if _, err := controller.SpawnAgent(context.Background(), &SpawnAgentArgs{AgentType: &role, Model: &requestedModel}); err != nil {
		t.Fatal(err)
	}
	got := delegate.spawned
	if got == nil || got.ResolvedRole != "reviewer" || value(got.Model) != "gpt-review" || value(got.ReasoningEffort) != "high" || value(got.ServiceTier) != "fast" || strings.Join(got.NicknameCandidates, ",") != "Sage,Scout" {
		t.Fatalf("spawned = %+v", got)
	}
}

func TestRoleAwareToolControllerUsesDefaultsWithoutConfiguredRole(t *testing.T) {
	delegate := &captureToolController{}
	controller := NewRoleAwareToolController(delegate, nil, SpawnDefaults{Model: "gpt-default", ReasoningEffort: "medium"})
	if _, err := controller.SpawnAgent(context.Background(), &SpawnAgentArgs{}); err != nil {
		t.Fatal(err)
	}
	if value(delegate.spawned.Model) != "gpt-default" || value(delegate.spawned.ReasoningEffort) != "medium" {
		t.Fatalf("spawned = %+v", delegate.spawned)
	}
}

func TestRoleAwareToolControllerRejectsUnknownRoleAndForkRole(t *testing.T) {
	controller := NewRoleAwareToolController(&captureToolController{}, nil, SpawnDefaults{})
	unknown := "missing"
	if _, err := controller.SpawnAgent(context.Background(), &SpawnAgentArgs{AgentType: &unknown}); err == nil || !strings.Contains(err.Error(), "unknown agent_type") {
		t.Fatalf("unknown role error = %v", err)
	}
	role := "reviewer"
	if _, err := controller.SpawnAgent(context.Background(), &SpawnAgentArgs{AgentType: &role, ForkContext: true}); err == nil || !strings.Contains(err.Error(), "fork_context") {
		t.Fatalf("fork role error = %v", err)
	}
}

func value(value *string) string {
	if value == nil {
		return ""
	}
	return *value
}
