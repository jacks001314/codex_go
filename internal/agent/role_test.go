package agent

import (
	"strings"
	"testing"
)

func TestRoleResolverResolvePrefersUserRoles(t *testing.T) {
	resolver := NewRoleResolver(map[string]RoleConfig{
		"explorer": {Description: "built-in"},
	})
	config := &RuntimeConfig{
		AgentRoles: map[string]RoleConfig{
			"explorer": {Description: "user"},
		},
	}
	role, ok := resolver.Resolve(config, "explorer")
	if !ok {
		t.Fatalf("Resolve() ok = false, want true")
	}
	if role.Description != "user" {
		t.Fatalf("Resolve() description = %q, want user", role.Description)
	}
}

func TestRoleResolverApplyPreservesProviderAndTierUnlessSet(t *testing.T) {
	resolver := NewRoleResolver(map[string]RoleConfig{
		"locked-model": {
			Settings: map[string]string{
				"model":                  "gpt-5",
				"model_reasoning_effort": "high",
			},
		},
		"locked-tier": {
			Settings: map[string]string{
				"service_tier": "priority",
			},
		},
	})
	config := &RuntimeConfig{
		ModelProvider: "openai",
		ServiceTier:   "default",
	}
	if err := resolver.Apply(config, "locked-model"); err != nil {
		t.Fatalf("Apply() error = %v", err)
	}
	if config.ModelProvider != "openai" || config.ServiceTier != "default" {
		t.Fatalf("Apply() provider/tier = %s/%s, want openai/default", config.ModelProvider, config.ServiceTier)
	}
	if config.Model != "gpt-5" || config.Settings["model_reasoning_effort"] != "high" {
		t.Fatalf("Apply() config = %#v", config)
	}
	if err := resolver.Apply(config, "locked-tier"); err != nil {
		t.Fatalf("Apply(locked-tier) error = %v", err)
	}
	if config.ServiceTier != "priority" {
		t.Fatalf("Apply(locked-tier) ServiceTier = %q, want priority", config.ServiceTier)
	}
}

func TestRoleResolverApplyUnknownRole(t *testing.T) {
	resolver := NewRoleResolver(nil)
	err := resolver.Apply(&RuntimeConfig{}, "missing")
	if err == nil || !strings.Contains(err.Error(), "unknown agent_type") {
		t.Fatalf("Apply(missing) error = %v, want unknown agent_type", err)
	}
}

func TestSpawnToolDescriptionFormatsRoles(t *testing.T) {
	resolver := NewRoleResolver(map[string]RoleConfig{
		"default": {Description: "default role"},
	})
	description := resolver.SpawnToolDescription(map[string]RoleConfig{
		"reviewer": {
			Description: "reviews changes",
			Settings: map[string]string{
				"model":                  "gpt-5",
				"model_reasoning_effort": "high",
				"service_tier":           "priority",
			},
		},
	})
	for _, want := range []string{
		"`default` is used",
		"`reviewer`: reviews changes",
		"model is set to `gpt-5`",
		"service tier is set to `priority`",
	} {
		if !strings.Contains(description, want) {
			t.Fatalf("SpawnToolDescription() missing %q in:\n%s", want, description)
		}
	}
}
