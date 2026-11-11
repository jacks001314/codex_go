package config

import (
	"path/filepath"
	"reflect"
	"testing"

	"codex_go/sandbox"
)

func permissionTestLayers(entries ...Layer) ([]Layer, map[string]any) {
	values := map[string]any{}
	for index := range entries {
		layerValues, _ := entries[index].Config.(map[string]any)
		mergeConfigMaps(values, cloneMap(layerValues))
	}
	return entries, values
}

func userPermissionLayer(values map[string]any) Layer {
	return Layer{Name: LayerSource{Type: LayerSourceUser, File: "config.toml"}, Config: values}
}

func managedLegacyPermissionLayer(values map[string]any) Layer {
	return Layer{Name: LayerSource{Type: LayerSourceLegacyManagedConfigFromMDM}, Config: values}
}

func sessionPermissionLayer(values map[string]any) Layer {
	return Layer{Name: LayerSource{Type: LayerSourceSessionFlags}, Config: values}
}

// TestPermissionConfigPreservesMixedSelectorPrecedenceLikeRust mirrors Rust
// #60702: the last layer that sets either selector decides, and an explicit
// session profile selection keeps the profile exception.
func TestPermissionConfigPreservesMixedSelectorPrecedenceLikeRust(t *testing.T) {
	for _, test := range []struct {
		name     string
		user     map[string]any
		session  map[string]any
		managed  map[string]any
		wantMode sandbox.SandboxMode
		wantID   string
	}{
		{
			name:     "managed legacy overrides user profile",
			user:     map[string]any{"default_permissions": ":workspace"},
			managed:  map[string]any{"sandbox_mode": "read-only"},
			wantMode: sandbox.SandboxReadOnly,
		},
		{
			name:    "managed profile overrides user legacy",
			user:    map[string]any{"sandbox_mode": "read-only"},
			managed: map[string]any{"default_permissions": ":workspace"},
			wantID:  ":workspace",
		},
		{
			name:    "session profile keeps the exception",
			session: map[string]any{"default_permissions": ":workspace"},
			managed: map[string]any{"sandbox_mode": "read-only"},
			wantID:  ":workspace",
		},
		{
			name:   "same layer profile wins",
			user:   map[string]any{"sandbox_mode": "read-only", "default_permissions": ":workspace"},
			wantID: ":workspace",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			layers, values := permissionTestLayers(
				userPermissionLayer(test.user),
				sessionPermissionLayer(test.session),
				managedLegacyPermissionLayer(test.managed),
			)
			selection := ResolvePermissionConfig(layers, values, nil)
			if selection == nil {
				t.Fatal("ResolvePermissionConfig() = nil")
			}
			if selection.Legacy != (test.wantID == "") || selection.Mode != test.wantMode || selection.ID != test.wantID {
				t.Fatalf("selection = %+v, want mode %q id %q", selection, test.wantMode, test.wantID)
			}
			override := sandbox.SandboxReadOnly
			overrideSelection := ResolvePermissionConfig(layers, values, &override)
			if overrideSelection == nil || !overrideSelection.Legacy || overrideSelection.Mode != sandbox.SandboxReadOnly {
				t.Fatalf("override selection = %+v", overrideSelection)
			}
		})
	}
}

// TestPermissionConfigMergesLegacyFieldsWithoutChangingSourcesLikeRust
// mirrors Rust #60702: sandbox_workspace_write settings merge across layers
// and the translation keeps them, while the source layer documents keep their
// original shape.
func TestPermissionConfigMergesLegacyFieldsWithoutChangingSourcesLikeRust(t *testing.T) {
	root := filepath.Join(t.TempDir(), "root")
	cloud := Layer{
		Name: LayerSource{Type: LayerSourceEnterpriseManaged, ID: "policy", Name: "policy"},
		Config: map[string]any{
			"default_permissions": ":read-only",
			"sandbox_workspace_write": map[string]any{
				"writable_roots":         []any{root},
				"exclude_tmpdir_env_var": true,
			},
		},
	}
	user := userPermissionLayer(map[string]any{
		"sandbox_workspace_write": map[string]any{"network_access": true},
	})
	managed := managedLegacyPermissionLayer(map[string]any{"sandbox_mode": "workspace-write"})
	layers, values := permissionTestLayers(cloud, user, managed)
	before := make([]any, len(layers))
	for index := range layers {
		before[index] = cloneMap(layers[index].Config.(map[string]any))
	}

	selection := ResolvePermissionConfig(layers, values, nil)
	if selection == nil || !selection.Legacy || selection.Mode != sandbox.SandboxWorkspaceWrite {
		t.Fatalf("selection = %+v", selection)
	}
	cfg := &Config{Values: values, featureLayers: layers}
	resolved, err := cfg.ResolveSandboxPermissionProfile("", t.TempDir())
	if err != nil {
		t.Fatalf("ResolveSandboxPermissionProfile() error = %v", err)
	}
	if resolved == nil || resolved.Profile == nil || resolved.ID != string(sandbox.SandboxWorkspaceWrite) {
		t.Fatalf("resolved = %+v", resolved)
	}
	if !resolved.Profile.AllowsNetwork() {
		t.Fatal("merged network_access was not applied")
	}
	roots := resolved.Profile.SandboxPolicy.GetWritableRootsWithCWD(t.TempDir())
	found := false
	for _, writable := range roots {
		if filepath.Clean(writable.Root) == filepath.Clean(root) {
			found = true
		}
	}
	if !found {
		t.Fatalf("merged writable root %q missing from %+v", root, roots)
	}
	for index := range layers {
		if got := layers[index].Config.(map[string]any); !reflect.DeepEqual(got, before[index]) {
			t.Fatalf("layer %d mutated: %#v, want %#v", index, got, before[index])
		}
	}
}

// TestResolveSandboxPermissionProfileUsesLayerSelectorPrecedenceLikeRust
// checks the end-to-end resolution: a higher legacy selector must beat a lower
// named profile even though merged default_permissions is still present.
func TestResolveSandboxPermissionProfileUsesLayerSelectorPrecedenceLikeRust(t *testing.T) {
	layers, values := permissionTestLayers(
		userPermissionLayer(map[string]any{"default_permissions": ":workspace"}),
		managedLegacyPermissionLayer(map[string]any{"sandbox_mode": "read-only"}),
	)
	cfg := &Config{Values: values, featureLayers: layers}
	resolved, err := cfg.ResolveSandboxPermissionProfile("", t.TempDir())
	if err != nil {
		t.Fatalf("ResolveSandboxPermissionProfile() error = %v", err)
	}
	if resolved == nil || resolved.ID != string(sandbox.SandboxReadOnly) {
		t.Fatalf("resolved = %+v, want read-only", resolved)
	}
}
