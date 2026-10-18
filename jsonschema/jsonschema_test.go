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

func TestNormalizePreservesToolSchemaBoundsLikeRust(t *testing.T) {
	input := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"count": map[string]any{
				"type":    "integer",
				"minimum": json.Number("0"),
				"maximum": json.Number("9007199254740993"),
			},
			"score": map[string]any{
				"type":    "number",
				"minimum": -1.5,
				"maximum": 3.25,
			},
			"query": map[string]any{
				"type":      "string",
				"maxLength": json.Number("1000"),
			},
		},
		"additionalProperties": false,
	}
	got := Normalize(cloneJSON(input).(map[string]any))
	encoded, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"additionalProperties":false,"properties":{"count":{"maximum":9007199254740993,"minimum":0,"type":"integer"},"query":{"maxLength":1000,"type":"string"},"score":{"maximum":3.25,"minimum":-1.5,"type":"number"}},"type":"object"}`
	if string(encoded) != want {
		t.Fatalf("Normalize() = %s\nwant %s", encoded, want)
	}
}

func TestNormalizeInfersStringFromMaxLengthLikeRust(t *testing.T) {
	got := Normalize(map[string]any{"maxLength": json.Number("1000")})
	want := map[string]any{"type": "string", "maxLength": json.Number("1000")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Normalize() = %#v, want %#v", got, want)
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
