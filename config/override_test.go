package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseOverrides(t *testing.T) {
	overrides, err := ParseOverrides([]string{
		`model="gpt-5.5"`,
		"features.unified_exec=true",
		"sandbox_workspace_write.writable_roots=[\"/tmp/a\", \"/tmp/b\"]",
		"use_legacy_landlock=true",
		"provider={name=\"openai\", retries=3}",
	})
	if err != nil {
		t.Fatalf("ParseOverrides returned error: %v", err)
	}
	if overrides[0].Path != "model" || overrides[0].Value != "gpt-5.5" {
		t.Fatalf("override[0] = %#v", overrides[0])
	}
	if overrides[1].Value != true {
		t.Fatalf("override[1].Value = %#v", overrides[1].Value)
	}
	if overrides[3].Path != "features.use_legacy_landlock" {
		t.Fatalf("override[3].Path = %q", overrides[3].Path)
	}
	table, ok := overrides[4].Value.(map[string]any)
	if !ok {
		t.Fatalf("override[4].Value type = %T", overrides[4].Value)
	}
	if table["name"] != "openai" || table["retries"] != int64(3) {
		t.Fatalf("inline table = %#v", table)
	}
}

func TestApplyOverrides(t *testing.T) {
	root := map[string]any{}
	overrides, err := ParseOverrides([]string{
		"features.unified_exec=false",
		"model=gpt-5.5",
	})
	if err != nil {
		t.Fatalf("ParseOverrides returned error: %v", err)
	}
	ApplyOverrides(root, overrides)
	want := map[string]any{
		"features": map[string]any{
			"unified_exec": false,
		},
		"model": "gpt-5.5",
	}
	if !reflect.DeepEqual(root, want) {
		t.Fatalf("root = %#v, want %#v", root, want)
	}
}

func TestApplyOverridesMultiAgentV2PreservesBooleanAndNestedLikeRust(t *testing.T) {
	// Mirrors Rust merge_tests.rs
	// multi_agent_v2_cli_overrides_preserve_boolean_and_nested_configuration:
	// CLI overrides in either ordering preserve the multi-agent toggle and
	// nested options under both features and profiles.<name>.features.
	for _, featurePath := range []string{"features", "profiles.work.features"} {
		enabled := featurePath + ".multi_agent_v2=true"
		instructions := featurePath + ".multi_agent_v2.subagent_usage_hint_text=Delegate carefully."
		table := featurePath + `.multi_agent_v2={subagent_usage_hint_text="Delegate carefully."}`
		for _, overrides := range [][]string{
			{enabled, instructions},
			{instructions, enabled},
			{enabled, table},
			{table, enabled},
		} {
			parsed, err := ParseOverrides(overrides)
			if err != nil {
				t.Fatalf("%v ParseOverrides error: %v", overrides, err)
			}
			root := map[string]any{}
			ApplyOverrides(root, parsed)
			featureTable := readConfigNested(root, append(strings.Split(featurePath, "."), "multi_agent_v2")).(map[string]any)
			if featureTable["enabled"] != true || featureTable["subagent_usage_hint_text"] != "Delegate carefully." {
				t.Fatalf("%s overrides %v -> multi_agent_v2 = %#v", featurePath, overrides, featureTable)
			}
		}
	}
}

func TestApplyOverridesMultiAgentV2ExcludesOpaqueDesktopPathsLikeRust(t *testing.T) {
	// Mirrors Rust merge_tests.rs
	// multi_agent_v2_cli_compatibility_excludes_opaque_desktop_paths: opaque
	// desktop paths keep ordinary replacement semantics.
	cases := []struct {
		overrides []string
		want      map[string]any
	}{
		{
			overrides: []string{"desktop.features.multi_agent_v2=true", `desktop.features.multi_agent_v2={custom=true}`},
			want:      map[string]any{"desktop": map[string]any{"features": map[string]any{"multi_agent_v2": map[string]any{"custom": true}}}},
		},
		{
			overrides: []string{`desktop.features.multi_agent_v2={custom=true}`, "desktop.features.multi_agent_v2=true"},
			want:      map[string]any{"desktop": map[string]any{"features": map[string]any{"multi_agent_v2": true}}},
		},
	}
	for _, tc := range cases {
		parsed, err := ParseOverrides(tc.overrides)
		if err != nil {
			t.Fatalf("%v ParseOverrides error: %v", tc.overrides, err)
		}
		root := map[string]any{}
		ApplyOverrides(root, parsed)
		if !reflect.DeepEqual(root, tc.want) {
			t.Fatalf("overrides %v -> %#v, want %#v", tc.overrides, root, tc.want)
		}
	}
}

func TestParseOverrideRejectsMissingEquals(t *testing.T) {
	if _, err := ParseOverrides([]string{"model"}); err == nil {
		t.Fatal("ParseOverrides returned nil error, want failure")
	}
}
