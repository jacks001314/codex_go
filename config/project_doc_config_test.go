package config

import (
	"reflect"
	"testing"
)

// TestProjectDocFallbackFilenamesLikeRust covers the config accessor for
// Rust's project_doc_fallback_filenames.
func TestProjectDocFallbackFilenamesLikeRust(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]any
		want   []string
	}{
		{name: "unset", values: map[string]any{}, want: nil},
		{name: "nil values", values: nil, want: nil},
		{name: "list", values: map[string]any{"project_doc_fallback_filenames": []any{"CLAUDE.md", "GEMINI.md"}}, want: []string{"CLAUDE.md", "GEMINI.md"}},
		{name: "string values", values: map[string]any{"project_doc_fallback_filenames": []string{"CLAUDE.md"}}, want: []string{"CLAUDE.md"}},
		{name: "single string", values: map[string]any{"project_doc_fallback_filenames": "CLAUDE.md"}, want: []string{"CLAUDE.md"}},
		{name: "blank entries dropped", values: map[string]any{"project_doc_fallback_filenames": []any{"", "  ", "CLAUDE.md"}}, want: []string{"CLAUDE.md"}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := &Config{Values: testCase.values}
			if got := cfg.ProjectDocFallbackFilenames(); !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("ProjectDocFallbackFilenames() = %#v, want %#v", got, testCase.want)
			}
		})
	}
	var nilConfig *Config
	if got := nilConfig.ProjectDocFallbackFilenames(); got != nil {
		t.Fatalf("nil config = %#v, want nil", got)
	}
}

// TestProjectRootMarkersLikeRust covers the config accessor for Rust's
// project_root_markers (an empty result lets the discovery helper default to
// [".git"]).
func TestProjectRootMarkersLikeRust(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]any
		want   []string
	}{
		{name: "unset", values: map[string]any{}, want: nil},
		{name: "list", values: map[string]any{"project_root_markers": []any{"PROJECT_MARKER", ".git"}}, want: []string{"PROJECT_MARKER", ".git"}},
		{name: "single string", values: map[string]any{"project_root_markers": "PROJECT_MARKER"}, want: []string{"PROJECT_MARKER"}},
		{name: "blank entries dropped", values: map[string]any{"project_root_markers": []any{"", "  "}}, want: nil},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := &Config{Values: testCase.values}
			if got := cfg.ProjectRootMarkers(); !reflect.DeepEqual(got, testCase.want) {
				t.Fatalf("ProjectRootMarkers() = %#v, want %#v", got, testCase.want)
			}
		})
	}
}
