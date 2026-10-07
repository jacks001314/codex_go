package hostconfig

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"codex_go/execserver"
	"codex_go/utils"

	"github.com/pelletier/go-toml/v2"
)

// TestReadEnvironmentConfigMatchesRust mirrors Rust
// exec-server/src/environment_config.rs::read_environment_config: validate the
// selectors, load the executor-local layers, project them, and report the
// executor user home, Codex home and hostname.
func TestReadEnvironmentConfigMatchesRust(t *testing.T) {
	fixture := newProjectFixture(t)
	resp, err := NewReader().ReadEnvironmentConfig(&execserver.EnvironmentConfigReadParams{
		CWD:               fileURI(t, fixture.nested),
		ConfigPaths:       [][]string{{"mcp_servers"}},
		RequirementsPaths: [][]string{{"allowed_sandbox_modes"}},
	}, nil)
	if err != nil {
		t.Fatalf("ReadEnvironmentConfig() error = %v", err)
	}

	if want := fileURI(t, fixture.home); resp.CodexHomeDir != want {
		t.Fatalf("CodexHomeDir = %q, want %q", resp.CodexHomeDir, want)
	}
	if resp.UserHomeDir == nil || !strings.HasPrefix(*resp.UserHomeDir, "file://") {
		t.Fatalf("UserHomeDir = %#v, want a file URI", resp.UserHomeDir)
	}
	if resp.Hostname == nil || strings.TrimSpace(*resp.Hostname) == "" {
		t.Fatalf("Hostname = %#v, want the executor hostname", resp.Hostname)
	}

	// The user layer carries mcp_servers, so both the user and project layers
	// survive projection; the legacy managed layer has no such key and is
	// dropped.
	sources := layerSources(resp.Config.Layers)
	wantSources := []string{
		"user (" + filepath.Join(fixture.home, "config.toml") + ")",
		"project (" + filepath.Join(fixture.dotCodex, "config.toml") + ")",
	}
	if !reflect.DeepEqual(sources, wantSources) {
		t.Fatalf("config layer sources = %#v, want %#v", sources, wantSources)
	}
	for index, want := range []string{fixture.home, fixture.dotCodex} {
		if got := resp.Config.Layers[index].BaseDir; got != fileURI(t, want) {
			t.Fatalf("layer %d base dir = %q, want %q", index, got, fileURI(t, want))
		}
	}
	for _, layer := range resp.Config.Layers {
		decoded := decodeLayerTOML(t, layer.TOML)
		if _, ok := decoded["mcp_servers"]; !ok {
			t.Fatalf("layer %q toml = %q, want only mcp_servers", layer.Source, layer.TOML)
		}
		if _, ok := decoded["model"]; ok {
			t.Fatalf("layer %q toml = %q, want no unselected keys", layer.Source, layer.TOML)
		}
	}
	if resp.Config.CloudInsertionIndex != 0 {
		t.Fatalf("cloud insertion index = %d, want 0", resp.Config.CloudInsertionIndex)
	}

	requirementSources := layerSources(resp.Requirements.Layers)
	wantRequirements := []string{filepath.Join(fixture.home, "requirements.toml")}
	if !reflect.DeepEqual(requirementSources, wantRequirements) {
		t.Fatalf("requirements layer sources = %#v, want %#v", requirementSources, wantRequirements)
	}
}

// TestReadEnvironmentConfigPreservesCLIPreferMXCLikeRust mirrors upstream
// #51525: the CLI `features.prefer_mxc` value is retained as a session-flags
// layer above project config and below the legacy managed file.
func TestReadEnvironmentConfigPreservesCLIPreferMXCLikeRust(t *testing.T) {
	fixture := newProjectFixture(t)
	managed := filepath.Join(fixture.home, "managed_config.toml")
	managedBody := "approval_policy = \"on-request\"\n\n[mcp_servers.legacy]\nurl = \"https://legacy.test/mcp\"\n"
	if err := os.WriteFile(managed, []byte(managedBody), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CODEX_APP_SERVER_MANAGED_CONFIG_PATH", managed)

	prefer := false
	resp, err := NewReader().ReadEnvironmentConfig(&execserver.EnvironmentConfigReadParams{
		CWD:               fileURI(t, fixture.nested),
		ConfigPaths:       [][]string{{"mcp_servers"}, {"features"}},
		RequirementsPaths: [][]string{{"allowed_approval_policies"}},
	}, &prefer)
	if err != nil {
		t.Fatalf("ReadEnvironmentConfig() error = %v", err)
	}
	sources := layerSources(resp.Config.Layers)
	wantSources := []string{
		"user (" + filepath.Join(fixture.home, "config.toml") + ")",
		"project (" + filepath.Join(fixture.dotCodex, "config.toml") + ")",
		"session-flags",
		"legacy managed_config.toml (" + managed + ")",
	}
	if !reflect.DeepEqual(sources, wantSources) {
		t.Fatalf("config layer sources = %#v, want %#v", sources, wantSources)
	}
	sessionLayer := decodeLayerTOML(t, resp.Config.Layers[2].TOML)
	features, ok := sessionLayer["features"].(map[string]any)
	if !ok || features["prefer_mxc"] != false {
		t.Fatalf("session flags toml = %q, want features.prefer_mxc = false", resp.Config.Layers[2].TOML)
	}
	// The legacy managed file backfills approval requirements below the session
	// flags layer's precedence.
	backfill := decodeLayerTOML(t, resp.Requirements.Layers[len(resp.Requirements.Layers)-1].TOML)
	if got := backfill["allowed_approval_policies"]; !reflect.DeepEqual(got, []any{"on-request"}) {
		t.Fatalf("legacy requirements = %#v", got)
	}

	// Without an explicit CLI preference the session-flags layer is absent
	// (Rust retains only the explicit value).
	plain, err := NewReader().ReadEnvironmentConfig(&execserver.EnvironmentConfigReadParams{
		CWD:         fileURI(t, fixture.nested),
		ConfigPaths: [][]string{{"features"}},
	}, nil)
	if err != nil {
		t.Fatalf("ReadEnvironmentConfig() error = %v", err)
	}
	for _, layer := range plain.Config.Layers {
		if strings.Contains(layer.Source, "session-flags") {
			t.Fatalf("session flags layer present without a CLI preference: %#v", plain.Config.Layers)
		}
	}
}

// TestReadEnvironmentConfigValidationMatchesRust mirrors Rust validate_paths.
func TestReadEnvironmentConfigValidationMatchesRust(t *testing.T) {
	fixture := newProjectFixture(t)
	cases := []struct {
		name    string
		params  *execserver.EnvironmentConfigReadParams
		message string
	}{
		{
			name:    "no selectors",
			params:  &execserver.EnvironmentConfigReadParams{CWD: fileURI(t, fixture.nested)},
			message: "at least one config or requirements path is required",
		},
		{
			name: "empty config path",
			params: &execserver.EnvironmentConfigReadParams{
				CWD:         fileURI(t, fixture.nested),
				ConfigPaths: [][]string{{}},
			},
			message: "TOML paths must contain at least one key segment",
		},
		{
			name: "empty requirements path",
			params: &execserver.EnvironmentConfigReadParams{
				CWD:               fileURI(t, fixture.nested),
				RequirementsPaths: [][]string{{}},
			},
			message: "TOML paths must contain at least one key segment",
		},
		{
			name: "relative cwd",
			params: &execserver.EnvironmentConfigReadParams{
				CWD:         "relative/path",
				ConfigPaths: [][]string{{"mcp_servers"}},
			},
			message: "cwd must be an absolute file URI",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := NewReader().ReadEnvironmentConfig(testCase.params, nil)
			if err == nil {
				t.Fatal("ReadEnvironmentConfig() error = nil")
			}
			var readErr *execserver.EnvironmentConfigReadError
			if !errors.As(err, &readErr) || !readErr.InvalidParams {
				t.Fatalf("error = %v, want invalid params", err)
			}
			if !strings.Contains(readErr.Message, testCase.message) {
				t.Fatalf("error = %q, want %q", readErr.Message, testCase.message)
			}
		})
	}
}

type projectFixture struct {
	home     string
	repo     string
	nested   string
	dotCodex string
}

func newProjectFixture(t *testing.T) projectFixture {
	t.Helper()
	root := t.TempDir()
	fixture := projectFixture{
		home:     filepath.Join(root, "home"),
		repo:     filepath.Join(root, "repo"),
		dotCodex: filepath.Join(root, "repo", ".gcode"),
		nested:   filepath.Join(root, "repo", "packages", "app"),
	}
	for _, dir := range []string{fixture.home, filepath.Join(fixture.repo, ".git"), fixture.nested, fixture.dotCodex} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(fixture.repo, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	userConfig := "model = \"gpt-5\"\n\n[mcp_servers.user]\nurl = \"https://user.test/mcp\"\n\n[projects.\"" +
		strings.ReplaceAll(fixture.repo, `\`, `\\`) + "\"]\ntrust_level = \"trusted\"\n"
	if err := os.WriteFile(filepath.Join(fixture.home, "config.toml"), []byte(userConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	projectConfig := "[mcp_servers.project]\nurl = \"https://project.test/mcp\"\n"
	if err := os.WriteFile(filepath.Join(fixture.dotCodex, "config.toml"), []byte(projectConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(fixture.home, "requirements.toml"), []byte("allowed_sandbox_modes = [\"read-only\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GCODE_HOME", "")
	t.Setenv("CODEX_HOME", fixture.home)
	// Pin the legacy managed path so a host-level /etc/codex/managed_config.toml
	// cannot change the fixture's layer set.
	t.Setenv("CODEX_APP_SERVER_MANAGED_CONFIG_PATH", filepath.Join(root, "absent-managed_config.toml"))
	return fixture
}

func fileURI(t *testing.T, path string) string {
	t.Helper()
	uri, err := utils.FromHostNativePath(path)
	if err != nil {
		t.Fatalf("FromHostNativePath(%q) error = %v", path, err)
	}
	return uri.String()
}

func layerSources(layers []execserver.EnvironmentConfigLayer) []string {
	sources := make([]string, 0, len(layers))
	for _, layer := range layers {
		sources = append(sources, layer.Source)
	}
	return sources
}

func decodeLayerTOML(t *testing.T, raw string) map[string]any {
	t.Helper()
	values := map[string]any{}
	if strings.TrimSpace(raw) == "" {
		return values
	}
	if err := toml.Unmarshal([]byte(raw), &values); err != nil {
		t.Fatalf("layer toml %q is invalid: %v", raw, err)
	}
	return values
}

// TestExecServerServesEnvironmentConfigReadLikeRust exercises the whole path the
// exec-server command uses: a server with this reader installed advertises
// environmentConfigRead and answers the request over stdio.
func TestExecServerServesEnvironmentConfigReadLikeRust(t *testing.T) {
	fixture := newProjectFixture(t)
	server := execserver.NewServer()
	server.SetEnvironmentConfigReader(NewReader())
	request := `{"id":1,"method":"initialize","params":{"clientName":"test"}}` + "\n" +
		`{"method":"initialized","params":{}}` + "\n" +
		`{"id":2,"method":"environmentConfig/read","params":{"cwd":"` + fileURI(t, fixture.nested) +
		`","configPaths":[["mcp_servers"]],"requirementsPaths":[["allowed_sandbox_modes"]]}}` + "\n"
	var stdout strings.Builder
	if err := server.Serve(t.Context(), strings.NewReader(request), &stdout); err != nil {
		t.Fatalf("Serve() error = %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, `"environmentConfigRead":true`) {
		t.Fatalf("initialize response = %q", output)
	}
	if !strings.Contains(output, escapeJSON(filepath.Join(fixture.dotCodex, "config.toml"))) {
		t.Fatalf("read response should name the executor-local project layer: %q", output)
	}
	if !strings.Contains(output, `"cloudInsertionIndex":0`) {
		t.Fatalf("read response should carry the cloud insertion index: %q", output)
	}
}

func escapeJSON(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return value
	}
	return string(encoded[1 : len(encoded)-1])
}
