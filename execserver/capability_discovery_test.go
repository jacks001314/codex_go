package execserver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDiscoverCapabilitiesFindsPluginAndSkillManifests(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, ".codex-plugin")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "plugin.json"), []byte(`{"name":"sample"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	skillDir := filepath.Join(root, "skills", "sample")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("# Sample"), 0o600); err != nil {
		t.Fatal(err)
	}
	response, err := discoverCapabilities(&CapabilityDiscoveryParams{Roots: []CapabilityDiscoveryRoot{{ID: "root", Path: fileURI(root)}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(response.Errors) != 0 || len(response.Manifests) != 2 || response.Manifests[0].Kind != "plugin" || response.Manifests[1].Kind != "skill" {
		t.Fatalf("response = %#v", response)
	}
}

func fileURI(path string) string {
	return "file:///" + filepath.ToSlash(path)
}
