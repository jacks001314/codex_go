package app

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/coder/websocket"

	appsapi "codex_go/apps"
	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	codexexec "codex_go/exec"
	"codex_go/mcp"
	"codex_go/plugin"
	promptctx "codex_go/prompt"
	"codex_go/protocol"
	"codex_go/review"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/tool"
	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	chatwidget "codex_go/tui/chatwidget"
	codextea "codex_go/tui/tea"
	"codex_go/turn"
)

func useLocalExecRunner(t *testing.T) {
	t.Helper()
	previous := newCodexExecRunner
	newCodexExecRunner = codexexec.NewLocalRunner
	t.Cleanup(func() {
		newCodexExecRunner = previous
	})
}

func writeAppResponseSSE(w io.Writer, payload string) {
	_, _ = fmt.Fprintf(w, "data: %s\n\n", payload)
}

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
	if err.Error() != "Unknown feature flag: does_not_exist" {
		t.Fatalf("error = %v", err)
	}

	err = Run(context.Background(), []string{"--disable", "does_not_exist"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || err.Error() != "Unknown feature flag: does_not_exist" {
		t.Fatalf("disable unknown feature error = %v", err)
	}

	err = Run(context.Background(), []string{"--strict-config", "--enable", "multi_agent_v2.subagent_usage_hint_text"}, strings.NewReader(""), &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || err.Error() != "Unknown feature flag: multi_agent_v2.subagent_usage_hint_text" {
		t.Fatalf("compound unknown feature error = %v", err)
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

func TestDebugUnknownSubcommandDoesNotExposeGoPortMessage(t *testing.T) {
	var stdout bytes.Buffer
	err := Run(context.Background(), []string{"debug", "unknown-tool"}, strings.NewReader(""), &stdout, &bytes.Buffer{})
	if err == nil || err.Error() != "unknown debug subcommand unknown-tool" {
		t.Fatalf("debug unknown error = %v", err)
	}
	if strings.Contains(err.Error(), "not implemented") || strings.Contains(err.Error(), "Go port") {
		t.Fatalf("debug unknown exposed stale wording: %v", err)
	}
}

func TestExecJSONEndToEnd(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv("OPENAI_API_KEY", "sk-test")
	seenPath := make(chan string, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath <- r.URL.Path
		w.Header().Set("Content-Type", "text/event-stream")
		writeAppResponseSSE(w, `{"type":"response.created","response":{"id":"resp-cli"}}`)
		writeAppResponseSSE(w, `{"type":"response.output_item.added","item":{"id":"msg-cli","type":"message","role":"assistant","content":[{"type":"output_text","text":""}]}}`)
		writeAppResponseSSE(w, `{"type":"response.output_text.delta","item_id":"msg-cli","delta":"cli default"}`)
		writeAppResponseSSE(w, `{"type":"response.output_item.done","item":{"id":"msg-cli","type":"message","role":"assistant","content":[{"type":"output_text","text":"cli default"}]}}`)
		writeAppResponseSSE(w, `{"type":"response.completed","response":{"id":"resp-cli","usage":{"input_tokens":1,"output_tokens":2,"total_tokens":3}}}`)
	}))
	defer server.Close()
	if err := os.WriteFile(config.ConfigPath(home), []byte("openai_base_url = \""+server.URL+"/v1\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"exec", "--json", "hello"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("exec returned error: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, `"type":"thread.started"`) ||
		!strings.Contains(output, `"type":"turn.completed"`) ||
		!strings.Contains(output, "cli default") ||
		strings.Contains(output, "Go Codex exec stub received") {
		t.Fatalf("exec json output = %q", output)
	}
	select {
	case path := <-seenPath:
		if path != "/v1/responses" {
			t.Fatalf("request path = %q", path)
		}
	default:
		t.Fatal("Responses API server did not receive a request")
	}
}

func TestInteractivePromptUsesExecRunner(t *testing.T) {
	useLocalExecRunner(t)
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
	useLocalExecRunner(t)
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "sk-test")
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{}, strings.NewReader("first turn\nsecond turn\n/exit\n"), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("interactive returned error: %v", err)
	}
	output := stdout.String()
	if !strings.Contains(output, "OpenAI Codex") ||
		!strings.Contains(output, "Model: gpt-5.5") ||
		!strings.Contains(output, "Directory:") ||
		!strings.Contains(output, "first turn") ||
		!strings.Contains(output, "second turn") {
		t.Fatalf("stdout = %q", stdout.String())
	}
}

func TestInteractiveSlashCommandsUpdateTUIState(t *testing.T) {
	useLocalExecRunner(t)
	t.Setenv("TERM", "xterm-256color")
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "sk-test")
	var stdout bytes.Buffer
	input := strings.Join([]string{
		"/help",
		"/keymap",
		"/model gpt-test",
		"/approval never",
		"/sandbox :workspace",
		"/permissions full-access",
		"/personality pragmatic",
		"/experimental memories on",
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
		"Codex TUI keymap:",
		"Model: gpt-test",
		"Approval: never",
		"Sandbox: :workspace",
		"Permissions: approval=never sandbox=:danger-full-access",
		"Personality set to Pragmatic",
		"Feature memories enabled.",
		"hello tui",
		"Started a new local thread.",
		"Thread: new",
	} {
		if !strings.Contains(output, want) {
			t.Fatalf("interactive output = %q, missing %q", output, want)
		}
	}
	data, err := os.ReadFile(filepath.Join(os.Getenv("CODEX_HOME"), "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(config) error = %v", err)
	}
	text := string(data)
	if !strings.Contains(text, `personality = "pragmatic"`) || !strings.Contains(text, "memories = true") {
		t.Fatalf("config missing settings writes:\n%s", text)
	}
}

func TestRootHelpAndVersionMatchSystemShape(t *testing.T) {
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"--help"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("--help returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Codex CLI") || !strings.Contains(stdout.String(), "Commands:") || !strings.Contains(stdout.String(), "apply") {
		t.Fatalf("--help output = %q", stdout.String())
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"--version"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("--version returned error: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(stdout.String()), "codex-cli ") {
		t.Fatalf("--version output = %q", stdout.String())
	}
}

func TestInteractiveUIStateUsesEffectiveConfigModelAndDirectory(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(project, 0o755); err != nil {
		t.Fatalf("MkdirAll project error = %v", err)
	}
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(config.ConfigPath(home), []byte(strings.Join([]string{
		`model = "gpt-config"`,
		`model_reasoning_effort = "xhigh"`,
		`approval_policy = "on-request"`,
		`sandbox_mode = "workspace-write"`,
	}, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}

	state := interactiveUIState(&cli.RootOptions{Shared: cli.SharedOptions{CWD: project}})
	if state.Model != "gpt-config" || state.ReasoningEffort != "xhigh" || state.ApprovalPolicy != "on-request" || state.Sandbox != "workspace-write" {
		t.Fatalf("state config fields = model %q reasoning %q approval %q sandbox %q", state.Model, state.ReasoningEffort, state.ApprovalPolicy, state.Sandbox)
	}
	if state.CWD != project {
		t.Fatalf("state CWD = %q, want %q", state.CWD, project)
	}

	override := interactiveUIState(&cli.RootOptions{Shared: cli.SharedOptions{CWD: project, Model: "gpt-cli"}})
	if override.Model != "gpt-cli" || override.ReasoningEffort != "xhigh" {
		t.Fatalf("override state = model %q reasoning %q", override.Model, override.ReasoningEffort)
	}
}

func TestInteractiveUIStateUsesConcreteDefaultModel(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	state := interactiveUIState(&cli.RootOptions{})
	if strings.TrimSpace(state.Model) == "" || state.Model == "default" {
		t.Fatalf("state model = %q, want concrete bundled default", state.Model)
	}
}

func TestInteractiveKeymapCommandPersistsConfig(t *testing.T) {
	t.Setenv("TERM", "xterm-256color")
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	var stdout bytes.Buffer
	input := strings.Join([]string{
		"/keymap set global.open_external_editor ctrl-e",
		"/keymap unbind composer.queue",
		"/keymap unset global.open_external_editor",
		"/exit",
	}, "\n") + "\n"
	if err := Run(context.Background(), []string{}, strings.NewReader(input), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("interactive returned error: %v", err)
	}
	output := stdout.String()
	for _, want := range []string{"Updated global.open_external_editor", "Unbound composer.queue", "Reset global.open_external_editor"} {
		if !strings.Contains(output, want) {
			t.Fatalf("stdout = %q, missing %q", output, want)
		}
	}
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile(config) error = %v", err)
	}
	text := string(data)
	if strings.Contains(text, "open_external_editor") {
		t.Fatalf("unset should remove open_external_editor:\n%s", text)
	}
	if !strings.Contains(text, "[tui.keymap.composer]") || !strings.Contains(text, "queue = []") {
		t.Fatalf("config missing explicit composer.queue unbind:\n%s", text)
	}
}

func TestInteractiveDebugConfigReaderUsesRustStyleRenderer(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(project, ".codex"), 0o755); err != nil {
		t.Fatalf("MkdirAll project .codex error = %v", err)
	}
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(config.ConfigPath(home), []byte("model = \"gpt-user\"\n"+trustedProjectConfigApp(project)+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile user config error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte("allowed_web_search_modes = []\nallow_remote_control = false\n[experimental_network]\nenabled = true\n"), 0o600); err != nil {
		t.Fatalf("WriteFile requirements error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(project, ".codex", "config.toml"), []byte("model_provider = \"openai\"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile project config error = %v", err)
	}
	root := &cli.RootOptions{Shared: cli.SharedOptions{CWD: project}}

	lines, err := interactiveDebugConfigReader(root)()
	if err != nil {
		t.Fatalf("interactiveDebugConfigReader returned error: %v", err)
	}
	rendered := strings.Join(lines, "\n")
	for _, want := range []string{
		"/debug-config",
		"Config layer stack (lowest precedence first):",
		"1. user (",
		"2. project (",
		"Requirements:",
		"allowed_web_search_modes: disabled",
		"allow_remote_control: false",
		"experimental_network: enabled=true",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("debug config reader output missing %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "<unavailable in embedded Tea model>") {
		t.Fatalf("debug config reader returned embedded fallback:\n%s", rendered)
	}
}

func trustedProjectConfigApp(path string) string {
	key := strings.ReplaceAll(filepath.Clean(path), `\`, `\\`)
	return "\n[projects.\"" + key + "\"]\ntrust_level = \"trusted\"\n"
}

func TestAppServerRequirementsFromFileParsesFullRequirements(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requirements.toml")
	if err := os.WriteFile(path, []byte("allowed_sandbox_modes = [\"workspace-write\"]\nallowed_web_search_modes = []\nallow_remote_control = true\n[experimental_network]\nenabled = true\n"), 0o600); err != nil {
		t.Fatalf("WriteFile requirements error = %v", err)
	}

	requirements, err := appServerRequirementsFromFile(path)
	if err != nil {
		t.Fatalf("appServerRequirementsFromFile returned error: %v", err)
	}
	if requirements == nil {
		t.Fatal("requirements = nil")
	}
	if len(requirements.AllowedSandboxModes) != 1 || requirements.AllowedSandboxModes[0] != sandbox.SandboxWorkspaceWrite {
		t.Fatalf("AllowedSandboxModes = %#v", requirements.AllowedSandboxModes)
	}
	if requirements.AllowedWebSearchModes == nil || len(requirements.AllowedWebSearchModes) != 0 {
		t.Fatalf("AllowedWebSearchModes = %#v", requirements.AllowedWebSearchModes)
	}
	if requirements.AllowRemoteControl == nil || !*requirements.AllowRemoteControl {
		t.Fatalf("AllowRemoteControl = %#v", requirements.AllowRemoteControl)
	}
	if requirements.Network == nil || requirements.Network.Enabled == nil || !*requirements.Network.Enabled {
		t.Fatalf("Network = %#v", requirements.Network)
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
	if completed.ThreadID != "thread-1" || completed.AssistantMessage != "" || completed.Err != nil {
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
	if completed.AssistantMessage != "ok" {
		t.Fatalf("completed assistant message = %q, want fallback last message", completed.AssistantMessage)
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

func TestInteractiveSubmitInputsIncludesSelectedSkill(t *testing.T) {
	inputs := interactiveSubmitInputs(codextea.SubmitRequest{
		Prompt:          "$imagegen",
		MentionBindings: []string{`imagegen|skill://C:\Users\me\.codex\skills\.system\imagegen\SKILL.md`},
		MentionCatalog: chatwidget.SubmissionMentionCatalog{
			Skills: []appserver.SkillsListEntry{{
				Name:    "imagegen",
				Path:    `C:\Users\me\.codex\skills\.system\imagegen\SKILL.md`,
				Enabled: true,
			}},
		},
	})

	if len(inputs) != 1 {
		t.Fatalf("inputs = %#v", inputs)
	}
	if inputs[0].Type != "skill" || inputs[0].Name != "imagegen" || inputs[0].Path != `C:\Users\me\.codex\skills\.system\imagegen\SKILL.md` {
		t.Fatalf("skill input = %#v", inputs[0])
	}
}

func TestInteractiveLocalSkillContextInjectsExplicitRepoSkill(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	cwd := t.TempDir()
	skillDir := filepath.Join(cwd, ".codex", "skills", "repo-helper")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatalf("MkdirAll(skill) error = %v", err)
	}
	skillPath := filepath.Join(skillDir, appserver.SkillFilename)
	if err := os.WriteFile(skillPath, []byte("---\nname: repo-helper\ndescription: Repo helper\n---\nUse repo context.\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}

	request := codextea.SubmitRequest{Prompt: "$repo-helper"}
	instructions, inputItems, err := interactiveLocalSkillContextForRequest(&cli.RootOptions{}, request, interactiveSubmitInputs(request), cwd)
	if err != nil {
		t.Fatalf("interactiveLocalSkillContextForRequest() error = %v", err)
	}
	for _, want := range []string{promptctx.SkillsInstructionsOpenTag, "repo-helper", "Repo helper", "### How to use skills", "must read its `SKILL.md` completely"} {
		if !strings.Contains(instructions, want) {
			t.Fatalf("instructions missing %q:\n%s", want, instructions)
		}
	}
	if strings.Contains(instructions, "hidden-skill") {
		t.Fatalf("instructions missing repo skill:\n%s", instructions)
	}
	if len(inputItems) != 1 {
		t.Fatalf("inputItems = %#v", inputItems)
	}
	if !interactiveInputItemContainsText(inputItems[0], "<name>repo-helper</name>") ||
		!interactiveInputItemContainsText(inputItems[0], "Use repo context.") ||
		!interactiveInputItemContainsText(inputItems[0], skillPath) {
		t.Fatalf("skill input item = %#v", inputItems[0])
	}
}

func interactiveInputItemContainsText(item any, want string) bool {
	raw, ok := item.(map[string]any)
	if !ok {
		return false
	}
	content, ok := raw["content"].([]map[string]any)
	if !ok {
		return false
	}
	for _, block := range content {
		if strings.Contains(fmt.Sprint(block["text"]), want) {
			return true
		}
	}
	return false
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

func TestInteractiveTurnCommandInterruptCancelsRunningContext(t *testing.T) {
	interrupts := newInteractiveInterruptController()
	startedRunning := make(chan struct{})
	runnerDone := make(chan error, 1)
	runner := interactiveTurnRunnerFunc(func(ctx context.Context, req *codexexec.Request, stdin io.Reader, stdout, stderr io.Writer) (*codexexec.Result, error) {
		close(startedRunning)
		<-ctx.Done()
		runnerDone <- ctx.Err()
		return nil, ctx.Err()
	})

	msg := interactiveTurnCommandWithRequest(context.Background(), &cli.RootOptions{}, runner, codextui.NewState(nil), codextea.SubmitRequest{Prompt: "long task"}, nil, nil, nil, interrupts)()
	stream, ok := msg.(codextea.StreamStartedMsg)
	if !ok {
		t.Fatalf("message = %T, want StreamStartedMsg", msg)
	}
	select {
	case <-startedRunning:
	case <-time.After(time.Second):
		t.Fatal("runner did not start")
	}
	if interrupted, ok := interrupts.interruptCommand()().(codextea.TurnInterruptedMsg); !ok || interrupted.Err != nil {
		t.Fatalf("interrupt command = %#v ok=%v", interrupted, ok)
	}
	select {
	case err := <-runnerDone:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("runner context error = %v, want canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("runner did not observe cancellation")
	}
	sawInterrupted := false
	for message := range stream.Messages {
		if interrupted, ok := message.(codextea.TurnInterruptedMsg); ok {
			sawInterrupted = true
			if !errors.Is(interrupted.Err, context.Canceled) {
				t.Fatalf("stream interrupt err = %v, want canceled", interrupted.Err)
			}
		}
	}
	if !sawInterrupted {
		t.Fatal("stream did not include TurnInterruptedMsg")
	}
}

func TestInteractiveStreamEventWriterParsesJSONLines(t *testing.T) {
	var messages []any
	writer := newInteractiveStreamEventWriter(func(message bubbletea.Msg) {
		messages = append(messages, message)
	})
	_, _ = writer.Write([]byte(`{"type":"thread.started","thread_id":"thread-1"}` + "\n"))
	_, _ = writer.Write([]byte(`{"type":"item.delta","delta":{"item_id":"msg-1","text":"hi"}}` + "\n"))
	_, _ = writer.Write([]byte(`{"type":"response.rate_limits","rateLimit":{"limitId":"codex","primary":{"usedPercent":90,"windowDurationMins":300}}}`))
	writer.Flush()

	if len(messages) != 3 {
		t.Fatalf("message len = %d, want 3 (%#v)", len(messages), messages)
	}
	if !writer.SawAssistantOutput() {
		t.Fatal("writer did not record assistant output")
	}
	first, ok := messages[0].(codextea.ThreadEventMsg)
	if !ok || first.Event.Type != "thread.started" || first.Event.ThreadID != "thread-1" {
		t.Fatalf("first message = %#v", messages[0])
	}
	second, ok := messages[1].(codextea.ThreadEventMsg)
	if !ok || second.Event.Delta == nil || second.Event.Delta.Text != "hi" {
		t.Fatalf("second message = %#v", messages[1])
	}
	third, ok := messages[2].(codextea.ThreadEventMsg)
	if !ok || third.Event.RateLimit == nil || third.Event.RateLimit.Primary == nil || third.Event.RateLimit.Primary.UsedPercent != 90 {
		t.Fatalf("third message = %#v", messages[2])
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
						map[string]any{"const": "accept", "title": "Allow once"},
						map[string]any{"const": "accept_session", "title": "Allow session"},
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
		OptionID: "accept_session",
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

func TestInteractiveMCPRuntimeLoadsConfiguredServersForSlashCommand(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	body := `
[features]
apps = false

[mcp_servers.angr]
command = "codex-go-missing-mcp-test"
`
	if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}

	service, statuses, expectedServers := interactiveMCPRuntime(&cli.RootOptions{})
	if service == nil {
		t.Fatal("interactiveMCPRuntime returned nil service for configured MCP server")
	}
	defer service.Close()
	if len(statuses) != 1 || statuses[0].Name != "angr" {
		t.Fatalf("statuses = %#v, want configured angr server", statuses)
	}
	if statuses[0].Auth != "Unsupported" {
		t.Fatalf("auth = %q, want Unsupported", statuses[0].Auth)
	}
	if !reflect.DeepEqual(expectedServers, []string{"angr"}) {
		t.Fatalf("expected servers = %#v, want angr", expectedServers)
	}

	runner := codexexec.NewLocalRunner(home)
	messages := interactiveMCPStartupMessages(context.Background(), service, runner, expectedServers)
	var updates []codextea.MCPStartupUpdateMsg
	for message := range messages {
		if update, ok := message.(codextea.MCPStartupUpdateMsg); ok {
			updates = append(updates, update)
		}
	}
	if len(updates) < 2 || updates[0].Name != "angr" || updates[0].Status.Kind != chatwidget.McpStartupStarting {
		t.Fatalf("startup updates = %#v", updates)
	}
	if updates[len(updates)-1].Status.Kind != chatwidget.McpStartupFailed {
		t.Fatalf("final startup update = %#v, want failed", updates[len(updates)-1])
	}
	if len(runner.MCPTools) != 0 {
		t.Fatalf("runner MCP tools = %#v, want none for missing helper", runner.MCPTools)
	}
}

func TestInteractiveMCPRuntimeDoesNotStartConfiguredServersBeforeTUI(t *testing.T) {
	if os.Getenv("INTERACTIVE_MCP_STARTUP_BLOCK_HELPER") == "1" {
		time.Sleep(2 * time.Second)
		return
	}
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	executable, err := os.Executable()
	if err != nil {
		t.Fatalf("Executable() error = %v", err)
	}
	body := fmt.Sprintf(`
[features]
apps = false

[mcp_servers.slow]
command = %q
args = ["-test.run=TestInteractiveMCPRuntimeDoesNotStartConfiguredServersBeforeTUI", "--"]
env = { INTERACTIVE_MCP_STARTUP_BLOCK_HELPER = "1" }
`, executable)
	if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile config returned error: %v", err)
	}

	start := time.Now()
	service, statuses, expectedServers := interactiveMCPRuntime(&cli.RootOptions{})
	if service == nil {
		t.Fatal("interactiveMCPRuntime returned nil service")
	}
	defer service.Close()
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("interactiveMCPRuntime took %s, want no startup probe before TUI", elapsed)
	}
	if len(statuses) != 1 || statuses[0].Name != "slow" {
		t.Fatalf("statuses = %#v, want configured slow server", statuses)
	}
	if !reflect.DeepEqual(expectedServers, []string{"slow"}) {
		t.Fatalf("expected servers = %#v, want slow", expectedServers)
	}
}

func TestInteractiveNotificationPosterWritesOSC9(t *testing.T) {
	t.Setenv("TERM_PROGRAM", "WezTerm")
	t.Setenv("TERM", "")
	t.Setenv("TMUX", "")
	var stdout bytes.Buffer

	cmd := interactiveNotificationPoster(&stdout)("done", codextui.NotificationMethodOSC9)
	if cmd == nil {
		t.Fatal("notification poster returned nil command")
	}
	if msg := cmd(); msg != nil {
		t.Fatalf("notification poster message = %#v, want nil", msg)
	}
	if got := stdout.String(); got != "\x1b]9;done\x07" {
		t.Fatalf("notification sequence = %q", got)
	}
}

func TestInteractiveSettingsFromConfigParsesTUINotifications(t *testing.T) {
	hidden := true
	settings := interactiveSettingsFromConfig(&config.Config{Values: map[string]any{
		"personality": "pragmatic",
		"notices":     map[string]any{"hide_rate_limit_model_nudge": hidden},
		"tui": map[string]any{
			"notifications":          []any{"approval-requested", "agent-turn-complete", "approval-requested", ""},
			"notification_method":    "bel",
			"notification_condition": "always",
			"theme":                  "dracula",
			"pet":                    "dewey",
			"session_picker_view":    "dense",
		},
		"requirements": map[string]any{
			"allowed_approval_policies":   []any{"on-request"},
			"allowed_approvals_reviewers": []any{"user"},
			"allowed_windows_sandbox_implementations": []any{
				"elevated",
			},
			"allowed_permission_profiles": map[string]any{
				":workspace":          true,
				":danger-full-access": false,
			},
		},
	}})
	if settings.Personality != chatwidget.PersonalityPragmatic {
		t.Fatalf("personality = %q", settings.Personality)
	}
	if settings.Notifications == nil || !settings.Notifications.CustomSet ||
		len(settings.Notifications.Custom) != 4 ||
		settings.Notifications.Custom[0] != "approval-requested" ||
		settings.Notifications.Custom[1] != "agent-turn-complete" ||
		settings.Notifications.Custom[2] != "approval-requested" ||
		settings.Notifications.Custom[3] != "" {
		t.Fatalf("notifications = %#v", settings.Notifications)
	}
	if settings.NotificationMethod != codextui.NotificationMethodBEL {
		t.Fatalf("notification method = %q", settings.NotificationMethod)
	}
	if settings.NotificationCondition != codextui.NotificationConditionAlways {
		t.Fatalf("notification condition = %q", settings.NotificationCondition)
	}
	if settings.HideRateLimitModelNudge == nil || *settings.HideRateLimitModelNudge != hidden {
		t.Fatalf("hide notice = %#v", settings.HideRateLimitModelNudge)
	}
	if settings.TUITheme != "dracula" || settings.TUIPet != "dewey" || settings.SessionPickerView != "dense" {
		t.Fatalf("tui theme/pet/session picker = %q/%q/%q", settings.TUITheme, settings.TUIPet, settings.SessionPickerView)
	}
	if settings.PermissionRequirements == nil ||
		len(settings.PermissionRequirements.AllowedApprovalPolicies) != 1 ||
		settings.PermissionRequirements.AllowedApprovalPolicies[0] != chatwidget.ApprovalOnRequest ||
		len(settings.PermissionRequirements.AllowedReviewers) != 1 ||
		settings.PermissionRequirements.AllowedReviewers[0] != chatwidget.ApprovalsReviewerUser ||
		len(settings.PermissionRequirements.AllowedWindowsSandboxModes) != 1 ||
		settings.PermissionRequirements.AllowedWindowsSandboxModes[0] != chatwidget.WindowsSandboxModeElevated ||
		!settings.PermissionRequirements.AllowedProfiles[chatwidget.WorkspaceProfile] ||
		settings.PermissionRequirements.AllowedProfiles[chatwidget.DangerFullAccessProfile] {
		t.Fatalf("permission requirements = %#v", settings.PermissionRequirements)
	}
}

func TestInteractiveSettingsFromConfigDefaultsTUINotifications(t *testing.T) {
	settings := interactiveSettingsFromConfig(&config.Config{Values: map[string]any{}})
	if settings.Notifications == nil || !settings.Notifications.Enabled || len(settings.Notifications.Custom) != 0 {
		t.Fatalf("default notifications = %#v", settings.Notifications)
	}
	if settings.NotificationMethod != codextui.NotificationMethodAuto {
		t.Fatalf("default notification method = %q", settings.NotificationMethod)
	}
	if settings.NotificationCondition != codextui.NotificationConditionUnfocused {
		t.Fatalf("default notification condition = %q", settings.NotificationCondition)
	}
}

func TestInteractiveRemoteLoadSettingsReadsRequirements(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	requests := make(chan remoteTUITestRequest, 8)
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
			case string(appserver.MethodConfigRead):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"config": map[string]any{
							"tui": map[string]any{"notification_method": "bel"},
						},
					},
				})
			case string(appserver.MethodConfigRequirementsRead):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"requirements": map[string]any{
							"allowedApprovalPolicies":              []any{"on-request"},
							"allowedApprovalsReviewers":            []any{"user"},
							"allowedWindowsSandboxImplementations": []any{"unelevated"},
							"allowedPermissionProfiles": map[string]any{
								":workspace":          true,
								":danger-full-access": false,
							},
						},
					},
				})
				return
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	settings, err := interactiveRemoteLoadSettings(ctx, endpoint)
	if err != nil {
		t.Fatalf("interactiveRemoteLoadSettings error = %v", err)
	}
	if settings.NotificationMethod != codextui.NotificationMethodBEL {
		t.Fatalf("remote notification method = %q", settings.NotificationMethod)
	}
	if settings.PermissionRequirements == nil ||
		len(settings.PermissionRequirements.AllowedApprovalPolicies) != 1 ||
		settings.PermissionRequirements.AllowedApprovalPolicies[0] != chatwidget.ApprovalOnRequest ||
		len(settings.PermissionRequirements.AllowedWindowsSandboxModes) != 1 ||
		settings.PermissionRequirements.AllowedWindowsSandboxModes[0] != chatwidget.WindowsSandboxModeUnelevated ||
		settings.PermissionRequirements.AllowedProfiles[chatwidget.DangerFullAccessProfile] {
		t.Fatalf("remote permission requirements = %#v", settings.PermissionRequirements)
	}
	for _, want := range []appserver.Method{appserver.MethodInitialize, appserver.MethodConfigRead, appserver.MethodConfigRequirementsRead} {
		got := remoteTUITestReadCapturedRequest(t, requests)
		if got.Method != string(want) {
			t.Fatalf("remote request = %q, want %q", got.Method, want)
		}
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

func TestInteractiveRemoteStartWindowsSandboxSetupCallsAppServer(t *testing.T) {
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
			case string(appserver.MethodWindowsSandboxSetupStart):
				var params sandbox.WindowsSetupStartParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.Mode != sandbox.WindowsSetupElevated || params.CWD == nil || *params.CWD != `D:\repo` {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("setup params = %#v", params))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"started": true},
				})
				return
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	outcome, err := interactiveRemoteStartWindowsSandboxSetup(ctx, endpoint, chatwidget.WindowsSandboxModeElevated, `D:\repo`)
	if err != nil {
		t.Fatalf("interactiveRemoteStartWindowsSandboxSetup error = %v", err)
	}
	if !outcome.Started {
		t.Fatalf("outcome = %#v, want started", outcome)
	}
	if got := remoteTUITestReadCapturedRequest(t, requests); got.Method != string(appserver.MethodInitialize) {
		t.Fatalf("first request = %q", got.Method)
	}
	if got := remoteTUITestReadCapturedRequest(t, requests); got.Method != string(appserver.MethodWindowsSandboxSetupStart) {
		t.Fatalf("second request = %q", got.Method)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

func TestInteractiveRemoteReadHooksCallsAppServer(t *testing.T) {
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
			case string(appserver.MethodHooksList):
				var params appserver.HookListParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if len(params.CWDs) != 1 || params.CWDs[0] != `D:\repo` {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("hooks params = %#v", params))
					return
				}
				command := "echo ok"
				matcher := "Bash"
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": appserver.HookListResponse{Data: []appserver.HookListEntry{{
						CWD: `D:\repo`,
						Hooks: []appserver.HookMetadata{{
							Key:         "hook-1",
							EventName:   appserver.HookEventPreToolUse,
							HandlerType: appserver.HookHandlerCommand,
							Matcher:     &matcher,
							Command:     &command,
							SourcePath:  `D:\repo\.codex\hooks.json`,
							Source:      appserver.HookSourceProject,
							Enabled:     true,
							TrustStatus: appserver.HookTrustTrusted,
						}},
						Warnings: []string{"review hook trust"},
					}}},
				})
				return
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	runs, err := interactiveRemoteReadHooks(ctx, endpoint, `D:\repo`)
	if err != nil {
		t.Fatalf("interactiveRemoteReadHooks error = %v", err)
	}
	if len(runs) != 2 {
		t.Fatalf("runs = %#v, want hook plus warning", runs)
	}
	if runs[0].ID != "hook-1" || runs[0].Name != "preToolUse / Bash" || runs[0].Command != "echo ok" {
		t.Fatalf("hook run = %#v", runs[0])
	}
	for _, want := range []string{`cwd: D:\repo`, "source: project", `path: D:\repo\.codex\hooks.json`, "trust: trusted"} {
		if !strings.Contains(runs[0].Issue, want) {
			t.Fatalf("hook issue missing %q: %q", want, runs[0].Issue)
		}
	}
	if runs[1].Name != "Hook warning" || !strings.Contains(runs[1].Issue, "review hook trust") {
		t.Fatalf("warning run = %#v", runs[1])
	}
	if got := remoteTUITestReadCapturedRequest(t, requests); got.Method != string(appserver.MethodInitialize) {
		t.Fatalf("first request = %q", got.Method)
	}
	if got := remoteTUITestReadCapturedRequest(t, requests); got.Method != string(appserver.MethodHooksList) {
		t.Fatalf("second request = %q", got.Method)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

func TestInteractiveRemoteReadPluginsCallsAppServer(t *testing.T) {
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
			case string(appserver.MethodPluginList):
				var params plugin.PluginListParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if !params.IncludeInstalled {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("plugin list params = %#v, want include installed", params))
					return
				}
				display := "Docs"
				short := "Search docs."
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": plugin.PluginListResponse{Marketplaces: []plugin.PluginMarketplaceEntry{{
						Name: "team",
						Plugins: []plugin.PluginSummary{{
							ID:            "docs@team",
							Name:          "docs",
							Availability:  plugin.PluginAvailable,
							InstallPolicy: plugin.InstallAllowed,
							Installed:     true,
							Enabled:       true,
							Interface: &plugin.PluginInterface{
								DisplayName:      &display,
								ShortDescription: &short,
							},
						}},
					}}},
				})
				return
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	response, err := interactiveRemoteReadPlugins(ctx, endpoint)
	if err != nil {
		t.Fatalf("interactiveRemoteReadPlugins error = %v", err)
	}
	if len(response.Marketplaces) != 1 || len(response.Marketplaces[0].Plugins) != 1 || response.Marketplaces[0].Plugins[0].ID != "docs@team" {
		t.Fatalf("plugin response = %#v", response)
	}
	if got := remoteTUITestReadCapturedRequest(t, requests); got.Method != string(appserver.MethodInitialize) {
		t.Fatalf("first request = %q", got.Method)
	}
	if got := remoteTUITestReadCapturedRequest(t, requests); got.Method != string(appserver.MethodPluginList) {
		t.Fatalf("second request = %q", got.Method)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

func TestInteractiveRemoteReadAppsCallsAppServer(t *testing.T) {
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
			case string(appserver.MethodAppList):
				var params appsapi.AppListParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ThreadID == nil || *params.ThreadID != "thread-apps" || !params.ForceRefetch {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("app list params = %#v, want thread-apps force refetch", params))
					return
				}
				desc := "Search Drive files."
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": appsapi.AppListResponse{Data: []appsapi.AppEntry{{
						ID:           "drive",
						Name:         "Google Drive",
						Description:  &desc,
						IsAccessible: true,
						IsEnabled:    true,
					}}},
				})
				return
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	response, err := interactiveRemoteReadApps(ctx, endpoint, "thread-apps", true)
	if err != nil {
		t.Fatalf("interactiveRemoteReadApps error = %v", err)
	}
	if len(response.Data) != 1 || response.Data[0].ID != "drive" {
		t.Fatalf("apps response = %#v", response)
	}
	if got := remoteTUITestReadCapturedRequest(t, requests); got.Method != string(appserver.MethodInitialize) {
		t.Fatalf("first request = %q", got.Method)
	}
	if got := remoteTUITestReadCapturedRequest(t, requests); got.Method != string(appserver.MethodAppList) {
		t.Fatalf("second request = %q", got.Method)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

func TestInteractiveRemoteStartReviewCallsAppServer(t *testing.T) {
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
			case string(appserver.MethodReviewStart):
				var params review.StartParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ThreadID != "thread-review" || params.Delivery == nil || *params.Delivery != "inline" {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("review start params = %#v, want inline thread-review", params))
					return
				}
				if params.Target.Type != "custom" || params.Target.Instructions != "check security" {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("review target = %#v", params.Target))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": review.StartResponse{
						Turn:           review.Turn{ID: "review-turn", Status: review.TurnStatusInProgress},
						ReviewThreadID: "thread-review",
					},
				})
				return
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	delivery := "inline"
	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	response, err := interactiveRemoteStartReview(ctx, endpoint, review.StartParams{
		ThreadID: "thread-review",
		Target: review.APITarget{
			Type:         "custom",
			Instructions: "check security",
		},
		Delivery: &delivery,
	})
	if err != nil {
		t.Fatalf("interactiveRemoteStartReview error = %v", err)
	}
	if response.Turn.ID != "review-turn" || response.ReviewThreadID != "thread-review" {
		t.Fatalf("review response = %#v", response)
	}
	if got := remoteTUITestReadCapturedRequest(t, requests); got.Method != string(appserver.MethodInitialize) {
		t.Fatalf("first request = %q", got.Method)
	}
	if got := remoteTUITestReadCapturedRequest(t, requests); got.Method != string(appserver.MethodReviewStart) {
		t.Fatalf("second request = %q", got.Method)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

func TestInteractiveRemoteStartSideForksAndInjectsBoundary(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	requests := make(chan remoteTUITestRequest, 6)
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
			case string(appserver.MethodThreadFork):
				var params appserver.ThreadForkParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ThreadID != "thread-parent" || !params.Ephemeral || params.Model == nil || *params.Model != "gpt-side" {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("side fork params = %#v", params))
					return
				}
				if params.CWD == nil || *params.CWD != `D:\repo` || params.DeveloperInstructions == nil || !strings.Contains(*params.DeveloperInstructions, "You are in a side conversation") {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("side fork config = %#v", params))
					return
				}
				if params.Config["model_provider"] != "openai" || params.Config["model_reasoning_effort"] != "high" {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("side fork config map = %#v", params.Config))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"thread": map[string]any{
							"id":        "thread-side",
							"ephemeral": true,
							"turns":     []any{},
						},
					},
				})
			case string(appserver.MethodThreadInjectItems):
				var params appserver.ThreadInjectItemsParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ThreadID != "thread-side" || len(params.Items) != 1 || !strings.Contains(string(params.Items[0]), "Side conversation boundary.") {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("inject params = %#v", params))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
				return
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	state := codextui.NewState(&codextui.Options{
		Model:           "gpt-side",
		ReasoningEffort: "high",
		ApprovalPolicy:  "on-request",
		Sandbox:         "workspace-write",
	})
	response, err := interactiveRemoteStartSide(ctx, &cli.RootOptions{
		ConfigOverrides: []string{`model_provider="openai"`},
		Shared: cli.SharedOptions{
			CWD:   `D:\repo`,
			Model: "gpt-root",
		},
	}, endpoint, state, codextea.SideStartParams{ParentThreadID: "thread-parent"})
	if err != nil {
		t.Fatalf("interactiveRemoteStartSide error = %v", err)
	}
	if response.ParentThreadID != "thread-parent" || response.SideThreadID != "thread-side" {
		t.Fatalf("side response = %#v", response)
	}
	if got := remoteTUITestReadCapturedRequest(t, requests); got.Method != string(appserver.MethodInitialize) {
		t.Fatalf("first request = %q", got.Method)
	}
	if got := remoteTUITestReadCapturedRequest(t, requests); got.Method != string(appserver.MethodThreadFork) {
		t.Fatalf("second request = %q", got.Method)
	}
	if got := remoteTUITestReadCapturedRequest(t, requests); got.Method != string(appserver.MethodThreadInjectItems) {
		t.Fatalf("third request = %q", got.Method)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

func TestInteractiveRemoteCloseSideUnsubscribesThread(t *testing.T) {
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
			case string(appserver.MethodThreadUnsubscribe):
				var params appserver.ThreadUnsubscribeParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ThreadID != "thread-side" {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("unsubscribe params = %#v", params))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"status": string(appserver.ThreadUnsubscribeStatusUnsubscribed)},
				})
				return
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if _, err := interactiveRemoteCloseSide(ctx, endpoint, codextea.SideCloseParams{ParentThreadID: "thread-parent", SideThreadID: "thread-side"}); err != nil {
		t.Fatalf("interactiveRemoteCloseSide error = %v", err)
	}
	if got := remoteTUITestReadCapturedRequest(t, requests); got.Method != string(appserver.MethodInitialize) {
		t.Fatalf("first request = %q", got.Method)
	}
	if got := remoteTUITestReadCapturedRequest(t, requests); got.Method != string(appserver.MethodThreadUnsubscribe) {
		t.Fatalf("second request = %q", got.Method)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

func TestRemoteNotificationForParentThreadUpdatesSideStatusOnly(t *testing.T) {
	state := codextui.NewState(nil)
	state.SetThreadID("thread-side")
	messages := make(chan bubbletea.Msg, 4)
	client := &remoteAppServerTUIClient{
		state:    state,
		messages: messages,
	}
	payload, err := json.Marshal(appserver.TurnCompletedNotification{
		ThreadID: "thread-parent",
		Turn:     appserver.Turn{ID: "turn-parent", Status: appserver.TurnStatusCompleted},
	})
	if err != nil {
		t.Fatalf("marshal notification: %v", err)
	}

	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(appserver.NotificationTurnCompleted),
		Params: payload,
	}); err != nil {
		t.Fatalf("handleNotification: %v", err)
	}
	if state.ThreadID != "thread-side" {
		t.Fatalf("state thread id = %q, want side", state.ThreadID)
	}
	if client.turnCompleted {
		t.Fatal("parent turn completion should not complete the active side stream")
	}
	select {
	case msg := <-messages:
		status, ok := msg.(codextea.SideParentStatusChangeMsg)
		if !ok {
			t.Fatalf("message = %#v, want side status change", msg)
		}
		if status.ParentThreadID != "thread-parent" || status.Kind != codextea.SideParentStatusChangeSet || status.Status != codextea.SideParentStatusFinished {
			t.Fatalf("side status change = %#v", status)
		}
	default:
		t.Fatal("missing side parent status change")
	}
	select {
	case msg := <-messages:
		scoped, ok := msg.(codextea.ThreadScopedEventMsg)
		if !ok {
			t.Fatalf("message = %#v, want scoped parent event", msg)
		}
		if scoped.ThreadID != "thread-parent" || scoped.Event.Type != "turn.completed" {
			t.Fatalf("scoped parent event = %#v", scoped)
		}
	default:
		t.Fatal("missing scoped parent event")
	}
	select {
	case msg := <-messages:
		t.Fatalf("unexpected extra message for parent thread: %#v", msg)
	default:
	}
}

func TestInteractiveRemoteReadSkillsCallsAppServer(t *testing.T) {
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
			case string(appserver.MethodSkillsList):
				var params appserver.SkillsListParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if len(params.CWDs) != 1 || params.CWDs[0] != `D:\repo` {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("skills list params = %#v, want D:\\repo", params))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": appserver.SkillsListResponse{Data: []appserver.SkillsListEntry{{
						CWD: `D:\repo`,
						Skills: []appserver.SkillsListEntry{{
							Name:             "Docs:review",
							Path:             `D:\repo\.codex\skills\review\SKILL.md`,
							Scope:            "plugin",
							ShortDescription: "Review code",
							Enabled:          true,
							PluginID:         "docs@team",
						}},
					}}},
				})
				return
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	response, err := interactiveRemoteReadSkills(ctx, endpoint, `D:\repo`)
	if err != nil {
		t.Fatalf("interactiveRemoteReadSkills error = %v", err)
	}
	if len(response.Data) != 1 || len(response.Data[0].Skills) != 1 || response.Data[0].Skills[0].Name != "Docs:review" {
		t.Fatalf("skills response = %#v", response)
	}
	if got := remoteTUITestReadCapturedRequest(t, requests); got.Method != string(appserver.MethodInitialize) {
		t.Fatalf("first request = %q", got.Method)
	}
	if got := remoteTUITestReadCapturedRequest(t, requests); got.Method != string(appserver.MethodSkillsList) {
		t.Fatalf("second request = %q", got.Method)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
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
				IsOther:  true,
				IsSecret: true,
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
	if len(request.Questions) != 1 || !request.Questions[0].IsOther || !request.Questions[0].IsSecret {
		t.Fatalf("request question flags = %#v", request.Questions)
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
	if !strings.Contains(stdout.String(), "OpenAI Codex") || !strings.Contains(stdout.String(), "Directory:") {
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
	}, messages, remoteTUIBrokers{}, nil)

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
	runInteractiveRemoteTurn(ctx, &cli.RootOptions{}, endpoint, state, codextea.SubmitRequest{Prompt: "continue"}, messages, remoteTUIBrokers{}, nil)
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

func TestInteractiveRemoteTurnInterruptSendsTurnInterrupt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	turnStarted := make(chan struct{})
	completeTurn := make(chan struct{})
	interruptRequests := make(chan remoteTUITestRequest, 1)
	serverErrs := make(chan error, 4)
	var turnStartedOnce sync.Once
	var completeOnce sync.Once

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
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodTurnStart):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"turn": map[string]any{"id": "turn-interrupt", "items": []any{}, "status": "inProgress"}},
				})
				turnStartedOnce.Do(func() { close(turnStarted) })
				<-completeTurn
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"method":  string(appserver.NotificationTurnCompleted),
					"params":  map[string]any{"threadId": "thread-interrupt", "turn": map[string]any{"id": "turn-interrupt", "items": []any{}, "status": "interrupted"}},
				})
			case string(appserver.MethodTurnInterrupt):
				interruptRequests <- req
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
				completeOnce.Do(func() { close(completeTurn) })
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	state := codextui.NewState(nil)
	state.SetThreadID("thread-interrupt")
	messages := make(chan bubbletea.Msg, 64)
	interrupts := newRemoteTUIInterruptController(ctx, endpoint)
	done := make(chan struct{})
	go func() {
		runInteractiveRemoteTurn(ctx, &cli.RootOptions{}, endpoint, state, codextea.SubmitRequest{Prompt: "interrupt me"}, messages, remoteTUIBrokers{}, interrupts)
		close(done)
	}()

	select {
	case <-turnStarted:
	case <-time.After(time.Second):
		t.Fatal("remote turn did not start")
	}
	waitForRemoteTUIInterruptActive(t, interrupts, "thread-interrupt", "turn-interrupt")
	if interrupted, ok := interrupts.interruptCommand()().(codextea.TurnInterruptedMsg); !ok || interrupted.Err != nil {
		t.Fatalf("interrupt command = %#v ok=%v", interrupted, ok)
	}
	interrupt := remoteTUITestReadCapturedRequest(t, interruptRequests)
	if interrupt.Method != string(appserver.MethodTurnInterrupt) {
		t.Fatalf("interrupt method = %q", interrupt.Method)
	}
	var params turn.TurnInterruptParams
	if err := json.Unmarshal(interrupt.Params, &params); err != nil {
		t.Fatalf("unmarshal interrupt params: %v", err)
	}
	if params.ThreadID != "thread-interrupt" || params.TurnID != "turn-interrupt" {
		t.Fatalf("interrupt params = %#v", params)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("remote turn did not finish after interrupt")
	}
	sawInterrupted := false
	for message := range messages {
		if _, ok := message.(codextea.TurnInterruptedMsg); ok {
			sawInterrupted = true
		}
	}
	if !sawInterrupted {
		t.Fatal("remote stream did not include TurnInterruptedMsg")
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

func TestInteractiveRemoteTurnHandlesCommandApprovalServerRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	responses := make(chan remoteTUITestResponse, 1)
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
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodThreadStart):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"thread": map[string]any{"id": "thread-approval"}},
				})
			case string(appserver.MethodTurnStart):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"turn": map[string]any{"id": "turn-approval", "items": []any{}, "status": "inProgress"}},
				})
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      "approval-remote",
					"method":  string(appserver.ServerRequestCommandExecutionApproval),
					"params": map[string]any{
						"threadId":    "thread-approval",
						"turnId":      "turn-approval",
						"itemId":      "exec-1",
						"startedAtMs": 123,
						"command":     "go test ./...",
						"cwd":         `D:\repo`,
						"reason":      "needs approval",
					},
				})
				response, err := remoteTUITestReadResponse(ctx, conn)
				if err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				responses <- response
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"method":  string(appserver.NotificationTurnCompleted),
					"params":  map[string]any{"threadId": "thread-approval", "turn": map[string]any{"id": "turn-approval", "items": []any{}, "status": "completed"}},
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
	messages := make(chan bubbletea.Msg, 64)
	brokers := newRemoteTUIBrokers()
	done := make(chan struct{})
	go func() {
		runInteractiveRemoteTurn(ctx, &cli.RootOptions{}, endpoint, state, codextea.SubmitRequest{Prompt: "run tests"}, messages, brokers, nil)
		close(done)
	}()

	approval := remoteTUITestReadApprovalMessage(t, messages)
	if approval.ID == "" || approval.Command != "go test ./..." || !strings.Contains(approval.Body, "needs approval") {
		t.Fatalf("approval message = %#v", approval)
	}
	brokers.respond(codextea.ModalResponse{ID: approval.ID, Kind: codextea.ModalKindApproval, OptionID: "allow_session"})

	response := remoteTUITestReadCapturedResponse(t, responses)
	if response.Error != nil {
		t.Fatalf("approval response error = %#v", response.Error)
	}
	var result appserver.CommandExecutionRequestApprovalResponse
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("unmarshal approval response: %v", err)
	}
	if got := fmt.Sprint(result.Decision); got != string(appserver.CommandExecutionApprovalAcceptForSession) {
		t.Fatalf("approval decision = %q", got)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for remote approval turn")
	}
	for range messages {
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

func TestInteractiveRemoteTurnHandlesUserInputServerRequest(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	responses := make(chan remoteTUITestResponse, 1)
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
			switch req.Method {
			case string(appserver.MethodInitialize):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
			case string(appserver.MethodThreadStart):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"thread": map[string]any{"id": "thread-input"}},
				})
			case string(appserver.MethodTurnStart):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"turn": map[string]any{"id": "turn-input", "items": []any{}, "status": "inProgress"}},
				})
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      "input-remote",
					"method":  string(appserver.ServerRequestToolUserInput),
					"params": map[string]any{
						"threadId": "thread-input",
						"turnId":   "turn-input",
						"itemId":   "input-1",
						"questions": []any{
							map[string]any{
								"header":   "Scope",
								"id":       "scope",
								"question": "Where should this apply?",
								"isOther":  true,
								"isSecret": true,
								"options": []any{
									map[string]any{"label": "Plan", "description": "Only update the plan."},
									map[string]any{"label": "All", "description": "Apply everywhere."},
								},
							},
						},
						"autoResolutionMs": 60000,
					},
				})
				response, err := remoteTUITestReadResponse(ctx, conn)
				if err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				responses <- response
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"method":  string(appserver.NotificationTurnCompleted),
					"params":  map[string]any{"threadId": "thread-input", "turn": map[string]any{"id": "turn-input", "items": []any{}, "status": "completed"}},
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
	messages := make(chan bubbletea.Msg, 64)
	brokers := newRemoteTUIBrokers()
	done := make(chan struct{})
	go func() {
		runInteractiveRemoteTurn(ctx, &cli.RootOptions{}, endpoint, state, codextea.SubmitRequest{Prompt: "ask"}, messages, brokers, nil)
		close(done)
	}()

	request := remoteTUITestReadUserInputMessage(t, messages)
	if request.ID == "" || len(request.Questions) != 1 || request.Questions[0].ID != "scope" {
		t.Fatalf("user input message = %#v", request)
	}
	if !request.Questions[0].IsOther || !request.Questions[0].IsSecret {
		t.Fatalf("user input flags = %#v", request.Questions[0])
	}
	if request.AutoResolutionMS == nil || *request.AutoResolutionMS != 60000 {
		t.Fatalf("autoResolutionMS = %#v", request.AutoResolutionMS)
	}
	brokers.respond(codextea.ModalResponse{
		ID:   request.ID,
		Kind: codextea.ModalKindUserInput,
		UserInput: &codextea.UserInputDecision{
			Answers:     map[string]string{"scope": "All"},
			AnswerLists: map[string][]string{"scope": []string{"All", "user_note: include tests"}},
		},
	})

	response := remoteTUITestReadCapturedResponse(t, responses)
	if response.Error != nil {
		t.Fatalf("user input response error = %#v", response.Error)
	}
	var result appserver.ToolRequestUserInputResponse
	if err := json.Unmarshal(response.Result, &result); err != nil {
		t.Fatalf("unmarshal user input response: %v", err)
	}
	answers := result.Answers["scope"].Answers
	if len(answers) != 2 || answers[0] != "All" || answers[1] != "user_note: include tests" {
		t.Fatalf("answers = %#v", result.Answers)
	}
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for remote user input turn")
	}
	for range messages {
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

func TestRemoteServerRequestLongTailResponses(t *testing.T) {
	client := &remoteAppServerTUIClient{}
	ctx := context.Background()
	for _, tc := range []struct {
		name     string
		method   appserver.ServerRequestMethod
		params   string
		wantCode int
		wantErr  string
	}{
		{
			name:     "dynamic tool",
			method:   appserver.ServerRequestDynamicToolCall,
			params:   `{"threadId":"thread-1","turnId":"turn-1","callId":"call-1","tool":"lookup","arguments":{}}`,
			wantCode: -32000,
			wantErr:  "Dynamic tool calls are not available in TUI yet.",
		},
		{
			name:     "attestation",
			method:   appserver.ServerRequestAttestationGenerate,
			params:   `{}`,
			wantCode: -32000,
			wantErr:  "Attestation generation is not available in TUI.",
		},
		{
			name:     "malformed dynamic tool",
			method:   appserver.ServerRequestDynamicToolCall,
			params:   `"bad"`,
			wantCode: -32602,
			wantErr:  "invalid server request params",
		},
		{
			name:     "current time",
			method:   appserver.ServerRequestCurrentTimeRead,
			params:   `{}`,
			wantCode: -32000,
			wantErr:  "External current time is not available in TUI.",
		},
		{
			name:     "legacy patch approval",
			method:   appserver.ServerRequestApplyPatchApproval,
			params:   `{}`,
			wantCode: -32000,
			wantErr:  "Legacy patch approval requests are not available in TUI yet.",
		},
		{
			name:     "legacy command approval",
			method:   appserver.ServerRequestExecCommandApproval,
			params:   `{}`,
			wantCode: -32000,
			wantErr:  "Legacy command approval requests are not available in TUI yet.",
		},
		{
			name:     "unknown",
			method:   appserver.ServerRequestMethod("thread/unknown"),
			params:   `{}`,
			wantCode: -32000,
			wantErr:  "Unsupported app-server request: thread/unknown",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result, code, err := client.remoteServerRequestResult(ctx, tc.method, json.RawMessage(tc.params))
			if err == nil || code != tc.wantCode || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("result=%#v code=%d err=%v, want code=%d err containing %q", result, code, err, tc.wantCode, tc.wantErr)
			}
			if strings.Contains(err.Error(), "not implemented") || strings.Contains(err.Error(), "Go TUI remote client") {
				t.Fatalf("err = %q, want Rust-style unsupported request wording", err.Error())
			}
			if result != nil {
				t.Fatalf("result = %#v, want nil", result)
			}
		})
	}
}

func TestRemoteAppServerTUIClientMapsHookNotifications(t *testing.T) {
	messages := make(chan bubbletea.Msg, 2)
	client := &remoteAppServerTUIClient{messages: messages}
	turnID := "turn-hook"
	statusMessage := "checking command"
	startedParams, err := json.Marshal(appserver.HookRunStartedNotification{
		ThreadID: "thread-hook",
		TurnID:   &turnID,
		Run: appserver.HookRunSummary{
			EventName:     appserver.HookEventPreToolUse,
			Status:        appserver.HookRunRunning,
			StatusMessage: &statusMessage,
		},
	})
	if err != nil {
		t.Fatalf("marshal hook started: %v", err)
	}
	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(appserver.NotificationHookStarted),
		Params: startedParams,
	}); err != nil {
		t.Fatalf("handle hook started: %v", err)
	}

	completedParams, err := json.Marshal(appserver.HookRunCompletedNotification{
		ThreadID: "thread-hook",
		TurnID:   &turnID,
		Run: appserver.HookRunSummary{
			EventName: appserver.HookEventPostToolUse,
			Status:    appserver.HookRunFailed,
			Entries: []appserver.HookOutputEntry{
				{Kind: appserver.HookOutputWarning, Text: "Heads up from the hook"},
				{Kind: appserver.HookOutputError, Text: "hook exited with code 7"},
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal hook completed: %v", err)
	}
	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(appserver.NotificationHookCompleted),
		Params: completedParams,
	}); err != nil {
		t.Fatalf("handle hook completed: %v", err)
	}

	started, ok := (<-messages).(codextea.HookRunMsg)
	if !ok || !started.Running || started.ThreadID != "thread-hook" || started.TurnID != "turn-hook" || started.EventName != "preToolUse" || started.StatusMessage != "checking command" {
		t.Fatalf("started hook message = %#v ok=%v", started, ok)
	}
	completed, ok := (<-messages).(codextea.HookRunMsg)
	if !ok || completed.Running || completed.EventName != "postToolUse" || completed.Status != "failed" || len(completed.Entries) != 2 {
		t.Fatalf("completed hook message = %#v ok=%v", completed, ok)
	}
	if completed.Entries[0].Kind != "warning" || completed.Entries[1].Kind != "error" {
		t.Fatalf("completed hook entries = %#v", completed.Entries)
	}
}

func TestRemoteServerRequestChatGPTAuthRefreshUsesLocalAuth(t *testing.T) {
	clearAuthEnvApp(t)
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	t.Setenv(auth.LoginClientIDEnvOverride, "client-test")

	idToken := fakeJWTApp(map[string]any{
		"chatgpt_account_id": "workspace-1",
		"plan_type":          "pro",
	})
	if err := auth.PersistChatGPTTokens(home, &auth.ExchangedTokens{
		IDToken:      idToken,
		AccessToken:  "old-access-token",
		RefreshToken: "old-refresh-token",
	}); err != nil {
		t.Fatalf("PersistChatGPTTokens error = %v", err)
	}

	var requestBody map[string]string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&requestBody); err != nil {
			t.Fatalf("Decode refresh request error = %v", err)
		}
		writeJSON(t, w, map[string]string{
			"access_token":  "new-access-token",
			"refresh_token": "new-refresh-token",
		})
	}))
	defer server.Close()
	t.Setenv(auth.RefreshTokenURLEnvOverride, server.URL)

	client := &remoteAppServerTUIClient{}
	result, code, err := client.remoteServerRequestResult(context.Background(), appserver.ServerRequestChatGPTAuthTokensRefresh, json.RawMessage(`{"reason":"unauthorized","previousAccountId":"workspace-1"}`))
	if err != nil || code != -32603 {
		t.Fatalf("refresh result=%#v code=%d err=%v", result, code, err)
	}
	response, ok := result.(*appserver.ChatGPTAuthTokensRefreshResponse)
	if !ok {
		t.Fatalf("refresh response = %T", result)
	}
	if response.AccessToken != "new-access-token" || response.ChatGPTAccountID != "workspace-1" || response.ChatGPTPlanType == nil || *response.ChatGPTPlanType != "pro" {
		t.Fatalf("refresh response = %+v", response)
	}
	if requestBody["grant_type"] != "refresh_token" || requestBody["refresh_token"] != "old-refresh-token" || requestBody["client_id"] != "client-test" {
		t.Fatalf("refresh request body = %#v", requestBody)
	}
	loaded, err := auth.NewStore(home).Load()
	if err != nil {
		t.Fatalf("Load refreshed auth error = %v", err)
	}
	if loaded.Tokens["access_token"] != "new-access-token" || loaded.Tokens["refresh_token"] != "new-refresh-token" {
		t.Fatalf("stored refreshed auth = %#v", loaded.Tokens)
	}
}

func TestRemoteAppServerTUIClientUsesUnixSocketJSONLineTransport(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	requests := make(chan remoteTUITestRequest, 4)
	serverErrs := make(chan error, 1)
	state := codextui.NewState(nil)
	messages := make(chan bubbletea.Msg, 16)
	client := &remoteAppServerTUIClient{
		endpoint: appserverdaemon.NewUnixSocketEndpoint(`/tmp/codex.sock`),
		state:    state,
		messages: messages,
		unixDial: func(ctx context.Context, socketPath string) (net.Conn, error) {
			if socketPath != `/tmp/codex.sock` {
				return nil, fmt.Errorf("socket path = %q", socketPath)
			}
			clientConn, serverConn := net.Pipe()
			go remoteTUITestServeJSONLineAppServer(ctx, serverConn, requests, serverErrs)
			return clientConn, nil
		},
	}
	if err := client.connect(ctx); err != nil {
		t.Fatalf("connect unix transport: %v", err)
	}
	defer client.close()
	if err := client.initialize(ctx); err != nil {
		t.Fatalf("initialize: %v", err)
	}
	threadID, err := client.startThread(ctx, &cli.RootOptions{}, state)
	if err != nil {
		t.Fatalf("startThread: %v", err)
	}
	if threadID != "thread-unix" {
		t.Fatalf("threadID = %q", threadID)
	}
	if _, err := client.startTurn(ctx, &cli.RootOptions{}, state, threadID, codextea.SubmitRequest{Prompt: "hello unix"}); err != nil {
		t.Fatalf("startTurn: %v", err)
	}
	if err := client.readUntilTurnCompleted(ctx); err != nil {
		t.Fatalf("readUntilTurnCompleted: %v", err)
	}
	initialize := remoteTUITestReadCapturedRequest(t, requests)
	threadStart := remoteTUITestReadCapturedRequest(t, requests)
	turnStart := remoteTUITestReadCapturedRequest(t, requests)
	if initialize.Method != string(appserver.MethodInitialize) || threadStart.Method != string(appserver.MethodThreadStart) || turnStart.Method != string(appserver.MethodTurnStart) {
		t.Fatalf("methods = %q, %q, %q", initialize.Method, threadStart.Method, turnStart.Method)
	}
	var sawCompleted bool
	for {
		select {
		case message := <-messages:
			if event, ok := message.(codextea.ThreadEventMsg); ok && event.Event.Type == "turn.completed" {
				sawCompleted = true
			}
		default:
			if !sawCompleted {
				t.Fatal("missing remote turn completed event")
			}
			return
		}
	}
}

func TestInteractiveRemoteSessionPickerItemsLoadFromAppServer(t *testing.T) {
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
			case string(appserver.MethodThreadList):
				var params appserver.ThreadListParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				archived := params.Archived != nil && *params.Archived
				if params.CWD == nil || len(params.CWD.Values) != 1 || params.CWD.Values[0] != `D:\repo` {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("thread/list cwd params = %#v", params.CWD))
					return
				}
				active := remoteSessionTestThread("thread-active", "Remote Active", false, 0)
				active["updatedAt"] = fixedAppSessionTime().Add(time.Minute).Unix()
				active["recencyAt"] = fixedAppSessionTime().Add(time.Minute).Unix()
				data := []any{active}
				if archived {
					data = []any{remoteSessionTestThread("thread-archived", "Remote Archived", true, 0)}
				}
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"data": data, "nextCursor": nil, "backwardsCursor": nil}})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	items := interactiveRemoteSessionPickerItems(ctx, &cli.RootOptions{Shared: cli.SharedOptions{CWD: `D:\repo`}}, endpoint)
	if len(items) != 2 || items[0].ThreadID != "thread-active" || items[1].ThreadID != "thread-archived" || !items[1].Archived {
		t.Fatalf("remote session items = %#v", items)
	}
	initialize := remoteTUITestReadCapturedRequest(t, requests)
	activeList := remoteTUITestReadCapturedRequest(t, requests)
	archivedList := remoteTUITestReadCapturedRequest(t, requests)
	if initialize.Method != string(appserver.MethodInitialize) || activeList.Method != string(appserver.MethodThreadList) || archivedList.Method != string(appserver.MethodThreadList) {
		t.Fatalf("methods = %q, %q, %q", initialize.Method, activeList.Method, archivedList.Method)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

func TestInteractiveResumeSessionHandlerReadsHistoryFromStore(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	store := session.NewStore(filepath.Join(auth.DefaultCodexHome(), "sessions"))
	record := &session.Record{
		ID:      "thread-resume",
		Title:   "Named Session",
		Preview: "hello prompt",
		Metadata: session.Metadata{
			CWD:           `D:\repo`,
			ModelProvider: "openai",
			Source:        "cli",
		},
		Items: []session.Item{
			{Type: "user_message", Role: "user", Text: "hello prompt"},
			{Type: "agent_message", Role: "assistant", Text: "hello answer"},
			{Type: "reasoning", Data: map[string]any{"summary": "thought summary"}},
			{Type: "command_execution", Name: "go test", Status: "completed", Data: map[string]any{"output": "ok"}},
		},
	}
	if err := store.Save(record); err != nil {
		t.Fatalf("Save() error = %v", err)
	}

	response, err := interactiveResumeSessionHandler(nil)(codextui.SessionSelection{
		Kind:   codextui.SessionSelectionResume,
		Target: codextui.SessionTarget{ThreadID: "thread-resume"},
	})
	if err != nil {
		t.Fatalf("resume handler error = %v", err)
	}
	if response.Summary == nil || response.Summary.ThreadID != "thread-resume" || response.Summary.Preview != "hello prompt" {
		t.Fatalf("summary = %#v", response.Summary)
	}
	if len(response.Messages) != 4 {
		t.Fatalf("messages = %#v", response.Messages)
	}
	if response.Messages[0].Role != codextui.RoleUser || response.Messages[0].Text != "hello prompt" {
		t.Fatalf("user message = %#v", response.Messages[0])
	}
	if response.Messages[1].Role != codextui.RoleAssistant || response.Messages[1].Text != "hello answer" {
		t.Fatalf("assistant message = %#v", response.Messages[1])
	}
	if response.Messages[2].Role != codextui.RoleHistory || !strings.Contains(response.Messages[2].Text, "thought summary") {
		t.Fatalf("reasoning message = %#v", response.Messages[2])
	}
	if response.Messages[3].Role != codextui.RoleHistory || !strings.Contains(response.Messages[3].Text, "ok") {
		t.Fatalf("tool message = %#v", response.Messages[3])
	}
}

func TestInteractiveRemoteSessionActionHandlerCallsAppServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	requests := make(chan remoteTUITestRequest, 16)
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
			case string(appserver.MethodThreadFork):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"thread": remoteSessionTestThread("thread-forked", "Remote Forked", false, 0)}})
				return
			case string(appserver.MethodThreadArchive):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
				return
			case string(appserver.MethodThreadUnarchive):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"thread": remoteSessionTestThread("thread-source", "Remote Source", false, 0)}})
				return
			case string(appserver.MethodThreadDelete):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}})
				return
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	handler := interactiveRemoteSessionActionHandler(ctx, endpoint)
	source := codextui.SessionTarget{ThreadID: "thread-source"}
	forked, err := handler(codextui.SessionSelection{Kind: codextui.SessionSelectionFork, Target: source})
	if err != nil || forked == nil || forked.ThreadID != "thread-forked" {
		t.Fatalf("forked = %#v err=%v", forked, err)
	}
	if _, err := handler(codextui.SessionSelection{Kind: codextui.SessionSelectionArchive, Target: source}); err != nil {
		t.Fatalf("archive action error = %v", err)
	}
	unarchived, err := handler(codextui.SessionSelection{Kind: codextui.SessionSelectionUnarchive, Target: source})
	if err != nil || unarchived == nil || unarchived.ThreadID != "thread-source" {
		t.Fatalf("unarchived = %#v err=%v", unarchived, err)
	}
	if _, err := handler(codextui.SessionSelection{Kind: codextui.SessionSelectionDelete, Target: source}); err != nil {
		t.Fatalf("delete action error = %v", err)
	}

	seen := map[string]int{}
	for {
		select {
		case request := <-requests:
			seen[request.Method]++
		default:
			for _, method := range []appserver.Method{appserver.MethodThreadFork, appserver.MethodThreadArchive, appserver.MethodThreadUnarchive, appserver.MethodThreadDelete} {
				if seen[string(method)] != 1 {
					t.Fatalf("seen[%s] = %d, all seen = %#v", method, seen[string(method)], seen)
				}
			}
			select {
			case err := <-serverErrs:
				t.Fatalf("server error: %v", err)
			default:
			}
			return
		}
	}
}

func TestInteractiveRemoteUsageCallbacksCallAccountRPC(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	requests := make(chan remoteTUITestRequest, 12)
	serverErrs := make(chan error, 1)
	lifetime := int64(123456)
	peak := int64(4500)
	longestTurn := int64(7200)
	currentStreak := int64(3)
	longestStreak := int64(9)
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
			case string(appserver.MethodGetAccountTokenUsage):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"summary": map[string]any{
							"lifetimeTokens":        lifetime,
							"peakDailyTokens":       peak,
							"longestRunningTurnSec": longestTurn,
							"currentStreakDays":     currentStreak,
							"longestStreakDays":     longestStreak,
						},
						"dailyUsageBuckets": []map[string]any{
							{"startDate": "2026-06-29", "tokens": 10},
						},
					},
				})
				return
			case string(appserver.MethodGetAccountRateLimits):
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{
						"rateLimits":            map[string]any{},
						"rateLimitsByLimitId":   map[string]any{},
						"rateLimitResetCredits": map[string]any{"availableCount": 2},
					},
				})
				return
			case string(appserver.MethodConsumeAccountRateLimitResetCredit):
				var params auth.ConsumeRateLimitResetCreditParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.IdempotencyKey != "reset-key" {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("idempotency key = %q", params.IdempotencyKey))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result":  map[string]any{"outcome": string(auth.ResetCreditOutcomeAlreadyRedeemed)},
				})
				return
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	activity, err := interactiveRemoteReadTokenActivity(ctx, endpoint, chatwidget.TokenActivityWeekly)
	if err != nil {
		t.Fatalf("interactiveRemoteReadTokenActivity: %v", err)
	}
	if activity.Summary.LifetimeTokens == nil || *activity.Summary.LifetimeTokens != lifetime ||
		activity.Summary.PeakDailyTokens == nil || *activity.Summary.PeakDailyTokens != peak ||
		activity.Summary.LongestRunningTurnSec == nil || *activity.Summary.LongestRunningTurnSec != longestTurn ||
		activity.Summary.CurrentStreakDays == nil || *activity.Summary.CurrentStreakDays != currentStreak ||
		activity.Summary.LongestStreakDays == nil || *activity.Summary.LongestStreakDays != longestStreak {
		t.Fatalf("activity summary = %#v", activity.Summary)
	}
	if activity.DailyUsageBuckets == nil || len(*activity.DailyUsageBuckets) != 1 || (*activity.DailyUsageBuckets)[0].Tokens != 10 {
		t.Fatalf("activity buckets = %#v", activity.DailyUsageBuckets)
	}
	credits, err := interactiveRemoteReadRateLimitResetCredits(ctx, endpoint)
	if err != nil {
		t.Fatalf("interactiveRemoteReadRateLimitResetCredits: %v", err)
	}
	if credits != 2 {
		t.Fatalf("credits = %d", credits)
	}
	outcome, err := interactiveRemoteConsumeRateLimitResetCredit(ctx, endpoint, "reset-key")
	if err != nil {
		t.Fatalf("interactiveRemoteConsumeRateLimitResetCredit: %v", err)
	}
	if outcome != chatwidget.RateLimitResetOutcomeAlreadyRedeemed {
		t.Fatalf("outcome = %q", outcome)
	}
	seen := map[string]int{}
	for {
		select {
		case request := <-requests:
			seen[request.Method]++
		default:
			for _, method := range []appserver.Method{appserver.MethodGetAccountTokenUsage, appserver.MethodGetAccountRateLimits, appserver.MethodConsumeAccountRateLimitResetCredit} {
				if seen[string(method)] != 1 {
					t.Fatalf("seen[%s] = %d, all seen = %#v", method, seen[string(method)], seen)
				}
			}
			if seen[string(appserver.MethodInitialize)] != 3 {
				t.Fatalf("initialize count = %d, all seen = %#v", seen[string(appserver.MethodInitialize)], seen)
			}
			select {
			case err := <-serverErrs:
				t.Fatalf("server error: %v", err)
			default:
			}
			return
		}
	}
}

func TestInteractiveRemoteGoalCallbacksCallAppServer(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	requests := make(chan remoteTUITestRequest, 12)
	serverErrs := make(chan error, 1)
	budget := int64(75000)
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
			case string(appserver.MethodThreadGoalGet):
				var params appserver.GoalGetParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ThreadID != "thread-goal" {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("goal get threadID = %q", params.ThreadID))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{"goal": map[string]any{
						"threadId":        "thread-goal",
						"objective":       "ship parity",
						"tokenBudget":     budget,
						"tokensUsed":      1200,
						"timeUsedSeconds": 60,
						"status":          string(appserver.GoalActive),
						"createdAt":       1,
						"updatedAt":       2,
					}},
				})
				return
			case string(appserver.MethodThreadGoalSet):
				var params appserver.GoalSetParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ThreadID != "thread-goal" || params.Objective == nil || *params.Objective != "finish goal" || params.TokenBudget == nil || *params.TokenBudget != budget || params.Status == nil || *params.Status != appserver.GoalActive {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("goal set params = %#v", params))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{
					"jsonrpc": "2.0",
					"id":      req.ID,
					"result": map[string]any{"goal": map[string]any{
						"threadId":        "thread-goal",
						"objective":       *params.Objective,
						"tokenBudget":     *params.TokenBudget,
						"tokensUsed":      0,
						"timeUsedSeconds": 0,
						"status":          string(*params.Status),
						"createdAt":       3,
						"updatedAt":       4,
					}},
				})
				return
			case string(appserver.MethodThreadGoalClear):
				var params appserver.GoalClearParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ThreadID != "thread-goal" {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("goal clear threadID = %q", params.ThreadID))
					return
				}
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"cleared": true}})
				return
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	goal, err := interactiveRemoteReadGoal(ctx, endpoint, "thread-goal")
	if err != nil {
		t.Fatalf("interactiveRemoteReadGoal: %v", err)
	}
	if goal == nil || goal.Objective != "ship parity" || goal.TokenBudget == nil || *goal.TokenBudget != budget {
		t.Fatalf("read goal = %#v", goal)
	}
	objective := " finish goal "
	status := appserver.GoalActive
	setGoal, err := interactiveRemoteSetGoal(ctx, endpoint, " thread-goal ", &objective, &budget, &status)
	if err != nil {
		t.Fatalf("interactiveRemoteSetGoal: %v", err)
	}
	if setGoal.Objective != "finish goal" || setGoal.Status != appserver.GoalActive {
		t.Fatalf("set goal = %#v", setGoal)
	}
	cleared, err := interactiveRemoteClearGoal(ctx, endpoint, "thread-goal")
	if err != nil {
		t.Fatalf("interactiveRemoteClearGoal: %v", err)
	}
	if !cleared {
		t.Fatal("cleared = false, want true")
	}
	seen := map[string]int{}
	for {
		select {
		case request := <-requests:
			seen[request.Method]++
		default:
			for _, method := range []appserver.Method{appserver.MethodThreadGoalGet, appserver.MethodThreadGoalSet, appserver.MethodThreadGoalClear} {
				if seen[string(method)] != 1 {
					t.Fatalf("seen[%s] = %d, all seen = %#v", method, seen[string(method)], seen)
				}
			}
			if seen[string(appserver.MethodInitialize)] != 3 {
				t.Fatalf("initialize count = %d, all seen = %#v", seen[string(appserver.MethodInitialize)], seen)
			}
			select {
			case err := <-serverErrs:
				t.Fatalf("server error: %v", err)
			default:
			}
			return
		}
	}
}

func TestInteractiveRemoteAgentThreadEntriesLoadsLoadedSubagents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	requests := make(chan remoteTUITestRequest, 8)
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
			case string(appserver.MethodThreadRead):
				var params appserver.ThreadReadParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				switch params.ThreadID {
				case "thread-main":
					remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"thread": remoteAgentTestThread("thread-main", "", "", "", "idle", nil, nil)}})
				case "thread-worker":
					parent := "thread-main"
					remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"thread": remoteAgentTestThread("thread-worker", "Scout", "review", "subagent", "active", &parent, nil)}})
				default:
					remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected thread/read id %q", params.ThreadID))
					return
				}
			case string(appserver.MethodThreadLoadedList):
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"data": []string{"thread-main", "thread-worker"}, "nextCursor": nil}})
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	entries, err := interactiveRemoteAgentThreadEntries(ctx, endpoint, "thread-main")
	if err != nil {
		t.Fatalf("agent entries error = %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("entries = %#v", entries)
	}
	if !entries[0].IsPrimary || entries[0].ThreadID != "thread-main" {
		t.Fatalf("main entry = %#v", entries[0])
	}
	if entries[1].ThreadID != "thread-worker" || entries[1].AgentNickname != "Scout" || entries[1].AgentRole != "review" || !entries[1].IsRunning {
		t.Fatalf("worker entry = %#v", entries[1])
	}
	for _, method := range []appserver.Method{appserver.MethodInitialize, appserver.MethodThreadRead, appserver.MethodThreadLoadedList, appserver.MethodThreadRead} {
		if got := remoteTUITestReadCapturedRequest(t, requests); got.Method != string(method) {
			t.Fatalf("method = %s, want %s", got.Method, method)
		}
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

func TestInteractiveRemoteSwitchAgentThreadReadsTranscript(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	requests := make(chan remoteTUITestRequest, 4)
	serverErrs := make(chan error, 1)
	parent := "thread-main"
	turns := []any{map[string]any{
		"id":     "turn-worker",
		"status": "completed",
		"items": []any{
			map[string]any{"id": "user-1", "type": "userMessage", "text": "worker prompt"},
			map[string]any{"id": "agent-1", "type": "agentMessage", "text": "worker answer"},
		},
	}}
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
			case string(appserver.MethodThreadRead):
				var params appserver.ThreadReadParams
				if err := json.Unmarshal(req.Params, &params); err != nil {
					remoteTUITestSendErr(serverErrs, err)
					return
				}
				if params.ThreadID != "thread-worker" || !params.IncludeTurns {
					remoteTUITestSendErr(serverErrs, fmt.Errorf("thread/read params = %#v", params))
					return
				}
				thread := remoteAgentTestThread("thread-worker", "Scout", "review", "subagent", "active", &parent, turns)
				remoteTUITestWrite(ctx, conn, map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{"thread": thread}})
				return
			default:
				remoteTUITestSendErr(serverErrs, fmt.Errorf("unexpected method %s", req.Method))
				return
			}
		}
	}))
	defer server.Close()

	endpoint := appserverdaemon.NewWebSocketEndpoint("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	response, err := interactiveRemoteSwitchAgentThread(ctx, endpoint, "thread-worker")
	if err != nil {
		t.Fatalf("switch agent error = %v", err)
	}
	if response.Entry.ThreadID != "thread-worker" || response.Entry.AgentNickname != "Scout" || response.Entry.AgentRole != "review" || response.Entry.IsPrimary {
		t.Fatalf("entry = %#v", response.Entry)
	}
	if response.Status != "running" {
		t.Fatalf("status = %q, want running", response.Status)
	}
	if len(response.Messages) != 2 || response.Messages[0].Role != codextui.RoleUser || response.Messages[0].Text != "worker prompt" || response.Messages[1].Role != codextui.RoleAssistant || response.Messages[1].Text != "worker answer" {
		t.Fatalf("messages = %#v", response.Messages)
	}
	if initialize := remoteTUITestReadCapturedRequest(t, requests); initialize.Method != string(appserver.MethodInitialize) {
		t.Fatalf("initialize method = %s", initialize.Method)
	}
	if read := remoteTUITestReadCapturedRequest(t, requests); read.Method != string(appserver.MethodThreadRead) {
		t.Fatalf("read method = %s", read.Method)
	}
	select {
	case err := <-serverErrs:
		t.Fatalf("server error: %v", err)
	default:
	}
}

func TestRemoteAppServerTUIClientMapsGoalNotifications(t *testing.T) {
	messages := make(chan bubbletea.Msg, 2)
	state := codextui.NewState(nil)
	client := &remoteAppServerTUIClient{state: state, messages: messages}
	updatedParams, err := json.Marshal(appserver.GoalUpdatedNotification{
		ThreadID: "thread-goal",
		Goal: appserver.Goal{
			ThreadID:  "thread-goal",
			Objective: "ship runtime",
			Status:    appserver.GoalActive,
		},
	})
	if err != nil {
		t.Fatalf("marshal goal updated: %v", err)
	}
	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(appserver.NotificationThreadGoalUpdated),
		Params: updatedParams,
	}); err != nil {
		t.Fatalf("handle goal updated: %v", err)
	}
	clearedParams, err := json.Marshal(appserver.GoalClearedNotification{ThreadID: "thread-goal"})
	if err != nil {
		t.Fatalf("marshal goal cleared: %v", err)
	}
	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(appserver.NotificationThreadGoalCleared),
		Params: clearedParams,
	}); err != nil {
		t.Fatalf("handle goal cleared: %v", err)
	}

	updated, ok := (<-messages).(codextea.GoalUpdatedMsg)
	if !ok || updated.Goal.ThreadID != "thread-goal" || updated.Goal.Objective != "ship runtime" {
		t.Fatalf("goal updated message = %#v ok=%v", updated, ok)
	}
	cleared, ok := (<-messages).(codextea.GoalClearedMsg)
	if !ok || cleared.ThreadID != "thread-goal" {
		t.Fatalf("goal cleared message = %#v ok=%v", cleared, ok)
	}
	if state.ThreadID != "thread-goal" {
		t.Fatalf("state threadID = %q, want thread-goal", state.ThreadID)
	}
}

func TestRemoteAppServerTUIClientMapsWindowsSandboxSetupCompleted(t *testing.T) {
	messages := make(chan bubbletea.Msg, 1)
	client := &remoteAppServerTUIClient{messages: messages}
	params, err := json.Marshal(sandbox.WindowsSetupCompletedNotification{
		Mode:    sandbox.WindowsSetupElevated,
		Success: true,
	})
	if err != nil {
		t.Fatalf("marshal setup completion: %v", err)
	}
	if err := client.handleNotification(remoteAppServerMessage{
		Method: string(appserver.NotificationWindowsSandboxSetupCompleted),
		Params: params,
	}); err != nil {
		t.Fatalf("handle setup completion: %v", err)
	}

	msg, ok := (<-messages).(codextea.WindowsSandboxSetupCompletedMsg)
	if !ok || !msg.Completion.Success || msg.Completion.Mode != chatwidget.WindowsSandboxModeElevated {
		t.Fatalf("setup completion message = %#v ok=%v", msg, ok)
	}
}

type remoteTUITestRequest struct {
	ID     any             `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func remoteAgentTestThread(id string, nickname string, role string, source string, status string, parentID *string, turns []any) map[string]any {
	thread := remoteSessionTestThread(id, id, false, 0)
	thread["status"] = map[string]any{"type": status}
	if strings.TrimSpace(source) != "" {
		thread["threadSource"] = source
	}
	if strings.TrimSpace(nickname) != "" {
		thread["agentNickname"] = nickname
	}
	if strings.TrimSpace(role) != "" {
		thread["agentRole"] = role
	}
	if parentID != nil {
		thread["parentThreadId"] = *parentID
	}
	if turns != nil {
		thread["turns"] = turns
	}
	return thread
}

type remoteTUITestResponse struct {
	ID     any                      `json:"id"`
	Result json.RawMessage          `json:"result"`
	Error  *appserver.ResponseError `json:"error"`
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

func remoteTUITestReadResponse(ctx context.Context, conn *websocket.Conn) (remoteTUITestResponse, error) {
	var response remoteTUITestResponse
	typ, data, err := conn.Read(ctx)
	if err != nil {
		return response, err
	}
	if typ != websocket.MessageText {
		return response, fmt.Errorf("message type = %v", typ)
	}
	if err := json.Unmarshal(data, &response); err != nil {
		return response, err
	}
	return response, nil
}

func remoteTUITestServeJSONLineAppServer(ctx context.Context, conn net.Conn, requests chan<- remoteTUITestRequest, errs chan<- error) {
	defer conn.Close()
	decoder := json.NewDecoder(conn)
	encoder := json.NewEncoder(conn)
	for {
		var req remoteTUITestRequest
		if err := decoder.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(ctx.Err(), context.Canceled) {
				return
			}
			remoteTUITestSendErr(errs, err)
			return
		}
		requests <- req
		switch req.Method {
		case string(appserver.MethodInitialize):
			if err := encoder.Encode(map[string]any{"jsonrpc": "2.0", "id": req.ID, "result": map[string]any{}}); err != nil {
				remoteTUITestSendErr(errs, err)
				return
			}
		case string(appserver.MethodThreadStart):
			if err := encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"thread": map[string]any{"id": "thread-unix"}},
			}); err != nil {
				remoteTUITestSendErr(errs, err)
				return
			}
		case string(appserver.MethodTurnStart):
			if err := encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      req.ID,
				"result":  map[string]any{"turn": map[string]any{"id": "turn-unix", "items": []any{}, "status": "inProgress"}},
			}); err != nil {
				remoteTUITestSendErr(errs, err)
				return
			}
			if err := encoder.Encode(map[string]any{
				"jsonrpc": "2.0",
				"method":  string(appserver.NotificationTurnCompleted),
				"params":  map[string]any{"threadId": "thread-unix", "turn": map[string]any{"id": "turn-unix", "items": []any{}, "status": "completed"}},
			}); err != nil {
				remoteTUITestSendErr(errs, err)
				return
			}
		default:
			remoteTUITestSendErr(errs, fmt.Errorf("unexpected method %s", req.Method))
			return
		}
	}
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

func remoteTUITestReadCapturedResponse(t *testing.T, responses <-chan remoteTUITestResponse) remoteTUITestResponse {
	t.Helper()
	select {
	case response := <-responses:
		return response
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for remote TUI response")
		return remoteTUITestResponse{}
	}
}

func waitForRemoteTUIInterruptActive(t *testing.T, controller *remoteTUIInterruptController, threadID string, turnID string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		controller.mu.Lock()
		gotThreadID := controller.threadID
		gotTurnID := controller.turnID
		controller.mu.Unlock()
		if gotThreadID == threadID && gotTurnID == turnID {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	controller.mu.Lock()
	defer controller.mu.Unlock()
	t.Fatalf("active interrupt target = %q/%q, want %q/%q", controller.threadID, controller.turnID, threadID, turnID)
}

func remoteTUITestReadApprovalMessage(t *testing.T, messages <-chan bubbletea.Msg) codextea.ApprovalRequestMsg {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case message, ok := <-messages:
			if !ok {
				t.Fatal("remote TUI messages closed before approval request")
			}
			if approval, ok := message.(codextea.ApprovalRequestMsg); ok {
				return approval
			}
		case <-timeout:
			t.Fatal("timed out waiting for remote approval message")
		}
	}
}

func remoteTUITestReadUserInputMessage(t *testing.T, messages <-chan bubbletea.Msg) codextea.RequestUserInputMsg {
	t.Helper()
	timeout := time.After(2 * time.Second)
	for {
		select {
		case message, ok := <-messages:
			if !ok {
				t.Fatal("remote TUI messages closed before user input request")
			}
			if request, ok := message.(codextea.RequestUserInputMsg); ok {
				return request
			}
		case <-timeout:
			t.Fatal("timed out waiting for remote user input message")
		}
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
	useLocalExecRunner(t)
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
	useLocalExecRunner(t)
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
	if !strings.Contains(stdout.String(), "Review the code changes introduced by commit "+sha+" (\"Fix bug\")") {
		t.Fatalf("review stdout = %q", stdout.String())
	}
}

func TestExecReviewEndToEnd(t *testing.T) {
	useLocalExecRunner(t)
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "sk-test")
	dir := initGitRepo(t)
	writeFile(t, dir, "tracked.txt", "old\nnew\n")
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"exec", "-C", dir, "review", "--uncommitted"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("exec review returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Review the current code changes (staged, unstaged, and untracked files)") {
		t.Fatalf("exec review stdout = %q", stdout.String())
	}
}

func TestReviewCustomPromptEndToEnd(t *testing.T) {
	useLocalExecRunner(t)
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv("OPENAI_API_KEY", "sk-test")
	var stdout, stderr bytes.Buffer
	if err := Run(context.Background(), []string{"review", "check auth flow"}, strings.NewReader(""), &stdout, &stderr); err != nil {
		t.Fatalf("review returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "check auth flow") {
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
