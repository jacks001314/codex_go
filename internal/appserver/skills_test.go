package appserver

import (
	"context"
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
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: skill-a\ndescription: Useful skill\n---\nbody\n"), 0o600); err != nil {
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

func TestListSkillsSkipsInvalidSkillAndReportsErrorLikeRust(t *testing.T) {
	root := t.TempDir()
	validDir := filepath.Join(root, "valid")
	invalidDir := filepath.Join(root, "invalid")
	overlongNameDir := filepath.Join(root, "overlong-name")
	if err := os.MkdirAll(validDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(valid) error = %v", err)
	}
	if err := os.MkdirAll(invalidDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(invalid) error = %v", err)
	}
	if err := os.MkdirAll(overlongNameDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(overlong name) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(validDir, SkillFilename), []byte("---\nname: valid\ndescription: Valid skill\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(valid) error = %v", err)
	}
	invalidPath := filepath.Join(invalidDir, SkillFilename)
	if err := os.WriteFile(invalidPath, []byte("---\nname: invalid"), 0o600); err != nil {
		t.Fatalf("WriteFile(invalid) error = %v", err)
	}
	overlongNamePath := filepath.Join(overlongNameDir, SkillFilename)
	if err := os.WriteFile(overlongNamePath, []byte("---\nname: "+strings.Repeat("n", skillMaxNameLen+1)+"\ndescription: too long name\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(overlong name) error = %v", err)
	}
	response, err := NewSkillsService([]string{root}).List(&SkillsListParams{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(response.Skills) != 1 || response.Skills[0].Name != "valid" {
		t.Fatalf("skills = %#v, want only valid skill", response.Skills)
	}
	if len(response.Data) != 1 || len(response.Data[0].Errors) != 2 {
		t.Fatalf("data errors = %#v, want invalid skill errors", response.Data)
	}
	errorsByPath := map[string]string{}
	for _, skillErr := range response.Data[0].Errors {
		errorsByPath[skillErr.Path] = skillErr.Message
	}
	if message := errorsByPath[filepath.Clean(invalidPath)]; !strings.Contains(message, "missing YAML frontmatter delimited by ---") {
		t.Fatalf("invalid frontmatter error = %q", message)
	}
	if message := errorsByPath[filepath.Clean(overlongNamePath)]; !strings.Contains(message, "invalid name: exceeds maximum length of 64 characters") {
		t.Fatalf("overlong name error = %q", message)
	}

	systemService := NewSkillsServiceWithOptions(&SkillsServiceOptions{
		RootSpecs: []SkillsRoot{{Path: root, Scope: "system"}},
	})
	systemResponse, err := systemService.List(&SkillsListParams{ForceReload: true})
	if err != nil {
		t.Fatalf("system List() error = %v", err)
	}
	if len(systemResponse.Skills) != 1 || len(systemResponse.Data) != 1 || len(systemResponse.Data[0].Errors) != 0 {
		t.Fatalf("system response = %#v, want invalid system skill ignored without error", systemResponse)
	}
}

func TestListSkillsPreservesOverlongDescriptionsLikeRust(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "long-description")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	overlong := strings.Repeat("x", skillMaxDescriptionLen+1)
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: long-description\ndescription: "+overlong+"\nmetadata:\n  short-description: "+overlong+"\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	response, err := NewSkillsService([]string{root}).List(&SkillsListParams{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(response.Skills) != 1 {
		t.Fatalf("skills = %#v", response.Skills)
	}
	if response.Skills[0].Description != overlong || response.Skills[0].ShortDescription != overlong {
		t.Fatalf("descriptions = description len %d short len %d", len(response.Skills[0].Description), len(response.Skills[0].ShortDescription))
	}
}

func TestListSkillsHonorsRustScanDepthLimit(t *testing.T) {
	root := t.TempDir()
	depthSix := filepath.Join(root, "d1", "d2", "d3", "d4", "d5", "d6")
	depthSeven := filepath.Join(depthSix, "d7")
	if err := os.MkdirAll(depthSix, 0o755); err != nil {
		t.Fatalf("MkdirAll(depthSix) error = %v", err)
	}
	if err := os.MkdirAll(depthSeven, 0o755); err != nil {
		t.Fatalf("MkdirAll(depthSeven) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(depthSix, SkillFilename), []byte("---\nname: depth-six\ndescription: Depth six\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(depthSix) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(depthSeven, SkillFilename), []byte("---\nname: depth-seven\ndescription: Depth seven\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(depthSeven) error = %v", err)
	}
	response, err := NewSkillsService([]string{root}).List(&SkillsListParams{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(response.Skills) != 1 || response.Skills[0].Name != "depth-six" {
		t.Fatalf("skills = %#v, want only depth-six like Rust max scan depth", response.Skills)
	}
}

func TestListSkillsFollowsDirectorySymlinksOutsideSystemLikeRust(t *testing.T) {
	root := t.TempDir()
	target := t.TempDir()
	skillDir := filepath.Join(target, "linked-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(skillDir) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: linked-skill\ndescription: Linked skill\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}
	linkPath := filepath.Join(root, "linked")
	if err := os.Symlink(target, linkPath); err != nil {
		t.Skipf("directory symlinks are unavailable in this environment: %v", err)
	}

	userResponse, err := NewSkillsServiceWithOptions(&SkillsServiceOptions{
		RootSpecs: []SkillsRoot{{Path: root, Scope: "user"}},
	}).List(&SkillsListParams{})
	if err != nil {
		t.Fatalf("user List() error = %v", err)
	}
	if len(userResponse.Skills) != 1 || userResponse.Skills[0].Name != "linked-skill" {
		t.Fatalf("user skills = %#v, want symlinked skill", userResponse.Skills)
	}
	expectedPath, err := filepath.EvalSymlinks(filepath.Join(skillDir, SkillFilename))
	if err != nil {
		t.Fatalf("EvalSymlinks(skill) error = %v", err)
	}
	if userResponse.Skills[0].Path != filepath.Clean(expectedPath) {
		t.Fatalf("symlinked skill path = %q, want canonical %q", userResponse.Skills[0].Path, filepath.Clean(expectedPath))
	}

	systemResponse, err := NewSkillsServiceWithOptions(&SkillsServiceOptions{
		RootSpecs: []SkillsRoot{{Path: root, Scope: "system"}},
	}).List(&SkillsListParams{})
	if err != nil {
		t.Fatalf("system List() error = %v", err)
	}
	if len(systemResponse.Skills) != 0 {
		t.Fatalf("system skills = %#v, want symlink dir ignored like Rust", systemResponse.Skills)
	}
}

func TestSkillsServiceLoadsPersistentConfig(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "skill-a")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: skill-a\ndescription: Skill A\n---\n"), 0o600); err != nil {
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
	if err := os.WriteFile(skillPath, []byte("---\nname: skill-a\ndescription: Skill A\n---\n"), 0o600); err != nil {
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
	if err := os.WriteFile(filepath.Join(root, "skill-b", SkillFilename), []byte("---\nname: skill-b\ndescription: Skill B\n---\n"), 0o600); err != nil {
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

func TestSkillsListIncludesCWDCodeXSkillsRootLikeRust(t *testing.T) {
	cwd := t.TempDir()
	skillDir := filepath.Join(cwd, ".codex", "skills", "repo-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: repo-skill\ndescription: from repo root\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}

	response, err := NewSkillsService(nil).List(&SkillsListParams{CWDs: []string{cwd}, ForceReload: true})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(response.Data) != 1 || len(response.Data[0].Skills) != 1 {
		t.Fatalf("response = %#v, want one cwd repo skill", response)
	}
	skill := response.Data[0].Skills[0]
	if skill.Name != "repo-skill" || skill.Scope != "repo" || skill.Description != "from repo root" {
		t.Fatalf("skill = %#v", skill)
	}
}

func TestSkillsListIncludesGlobalSkillsForRequestedCWDLikeRust(t *testing.T) {
	cwd := t.TempDir()
	root := t.TempDir()
	skillDir := filepath.Join(root, "imagegen")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: imagegen\ndescription: Generate images\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}

	response, err := NewSkillsServiceWithOptions(&SkillsServiceOptions{
		RootSpecs: []SkillsRoot{{Path: root, Scope: "system"}},
	}).List(&SkillsListParams{CWDs: []string{cwd}, ForceReload: true})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(response.Data) != 1 || len(response.Data[0].Skills) != 1 {
		t.Fatalf("response = %#v, want global skill in cwd data", response)
	}
	skill := response.Data[0].Skills[0]
	if skill.Name != "imagegen" || skill.Scope != "system" || skill.Description != "Generate images" {
		t.Fatalf("skill = %#v", skill)
	}
}

func TestSkillsListPreservesRequestedCWDOrderAndRelativeCWDLikeRust(t *testing.T) {
	relative := filepath.Join("relative-cwd")
	absolute := t.TempDir()
	response, err := NewSkillsService(nil).List(&SkillsListParams{CWDs: []string{relative, absolute}, ForceReload: true})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(response.Data) != 2 {
		t.Fatalf("response = %#v, want two cwd entries", response)
	}
	if response.Data[0].CWD != relative || response.Data[1].CWD != absolute {
		t.Fatalf("cwd order = %#v, want %q then %q", []string{response.Data[0].CWD, response.Data[1].CWD}, relative, absolute)
	}
}

func TestSkillsListUsesCachedResultUntilForceReloadLikeRust(t *testing.T) {
	cwd := t.TempDir()
	service := NewSkillsService(nil)
	first, err := service.List(&SkillsListParams{CWDs: []string{cwd}})
	if err != nil {
		t.Fatalf("List(first) error = %v", err)
	}
	if len(first.Data) != 1 || len(first.Data[0].Skills) != 0 {
		t.Fatalf("first response = %#v, want no skills", first)
	}

	skillDir := filepath.Join(cwd, ".codex", "skills", "late-extra-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: late-extra-skill\ndescription: late skill\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}

	cached, err := service.List(&SkillsListParams{CWDs: []string{cwd}})
	if err != nil {
		t.Fatalf("List(cached) error = %v", err)
	}
	if len(cached.Data) != 1 || len(cached.Data[0].Skills) != 0 {
		t.Fatalf("cached response = %#v, want cached empty skills", cached)
	}
	refreshed, err := service.List(&SkillsListParams{CWDs: []string{cwd}, ForceReload: true})
	if err != nil {
		t.Fatalf("List(force) error = %v", err)
	}
	if len(refreshed.Data) != 1 || len(refreshed.Data[0].Skills) != 1 || refreshed.Data[0].Skills[0].Name != "late-extra-skill" {
		t.Fatalf("refreshed response = %#v, want late-extra-skill", refreshed)
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

func TestListSkillsIgnoresGoOnlyMetadataAliasesLikeRust(t *testing.T) {
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
	if skill.Description != "Long alias description" || skill.ShortDescription != "" {
		t.Fatalf("skill descriptions = description:%q short:%q", skill.Description, skill.ShortDescription)
	}
	if skill.Interface != nil {
		t.Fatalf("Go-only interface aliases should be ignored like Rust: %#v", skill.Interface)
	}
	if skill.Dependencies != nil {
		t.Fatalf("Go-only dependency aliases should be ignored like Rust: %#v", skill.Dependencies)
	}
	if skill.Policy != nil {
		t.Fatalf("Go-only policy aliases should be ignored like Rust: %#v", skill.Policy)
	}
}

func TestListSkillsFiltersPolicyProductsLikeRust(t *testing.T) {
	root := t.TempDir()
	writeSkill := func(name string, metadata string) {
		t.Helper()
		skillDir := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Join(skillDir, SkillMetadataDir), 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: "+name+"\ndescription: "+name+" skill\n---\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s skill) error = %v", name, err)
		}
		if strings.TrimSpace(metadata) != "" {
			if err := os.WriteFile(filepath.Join(skillDir, SkillMetadataDir, SkillMetadataFilename), []byte(metadata), 0o600); err != nil {
				t.Fatalf("WriteFile(%s metadata) error = %v", name, err)
			}
		}
	}
	writeSkill("codex-skill", "policy:\n  products: [codex]\n")
	writeSkill("chatgpt-skill", "policy:\n  products: [chatgpt]\n")
	writeSkill("open-skill", "")

	response, err := NewSkillsService([]string{root}).List(&SkillsListParams{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	names := make([]string, 0, len(response.Skills))
	for _, skill := range response.Skills {
		names = append(names, skill.Name)
	}
	if strings.Join(names, ",") != "codex-skill,open-skill" {
		t.Fatalf("skill names = %v, want Codex product filtering like Rust", names)
	}
}

func TestRemoteSkillMetadataAssetsUseEnvironmentLocator(t *testing.T) {
	entry, warning, ok := remoteSkillEntryFromContents("remote-env", "file:///remote/skills/demo/SKILL.md", `---
name: demo
description: Remote demo
---
`, `
interface:
  icon_small: assets/icon.png
  icon_large: assets/large.png
`, "")
	if !ok {
		t.Fatalf("remoteSkillEntryFromContents() ok = false, warning = %q", warning)
	}
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

func TestRemoteSkillEntrySkipsInvalidFrontmatterLikeRust(t *testing.T) {
	tests := []struct {
		name     string
		contents string
	}{
		{
			name:     "missing frontmatter",
			contents: "# Legacy heading\nbody\n",
		},
		{
			name: "missing description",
			contents: `---
name: demo
---
body
`,
		},
		{
			name:     "overlong name",
			contents: "---\nname: " + strings.Repeat("n", skillMaxNameLen+1) + "\ndescription: Remote demo\n---\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if entry, warning, ok := remoteSkillEntryFromContents("remote-env", "file:///remote/skills/demo/SKILL.md", tc.contents, "", ""); ok {
				t.Fatalf("remoteSkillEntryFromContents() = %#v, want skipped", entry)
			} else if !strings.Contains(warning, "Failed to load environment skill at file:///remote/skills/demo/SKILL.md:") {
				t.Fatalf("warning = %q, want Rust environment skill load warning", warning)
			}
		})
	}
}

func TestRemoteSkillEntryAppliesPluginNamespaceAndQualifiedNameLimitLikeRust(t *testing.T) {
	entry, warning, ok := remoteSkillEntryFromContents("remote-env", "file:///remote/plugin/skills/deploy/SKILL.md", `---
name: deploy
description: Deploy remotely
---
`, "", "demo-plugin")
	if !ok {
		t.Fatalf("remoteSkillEntryFromContents() ok = false, warning = %q", warning)
	}
	if entry.Name != "demo-plugin:deploy" {
		t.Fatalf("entry.Name = %q, want namespaced skill", entry.Name)
	}

	_, warning, ok = remoteSkillEntryFromContents("remote-env", "file:///remote/plugin/skills/deploy/SKILL.md", `---
name: deploy
description: Deploy remotely
---
`, "", strings.Repeat("n", skillMaxQualifiedNameLen))
	if ok {
		t.Fatal("remoteSkillEntryFromContents() ok = true, want overlong qualified name skipped")
	}
	if !strings.Contains(warning, "invalid qualified name: exceeds maximum length of 128 characters") {
		t.Fatalf("warning = %q", warning)
	}
}

func TestDiscoverRemoteEnvironmentSkillsNamespacesAndSortsLikeRust(t *testing.T) {
	rootURI := "file:///remote/plugin"
	manifestURI := "file:///remote/plugin/.codex-plugin/plugin.json"
	alphaPath := "file:///remote/plugin/skills/z-path/SKILL.md"
	chatgptPath := "file:///remote/plugin/skills/chatgpt-only/SKILL.md"
	chatgptMetadataPath := "file:///remote/plugin/skills/chatgpt-only/agents/openai.yaml"
	zetaPath := "file:///remote/plugin/skills/a-path/SKILL.md"
	execServerURL, done := newRemoteSkillsExecServerForTest(t, rootURI, map[string]string{
		manifestURI:         `{"name":"demo-plugin"}`,
		alphaPath:           "---\nname: alpha\ndescription: Alpha skill\n---\n",
		chatgptPath:         "---\nname: chatgpt-only\ndescription: ChatGPT-only skill\n---\n",
		chatgptMetadataPath: "policy:\n  products: [chatgpt]\n",
		zetaPath:            "---\nname: zeta\ndescription: Zeta skill\n---\n",
	})
	entries, warnings, err := discoverRemoteEnvironmentSkills(context.Background(), &EnvironmentRecord{
		EnvironmentID: "remote-env",
		ExecServerURL: execServerURL,
	}, rootURI)
	if err != nil {
		t.Fatalf("discoverRemoteEnvironmentSkills() error = %v", err)
	}
	waitEnvironmentInfoExecServerForTest(t, done)
	if len(warnings) != 0 {
		t.Fatalf("warnings = %#v, want none", warnings)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %#v", entries)
	}
	if entries[0].Name != "demo-plugin:alpha" || entries[1].Name != "demo-plugin:zeta" {
		t.Fatalf("entry names = %q, %q; want Rust namespace and name sort", entries[0].Name, entries[1].Name)
	}
	if entries[0].Path != "environment://remote-env/remote/plugin/skills/z-path/SKILL.md" {
		t.Fatalf("alpha path = %q", entries[0].Path)
	}
}

func TestRemoteEnvironmentSkillWalkWarningsLikeRust(t *testing.T) {
	warnings := remoteEnvironmentSkillWalkWarnings("file:///remote/skills", remoteFSWalkResponse{
		Errors: []remoteFSWalkError{{
			Path:    "file:///remote/skills/private",
			Message: "permission denied",
		}},
		Truncated: true,
	})
	want := []string{
		"failed to scan skill path file:///remote/skills/private: permission denied",
		"skills scan reached its traversal limit (root: file:///remote/skills)",
	}
	if len(warnings) != len(want) {
		t.Fatalf("warnings = %#v, want %#v", warnings, want)
	}
	for i := range want {
		if warnings[i] != want[i] {
			t.Fatalf("warnings[%d] = %q, want %q", i, warnings[i], want[i])
		}
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
