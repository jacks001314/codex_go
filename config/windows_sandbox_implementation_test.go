package config

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestWindowsSandboxImplementationParsingLikeRust mirrors Rust #45737: the
// allowed-implementation type carries elevated, unelevated, and the native mxc
// backend, while the legacy setup-mode spellings and anything unknown are
// rejected instead of being stored verbatim.
func TestWindowsSandboxImplementationParsingLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		value string
		want  WindowsSandboxImplementation
	}{
		{"elevated", WindowsSandboxImplementationElevated},
		{"unelevated", WindowsSandboxImplementationUnelevated},
		{"mxc", WindowsSandboxImplementationMxc},
		{"  MXC  ", WindowsSandboxImplementationMxc},
	} {
		got, err := ParseWindowsSandboxImplementation(testCase.value)
		if err != nil || got != testCase.want {
			t.Fatalf("ParseWindowsSandboxImplementation(%q) = %q, %v; want %q", testCase.value, got, err, testCase.want)
		}
	}
	for _, invalid := range []string{"", "disabled", "default", "restricted-token", "bogus"} {
		if got, err := ParseWindowsSandboxImplementation(invalid); err == nil {
			t.Fatalf("ParseWindowsSandboxImplementation(%q) = %q, want an error", invalid, got)
		}
	}
}

// TestParseRequirementsTOMLWindowsImplementationValidationLikeRust pins that a
// requirements file can only name real backends: Rust's TOML enum has no
// disabled/default/mxc spellings, so unknown entries are a parse error rather
// than a silently stored value.
func TestParseRequirementsTOMLWindowsImplementationValidationLikeRust(t *testing.T) {
	requirements, err := ParseRequirementsTOML([]byte(`
[windows]
allowed_sandbox_implementations = ["unelevated", "elevated"]
`))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	if requirements == nil || len(requirements.AllowedWindowsSandboxImplementations) != 2 ||
		requirements.AllowedWindowsSandboxImplementations[0] != WindowsSandboxImplementationUnelevated ||
		requirements.AllowedWindowsSandboxImplementations[1] != WindowsSandboxImplementationElevated {
		t.Fatalf("allowed implementations = %#v", requirements)
	}
	for _, invalid := range []string{"default", "disabled", "bogus"} {
		if _, err := ParseRequirementsTOML([]byte("[windows]\nallowed_sandbox_implementations = [\"" + invalid + "\"]\n")); err == nil {
			t.Fatalf("requirements entry %q must be rejected", invalid)
		}
	}
}

// TestConfigRequirementsProjectsWindowsSandboxImplementationsLikeRust covers the
// app-server projection: the field carries implementation values (including mxc)
// and never the legacy setup-mode spellings.
func TestConfigRequirementsProjectsWindowsSandboxImplementationsLikeRust(t *testing.T) {
	requirements := &ConfigRequirements{AllowedWindowsSandboxImplementations: []WindowsSandboxImplementation{
		WindowsSandboxImplementationElevated,
		WindowsSandboxImplementationMxc,
	}}
	data, err := json.Marshal(requirements)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(data), `"allowedWindowsSandboxImplementations":["elevated","mxc"]`) {
		t.Fatalf("projected requirements = %s", data)
	}
}
