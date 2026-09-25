package execserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Rust parity: codex-rs/exec-server/src/environment_toml.rs tests.

func mustProviderSnapshot(t *testing.T, config *EnvironmentsTOML, configDir string) EnvironmentProviderSnapshot {
	t.Helper()
	snapshot, err := NewEnvironmentProviderSnapshot(config, configDir)
	if err != nil {
		t.Fatalf("NewEnvironmentProviderSnapshot() error = %v", err)
	}
	return snapshot
}

func environmentByID(t *testing.T, snapshot EnvironmentProviderSnapshot, id string) EnvironmentTransport {
	t.Helper()
	for _, environment := range snapshot.Environments {
		if environment.ID == id {
			return environment.Transport
		}
	}
	t.Fatalf("environment %q is not configured: %#v", id, snapshot.Environments)
	return EnvironmentTransport{}
}

func TestEnvironmentAuthBearerTokenRequiresWebSocketAndPreservesTimeoutsLikeRust(t *testing.T) {
	token := "private-token"
	websocketURL := "wss://executor.example"
	seconds := 17.0
	snapshot := mustProviderSnapshot(t, &EnvironmentsTOML{
		Environments: []EnvironmentTOML{{
			ID:         "remote",
			URL:        &websocketURL,
			Token:      &token,
			Initialize: &seconds,
		}},
	}, "")
	transport := environmentByID(t, snapshot, "remote")
	if transport.Kind != EnvironmentTransportWebSocket {
		t.Fatalf("transport kind = %q, want websocket", transport.Kind)
	}
	if got := transport.HTTPHeaders.Get("Authorization"); got != "Bearer private-token" {
		t.Fatalf("Authorization = %q", got)
	}
	if transport.InitializeTimeout != 17*time.Second {
		t.Fatalf("initialize timeout = %v, want 17s", transport.InitializeTimeout)
	}
	if transport.ConnectTimeout != DefaultRemoteExecServerConnectTimeout {
		t.Fatalf("connect timeout = %v, want the default", transport.ConnectTimeout)
	}
	// The token never reaches a diagnostic rendering of the configuration.
	if rendered := RedactedExecServerHeaders(transport.HTTPHeaders); strings.Contains(rendered, token) {
		t.Fatalf("redacted headers leaked the token: %s", rendered)
	}

	// A token on a stdio environment is rejected.
	program := "executor"
	_, err := NewEnvironmentProviderSnapshot(&EnvironmentsTOML{
		Environments: []EnvironmentTOML{{ID: "stdio", Program: &program, Token: &token}},
	}, "")
	if err == nil || !strings.Contains(err.Error(), "auth_bearer_token requires url") {
		t.Fatalf("stdio token error = %v, want the url requirement", err)
	}
}

func TestEnvironmentTOMLProviderIncludesLocalAndConfiguredEnvironmentsLikeRust(t *testing.T) {
	defaultID := "ssh-dev"
	url := " ws://127.0.0.1:8765 "
	program := " ssh "
	args := []string{"dev", "codex exec-server --listen stdio"}
	env := map[string]string{"CODEX_LOG": "debug"}
	snapshot := mustProviderSnapshot(t, &EnvironmentsTOML{
		Default: &defaultID,
		Environments: []EnvironmentTOML{
			{ID: "devbox", URL: &url},
			{ID: "ssh-dev", Program: &program, Args: &args, Env: &env},
		},
	}, "")
	if !snapshot.IncludeLocal {
		t.Fatal("include local = false, want true")
	}
	if len(snapshot.Environments) != 2 || snapshot.Environments[0].ID != "devbox" || snapshot.Environments[1].ID != "ssh-dev" {
		t.Fatalf("environments = %#v", snapshot.Environments)
	}
	if kind := environmentByID(t, snapshot, "devbox").Kind; kind != EnvironmentTransportWebSocket {
		t.Fatalf("devbox kind = %q", kind)
	}
	if kind := environmentByID(t, snapshot, "ssh-dev").Kind; kind != EnvironmentTransportStdio {
		t.Fatalf("ssh-dev kind = %q", kind)
	}
	if id, ok := snapshot.EnvironmentID(); !ok || id != "ssh-dev" {
		t.Fatalf("default = %#v", snapshot.Default)
	}
	// The websocket URL is trimmed and the program is trimmed.
	if got := environmentByID(t, snapshot, "devbox").WebSocketURL; got != "ws://127.0.0.1:8765" {
		t.Fatalf("websocket url = %q", got)
	}
	command := environmentByID(t, snapshot, "ssh-dev").Command
	if command == nil || command.Program != "ssh" || len(command.Args) != 2 || command.Env["CODEX_LOG"] != "debug" {
		t.Fatalf("stdio command = %#v", command)
	}
}

func TestEnvironmentTOMLProviderDefaultSelectionLikeRust(t *testing.T) {
	// Omitted default selects local, including for an empty configuration.
	snapshot := mustProviderSnapshot(t, &EnvironmentsTOML{}, "")
	if !snapshot.IncludeLocal {
		t.Fatal("empty config include local = false")
	}
	if id, ok := snapshot.EnvironmentID(); !ok || id != LocalEnvironmentID {
		t.Fatalf("omitted default = %#v, want local", snapshot.Default)
	}

	// `default = "none"` disables the default.
	none := "none"
	snapshot = mustProviderSnapshot(t, &EnvironmentsTOML{Default: &none}, "")
	if snapshot.Default.Kind != EnvironmentDefaultDisabled {
		t.Fatalf("none default = %#v", snapshot.Default)
	}

	// include_local = false with an explicit default.
	disabledLocal := false
	defaultID := "ssh-dev"
	program := "ssh"
	snapshot = mustProviderSnapshot(t, &EnvironmentsTOML{
		Default:      &defaultID,
		IncludeLocal: &disabledLocal,
		Environments: []EnvironmentTOML{{ID: "ssh-dev", Program: &program}},
	}, "")
	if snapshot.IncludeLocal {
		t.Fatal("include local = true, want false")
	}
	if id, ok := snapshot.EnvironmentID(); !ok || id != "ssh-dev" {
		t.Fatalf("default = %#v", snapshot.Default)
	}

	// include_local = false with no default disables the default.
	snapshot = mustProviderSnapshot(t, &EnvironmentsTOML{IncludeLocal: &disabledLocal}, "")
	if snapshot.IncludeLocal || snapshot.Default.Kind != EnvironmentDefaultDisabled {
		t.Fatalf("snapshot = %#v, want local disabled and no default", snapshot)
	}

	// The default id must match exactly: Rust's `ids.contains(default)` is
	// case-sensitive even though `none` is case-insensitive.
	upperDefault := "LOCAL"
	if _, err := NewEnvironmentProviderSnapshot(&EnvironmentsTOML{Default: &upperDefault}, ""); err == nil {
		t.Fatal("a case-different default id was accepted")
	}
}

func TestEnvironmentTOMLProviderRejectsInvalidConfigurationsLikeRust(t *testing.T) {
	longID := strings.Repeat("a", maxEnvironmentIDLen+1)
	for _, testCase := range []struct {
		name      string
		config    *EnvironmentsTOML
		configDir string
		want      string
	}{
		{
			name: "local id is reserved",
			config: &EnvironmentsTOML{Environments: []EnvironmentTOML{{
				ID: LocalEnvironmentID, URL: envTestStringPtr("ws://127.0.0.1:8765"),
			}}},
			want: "environment id `local` is reserved",
		},
		{
			name: "whitespace around the id",
			config: &EnvironmentsTOML{Environments: []EnvironmentTOML{{
				ID: " devbox ", URL: envTestStringPtr("ws://127.0.0.1:8765"),
			}}},
			want: "environment id ` devbox ` must not contain surrounding whitespace",
		},
		{
			name: "invalid id characters",
			config: &EnvironmentsTOML{Environments: []EnvironmentTOML{{
				ID: "dev box", URL: envTestStringPtr("ws://127.0.0.1:8765"),
			}}},
			want: "environment id `dev box` must contain only ASCII letters, numbers, '-' or '_'",
		},
		{
			name: "non websocket url",
			config: &EnvironmentsTOML{Environments: []EnvironmentTOML{{
				ID: "devbox", URL: envTestStringPtr("http://127.0.0.1:8765"),
			}}},
			want: "environment url `http://127.0.0.1:8765` must use ws:// or wss://",
		},
		{
			name: "url and program",
			config: &EnvironmentsTOML{Environments: []EnvironmentTOML{{
				ID: "devbox", URL: envTestStringPtr("ws://127.0.0.1:8765"), Program: envTestStringPtr("codex"),
			}}},
			want: "environment `devbox` must set exactly one of url or program",
		},
		{
			name: "empty program",
			config: &EnvironmentsTOML{Environments: []EnvironmentTOML{{
				ID: "devbox", Program: envTestStringPtr(" "),
			}}},
			want: "environment `devbox` program cannot be empty",
		},
		{
			name: "args without program",
			config: &EnvironmentsTOML{Environments: []EnvironmentTOML{{
				ID: "devbox", Args: envTestStringSlicePtr(),
			}}},
			want: "environment `devbox` args, env, and cwd require program",
		},
		{
			name: "connect timeout without url",
			config: &EnvironmentsTOML{Environments: []EnvironmentTOML{{
				ID: "ssh-dev", Program: envTestStringPtr("ssh"), Connect: envTestFloatPtr(1),
			}}},
			want: "environment `ssh-dev` connect_timeout_sec requires url",
		},
		{
			name: "malformed websocket url",
			config: &EnvironmentsTOML{Environments: []EnvironmentTOML{{
				ID: "devbox", URL: envTestStringPtr("ws://"),
			}}},
			want: "environment url `ws://` is invalid",
		},
		{
			name: "duplicate ids",
			config: &EnvironmentsTOML{Environments: []EnvironmentTOML{
				{ID: "devbox", URL: envTestStringPtr("ws://127.0.0.1:8765")},
				{ID: "devbox", Program: envTestStringPtr("codex")},
			}},
			want: "environment id `devbox` is duplicated",
		},
		{
			name: "overlong id",
			config: &EnvironmentsTOML{Environments: []EnvironmentTOML{{
				ID: longID, URL: envTestStringPtr("ws://127.0.0.1:8765"),
			}}},
			want: "environment id `" + longID + "` cannot be longer than 64 characters",
		},
		{
			name:   "unknown default",
			config: &EnvironmentsTOML{Default: envTestStringPtr("missing")},
			want:   "default environment `missing` is not configured",
		},
		{
			name:   "empty default",
			config: &EnvironmentsTOML{Default: envTestStringPtr(" ")},
			want:   "default environment id cannot be empty",
		},
		{
			name: "relative stdio cwd without a config dir",
			config: &EnvironmentsTOML{Environments: []EnvironmentTOML{{
				ID: "ssh-dev", Program: envTestStringPtr("ssh"), CWD: envTestStringPtr("workspace"),
			}}},
			want: "environment `ssh-dev` cwd must be absolute",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := NewEnvironmentProviderSnapshot(testCase.config, testCase.configDir)
			if err == nil {
				t.Fatalf("NewEnvironmentProviderSnapshot() error = nil, want %q", testCase.want)
			}
			if !strings.Contains(err.Error(), testCase.want) {
				t.Fatalf("error = %v, want %q", err, testCase.want)
			}
			if !strings.HasPrefix(err.Error(), "exec-server protocol error: ") {
				t.Fatalf("error = %v, want Rust's protocol-error prefix", err)
			}
		})
	}

	// The local default is rejected when local is disabled.
	disabledLocal := false
	localDefault := LocalEnvironmentID
	_, err := NewEnvironmentProviderSnapshot(&EnvironmentsTOML{
		Default:      &localDefault,
		IncludeLocal: &disabledLocal,
	}, "")
	if err == nil || err.Error() != "exec-server protocol error: default environment `local` is not configured" {
		t.Fatalf("error = %v, want the disabled-local rejection", err)
	}
}

func TestEnvironmentTOMLProviderResolvesRelativeStdioCWDFromConfigDirLikeRust(t *testing.T) {
	configDir := t.TempDir()
	program := "ssh"
	cwd := "workspace"
	snapshot := mustProviderSnapshot(t, &EnvironmentsTOML{Environments: []EnvironmentTOML{{
		ID: "ssh-dev", Program: &program, CWD: &cwd,
	}}}, configDir)
	command := environmentByID(t, snapshot, "ssh-dev").Command
	if command == nil || command.CWD != filepath.Join(configDir, "workspace") {
		t.Fatalf("stdio command = %#v, want the config-dir-relative cwd", command)
	}
	if command.Program != "ssh" || len(command.Args) != 0 || len(command.Env) != 0 {
		t.Fatalf("stdio command defaults = %#v", command)
	}
	if timeout := environmentByID(t, snapshot, "ssh-dev").InitializeTimeout; timeout != DefaultRemoteExecServerInitializeTimeout {
		t.Fatalf("initialize timeout = %v, want the default", timeout)
	}

	// An absolute cwd is preserved.
	absolute := filepath.Join(configDir, "elsewhere")
	snapshot = mustProviderSnapshot(t, &EnvironmentsTOML{Environments: []EnvironmentTOML{{
		ID: "ssh-dev", Program: &program, CWD: &absolute,
	}}}, configDir)
	if command := environmentByID(t, snapshot, "ssh-dev").Command; command == nil || command.CWD != absolute {
		t.Fatalf("absolute cwd = %#v", command)
	}
}

func TestEnvironmentTOMLProviderParsesConfiguredTransportTimeoutsLikeRust(t *testing.T) {
	url := "ws://127.0.0.1:8765"
	program := "ssh"
	connect := 12.0
	initialize := 34.0
	sshInitialize := 56.0
	snapshot := mustProviderSnapshot(t, &EnvironmentsTOML{Environments: []EnvironmentTOML{
		{ID: "devbox", URL: &url, Connect: &connect, Initialize: &initialize},
		{ID: "ssh-dev", Program: &program, Initialize: &sshInitialize},
	}}, "")
	websocket := environmentByID(t, snapshot, "devbox")
	if websocket.WebSocketURL != url || websocket.ConnectTimeout != 12*time.Second || websocket.InitializeTimeout != 34*time.Second {
		t.Fatalf("websocket transport = %#v", websocket)
	}
	stdio := environmentByID(t, snapshot, "ssh-dev")
	if stdio.InitializeTimeout != 56*time.Second {
		t.Fatalf("stdio initialize timeout = %v, want 56s", stdio.InitializeTimeout)
	}
}

func TestLoadEnvironmentsTOMLReadsRootEnvironmentListLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	path := filepath.Join(codexHome, EnvironmentsTOMLFile)
	contents := `
default = "ssh-dev"
include_local = false

[[environments]]
id = "devbox"
url = "ws://127.0.0.1:4512"
connect_timeout_sec = 12.0
initialize_timeout_sec = 34.0

[[environments]]
id = "ssh-dev"
program = "ssh"
args = ["dev", "codex exec-server --listen stdio"]
cwd = "/tmp"
[environments.env]
CODEX_LOG = "debug"
`
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatalf("write environments.toml: %v", err)
	}
	config, err := LoadEnvironmentsTOML(path)
	if err != nil || config == nil {
		t.Fatalf("LoadEnvironmentsTOML() = %#v, %v", config, err)
	}
	if config.Default == nil || *config.Default != "ssh-dev" || config.IncludeLocal == nil || *config.IncludeLocal {
		t.Fatalf("config header = %#v", config)
	}
	if len(config.Environments) != 2 {
		t.Fatalf("environments = %#v", config.Environments)
	}
	first := config.Environments[0]
	if first.ID != "devbox" || first.URL == nil || *first.URL != "ws://127.0.0.1:4512" ||
		first.Connect == nil || *first.Connect != 12 || first.Initialize == nil || *first.Initialize != 34 {
		t.Fatalf("first environment = %#v", first)
	}
	second := config.Environments[1]
	if second.ID != "ssh-dev" || second.Program == nil || *second.Program != "ssh" ||
		second.Args == nil || len(*second.Args) != 2 || second.CWD == nil || *second.CWD != "/tmp" ||
		second.Env == nil || (*second.Env)["CODEX_LOG"] != "debug" {
		t.Fatalf("second environment = %#v", second)
	}
}

func TestLoadEnvironmentsTOMLRejectsUnknownFieldsLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	for index, testCase := range []struct {
		contents string
		want     string
	}{
		{contents: "unknown = true\n", want: "unknown field `unknown`"},
		{
			contents: "[[environments]]\nid = \"devbox\"\nurl = \"ws://127.0.0.1:4512\"\nunknown = true\n",
			want:     "unknown field `unknown`",
		},
	} {
		path := filepath.Join(codexHome, environmentsTestFileName(index))
		if err := os.WriteFile(path, []byte(testCase.contents), 0o600); err != nil {
			t.Fatalf("write config: %v", err)
		}
		_, err := LoadEnvironmentsTOML(path)
		if err == nil {
			t.Fatalf("LoadEnvironmentsTOML(%s) error = nil, want a rejection", path)
		}
		if !strings.Contains(err.Error(), testCase.want) {
			t.Fatalf("error = %v, want %q", err, testCase.want)
		}
		// The failure never echoes configuration values.
		if strings.Contains(err.Error(), "ws://127.0.0.1:4512") || strings.Contains(err.Error(), "devbox") {
			t.Fatalf("error echoed configuration values: %v", err)
		}
	}

	// Syntax failures report a position, not the document text.
	path := filepath.Join(codexHome, "environments-syntax.toml")
	if err := os.WriteFile(path, []byte("[[environments]\nbroken = \"secret-value\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err := LoadEnvironmentsTOML(path)
	if err == nil || !strings.Contains(err.Error(), "invalid TOML") {
		t.Fatalf("syntax error = %v", err)
	}
	if strings.Contains(err.Error(), "secret-value") {
		t.Fatalf("syntax error echoed the document: %v", err)
	}
}

func TestEnvironmentProviderFromCodexHomeLikeRust(t *testing.T) {
	// A present configuration file wins.
	codexHome := t.TempDir()
	if err := os.WriteFile(filepath.Join(codexHome, EnvironmentsTOMLFile), []byte("default = \"none\"\ninclude_local = false\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	snapshot, err := EnvironmentProviderFromCodexHome(codexHome)
	if err != nil {
		t.Fatalf("EnvironmentProviderFromCodexHome() error = %v", err)
	}
	if snapshot.IncludeLocal || snapshot.Default.Kind != EnvironmentDefaultDisabled || len(snapshot.Environments) != 0 {
		t.Fatalf("snapshot = %#v", snapshot)
	}

	// A missing file falls back to the CODEX_EXEC_SERVER_URL provider.
	t.Setenv(CodexExecServerURLEnvVarName, "")
	snapshot, err = EnvironmentProviderFromCodexHome(t.TempDir())
	if err != nil {
		t.Fatalf("EnvironmentProviderFromCodexHome(missing) error = %v", err)
	}
	if !snapshot.IncludeLocal {
		t.Fatal("fallback include local = false")
	}
	if id, ok := snapshot.EnvironmentID(); !ok || id != LocalEnvironmentID {
		t.Fatalf("fallback default = %#v, want local", snapshot.Default)
	}

	// CODEX_EXEC_SERVER_URL selects a single remote environment.
	t.Setenv(CodexExecServerURLEnvVarName, "ws://127.0.0.1:4512")
	snapshot, err = EnvironmentProviderFromCodexHome(t.TempDir())
	if err != nil {
		t.Fatalf("EnvironmentProviderFromCodexHome(env) error = %v", err)
	}
	if snapshot.IncludeLocal || len(snapshot.Environments) != 1 || snapshot.Environments[0].ID != RemoteEnvironmentID {
		t.Fatalf("env snapshot = %#v", snapshot)
	}
	if id, ok := snapshot.EnvironmentID(); !ok || id != RemoteEnvironmentID {
		t.Fatalf("env default = %#v", snapshot.Default)
	}

	// `none` disables the provider's environments entirely.
	t.Setenv(CodexExecServerURLEnvVarName, "none")
	snapshot, err = EnvironmentProviderFromCodexHome(t.TempDir())
	if err != nil {
		t.Fatalf("EnvironmentProviderFromCodexHome(none) error = %v", err)
	}
	if snapshot.IncludeLocal || len(snapshot.Environments) != 0 || snapshot.Default.Kind != EnvironmentDefaultDisabled {
		t.Fatalf("none snapshot = %#v", snapshot)
	}
}

func environmentsTestFileName(index int) string {
	return "environments-" + string(rune('0'+index)) + ".toml"
}

func envTestStringPtr(value string) *string { return &value }

func envTestFloatPtr(value float64) *float64 { return &value }

func envTestStringSlicePtr() *[]string {
	values := []string{}
	return &values
}
