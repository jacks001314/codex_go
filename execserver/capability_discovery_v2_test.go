package execserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
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

func TestV2DiscoveryManagerCachesUntilDirtyLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	t.Setenv("CODEX_HOME", codexHome)
	writeV2File(t, filepath.Join(codexHome, "skills", "demo", "SKILL.md"), "---\nname: demo\ndescription: Demo\n---\n")
	cwd, err := utils.FromHostNativePath(codexHome)
	if err != nil {
		t.Fatal(err)
	}
	manager := newV2DiscoveryManager()
	params := &DiscoverV2CapabilitiesParams{CWD: cwd.String()}
	first, err := manager.Snapshot(params)
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Snapshot(params)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatal("clean V2 discovery cache was not reused")
	}
	manager.markDirty()
	third, err := manager.Snapshot(params)
	if err != nil {
		t.Fatal(err)
	}
	if third == first {
		t.Fatal("dirty V2 discovery cache was reused")
	}
}

func TestV2CapabilityLocationsIncludeSystemSkillsRootLikeRust(t *testing.T) {
	if runtime.GOOS == "windows" {
		programData := t.TempDir()
		t.Setenv("ProgramData", programData)
		if got, want := v2SystemSkillsRoot(), filepath.Join(programData, "OpenAI", "Codex", "skills"); got != want {
			t.Fatalf("system skills root = %q, want %q", got, want)
		}
	} else if got, want := v2SystemSkillsRoot(), filepath.Join(string(filepath.Separator), "etc", "codex", "skills"); got != want {
		t.Fatalf("system skills root = %q, want %q", got, want)
	}
	locations, _ := v2DefaultCapabilityLocations()
	found := false
	for _, location := range locations {
		if location.id == "system-skills" {
			found = location.root == v2SystemSkillsRoot()
		}
	}
	if !found {
		t.Fatalf("system-skills location missing: %#v", locations)
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
