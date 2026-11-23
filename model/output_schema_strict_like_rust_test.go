package model

import "testing"

// Mirrors Rust #63320: `output_schema_strict` reaches text.format, whose
// default stays strict for ordinary turns.
func TestResponsesTextParamHonorsOutputSchemaStrictLikeRust(t *testing.T) {
	schema := map[string]any{"type": "object"}
	strict := false
	param := responsesTextParamForRequest(schema, "", nil, &strict)
	if param == nil || param.Format == nil {
		t.Fatalf("non-strict text param = %#v", param)
	}
	if param.Format.Strict || param.Format.Type != "json_schema" || param.Format.Name != "codex_output_schema" {
		t.Fatalf("non-strict format = %#v", param.Format)
	}

	def := responsesTextParamForRequest(schema, "", nil, nil)
	if def == nil || def.Format == nil || !def.Format.Strict {
		t.Fatalf("default format = %#v, want strict", def)
	}
}
