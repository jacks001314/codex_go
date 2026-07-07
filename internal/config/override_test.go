package config

import (
	"reflect"
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

func TestParseOverrideRejectsMissingEquals(t *testing.T) {
	if _, err := ParseOverrides([]string{"model"}); err == nil {
		t.Fatal("ParseOverrides returned nil error, want failure")
	}
}
