package tool

import (
	"errors"
	"log/slog"
)

// CatalogToolOverride carries one model-catalog tool entry. Pointer fields keep
// explicit empty descriptions and schemas distinguishable from absent ones.
type CatalogToolOverride struct {
	Description *string
	Parameters  *string
}

// ApplyCatalogToolOverride mirrors Rust tools::catalog_tool_message::
// with_catalog_tool_message (#56313). Description and parameter overrides apply
// independently; an invalid schema, or one that drops a bundled encrypted
// parameter, keeps the bundled schema. Tool identity and transport fields are
// never changed.
func ApplyCatalogToolOverride(spec Spec, override *CatalogToolOverride) Spec {
	if override == nil {
		return spec
	}
	if override.Description != nil {
		spec.Description = *override.Description
	}
	if override.Parameters == nil {
		return spec
	}
	schema, err := catalogParametersParse(*override.Parameters)
	if err == nil {
		schema, err = preserveEncryptedCatalogProperties(spec.InputSchema, schema)
	}
	if err != nil {
		slog.Warn(
			"Invalid catalog tool parameters; using bundled parameters",
			"tool", spec.Name.Key(),
			"reason", err,
		)
		return spec
	}
	spec.InputSchema = schema
	return spec
}

// preserveEncryptedCatalogProperties restores harness-owned encryption markers
// on a catalog parameter override. Rust treats a schema that omits a bundled
// encrypted property as invalid so encryption cannot be silently downgraded.
func preserveEncryptedCatalogProperties(bundled, override map[string]any) (map[string]any, error) {
	bundledProperties, _ := bundled["properties"].(map[string]any)
	if len(bundledProperties) == 0 {
		return override, nil
	}
	overrideProperties, _ := override["properties"].(map[string]any)
	for name, raw := range bundledProperties {
		bundledProperty, _ := raw.(map[string]any)
		if encrypted, _ := bundledProperty["encrypted"].(bool); !encrypted {
			continue
		}
		property, _ := overrideProperties[name].(map[string]any)
		if property == nil {
			return nil, errors.New("schema omits an encrypted parameter")
		}
		property["encrypted"] = true
	}
	return override, nil
}
