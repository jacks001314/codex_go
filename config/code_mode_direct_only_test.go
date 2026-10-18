package config

import (
	"reflect"
	"testing"
)

// Rust codes: features.code_mode.direct_only_tool_namespaces preserves the
// configured namespace order and trims entries.
func TestCodeModeDirectOnlyToolNamespacesLikeRust(t *testing.T) {
	cfg := &Config{Values: map[string]any{
		"features": map[string]any{
			"code_mode": map[string]any{
				"direct_only_tool_namespaces": []any{" mcp__environment ", "", "functions"},
			},
		},
	}}
	if got, want := cfg.CodeModeDirectOnlyToolNamespaces(), []string{"mcp__environment", "functions"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("CodeModeDirectOnlyToolNamespaces() = %#v, want %#v", got, want)
	}
	if got := (&Config{}).CodeModeDirectOnlyToolNamespaces(); got != nil {
		t.Fatalf("absent config = %#v, want nil", got)
	}
}
