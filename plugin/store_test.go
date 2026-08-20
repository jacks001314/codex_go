package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPluginStoreInstallAndVersionTracking(t *testing.T) {
	home := t.TempDir()
	store, err := NewPluginStore(home)
	if err != nil {
		t.Fatalf("NewPluginStore() error = %v", err)
	}

	pluginID, _ := NewPluginId("sample", "debug")
	if store.IsInstalled(pluginID) {
		t.Fatalf("plugin should not be installed yet")
	}
	if _, ok := store.ActivePluginVersion(pluginID); ok {
		t.Fatalf("no active version expected for uninstalled plugin")
	}

	// Create a source plugin
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, ".codex-plugin"), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	pluginJSON := `{"name":"sample","version":"1.0.0","description":"Test plugin"}`
	if err := os.WriteFile(filepath.Join(src, ".codex-plugin", "plugin.json"), []byte(pluginJSON), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	// Install with detected version
	result, err := store.Install(src, pluginID)
	if err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	if result.PluginID.Key() != "sample@debug" {
		t.Fatalf("installed id = %q, want sample@debug", result.PluginID.Key())
	}
	if result.PluginVersion != "1.0.0" {
		t.Fatalf("installed version = %q, want 1.0.0", result.PluginVersion)
	}
	if !store.IsInstalled(pluginID) {
		t.Fatalf("plugin should be installed")
	}
	version, ok := store.ActivePluginVersion(pluginID)
	if !ok || version != "1.0.0" {
		t.Fatalf("ActivePluginVersion = %q/%v, want 1.0.0/true", version, ok)
	}
}

func TestPluginStoreRejectsSymlinkedManifestLikeRust(t *testing.T) {
	home := t.TempDir()
	store, err := NewPluginStore(home)
	if err != nil {
		t.Fatalf("NewPluginStore() error = %v", err)
	}
	pluginID, _ := NewPluginId("sample", "debug")
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, ".codex-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "plugin.json")
	if err := os.WriteFile(target, []byte(`{"name":"sample","version":"1.0.0"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(src, ".codex-plugin", "plugin.json")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if _, err := store.Install(src, pluginID); err == nil || !strings.Contains(err.Error(), "regular file") {
		t.Fatalf("Install(symlinked manifest) error = %v", err)
	}
}

func TestPluginStoreInstallWithExplicitVersion(t *testing.T) {
	home := t.TempDir()
	store, _ := NewPluginStore(home)

	pluginID, _ := NewPluginId("sample", "debug")
	src := t.TempDir()
	if err := os.MkdirAll(filepath.Join(src, ".codex-plugin"), 0o755); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(src, ".codex-plugin", "plugin.json"), []byte(`{"name":"sample","version":"2.0.0"}`), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}

	result, err := store.InstallWithVersion(src, pluginID, "dev-build")
	if err != nil {
		t.Fatalf("InstallWithVersion() error = %v", err)
	}
	if result.PluginVersion != "dev-build" {
		t.Fatalf("installed version = %q, want dev-build", result.PluginVersion)
	}
	version, ok := store.ActivePluginVersion(pluginID)
	if !ok || version != "dev-build" {
		t.Fatalf("ActivePluginVersion = %q/%v, want dev-build/true", version, ok)
	}

	// Verify files exist
	installedRoot := store.PluginRoot(pluginID, "dev-build")
	if _, err := os.Stat(filepath.Join(installedRoot, ".codex-plugin", "plugin.json")); err != nil {
		t.Fatalf("installed plugin.json stat error = %v", err)
	}
}

func TestPluginStoreVersionSelectionPreference(t *testing.T) {
	home := t.TempDir()
	store, _ := NewPluginStore(home)
	pluginID, _ := NewPluginId("sample", "debug")

	// Install multiple versions
	for _, v := range []string{"0.1.0", "1.0.0", "2.0.0"} {
		src := t.TempDir()
		os.MkdirAll(filepath.Join(src, ".codex-plugin"), 0o755)
		os.WriteFile(filepath.Join(src, ".codex-plugin", "plugin.json"),
			[]byte(`{"name":"sample","version":"`+v+`"}`), 0o600)
		store.InstallWithVersion(src, pluginID, v)
	}

	// Highest semver should be selected
	version, ok := store.ActivePluginVersion(pluginID)
	if !ok || version != "2.0.0" {
		t.Fatalf("ActivePluginVersion = %q/%v, want 2.0.0/true", version, ok)
	}

	// Install "local" version — it should take priority
	src := t.TempDir()
	os.MkdirAll(filepath.Join(src, ".codex-plugin"), 0o755)
	os.WriteFile(filepath.Join(src, ".codex-plugin", "plugin.json"),
		[]byte(`{"name":"sample"}`), 0o600)
	store.InstallWithVersion(src, pluginID, DefaultPluginVersion)

	version, ok = store.ActivePluginVersion(pluginID)
	if !ok || version != DefaultPluginVersion {
		t.Fatalf("ActivePluginVersion = %q/%v, want %s/true", version, ok, DefaultPluginVersion)
	}
}

func TestPluginStoreRemoteMetadata(t *testing.T) {
	home := t.TempDir()
	store, _ := NewPluginStore(home)
	pluginID, _ := NewPluginId("sample", "debug")

	// Try reading from uninstalled plugin
	if _, err := store.RemotePluginID(pluginID); err == nil || !strings.Contains(err.Error(), "not installed") {
		t.Fatalf("RemotePluginID(uninstalled) error = %v, want 'not installed'", err)
	}

	// Install plugin without version
	src := t.TempDir()
	os.MkdirAll(filepath.Join(src, ".codex-plugin"), 0o755)
	os.WriteFile(filepath.Join(src, ".codex-plugin", "plugin.json"),
		[]byte(`{"name":"sample"}`), 0o600)
	store.InstallWithVersion(src, pluginID, DefaultPluginVersion)

	// No remote metadata yet
	remoteID, err := store.RemotePluginID(pluginID)
	if err != nil || remoteID != "" {
		t.Fatalf("RemotePluginID = %q/%v, want empty", remoteID, err)
	}

	// Write remote metadata
	if err := store.WriteRemotePluginID(pluginID, "remote-sample-123"); err != nil {
		t.Fatalf("WriteRemotePluginID() error = %v", err)
	}

	// Read back
	remoteID, err = store.RemotePluginID(pluginID)
	if err != nil || remoteID != "remote-sample-123" {
		t.Fatalf("RemotePluginID after write = %q/%v, want remote-sample-123/nil", remoteID, err)
	}

	// Verify metadata file format
	metaPath := filepath.Join(store.PluginBaseRoot(pluginID), RemotePluginInstallMetadataFile)
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("ReadFile(metadata) error = %v", err)
	}
	var meta remotePluginInstallMetadata
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("Unmarshal metadata error = %v", err)
	}
	if meta.SchemaVersion != RemotePluginInstallMetadataSchemaVersion || meta.RemotePluginID != "remote-sample-123" {
		t.Fatalf("metadata = %+v", meta)
	}
}

func TestPluginStoreUninstall(t *testing.T) {
	home := t.TempDir()
	store, _ := NewPluginStore(home)
	pluginID, _ := NewPluginId("sample", "debug")

	// Install
	src := t.TempDir()
	os.MkdirAll(filepath.Join(src, ".codex-plugin"), 0o755)
	os.WriteFile(filepath.Join(src, ".codex-plugin", "plugin.json"),
		[]byte(`{"name":"sample","version":"1.0.0"}`), 0o600)
	store.Install(src, pluginID)
	if !store.IsInstalled(pluginID) {
		t.Fatalf("plugin should be installed")
	}

	// Uninstall
	if err := store.Uninstall(pluginID); err != nil {
		t.Fatalf("Uninstall() error = %v", err)
	}
	if store.IsInstalled(pluginID) {
		t.Fatalf("plugin should not be installed after uninstall")
	}
	// Uninstall again should be a no-op
	if err := store.Uninstall(pluginID); err != nil {
		t.Fatalf("Uninstall() again error = %v", err)
	}
}

func TestPluginStoreNameMismatchValidation(t *testing.T) {
	home := t.TempDir()
	store, _ := NewPluginStore(home)
	pluginID, _ := NewPluginId("sample", "debug")

	src := t.TempDir()
	os.MkdirAll(filepath.Join(src, ".codex-plugin"), 0o755)
	// Name in manifest doesn't match pluginID
	os.WriteFile(filepath.Join(src, ".codex-plugin", "plugin.json"),
		[]byte(`{"name":"wrong-name","version":"1.0.0"}`), 0o600)

	_, err := store.Install(src, pluginID)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("Install() error = %v, want 'does not match'", err)
	}
}

func TestPluginStoreDataRoot(t *testing.T) {
	home := t.TempDir()
	store, _ := NewPluginStore(home)
	pluginID, _ := NewPluginId("sample", "debug")

	dataRoot := store.PluginDataRoot(pluginID)
	expected := filepath.Join(home, PluginsDataDir, "sample-debug")
	if dataRoot != expected {
		t.Fatalf("PluginDataRoot = %q, want %q", dataRoot, expected)
	}
}

func TestPluginVersionForSource(t *testing.T) {
	src := t.TempDir()
	os.MkdirAll(filepath.Join(src, ".codex-plugin"), 0o755)

	// No version field
	os.WriteFile(filepath.Join(src, ".codex-plugin", "plugin.json"),
		[]byte(`{"name":"sample"}`), 0o600)
	version, err := PluginVersionForSource(src)
	if err != nil || version != DefaultPluginVersion {
		t.Fatalf("PluginVersionForSource(no version) = %q/%v, want local/nil", version, err)
	}

	// With version
	os.WriteFile(filepath.Join(src, ".codex-plugin", "plugin.json"),
		[]byte(`{"name":"sample","version":"1.2.3"}`), 0o600)
	version, err = PluginVersionForSource(src)
	if err != nil || version != "1.2.3" {
		t.Fatalf("PluginVersionForSource(with version) = %q/%v, want 1.2.3/nil", version, err)
	}
}

func TestPluginStoreInstallsAgentPluginManifestLikeRust(t *testing.T) {
	home := t.TempDir()
	store, err := NewPluginStore(home)
	if err != nil {
		t.Fatal(err)
	}
	pluginID, err := NewPluginId("sample", "debug")
	if err != nil {
		t.Fatal(err)
	}
	source := t.TempDir()
	if err := os.WriteFile(filepath.Join(source, "plugin.json"), []byte(`{"$schema":"`+AgentPluginSchemaURI+`","name":"sample","version":"1.2.3"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	result, err := store.Install(source, pluginID)
	if err != nil {
		t.Fatalf("Install(agent plugin) error = %v", err)
	}
	if result.PluginVersion != "1.2.3" {
		t.Fatalf("version = %q", result.PluginVersion)
	}
	if _, err := os.Stat(filepath.Join(result.InstalledPath, "plugin.json")); err != nil {
		t.Fatalf("installed root manifest missing: %v", err)
	}
}

func TestValidatePluginVersionSegment(t *testing.T) {
	valid := []string{"1.0.0", "local", "v2", "dev-build", "1.2.3-alpha+001"}
	for _, v := range valid {
		if err := ValidatePluginVersionSegment(v); err != nil {
			t.Fatalf("ValidatePluginVersionSegment(%q) error = %v, want nil", v, err)
		}
	}

	invalid := map[string]string{
		"":    "must not be empty",
		".":   "path traversal",
		"..":  "path traversal",
		"v 1": "only ASCII",
		"a/b": "only ASCII",
	}
	for v, want := range invalid {
		err := ValidatePluginVersionSegment(v)
		if err == nil {
			t.Fatalf("ValidatePluginVersionSegment(%q) expected error containing %q", v, want)
		}
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("ValidatePluginVersionSegment(%q) error = %v, want containing %q", v, err, want)
		}
	}
}

func TestComparePluginVersions(t *testing.T) {
	tests := []struct {
		a, b     string
		expected int // -1 if a < b, 0 if equal, 1 if a > b
	}{
		{"local", "local", 0},
		{"1.0.0", "1.0.0", 0},
		{"2.0.0", "1.0.0", 1},
		{"0.1.0", "1.0.0", -1},
		{"local", "1.0.0", 1}, // lexicographic fallback
	}
	for _, tc := range tests {
		result := ComparePluginVersions(tc.a, tc.b)
		if tc.expected == 0 && result != 0 {
			t.Fatalf("ComparePluginVersions(%q, %q) = %d, want 0", tc.a, tc.b, result)
		} else if tc.expected > 0 && result <= 0 {
			t.Fatalf("ComparePluginVersions(%q, %q) = %d, want > 0", tc.a, tc.b, result)
		} else if tc.expected < 0 && result >= 0 {
			t.Fatalf("ComparePluginVersions(%q, %q) = %d, want < 0", tc.a, tc.b, result)
		}
	}
}
