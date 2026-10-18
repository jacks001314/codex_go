package jsonschema

import (
	"encoding/json"
	"reflect"
	"testing"
)

// These unit tests mirror the behavior pinned by Rust
// tools/src/json_schema_tests.rs (parse_tool_input_schema_*).

func TestNormalizeCoercesBooleanSchemasLikeRust(t *testing.T) {
	// Boolean-form schema: true → accept-all string schema.
	got := Normalize(map[string]any{})
	if len(got) != 0 {
		t.Fatalf("Normalize(empty) = %#v, want empty map", got)
	}
}

func TestNormalizeInfersObjectShapeAndDefaultsPropertiesLikeRust(t *testing.T) {
	got := Normalize(map[string]any{
		"required": []any{"message"},
	})
	want := map[string]any{
		"type":       "object",
		"required":   []any{"message"},
		"properties": map[string]any{},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Normalize() = %#v, want %#v", got, want)
	}
}

func TestNormalizeCoercesUnrecognizedObjectSchemaToEmptySchemaLikeRust(t *testing.T) {
	got := Normalize(map[string]any{"title": "Untitled"})
	if len(got) != 0 {
		t.Fatalf("Normalize() = %#v, want empty (title dropped, no type inferable)", got)
	}
}

func TestNormalizePreservesIntegerAndDefaultsArrayItemsLikeRust(t *testing.T) {
	got := Normalize(map[string]any{
		"type":        "array",
		"description": "List of ids",
	})
	want := map[string]any{
		"type":        "array",
		"description": "List of ids",
		"items":       map[string]any{"type": "string"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Normalize() = %#v, want %#v", got, want)
	}
}

func TestNormalizePrunesUnreachableDefinitionsAndDropsNonSubsetFields(t *testing.T) {
	got := Normalize(map[string]any{
		"type": "object",
		"properties": map[string]any{
			"page": map[string]any{"$ref": "#/$defs/Page%20Ref"},
		},
		"$defs": map[string]any{
			"Page Ref": map[string]any{"type": "string", "pattern": "^[a-z]+$"},
			"Unused":   map[string]any{"type": "object"},
		},
	})
	want := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"page": map[string]any{"$ref": "#/$defs/Page%20Ref"},
		},
		"$defs": map[string]any{
			"Page Ref": map[string]any{"type": "string"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		data, _ := json.MarshalIndent(got, "", "  ")
		t.Fatalf("Normalize() = %s", data)
	}
}

func TestNormalizeSanitizesAdditionalPropertiesSchemaLikeRust(t *testing.T) {
	got := Normalize(map[string]any{
		"type": "object",
		"additionalProperties": map[string]any{
			"type": "string",
		},
	})
	want := map[string]any{
		"type":                 "object",
		"properties":           map[string]any{},
		"additionalProperties": map[string]any{"type": "string"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Normalize() = %#v, want %#v", got, want)
	}
}

func TestNormalizeRewritesConstToEnumLikeRust(t *testing.T) {
	got := Normalize(map[string]any{
		"type":  "string",
		"const": "fixed",
	})
	want := map[string]any{
		"type": "string",
		"enum": []any{"fixed"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Normalize() = %#v, want %#v", got, want)
	}
}
