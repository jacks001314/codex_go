package jsonschema

import (
	"reflect"
	"testing"
)

// TestSubsetSchemaMatchesRustSubset mirrors serde deserializing a caller-supplied
// document into Rust's `JsonSchema`: unknown keys are dropped, known keys keep
// their declared shapes (including `minItems`), nested schemas are validated
// recursively, and a malformed known key reports ok=false.
func TestSubsetSchemaMatchesRustSubset(t *testing.T) {
	value := map[string]any{
		"type":        "object",
		"title":       "dropped by the subset",
		"pattern":     "^dropped$",
		"description": "kept",
		"required":    []any{"name"},
		"properties": map[string]any{
			"name": map[string]any{"type": "string", "encrypted": true},
			"tags": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": float64(1)},
		},
		"additionalProperties": false,
		"$defs":                map[string]any{"status": map[string]any{"type": "string", "enum": []any{"ok", "bad"}}},
	}
	got, ok := SubsetSchema(value)
	if !ok {
		t.Fatal("SubsetSchema() rejected a supported document")
	}
	want := map[string]any{
		"type":        "object",
		"description": "kept",
		"required":    []any{"name"},
		"properties": map[string]any{
			"name": map[string]any{"type": "string", "encrypted": true},
			"tags": map[string]any{"type": "array", "items": map[string]any{"type": "string"}, "minItems": float64(1)},
		},
		"additionalProperties": false,
		"$defs":                map[string]any{"status": map[string]any{"type": "string", "enum": []any{"ok", "bad"}}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("SubsetSchema() = %#v\nwant %#v", got, want)
	}
	// The input is not mutated.
	if _, present := value["title"]; !present {
		t.Fatal("SubsetSchema() mutated its input")
	}
}

// TestSubsetSchemaRejectsUnsupportedStructures covers the serde error path:
// a known key with the wrong shape fails the whole document.
func TestSubsetSchemaRejectsUnsupportedStructures(t *testing.T) {
	for _, value := range []any{
		[]any{},
		"not an object",
		map[string]any{"type": "object", "properties": []any{"not", "a", "table"}},
		map[string]any{"type": "object", "properties": map[string]any{"a": map[string]any{"required": "not a list"}}},
		map[string]any{"type": "object", "items": "not a schema"},
		map[string]any{"type": "object", "anyOf": []any{"not a schema"}},
		map[string]any{"type": "object", "additionalProperties": "not a schema"},
		map[string]any{"type": "object", "encrypted": "not a bool"},
		map[string]any{"type": "object", "minItems": "not a number"},
		map[string]any{"type": []any{"object", "not-a-type"}},
	} {
		if _, ok := SubsetSchema(value); ok {
			t.Fatalf("SubsetSchema(%#v) reported a supported document", value)
		}
	}
	for _, value := range []any{
		map[string]any{"type": []any{"string", "null"}},
		map[string]any{"additionalProperties": map[string]any{"type": "string"}},
		map[string]any{"minItems": 0},
	} {
		if _, ok := SubsetSchema(value); !ok {
			t.Fatalf("SubsetSchema(%#v) rejected a supported document", value)
		}
	}
}
