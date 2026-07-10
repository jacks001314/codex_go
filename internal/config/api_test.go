package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/internal/sandbox"
)

func TestConfigReadResponseMarshalRustShape(t *testing.T) {
	response := &ConfigReadResponse{
		Config: map[string]any{
			"model": "gpt-5",
			"sandbox_workspace_write": map[string]any{
				"writable_roots": []any{"D:\\work"},
			},
			"tools": map[string]any{
				"web_search": map[string]any{
					"context_size": "low",
					"location": map[string]any{
						"city": "Hong Kong",
					},
				},
			},
			"analytics": map[string]any{"channel": "desktop"},
			"apps": map[string]any{
				"_default": map[string]any{"approvals_reviewer": "auto_review"},
				"app1":     map[string]any{"enabled": false},
			},
		},
		Origins: map[string]LayerMetadata{},
	}

	data, err := json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal ConfigReadResponse error = %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("Unmarshal ConfigReadResponse error = %v", err)
	}
	if _, ok := root["layers"]; ok {
		t.Fatalf("layers present for nil layers: %s", data)
	}
	configMap := root["config"].(map[string]any)
	for _, key := range []string{"review_model", "approval_policy", "apps", "desktop"} {
		if _, ok := configMap[key]; !ok {
			t.Fatalf("config missing required nullable key %q: %s", key, data)
		}
	}
	sandboxConfig := configMap["sandbox_workspace_write"].(map[string]any)
	if sandboxConfig["network_access"] != false || sandboxConfig["exclude_tmpdir_env_var"] != false || sandboxConfig["exclude_slash_tmp"] != false {
		t.Fatalf("sandbox defaults = %+v", sandboxConfig)
	}
	webSearch := configMap["tools"].(map[string]any)["web_search"].(map[string]any)
	if _, ok := webSearch["allowed_domains"]; !ok || webSearch["allowed_domains"] != nil {
		t.Fatalf("web_search allowed_domains = %+v", webSearch)
	}
	location := webSearch["location"].(map[string]any)
	if location["country"] != nil || location["region"] != nil || location["timezone"] != nil || location["city"] != "Hong Kong" {
		t.Fatalf("web_search location = %+v", location)
	}
	analytics := configMap["analytics"].(map[string]any)
	if analytics["enabled"] != nil || analytics["channel"] != "desktop" {
		t.Fatalf("analytics = %+v", analytics)
	}
	apps := configMap["apps"].(map[string]any)
	defaults := apps["_default"].(map[string]any)
	if defaults["enabled"] != true || defaults["destructive_enabled"] != true || defaults["open_world_enabled"] != true || defaults["default_tools_approval_mode"] != nil {
		t.Fatalf("apps default = %+v", defaults)
	}
	app := apps["app1"].(map[string]any)
	if app["enabled"] != false || app["approvals_reviewer"] != nil || app["tools"] != nil {
		t.Fatalf("app config = %+v", app)
	}

	response.Layers = []Layer{{
		Name:    LayerSource{Type: LayerSourceUser, File: "D:\\codex\\config.toml"},
		Version: "v1",
		Config:  map[string]any{"model": "gpt-5"},
	}}
	data, err = json.Marshal(response)
	if err != nil {
		t.Fatalf("Marshal ConfigReadResponse with layers error = %v", err)
	}
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("Unmarshal ConfigReadResponse with layers error = %v", err)
	}
	layers := root["layers"].([]any)
	layer := layers[0].(map[string]any)
	if _, ok := layer["disabledReason"]; ok {
		t.Fatalf("disabledReason should be omitted when nil: %+v", layer)
	}
	name := layer["name"].(map[string]any)
	if name["type"] != "user" || name["file"] != "D:\\codex\\config.toml" || name["profile"] != nil {
		t.Fatalf("layer source = %+v", name)
	}
}

func TestConfigRequirementsMarshalRustShape(t *testing.T) {
	model := "gpt-5"
	requirements := &ConfigRequirements{
		AllowedApprovalPolicies: []sandbox.AskForApproval{},
		AllowedSandboxModes:     []sandbox.SandboxMode{sandbox.SandboxWorkspaceWrite},
		ComputerUse:             &ComputerUseRequirements{},
		Hooks: &ManagedHooksRequirements{
			PreToolUse: []ConfiguredHookGroup{{
				Hooks: []ConfiguredHookHandler{{Type: "command", Command: "echo ok"}},
			}},
		},
		Network: &NetworkRequirements{
			AllowedDomains: []string{},
			Domains:        map[string]NetworkPermission{"example.com": NetworkAllow},
		},
		Models: &ModelsRequirements{NewThread: &NewThreadModelDefaults{Model: &model}},
	}
	data, err := json.Marshal(requirements)
	if err != nil {
		t.Fatalf("Marshal ConfigRequirements error = %v", err)
	}
	var root map[string]any
	if err := json.Unmarshal(data, &root); err != nil {
		t.Fatalf("Unmarshal ConfigRequirements error = %v", err)
	}
	if policies, ok := root["allowedApprovalPolicies"].([]any); !ok || len(policies) != 0 {
		t.Fatalf("allowedApprovalPolicies = %+v", root["allowedApprovalPolicies"])
	}
	for _, key := range []string{"allowedApprovalsReviewers", "allowedPermissionProfiles", "defaultPermissions", "hooks", "network"} {
		if _, ok := root[key]; !ok {
			t.Fatalf("requirements missing key %q: %s", key, data)
		}
	}
	computerUse := root["computerUse"].(map[string]any)
	if computerUse["allowLockedComputerUse"] != nil {
		t.Fatalf("computerUse = %+v", computerUse)
	}
	network := root["network"].(map[string]any)
	if _, ok := network["allowedDomains"].([]any); !ok {
		t.Fatalf("network allowedDomains = %+v", network["allowedDomains"])
	}
	if network["httpPort"] != nil || network["allowLocalBinding"] != nil {
		t.Fatalf("network nullable fields = %+v", network)
	}
	models := root["models"].(map[string]any)
	newThread := models["newThread"].(map[string]any)
	if newThread["model"] != "gpt-5" || newThread["modelReasoningEffort"] != nil || newThread["serviceTier"] != nil {
		t.Fatalf("models.newThread = %+v", newThread)
	}
	hooks := root["hooks"].(map[string]any)
	preToolUse := hooks["PreToolUse"].([]any)
	command := preToolUse[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)
	if command["type"] != "command" || command["commandWindows"] != nil || command["timeoutSec"] != nil || command["statusMessage"] != nil || command["async"] != false {
		t.Fatalf("command hook = %+v", command)
	}
}

func TestLayerSourcePrecedence(t *testing.T) {
	profile := "work"
	cases := []struct {
		source LayerSource
		want   int16
	}{
		{LayerSource{Type: LayerSourceMDM}, 0},
		{LayerSource{Type: LayerSourceSystem}, 10},
		{LayerSource{Type: LayerSourceEnterpriseManaged}, 15},
		{LayerSource{Type: LayerSourceUser}, 20},
		{LayerSource{Type: LayerSourceUser, Profile: &profile}, 21},
		{LayerSource{Type: LayerSourceProject}, 25},
		{LayerSource{Type: LayerSourceSessionFlags}, 30},
		{LayerSource{Type: LayerSourceLegacyManagedConfigFromFile}, 40},
		{LayerSource{Type: LayerSourceLegacyManagedConfigFromMDM}, 50},
	}
	for i := range cases {
		if got := cases[i].source.Precedence(); got != cases[i].want {
			t.Fatalf("case %d Precedence() = %d, want %d", i, got, cases[i].want)
		}
	}
}

func TestServiceReadConfigWithLayersAndOrigins(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "model = \"gpt-5\"\n[features]\nweb_search = true\n")
	service := NewConfigService(home)

	response, err := service.Read(&ConfigReadParams{IncludeLayers: true})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if response.Config["model"] != "gpt-5" {
		t.Fatalf("model = %v, want gpt-5", response.Config["model"])
	}
	features := response.Config["features"].(map[string]any)
	if features["web_search"] != true {
		t.Fatalf("features = %+v, want web_search true", features)
	}
	if _, ok := response.Origins["features.web_search"]; !ok {
		t.Fatalf("origins = %+v, missing features.web_search", response.Origins)
	}
	if len(response.Layers) != 1 || response.Layers[0].Name.Type != LayerSourceUser {
		t.Fatalf("layers = %+v", response.Layers)
	}
}

func TestServiceReadToolsAndAppsOriginsMatchRustConfigRPC(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `
model = "gpt-user"

[tools.web_search]
context_size = "low"
allowed_domains = ["example.com"]

[apps._default]
approvals_reviewer = "auto_review"
default_tools_approval_mode = "approve"

[apps.app1]
enabled = false
approvals_reviewer = "user"
destructive_enabled = false
default_tools_approval_mode = "prompt"
`)
	service := NewConfigService(home)

	response, err := service.Read(&ConfigReadParams{IncludeLayers: true})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	tools := response.Config["tools"].(map[string]any)
	webSearch := tools["web_search"].(map[string]any)
	if webSearch["context_size"] != "low" {
		t.Fatalf("tools.web_search.context_size = %#v, want low", webSearch["context_size"])
	}
	allowedDomains := webSearch["allowed_domains"].([]any)
	if len(allowedDomains) != 1 || allowedDomains[0] != "example.com" {
		t.Fatalf("tools.web_search.allowed_domains = %#v", allowedDomains)
	}
	apps := response.Config["apps"].(map[string]any)
	defaultApp := apps["_default"].(map[string]any)
	if defaultApp["approvals_reviewer"] != "auto_review" || defaultApp["default_tools_approval_mode"] != "approve" {
		t.Fatalf("apps._default = %#v", defaultApp)
	}
	app1 := apps["app1"].(map[string]any)
	if app1["enabled"] != false || app1["approvals_reviewer"] != "user" || app1["destructive_enabled"] != false || app1["default_tools_approval_mode"] != "prompt" {
		t.Fatalf("apps.app1 = %#v", app1)
	}
	for _, key := range []string{
		"tools.web_search.context_size",
		"tools.web_search.allowed_domains.0",
		"apps._default.approvals_reviewer",
		"apps._default.default_tools_approval_mode",
		"apps.app1.enabled",
		"apps.app1.approvals_reviewer",
		"apps.app1.destructive_enabled",
		"apps.app1.default_tools_approval_mode",
	} {
		origin, ok := response.Origins[key]
		if !ok {
			t.Fatalf("origin %s missing in %+v", key, response.Origins)
		}
		if origin.Name.Type != LayerSourceUser {
			t.Fatalf("origin %s = %+v, want user layer", key, origin)
		}
	}
	if len(response.Layers) != 1 || response.Layers[0].Name.Type != LayerSourceUser {
		t.Fatalf("layers = %+v", response.Layers)
	}
}

func TestServiceWriteValueAndBatchWrite(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "model = \"gpt-5\"\n[features]\nweb_search = false\n")
	service := NewConfigService(home)
	read, err := service.Read(&ConfigReadParams{})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	version := read.Layers
	_ = version

	response, err := service.WriteValue(&ConfigValueWriteParams{
		KeyPath:       "features.web_search",
		Value:         true,
		MergeStrategy: MergeReplace,
	})
	if err != nil {
		t.Fatalf("WriteValue() error = %v", err)
	}
	if response.Status != WriteOK || response.FilePath != filepath.Join(home, "config.toml") {
		t.Fatalf("WriteValue() = %+v", response)
	}
	loaded, err := service.Read(&ConfigReadParams{})
	if err != nil {
		t.Fatalf("Read(after write) error = %v", err)
	}
	if loaded.Config["features"].(map[string]any)["web_search"] != true {
		t.Fatalf("config after write = %+v", loaded.Config)
	}

	if _, err := service.BatchWrite(&ConfigBatchWriteParams{Edits: []ConfigEdit{
		{KeyPath: "model", Value: "gpt-5.1", MergeStrategy: MergeReplace},
		{KeyPath: "tools", Value: map[string]any{"web_search": "enabled"}, MergeStrategy: MergeUpsert},
	}}); err != nil {
		t.Fatalf("BatchWrite() error = %v", err)
	}
	loaded, err = service.Read(&ConfigReadParams{})
	if err != nil {
		t.Fatalf("Read(after batch) error = %v", err)
	}
	if loaded.Config["model"] != "gpt-5.1" || loaded.Config["tools"].(map[string]any)["web_search"] != "enabled" {
		t.Fatalf("config after batch = %+v", loaded.Config)
	}
}

func TestServiceWriteValueNilDeletesKeyPath(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "[tui.keymap.global]\nopen_external_editor = \"ctrl-e\"\n")
	service := NewConfigService(home)

	if _, err := service.WriteValue(&ConfigValueWriteParams{
		KeyPath: "tui.keymap.global.open_external_editor",
		Value:   nil,
	}); err != nil {
		t.Fatalf("WriteValue(delete) error = %v", err)
	}
	loaded, err := service.Read(&ConfigReadParams{})
	if err != nil {
		t.Fatalf("Read(after delete) error = %v", err)
	}
	if tuiConfig, ok := loaded.Config["tui"].(map[string]any); ok {
		if keymap, ok := tuiConfig["keymap"].(map[string]any); ok && len(keymap) != 0 {
			t.Fatalf("keymap after delete = %#v, want empty/pruned", keymap)
		}
	}
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(config) error = %v", err)
	}
	if strings.Contains(string(data), "open_external_editor") {
		t.Fatalf("config still contains deleted key:\n%s", data)
	}
}

func TestServiceWriteSkillConfig(t *testing.T) {
	home := t.TempDir()
	service := NewConfigService(home)
	skillPath := filepath.Join(home, "skills", "demo", "SKILL.md")
	response, err := service.WriteSkillConfig(&SkillConfigWriteParams{Path: skillPath, Enabled: false})
	if err != nil {
		t.Fatalf("WriteSkillConfig(disable) error = %v", err)
	}
	if response.Status != WriteOK || response.FilePath != filepath.Join(home, "config.toml") {
		t.Fatalf("WriteSkillConfig(disable) = %+v", response)
	}
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(config) error = %v", err)
	}
	if text := string(data); !strings.Contains(text, "[[skills.config]]") || !strings.Contains(text, "enabled = false") || !strings.Contains(text, "SKILL.md") {
		t.Fatalf("config.toml = %s", text)
	}

	if _, err := service.WriteSkillConfig(&SkillConfigWriteParams{Path: skillPath, Enabled: true}); err != nil {
		t.Fatalf("WriteSkillConfig(enable) error = %v", err)
	}
	data, err = os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(config after enable) error = %v", err)
	}
	if strings.Contains(string(data), "skills.config") {
		t.Fatalf("config.toml after enable = %s", data)
	}
}

func TestServiceManagedLayersOverrideUserConfig(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "model = \"gpt-user\"\n")
	service := NewConfigService(home)
	service.SetManagedLayers([]Layer{{
		Name:    LayerSource{Type: LayerSourceLegacyManagedConfigFromFile, File: filepath.Join(home, "managed_config.toml")},
		Version: "managed-v1",
		Config:  map[string]any{"model": "gpt-managed"},
	}})

	read, err := service.Read(&ConfigReadParams{IncludeLayers: true})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.Config["model"] != "gpt-managed" {
		t.Fatalf("model = %v, want managed", read.Config["model"])
	}
	if read.Origins["model"].Name.Type != LayerSourceLegacyManagedConfigFromFile {
		t.Fatalf("origin = %+v", read.Origins["model"])
	}
	if len(read.Layers) != 2 || read.Layers[1].Name.Type != LayerSourceLegacyManagedConfigFromFile {
		t.Fatalf("layers = %+v", read.Layers)
	}
}

func TestNewConfigServiceLoadsManagedConfigFromAppServerEnvLikeRust(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, `
model = "gpt-user"
approval_policy = "on-request"
`)
	managedPath := filepath.Join(t.TempDir(), "managed_config.toml")
	if err := os.WriteFile(managedPath, []byte(`
model = "gpt-managed"
approval_policy = "never"
`), 0o600); err != nil {
		t.Fatalf("WriteFile managed config error = %v", err)
	}
	t.Setenv(appServerManagedConfigPathEnv, managedPath)

	service := NewConfigService(home)
	read, err := service.Read(&ConfigReadParams{IncludeLayers: true})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.Config["model"] != "gpt-managed" || read.Config["approval_policy"] != "never" {
		t.Fatalf("config = %+v, want managed model and approval policy", read.Config)
	}
	if read.Origins["model"].Name.Type != LayerSourceLegacyManagedConfigFromFile || read.Origins["model"].Name.File != managedPath {
		t.Fatalf("model origin = %+v, want managed file", read.Origins["model"])
	}
	if len(read.Layers) != 2 || read.Layers[0].Name.Type != LayerSourceUser || read.Layers[1].Name.Type != LayerSourceLegacyManagedConfigFromFile {
		t.Fatalf("layers = %+v, want user then managed file", read.Layers)
	}
}

func TestServiceReadIncludesProjectConfigForCWD(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	writeConfig(t, home, "model = \"gpt-user\"\nmodel_provider = \"openai\"\n"+trustedProjectConfig(project)+"\n")
	if err := os.MkdirAll(filepath.Join(project, ".codex"), 0o755); err != nil {
		t.Fatalf("MkdirAll project .codex error = %v", err)
	}
	if err := os.WriteFile(ProjectConfigPath(project), []byte("model = \"gpt-project\"\nmodel_provider = \"attacker\"\nmodel_instructions_file = \"instructions.md\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile project config error = %v", err)
	}
	service := NewConfigService(home)

	read, err := service.Read(&ConfigReadParams{IncludeLayers: true, CWD: &project})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.Config["model"] != "gpt-project" || read.Config["model_provider"] != "openai" {
		t.Fatalf("config = %+v", read.Config)
	}
	wantInstructions := filepath.Join(project, ".codex", "instructions.md")
	if read.Config["model_instructions_file"] != wantInstructions {
		t.Fatalf("model_instructions_file = %#v, want %q", read.Config["model_instructions_file"], wantInstructions)
	}
	modelOrigin := read.Origins["model"]
	if modelOrigin.Name.Type != LayerSourceProject || modelOrigin.Name.DotCodexFolder != filepath.Join(project, ".codex") {
		t.Fatalf("model origin = %+v", modelOrigin)
	}
	if read.Origins["model_provider"].Name.Type != LayerSourceUser {
		t.Fatalf("model_provider origin = %+v", read.Origins["model_provider"])
	}
	if len(read.Layers) != 2 || read.Layers[0].Name.Type != LayerSourceUser || read.Layers[1].Name.Type != LayerSourceProject {
		t.Fatalf("layers = %+v", read.Layers)
	}
	projectLayerConfig := read.Layers[1].Config.(map[string]any)
	if _, ok := projectLayerConfig["model_provider"]; ok {
		t.Fatalf("project layer retained unsupported model_provider: %+v", projectLayerConfig)
	}
}

func TestServiceReadIncludesEmptyProjectLayerForDotCodexWithoutConfig(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	writeConfig(t, home, "model = \"gpt-user\"\n"+trustedProjectConfig(project)+"\n")
	if err := os.MkdirAll(filepath.Join(project, ".codex"), 0o755); err != nil {
		t.Fatalf("MkdirAll project .codex error = %v", err)
	}
	service := NewConfigService(home)

	read, err := service.Read(&ConfigReadParams{IncludeLayers: true, CWD: &project})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.Config["model"] != "gpt-user" {
		t.Fatalf("config = %+v", read.Config)
	}
	if len(read.Layers) != 2 || read.Layers[1].Name.Type != LayerSourceProject {
		t.Fatalf("layers = %+v", read.Layers)
	}
	if read.Layers[1].Name.DotCodexFolder != filepath.Join(project, ".codex") {
		t.Fatalf("project layer source = %+v", read.Layers[1].Name)
	}
	projectConfig, ok := read.Layers[1].Config.(map[string]any)
	if !ok || len(projectConfig) != 0 {
		t.Fatalf("project layer config = %#v, want empty map", read.Layers[1].Config)
	}
}

func TestServiceManagedLayersOverrideProjectConfig(t *testing.T) {
	home := t.TempDir()
	project := t.TempDir()
	writeConfig(t, home, "model = \"gpt-user\"\n"+trustedProjectConfig(project)+"\n")
	if err := os.MkdirAll(filepath.Join(project, ".codex"), 0o755); err != nil {
		t.Fatalf("MkdirAll project .codex error = %v", err)
	}
	if err := os.WriteFile(ProjectConfigPath(project), []byte("model = \"gpt-project\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile project config error = %v", err)
	}
	service := NewConfigService(home)
	service.SetManagedLayers([]Layer{{
		Name:    LayerSource{Type: LayerSourceLegacyManagedConfigFromFile, File: filepath.Join(home, "managed_config.toml")},
		Version: "managed-v1",
		Config:  map[string]any{"model": "gpt-managed"},
	}})

	read, err := service.Read(&ConfigReadParams{IncludeLayers: true, CWD: &project})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.Config["model"] != "gpt-managed" {
		t.Fatalf("model = %v, want managed", read.Config["model"])
	}
	if read.Origins["model"].Name.Type != LayerSourceLegacyManagedConfigFromFile {
		t.Fatalf("origin = %+v", read.Origins["model"])
	}
	if len(read.Layers) != 3 || read.Layers[1].Name.Type != LayerSourceProject || read.Layers[2].Name.Type != LayerSourceLegacyManagedConfigFromFile {
		t.Fatalf("layers = %+v", read.Layers)
	}
}

func TestServiceWriteReportsOverriddenByManagedLayer(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "model = \"gpt-user\"\n")
	service := NewConfigService(home)
	service.SetManagedLayers([]Layer{{
		Name:    LayerSource{Type: LayerSourceLegacyManagedConfigFromFile, File: filepath.Join(home, "managed_config.toml")},
		Version: "managed-v1",
		Config:  map[string]any{"model": "gpt-managed"},
	}})

	response, err := service.WriteValue(&ConfigValueWriteParams{KeyPath: "model", Value: "gpt-written"})
	if err != nil {
		t.Fatalf("WriteValue() error = %v", err)
	}
	if response.Status != WriteOKOverridden || response.OverriddenMetadata == nil {
		t.Fatalf("response = %+v", response)
	}
	if response.OverriddenMetadata.EffectiveValue != "gpt-managed" {
		t.Fatalf("overridden metadata = %+v", response.OverriddenMetadata)
	}
}

func TestServiceBatchWritePreservesQuotedHookStateKeys(t *testing.T) {
	home := t.TempDir()
	service := NewConfigService(home)
	hookKey := `file:C:\Users\me\.codex\hooks.json:pre_tool_use:0:0`
	_, err := service.BatchWrite(&ConfigBatchWriteParams{Edits: []ConfigEdit{{
		KeyPath: "hooks.state",
		Value: map[string]any{
			hookKey: map[string]any{
				"enabled":      false,
				"trusted_hash": "sha256:abc",
			},
		},
		MergeStrategy: MergeUpsert,
	}}})
	if err != nil {
		t.Fatalf("BatchWrite() error = %v", err)
	}
	read, err := service.Read(&ConfigReadParams{})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	state := read.Config["hooks"].(map[string]any)["state"].(map[string]any)[hookKey].(map[string]any)
	if state["enabled"] != false || state["trusted_hash"] != "sha256:abc" {
		t.Fatalf("state = %+v", state)
	}
}

func TestServiceProfileReadAndWriteUseSelectedConfigFile(t *testing.T) {
	home := t.TempDir()
	writeConfig(t, home, "model = \"gpt-main\"\n[features]\nshell_tool = true\n")
	if err := os.WriteFile(ProfileConfigPath(home, "work"), []byte("model = \"gpt-work\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile profile config error = %v", err)
	}
	service := NewProfileConfigService(home, "work")

	read, err := service.Read(&ConfigReadParams{IncludeLayers: true})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if read.Config["model"] != "gpt-work" {
		t.Fatalf("profile model = %v", read.Config["model"])
	}
	if read.Config["features"].(map[string]any)["shell_tool"] != true {
		t.Fatalf("base feature not preserved in effective read: %+v", read.Config)
	}
	if len(read.Layers) != 1 || read.Layers[0].Name.Profile == nil || *read.Layers[0].Name.Profile != "work" {
		t.Fatalf("layers = %+v", read.Layers)
	}

	response, err := service.WriteValue(&ConfigValueWriteParams{
		KeyPath:       "model",
		Value:         "gpt-updated-work",
		MergeStrategy: MergeReplace,
	})
	if err != nil {
		t.Fatalf("WriteValue() error = %v", err)
	}
	if response.FilePath != ProfileConfigPath(home, "work") {
		t.Fatalf("FilePath = %q, want profile config", response.FilePath)
	}
	profileData, err := os.ReadFile(ProfileConfigPath(home, "work"))
	if err != nil {
		t.Fatalf("ReadFile profile config error = %v", err)
	}
	if string(profileData) != "model = \"gpt-updated-work\"\n" {
		t.Fatalf("profile config = %q", string(profileData))
	}
	mainData, err := os.ReadFile(ConfigPath(home))
	if err != nil {
		t.Fatalf("ReadFile main config error = %v", err)
	}
	if !strings.Contains(string(mainData), `model = "gpt-main"`) {
		t.Fatalf("main config was changed: %q", string(mainData))
	}
}

func TestServiceWriteValidation(t *testing.T) {
	service := NewConfigService(t.TempDir())
	if read, err := service.Read(nil); err != nil || read == nil {
		t.Fatalf("Read(nil) = %+v/%v, want default read", read, err)
	}
	_, err := service.WriteValue(nil)
	if !errors.Is(err, ErrInvalidConfigRequest) {
		t.Fatalf("WriteValue(nil) error = %v, want ErrInvalidConfigRequest", err)
	}
	_, err = service.BatchWrite(nil)
	if !errors.Is(err, ErrInvalidConfigRequest) {
		t.Fatalf("BatchWrite(nil) error = %v, want ErrInvalidConfigRequest", err)
	}
	_, err = service.WriteValue(&ConfigValueWriteParams{Value: true})
	if !errors.Is(err, ErrInvalidConfigRequest) {
		t.Fatalf("WriteValue(empty key) error = %v, want ErrInvalidConfigRequest", err)
	}
	relative := "config.toml"
	_, err = service.WriteValue(&ConfigValueWriteParams{KeyPath: "model", Value: "gpt-5", FilePath: &relative})
	if !errors.Is(err, ErrInvalidConfigRequest) {
		t.Fatalf("WriteValue(relative path) error = %v, want ErrInvalidConfigRequest", err)
	}
	otherPath := filepath.Join(t.TempDir(), "other-config.toml")
	_, err = service.WriteValue(&ConfigValueWriteParams{KeyPath: "model", Value: "gpt-5", FilePath: &otherPath})
	if !errors.Is(err, ErrInvalidConfigRequest) || configWriteErrorCode(err) != ConfigWriteLayerReadonly {
		t.Fatalf("WriteValue(other path) error = %v, code = %s, want %s", err, configWriteErrorCode(err), ConfigWriteLayerReadonly)
	}
	if _, statErr := os.Stat(otherPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("other config path stat error = %v, want not written", statErr)
	}
	staleVersion := "sha256:stale"
	_, err = service.WriteValue(&ConfigValueWriteParams{KeyPath: "model", Value: "gpt-5", ExpectedVersion: &staleVersion})
	if !errors.Is(err, ErrInvalidConfigRequest) || configWriteErrorCode(err) != ConfigWriteVersionConflict {
		t.Fatalf("WriteValue(stale version) error = %v, code = %s, want %s", err, configWriteErrorCode(err), ConfigWriteVersionConflict)
	}
	_, err = service.WriteValue(&ConfigValueWriteParams{KeyPath: "profile", Value: "work"})
	if !errors.Is(err, ErrInvalidConfigRequest) || !strings.Contains(err.Error(), "legacy config selector") || configWriteErrorCode(err) != ConfigWriteValidation {
		t.Fatalf("WriteValue(profile) error = %v, code = %s, want legacy profile selector rejection", err, configWriteErrorCode(err))
	}
	_, err = service.WriteValue(&ConfigValueWriteParams{KeyPath: "profiles.work.model", Value: "gpt-5"})
	if !errors.Is(err, ErrInvalidConfigRequest) || !strings.Contains(err.Error(), "legacy config profile tables") || configWriteErrorCode(err) != ConfigWriteValidation {
		t.Fatalf("WriteValue(profiles.*) error = %v, code = %s, want legacy profile table rejection", err, configWriteErrorCode(err))
	}
}

func configWriteErrorCode(err error) ConfigWriteErrorCode {
	var writeErr *ConfigWriteError
	if errors.As(err, &writeErr) && writeErr != nil {
		return writeErr.Code
	}
	return ""
}

func TestRequirementsClone(t *testing.T) {
	service := NewConfigService(t.TempDir())
	allow := true
	defaultPermissions := "default"
	service.SetRequirements(&ConfigRequirements{
		AllowedApprovalPolicies: []sandbox.AskForApproval{sandbox.ApprovalOnRequest},
		AllowedSandboxModes:     []sandbox.SandboxMode{sandbox.SandboxWorkspaceWrite},
		DefaultPermissions:      &defaultPermissions,
		AllowRemoteControl:      &allow,
		Network:                 &NetworkRequirements{Enabled: &allow, Domains: map[string]NetworkPermission{"example.com": NetworkAllow}},
	})
	read := service.Requirements()
	read.Requirements.AllowedApprovalPolicies[0] = sandbox.ApprovalNever
	read.Requirements.Network.Domains["example.com"] = NetworkDeny

	again := service.Requirements()
	if again.Requirements.AllowedApprovalPolicies[0] != sandbox.ApprovalOnRequest {
		t.Fatalf("requirements slice mutation leaked")
	}
	if again.Requirements.Network.Domains["example.com"] != NetworkAllow {
		t.Fatalf("requirements map mutation leaked")
	}
}

func TestNewConfigServiceLoadsRequirementsFileLikeRust(t *testing.T) {
	home := t.TempDir()
	body := `
[models.new_thread]
model = "gpt-managed"
model_reasoning_effort = "medium"
service_tier = "fast"
`
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile requirements error = %v", err)
	}

	service := NewConfigService(home)
	read := service.Requirements()
	if read.Requirements == nil || read.Requirements.Models == nil || read.Requirements.Models.NewThread == nil {
		t.Fatalf("requirements = %#v, want new-thread model defaults", read.Requirements)
	}
	defaults := read.Requirements.Models.NewThread
	if defaults.Model == nil || *defaults.Model != "gpt-managed" {
		t.Fatalf("Model = %#v, want gpt-managed", defaults.Model)
	}
	if defaults.ModelReasoningEffort == nil || *defaults.ModelReasoningEffort != "medium" {
		t.Fatalf("ModelReasoningEffort = %#v, want medium", defaults.ModelReasoningEffort)
	}
	if defaults.ServiceTier == nil || *defaults.ServiceTier != "fast" {
		t.Fatalf("ServiceTier = %#v, want fast", defaults.ServiceTier)
	}
}

func TestConfigWarningsClone(t *testing.T) {
	service := NewConfigService(t.TempDir())
	details := "invalid value"
	path := filepath.Join(t.TempDir(), "config.toml")
	warnings := []ConfigWarningNotification{{
		Summary: "Invalid configuration; using defaults.",
		Details: &details,
		Path:    &path,
		Range: &TextRange{
			Start: TextPosition{Line: 1, Column: 2},
			End:   TextPosition{Line: 3, Column: 4},
		},
	}}
	service.SetWarnings(warnings)
	warnings[0].Summary = "mutated"
	warnings[0].Range.Start.Line = 99

	read := service.Warnings()
	if len(read) != 1 || read[0].Summary != "Invalid configuration; using defaults." || read[0].Range.Start.Line != 1 {
		t.Fatalf("warnings clone = %+v", read)
	}
	read[0].Summary = "changed"
	read[0].Range.Start.Line = 42

	again := service.Warnings()
	if again[0].Summary != "Invalid configuration; using defaults." || again[0].Range.Start.Line != 1 {
		t.Fatalf("warnings mutation leaked = %+v", again)
	}
}

func TestExternalAgentConfigDetectAndImport(t *testing.T) {
	service := NewConfigService(t.TempDir())
	service.SetClock(func() time.Time { return time.UnixMilli(1234) })
	cwd := t.TempDir()
	detected := service.DetectExternalAgentConfig(&ExternalAgentConfigDetectParams{IncludeHome: true, CWDs: []string{cwd}})
	if len(detected.Items) != 2 || detected.Items[0].ItemType != MigrationConfig || detected.Items[1].ItemType != MigrationAgentsMD {
		t.Fatalf("detected = %+v", detected.Items)
	}

	source := "cursor"
	response, notification := service.ImportExternalAgentConfig(&ExternalAgentConfigImportParams{
		MigrationItems: detected.Items,
		Source:         &source,
	})
	if response.ImportID != "import-1" || notification.ImportID != "import-1" {
		t.Fatalf("import response = %+v notification = %+v", response, notification)
	}
	histories := service.ImportHistories()
	if len(histories.Data) != 1 || histories.Data[0].CompletedAtMS != 1234 || len(histories.Data[0].Successes) != 2 {
		t.Fatalf("histories = %+v", histories.Data)
	}
}

func writeConfig(t *testing.T, home string, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
}
