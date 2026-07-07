package sandbox

import (
	"path/filepath"
	"testing"
)

func TestBuiltinApprovalPresets(t *testing.T) {
	presets := BuiltinApprovalPresets()
	if len(presets) != 3 {
		t.Fatalf("presets len = %d", len(presets))
	}
	if presets[0].ID != "read-only" || presets[0].Approval != ApprovalOnRequest {
		t.Fatalf("read-only preset = %#v", presets[0])
	}
	if presets[1].ID != "auto" || presets[1].PermissionProfile.SandboxPolicy.Kind != SandboxWorkspaceWrite {
		t.Fatalf("auto preset = %#v", presets[1])
	}
	if presets[2].ID != "full-access" || presets[2].Approval != ApprovalNever || !presets[2].PermissionProfile.Disabled {
		t.Fatalf("full-access preset = %#v", presets[2])
	}
}

func TestBuiltinPermissionProfileForActivePermissionProfile(t *testing.T) {
	profile, ok := BuiltinPermissionProfileForActivePermissionProfile(&ActivePermissionProfile{ID: BuiltInPermissionProfileWorkspace})
	if !ok {
		t.Fatal("expected builtin profile")
	}
	if profile.SandboxPolicy == nil || profile.SandboxPolicy.Kind != SandboxWorkspaceWrite {
		t.Fatalf("profile = %#v", profile)
	}
	if _, ok := BuiltinPermissionProfileForActivePermissionProfile(&ActivePermissionProfile{ID: BuiltInPermissionProfileWorkspace, Extends: "custom"}); ok {
		t.Fatal("profile with extends should not resolve as builtin")
	}
}

func TestPermissionProfileLegacySandboxPolicy(t *testing.T) {
	full := FullAccessPermissionProfile()
	if full.LegacySandboxPolicy().Kind != SandboxDangerFullAccess {
		t.Fatalf("full legacy policy = %#v", full.LegacySandboxPolicy())
	}
	readOnly := ReadOnlyPermissionProfile()
	if readOnly.LegacySandboxPolicy().Kind != SandboxReadOnly {
		t.Fatalf("read-only legacy policy = %#v", readOnly.LegacySandboxPolicy())
	}
}

func TestNormalizeAndValidateAdditionalPermissionsRequiresFeature(t *testing.T) {
	network := true
	_, err := NormalizeAndValidateAdditionalPermissions(
		false,
		ApprovalOnRequest,
		SandboxPermissionsWithAdditionalPermissions,
		&AdditionalPermissionProfile{Network: &network},
		false,
		t.TempDir(),
	)
	if err == nil {
		t.Fatal("NormalizeAndValidateAdditionalPermissions returned nil error, want failure")
	}
}

func TestNormalizeAndValidateAdditionalPermissionsRequiresOnRequestApproval(t *testing.T) {
	network := true
	_, err := NormalizeAndValidateAdditionalPermissions(
		true,
		ApprovalNever,
		SandboxPermissionsWithAdditionalPermissions,
		&AdditionalPermissionProfile{Network: &network},
		false,
		t.TempDir(),
	)
	if err == nil {
		t.Fatal("NormalizeAndValidateAdditionalPermissions returned nil error, want failure")
	}
}

func TestNormalizeAndValidateAdditionalPermissionsNormalizesPaths(t *testing.T) {
	cwd := t.TempDir()
	network := true
	normalized, err := NormalizeAndValidateAdditionalPermissions(
		true,
		ApprovalOnRequest,
		SandboxPermissionsWithAdditionalPermissions,
		&AdditionalPermissionProfile{
			Network:    &network,
			FileSystem: []string{"src", "src", filepath.Join(cwd, "docs")},
		},
		false,
		cwd,
	)
	if err != nil {
		t.Fatalf("NormalizeAndValidateAdditionalPermissions returned error: %v", err)
	}
	if normalized.Network == nil || !*normalized.Network {
		t.Fatalf("network = %#v", normalized.Network)
	}
	wantSrc := cleanAbs(filepath.Join(cwd, "src"))
	wantDocs := cleanAbs(filepath.Join(cwd, "docs"))
	if len(normalized.FileSystem) != 2 || normalized.FileSystem[0] != wantSrc || normalized.FileSystem[1] != wantDocs {
		t.Fatalf("file system permissions = %#v", normalized.FileSystem)
	}
}

func TestNormalizeAndValidateAdditionalPermissionsRejectsDetachedProfile(t *testing.T) {
	network := true
	_, err := NormalizeAndValidateAdditionalPermissions(
		true,
		ApprovalOnRequest,
		SandboxPermissionsUseDefault,
		&AdditionalPermissionProfile{Network: &network},
		false,
		t.TempDir(),
	)
	if err == nil {
		t.Fatal("NormalizeAndValidateAdditionalPermissions returned nil error, want failure")
	}
}

func TestMergePermissionProfiles(t *testing.T) {
	network := true
	left := &AdditionalPermissionProfile{
		FileSystem: []string{"src"},
	}
	right := &AdditionalPermissionProfile{
		Network:    &network,
		FileSystem: []string{"src", "docs"},
	}
	merged := MergePermissionProfiles(left, right)
	if merged == nil {
		t.Fatal("merged is nil")
	}
	if merged.Network == nil || !*merged.Network {
		t.Fatalf("network = %#v", merged.Network)
	}
	if len(merged.FileSystem) != 2 {
		t.Fatalf("file system permissions = %#v", merged.FileSystem)
	}
}
