package app

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/appserverdaemon"
	codexauth "codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
)

const testRemoteControlWebSocketURL = "wss://chatgpt.com/backend-api/wham/remote/control/server"

// TestAppServerDaemonUpdateWithoutSelectionIsUnsupportedLikeRust pins the
// manual update path: without a managed package selection the CLI reports the
// updater's unsupported status instead of failing.
func TestAppServerDaemonUpdateWithoutSelectionIsUnsupportedLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	var stdout bytes.Buffer
	err := Run(context.Background(), []string{"app-server", "daemon", "update"}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if err != nil {
		t.Fatalf("daemon update error = %v", err)
	}
	output := stdout.String()
	for _, want := range []string{`"status":"unsupported"`, "This command requires a daemon package selected from its managed releases directory."} {
		if !strings.Contains(output, want) {
			t.Fatalf("stdout = %q, missing %q", output, want)
		}
	}
}

// TestAppServerDaemonUpdateFromCLINeedsASelectedPackage mirrors the Replace
// guard: copying this CLI's package replaces an existing selection, so a
// CODEX_HOME without one reports Rust's refusal before anything is staged.
func TestAppServerDaemonUpdateFromCLINeedsASelectedPackage(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	var stderr bytes.Buffer
	err := Run(context.Background(), []string{"app-server", "daemon", "update", "--from-cli", "--yes"}, strings.NewReader(""), &bytes.Buffer{}, &stderr)
	if err == nil || !strings.Contains(err.Error(), "no daemon package is selected; run `codex app-server daemon start` first") {
		t.Fatalf("daemon update --from-cli error = %v", err)
	}
}

// TestConfirmDaemonInstallLikeRust pins the confirmation text and the --yes and
// interactive answers (Rust cli::daemon_install).
func TestConfirmDaemonInstallLikeRust(t *testing.T) {
	request := &appserverdaemon.DaemonInstallRequest{
		Source:          "/cli/package",
		Version:         "1.2.3",
		Destination:     "/home/packages/app-server-daemon",
		RestartRequired: true,
	}
	version := "1.0.0"
	request.InstalledVersion = &version
	var stderr bytes.Buffer
	confirmed, err := confirmDaemonInstall(request, true, strings.NewReader(""), &stderr)
	if err != nil || !confirmed {
		t.Fatalf("confirmDaemonInstall(--yes) = %v, %v", confirmed, err)
	}
	description := stderr.String()
	for _, want := range []string{
		"Replace installed daemon version 1.0.0 with CLI version 1.2.3 from /cli/package.",
		"The daemon package will be installed in /home/packages/app-server-daemon.",
		"The selected package will be pinned.",
		"The running daemon will restart; active or queued work may be interrupted.",
	} {
		if !strings.Contains(description, want) {
			t.Fatalf("description %q missing %q", description, want)
		}
	}

	// Without --yes a non-terminal caller must be told to rerun with --yes.
	if _, err := confirmDaemonInstall(request, false, strings.NewReader(""), &bytes.Buffer{}); err == nil ||
		!strings.Contains(err.Error(), "rerun with --yes") {
		t.Fatalf("confirmDaemonInstall(non-terminal) error = %v", err)
	}
	if confirmed, err := readConfirmation(strings.NewReader("yes\n")); err != nil || !confirmed {
		t.Fatalf("readConfirmation(yes) = %v, %v", confirmed, err)
	}
	if confirmed, err := readConfirmation(strings.NewReader("n\n")); err != nil || confirmed {
		t.Fatalf("readConfirmation(n) = %v, %v", confirmed, err)
	}
}

func TestAppServerDaemonPlatformBoundary(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())

	var stdout bytes.Buffer
	err := Run(context.Background(), []string{"app-server", "daemon", "bootstrap", "--remote-control"}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	// The bootstrap installs the daemon's own package first, which a
	// non-packaged CLI cannot provide (Rust prepare_install::prepare).
	if err == nil || !strings.Contains(err.Error(), "this CLI has no complete local package") {
		t.Fatalf("bootstrap error = %v", err)
	}
}

func TestAppServerListenOffReturnsNoTransport(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	err := Run(context.Background(), []string{"app-server", "--listen", "off"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "no transport configured; use --listen or enable remote control") {
		t.Fatalf("app-server --listen off error = %v", err)
	}
}

func TestAppServerListenOffRemoteControlNoStateDBError(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	err := Run(context.Background(), []string{"app-server", "--listen", "off", "--remote-control"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || err.Error() != "no transport configured; remote control disabled because sqlite state db is unavailable" {
		t.Fatalf("app-server --listen off --remote-control error = %v", err)
	}
}

func TestAppServerListenOffUsesPersistedRemoteControlPreference(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	clearAuthEnv(t)
	writeChatGPTAuth(t, home, "account-id")
	writeRemoteControlEnrollment(t, home, true)
	enabled, err := appServerPersistedRemoteControlEnabled(context.Background(), home, nil)
	if err != nil || !enabled {
		t.Fatalf("persisted remote-control enabled = %v, %v", enabled, err)
	}
	if err := runAppServerUntilCanceled([]string{"app-server", "--listen", "off"}); err != nil {
		t.Fatalf("app-server --listen off persisted remote-control error = %v", err)
	}
}

func TestAppServerListenOffDisabledPersistedPreferenceReturnsNoTransport(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	clearAuthEnv(t)
	writeChatGPTAuth(t, home, "account-id")
	writeRemoteControlEnrollment(t, home, false)

	err := Run(context.Background(), []string{"app-server", "--listen", "off"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || err.Error() != "no transport configured; use --listen or enable remote control" {
		t.Fatalf("app-server --listen off disabled persisted preference error = %v", err)
	}
}

func TestAppServerListenOffPersistedPreferenceRequiresMatchingAccount(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	clearAuthEnv(t)
	writeChatGPTAuth(t, home, "other-account")
	writeRemoteControlEnrollment(t, home, true)

	enabled, err := appServerPersistedRemoteControlEnabled(context.Background(), home, nil)
	if err != nil || enabled {
		t.Fatalf("persisted remote-control enabled = %v, %v; want false, nil", enabled, err)
	}
	err = Run(context.Background(), []string{"app-server", "--listen", "off"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || err.Error() != "no transport configured; use --listen or enable remote control" {
		t.Fatalf("app-server --listen off mismatched account error = %v", err)
	}
}

func TestAppServerListenOffPersistedPreferenceRequiresEmptyClientName(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	clearAuthEnv(t)
	writeChatGPTAuth(t, home, "account-id")
	writeRemoteControlEnrollmentWithScope(t, home, testRemoteControlWebSocketURL, "account-id", "desktop-client", true)

	enabled, err := appServerPersistedRemoteControlEnabled(context.Background(), home, nil)
	if err != nil || enabled {
		t.Fatalf("persisted remote-control enabled = %v, %v; want false, nil", enabled, err)
	}
}

func TestAppServerListenOffUsesConfiguredRemoteControlURL(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	clearAuthEnv(t)
	writeChatGPTAuth(t, home, "account-id")
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(`chatgpt_base_url = "http://localhost:1234/backend-api"`), 0o600); err != nil {
		t.Fatalf("WriteFile config.toml error = %v", err)
	}
	writeRemoteControlEnrollmentWithScope(t, home, "ws://localhost:1234/backend-api/wham/remote/control/server", "account-id", "", true)

	if err := runAppServerUntilCanceled([]string{"app-server", "--listen", "off"}); err != nil {
		t.Fatalf("app-server --listen off configured remote-control URL error = %v", err)
	}
}

func TestAppServerListenOffRemoteControlUsesStateDBWhenAvailable(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	writeRemoteControlEnrollment(t, home, true)
	if err := runAppServerUntilCanceled([]string{"app-server", "--listen", "off", "--remote-control"}); err != nil {
		t.Fatalf("app-server --listen off --remote-control with state db error = %v", err)
	}
}

func TestAppServerRemoteControlFlagHonorsRequirements(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte("allow_remote_control = false\n"), 0o600); err != nil {
		t.Fatalf("WriteFile requirements returned error: %v", err)
	}

	var stdout bytes.Buffer
	err := Run(context.Background(), []string{"app-server", "--remote-control"}, strings.NewReader(appServerRemoteControlStatusInput()), &stdout, &bytes.Buffer{})
	if err == nil || err.Error() != "remote control is disabled by managed requirements" {
		t.Fatalf("app-server --remote-control requirements error = %v", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
}

func TestAppServerRootConfigOverrideHonorsRequirements(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	err := Run(context.Background(), []string{
		"--strict-config",
		"-c", "requirements.allow_remote_control=false",
		"app-server",
		"--remote-control",
	}, strings.NewReader(appServerRemoteControlStatusInput()), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || err.Error() != "remote control is disabled by managed requirements" {
		t.Fatalf("app-server root requirements override error = %v", err)
	}
}

func TestAppServerListenOffRequirementsError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte("allow_remote_control = false\n"), 0o600); err != nil {
		t.Fatalf("WriteFile requirements returned error: %v", err)
	}
	err := Run(context.Background(), []string{"app-server", "--listen", "off"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || err.Error() != "no transport configured; remote control disabled by managed requirements" {
		t.Fatalf("app-server --listen off requirements error = %v", err)
	}
}

func TestAppServerRemoteControlRPCsHonorRequirements(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte("allow_remote_control = false\n"), 0o600); err != nil {
		t.Fatalf("WriteFile requirements returned error: %v", err)
	}

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"app-server"}, strings.NewReader(appServerRemoteControlStatusInput()), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("app-server returned error: %v", err)
	}
	got := stdout.String()
	if !strings.Contains(got, `"code":-32600`) || !strings.Contains(got, `"message":"remote control is disabled by managed requirements"`) {
		t.Fatalf("stdout = %q", got)
	}
}

func TestAppServerRemoteControlFlagEnablesStartupMode(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	input := appServerRemoteControlStatusInput()
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"app-server", "--remote-control"}, strings.NewReader(input), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("app-server --remote-control returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), `"id":2`) || !strings.Contains(stdout.String(), `"status":"connected"`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestAppServerRemoteControlFlagOverridesDisabledEnv(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("CODEX_INTERNAL_APP_SERVER_REMOTE_CONTROL_DISABLED", "1")
	input := appServerRemoteControlStatusInput()
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"app-server", "--remote-control"}, strings.NewReader(input), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("app-server --remote-control returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), `"status":"connected"`) {
		t.Fatalf("stdout = %q", stdout.String())
	}
	if value := os.Getenv("CODEX_INTERNAL_APP_SERVER_REMOTE_CONTROL_DISABLED"); value != "" {
		t.Fatalf("disabled env = %q, want removed", value)
	}
}

func appServerRemoteControlStatusInput() string {
	return strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"clientInfo":{"name":"test","version":"1"},"capabilities":{"experimentalApi":true}}}`,
		`{"jsonrpc":"2.0","method":"initialized","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"remoteControl/status/read","params":{}}`,
		"",
	}, "\n")
}

func clearAuthEnv(t *testing.T) {
	t.Helper()
	t.Setenv(codexauth.OpenAIAPIKeyEnv, "")
	t.Setenv(codexauth.CodexAPIKeyEnv, "")
	t.Setenv(codexauth.CodexAccessTokenEnv, "")
}

func writeChatGPTAuth(t *testing.T, codexHome string, accountID string) {
	t.Helper()
	authSnapshot := codexauth.FromChatGPTAuthTokens("access-token", accountID, nil)
	if err := codexauth.NewStore(codexHome).Save(authSnapshot); err != nil {
		t.Fatalf("Save auth.json error = %v", err)
	}
}

func writeRemoteControlEnrollment(t *testing.T, codexHome string, enabled bool) {
	t.Helper()
	writeRemoteControlEnrollmentWithScope(t, codexHome, testRemoteControlWebSocketURL, "account-id", "", enabled)
}

func writeRemoteControlEnrollmentWithScope(t *testing.T, codexHome string, websocketURL string, accountID string, appServerClientName string, enabled bool) {
	t.Helper()
	db, err := sql.Open("sqlite", appServerStateDBPath(codexHome))
	if err != nil {
		t.Fatalf("sql.Open error = %v", err)
	}
	defer db.Close()
	if _, err := db.Exec(`
CREATE TABLE remote_control_enrollments (
	websocket_url TEXT NOT NULL,
	account_id TEXT NOT NULL,
	app_server_client_name TEXT NOT NULL,
	server_id TEXT NOT NULL,
	environment_id TEXT NOT NULL,
	server_name TEXT NOT NULL,
	remote_control_enabled INTEGER,
	updated_at INTEGER,
	PRIMARY KEY (websocket_url, account_id, app_server_client_name)
)`); err != nil {
		t.Fatalf("create remote_control_enrollments error = %v", err)
	}
	value := 0
	if enabled {
		value = 1
	}
	if _, err := db.Exec(`
INSERT INTO remote_control_enrollments (
	websocket_url, account_id, app_server_client_name, server_id, environment_id,
	server_name, remote_control_enabled, updated_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		websocketURL, accountID, appServerClientName,
		"server-id", "environment-id", "server-name", value, int64(1),
	); err != nil {
		t.Fatalf("insert remote_control_enrollments error = %v", err)
	}
}

func runAppServerUntilCanceled(args []string) error {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- Run(ctx, args, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	}()
	timer := time.NewTimer(2 * time.Second)
	defer timer.Stop()
	select {
	case err := <-done:
		cancel()
		return err
	case <-timer.C:
		cancel()
		return <-done
	}
}

func TestAppServerRuntimeOptionsRequireFeatureForRemoteCodeModeHost(t *testing.T) {
	opts := cli.AppServerOptions{CodeModeHostURL: "ws://127.0.0.1:8765"}
	disabled := &config.Config{Values: map[string]any{"features": map[string]any{"code_mode_host": false}}}
	if _, err := appServerRuntimeOptionsFromCLI(opts, disabled); err == nil || !strings.Contains(err.Error(), "requires the code_mode_host feature") {
		t.Fatalf("disabled feature error = %v", err)
	}
	cfg := &config.Config{Values: map[string]any{}}
	got, err := appServerRuntimeOptionsFromCLI(opts, cfg)
	if err != nil {
		t.Fatalf("appServerRuntimeOptionsFromCLI() error = %v", err)
	}
	if got.CodeModeHostURL != opts.CodeModeHostURL || got.CodeModeHostHTTPClient == nil || !got.CodeModeHostEnabled || !got.DisableCodeModeInProcessFallback {
		t.Fatalf("runtime options = %#v", got)
	}
}

func TestAppServerGenerateArtifacts(t *testing.T) {
	dir := t.TempDir()
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"app-server", "generate-ts", "--out", dir}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("generate-ts returned error: %v", err)
	}
	tsData, err := os.ReadFile(filepath.Join(dir, "ClientRequest.ts"))
	if err != nil {
		t.Fatalf("ReadFile ClientRequest.ts error = %v", err)
	}
	if !strings.Contains(string(tsData), "ClientRequest") {
		t.Fatalf("ClientRequest.ts = %q", string(tsData))
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"app-server", "generate-json-schema", "--out", dir, "--experimental"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("generate-json-schema returned error: %v", err)
	}
	jsonData, err := os.ReadFile(filepath.Join(dir, "ClientRequest.json"))
	if err != nil {
		t.Fatalf("ReadFile ClientRequest.json error = %v", err)
	}
	if !strings.Contains(string(jsonData), `"thread/realtime/start"`) {
		t.Fatalf("ClientRequest.json = %q", string(jsonData))
	}
}
