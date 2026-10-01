package config

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/sandbox"
)

// TestEffectivePermissionProfileForCWDLikeRust pins the profile the startup
// sandbox warning inspects: the config's default permission profile.
func TestEffectivePermissionProfileForCWDLikeRust(t *testing.T) {
	home := t.TempDir()
	service := NewConfigService(home)
	layers, layerErr := service.readLayersForCWD(home, service.currentProfile())
	values, _ := mergeConfigLayers(layers)
	resolution, resolveErr := (&Config{Values: values, Requirements: service.LocalRequirements()}).ResolveSandboxPermissionProfile("", home)
	t.Logf("layers=%d layerErr=%v resolveErr=%v resolution=%+v", len(layers), layerErr, resolveErr, resolution)
	profile := service.EffectivePermissionProfileForCWD(home)
	if profile == nil {
		t.Fatal("EffectivePermissionProfileForCWD() = nil for a default config")
	}
	if policy := profile.LegacySandboxPolicy(); policy == nil || policy.Kind != sandbox.SandboxReadOnly {
		t.Fatalf("default profile kind = %+v, want read-only", policy)
	}

	if err := os.WriteFile(
		filepath.Join(home, "config.toml"),
		[]byte("default_permissions = \":danger-full-access\"\n"),
		0o600,
	); err != nil {
		t.Fatalf("write config.toml: %v", err)
	}
	profile = NewConfigService(home).EffectivePermissionProfileForCWD(home)
	if profile == nil {
		t.Fatal("EffectivePermissionProfileForCWD() = nil for a full-access default")
	}
	if policy := profile.LegacySandboxPolicy(); policy == nil || policy.Kind != sandbox.SandboxDangerFullAccess {
		t.Fatalf("configured profile kind = %+v, want danger-full-access", policy)
	}
}
