package codemode

import (
	"fmt"
	"strings"
	"testing"
)

func TestRenderJSONSchemaResolvesRecursiveLocalRefsWithEscapedPointerSegments(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"clauses": map[string]any{
				"type":  "array",
				"items": map[string]any{"$ref": "#/$defs/Boolean~1Clause~0v1"},
			},
		},
		"$defs": map[string]any{
			"Boolean/Clause~v1": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"$ref": "#/$defs/Query"},
				},
			},
			"Query": map[string]any{
				"oneOf": []any{
					map[string]any{"type": "string"},
					map[string]any{
						"type": "object",
						"properties": map[string]any{
							"clauses": map[string]any{
								"type":  "array",
								"items": map[string]any{"$ref": "#/$defs/Boolean~1Clause~0v1"},
							},
						},
					},
				},
			},
		},
	}
	rendered := RenderJSONSchemaToTypeScript(schema)
	if !strings.Contains(rendered, "clauses?: Array<{ query?: string | { clauses?: Array<{") {
		t.Fatalf("recursive rendering missing nested shape:\n%s", rendered)
	}
	if !strings.Contains(rendered, "query?: string | { clauses?: Array<unknown>; };") {
		t.Fatalf("recursive rendering missing bounded closure:\n%s", rendered)
	}
}

func TestRenderJSONSchemaResolvesRefSiblingsAndAllOfPrecedence(t *testing.T) {
	if got := RenderJSONSchemaToTypeScript(map[string]any{
		"$ref":  "#/$defs/Label",
		"enum":  []any{"A"},
		"$defs": map[string]any{"Label": map[string]any{"type": "string"}},
	}); got != `(string) & ("A")` {
		t.Fatalf("ref siblings = %q", got)
	}
	if got := RenderJSONSchemaToTypeScript(map[string]any{
		"$ref":  "#/$defs/Foo%20Bar",
		"$defs": map[string]any{"Foo Bar": map[string]any{"type": "string"}},
	}); got != "string" {
		t.Fatalf("percent-encoded ref = %q", got)
	}
	if got := RenderJSONSchemaToTypeScript(map[string]any{
		"allOf": []any{
			map[string]any{"$ref": "#/$defs/Choice"},
			map[string]any{"type": "object", "properties": map[string]any{"value": map[string]any{"type": "string"}}},
		},
		"$defs": map[string]any{
			"Choice": map[string]any{"oneOf": []any{map[string]any{"type": "string"}, map[string]any{"type": "number"}}},
		},
	}); got != "(string | number) & { value?: string; }" {
		t.Fatalf("allOf precedence = %q", got)
	}
}

func TestRenderJSONSchemaLeavesLocalRefsUnderNestedResourcesUnresolved(t *testing.T) {
	schema := map[string]any{
		"$defs": map[string]any{"Choice": map[string]any{"type": "string"}},
		"type":  "object",
		"properties": map[string]any{
			"nested": map[string]any{
				"$id": "urn:nested",
				"$defs": map[string]any{
					"Choice": map[string]any{"type": "number"},
				},
				"type": "object",
				"properties": map[string]any{
					"value": map[string]any{"$ref": "#/$defs/Choice"},
				},
			},
		},
	}
	if got := RenderJSONSchemaToTypeScript(schema); got != "{ nested?: { value?: unknown; }; }" {
		t.Fatalf("nested resource = %q", got)
	}
}

func TestRenderJSONSchemaBoundsExpansionsWithoutChargingDanglingRefs(t *testing.T) {
	properties := map[string]any{}
	for index := 0; index < maxTotalLocalRefExpansions; index++ {
		properties[fmt.Sprintf("a_missing_%d", index)] = map[string]any{
			"$ref": "#/$defs/Missing",
		}
	}
	properties["z_valid"] = map[string]any{"$ref": "#/$defs/Valid"}
	rendered := RenderJSONSchemaToTypeScript(map[string]any{
		"type":       "object",
		"properties": properties,
		"$defs":      map[string]any{"Valid": map[string]any{"type": "string"}},
	})
	if !strings.Contains(rendered, "z_valid?: string;") {
		t.Fatalf("dangling refs must not consume the expansion budget:\n%s", rendered)
	}

	expanded := map[string]any{}
	for index := 0; index < maxTotalLocalRefExpansions+2; index++ {
		expanded[fmt.Sprintf("property_%d", index)] = map[string]any{"$ref": "#/$defs/Item"}
	}
	rendered = RenderJSONSchemaToTypeScript(map[string]any{
		"type":       "object",
		"properties": expanded,
		"$defs":      map[string]any{"Item": map[string]any{"type": "string"}},
	})
	if strings.Count(rendered, "string") != maxTotalLocalRefExpansions {
		t.Fatalf("expanded strings = %d, want %d:\n%s", strings.Count(rendered, "string"), maxTotalLocalRefExpansions, rendered)
	}
	if strings.Count(rendered, "unknown") != 2 {
		t.Fatalf("unknown count = %d, want 2:\n%s", strings.Count(rendered, "unknown"), rendered)
	}
}

func TestRenderJSONSchemaRepeatedLargeRefsExhaustRenderWorkBudget(t *testing.T) {
	properties := map[string]any{}
	for index := 0; index < maxTotalLocalRefExpansions; index++ {
		properties[fmt.Sprintf("property_%d", index)] = map[string]any{"$ref": "#/$defs/Item"}
	}
	description := strings.Repeat("x", maxRenderedSchemaBytes/2)
	schema := map[string]any{
		"type":       "object",
		"properties": properties,
		"$defs": map[string]any{
			"Item": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"value": map[string]any{"type": "string", "description": description},
				},
			},
		},
	}
	renderer := newJSONSchemaTypeRenderer(schema)
	if got := renderer.render(schema); got != "unknown" {
		t.Fatalf("render = %q, want unknown", got)
	}
	if !renderer.renderWorkBudgetExhausted {
		t.Fatal("render work budget should be exhausted")
	}
}

func TestRenderJSONSchemaOversizedRefLiteralExhaustsRenderWorkBudget(t *testing.T) {
	schema := map[string]any{
		"$ref":  "#/$defs/Value",
		"$defs": map[string]any{"Value": map[string]any{"const": strings.Repeat("x", maxRenderWorkBytes)}},
	}
	renderer := newJSONSchemaTypeRenderer(schema)
	if got := renderer.render(schema); got != "unknown" {
		t.Fatalf("render = %q, want unknown", got)
	}
	if !renderer.renderWorkBudgetExhausted {
		t.Fatal("render work budget should be exhausted")
	}
}

func TestRenderJSONSchemaHasHardSizeCap(t *testing.T) {
	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"value": map[string]any{"type": "string", "description": strings.Repeat("x", maxRenderedSchemaBytes)},
		},
	}
	if got := RenderJSONSchemaToTypeScript(schema); got != "unknown" {
		t.Fatalf("oversized render = %q, want unknown", got)
	}
}
