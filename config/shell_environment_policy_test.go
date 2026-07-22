package config

import (
	"reflect"
	"strings"
	"testing"
)

func TestShellEnvironmentPolicyFiltersValidateAndMerge(t *testing.T) {
	if err := ValidateShellEnvironmentPolicy(map[string]any{"filters": map[string]any{"PATH": "include"}, "exclude": []any{"TOKEN"}}); err == nil {
		t.Fatal("expected mixed representation rejection")
	}
	base := map[string]any{"filters": map[string]any{"Path": "include", "TOKEN": "exclude"}}
	merged := mergeShellEnvironmentPolicy(base, map[string]any{"filters": map[string]any{"path": "exclude", "HOME": "include"}})
	include, exclude, err := ShellEnvironmentFilterPatterns(merged)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(include, []string{"home"}) || !reflect.DeepEqual(exclude, []string{"path", "token"}) {
		t.Fatalf("include=%v exclude=%v", include, exclude)
	}
	legacy := mergeShellEnvironmentPolicy(merged, map[string]any{"include_only": []any{"PATH"}})
	if _, ok := legacy["filters"]; ok {
		t.Fatalf("filters retained: %#v", legacy)
	}
}
func TestStrictConfigRejectsMalformedShellEnvironmentFilters(t *testing.T) {
	err := validateKnownTopLevelConfigFields(map[string]any{"shell_environment_policy": map[string]any{"filters": map[string]any{"PATH": "maybe"}}})
	if err == nil || !strings.Contains(err.Error(), "include") {
		t.Fatalf("err=%v", err)
	}
}
