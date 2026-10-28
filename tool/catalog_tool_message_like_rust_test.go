package tool

import (
	"testing"
)

func catalogTestSpec() Spec {
	return Spec{
		Name:        PlainName("history/list_windows"),
		Description: "bundled description",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent":   map[string]any{"type": "string"},
				"payload": map[string]any{"type": "string", "encrypted": true},
			},
			"required": []string{"payload"},
		},
		Parallel:             true,
		NamespaceDescription: "bundled namespace",
	}
}

func TestApplyCatalogToolOverrideDescriptionLikeRust(t *testing.T) {
	bundled := catalogTestSpec()

	if got := ApplyCatalogToolOverride(bundled, nil); got.Description != "bundled description" {
		t.Fatalf("nil override description = %q", got.Description)
	}

	description := "catalog description"
	got := ApplyCatalogToolOverride(bundled, &CatalogToolOverride{Description: &description})
	if got.Description != description {
		t.Fatalf("description = %q, want %q", got.Description, description)
	}
	if got.Name != bundled.Name || !got.Parallel || got.NamespaceDescription != bundled.NamespaceDescription {
		t.Fatalf("identity or transport fields changed: %#v", got)
	}

	empty := ""
	got = ApplyCatalogToolOverride(bundled, &CatalogToolOverride{Description: &empty})
	if got.Description != "" {
		t.Fatalf("explicit empty description = %q, want empty", got.Description)
	}
}

func TestApplyCatalogToolOverrideParametersLikeRust(t *testing.T) {
	bundled := catalogTestSpec()
	parameters := `{"type":"object","properties":{"payload":{"type":"string"},"agent":{"type":"number"}},"required":["payload"]}`
	got := ApplyCatalogToolOverride(bundled, &CatalogToolOverride{Parameters: &parameters})
	properties, _ := got.InputSchema["properties"].(map[string]any)
	payload, _ := properties["payload"].(map[string]any)
	agent, _ := properties["agent"].(map[string]any)
	if encrypted, _ := payload["encrypted"].(bool); !encrypted {
		t.Fatalf("encrypted marker was not restored: %#v", payload)
	}
	if agent["type"] != "number" {
		t.Fatalf("catalog parameter override was not applied: %#v", agent)
	}
	if bundled.InputSchema["properties"].(map[string]any)["payload"].(map[string]any)["encrypted"] != true {
		t.Fatal("bundled schema was mutated")
	}
}

func TestApplyCatalogToolOverrideRejectsIncompatibleParametersLikeRust(t *testing.T) {
	bundled := catalogTestSpec()
	for name, parameters := range map[string]string{
		"invalid json":    `{"type":`,
		"non-object root": `{"type":"array","items":{"type":"string"}}`,
		"drops encrypted": `{"type":"object","properties":{"agent":{"type":"string"}}}`,
	} {
		t.Run(name, func(t *testing.T) {
			got := ApplyCatalogToolOverride(bundled, &CatalogToolOverride{Parameters: &parameters})
			if got.InputSchema["required"] == nil {
				t.Fatalf("bundled schema was replaced by %#v", got.InputSchema)
			}
			properties, _ := got.InputSchema["properties"].(map[string]any)
			agent, _ := properties["agent"].(map[string]any)
			if agent["type"] != "string" {
				t.Fatalf("bundled agent schema changed: %#v", agent)
			}
		})
	}
}
