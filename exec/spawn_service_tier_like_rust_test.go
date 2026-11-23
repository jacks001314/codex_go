package exec

import (
	"strings"
	"testing"

	"codex_go/agent"
	"codex_go/config"
	"codex_go/model"
)

// Mirrors Rust #63376 in the exec spawn path: an explicit tier is normalized,
// validated against the child's model, and ignored when the argument is not
// exposed.
func TestExecSpawnServiceTierLikeRust(t *testing.T) {
	modelsManager := model.NewStaticModelsManager(model.ModelsResponse{Models: []model.ModelInfo{{
		Slug: "tier-model", DisplayName: "tier-model", Visibility: model.VisibilityList, SupportedInAPI: true,
		ServiceTiers: []string{"priority"}, MultiAgentVersion: "v2",
	}}})
	controller := &execAgentController{
		modelsManager: modelsManager, featureSettings: map[string]bool{"fast_mode": true},
		exposeSpawnServiceTier: true, parentModel: "tier-model", multiAgentVersion: agent.VersionV2,
	}
	tier := "priority"
	args := &agent.SpawnAgentArgs{Model: stringPtr("tier-model"), ServiceTier: &tier}
	if err := controller.resolveSpawnModelOverrides(args); err != nil {
		t.Fatalf("resolveSpawnModelOverrides() error = %v", err)
	}
	if args.ServiceTier == nil || *args.ServiceTier != "priority" {
		t.Fatalf("resolved tier = %#v, want priority", args.ServiceTier)
	}

	unsupported := "flex"
	args = &agent.SpawnAgentArgs{Model: stringPtr("tier-model"), ServiceTier: &unsupported}
	err := controller.resolveSpawnModelOverrides(args)
	if err == nil || !strings.Contains(err.Error(), "Service tier `flex` is unavailable for model `tier-model`") {
		t.Fatalf("unsupported tier error = %v", err)
	}

	def := "default"
	args = &agent.SpawnAgentArgs{Model: stringPtr("tier-model"), ServiceTier: &def}
	if err := controller.resolveSpawnModelOverrides(args); err != nil {
		t.Fatalf("default tier rejected: %v", err)
	}
	if args.ServiceTier == nil || *args.ServiceTier != "default" {
		t.Fatalf("resolved default tier = %#v", args.ServiceTier)
	}

	controller.exposeSpawnServiceTier = false
	tier = "priority"
	args = &agent.SpawnAgentArgs{Model: stringPtr("tier-model"), ServiceTier: &tier}
	if err := controller.resolveSpawnModelOverrides(args); err != nil {
		t.Fatalf("gated resolve error = %v", err)
	}
	if args.ServiceTier != nil {
		t.Fatalf("gated tier = %#v, want dropped", args.ServiceTier)
	}
}

// Mirrors Rust #63376 request construction: an explicit `default` reaches the
// request as the default sentinel and a flex tier survives configuration
// filtering.
func TestEffectiveServiceTierExplicitOverrideLikeRust(t *testing.T) {
	cfg := &config.Config{Values: map[string]any{"features": map[string]any{"fast_mode": true}}}
	if got := effectiveServiceTier(cfg, "gpt-5", "default"); got != "default" {
		t.Fatalf("effectiveServiceTier(default) = %q", got)
	}
	if got := effectiveServiceTier(cfg, "gpt-5", "flex"); got != "flex" {
		t.Fatalf("effectiveServiceTier(flex) = %q", got)
	}
	if got := effectiveServiceTier(cfg, "gpt-5", ""); got != "" {
		t.Fatalf("effectiveServiceTier(none) = %q", got)
	}
}

func stringPtr(value string) *string { return &value }
