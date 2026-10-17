package tool

import (
	"reflect"
	"testing"
)

// request_user_input_async honors the model catalog's
// `send_user_message_async.parameters` override (Rust #47773): a valid object
// schema replaces the bundled one, everything else falls back to the bundled
// schema while the catalog description is preserved.
func TestRequestUserInputAsyncSpecUsesCatalogParametersLikeRust(t *testing.T) {
	override := `{"title":"ignored","$schema":"https://example.test/schema","type":"object","properties":{"question":{"type":"string","format":"date","description":"Catalog question."}},"required":["question"],"additionalProperties":false}`
	handler := &RequestUserInputAsyncHandler{Parameters: &override}
	spec := handler.Spec()
	want := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"question": map[string]any{
				"type":        "string",
				"description": "Catalog question.",
			},
		},
		"required":             []any{"question"},
		"additionalProperties": false,
	}
	if !reflect.DeepEqual(spec.InputSchema, want) {
		t.Fatalf("catalog schema = %#v, want %#v", spec.InputSchema, want)
	}
	if _, ok := spec.InputSchema["questions"]; ok {
		t.Fatalf("catalog schema kept bundled questions property: %#v", spec.InputSchema)
	}
	if _, ok := spec.InputSchema["title"]; ok {
		t.Fatalf("subset normalization kept unsupported key: %#v", spec.InputSchema)
	}
}

func TestRequestUserInputAsyncSpecFallsBackOnInvalidCatalogParametersLikeRust(t *testing.T) {
	catalogDescription := "Catalog async question description."
	cases := map[string]string{
		"empty":                  "",
		"whitespace":             "   ",
		"not json":               "not json",
		"non-object root":        `[]`,
		"string root":            `"object"`,
		"missing type":           `{"properties":{}}`,
		"wrong type":             `{"type":"array","items":{"type":"string"}}`,
		"unsupported structures": `{"type":"object","properties":"nope"}`,
		"unsupported required":   `{"type":"object","required":"questions"}`,
	}
	bundled := requestUserInputAsyncBundledSchema()
	for name, raw := range cases {
		raw := raw
		t.Run(name, func(t *testing.T) {
			handler := &RequestUserInputAsyncHandler{Description: &catalogDescription, Parameters: &raw}
			spec := handler.Spec()
			if spec.Description != catalogDescription {
				t.Fatalf("description = %q, want catalog description preserved", spec.Description)
			}
			if !reflect.DeepEqual(spec.InputSchema, bundled) {
				t.Fatalf("schema = %#v, want bundled fallback %#v", spec.InputSchema, bundled)
			}
		})
	}
}

func TestCatalogParametersParseRejectsUnsupportedSubsetLikeRust(t *testing.T) {
	if _, err := catalogParametersParse(`{"type":"object","properties":{"q":{"type":"strin"}}}`); err == nil {
		t.Fatal("expected error for an unknown primitive type")
	} else if err.Error() != "schema uses unsupported JSON Schema structures" {
		t.Fatalf("error = %v", err)
	}
	if _, err := catalogParametersParse(`{"type":"object","required":[1]}`); err == nil {
		t.Fatal("expected error for a non-string required entry")
	}
	if schema, err := catalogParametersParse(`{"type":"object","properties":{},"required":[]}`); err != nil {
		t.Fatalf("valid schema error = %v", err)
	} else if schema["type"] != "object" {
		t.Fatalf("schema = %#v", schema)
	}
}
