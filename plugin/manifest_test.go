package plugin

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadAgentPluginManifestUsesPortableMetadataAndFixedComponentsLikeRust(t *testing.T) {
	root := t.TempDir()
	writeAgentPluginManifestForTest(t, root, `,
  "version": "release-2026-07",
  "description": "Portable demo",
  "author": {"name": "Portable Author"},
  "homepage": "https://example.com/plugin",
  "keywords": ["portable"]`)
	if err := os.MkdirAll(filepath.Join(root, "skills", "deploy"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "skills", "deploy", "SKILL.md"), []byte("---\nname: deploy\ndescription: deploy\n---\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "mcp.json"), []byte(`{"mcpServers":{"docs":{"type":"http","url":"https://example.test/mcp"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	resolved, err := loadPluginManifest(root)
	if err != nil {
		t.Fatalf("loadPluginManifest() error = %v", err)
	}
	manifest := resolved.Manifest
	if resolved.Path != filepath.Join(root, "plugin.json") || !manifest.AgentPlugin {
		t.Fatalf("resolved manifest = %#v", resolved)
	}
	if manifest.Name != "demo-plugin" || manifest.Version != "release-2026-07" || manifest.Description != "Portable demo" {
		t.Fatalf("portable metadata = %#v", manifest)
	}
	if manifest.Interface == nil || stringValuePtr(manifest.Interface.DisplayName) != "demo-plugin" || stringValuePtr(manifest.Interface.DeveloperName) != "Portable Author" || stringValuePtr(manifest.Interface.WebsiteURL) != "https://example.com/plugin" || manifest.Interface.Category != "Other" {
		t.Fatalf("portable interface = %#v", manifest.Interface)
	}
	if !marketplacePluginHasSkillsForManifest(root, &manifest) || strings.Join(marketplacePluginMCPServersForManifest(root, &manifest), ",") != "docs" {
		t.Fatalf("portable components skills=%v mcp=%#v", marketplacePluginHasSkillsForManifest(root, &manifest), marketplacePluginMCPServersForManifest(root, &manifest))
	}
}

func TestAgentPluginManifestInlineOpenAIExtensionPrecedesLegacyOverlayLikeRust(t *testing.T) {
	root := t.TempDir()
	writeAgentPluginManifestForTest(t, root, `,
  "extensions": {"com.openai":{"interface":{"displayName":"Inline Codex"}}}`)
	if err := os.MkdirAll(filepath.Join(root, ".codex-plugin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".codex-plugin", "plugin.json"), []byte(`{"interface":{"displayName":"Legacy Codex"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	resolved, err := loadPluginManifest(root)
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Manifest.Interface == nil || stringValuePtr(resolved.Manifest.Interface.DisplayName) != "Inline Codex" {
		t.Fatalf("interface = %#v", resolved.Manifest.Interface)
	}
}

func TestFindPluginManifestIgnoresUnrelatedRootAndRejectsUnsupportedAgentSchemaLikeRust(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "plugin.json"), []byte(`{"name":"npm-package"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(root, ".codex-plugin", "plugin.json")
	if err := os.MkdirAll(filepath.Dir(legacy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(legacy, []byte(`{"name":"legacy"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	path, err := findPluginManifestPath(root)
	if err != nil || path != legacy {
		t.Fatalf("findPluginManifestPath() = %q, %v; want legacy", path, err)
	}

	if err := os.WriteFile(filepath.Join(root, "plugin.json"), []byte(`{"$schema":"https://agent-plugins.org/schemas/2.0.0/plugin.schema.json","name":"demo"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := findPluginManifestPath(root); err == nil || !strings.Contains(err.Error(), "unsupported Agent Plugins schema") {
		t.Fatalf("unsupported schema error = %v", err)
	}
}

func TestAgentPluginSkillsOnlyIncludeDirectChildrenAndStayWithinRootLikeRust(t *testing.T) {
	root := t.TempDir()
	writeAgentPluginManifestForTest(t, root, "")
	writeSkill := func(path, name string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte("---\nname: "+name+"\ndescription: "+name+"\n---\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeSkill(filepath.Join(root, "skills", "direct", "SKILL.md"), "direct")
	writeSkill(filepath.Join(root, "skills", "group", "nested", "SKILL.md"), "nested")
	manifest := readPluginManifestForRoot(root)
	skilled := marketplacePluginSkillsForManifest(root, manifest)
	if len(skilled) != 1 || skilled[0].Name != "direct" {
		t.Fatalf("skills = %#v, want direct child only", skilled)
	}
}

func TestPluginSharePublishCapabilityUsesRustWireShape(t *testing.T) {
	allowed := true
	contextPayload := manifestTestObject(t, &PluginShareContext{RemotePluginID: "remote-1", CanPublishToWorkspace: &allowed})
	if contextPayload["canPublishToWorkspace"] != true {
		t.Fatalf("share context = %#v", contextPayload)
	}
	responsePayload := manifestTestObject(t, &PluginShareSaveResponse{RemotePluginID: "remote-1", ShareURL: "https://example.test/share", CanPublishToWorkspace: &allowed})
	if responsePayload["canPublishToWorkspace"] != true {
		t.Fatalf("share response = %#v", responsePayload)
	}
	nullPayload := manifestTestObject(t, &PluginShareSaveResponse{RemotePluginID: "remote-1"})
	if value, ok := nullPayload["canPublishToWorkspace"]; !ok || value != nil {
		t.Fatalf("nullable publish capability = %#v", nullPayload)
	}
}

func writeAgentPluginManifestForTest(t *testing.T, root string, extra string) {
	t.Helper()
	contents := `{"$schema":"` + AgentPluginSchemaURI + `","name":"demo-plugin"` + extra + `}`
	if err := os.WriteFile(filepath.Join(root, "plugin.json"), []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
}

func manifestTestObject(t *testing.T, value any) map[string]any {
	t.Helper()
	data, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	return out
}
