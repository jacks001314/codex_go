package turn

import (
	"errors"
	"reflect"
	"strings"
	"testing"
)

// TestDynamicToolInputSchemaNormalizesLikeRust pins the parse_dynamic_tool
// wiring: dynamic tool input schemas go through the JsonSchema subset policy,
// so non-subset fields are dropped and unreachable $defs are pruned.
func TestDynamicToolInputSchemaNormalizesLikeRust(t *testing.T) {
	schema, ok := dynamicToolInputSchema(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"page": map[string]any{
				"$ref":    "#/$defs/Page",
				"pattern": "^[a-z]+$",
			},
		},
		"$defs": map[string]any{
			"Page":   map[string]any{"type": "string", "format": "uri"},
			"Unused": map[string]any{"type": "object"},
		},
	})
	if !ok {
		t.Fatal("dynamicToolInputSchema() = false, want true")
	}
	want := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"page": map[string]any{"$ref": "#/$defs/Page"},
		},
		"$defs": map[string]any{
			"Page": map[string]any{"type": "string"},
		},
	}
	if !reflect.DeepEqual(schema, want) {
		t.Fatalf("dynamicToolInputSchema() = %#v, want %#v", schema, want)
	}
}

func TestValidateDynamicToolsRejectsEmptyNamespace(t *testing.T) {
	err := ValidateDynamicTools([]DynamicToolSpec{{
		Type: "namespace",
		Name: "empty_namespace",
	}})
	if !errors.Is(err, ErrInvalidTurnRequest) {
		t.Fatalf("ValidateDynamicTools() error = %v, want ErrInvalidTurnRequest", err)
	}
	if !strings.Contains(err.Error(), "must contain at least one tool") {
		t.Fatalf("ValidateDynamicTools() error = %q", err.Error())
	}
}
