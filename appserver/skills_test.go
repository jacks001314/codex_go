package appserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"codex_go/config"
	"codex_go/install"
	promptctx "codex_go/prompt"
	"codex_go/systemskills"
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
	if len(response.Skills) != 1 || response.Skills[0].Enabled {
		t.Fatalf("response = %#v, want retained disabled skill like Rust", response)
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
	if len(response.Errors) != 2 {
		t.Fatalf("internal catalog errors = %#v, want invalid skill errors", response.Errors)
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
	if len(response.Skills) != 1 || response.Skills[0].Enabled {
		t.Fatalf("skills = %#v, want retained disabled skill like Rust", response.Skills)
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

func TestSkillsListUsesEachCWDBundledSkillsConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	packageDir := t.TempDir()
	resourcesDir := filepath.Join(packageDir, "codex-resources")
	skillDir := filepath.Join(resourcesDir, "skills", ".system", "sys-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(system skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: sys-skill\ndescription: Bundled system skill\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(system skill) error = %v", err)
	}
	disabledCWD := t.TempDir()
	enabledCWD := t.TempDir()
	var userConfig strings.Builder
	userConfig.WriteString("model = \"gpt-user\"\n")
	for _, tc := range []struct {
		cwd     string
		enabled bool
	}{
		{disabledCWD, false},
		{enabledCWD, true},
	} {
		if err := os.MkdirAll(filepath.Join(tc.cwd, ".git"), 0o755); err != nil {
			t.Fatalf("MkdirAll(.git) error = %v", err)
		}
		trustKey := strings.ReplaceAll(filepath.Clean(tc.cwd), `\`, `\\`)
		userConfig.WriteString("\n[projects.\"" + trustKey + "\"]\ntrust_level = \"trusted\"\n")
		dotCodex := filepath.Join(tc.cwd, ".codex")
		if err := os.MkdirAll(dotCodex, 0o755); err != nil {
			t.Fatalf("MkdirAll(.codex) error = %v", err)
		}
		contents := "[skills.bundled]\nenabled = " + strconv.FormatBool(tc.enabled) + "\n"
		if err := os.WriteFile(filepath.Join(dotCodex, "config.toml"), []byte(contents), 0o600); err != nil {
			t.Fatalf("WriteFile(project config) error = %v", err)
		}
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte(userConfig.String()), 0o600); err != nil {
		t.Fatalf("WriteFile(user config) error = %v", err)
	}
	service := NewSkillsServiceWithOptions(&SkillsServiceOptions{
		InstallContext:      &install.InstallContext{PackageLayout: &install.CodexPackageLayout{ResourcesDir: &resourcesDir}},
		IncludeDefaultRoots: true,
		Config:              config.NewConfigService(home),
	})
	response, err := service.List(&SkillsListParams{CWDs: []string{disabledCWD, enabledCWD}})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(response.Data) != 2 {
		t.Fatalf("data = %d entries, want 2 per-CWD entries", len(response.Data))
	}
	byCWD := map[string]*SkillsListEntry{}
	for i := range response.Data {
		byCWD[response.Data[i].CWD] = &response.Data[i]
	}
	disabled := byCWD[disabledCWD]
	enabled := byCWD[enabledCWD]
	if disabled == nil || enabled == nil {
		t.Fatalf("data = %#v, want entries for both cwds", response.Data)
	}
	for _, skill := range disabled.Skills {
		if skill.Scope == "system" {
			t.Fatalf("disabled cwd includes system skill %s: %#v", skill.Name, disabled.Skills)
		}
	}
	foundSystem := false
	for _, skill := range enabled.Skills {
		if skill.Scope == "system" {
			foundSystem = true
			break
		}
	}
	if !foundSystem {
		t.Fatalf("enabled cwd missing system skill: %#v", enabled.Skills)
	}
}

func TestSkillsWatcherIgnoresGeneratedSystemSkillsLikeRust(t *testing.T) {
	userRoot := t.TempDir()
	systemRoot := filepath.Join(userRoot, ".system")
	if err := os.MkdirAll(systemRoot, 0o755); err != nil {
		t.Fatalf("MkdirAll(system) error = %v", err)
	}
	service := NewSkillsServiceWithOptions(&SkillsServiceOptions{
		RootSpecs: []SkillsRoot{
			{Path: userRoot, Scope: "user"},
			{Path: systemRoot, Scope: "system"},
		},
		WatchInterval: 10 * time.Millisecond,
	})
	defer service.Close()
	changed := make(chan struct{}, 1)
	service.SetChangedCallback(func() {
		select {
		case changed <- struct{}{}:
		default:
		}
	})
	service.WatchCWDs(nil)
	if err := os.WriteFile(filepath.Join(systemRoot, "generated.txt"), []byte("generated"), 0o600); err != nil {
		t.Fatalf("WriteFile(system) error = %v", err)
	}
	select {
	case <-changed:
		t.Fatal("generated system skill change triggered watcher")
	case <-time.After(80 * time.Millisecond):
	}
	if err := os.WriteFile(filepath.Join(userRoot, "user.txt"), []byte("user"), 0o600); err != nil {
		t.Fatalf("WriteFile(user) error = %v", err)
	}
	select {
	case <-changed:
	case <-time.After(time.Second):
		t.Fatal("user skill change did not trigger watcher")
	}
}

func TestSkillsServiceLoadsUserAndAdminRootsFromConfigLayersLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	customUserFolder := t.TempDir()
	customUserConfig := filepath.Join(customUserFolder, "config.toml")
	if err := os.WriteFile(customUserConfig, []byte(""), 0o600); err != nil {
		t.Fatalf("WriteFile(user config) error = %v", err)
	}
	adminFolder := t.TempDir()
	adminConfig := filepath.Join(adminFolder, "config.toml")
	writeLayerSkill := func(folder string, name string) {
		t.Helper()
		dir := filepath.Join(folder, "skills", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, SkillFilename), []byte("---\nname: "+name+"\ndescription: "+name+"\n---\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	writeLayerSkill(customUserFolder, "custom-user-skill")
	writeLayerSkill(adminFolder, "admin-skill")
	configService := config.NewConfigService(codexHome)
	configService.SetUserConfigPath(customUserConfig)
	configService.SetManagedLayers([]config.Layer{{
		Name:   config.LayerSource{Type: config.LayerSourceSystem, File: adminConfig},
		Config: map[string]any{},
	}})

	response, err := NewSkillsServiceWithOptions(&SkillsServiceOptions{
		CodexHome:           codexHome,
		HomeDir:             t.TempDir(),
		Config:              configService,
		IncludeDefaultRoots: true,
	}).List(&SkillsListParams{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	byName := map[string]SkillsListEntry{}
	for _, skill := range response.Skills {
		byName[skill.Name] = skill
	}
	if byName["custom-user-skill"].Scope != "user" || byName["admin-skill"].Scope != "admin" {
		t.Fatalf("layer skills = %#v, want user and admin scopes", byName)
	}
}

func TestSkillsServiceInstallsEmbeddedSystemSkillsLikeRust(t *testing.T) {
	home := t.TempDir()
	service := NewSkillsServiceWithOptions(&SkillsServiceOptions{
		CodexHome:           home,
		HomeDir:             t.TempDir(),
		IncludeDefaultRoots: true,
	})
	response, err := service.List(&SkillsListParams{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	names := map[string]bool{}
	for _, skill := range response.Skills {
		if skill.Scope == "system" {
			names[skill.Name] = true
		}
	}
	for _, want := range []string{"skill-installer", "skill-creator", "plugin-creator", "imagegen", "openai-docs"} {
		if !names[want] {
			t.Fatalf("embedded system skills = %#v, missing %q", names, want)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "skills", ".system", "skill-creator", "scripts", "init_skill.py")); err != nil {
		t.Fatalf("embedded nested script missing: %v", err)
	}
}

func TestSkillsServiceDisabledBundledSkillsRemovesCacheLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := systemskills.Install(home); err != nil {
		t.Fatalf("Install() error = %v", err)
	}
	configService := config.NewConfigService(home)
	if _, err := configService.WriteValue(&config.ConfigValueWriteParams{KeyPath: "skills.bundled.enabled", Value: false, MergeStrategy: config.MergeReplace}); err != nil {
		t.Fatalf("WriteValue() error = %v", err)
	}
	service := NewSkillsServiceWithOptions(&SkillsServiceOptions{
		CodexHome:           home,
		HomeDir:             t.TempDir(),
		Config:              configService,
		IncludeDefaultRoots: true,
	})
	response, err := service.List(&SkillsListParams{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	for _, skill := range response.Skills {
		if skill.Scope == "system" {
			t.Fatalf("disabled bundled skill remained: %#v", skill)
		}
	}
	if _, err := os.Stat(filepath.Join(home, "skills", ".system")); !os.IsNotExist(err) {
		t.Fatalf("disabled bundled cache stat err=%v", err)
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
	if len(response.Skills) != 1 || response.Skills[0].Enabled {
		t.Fatalf("skills = %#v, want retained disabled skill", response.Skills)
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

func TestSkillsListDoesNotScanArbitraryCWDSubdirectoriesLikeRust(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, ".git"), []byte("gitdir: fake\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(.git) error = %v", err)
	}
	skillDir := filepath.Join(cwd, "packages", "accidental-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: accidental-skill\ndescription: should not load\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}

	response, err := NewSkillsService(nil).List(&SkillsListParams{CWDs: []string{cwd}, ForceReload: true})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(response.Data) != 1 || len(response.Data[0].Skills) != 0 {
		t.Fatalf("response = %#v, want arbitrary cwd skill ignored", response)
	}
}

func TestSkillsListBoundsAgentsRootsAtProjectRootLikeRust(t *testing.T) {
	parent := t.TempDir()
	project := filepath.Join(parent, "project")
	cwd := filepath.Join(project, "packages", "app")
	if err := os.MkdirAll(cwd, 0o755); err != nil {
		t.Fatalf("MkdirAll(cwd) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, ".git"), []byte("gitdir: fake\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(.git) error = %v", err)
	}
	writeSkill := func(root string, name string) {
		t.Helper()
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, SkillFilename), []byte("---\nname: "+name+"\ndescription: "+name+"\n---\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", name, err)
		}
	}
	writeSkill(filepath.Join(project, ".agents", "skills"), "project-agent")
	writeSkill(filepath.Join(project, "packages", ".agents", "skills"), "nested-agent")
	writeSkill(filepath.Join(parent, ".agents", "skills"), "outside-agent")

	response, err := NewSkillsService(nil).List(&SkillsListParams{CWDs: []string{cwd}, ForceReload: true})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	names := map[string]bool{}
	for _, skill := range response.Data[0].Skills {
		names[skill.Name] = true
	}
	if !names["project-agent"] || !names["nested-agent"] || names["outside-agent"] {
		t.Fatalf("agents skills = %#v, want project+nested without outside", names)
	}
}

func TestSkillsListDeduplicatesByPathPreferringRepoRootLikeRust(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, ".git"), []byte("gitdir: fake\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(.git) error = %v", err)
	}
	skillsRoot := filepath.Join(cwd, ".codex", "skills")
	skillDir := filepath.Join(skillsRoot, "shared-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: shared-skill\ndescription: shared\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}

	response, err := NewSkillsServiceWithOptions(&SkillsServiceOptions{
		RootSpecs: []SkillsRoot{{Path: skillsRoot, Scope: "user"}},
	}).List(&SkillsListParams{CWDs: []string{cwd}, ForceReload: true})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(response.Skills) != 1 || response.Skills[0].Scope != "repo" {
		t.Fatalf("skills = %#v, want one repo-preferred canonical path", response.Skills)
	}
}

func TestSkillsListKeepsDuplicateNamesAndSortsByScopeLikeRust(t *testing.T) {
	cwd := t.TempDir()
	if err := os.WriteFile(filepath.Join(cwd, ".git"), []byte("gitdir: fake\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(.git) error = %v", err)
	}
	writeNamedSkill := func(root string, dir string, description string) {
		t.Helper()
		skillDir := filepath.Join(root, dir)
		if err := os.MkdirAll(skillDir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%s) error = %v", dir, err)
		}
		if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: duplicate\ndescription: "+description+"\n---\n"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", dir, err)
		}
	}
	userRoot := t.TempDir()
	writeNamedSkill(userRoot, "user-copy", "user")
	writeNamedSkill(filepath.Join(cwd, ".codex", "skills"), "repo-copy", "repo")

	response, err := NewSkillsServiceWithOptions(&SkillsServiceOptions{
		RootSpecs: []SkillsRoot{{Path: userRoot, Scope: "user"}},
	}).List(&SkillsListParams{CWDs: []string{cwd}, ForceReload: true})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(response.Skills) != 2 || response.Skills[0].Scope != "repo" || response.Skills[1].Scope != "user" {
		t.Fatalf("skills = %#v, want duplicate names ordered repo then user", response.Skills)
	}
}

func TestSkillsListSharedAgentsRootAppliesToEachRequestedCWDLikeRust(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".git"), []byte("gitdir: fake\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(.git) error = %v", err)
	}
	firstCWD := filepath.Join(project, "first")
	secondCWD := filepath.Join(project, "second")
	if err := os.MkdirAll(firstCWD, 0o755); err != nil {
		t.Fatalf("MkdirAll(first cwd) error = %v", err)
	}
	if err := os.MkdirAll(secondCWD, 0o755); err != nil {
		t.Fatalf("MkdirAll(second cwd) error = %v", err)
	}
	skillDir := filepath.Join(project, ".agents", "skills", "shared-agent")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: shared-agent\ndescription: shared\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}

	response, err := NewSkillsService(nil).List(&SkillsListParams{CWDs: []string{firstCWD, secondCWD}, ForceReload: true})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(response.Data) != 2 || len(response.Data[0].Skills) != 1 || len(response.Data[1].Skills) != 1 {
		t.Fatalf("response = %#v, want shared ancestor skill for both cwds", response)
	}
}

func TestSkillsListLoadsRepoSkillsWhenCWDIsFileLikeRust(t *testing.T) {
	project := t.TempDir()
	if err := os.WriteFile(filepath.Join(project, ".git"), []byte("gitdir: fake\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(.git) error = %v", err)
	}
	cwdFile := filepath.Join(project, "src", "main.go")
	if err := os.MkdirAll(filepath.Dir(cwdFile), 0o755); err != nil {
		t.Fatalf("MkdirAll(src) error = %v", err)
	}
	if err := os.WriteFile(cwdFile, []byte("package main\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(cwd file) error = %v", err)
	}
	skillDir := filepath.Join(project, ".codex", "skills", "repo-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: repo-skill\ndescription: repo\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}

	response, err := NewSkillsService(nil).List(&SkillsListParams{CWDs: []string{cwdFile}, ForceReload: true})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(response.Data) != 1 || len(response.Data[0].Skills) != 1 || response.Data[0].Skills[0].Name != "repo-skill" {
		t.Fatalf("response = %#v, want repo skill for file cwd", response)
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

func TestSkillsServiceTurnConfigOverridesPersistentRulesAndCacheLikeRust(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "demo")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	skillPath := filepath.Join(skillDir, SkillFilename)
	if err := os.WriteFile(skillPath, []byte("---\nname: github:yeet\ndescription: Demo\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	home := t.TempDir()
	configService := config.NewConfigService(home)
	if _, err := configService.WriteSkillConfig(&config.SkillConfigWriteParams{Path: skillPath, Enabled: false}); err != nil {
		t.Fatalf("WriteSkillConfig() error = %v", err)
	}
	service := NewSkillsServiceWithOptions(&SkillsServiceOptions{Roots: []string{root}, Config: configService})
	disabled, err := service.List(&SkillsListParams{})
	if err != nil || len(disabled.Skills) != 1 || disabled.Skills[0].Enabled {
		t.Fatalf("persistent disabled skills = %#v err=%v", disabled, err)
	}
	sessionRules := skillConfigEntriesFromValues(map[string]any{"skills": map[string]any{"config": []any{
		map[string]any{"name": "github:yeet", "enabled": true},
	}}})
	reenabled, err := service.List(&SkillsListParams{Config: sessionRules})
	if err != nil || len(reenabled.Skills) != 1 || reenabled.Skills[0].Name != "github:yeet" {
		t.Fatalf("session re-enabled skills = %#v err=%v", reenabled, err)
	}
	disabledAgain, err := service.List(&SkillsListParams{})
	if err != nil || len(disabledAgain.Skills) != 1 || disabledAgain.Skills[0].Enabled {
		t.Fatalf("persistent cache after session override = %#v err=%v", disabledAgain, err)
	}
}

func TestSkillsServiceAppliesNameConfigToPluginEntriesLikeRust(t *testing.T) {
	home := t.TempDir()
	configService := config.NewConfigService(home)
	if _, err := configService.WriteSkillConfig(&config.SkillConfigWriteParams{Name: "Sample:sample-search", Enabled: false}); err != nil {
		t.Fatalf("WriteSkillConfig() error = %v", err)
	}
	service := NewSkillsServiceWithOptions(&SkillsServiceOptions{Config: configService})
	pluginPath := filepath.Join(t.TempDir(), "sample-search", SkillFilename)
	entries, err := service.applyConfigEntries([]SkillsListEntry{{
		Name:     "Sample:sample-search",
		Path:     pluginPath,
		Scope:    "plugin",
		Enabled:  true,
		PluginID: "sample@test",
	}}, nil)
	if err != nil {
		t.Fatalf("applyConfigEntries() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Enabled {
		t.Fatalf("plugin entries = %#v, want retained disabled plugin skill", entries)
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

func TestListSkillsPreservesEmptyPolicyLikeRust(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "empty-policy")
	if err := os.MkdirAll(filepath.Join(skillDir, SkillMetadataDir), 0o755); err != nil {
		t.Fatalf("MkdirAll(metadata) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: empty-policy\ndescription: empty policy\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillMetadataDir, SkillMetadataFilename), []byte("policy: {}\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(metadata) error = %v", err)
	}

	response, err := NewSkillsService([]string{root}).List(&SkillsListParams{})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(response.Skills) != 1 || response.Skills[0].Policy == nil || response.Skills[0].Policy.AllowImplicitInvocation != nil || len(response.Skills[0].Policy.Products) != 0 {
		t.Fatalf("skill = %#v, want preserved empty policy", response.Skills)
	}
}

func TestListSkillsInvalidProductIgnoresWholeMetadataFileLikeRust(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, "invalid-product")
	if err := os.MkdirAll(filepath.Join(skillDir, SkillMetadataDir), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: invalid-product\ndescription: demo\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}
	metadata := "interface:\n  display_name: Ignored\ndependencies:\n  tools:\n    - type: mcp\n      value: docs\npolicy:\n  products: [codex, unknown]\n"
	if err := os.WriteFile(filepath.Join(skillDir, SkillMetadataDir, SkillMetadataFilename), []byte(metadata), 0o600); err != nil {
		t.Fatalf("WriteFile(metadata) error = %v", err)
	}
	response, err := NewSkillsService([]string{root}).List(&SkillsListParams{})
	if err != nil || len(response.Skills) != 1 {
		t.Fatalf("List() response=%#v err=%v", response, err)
	}
	skill := response.Skills[0]
	if skill.Interface != nil || skill.Dependencies != nil || skill.Policy != nil {
		t.Fatalf("invalid product metadata was partially retained: %#v", skill)
	}
}

func TestRemoteSkillInvalidProductIgnoresWholeMetadataFileLikeRust(t *testing.T) {
	entry, warning, ok := remoteSkillEntryFromContents(
		"remote-env",
		"/skills/demo/SKILL.md",
		"---\nname: demo\ndescription: demo\n---\n",
		"interface:\n  display_name: Ignored\npolicy:\n  products: [Codex]\n",
		"",
	)
	if !ok || warning != "" {
		t.Fatalf("remoteSkillEntryFromContents() entry=%#v warning=%q ok=%v", entry, warning, ok)
	}
	if entry.Interface != nil || entry.Dependencies != nil || entry.Policy != nil {
		t.Fatalf("invalid remote product metadata was partially retained: %#v", entry)
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
	if skill.Policy == nil || skill.Policy.AllowImplicitInvocation != nil || len(skill.Policy.Products) != 0 {
		t.Fatalf("unknown policy fields should leave a present empty policy like Rust: %#v", skill.Policy)
	}
}

func TestDiscoverPluginSkillAllowsSharedPluginAssetsLikeRust(t *testing.T) {
	pluginRoot := filepath.Join(t.TempDir(), "plugin")
	skillDir := filepath.Join(pluginRoot, "skills", "send-message")
	if err := os.MkdirAll(filepath.Join(skillDir, SkillMetadataDir), 0o755); err != nil {
		t.Fatalf("MkdirAll(skill) error = %v", err)
	}
	if err := os.MkdirAll(filepath.Join(pluginRoot, "assets"), 0o755); err != nil {
		t.Fatalf("MkdirAll(assets) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: send-message\ndescription: send messages\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillMetadataDir, SkillMetadataFilename), []byte("interface:\n  icon_small: ../../assets/logo.svg\n  icon_large: ../../assets/logo.svg\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(metadata) error = %v", err)
	}
	entries, skillErrors, err := discover(SkillsRoot{
		Path:       filepath.Join(pluginRoot, "skills"),
		Scope:      "plugin",
		PluginID:   "plugin@test",
		PluginRoot: pluginRoot,
	})
	if err != nil || len(skillErrors) != 0 || len(entries) != 1 {
		t.Fatalf("discover() entries=%#v errors=%#v err=%v", entries, skillErrors, err)
	}
	want := filepath.Join(pluginRoot, "assets", "logo.svg")
	if entries[0].Interface == nil || entries[0].Interface.IconSmall == nil || entries[0].Interface.IconLarge == nil || *entries[0].Interface.IconSmall != want || *entries[0].Interface.IconLarge != want {
		t.Fatalf("interface = %#v, want shared asset %q", entries[0].Interface, want)
	}
}

func TestDiscoverPluginSkillRejectsSharedAssetEscapeLikeRust(t *testing.T) {
	pluginRoot := filepath.Join(t.TempDir(), "plugin")
	skillDir := filepath.Join(pluginRoot, "skills", "send-message")
	if err := os.MkdirAll(filepath.Join(skillDir, SkillMetadataDir), 0o755); err != nil {
		t.Fatalf("MkdirAll(skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillFilename), []byte("---\nname: send-message\ndescription: send messages\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(skillDir, SkillMetadataDir, SkillMetadataFilename), []byte("interface:\n  icon_small: ../../other/logo.svg\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(metadata) error = %v", err)
	}
	entries, skillErrors, err := discover(SkillsRoot{
		Path:       filepath.Join(pluginRoot, "skills"),
		Scope:      "plugin",
		PluginID:   "plugin@test",
		PluginRoot: pluginRoot,
	})
	if err != nil || len(skillErrors) != 0 || len(entries) != 1 {
		t.Fatalf("discover() entries=%#v errors=%#v err=%v", entries, skillErrors, err)
	}
	if entries[0].Interface != nil {
		t.Fatalf("interface = %#v, want nil for escaped shared asset", entries[0].Interface)
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

func TestRemoteSkillMetadataIgnoresInterfaceLikeRust(t *testing.T) {
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
	if entry.Interface != nil {
		t.Fatalf("remote environment interface = %#v, want nil", entry.Interface)
	}
}

func TestParseSkillFrontmatterIgnoresModelAnnotationLikeRust(t *testing.T) {
	// Rust #39068 removed skill model delegation: the `model` frontmatter
	// field is no longer parsed or exposed.
	tests := []struct {
		contents    string
		description string
	}{
		{"---\nname: demo\ndescription: Demo skill\nmodel: luna\n---\n", "Demo skill"},
		{"---\nname: demo\ndescription: Demo skill\nmodel: terra\n---\n", "Demo skill"},
		{"---\nname: demo\ndescription: Demo skill\n---\n", "Demo skill"},
		{"---\nname: demo\ndescription: Build for AWS: ECS\nmodel: luna\n---\n", "Build for AWS: ECS"},
	}
	for _, test := range tests {
		contents := test.contents
		parsed, ok := parseSkillFrontmatter(contents, "fallback")
		if !ok {
			t.Fatalf("parseSkillFrontmatter(%q) failed", contents)
		}
		if parsed.Name != "demo" || parsed.Description != test.description {
			t.Fatalf("model-annotated frontmatter parsed as %#v", parsed)
		}
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

func TestRemotePluginManifestPathClassificationKeepsLegacyRootsAndAgentPriority(t *testing.T) {
	tests := []struct {
		path      string
		wantRoot  string
		wantAgent bool
	}{
		{"file:///remote/plugin/plugin.json", "file:///remote/plugin", true},
		{"file:///remote/plugin/.codex-plugin/plugin.json", "file:///remote/plugin", false},
		{"file:///remote/plugin/.claude-plugin/plugin.json", "file:///remote/plugin", false},
		{"file:///remote/plugin/.cursor-plugin/plugin.json", "file:///remote/plugin", false},
	}
	for _, tc := range tests {
		root, _, agentManifest, ok := remotePluginRootFromManifestPath(tc.path)
		if !ok || root != tc.wantRoot || agentManifest != tc.wantAgent {
			t.Fatalf("remotePluginRootFromManifestPath(%q) = %q, agent=%v, ok=%v", tc.path, root, agentManifest, ok)
		}
	}
}

type remotePluginTestCaller struct {
	files map[string]string
}

func (c remotePluginTestCaller) Call(_ context.Context, _ int, method string, params any) (json.RawMessage, error) {
	values, _ := params.(map[string]any)
	path, _ := values["path"].(string)
	switch method {
	case "fs/getMetadata":
		_, ok := c.files[path]
		return json.Marshal(map[string]any{"isFile": ok})
	case "fs/readFile":
		contents, ok := c.files[path]
		if !ok {
			return nil, fmt.Errorf("missing test file %s", path)
		}
		return json.Marshal(map[string]any{"dataBase64": base64.StdEncoding.EncodeToString([]byte(contents))})
	default:
		return nil, fmt.Errorf("unsupported method %s", method)
	}
}

func TestRemotePluginNamespaceSupportsAgentCursorAndLegacyFallbackLikeRust(t *testing.T) {
	root := "file:///remote/plugin"
	tests := []struct {
		name      string
		files     map[string]string
		wantName  string
		wantAgent bool
		wantOK    bool
	}{
		{
			name:     "agent root",
			files:    map[string]string{remoteJoin(root, "plugin.json"): `{"$schema":"https://agent-plugins.org/schemas/1.0.0/plugin.schema.json","name":"agent-demo"}`},
			wantName: "agent-demo", wantAgent: true, wantOK: true,
		},
		{
			name: "unrelated root falls back",
			files: map[string]string{
				remoteJoin(root, "plugin.json"):               `{"name":"npm-package"}`,
				remoteJoin(root, ".codex-plugin/plugin.json"): `{"name":"legacy-demo"}`,
			},
			wantName: "legacy-demo", wantOK: true,
		},
		{
			name:     "cursor legacy",
			files:    map[string]string{remoteJoin(root, ".cursor-plugin/plugin.json"): `{"name":"cursor-demo"}`},
			wantName: "cursor-demo", wantOK: true,
		},
		{
			name: "unsupported agent schema blocks root",
			files: map[string]string{
				remoteJoin(root, "plugin.json"):               `{"$schema":"https://agent-plugins.org/schemas/2.0.0/plugin.schema.json","name":"future"}`,
				remoteJoin(root, ".codex-plugin/plugin.json"): `{"name":"legacy-demo"}`,
			},
			wantOK: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			nextID := 1
			descriptor, ok := remotePluginNamespaceForRoot(context.Background(), remotePluginTestCaller{files: tc.files}, &nextID, root)
			if ok != tc.wantOK || descriptor.Name != tc.wantName || descriptor.Agent != tc.wantAgent {
				t.Fatalf("descriptor = %#v, ok=%v", descriptor, ok)
			}
		})
	}
}

func TestRemoteAgentPluginOnlyAcceptsDirectChildSkillsLikeRust(t *testing.T) {
	root := "file:///remote/plugin"
	if !remoteAgentPluginDirectChildSkill(root, "file:///remote/plugin/skills/direct/SKILL.md") {
		t.Fatal("direct child skill rejected")
	}
	for _, path := range []string{
		"file:///remote/plugin/skills/group/nested/SKILL.md",
		"file:///remote/other/skills/direct/SKILL.md",
		"file:///remote/plugin/SKILL.md",
	} {
		if remoteAgentPluginDirectChildSkill(root, path) {
			t.Fatalf("non-direct skill accepted: %s", path)
		}
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

func TestSkillsListEntryMarshalOmitsInternalPluginIDLikeRust(t *testing.T) {
	payload, err := json.Marshal(&SkillsListEntry{
		Name:           "review",
		Description:    "Review with plugin context",
		Path:           "/plugins/docs/skills/review/SKILL.md",
		Scope:          "plugin",
		Enabled:        true,
		PluginID:       "docs@market",
		RemotePluginID: "plugins~Plugin_docs",
	})
	if err != nil {
		t.Fatalf("Marshal skill entry error = %v", err)
	}
	var values map[string]any
	if err := json.Unmarshal(payload, &values); err != nil {
		t.Fatalf("Unmarshal skill entry error = %v", err)
	}
	if _, ok := values["pluginId"]; ok {
		t.Fatalf("pluginId is not part of Rust SkillMetadata: %s", payload)
	}
	if _, ok := values["remotePluginId"]; ok {
		t.Fatalf("remotePluginId is internal skill metadata: %s", payload)
	}
}

func TestSkillsListSymlinkedSkillPreservesDiscoveryPathLikeRust(t *testing.T) {
	base := t.TempDir()
	realRoot := filepath.Join(base, "real-skills")
	realSkill := filepath.Join(realRoot, "demo")
	if err := os.MkdirAll(realSkill, 0o755); err != nil {
		t.Fatalf("MkdirAll(real skill) error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(realSkill, SkillFilename), []byte("---\nname: demo\ndescription: demo skill\n---\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(real skill) error = %v", err)
	}
	aliasRoot := filepath.Join(base, "linked-skills")
	if err := os.Symlink(realRoot, aliasRoot); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	response, err := NewSkillsServiceWithOptions(&SkillsServiceOptions{
		RootSpecs: []SkillsRoot{{Path: aliasRoot, Scope: "user"}},
	}).List(&SkillsListParams{ForceReload: true})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(response.Skills) != 1 {
		t.Fatalf("skills = %#v, want exactly one discovered through the alias", response.Skills)
	}
	entry := response.Skills[0]
	wantCanonical := filepath.ToSlash(filepath.Clean(filepath.Join(realSkill, SkillFilename)))
	if entry.Path != wantCanonical {
		t.Fatalf("Path = %q, want canonical identity %q (Rust 72d937ed4d)", entry.Path, wantCanonical)
	}
	wantDiscovery := filepath.ToSlash(filepath.Clean(filepath.Join(aliasRoot, "demo", SkillFilename)))
	if entry.DiscoveryPath != wantDiscovery {
		t.Fatalf("DiscoveryPath = %q, want %q (Rust 72d937ed4d)", entry.DiscoveryPath, wantDiscovery)
	}
}

func TestPromptSkillMetadataAcceptsCanonicalAndDiscoveryPathsLikeRust(t *testing.T) {
	metadata := promptSkillMetadataFromEntries([]SkillsListEntry{{
		Name: "demo", Path: "/real/demo/SKILL.md", DiscoveryPath: "/linked/demo/SKILL.md",
		Enabled: true, Scope: "user",
	}})
	if len(metadata) != 1 {
		t.Fatalf("metadata = %#v, want 1", metadata)
	}
	if metadata[0].LocatorPath != "/linked/demo/SKILL.md" {
		t.Fatalf("LocatorPath = %q, want discovery path (Rust 72d937ed4d)", metadata[0].LocatorPath)
	}
	collect := func(path string) []promptctx.InstructionsSkillMetadata {
		return promptctx.CollectExplicitSkillMentions(&promptctx.ExplicitSkillMentionOptions{
			Inputs: []promptctx.SkillMentionInput{{Type: "skill", Path: path}},
			Skills: metadata,
		})
	}
	if selected := collect("/linked/demo/SKILL.md"); len(selected) != 1 {
		t.Fatalf("mention via discovery path selected %d skills, want 1", len(selected))
	}
	if selected := collect("/real/demo/SKILL.md"); len(selected) != 1 {
		t.Fatalf("mention via canonical path selected %d skills, want 1", len(selected))
	}
}
