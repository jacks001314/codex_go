package execserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/utils"
)

func TestDiscoverCapabilitiesV2MetadataOnlyLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	userHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	t.Setenv("HOME", userHome)
	t.Setenv("USERPROFILE", userHome)
	writeV2File(t, filepath.Join(codexHome, "skills", "demo", "SKILL.md"), `---
name: demo
description: Standalone demo skill
---
PRIVATE INSTRUCTION BODY
`)
	pluginRoot := filepath.Join(codexHome, "plugins", "cache", "test", "mail", "1.0.0")
	writeV2File(t, filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"), `{"name":"mail","interface":{"displayName":"Mail"}}`)
	writeV2File(t, filepath.Join(pluginRoot, "skills", "search", "SKILL.md"), `---
name: search
description: Search mail
---
PRIVATE PLUGIN BODY
`)
	cwd, err := utils.FromHostNativePath(codexHome)
	if err != nil {
		t.Fatal(err)
	}
	response, err := discoverCapabilitiesV2(&DiscoverV2CapabilitiesParams{CWD: cwd.String()})
	if err != nil {
		t.Fatalf("discoverCapabilitiesV2() error = %v", err)
	}
	if len(response.Skills) != 1 || response.Skills[0].Name != "demo" ||
		response.Skills[0].Description != "Standalone demo skill" ||
		response.Skills[0].DiscoveryRoot == nil || response.Skills[0].DiscoveryRoot.ID != "codex-home-skills" {
		t.Fatalf("standalone skills = %#v", response.Skills)
	}
	if len(response.Plugins) != 1 {
		t.Fatalf("plugins = %#v", response.Plugins)
	}
	plugin := response.Plugins[0]
	if plugin.ID != "mail@test" || plugin.DisplayName != "Mail" || plugin.Version != "1.0.0" ||
		len(plugin.Skills) != 1 || plugin.Skills[0].Name != "search" || plugin.Skills[0].Description != "Search mail" {
		t.Fatalf("plugin = %#v", plugin)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "PRIVATE") {
		t.Fatalf("discovery response leaked skill instructions: %s", encoded)
	}
}

func TestEnvironmentInfoAdvertisesCapabilityDiscoveryV2(t *testing.T) {
	info := localEnvironmentInfo()
	if !info.Capabilities.CapabilityDiscoveryV2 {
		t.Fatal("local executor did not advertise capabilityDiscoveryV2")
	}
}

func writeV2File(t *testing.T, path string, contents string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}
