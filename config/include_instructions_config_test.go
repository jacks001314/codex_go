package config

import "testing"

// TestIncludeCollaborationModeInstructionsLikeRust covers Rust's
// `include_collaboration_mode_instructions` (default true).
func TestIncludeCollaborationModeInstructionsLikeRust(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]any
		want   bool
	}{
		{name: "default", values: map[string]any{}, want: true},
		{name: "nil values", values: nil, want: true},
		{name: "explicit true", values: map[string]any{"include_collaboration_mode_instructions": true}, want: true},
		{name: "explicit false", values: map[string]any{"include_collaboration_mode_instructions": false}, want: false},
		{name: "wrong type falls back to true", values: map[string]any{"include_collaboration_mode_instructions": "no"}, want: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := &Config{Values: testCase.values}
			if got := cfg.IncludeCollaborationModeInstructions(); got != testCase.want {
				t.Fatalf("IncludeCollaborationModeInstructions() = %v, want %v", got, testCase.want)
			}
		})
	}
	var nilConfig *Config
	if !nilConfig.IncludeCollaborationModeInstructions() {
		t.Fatal("nil config must default to including the section")
	}
}

// TestIncludePermissionsInstructionsLikeRust covers Rust's
// `include_permissions_instructions` (default true).
func TestIncludePermissionsInstructionsLikeRust(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]any
		want   bool
	}{
		{name: "default", values: map[string]any{}, want: true},
		{name: "nil values", values: nil, want: true},
		{name: "explicit true", values: map[string]any{"include_permissions_instructions": true}, want: true},
		{name: "explicit false", values: map[string]any{"include_permissions_instructions": false}, want: false},
		{name: "wrong type falls back to true", values: map[string]any{"include_permissions_instructions": "no"}, want: true},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			cfg := &Config{Values: testCase.values}
			if got := cfg.IncludePermissionsInstructions(); got != testCase.want {
				t.Fatalf("IncludePermissionsInstructions() = %v, want %v", got, testCase.want)
			}
		})
	}
	var nilConfig *Config
	if !nilConfig.IncludePermissionsInstructions() {
		t.Fatal("nil config must default to including the section")
	}
}
