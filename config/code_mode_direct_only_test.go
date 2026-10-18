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

// Rust also omits exact namespaces from the nested Code Mode surface while
// keeping the tools registered for direct model access.
func TestCodeModeExcludedToolNamespacesLikeRust(t *testing.T) {
	cfg := &Config{Values: map[string]any{
		"features": map[string]any{
			"code_mode": map[string]any{
				"excluded_tool_namespaces": []any{" mcp__environment ", "", "functions"},
			},
		},
	}}
	if got, want := cfg.CodeModeExcludedToolNamespaces(), []string{"mcp__environment", "functions"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("CodeModeExcludedToolNamespaces() = %#v, want %#v", got, want)
	}
	if got := (&Config{}).CodeModeExcludedToolNamespaces(); got != nil {
		t.Fatalf("absent config = %#v, want nil", got)
	}
}
