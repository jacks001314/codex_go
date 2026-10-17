package tool

import (
	"encoding/json"
	"errors"

	"codex_go/jsonschema"
)

// ParseCatalogParameters is the exported form of catalogParametersParse for
// other packages that own catalog overrides (Rust's
// `crate::tools::catalog_parameters::parse`).
func ParseCatalogParameters(raw string) (map[string]any, error) {
	return catalogParametersParse(raw)
}

// catalogParametersParse mirrors Rust core/src/tools/catalog_parameters.rs
// (#47773): catalog parameter overrides are accepted only when they decode as
// JSON, declare an object root, and use the harness-supported JSON Schema
// subset. The returned schema is the subset-normalized form serde would
// produce, so unknown keys are dropped exactly as the Rust round trip drops
// them. Callers fall back to their bundled schema on any error.
func catalogParametersParse(raw string) (map[string]any, error) {
	var decoded any
	if err := json.Unmarshal([]byte(raw), &decoded); err != nil {
		return nil, errors.New("schema is not valid JSON")
	}
	object, ok := decoded.(map[string]any)
	if !ok {
		return nil, errors.New("schema must declare an object type")
	}
	if kind, _ := object["type"].(string); kind != "object" {
		return nil, errors.New("schema must declare an object type")
	}
	schema, ok := jsonschema.SubsetSchema(object)
	if !ok {
		return nil, errors.New("schema uses unsupported JSON Schema structures")
	}
	return schema, nil
}
