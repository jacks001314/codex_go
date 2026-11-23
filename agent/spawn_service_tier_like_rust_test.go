package agent

import (
	"testing"

	"codex_go/tool"
)

// Mirrors Rust #63376: `service_tier` is an optional spawn argument exposed only
// by `multi_agent_v2.expose_spawn_agent_service_tier`, for V1 and V2 alike, and
// hidden spawn metadata still removes it.
func TestSpawnAgentServiceTierSchemaLikeRust(t *testing.T) {
	for _, version := range []struct {
		name string
		kind MultiAgentVersion
	}{
		{"v1", VersionV1},
		{"v2", VersionV2},
	} {
		registry := tool.NewRegistry()
		options := &MultiAgentHandlerOptions{
			Controller:                NewMemoryToolController(),
			Exposure:                  tool.ExposureModelVisible,
			Version:                   version.kind,
			Namespace:                 MultiAgentV2Namespace,
			ExposeSpawnModelOverrides: true,
		}
		if version.kind == VersionV1 {
			options.Namespace = MultiAgentV1Namespace
		}
		if err := RegisterMultiAgentHandlersWithOptions(registry, options); err != nil {
			t.Fatalf("%s: register error = %v", version.name, err)
		}
		spawn, ok := registry.Lookup(tool.NamespacedName(options.Namespace, "spawn_agent"))
		if !ok {
			t.Fatalf("%s: spawn_agent missing", version.name)
		}
		properties, _ := spawn.Spec().InputSchema["properties"].(map[string]any)
		if _, exposed := properties["service_tier"]; exposed {
			t.Fatalf("%s: service_tier exposed without the setting", version.name)
		}

		options.ExposeSpawnServiceTier = true
		registry = tool.NewRegistry()
		if err := RegisterMultiAgentHandlersWithOptions(registry, options); err != nil {
			t.Fatalf("%s: register error = %v", version.name, err)
		}
		spawn, _ = registry.Lookup(tool.NamespacedName(options.Namespace, "spawn_agent"))
		properties, _ = spawn.Spec().InputSchema["properties"].(map[string]any)
		tier, exposed := properties["service_tier"].(map[string]any)
		if !exposed {
			t.Fatalf("%s: service_tier missing with the setting", version.name)
		}
		want := "Service tier override for this agent. Use `default` or a tier supported by its model. Omit unless explicitly requested."
		if tier["type"] != "string" || tier["description"] != want {
			t.Fatalf("%s: service_tier schema = %#v", version.name, tier)
		}
	}
}

// Mirrors Rust #63376's catalog parameter rule: `service_tier` follows `model`
// into a catalog schema, and is dropped when the catalog drops the model.
func TestSpawnAgentServiceTierCatalogParametersLikeRust(t *testing.T) {
	bundled := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"model":        map[string]any{"type": "string"},
			"service_tier": spawnServiceTierSchema(),
			"message":      map[string]any{"type": "string", "encrypted": true},
		},
	}
	withModel := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"model":   map[string]any{"type": "string"},
			"message": map[string]any{"type": "string", "encrypted": true},
		},
	}
	moved := spawnServiceTierInCatalogParameters(withModel, bundled)
	properties := moved["properties"].(map[string]any)
	if _, ok := properties["service_tier"]; !ok {
		t.Fatal("catalog schema with model dropped service_tier")
	}

	withoutModel := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"message": map[string]any{"type": "string", "encrypted": true},
		},
		"required": []any{"service_tier"},
	}
	pruned := spawnServiceTierInCatalogParameters(withoutModel, bundled)
	prunedProperties := pruned["properties"].(map[string]any)
	if _, ok := prunedProperties["service_tier"]; ok {
		t.Fatal("catalog schema without model kept service_tier")
	}
	for _, name := range pruned["required"].([]any) {
		if name == "service_tier" {
			t.Fatal("catalog schema kept service_tier required")
		}
	}
}
