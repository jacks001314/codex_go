package config

import (
	"strings"
	"testing"
)

// Mirrors Rust #62358: `permissions.credentials` in requirements.toml is
// reserved for shared credential policy and must be omitted, while profiles
// named `credentials` stay valid in config.toml.
func TestPermissionsCredentialsReservedInRequirementsLikeRust(t *testing.T) {
	for _, field := range []string{
		"",
		"description = 'credential profile'",
		"extends = ':workspace'",
		"workspace_roots = {}",
		"filesystem = {}",
		"network = {}",
	} {
		document := "[permissions.credentials]\n" + field
		_, err := ParseRequirementsTOML([]byte(document))
		if err == nil {
			t.Fatalf("%q was accepted as shared credential requirements", document)
		}
		if !strings.Contains(err.Error(), permissionsCredentialsReservedProfileError) {
			t.Fatalf("%q error = %v, want the reserved-profile guidance", document, err)
		}
	}
}

// Mirrors Rust #62358: a named profile reserves the credentials slot with an
// empty table in both files, and any setting inside it is an unknown field.
func TestPermissionsCredentialsProfileTableLikeRust(t *testing.T) {
	requirements, err := ParseRequirementsTOML([]byte(`
[permissions.credential-access]
description = "credential access"
[permissions.credential-access.credentials]
`))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	profile, ok := requirements.Permissions["credential-access"].(map[string]any)
	if !ok {
		t.Fatalf("requirements permissions = %#v", requirements.Permissions)
	}
	if _, ok := profile["credentials"].(map[string]any); !ok {
		t.Fatalf("named profile credentials slot = %#v", profile["credentials"])
	}

	if err := ValidateConfigValues(map[string]any{
		"permissions": map[string]any{
			"credential-access": map[string]any{
				"description": "credential access",
				"credentials": map[string]any{},
			},
		},
	}); err != nil {
		t.Fatalf("ValidateConfigValues() error = %v", err)
	}
	err = ValidateConfigValues(map[string]any{
		"permissions": map[string]any{
			"credential-access": map[string]any{
				"credentials": map[string]any{"scope": "workspace"},
			},
		},
	})
	if err == nil || !strings.Contains(err.Error(), "there are no fields") {
		t.Fatalf("configured credentials settings error = %v, want the unknown-field rejection", err)
	}
}
