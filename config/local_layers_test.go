package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// TestLoadLocalConfigLayersMatchesRust mirrors Rust
// config/src/loader/local.rs::load_local_config_layers: the user layer is always
// present, trusted project layers follow, and the legacy managed layer is last;
// requirements come from CODEX_HOME/requirements.toml plus the legacy managed
// backfill (loader::legacy_requirements_to_toml_value).
func TestLoadLocalConfigLayersMatchesRust(t *testing.T) {
	root := t.TempDir()
	home := filepath.Join(root, "home")
	repo := filepath.Join(root, "repo")
	nested := filepath.Join(repo, "packages", "app")
	for _, dir := range []string{home, filepath.Join(repo, ".git"), nested} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	userConfig := "model = \"gpt-5\"\n\n[projects.\"" + escapeTOMLPath(repo) + "\"]\ntrust_level = \"trusted\"\n"
	if err := os.WriteFile(ConfigPath(home), []byte(userConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	dotCodex := filepath.Join(repo, ".gcode")
	if err := os.MkdirAll(dotCodex, 0o755); err != nil {
		t.Fatal(err)
	}
	projectConfig := "[mcp_servers.docs]\nurl = \"https://example.test/mcp\"\n"
	if err := os.WriteFile(filepath.Join(dotCodex, "config.toml"), []byte(projectConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte("allowed_sandbox_modes = [\"read-only\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	managed := filepath.Join(home, "managed_config.toml")
	managedConfig := "approval_policy = \"on-request\"\napprovals_reviewer = \"auto_review\"\nsandbox_mode = \"workspace-write\"\n[windows]\nsandbox = \"elevated\"\n"
	if err := os.WriteFile(managed, []byte(managedConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv(appServerManagedConfigPathEnv, managed)

	layers, err := LoadLocalConfigLayers(home, nested)
	if err != nil {
		t.Fatalf("LoadLocalConfigLayers() error = %v", err)
	}

	gotSources := make([]string, 0, len(layers.Config.Layers))
	for _, layer := range layers.Config.Layers {
		gotSources = append(gotSources, layer.Source)
	}
	wantSources := []string{
		formatUserLayerSource(ConfigPath(home)),
		formatProjectLayerSource(dotCodex),
		formatLegacyManagedLayerSource(managed),
	}
	if !reflect.DeepEqual(gotSources, wantSources) {
		t.Fatalf("config layer sources = %#v, want %#v", gotSources, wantSources)
	}
	wantBaseDirs := []string{home, dotCodex, filepath.Dir(managed)}
	for index, want := range wantBaseDirs {
		if got := layers.Config.Layers[index].BaseDir; got != want {
			t.Fatalf("config layer %d base dir = %q, want %q", index, got, want)
		}
	}
	if layers.Config.CloudInsertionIndex != 0 {
		t.Fatalf("config cloud insertion index = %d, want 0 (Go has no system layer)", layers.Config.CloudInsertionIndex)
	}
	// Raw TOML is preserved verbatim: relative paths are not normalized.
	if got := layers.Config.Layers[1].TOML["mcp_servers"]; got == nil {
		t.Fatalf("project layer TOML = %#v, want the raw mcp_servers table", layers.Config.Layers[1].TOML)
	}

	if len(layers.Requirements.Layers) != 2 {
		t.Fatalf("requirements layers = %#v, want the home file plus the legacy backfill", layers.Requirements.Layers)
	}
	if got := layers.Requirements.Layers[0].Source; got != filepath.Join(home, "requirements.toml") {
		t.Fatalf("requirements layer 0 source = %q", got)
	}
	backfill := layers.Requirements.Layers[1].TOML
	if got := backfill["allowed_approval_policies"]; !reflect.DeepEqual(got, []any{"on-request"}) {
		t.Fatalf("legacy allowed_approval_policies = %#v", got)
	}
	if got := backfill["allowed_approvals_reviewers"]; !reflect.DeepEqual(got, []any{ApprovalsReviewerAutoReview, ApprovalsReviewerUser}) {
		t.Fatalf("legacy allowed_approvals_reviewers = %#v", got)
	}
	if got := backfill["allowed_sandbox_modes"]; !reflect.DeepEqual(got, []any{"read-only", "workspace-write"}) {
		t.Fatalf("legacy allowed_sandbox_modes = %#v", got)
	}
	if _, ok := backfill["windows"]; ok {
		t.Fatal("the legacy backfill must only carry approval and sandbox requirements")
	}
}

// TestProjectLocalConfigLayersMatchesRust mirrors Rust
// LocalTomlLayerStack::project: only the requested paths survive, and a layer
// that projects to an empty table is dropped.
func TestProjectLocalConfigLayersMatchesRust(t *testing.T) {
	stack := LocalTomlLayerStack{
		Layers: []LocalTomlLayer{
			{Source: "user", BaseDir: "/home/u", TOML: map[string]any{"model": "gpt-5", "mcp_servers": map[string]any{"a": map[string]any{"url": "https://a"}}}},
			{Source: "project", BaseDir: "/repo/.gcode", TOML: map[string]any{"mcp_servers": map[string]any{"b": map[string]any{"url": "https://b"}}, "model": "gpt-6"}},
			{Source: "legacy", BaseDir: "/etc/codex", TOML: map[string]any{"windows": map[string]any{"sandbox": "elevated"}}},
		},
		CloudInsertionIndex: 1,
	}
	projected := stack.Project([][]string{{"mcp_servers"}})
	if len(projected.Layers) != 2 {
		t.Fatalf("projected layers = %#v, want the user and project layers", projected.Layers)
	}
	for _, layer := range projected.Layers {
		if len(layer.TOML) != 1 {
			t.Fatalf("projected layer %s TOML = %#v, want only mcp_servers", layer.Source, layer.TOML)
		}
		if _, ok := layer.TOML["mcp_servers"]; !ok {
			t.Fatalf("projected layer %s TOML = %#v, want mcp_servers", layer.Source, layer.TOML)
		}
	}
	// Rust adjusts the cloud insertion index for the dropped layer.
	if projected.CloudInsertionIndex != 1 {
		t.Fatalf("cloud insertion index = %d, want 1", projected.CloudInsertionIndex)
	}

	// An empty path selects the whole document, which keeps every layer.
	whole := stack.Project([][]string{{}})
	if len(whole.Layers) != 3 {
		t.Fatalf("whole-document projection dropped layers: %#v", whole.Layers)
	}

	// A terminal selector keeps a scalar value as-is.
	scalar := stack.Project([][]string{{"model"}})
	if len(scalar.Layers) != 2 {
		t.Fatalf("scalar projection layers = %#v, want the two model-bearing layers", scalar.Layers)
	}
}

// TestSessionFlagsLayerOrderingMatchesRust mirrors Rust
// environment_config.rs::include_startup_preference: the session-flags layer sits
// above project layers and below the legacy managed file.
func TestSessionFlagsLayerOrderingMatchesRust(t *testing.T) {
	stack := LocalTomlLayerStack{
		Layers: []LocalTomlLayer{
			{Source: formatUserLayerSource("/home/u/.gcode/config.toml"), BaseDir: "/home/u/.gcode"},
			{Source: formatProjectLayerSource("/repo/.gcode"), BaseDir: "/repo/.gcode"},
			{Source: formatLegacyManagedLayerSource("/etc/codex/managed_config.toml"), BaseDir: "/etc/codex"},
		},
		CloudInsertionIndex: 0,
	}
	document := BuildCLIOverridesLayer([]Override{{Path: "features.prefer_mxc", Value: false}})
	stack = stack.InsertSessionFlagsLayer(document, "/repo")
	if len(stack.Layers) != 4 {
		t.Fatalf("layers = %#v, want four", stack.Layers)
	}
	if stack.Layers[2].Source != SessionFlagsLayerSource {
		t.Fatalf("layer order = %#v, want session flags above project layers", stack.Layers)
	}
	if stack.Layers[3].Source != formatLegacyManagedLayerSource("/etc/codex/managed_config.toml") {
		t.Fatalf("layer order = %#v, want legacy managed last", stack.Layers)
	}
	if stack.Layers[2].BaseDir != "/repo" {
		t.Fatalf("session flags base dir = %q, want the cwd", stack.Layers[2].BaseDir)
	}
	if got := stack.Layers[2].TOML["features"].(map[string]any)["prefer_mxc"]; got != false {
		t.Fatalf("session flags prefer_mxc = %#v, want false", got)
	}
	projected := stack.Project([][]string{{"features", "prefer_mxc"}})
	if len(projected.Layers) != 1 || projected.Layers[0].Source != SessionFlagsLayerSource {
		t.Fatalf("projected layers = %#v, want only the session flags layer", projected.Layers)
	}
}

func TestBuildCLIOverridesLayerExpandsDottedKeysMatchesRust(t *testing.T) {
	overrides, err := ParseOverrides([]string{"features.prefer_mxc=true", "model=gpt-5"})
	if err != nil {
		t.Fatal(err)
	}
	layer := BuildCLIOverridesLayer(overrides)
	features, ok := layer["features"].(map[string]any)
	if !ok || features["prefer_mxc"] != true {
		t.Fatalf("layer = %#v, want features.prefer_mxc", layer)
	}
	if layer["model"] != "gpt-5" {
		t.Fatalf("layer = %#v, want model", layer)
	}
}

func TestLegacyManagedRequirementValuesMatchesRust(t *testing.T) {
	values := LegacyManagedRequirementValues(map[string]any{
		"approval_policy":    "untrusted",
		"sandbox_mode":       "read-only",
		"approvals_reviewer": "user",
		"model_provider":     "evil",
	})
	if got := values["allowed_approval_policies"]; !reflect.DeepEqual(got, []any{"untrusted"}) {
		t.Fatalf("allowed_approval_policies = %#v", got)
	}
	if got := values["allowed_approvals_reviewers"]; !reflect.DeepEqual(got, []any{ApprovalsReviewerUser}) {
		t.Fatalf("allowed_approvals_reviewers = %#v", got)
	}
	// read-only is the only entry when the legacy mode is already read-only.
	if got := values["allowed_sandbox_modes"]; !reflect.DeepEqual(got, []any{"read-only"}) {
		t.Fatalf("allowed_sandbox_modes = %#v", got)
	}
	if _, ok := values["model_provider"]; ok {
		t.Fatal("the legacy backfill must never carry model provider policy")
	}
}

func escapeTOMLPath(path string) string {
	return strings.ReplaceAll(path, `\`, `\\`)
}
