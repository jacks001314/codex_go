package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/coder/websocket"

	"codex_go/internal/appserver"
	"codex_go/internal/appserverdaemon"
	"codex_go/internal/auth"
	"codex_go/internal/cli"
	"codex_go/internal/config"
	codexexec "codex_go/internal/exec"
	"codex_go/internal/mcp"
	"codex_go/internal/protocol"
	"codex_go/internal/sandbox"
	"codex_go/internal/session"
	"codex_go/internal/tool"
	codextui "codex_go/internal/tui"
	bottompane "codex_go/internal/tui/bottom_pane"
	codextea "codex_go/internal/tui/tea"
	"codex_go/internal/turn"
)

func TestLoginStatusLogoutFlow(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"login", "--with-api-key"}, strings.NewReader("sk-test\n"), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("login returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), auth.LoginFlowSuccessMessage) {
		t.Fatalf("login stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"login", "status"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("status returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Logged in using an API key") {
		t.Fatalf("status stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"logout"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("logout returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Successfully logged out") {
		t.Fatalf("logout stdout = %q", stdout.String())
	}
}

func TestLoginStatusNotLoggedInReturnsExitError(t *testing.T) {
	clearAuthEnvApp(t)
	t.Setenv("CODEX_HOME", t.TempDir())
	var stdout bytes.Buffer
	err := Run(context.Background(), []string{"login", "status"}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 || !exitErr.Silent {
		t.Fatalf("status error = %+v, want silent exit 1", err)
	}
	if !strings.Contains(stdout.String(), "Not logged in") {
		t.Fatalf("status stdout = %q", stdout.String())
	}
}

func TestLoginStatusAuthStorageErrorUsesExitMessage(t *testing.T) {
	clearAuthEnvApp(t)
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(filepath.Join(home, "auth.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("WriteFile(auth.json) error = %v", err)
	}

	err := Run(context.Background(), []string{"login", "status"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 || exitErr.Silent {
		t.Fatalf("status error = %+v, want printable exit 1", err)
	}
	if !strings.Contains(err.Error(), "Error checking login status:") {
		t.Fatalf("status error = %v, want Rust status prefix", err)
	}
}

func TestLoginStatusUsesEnv(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "sk-env-secret")
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"login", "status"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("status returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "***") {
		t.Fatalf("status stdout = %q", stdout.String())
	}
}

func TestLoginUsesConfiguredKeyringAuthStore(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"-c", `cli_auth_credentials_store="keyring"`, "login", "--with-api-key"}, strings.NewReader("sk-keyring\n"), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("login returned error: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "auth.json")); !os.IsNotExist(err) {
		t.Fatalf("auth.json should not exist for keyring login: %v", err)
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"-c", `cli_auth_credentials_store="keyring"`, "login", "status"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("status returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Logged in using an API key") {
		t.Fatalf("status stdout = %q", stdout.String())
	}
}

func TestLoginCredentialSourceConflictsMatchRustExit(t *testing.T) {
	clearAuthEnvApp(t)
	t.Setenv("CODEX_HOME", t.TempDir())

	err := Run(context.Background(), []string{"login", "--with-api-key", "--with-access-token"}, strings.NewReader("sk-test\n"), &bytes.Buffer{}, &bytes.Buffer{})
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 || exitErr.Silent {
		t.Fatalf("login conflict error = %+v, want printable exit 1", err)
	}
	if err.Error() != loginCredentialSourceConflict {
		t.Fatalf("login conflict error = %q, want %q", err.Error(), loginCredentialSourceConflict)
	}
}

func TestLoginDeprecatedAPIKeyFlagMatchesRustExit(t *testing.T) {
	clearAuthEnvApp(t)
	t.Setenv("CODEX_HOME", t.TempDir())

	err := Run(context.Background(), []string{"login", "--api-key", "sk-test"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 || exitErr.Silent {
		t.Fatalf("login --api-key error = %+v, want printable exit 1", err)
	}
	if err.Error() != apiKeyFlagUnsupportedMessage {
		t.Fatalf("login --api-key error = %q, want %q", err.Error(), apiKeyFlagUnsupportedMessage)
	}
}

func TestLoginWithAPIKeyEmptyStdinMatchesRustExit(t *testing.T) {
	clearAuthEnvApp(t)
	t.Setenv("CODEX_HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"login", "--with-api-key"}, strings.NewReader(" \n"), &stdout, &stderr)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 || !exitErr.Silent {
		t.Fatalf("login empty stdin error = %+v, want silent exit 1", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	for _, want := range []string{apiKeyStdinReadingMessage, apiKeyStdinEmptyMessage} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, missing %q", stderr.String(), want)
		}
	}
}

func TestLoginWithAPIKeyTTYStdinMatchesRustExit(t *testing.T) {
	clearAuthEnvApp(t)
	t.Setenv("CODEX_HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"login", "--with-api-key"}, newTerminalReader("sk-test\n"), &stdout, &stderr)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 || !exitErr.Silent {
		t.Fatalf("login tty stdin error = %+v, want silent exit 1", err)
	}
	if stdout.Len() != 0 {
		t.Fatalf("stdout = %q, want empty", stdout.String())
	}
	if strings.TrimSpace(stderr.String()) != apiKeyStdinTerminalMessage {
		t.Fatalf("stderr = %q, want terminal message", stderr.String())
	}
}

func TestLoginWithAccessTokenEmptyStdinMatchesRustExit(t *testing.T) {
	clearAuthEnvApp(t)
	t.Setenv("CODEX_HOME", t.TempDir())

	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"login", "--with-access-token"}, strings.NewReader(""), &stdout, &stderr)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 || !exitErr.Silent {
		t.Fatalf("login empty access token error = %+v, want silent exit 1", err)
	}
	for _, want := range []string{accessTokenStdinReadingMessage, accessTokenStdinEmptyMessage} {
		if !strings.Contains(stderr.String(), want) {
			t.Fatalf("stderr = %q, missing %q", stderr.String(), want)
		}
	}
}

func TestLogoutConfigErrorUsesRustExitMessage(t *testing.T) {
	clearAuthEnvApp(t)
	t.Setenv("CODEX_HOME", t.TempDir())

	err := Run(context.Background(), []string{"logout", "-c", "bad"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	assertPrintableExitOne(t, err)
	if !strings.Contains(err.Error(), "Error loading configuration:") {
		t.Fatalf("logout error = %v, want Rust config prefix", err)
	}
}

func TestLoginRejectsForcedMethod(t *testing.T) {
	clearAuthEnvApp(t)
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	var stdout bytes.Buffer
	err := Run(context.Background(), []string{"-c", `forced_login_method="chatgpt"`, "login", "--with-api-key"}, strings.NewReader("sk-test\n"), &stdout, &bytes.Buffer{})
	if err == nil || err.Error() != apiKeyLoginDisabledMessage {
		t.Fatalf("api key forced method error = %v", err)
	}
	assertPrintableExitOne(t, err)

	stdout.Reset()
	err = Run(context.Background(), []string{"-c", `forced_login_method="api"`, "login", "--with-access-token"}, strings.NewReader("at-test\n"), &stdout, &bytes.Buffer{})
	if err == nil || err.Error() != accessTokenLoginDisabledMessage {
		t.Fatalf("access token forced method error = %v", err)
	}
	assertPrintableExitOne(t, err)

	stdout.Reset()
	err = Run(context.Background(), []string{"-c", `forced_login_method="api"`, "login", "--device-auth", "--experimental_issuer", "http://127.0.0.1"}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if err == nil || err.Error() != chatGPTLoginDisabledMessage {
		t.Fatalf("device auth forced method error = %v", err)
	}
	assertPrintableExitOne(t, err)
}

func TestLoginWithAccessTokenWritesOnlyPersonalAccessToken(t *testing.T) {
	clearAuthEnvApp(t)
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	requests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.URL.Path != "/v1/user-auth-credential/whoami" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer at-login-test" {
			t.Fatalf("Authorization = %q", got)
		}
		writeJSON(t, w, map[string]any{
			"email":                      "me@example.com",
			"chatgpt_user_id":            "user-123",
			"chatgpt_account_id":         "workspace-allowed",
			"chatgpt_plan_type":          "pro",
			"chatgpt_account_is_fedramp": false,
		})
	}))
	defer server.Close()
	t.Setenv(auth.AuthAPIBaseURLEnv, server.URL)

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"-c", `forced_chatgpt_workspace_id=["workspace-allowed"]`, "login", "--with-access-token"}, strings.NewReader("at-login-test\n"), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("login returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), auth.LoginFlowSuccessMessage) {
		t.Fatalf("login stdout = %q", stdout.String())
	}
	var stored map[string]any
	data, err := os.ReadFile(filepath.Join(home, "auth.json"))
	if err != nil {
		t.Fatalf("ReadFile auth.json returned error: %v", err)
	}
	if err := json.Unmarshal(data, &stored); err != nil {
		t.Fatalf("Unmarshal auth.json returned error: %v", err)
	}
	if stored["personal_access_token"] != "at-login-test" {
		t.Fatalf("personal_access_token = %#v", stored["personal_access_token"])
	}
	if _, ok := stored["auth_mode"]; ok {
		t.Fatalf("auth_mode should be omitted: %s", string(data))
	}
	if _, ok := stored["tokens"]; ok {
		t.Fatalf("tokens should be omitted: %s", string(data))
	}
	if requests != 1 {
		t.Fatalf("whoami requests = %d, want 1", requests)
	}
}

func TestLoginWithAccessTokenRejectsForcedWorkspaceMismatch(t *testing.T) {
	clearAuthEnvApp(t)
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(t, w, map[string]any{
			"chatgpt_user_id":            "user-123",
			"chatgpt_account_id":         "workspace-denied",
			"chatgpt_plan_type":          "pro",
			"chatgpt_account_is_fedramp": false,
		})
	}))
	defer server.Close()
	t.Setenv(auth.AuthAPIBaseURLEnv, server.URL)

	var stdout bytes.Buffer
	err := Run(context.Background(), []string{"-c", `forced_chatgpt_workspace_id=["workspace-allowed"]`, "login", "--with-access-token"}, strings.NewReader("at-login-test\n"), &stdout, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "Login is restricted to workspace id(s) workspace-allowed.") {
		t.Fatalf("login error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, "auth.json")); !os.IsNotExist(statErr) {
		t.Fatalf("auth.json should not exist after rejected login: %v", statErr)
	}
}

func TestLoginChatGPTDeviceAuth(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			writeJSON(t, w, map[string]string{
				"device_auth_id": "device-1",
				"user_code":      "CODEX-GO",
				"interval":       "1",
			})
		case "/api/accounts/deviceauth/token":
			writeJSON(t, w, map[string]string{
				"authorization_code": "auth-code",
				"code_challenge":     "challenge",
				"code_verifier":      "verifier",
			})
		case "/oauth/token":
			writeJSON(t, w, map[string]string{
				"id_token":      "header.eyJjaGF0Z3B0X2FjY291bnRfaWQiOiJhY2NvdW50LXRlc3QifQ.sig",
				"access_token":  "access-token",
				"refresh_token": "refresh-token",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"login", "--device-auth", "--experimental_issuer", server.URL}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("device login returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "CODEX-GO") {
		t.Fatalf("device stdout = %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), auth.LoginFlowSuccessMessage) {
		t.Fatalf("device stdout missing success message: %q", stdout.String())
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"login", "status"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("status returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Logged in using ChatGPT") {
		t.Fatalf("status stdout = %q", stdout.String())
	}
}

func TestLoginChatGPTClearsExistingAuthBeforeDeviceAuth(t *testing.T) {
	clearAuthEnvApp(t)
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := auth.PersistChatGPTTokens(home, &auth.ExchangedTokens{
		IDToken:      fakeJWTApp(map[string]any{"chatgpt_account_id": "old-account"}),
		AccessToken:  "old-access",
		RefreshToken: "old-refresh",
	}); err != nil {
		t.Fatalf("PersistChatGPTTokens() error = %v", err)
	}
	revokeRequests := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/oauth/revoke":
			revokeRequests++
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Fatalf("Decode revoke body error = %v", err)
			}
			if body["token"] != "old-refresh" {
				t.Fatalf("revoke body = %#v", body)
			}
			writeJSON(t, w, map[string]string{"message": "success"})
		case "/api/accounts/deviceauth/usercode":
			writeJSON(t, w, map[string]string{
				"device_auth_id": "device-1",
				"user_code":      "CODEX-GO",
				"interval":       "1",
			})
		case "/api/accounts/deviceauth/token":
			writeJSON(t, w, map[string]string{
				"authorization_code": "auth-code",
				"code_challenge":     "challenge",
				"code_verifier":      "verifier",
			})
		case "/oauth/token":
			writeJSON(t, w, map[string]string{
				"id_token":      fakeJWTApp(map[string]any{"chatgpt_account_id": "new-account"}),
				"access_token":  "new-access",
				"refresh_token": "new-refresh",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	t.Setenv(auth.RevokeTokenURLEnvOverride, server.URL+"/oauth/revoke")

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"login", "--device-auth", "--experimental_issuer", server.URL}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("device login returned error: %v", err)
	}
	if revokeRequests != 1 {
		t.Fatalf("revoke requests = %d, want 1", revokeRequests)
	}
	loaded, err := auth.NewStore(home).Load()
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if loaded == nil || loaded.Tokens["access_token"] != "new-access" || loaded.Tokens["refresh_token"] != "new-refresh" {
		t.Fatalf("loaded auth = %+v", loaded)
	}
}

func TestLoginChatGPTDeviceAuthHonorsForcedWorkspaceConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(config.ConfigPath(home), []byte(`forced_chatgpt_workspace_id = ["workspace-allowed"]`), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/accounts/deviceauth/usercode":
			writeJSON(t, w, map[string]string{
				"device_auth_id": "device-1",
				"user_code":      "CODEX-GO",
				"interval":       "1",
			})
		case "/api/accounts/deviceauth/token":
			writeJSON(t, w, map[string]string{
				"authorization_code": "auth-code",
				"code_challenge":     "challenge",
				"code_verifier":      "verifier",
			})
		case "/oauth/token":
			writeJSON(t, w, map[string]string{
				"id_token":      fakeJWTApp(map[string]any{"https://api.openai.com/auth": map[string]any{"chatgpt_account_id": "workspace-denied"}}),
				"access_token":  "access-token",
				"refresh_token": "refresh-token",
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	var stdout bytes.Buffer
	err := Run(context.Background(), []string{"login", "--device-auth", "--experimental_issuer", server.URL}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "Login is restricted to workspace id(s) workspace-allowed.") {
		t.Fatalf("device login error = %v", err)
	}
	if _, statErr := os.Stat(filepath.Join(home, "auth.json")); !os.IsNotExist(statErr) {
		t.Fatalf("auth.json should not exist after rejected login: %v", statErr)
	}
}

func writeJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
}

func fakeJWTApp(payload map[string]any) string {
	header, _ := json.Marshal(map[string]string{"alg": "none"})
	body, _ := json.Marshal(payload)
	return base64.RawURLEncoding.EncodeToString(header) + "." + base64.RawURLEncoding.EncodeToString(body) + ".sig"
}

func clearAuthEnvApp(t *testing.T) {
	t.Helper()
	t.Setenv(auth.OpenAIAPIKeyEnv, "")
	t.Setenv(auth.CodexAPIKeyEnv, "")
	t.Setenv(auth.CodexAccessTokenEnv, "")
}

func assertPrintableExitOne(t *testing.T, err error) {
	t.Helper()
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 || exitErr.Silent {
		t.Fatalf("error = %+v, want printable exit 1", err)
	}
}

func TestUnknownFeatureToggleFails(t *testing.T) {
	err := Run(context.Background(), []string{"--enable", "does_not_exist", "features", "list"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("Run returned nil error, want failure")
	}
	if !strings.Contains(err.Error(), "unknown feature flag") {
		t.Fatalf("error = %v", err)
	}
}

func TestFeaturesList(t *testing.T) {
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"features", "list"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("features list returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "shell_tool") {
		t.Fatalf("features list stdout missing shell_tool: %q", stdout.String())
	}
}

func TestFeaturesEnablePersists(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"features", "enable", "unified_exec"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("features enable returned error: %v", err)
	}
	stdout.Reset()
	if err := Run(context.Background(), []string{"features", "list"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("features list returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "unified_exec") || !strings.Contains(stdout.String(), "true") {
		t.Fatalf("features list stdout = %q", stdout.String())
	}
}

func TestFeaturesListHonorsRootOverrides(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{
		"-c", "features.shell_tool=false",
		"--enable", "unified_exec",
		"features", "list",
	}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("features list returned error: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "shell_tool") || !strings.Contains(output, "false") {
		t.Fatalf("features list did not honor -c override: %q", output)
	}
	if !strings.Contains(output, "unified_exec") || !strings.Contains(output, "true") {
		t.Fatalf("features list did not honor --enable: %q", output)
	}
}

func TestDebugPromptInput(t *testing.T) {
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"debug", "prompt-input", "--image", "a.png", "hello"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("debug prompt-input returned error: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, `"role": "user"`) || !strings.Contains(output, `"text": "hello"`) || !strings.Contains(output, `"a.png"`) {
		t.Fatalf("debug prompt-input output = %q", output)
	}
}

func TestExecJSONEndToEnd(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "sk-test")
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"exec", "--json", "hello"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("exec returned error: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, `"type":"thread.started"`) || !strings.Contains(output, `"type":"turn.completed"`) {
		t.Fatalf("exec json output = %q", output)
	}
}

func TestInteractivePromptUsesExecRunner(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "sk-test")
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"hello", "from", "interactive"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("interactive prompt returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "hello from interactive") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestInteractivePromptNormalizesCRLF(t *testing.T) {
	got := normalizeInteractivePrompt("hello\r\nfrom\rinteractive")
	if got != "hello\nfrom\ninteractive" {
		t.Fatalf("normalized prompt = %q", got)
	}
}

func TestInteractiveWithoutPromptRunsLineSession(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "sk-test")
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{}, strings.NewReader("first turn\nsecond turn\n/exit\n"), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("interactive returned error: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "Codex interactive session") ||
		!strings.Contains(output, "first turn") ||
		!strings.Contains(output, "second turn") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestInteractiveSlashCommandsUpdateTUIState(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "sk-test")
	var stdout bytes.Buffer
	input := strings.Join([]string{
		"/help",
		"/model gpt-test",
		"/approval never",
		"/sandbox :workspace",
		"hello tui",
		"/new",
		"/status",
		"/exit",
	}, "\n") + "\n"
	if err := Run(context.Background(), []string{}, strings.NewReader(input), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("interactive returned error: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{
		"Codex TUI commands:",
		"Model: gpt-test",
		"Approval: never",
		"Sandbox: :workspace",
		"hello tui",
		"Started a new local thread.",
		"Thread: new",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("interactive output = %q, missing %q", output, want)
		}
	}
}

func TestInteractiveSlashCommandRejectsInvalidApproval(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("CODEX_HOME", t.TempDir())
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{}, strings.NewReader("/approval always\n/exit\n"), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("interactive returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Approval must be one of untrusted, on-request, never.") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestInteractiveTUIRequiresRealTerminal(t *testing.T) {
	var stdout terminalBuffer
	if shouldRunInteractiveTUI(newTerminalReader("/exit\n"), &stdout) {
		t.Fatal("fake terminal reader/buffer should use line fallback")
	}
	if shouldRunInteractiveTUI(strings.NewReader("/exit\n"), &bytes.Buffer{}) {
		t.Fatal("plain reader/buffer should use line fallback")
	}
}

func TestInteractiveTurnCommandUsesTUIStateAndResume(t *testing.T) {
	state := codextui.NewState(&codextui.Options{
		Model:           "gpt-tui",
		ReasoningEffort: "high",
		ApprovalPolicy:  "never",
		Sandbox:         "workspace-write",
		Search:          true,
		NoAltScreen:     true,
	})
	state.SetThreadID("thread-1")
	root := &cli.RootOptions{
		Shared: cli.SharedOptions{
			Model:          "old-model",
			ApprovalPolicy: "on-request",
			Sandbox:        "read-only",
		},
	}
	var captured *codexexec.Request
	runner := interactiveTurnRunnerFunc(func(ctx context.Context, req *codexexec.Request, stdin io.Reader, stdout, stderr io.Writer) (*codexexec.Result, error) {
		captured = req
		_ = json.NewEncoder(stdout).Encode(protocol.ThreadStarted("thread-1"))
		_ = json.NewEncoder(stdout).Encode(protocol.AgentMessageDelta("msg-1", "assistant "))
		return &codexexec.Result{ThreadID: "thread-1", LastMessage: "assistant reply"}, nil
	})

	msg := interactiveTurnCommand(context.Background(), root, runner, state, "hello from tui", nil, nil, nil)()
	started, ok := msg.(codextea.StreamStartedMsg)
	if !ok {
		t.Fatalf("message = %T, want StreamStartedMsg", msg)
	}
	completed, sawThreadStarted, sawDelta := collectInteractiveStream(t, started)
	if completed.ThreadID != "thread-1" || completed.AssistantMessage != "assistant reply" || completed.Err != nil {
		t.Fatalf("completed = %#v", completed)
	}
	if !sawThreadStarted || !sawDelta {
		t.Fatalf("stream events sawThreadStarted=%v sawDelta=%v", sawThreadStarted, sawDelta)
	}
	if captured == nil {
		t.Fatal("runner was not called")
	}
	if captured.Exec.Subcommand != "resume" || captured.Exec.Resume.SessionID != "thread-1" || captured.Exec.Resume.Prompt != "hello from tui" {
		t.Fatalf("captured exec resume = %#v", captured.Exec)
	}
	if captured.Exec.Prompt != "" {
		t.Fatalf("captured prompt = %q, want empty resume prompt", captured.Exec.Prompt)
	}
	if !captured.Exec.JSON {
		t.Fatal("captured Exec.JSON = false, want true for stream bridge")
	}
	if captured.Exec.Shared.Model != "gpt-tui" ||
		captured.Exec.Shared.ModelReasoningEffort != "high" ||
		captured.Exec.Shared.ApprovalPolicy != "never" ||
		captured.Exec.Shared.Sandbox != "workspace-write" ||
		!captured.Exec.Shared.Search ||
		!captured.Exec.Shared.NoAltScreen {
		t.Fatalf("captured shared = %#v", captured.Exec.Shared)
	}
	if captured.Root.Shared.Model != "gpt-tui" || captured.Root.Shared.ModelReasoningEffort != "high" {
		t.Fatalf("captured root shared = %#v", captured.Root.Shared)
	}
}

func TestInteractiveTurnCommandUsesPlanModeReasoningOverride(t *testing.T) {
	state := codextui.NewState(&codextui.Options{
		Model:                   "gpt-tui",
		ReasoningEffort:         "medium",
		PlanMode:                true,
		PlanModeReasoningEffort: "high",
	})
	state.SetThreadID("thread-1")
	var captured *codexexec.Request
	runner := interactiveTurnRunnerFunc(func(ctx context.Context, req *codexexec.Request, stdin io.Reader, stdout, stderr io.Writer) (*codexexec.Result, error) {
		captured = req
		_ = json.NewEncoder(stdout).Encode(protocol.ThreadStarted("thread-1"))
		return &codexexec.Result{ThreadID: "thread-1", LastMessage: "ok"}, nil
	})

	msg := interactiveTurnCommand(context.Background(), &cli.RootOptions{}, runner, state, "hello from plan", nil, nil, nil)()
	started, ok := msg.(codextea.StreamStartedMsg)
	if !ok {
		t.Fatalf("message = %T, want StreamStartedMsg", msg)
	}
	completed, _, _ := collectInteractiveStream(t, started)
	if completed.Err != nil {
		t.Fatalf("completed error = %v", completed.Err)
	}
	if captured == nil {
		t.Fatal("runner was not called")
	}
	if captured.Exec.Shared.ModelReasoningEffort != "high" || captured.Root.Shared.ModelReasoningEffort != "high" {
		t.Fatalf("captured shared exec=%#v root=%#v", captured.Exec.Shared, captured.Root.Shared)
	}
	if state.ReasoningEffort != "medium" {
		t.Fatalf("global reasoning = %q, want medium", state.ReasoningEffort)
	}
}

func TestInteractiveTurnCommandPassesStructuredAttachments(t *testing.T) {
	state := codextui.NewState(nil)
	var captured *codexexec.Request
	runner := interactiveTurnRunnerFunc(func(ctx context.Context, req *codexexec.Request, stdin io.Reader, stdout, stderr io.Writer) (*codexexec.Result, error) {
		captured = req
		_ = json.NewEncoder(stdout).Encode(protocol.ThreadStarted("thread-attachments"))
		return &codexexec.Result{ThreadID: "thread-attachments", LastMessage: "ok"}, nil
	})

	request := codextea.SubmitRequest{
		Prompt: "review these",
		Attachments: []bottompane.ComposerAttachment{{
			Kind: bottompane.AttachmentRemoteImage,
			URL:  "https://example.test/preview.png",
		}, {
			Kind: bottompane.AttachmentImage,
			Path: `D:\repo\diagram.png`,
		}, {
			Kind: bottompane.AttachmentFile,
			Path: `D:\repo\notes.md`,
		}},
	}
	msg := interactiveTurnCommandWithRequest(context.Background(), &cli.RootOptions{}, runner, state, request, nil, nil, nil)()
	started, ok := msg.(codextea.StreamStartedMsg)
	if !ok {
		t.Fatalf("message = %T, want StreamStartedMsg", msg)
	}
	completed, _, _ := collectInteractiveStream(t, started)
	if completed.Err != nil {
		t.Fatalf("completed error = %v", completed.Err)
	}
	if captured == nil {
		t.Fatal("runner was not called")
	}
	if captured.Exec.Prompt != "" {
		t.Fatalf("Exec.Prompt = %q, want empty when structured input is present", captured.Exec.Prompt)
	}
	if len(captured.Input) != 4 {
		t.Fatalf("captured input = %#v", captured.Input)
	}
	if captured.Input[0].Type != "image" || captured.Input[0].URL != "https://example.test/preview.png" {
		t.Fatalf("remote image input = %#v", captured.Input[0])
	}
	if captured.Input[1].Type != "localImage" || captured.Input[1].Path != `D:\repo\diagram.png` {
		t.Fatalf("local image input = %#v", captured.Input[1])
	}
	if captured.Input[2].Type != "text" || !strings.Contains(captured.Input[2].Text, `D:\repo\notes.md`) {
		t.Fatalf("file input = %#v", captured.Input[2])
	}
	if captured.Input[3].Type != "text" || captured.Input[3].Text != "review these" {
		t.Fatalf("prompt input = %#v", captured.Input[3])
	}
}

func TestInteractiveTurnCommandReportsNilRunner(t *testing.T) {
	msg := interactiveTurnCommand(context.Background(), &cli.RootOptions{}, nil, codextui.NewState(nil), "hello", nil, nil, nil)()
	started, ok := msg.(codextea.StreamStartedMsg)
	if !ok {
		t.Fatalf("message = %T, want StreamStartedMsg", msg)
	}
	completed, _, _ := collectInteractiveStream(t, started)
	if completed.Err == nil || !strings.Contains(completed.Err.Error(), "interactive runner is nil") {
		t.Fatalf("completed error = %v", completed.Err)
	}
}

func TestInteractiveStreamEventWriterParsesJSONLines(t *testing.T) {
	var messages []any
	writer := newInteractiveStreamEventWriter(func(message bubbletea.Msg) {
		messages = append(messages, message)
	})
	_, _ = writer.Write([]byte(`{"type":"thread.started","thread_id":"thread-1"}` + "\n"))
	_, _ = writer.Write([]byte(`{"type":"item.delta","delta":{"item_id":"msg-1","text":"hi"}}`))
	writer.Flush()

	if len(messages) != 2 {
		t.Fatalf("message len = %d, want 2 (%#v)", len(messages), messages)
	}
	first, ok := messages[0].(codextea.ThreadEventMsg)
	if !ok || first.Event.Type != "thread.started" || first.Event.ThreadID != "thread-1" {
		t.Fatalf("first message = %#v", messages[0])
	}
	second, ok := messages[1].(codextea.ThreadEventMsg)
	if !ok || second.Event.Delta == nil || second.Event.Delta.Text != "hi" {
		t.Fatalf("second message = %#v", messages[1])
	}
}

func TestInteractiveApprovalBrokerApprovesAndCachesSession(t *testing.T) {
	broker := newInteractiveApprovalBroker()
	sent := make(chan bubbletea.Msg, 1)
	result := make(chan struct {
		decision tool.ShellApprovalDecision
		err      error
	}, 1)
	callback := broker.shellApprovalFunc(func(message bubbletea.Msg) {
		sent <- message
	})
	request := &tool.ShellApprovalRequest{Request: &tool.ShellRequest{
		HookCommand:        "go test ./...",
		CWD:                `D:\repo`,
		SandboxPermissions: sandbox.SandboxPermissionsRequireEscalated,
		ApprovalReason:     "command requested escalated sandbox permissions",
		Justification:      "needs network",
	}}
	go func() {
		decision, err := callback(context.Background(), request)
		result <- struct {
			decision tool.ShellApprovalDecision
			err      error
		}{decision: decision, err: err}
	}()

	message := <-sent
	approval, ok := message.(codextea.ApprovalRequestMsg)
	if !ok {
		t.Fatalf("message = %T, want ApprovalRequestMsg", message)
	}
	if approval.ID == "" || !strings.Contains(approval.Body, "needs network") || approval.Command != "go test ./..." {
		t.Fatalf("approval message = %#v", approval)
	}
	broker.respond(codextea.ModalResponse{ID: approval.ID, Kind: codextea.ModalKindApproval, OptionID: "allow_session"})
	got := <-result
	if got.err != nil || !got.decision.Approved || !got.decision.AllowSession {
		t.Fatalf("decision = %#v err=%v", got.decision, got.err)
	}

	decision, err := callback(context.Background(), request)
	if err != nil || !decision.Approved || !decision.AllowSession {
		t.Fatalf("cached decision = %#v err=%v", decision, err)
	}
}

func TestInteractiveElicitationBrokerReturnsMCPResponse(t *testing.T) {
	broker := newInteractiveElicitationBroker()
	sent := make(chan bubbletea.Msg, 1)
	result := make(chan struct {
		response *mcp.MCPElicitationResponse
		err      error
	}, 1)
	callback := broker.mcpElicitationFunc(func(message bubbletea.Msg) {
		sent <- message
	})
	request := &mcp.MCPElicitationRequest{
		ServerName: "docs",
		ThreadID:   "thread-1",
		TurnID:     "turn-1",
		Message:    "Allow docs search?",
		RequestedSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"decision": map[string]any{
					"type": "string",
					"anyOf": []any{
						map[string]any{"const": "approve_once", "title": "Allow once"},
						map[string]any{"const": "approve_session", "title": "Allow session"},
						map[string]any{"const": "decline", "title": "Decline"},
						map[string]any{"const": "cancel", "title": "Cancel"},
					},
				},
			},
		},
	}
	go func() {
		response, err := callback.HandleMCPElicitation(context.Background(), request)
		result <- struct {
			response *mcp.MCPElicitationResponse
			err      error
		}{response: response, err: err}
	}()

	message := <-sent
	elicitation, ok := message.(codextea.ElicitationRequestMsg)
	if !ok {
		t.Fatalf("message = %T, want ElicitationRequestMsg", message)
	}
	if elicitation.ID == "" || elicitation.ServerName != "docs" || elicitation.ThreadID != "thread-1" || elicitation.Message != "Allow docs search?" {
		t.Fatalf("elicitation message = %#v", elicitation)
	}
	broker.respond(codextea.ModalResponse{
		ID:       elicitation.ID,
		Kind:     codextea.ModalKindElicitation,
		OptionID: "approve_session",
		Elicitation: &codextea.ElicitationDecision{
			Action:  "accept",
			Persist: "session",
		},
	})
	got := <-result
	if got.err != nil || got.response == nil || got.response.Action != mcp.MCPElicitationActionAccept {
		t.Fatalf("response = %#v err=%v", got.response, got.err)
	}
	meta, ok := got.response.Meta.(map[string]any)
	if !ok || meta["persist"] != "session" {
		t.Fatalf("meta = %#v", got.response.Meta)
	}
}

func TestInteractiveUserInputBrokerReturnsToolResponse(t *testing.T) {
	broker := newInteractiveUserInputBroker()
	messages := make(chan bubbletea.Msg, 1)
	done := make(chan *tool.UserInputResponse, 1)
	go func() {
		response, err := broker.userInputResponder(func(message bubbletea.Msg) {
			messages <- message
		})(context.Background(), &tool.RequestUserInputArgs{
			Questions: []tool.UserInputQuestion{{
				Header:   "Scope",
				ID:       "scope",
				Question: "Where should this apply?",
				Options:  []tool.UserInputChoice{{Label: "Plan"}, {Label: "All"}},
			}},
		})
		if err != nil {
			t.Errorf("userInputResponder() error = %v", err)
			done <- nil
			return
		}
		done <- response
	}()
	message := <-messages
	request, ok := message.(codextea.RequestUserInputMsg)
	if !ok {
		t.Fatalf("message = %T, want RequestUserInputMsg", message)
	}
	broker.respond(codextea.ModalResponse{
		ID:   request.ID,
		Kind: codextea.ModalKindUserInput,
		UserInput: &codextea.UserInputDecision{
			Answers:     map[string]string{"scope": "All"},
			AnswerLists: map[string][]string{"scope": []string{"All", "user_note: include tests"}},
		},
	})
	response := <-done
	if response == nil || response.Answers["scope"] != "All" {
		t.Fatalf("response = %#v", response)
	}
	if got := response.StructuredAnswers["scope"]; len(got) != 2 || got[0] != "All" || got[1] != "user_note: include tests" {
		t.Fatalf("structured response = %#v", response)
	}
}

func TestInteractiveSessionActionHandlerMutatesLocalStore(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	store := session.NewStore(filepath.Join(auth.DefaultCodexHome(), "sessions"))
	now := fixedAppSessionTime()
	if err := store.Save(&session.Record{
		ID:        "thread-action",
		Title:     "Action",
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{CWD: `D:\repo\a`, Source: "cli"},
		Items:     []session.Item{{ID: "item-1", Type: "user_message", Text: "hello"}},
	}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	handler := interactiveSessionActionHandler(nil)

	if _, err := handler(codextui.SessionSelection{
		Kind:   codextui.SessionSelectionArchive,
		Target: codextui.SessionTarget{ThreadID: "thread-action"},
	}); err != nil {
		t.Fatalf("archive action error = %v", err)
	}
	archived, err := store.Read("thread-action", true, false)
	if err != nil || !archived.Archived {
		t.Fatalf("archived record = %#v err=%v", archived, err)
	}

	summary, err := handler(codextui.SessionSelection{
		Kind:   codextui.SessionSelectionUnarchive,
		Target: codextui.SessionTarget{ThreadID: "thread-action"},
	})
	if err != nil {
		t.Fatalf("unarchive action error = %v", err)
	}
	if summary == nil || summary.ThreadID != "thread-action" || summary.Archived {
		t.Fatalf("unarchive summary = %#v", summary)
	}

	forked, err := handler(codextui.SessionSelection{
		Kind:   codextui.SessionSelectionFork,
		Target: codextui.SessionTarget{ThreadID: "thread-action"},
	})
	if err != nil {
		t.Fatalf("fork action error = %v", err)
	}
	if forked == nil || forked.ThreadID == "" || forked.ThreadID == "thread-action" {
		t.Fatalf("forked summary = %#v", forked)
	}

	if _, err := handler(codextui.SessionSelection{
		Kind:   codextui.SessionSelectionDelete,
		Target: codextui.SessionTarget{ThreadID: "thread-action"},
	}); err != nil {
		t.Fatalf("delete action error = %v", err)
	}
	if _, err := store.Read("thread-action", true, false); err == nil {
		t.Fatal("delete action left source record behind")
	}
}

func collectInteractiveStream(t *testing.T, started codextea.StreamStartedMsg) (codextea.TurnCompletedMsg, bool, bool) {
	t.Helper()
	var completed codextea.TurnCompletedMsg
	var sawCompleted bool
	var sawThreadStarted bool
	var sawDelta bool
	for message := range started.Messages {
		switch msg := message.(type) {
		case codextea.ThreadEventMsg:
			if msg.Event.Type == "thread.started" {
				sawThreadStarted = true
			}
			if msg.Event.Delta != nil && msg.Event.Delta.Text != "" {
				sawDelta = true
			}
		case codextea.TurnCompletedMsg:
			completed = msg
			sawCompleted = true
		}
	}
	if !sawCompleted {
		t.Fatal("stream did not include TurnCompletedMsg")
	}
	return completed, sawThreadStarted, sawDelta
}

type interactiveTurnRunnerFunc func(ctx context.Context, req *codexexec.Request, stdin io.Reader, stdout, stderr io.Writer) (*codexexec.Result, error)

func (f interactiveTurnRunnerFunc) RunContext(ctx context.Context, req *codexexec.Request, stdin io.Reader, stdout, stderr io.Writer) (*codexexec.Result, error) {
	return f(ctx, req, stdin, stdout, stderr)
}

func TestInteractiveDumbTerminalWithoutTTYFailsLikeRust(t *testing.T) {
	t.Setenv("TERM", "dumb")
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{}, strings.NewReader("/exit\n"), &stdout, &stderr)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 || !exitErr.Silent {
		t.Fatalf("error = %+v, want silent exit 1", err)
	}
	if !strings.Contains(stderr.String(), "ERROR: "+interactiveDumbTerminalNoTTYMessage) {
		t.Fatalf("stderr = %q, want Rust fatal error", stderr.String())
	}
}

func TestInteractiveDumbTerminalTTYConfirmationLikeRust(t *testing.T) {
	t.Setenv("TERM", "dumb")
	var stdout terminalBuffer
	var stderr terminalBuffer
	err := runInteractive(context.Background(), &cli.RootOptions{}, newTerminalReader("no\n"), &stdout, &stderr)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 || !exitErr.Silent {
		t.Fatalf("decline error = %+v, want silent exit 1", err)
	}
	if !strings.Contains(stderr.String(), `WARNING: TERM is set to "dumb".`) || !strings.Contains(stderr.String(), "Continue anyway? [y/N]:") {
		t.Fatalf("stderr = %q", stderr.String())
	}
	if !strings.Contains(stderr.String(), "ERROR: "+interactiveDumbTerminalRefusedMessage) {
		t.Fatalf("stderr = %q, want Rust fatal error", stderr.String())
	}

	stdout.Reset()
	stderr.Reset()
	err = runInteractive(context.Background(), &cli.RootOptions{}, newTerminalReader("yes\n/exit\n"), &stdout, &stderr)
	if err != nil {
		t.Fatalf("accept error = %v", err)
	}
	if !strings.Contains(stdout.String(), "Codex interactive session") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestInteractiveRemoteAuthRequiresRemoteAtRuntime(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--remote-auth-token-env", "CODEX_REMOTE_AUTH_TOKEN"}, strings.NewReader(""), &stdout, &stderr)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) || exitErr.Code != 1 || !exitErr.Silent {
		t.Fatalf("error = %+v, want silent exit 1", err)
	}
	if !strings.Contains(stderr.String(), "ERROR: `--remote-auth-token-env` requires `--remote`.") {
		t.Fatalf("stderr = %q, want Rust fatal error", stderr.String())
	}
}

func TestResolveInteractiveRemoteEndpointWebSocketAuth(t *testing.T) {
	t.Setenv("CODEX_REMOTE_AUTH_TOKEN", "  bearer-token  \n")
	endpoint, err := resolveInteractiveRemoteEndpoint(&cli.RootOptions{
		Remote:        "wss://example.com:443",
		RemoteAuthEnv: "CODEX_REMOTE_AUTH_TOKEN",
	})
	if err != nil {
		t.Fatalf("resolveInteractiveRemoteEndpoint returned error: %v", err)
	}
	if endpoint.Kind != appserverdaemon.RemoteEndpointWebSocket {
		t.Fatalf("endpoint kind = %q", endpoint.Kind)
	}
	if endpoint.WebSocketURL != "wss://example.com/" {
		t.Fatalf("websocket URL = %q", endpoint.WebSocketURL)
	}
	if endpoint.AuthToken == nil || *endpoint.AuthToken != "bearer-token" {
		t.Fatalf("auth token = %#v", endpoint.AuthToken)
	}
}

func TestResolveInteractiveRemoteEndpointUnixDefault(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	endpoint, err := resolveInteractiveRemoteEndpoint(&cli.RootOptions{Remote: "unix://"})
	if err != nil {
		t.Fatalf("resolveInteractiveRemoteEndpoint returned error: %v", err)
	}
	if endpoint.Kind != appserverdaemon.RemoteEndpointUnixSocket {
		t.Fatalf("endpoint kind = %q", endpoint.Kind)
	}
	want := filepath.Join(home, "app-server-control", "app-server-control.sock")
	if endpoint.SocketPath != want {
		t.Fatalf("socket path = %q, want %q", endpoint.SocketPath, want)
	}
}

func TestResolveInteractiveRemoteEndpointRejectsInvalidRemote(t *testing.T) {
	_, err := resolveInteractiveRemoteEndpoint(&cli.RootOptions{Remote: "https://127.0.0.1:4500"})
	if err == nil {
		t.Fatal("resolveInteractiveRemoteEndpoint returned nil error, want invalid remote")
	}
	if !strings.Contains(err.Error(), "expected `ws://host:port`, `wss://host:port`, `unix://`, or `unix://PATH`") {
		t.Fatalf("error = %q", err.Error())
	}
}

func TestResolveInteractiveRemoteEndpointRejectsNonLoopbackWSAuth(t *testing.T) {
	t.Setenv("CODEX_REMOTE_AUTH_TOKEN", "token")
	_, err := resolveInteractiveRemoteEndpoint(&cli.RootOptions{
		Remote:        "ws://example.com:80",
		RemoteAuthEnv: "CODEX_REMOTE_AUTH_TOKEN",
	})
	if err == nil {
		t.Fatal("resolveInteractiveRemoteEndpoint returned nil error, want auth support error")
	}
	if got := err.Error(); got != "`--remote-auth-token-env` requires a `wss://` or loopback `ws://` remote." {
		t.Fatalf("error = %q", got)
	}
}

func TestInteractiveRemoteTurnStartsThreadThenTurnAndStreamsEvents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	requests := make(chan remoteTUITestRequest, 8)
	authHeaders := make(chan string, 1)
	serverErrs := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		authHeaders <- r.Header.Get("Authorization")
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			remoteTUITestSendErr(serverErrs, err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			req, err := remoteTUITestReadRequest(ctx, conn)
			if err != nil {
				if websocket.CloseStatus(err) == websocket.StatusNormalClosure || websocket.CloseStatus(err) == websocket.StatusGoingAway || errors.Is(err, context.Canceled) {
					return
				}
				remoteTUITestSendErr(serverErrs, err)
				return
			}
			requests <- req
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodThreadStart):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"method":  string(appserver.NotificationThreadStarted),
					"params":  map[string]any{"thread": map[string]any{"id": "thread-remote"}},
				})
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"thread": map[string]any{"id": "thread-remote"}},
				})
			case string(appserver.MethodTurnStart):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"turn": map[string]any{"id": "turn-remote", "items": []any{}, "status": "inProgress"}},
				})
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"method":  string(appserver.NotificationTurnStarted),
					"params":  map[string]any{"threadId": "thread-remote", "turn": map[string]any{"id": "turn-remote", "items": []any{}, "status": "inProgress"}},
				})
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"method":  string(appserver.NotificationAgentMessageDelta),
					"params":  map[string]any{"threadId": "thread-remote", "turnId": "turn-remote", "itemId": "msg-1", "delta": "hello"},
				})
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"method":  string(appserver.NotificationItemCompleted),
					"params":  map[string]any{"threadId": "thread-remote", "turnId": "turn-remote", "item": map[string]any{"id": "msg-1", "type": "agentMessage", "text": "hello"}},
				})
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"method":  string(appserver.NotificationTurnCompleted),
					"params":  map[string]any{"threadId": "thread-remote", "turn": map[string]any{"id": "turn-remote", "items": []any{}, "status": "completed"}},
				})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	token := "remote-token"
	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), &token)
	state := codextui.NewState(&codextui.Options{
		Model:           "gpt-remote",
		ReasoningEffort: "high",
		ApprovalPolicy:  "on-request",
		Sandbox:         "workspace-write",
	})
	root := &cli.RootOptions{
		ConfigOverrides: []string{`model_provider="openai"`},
		Shared: cli.SharedOptions{
			CWD:            `D:\repo`,
			Model:          "gpt-root",
			ApprovalPolicy: "never",
			Sandbox:        "read-only",
		},
	}
	messages := make(chan bubbletea.Msg, 64)
	runInteractiveRemoteTurn(ctx, root, endpoint, state, codextea.SubmitRequest{
		Prompt: "describe this",
		Attachments: []bottompane.ComposerAttachment{
			{Kind: bottompane.AttachmentRemoteImage, URL: "https://example.test/image.png"},
			{Kind: bottompane.AttachmentFile, Path: `D:\repo\notes.md`},
		},
	}, messages)

	if got := remoteTUITestReadString(t, authHeaders); got != "Bearer remote-token" {
		t.Fatalf("Authorization header = %q", got)
	}
	initialize := remoteTUITestReadCapturedRequest(t, requests)
	threadStart := remoteTUITestReadCapturedRequest(t, requests)
	turnStart := remoteTUITestReadCapturedRequest(t, requests)
	if initialize.Method != string(appserver.MethodInitialize) || threadStart.Method != string(appserver.MethodThreadStart) || turnStart.Method != string(appserver.MethodTurnStart) {
		t.Fatalf("methods = %q, %q, %q", initialize.Method, threadStart.Method, turnStart.Method)
	}
	var threadParams appserver.ThreadStartParams
	if err := json.Unmarshal(threadStart.Params, &threadParams); err != nil {
		t.Fatalf("unmarshal thread/start params: %v", err)
	}
	if threadParams.Prompt != "" || threadParams.CWD != `D:\repo` || threadParams.Model != "gpt-remote" {
		t.Fatalf("thread/start params = %+v", threadParams)
	}
	if threadParams.Config["model_provider"] != "openai" || threadParams.Config["model_reasoning_effort"] != "high" {
		t.Fatalf("thread/start config = %#v", threadParams.Config)
	}
	var turnParams turn.TurnStartParams
	if err := json.Unmarshal(turnStart.Params, &turnParams); err != nil {
		t.Fatalf("unmarshal turn/start params: %v", err)
	}
	if turnParams.ThreadID != "thread-remote" || turnParams.Model != "gpt-remote" || turnParams.CWD != `D:\repo` {
		t.Fatalf("turn/start params = %+v", turnParams)
	}
	if turnParams.Effort == nil || *turnParams.Effort != "high" {
		t.Fatalf("turn/start effort = %#v", turnParams.Effort)
	}
	if len(turnParams.Input) != 3 {
		t.Fatalf("turn/start input = %#v", turnParams.Input)
	}
	if turnParams.Input[0].Type != "image" || turnParams.Input[0].URL != "https://example.test/image.png" {
		t.Fatalf("first input = %#v", turnParams.Input[0])
	}
	if turnParams.Input[1].Type != "text" || !strings.Contains(turnParams.Input[1].Text, `D:\repo\notes.md`) {
		t.Fatalf("second input = %#v", turnParams.Input[1])
	}
	if turnParams.Input[2].Type != "text" || turnParams.Input[2].Text != "describe this" {
		t.Fatalf("third input = %#v", turnParams.Input[2])
	}
	var sawDelta, sawCompleted bool
	for message := range messages {
		switch msg := message.(type) {
		case codextea.ThreadEventMsg:
			if msg.Event.Delta != nil && msg.Event.Delta.Text == "hello" {
				sawDelta = true
			}
		case codextea.TurnCompletedMsg:
			if msg.ThreadID == "thread-remote" && msg.Err == nil {
				sawCompleted = true
			}
		}
	}
	if !sawDelta || !sawCompleted {
		t.Fatalf("remote messages sawDelta=%v sawCompleted=%v", sawDelta, sawCompleted)
	}
	if state.ThreadID != "thread-remote" {
		t.Fatalf("state thread id = %q", state.ThreadID)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

func TestInteractiveRemoteTurnUsesExistingThread(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	requests := make(chan remoteTUITestRequest, 4)
	serverErrs := make(chan error, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			remoteTUITestSendErr(serverErrs, err)
			return
		}
		defer conn.Close(websocket.StatusNormalClosure, "")
		for {
			req, err := remoteTUITestReadRequest(ctx, conn)
			if err != nil {
				if websocket.CloseStatus(err) == websocket.StatusNormalClosure || websocket.CloseStatus(err) == websocket.StatusGoingAway || errors.Is(err, context.Canceled) {
					return
				}
				remoteTUITestSendErr(serverErrs, err)
				return
			}
			requests <- req
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodTurnStart):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"turn": map[string]any{"id": "turn-existing", "items": []any{}, "status": "inProgress"}},
				})
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"method":  string(appserver.NotificationTurnCompleted),
					"params":  map[string]any{"threadId": "thread-existing", "turn": map[string]any{"id": "turn-existing", "items": []any{}, "status": "completed"}},
				})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	state := codextui.NewState(nil)
	state.SetThreadID("thread-existing")
	messages := make(chan bubbletea.Msg, 16)
	runInteractiveRemoteTurn(ctx, &cli.RootOptions{}, endpoint, state, codextea.SubmitRequest{Prompt: "continue"}, messages)
	for range messages {
	}
	initialize := remoteTUITestReadCapturedRequest(t, requests)
	turnStart := remoteTUITestReadCapturedRequest(t, requests)
	if initialize.Method != string(appserver.MethodInitialize) || turnStart.Method != string(appserver.MethodTurnStart) {
		t.Fatalf("methods = %q, %q", initialize.Method, turnStart.Method)
	}
	select {
	case extra := <-requests:
		t.Fatalf("unexpected extra request: %s", extra.Method)
	default:
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

type remoteTUITestRequest struct {
	ID     any             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func remoteTUITestReadRequest(ctx context.Context, conn *websocket.Conn) (remoteTUITestRequest, error) {
	var req remoteTUITestRequest
	typ, data, err := conn.Read(ctx)
	if err != nil {
		return req, err
	}
	if typ != websocket.MessageText {
		return req, fmt.Errorf("message type = %v", typ)
	}
	if err := json.Unmarshal(data, &req); err != nil {
		return req, err
	}
	return req, nil
}

func remoteTUITestWrite(ctx context.Context, conn *websocket.Conn, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
		panic(err)
	}
}

func remoteTUITestSendErr(errs chan<- error, err error) {
	select {
	case errs <- err:
	default:
	}
}

func remoteTUITestReadCapturedRequest(t *testing.T, requests <-chan remoteTUITestRequest) remoteTUITestRequest {
	t.Helper()
	select {
	case request := <-requests:
		return request
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for remote TUI request")
		return remoteTUITestRequest{}
	}
}

func remoteTUITestReadString(t *testing.T, values <-chan string) string {
	t.Helper()
	select {
	case value := <-values:
		return value
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for remote TUI value")
		return ""
	}
}

func TestReadRemoteAuthTokenFromEnvVar(t *testing.T) {
	token, err := readRemoteAuthTokenFromEnvVarWith("CODEX_REMOTE_AUTH_TOKEN", func(string) (string, bool) {
		return "  token  ", true
	})
	if err != nil {
		t.Fatalf("readRemoteAuthTokenFromEnvVarWith returned error: %v", err)
	}
	if token != "token" {
		t.Fatalf("token = %q", token)
	}
	_, err = readRemoteAuthTokenFromEnvVarWith("CODEX_REMOTE_AUTH_TOKEN", func(string) (string, bool) {
		return "", false
	})
	if err == nil || !strings.Contains(err.Error(), "is not set") {
		t.Fatalf("missing env error = %v", err)
	}
	_, err = readRemoteAuthTokenFromEnvVarWith("CODEX_REMOTE_AUTH_TOKEN", func(string) (string, bool) {
		return " \n\t ", true
	})
	if err == nil || !strings.Contains(err.Error(), "is empty") {
		t.Fatalf("empty env error = %v", err)
	}
}

func TestExecPromptFromStdinEndToEnd(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "sk-test")
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"exec", "-"}, strings.NewReader("from stdin\n"), &stdout, &stderr); err != nil {
		t.Fatalf("exec returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "from stdin") {
		t.Fatalf("exec stdout = %q", stdout.String())
	}
}

func TestReviewEndToEnd(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "sk-test")
	dir := initGitRepo(t)
	writeFile(t, dir, "tracked.txt", "old\nnew\n")
	git(t, dir, "add", "tracked.txt")
	git(t, dir, "commit", "-m", "Fix bug")
	sha := strings.TrimSpace(git(t, dir, "rev-parse", "HEAD"))
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"-C", dir, "review", "--commit", sha, "--title", "Fix bug"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("review returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Review commit "+sha+" (Fix bug)") {
		t.Fatalf("review stdout = %q", stdout.String())
	}
}

func TestExecReviewEndToEnd(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "sk-test")
	dir := initGitRepo(t)
	writeFile(t, dir, "tracked.txt", "old\nnew\n")
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"exec", "-C", dir, "review", "--uncommitted"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("exec review returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Review uncommitted changes") {
		t.Fatalf("exec review stdout = %q", stdout.String())
	}
}

func TestReviewCustomPromptEndToEnd(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "sk-test")
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"review", "check auth flow"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("review returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Review with custom instructions: check auth flow") {
		t.Fatalf("review stdout = %q", stdout.String())
	}
}

func TestCompletionOutputsScript(t *testing.T) {
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"completion", "bash"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("completion returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "complete -F") || !strings.Contains(stdout.String(), "features") {
		t.Fatalf("completion stdout = %q", stdout.String())
	}
}

func TestDoctorFailReturnsSilentExitError(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("OPENAI_BASE_URL", "http://127.0.0.1:1/v1")
	var stdout, stderr bytes.Buffer
	err := Run(context.Background(), []string{"--profile", "missing", "doctor", "--json"}, strings.NewReader(""), &stdout, &stderr)
	var exitErr *ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("Run error = %v, want ExitError", err)
	}
	if exitErr.Code != 1 || !exitErr.Silent {
		t.Fatalf("ExitError = %+v, want code 1 silent", exitErr)
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
	output := stdout.String()
	if !strings.Contains(output, `"overallStatus": "fail"`) || !strings.Contains(output, `"id": "config.load"`) {
		t.Fatalf("doctor json output = %q", output)
	}
}

func initGitRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}
	dir := t.TempDir()
	git(t, dir, "init")
	git(t, dir, "config", "user.email", "codex@example.test")
	git(t, dir, "config", "user.name", "Codex Test")
	writeFile(t, dir, "tracked.txt", "old\n")
	git(t, dir, "add", "tracked.txt")
	git(t, dir, "commit", "-m", "initial")
	return dir
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	path := filepath.Join(dir, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, string(output))
	}
	return string(output)
}
