package appserver

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/internal/config"
	"codex_go/internal/install"
)

func TestListSkillsAndConfig(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "skill-a")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("# Useful skill\nbody"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	service := NewSkillsService([]string{root})
	response, err := service.List(&SkillsListParams{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(response.Skills) != 1 || response.Skills[0].Name != "skill-a" || response.Skills[0].Description != "Useful skill" {
		t.Fatalf("response = %#v", response)
	}
	writeResponse, err := service.WriteConfig(&SkillsConfigWriteParams{Name: "skill-a", Enabled: false})
	if err != nil {
		t.Fatalf("WriteConfig() error = %v", err)
	}
	if writeResponse.EffectiveEnabled || !writeResponse.Updated {
		t.Fatalf("WriteConfig() = %#v", writeResponse)
	}
	writePayload, err := json.Marshal(writeResponse)
	if err != nil {
		t.Fatalf("Marshal(WriteConfig) error = %v", err)
	}
	var writeTop map[string]any
	if err := json.Unmarshal(writePayload, &writeTop); err != nil {
		t.Fatalf("Unmarshal(WriteConfig) error = %v", err)
	}
	if _, ok := writeTop["updated"]; ok || writeTop["effectiveEnabled"] != false {
		t.Fatalf("WriteConfig payload = %s", writePayload)
	}
	response, err = service.List(&SkillsListParams{ForceReload: true})
	if err != nil {
		t.Fatalf("List(after config) error = %v", err)
	}
	if len(response.Skills) != 0 {
		t.Fatalf("response = %#v", response)
	}
	if _, err := service.WriteConfig(&SkillsConfigWriteParams{}); !errors.Is(err, ErrInvalidSkillsRequest) {
		t.Fatalf("WriteConfig(empty) error = %v, want ErrInvalidSkillsRequest", err)
	}
	if _, err := service.WriteConfig(&SkillsConfigWriteParams{Name: "skill-a", Path: filepath.Join(skillDir, SkillFilename), Enabled: true}); !errors.Is(err, ErrInvalidSkillsRequest) {
		t.Fatalf("WriteConfig(name+path) error = %v, want ErrInvalidSkillsRequest", err)
	}
}

func TestSkillsServiceLoadsPersistentConfig(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "skill-a")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("# Skill A"), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("[[skills.config]]\nname = \"skill-a\"\nenabled = false\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	service := NewSkillsServiceWithOptions(&SkillsServiceOptions{
		Roots:  []string{root},
		Config: config.NewConfigService(home),
	})
	response, err := service.List(&SkillsListParams{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(response.Skills) != 0 {
		t.Fatalf("skills = %#v, want disabled by persistent config", response.Skills)
	}
}

func TestSkillsServiceIncludesBundledSystemSkillsRoot(t *testing.T) {
	packageDir := t.TempDir()
	resourcesDir := filepath.Join(packageDir, "codex-resources")
	skillDir := filepath.Join(resourcesDir, "skills", ".system", "sys-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(system skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: sys-skill\ndescription: Bundled system skill\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(system skill) error = %v", err)
	}
	service := NewSkillsServiceWithOptions(&SkillsServiceOptions{
		CodexHome:           t.TempDir(),
		InstallContext:      &install.InstallContext{PackageLayout: &install.CodexPackageLayout{ResourcesDir: &resourcesDir}},
		IncludeDefaultRoots: true,
	})
	response, err := service.List(&SkillsListParams{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(response.Skills) != 1 || response.Skills[0].Name != "sys-skill" || response.Skills[0].Scope != "system" {
		t.Fatalf("skills = %#v, want bundled system skill", response.Skills)
	}
}

func TestWriteConfigPersistsAndRemovesSkillOverride(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "skill-a")
	skillPath := filepath.Join(skillDir, SkillFilename)
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(skillPath, []byte("# Skill A"), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}
	home := t.TempDir()
	service := NewSkillsServiceWithOptions(&SkillsServiceOptions{
		Roots:  []string{root},
		Config: config.NewConfigService(home),
	})

	writeResponse, err := service.WriteConfig(&SkillsConfigWriteParams{Path: skillPath, Enabled: false})
	if err != nil {
		t.Fatalf("WriteConfig(disable) error = %v", err)
	}
	if writeResponse.EffectiveEnabled {
		t.Fatalf("WriteConfig(disable) = %#v", writeResponse)
	}
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(config) error = %v", err)
	}
	configText := string(data)
	if !strings.Contains(configText, "[[skills.config]]") || !strings.Contains(configText, "enabled = false") || !strings.Contains(configText, SkillFilename) {
		t.Fatalf("config.toml = %s", configText)
	}
	response, err := service.List(&SkillsListParams{ForceReload: true})
	if err != nil {
		t.Fatalf("List(after disable) error = %v", err)
	}
	if len(response.Skills) != 0 {
		t.Fatalf("skills = %#v, want disabled", response.Skills)
	}

	if _, err := service.WriteConfig(&SkillsConfigWriteParams{Path: skillPath, Enabled: true}); err != nil {
		t.Fatalf("WriteConfig(enable) error = %v", err)
	}
	data, err = os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(config after enable) error = %v", err)
	}
	if strings.Contains(string(data), "skills.config") {
		t.Fatalf("config.toml after enable = %s", data)
	}
	response, err = service.List(&SkillsListParams{ForceReload: true})
	if err != nil {
		t.Fatalf("List(after enable) error = %v", err)
	}
	if len(response.Skills) != 1 || response.Skills[0].Name != "skill-a" {
		t.Fatalf("skills = %#v, want enabled skill-a", response.Skills)
	}
}

func TestSetExtraRoots(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "skill-b"), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "skill-b", SkillFilename), []byte("Skill B"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	service := NewSkillsService(nil)
	if _, err := service.SetExtraRoots(&SkillsExtraRootsSetParams{ExtraRoots: []string{root}}); err != nil {
		t.Fatalf("SetExtraRoots() error = %v", err)
	}
	response, err := service.List(&SkillsListParams{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(response.Skills) != 1 || response.Skills[0].Name != "skill-b" {
		t.Fatalf("response = %#v", response)
	}
}

func TestListSkillsParsesFrontmatterAndMetadata(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "skill-c")
	if err := os.MkdirAll(filepath.Join(skillDir, SkillMetadataDir), 0o755); err != nil {
		t.Fatalf("MkdirAll(metadata) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte(`---
name: demo-skill
description: Build for AWS: ECS
metadata:
  short-description: What's included: builds and tests
---

# Body
`), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillMetadataDir, SkillMetadataFilename), []byte(`
interface:
  display_name: "Demo Skill"
  short_description: "  UI   short  "
  icon_small: "./assets/small.png"
  icon_large: "icon.png"
  brand_color: "#336699"
  default_prompt: "  Use   this skill  "
dependencies:
  tools:
    - type: mcp
      value: docs
      description: "Docs server"
      transport: stdio
      command: docs-mcp
    - type: cli
      value: gh
      description: "GitHub CLI"
      url: "https://example.com"
    - type: ""
      value: ignored
policy:
  allow_implicit_invocation: false
  products:
    - CODEX
    - ignored-product
`), 0o600); err != nil {
		t.Fatalf("WriteFile(metadata) error = %v", err)
	}
	service := NewSkillsService([]string{root})
	response, err := service.List(&SkillsListParams{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(response.Skills) != 1 {
		t.Fatalf("skills = %#v", response.Skills)
	}
	skill := response.Skills[0]
	if skill.Name != "demo-skill" || skill.Description != "Build for AWS: ECS" || skill.ShortDescription != "What's included: builds and tests" {
		t.Fatalf("skill metadata = %#v", skill)
	}
	if skill.Interface == nil {
		t.Fatalf("Interface is nil")
	}
	if skill.Interface.DisplayName != "Demo Skill" || skill.Interface.ShortDescription != "UI short" {
		t.Fatalf("Interface = %#v", skill.Interface)
	}
	if skill.Interface.IconSmall == nil || *skill.Interface.IconSmall != filepath.Join(skillDir, "assets", "small.png") {
		t.Fatalf("IconSmall = %#v", skill.Interface.IconSmall)
	}
	if skill.Interface.IconLarge != nil {
		t.Fatalf("IconLarge = %#v, want nil for non-assets path", *skill.Interface.IconLarge)
	}
	if skill.Interface.BrandColor == nil || *skill.Interface.BrandColor != "#336699" {
		t.Fatalf("BrandColor = %#v", skill.Interface.BrandColor)
	}
	if skill.Interface.DefaultPrompt == nil || *skill.Interface.DefaultPrompt != "Use this skill" {
		t.Fatalf("DefaultPrompt = %#v", skill.Interface.DefaultPrompt)
	}
	if skill.Dependencies == nil || len(skill.Dependencies.Tools) != 2 {
		t.Fatalf("Dependencies = %#v", skill.Dependencies)
	}
	first := skill.Dependencies.Tools[0]
	if first.Type != "mcp" || first.Value != "docs" || first.Description != "Docs server" || first.Transport != "stdio" {
		t.Fatalf("first dependency = %#v", first)
	}
	if first.Command == nil || *first.Command != "docs-mcp" || first.URL != nil {
		t.Fatalf("first dependency optional fields = %#v", first)
	}
	second := skill.Dependencies.Tools[1]
	if second.Type != "cli" || second.Value != "gh" || second.URL == nil || *second.URL != "https://example.com" {
		t.Fatalf("second dependency = %#v", second)
	}
	if skill.AllowsImplicitInvocation() {
		t.Fatalf("AllowsImplicitInvocation() = true, want false")
	}
	if skill.Policy == nil || len(skill.Policy.Products) != 1 || skill.Policy.Products[0] != "codex" {
		t.Fatalf("Policy = %#v", skill.Policy)
	}
	payload, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal(response) error = %v", err)
	}
	var top map[string]any
	if err := json.Unmarshal(payload, &top); err != nil {
		t.Fatalf("Unmarshal(response) error = %v", err)
	}
	if _, ok := top["skills"]; ok {
		t.Fatalf("top-level legacy skills key should not be emitted: %s", payload)
	}
	if string(payload) == "" || top["data"] == nil {
		t.Fatalf("response payload = %s", payload)
	}
	dataEntries := top["data"].([]any)
	firstEntry := dataEntries[0].(map[string]any)
	for _, legacyKey := range []string{"name", "path", "enabled"} {
		if _, ok := firstEntry[legacyKey]; ok {
			t.Fatalf("cwd skill list entry leaked %q: %#v", legacyKey, firstEntry)
		}
	}
	skills, ok := firstEntry["skills"].([]any)
	if !ok || len(skills) != 1 {
		t.Fatalf("cwd entry skills = %#v", firstEntry["skills"])
	}
	skillPayload := skills[0].(map[string]any)
	dependencies := skillPayload["dependencies"].(map[string]any)
	tools := dependencies["tools"].([]any)
	firstTool := tools[0].(map[string]any)
	if firstTool["type"] != "mcp" || firstTool["value"] != "docs" {
		t.Fatalf("tool dependency required fields = %#v", firstTool)
	}
	emptyDependency, err := json.Marshal(&SkillToolDependency{})
	if err != nil {
		t.Fatalf("Marshal empty dependency error = %v", err)
	}
	if !strings.Contains(string(emptyDependency), `"type":""`) || !strings.Contains(string(emptyDependency), `"value":""`) {
		t.Fatalf("empty dependency JSON = %s", emptyDependency)
	}
}

func TestListSkillsParsesMetadataAliases(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "skill-alias")
	if err := os.MkdirAll(filepath.Join(skillDir, SkillMetadataDir), 0o755); err != nil {
		t.Fatalf("MkdirAll(metadata) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte(`---
name: alias-skill
description: Long alias description
metadata:
  shortDescription: Alias short
---
`), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillMetadataDir, SkillMetadataFilename), []byte(`
interface:
  displayName: Alias Skill
  short-description: UI alias short
  iconSmall: assets/icon.png
  brand-color: "#123456"
  defaultPrompt: Use aliases
dependencies:
  tools:
    - kind: mcp
      name: alias-calendar
      transport: stdio
      command: alias-calendar-mcp
    - type: mcp
      mcpServer: alias-docs
      url: https://mcp.example.test
policy:
  allowImplicitInvocation: false
`), 0o600); err != nil {
		t.Fatalf("WriteFile(metadata) error = %v", err)
	}
	service := NewSkillsService([]string{root})
	response, err := service.List(&SkillsListParams{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(response.Skills) != 1 {
		t.Fatalf("skills = %#v", response.Skills)
	}
	skill := response.Skills[0]
	if skill.ShortDescription != "Alias short" {
		t.Fatalf("ShortDescription = %q", skill.ShortDescription)
	}
	if skill.Interface == nil || skill.Interface.DisplayName != "Alias Skill" || skill.Interface.ShortDescription != "UI alias short" {
		t.Fatalf("Interface aliases = %#v", skill.Interface)
	}
	if skill.Interface.IconSmall == nil || *skill.Interface.IconSmall != filepath.Join(skillDir, "assets", "icon.png") {
		t.Fatalf("IconSmall alias = %#v", skill.Interface.IconSmall)
	}
	if skill.Interface.BrandColor == nil || *skill.Interface.BrandColor != "#123456" {
		t.Fatalf("BrandColor alias = %#v", skill.Interface.BrandColor)
	}
	if skill.Interface.DefaultPrompt == nil || *skill.Interface.DefaultPrompt != "Use aliases" {
		t.Fatalf("DefaultPrompt alias = %#v", skill.Interface.DefaultPrompt)
	}
	if skill.Dependencies == nil || len(skill.Dependencies.Tools) != 2 {
		t.Fatalf("Dependency aliases = %#v", skill.Dependencies)
	}
	first := skill.Dependencies.Tools[0]
	if first.Type != "mcp" || first.Value != "alias-calendar" || first.Transport != "stdio" || first.Command == nil || *first.Command != "alias-calendar-mcp" {
		t.Fatalf("first dependency alias = %#v", first)
	}
	second := skill.Dependencies.Tools[1]
	if second.Type != "mcp" || second.Value != "alias-docs" || second.URL == nil || *second.URL != "https://mcp.example.test" {
		t.Fatalf("second dependency alias = %#v", second)
	}
	if skill.AllowsImplicitInvocation() {
		t.Fatalf("AllowsImplicitInvocation() = true, want false from camelCase alias")
	}
}

func TestRemoteSkillMetadataAssetsUseEnvironmentLocator(t *testing.T) {
	entry := remoteSkillEntryFromContents("remote-env", "file:///remote/skills/demo/SKILL.md", `---
name: demo
description: Remote demo
---
`, `
interface:
  icon_small: assets/icon.png
  icon_large: assets/large.png
`)
	if entry.Interface == nil || entry.Interface.IconSmall == nil || entry.Interface.IconLarge == nil {
		t.Fatalf("remote interface = %#v", entry.Interface)
	}
	if *entry.Interface.IconSmall != "environment://remote-env/remote/skills/demo/assets/icon.png" {
		t.Fatalf("icon small = %q", *entry.Interface.IconSmall)
	}
	if *entry.Interface.IconLarge != "environment://remote-env/remote/skills/demo/assets/large.png" {
		t.Fatalf("icon large = %q", *entry.Interface.IconLarge)
	}
}

func TestRemoteSkillSourcePathEscapesLocalPaths(t *testing.T) {
	got := remoteSkillSourcePath("remote-env", "/remote/skills/demo app/SKILL.md")
	if got != "environment://remote-env/remote/skills/demo%20app/SKILL.md" {
		t.Fatalf("remoteSkillSourcePath() = %q", got)
	}
}

func TestSkillsListEntryMarshalIncludesPluginID(t *testing.T) {
	payload, err := json.Marshal(&SkillsListEntry{
		Name:        "review",
		Description: "Review with plugin context",
		Path:        "/plugins/docs/skills/review/SKILL.md",
		Scope:       "plugin",
		Enabled:     true,
		PluginID:    "docs@market",
	})
	if err != nil {
		t.Fatalf("Marshal skill entry error = %v", err)
	}
	var values map[string]any
	if err := json.Unmarshal(payload, &values); err != nil {
		t.Fatalf("Unmarshal skill entry error = %v", err)
	}
	if values["pluginId"] != "docs@market" {
		t.Fatalf("pluginId missing from payload: %s", payload)
	}
}
