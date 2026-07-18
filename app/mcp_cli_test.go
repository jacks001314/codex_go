package app

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"codex_go/mcp"
)

func TestMCPAddListGetRemove(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"mcp", "add", "fs", "--env", "ROOT=.", "--", "mcp-fs", "--readonly"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp add returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Added global MCP server 'fs'.") {
		t.Fatalf("add stdout = %q", stdout.String())
	}
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile config error = %v", err)
	}
	if !strings.Contains(string(data), "[mcp_servers.fs]") || !strings.Contains(string(data), `command = "mcp-fs"`) {
		t.Fatalf("config = %q", string(data))
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"mcp", "list"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp list returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "fs") || !strings.Contains(stdout.String(), "mcp-fs") {
		t.Fatalf("list stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"mcp", "get", "fs", "--json"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp get returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), `"name": "fs"`) || !strings.Contains(stdout.String(), `"type": "stdio"`) {
		t.Fatalf("get stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"mcp", "remove", "fs"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp remove returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Removed global MCP server 'fs'.") {
		t.Fatalf("remove stdout = %q", stdout.String())
	}
}

func TestMCPHTTPLoginLogout(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	oldOpenBrowser := openMCPLoginBrowser
	openMCPLoginBrowser = func(target string) error { return nil }
	defer func() { openMCPLoginBrowser = oldOpenBrowser }()
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"mcp", "add", "docs", "--url", "https://mcp.example.test/mcp", "--oauth-client-id", "client-1", "--oauth-resource", "resource-1"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp add returned error: %v", err)
	}
	stdout.Reset()
	if err := Run(context.Background(), []string{"mcp", "login", "docs", "--scopes", "read,write"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp login returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "https://mcp.example.test/mcp/oauth/authorize") || !strings.Contains(stdout.String(), "client_id=client-1") || !strings.Contains(stdout.String(), "resource=resource-1") || !strings.Contains(stdout.String(), "scope=read+write") {
		t.Fatalf("login stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"mcp", "list", "--json"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp list returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), `"auth_status": "notLoggedIn"`) {
		t.Fatalf("list without token stdout = %q", stdout.String())
	}

	expiresAt := time.Now().Add(time.Hour).UnixMilli()
	if err := mcp.NewOAuthStore(home).Save(&mcp.OAuthTokenSet{
		ServerName:      "docs",
		ServerURL:       "https://mcp.example.test/mcp",
		ClientID:        "client-1",
		AccessToken:     "access-token",
		ExpiresAtMillis: &expiresAt,
	}); err != nil {
		t.Fatalf("Save OAuth token error = %v", err)
	}
	stdout.Reset()
	if err := Run(context.Background(), []string{"mcp", "list", "--json"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp list returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), `"auth_status": "oAuth"`) {
		t.Fatalf("list with token stdout = %q", stdout.String())
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"mcp", "logout", "docs"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp logout returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Removed OAuth credentials for 'docs'.") {
		t.Fatalf("logout stdout = %q", stdout.String())
	}
	stdout.Reset()
	if err := Run(context.Background(), []string{"mcp", "logout", "docs"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp second logout returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "No OAuth credentials stored for 'docs'.") {
		t.Fatalf("second logout stdout = %q", stdout.String())
	}
}

func TestMCPAddStartsOAuthWhenSupported(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	var tokenForm url.Values
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server/mcp":
			writeMCPCLIJSON(t, w, map[string]any{
				"authorization_endpoint": "http://" + r.Host + "/authorize",
				"token_endpoint":         "http://" + r.Host + "/token",
				"scopes_supported":       []string{"read", "write"},
			})
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm() error = %v", err)
			}
			tokenForm = cloneURLValues(r.Form)
			writeMCPCLIJSON(t, w, map[string]any{
				"access_token":  "access-add",
				"refresh_token": "refresh-add",
				"expires_in":    3600,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer issuer.Close()

	var browserURL string
	withMCPLoginBrowser(t, func(target string) error {
		browserURL = target
		mcpCompleteBrowserOAuthCallback(t, target, "code-add")
		return nil
	})
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"mcp", "add", "docs", "--url", issuer.URL + "/mcp", "--oauth-client-id", "client-1"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp add returned error: %v", err)
	}
	text := stdout.String()
	for _, want := range []string{
		"Added global MCP server 'docs'.",
		"Detected OAuth support. Starting OAuth flow",
		"Authorize `docs` by opening this URL in your browser:",
		"Successfully logged in.",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("add stdout missing %q:\n%s", want, text)
		}
	}
	parsed, err := url.Parse(browserURL)
	if err != nil {
		t.Fatalf("Parse browser URL error = %v", err)
	}
	if parsed.Query().Get("client_id") != "client-1" || parsed.Query().Get("scope") != "read write" {
		t.Fatalf("browser URL query = %#v", parsed.Query())
	}
	if tokenForm.Get("client_id") != "client-1" || tokenForm.Get("code") != "code-add" {
		t.Fatalf("token form = %#v", tokenForm)
	}
	tokens, err := mcp.NewOAuthStore(home).Load("docs", issuer.URL+"/mcp")
	if err != nil {
		t.Fatalf("Load OAuth token error = %v", err)
	}
	if tokens == nil || tokens.AccessToken != "access-add" || tokens.RefreshToken != "refresh-add" {
		t.Fatalf("stored tokens = %#v", tokens)
	}
}

func TestMCPAddUnknownOAuthPrintsLoginHint(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "metadata failed", http.StatusInternalServerError)
	}))
	defer issuer.Close()

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"mcp", "add", "docs", "--url", issuer.URL + "/mcp", "--oauth-client-id", "client-1"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp add returned error: %v", err)
	}
	text := stdout.String()
	if !strings.Contains(text, "MCP server may or may not require login. Run `codex mcp login docs` to login.") {
		t.Fatalf("add stdout missing login hint:\n%s", text)
	}
	if strings.Contains(text, "Detected OAuth support") {
		t.Fatalf("add stdout should not start OAuth:\n%s", text)
	}
}

func TestMCPListGetRustShapeAndRedaction(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	writeMCPCLIConfig(t, home, `
[mcp_servers.docs]
url = "https://mcp.example.test/mcp"
bearer_token_env_var = "DOCS_TOKEN"
http_headers = { "X-Static" = "static-secret" }
env_http_headers = { "X-Token" = "DOCS_TOKEN" }
oauth_resource = "resource-1"
scopes = ["read", "write"]

[mcp_servers.docs.oauth]
client_id = "client-1"

[mcp_servers.fs]
command = "mcp-fs"
args = ["--readonly"]
env = { "TOKEN" = "secret", "HOME" = "/tmp" }
env_vars = ["APP_TOKEN", { name = "REMOTE_TOKEN", source = "remote" }]
cwd = "/workspace"
startup_timeout_sec = 1.5
tool_timeout_sec = 2
enabled_tools = ["read"]
disabled_tools = ["write"]
default_tools_approval_mode = "approve"
required = true
supports_parallel_tool_calls = true

[mcp_servers.fs.tools.search]
approval_mode = "prompt"
`)

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"mcp", "list"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp list returned error: %v", err)
	}
	listText := stdout.String()
	for _, want := range []string{
		"Name", "Command", "Args", "Env", "Cwd", "Auth",
		"fs", "mcp-fs", "--readonly", "HOME=*****", "TOKEN=*****", "APP_TOKEN=*****", "REMOTE_TOKEN=*****", "/workspace",
		"Url", "Bearer Token Env Var", "docs", "https://mcp.example.test/mcp", "DOCS_TOKEN",
	} {
		if !strings.Contains(listText, want) {
			t.Fatalf("list stdout missing %q:\n%s", want, listText)
		}
	}
	if strings.Contains(listText, "secret") || strings.Contains(listText, "static-secret") {
		t.Fatalf("list stdout leaked secret:\n%s", listText)
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"mcp", "list", "--json"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp list --json returned error: %v", err)
	}
	var listJSON []map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &listJSON); err != nil {
		t.Fatalf("Unmarshal list JSON error = %v; stdout = %s", err, stdout.String())
	}
	fs := mcpJSONServer(t, listJSON, "fs")
	if fs["startup_timeout_sec"] != 1.5 || fs["tool_timeout_sec"] != float64(2) {
		t.Fatalf("fs timeouts = %#v %#v", fs["startup_timeout_sec"], fs["tool_timeout_sec"])
	}
	fsTransport := fs["transport"].(map[string]any)
	if fsTransport["type"] != "stdio" || fsTransport["command"] != "mcp-fs" || fsTransport["cwd"] != "/workspace" {
		t.Fatalf("fs transport = %#v", fsTransport)
	}
	env := fsTransport["env"].(map[string]any)
	if env["TOKEN"] != "secret" || env["HOME"] != "/tmp" {
		t.Fatalf("fs env JSON = %#v", env)
	}
	envVars := fsTransport["env_vars"].([]any)
	if envVars[0] != "APP_TOKEN" || envVars[1].(map[string]any)["name"] != "REMOTE_TOKEN" || envVars[1].(map[string]any)["source"] != "remote" {
		t.Fatalf("fs env_vars JSON = %#v", envVars)
	}
	docs := mcpJSONServer(t, listJSON, "docs")
	docsTransport := docs["transport"].(map[string]any)
	if docsTransport["type"] != "streamable_http" || docsTransport["bearer_token_env_var"] != "DOCS_TOKEN" {
		t.Fatalf("docs transport = %#v", docsTransport)
	}
	if docsTransport["http_headers"].(map[string]any)["X-Static"] != "static-secret" || docsTransport["env_http_headers"].(map[string]any)["X-Token"] != "DOCS_TOKEN" {
		t.Fatalf("docs headers JSON = %#v", docsTransport)
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"mcp", "get", "fs"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp get fs returned error: %v", err)
	}
	getFS := stdout.String()
	for _, want := range []string{
		"enabled_tools: read",
		"disabled_tools: write",
		"transport: stdio",
		"cwd: /workspace",
		"env: HOME=*****, TOKEN=*****, APP_TOKEN=*****, REMOTE_TOKEN=*****",
		"startup_timeout_sec: 1.5",
		"tool_timeout_sec: 2",
		"default_tools_approval_mode: approve",
	} {
		if !strings.Contains(getFS, want) {
			t.Fatalf("get fs stdout missing %q:\n%s", want, getFS)
		}
	}
	if strings.Contains(getFS, "secret") {
		t.Fatalf("get fs stdout leaked secret:\n%s", getFS)
	}

	stdout.Reset()
	if err := Run(context.Background(), []string{"mcp", "get", "docs"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp get docs returned error: %v", err)
	}
	getDocs := stdout.String()
	for _, want := range []string{
		"transport: streamable_http",
		"url: https://mcp.example.test/mcp",
		"bearer_token_env_var: DOCS_TOKEN",
		"http_headers: X-Static=*****",
		"env_http_headers: X-Token=DOCS_TOKEN",
	} {
		if !strings.Contains(getDocs, want) {
			t.Fatalf("get docs stdout missing %q:\n%s", want, getDocs)
		}
	}
	if strings.Contains(getDocs, "static-secret") || strings.Contains(getDocs, "client-1") || strings.Contains(getDocs, "resource-1") {
		t.Fatalf("get docs stdout diverged from Rust or leaked secret:\n%s", getDocs)
	}
}

func TestMCPGetDisabledServerShowsSingleLineLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	writeMCPCLIConfig(t, home, `
[mcp_servers.docs]
command = "docs-server"
enabled = false
`)

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"mcp", "get", "docs"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp get returned error: %v", err)
	}
	if got := strings.TrimSpace(stdout.String()); got != "docs (disabled)" {
		t.Fatalf("get disabled stdout = %q", got)
	}
}

func TestMCPRemovePreservesRustConfigFields(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	writeMCPCLIConfig(t, home, `
[mcp_servers.docs]
url = "https://mcp.example.test/mcp"
bearer_token_env_var = "DOCS_TOKEN"
http_headers = { "X-Static" = "static-secret" }
env_http_headers = { "X-Token" = "DOCS_TOKEN" }
oauth_resource = "resource-1"

[mcp_servers.docs.oauth]
client_id = "client-1"

[mcp_servers.fs]
command = "mcp-fs"
args = ["--readonly"]
env = { "TOKEN" = "secret" }
env_vars = ["APP_TOKEN", { name = "REMOTE_TOKEN", source = "remote" }]
cwd = "/workspace"
startup_timeout_sec = 1.5
tool_timeout_sec = 2
enabled_tools = ["read"]
disabled_tools = ["write"]
default_tools_approval_mode = "approve"
required = true
supports_parallel_tool_calls = true

[mcp_servers.fs.tools.search]
approval_mode = "prompt"
`)

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"mcp", "remove", "docs"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp remove returned error: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, "config.toml"))
	if err != nil {
		t.Fatalf("ReadFile config error = %v", err)
	}
	text := string(data)
	for _, want := range []string{
		`[mcp_servers.fs]`,
		`command = "mcp-fs"`,
		`args = ["--readonly"]`,
		`cwd = "/workspace"`,
		`env_vars = ["APP_TOKEN", { name = "REMOTE_TOKEN", source = "remote" }]`,
		`startup_timeout_sec = 1.5`,
		`tool_timeout_sec = 2`,
		`enabled_tools = ["read"]`,
		`disabled_tools = ["write"]`,
		`default_tools_approval_mode = "approve"`,
		`required = true`,
		`supports_parallel_tool_calls = true`,
		`[mcp_servers.fs.tools.search]`,
		`approval_mode = "prompt"`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("config after remove missing %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "mcp_servers.docs") || strings.Contains(text, "client-1") || strings.Contains(text, "static-secret") {
		t.Fatalf("removed server or secret remained:\n%s", text)
	}
}

func TestMCPLoginUsesConfiguredScopes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	writeMCPCLIConfig(t, home, `
[mcp_servers.docs]
url = "http://127.0.0.1:9/mcp"
scopes = ["read", "write"]

[mcp_servers.docs.oauth]
client_id = "client-1"
`)
	oldOpenBrowser := openMCPLoginBrowser
	openMCPLoginBrowser = func(target string) error { return nil }
	defer func() { openMCPLoginBrowser = oldOpenBrowser }()

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"mcp", "login", "docs"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp login returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "scope=read+write") {
		t.Fatalf("login stdout missing configured scope: %q", stdout.String())
	}
}

func TestMCPLoginDiscoversScopesWhenUnconfigured(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	var tokenForm url.Values
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server/mcp":
			writeMCPCLIJSON(t, w, map[string]any{
				"authorization_endpoint": "http://" + r.Host + "/authorize",
				"token_endpoint":         "http://" + r.Host + "/token",
				"scopes_supported":       []string{"read", "write"},
			})
		case "/token":
			if err := r.ParseForm(); err != nil {
				t.Fatalf("ParseForm() error = %v", err)
			}
			tokenForm = cloneURLValues(r.Form)
			writeMCPCLIJSON(t, w, map[string]any{
				"access_token":  "access-login",
				"refresh_token": "refresh-login",
				"expires_in":    3600,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer issuer.Close()
	writeMCPCLIConfig(t, home, `
[mcp_servers.docs]
url = "`+issuer.URL+`/mcp"

[mcp_servers.docs.oauth]
client_id = "client-1"
`)

	var browserURL string
	withMCPLoginBrowser(t, func(target string) error {
		browserURL = target
		mcpCompleteBrowserOAuthCallback(t, target, "code-login")
		return nil
	})
	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"mcp", "login", "docs"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp login returned error: %v", err)
	}
	if !strings.Contains(stdout.String(), "Successfully logged in to MCP server 'docs'.") {
		t.Fatalf("login stdout = %q", stdout.String())
	}
	parsed, err := url.Parse(browserURL)
	if err != nil {
		t.Fatalf("Parse browser URL error = %v", err)
	}
	if parsed.Query().Get("scope") != "read write" {
		t.Fatalf("browser URL query = %#v", parsed.Query())
	}
	if tokenForm.Get("code") != "code-login" {
		t.Fatalf("token form = %#v", tokenForm)
	}
}

func TestMCPAddRetriesDiscoveredScopesWithoutScopes(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	issuer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/.well-known/oauth-authorization-server/mcp":
			writeMCPCLIJSON(t, w, map[string]any{
				"authorization_endpoint": "http://" + r.Host + "/authorize",
				"token_endpoint":         "http://" + r.Host + "/token",
				"scopes_supported":       []string{"read"},
			})
		case "/token":
			writeMCPCLIJSON(t, w, map[string]any{
				"access_token":  "access-retry",
				"refresh_token": "refresh-retry",
				"expires_in":    3600,
			})
		default:
			http.NotFound(w, r)
		}
	}))
	defer issuer.Close()

	var browserScopes []string
	withMCPLoginBrowser(t, func(target string) error {
		parsed, err := url.Parse(target)
		if err != nil {
			t.Fatalf("Parse browser URL error = %v", err)
		}
		browserScopes = append(browserScopes, parsed.Query().Get("scope"))
		if len(browserScopes) == 1 {
			mcpCompleteBrowserOAuthError(t, target, "invalid_scope", "scope rejected")
			return nil
		}
		mcpCompleteBrowserOAuthCallback(t, target, "code-retry")
		return nil
	})

	var stdout bytes.Buffer
	if err := Run(context.Background(), []string{"mcp", "add", "docs", "--url", issuer.URL + "/mcp", "--oauth-client-id", "client-1"}, strings.NewReader(""), &stdout, &bytes.Buffer{}); err != nil {
		t.Fatalf("mcp add returned error: %v", err)
	}
	if len(browserScopes) != 2 || browserScopes[0] != "read" || browserScopes[1] != "" {
		t.Fatalf("browser scopes = %#v", browserScopes)
	}
	text := stdout.String()
	if !strings.Contains(text, "OAuth provider rejected discovered scopes. Retrying without scopes") || !strings.Contains(text, "Successfully logged in.") {
		t.Fatalf("add stdout = %q", text)
	}
}

func mcpJSONServer(t *testing.T, entries []map[string]any, name string) map[string]any {
	t.Helper()
	for _, entry := range entries {
		if entry["name"] == name {
			return entry
		}
	}
	t.Fatalf("server %q missing from %#v", name, entries)
	return nil
}

func writeMCPCLIConfig(t *testing.T, home string, body string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(strings.TrimSpace(body)+"\n"), 0o600); err != nil {
		t.Fatalf("WriteFile config error = %v", err)
	}
}

func withMCPLoginBrowser(t *testing.T, open func(string) error) {
	t.Helper()
	oldOpenBrowser := openMCPLoginBrowser
	openMCPLoginBrowser = open
	t.Cleanup(func() {
		openMCPLoginBrowser = oldOpenBrowser
	})
}

func mcpCompleteBrowserOAuthCallback(t *testing.T, target string, code string) {
	t.Helper()
	callbackURL := mcpBrowserCallbackURL(t, target, url.Values{"code": {code}})
	response, err := http.Get(callbackURL)
	if err != nil {
		t.Fatalf("OAuth callback GET error = %v", err)
	}
	_ = response.Body.Close()
}

func mcpCompleteBrowserOAuthError(t *testing.T, target string, code string, description string) {
	t.Helper()
	callbackURL := mcpBrowserCallbackURL(t, target, url.Values{
		"error":             {code},
		"error_description": {description},
	})
	response, err := http.Get(callbackURL)
	if err != nil {
		t.Fatalf("OAuth error callback GET error = %v", err)
	}
	_ = response.Body.Close()
}

func mcpBrowserCallbackURL(t *testing.T, target string, extra url.Values) string {
	t.Helper()
	parsed, err := url.Parse(target)
	if err != nil {
		t.Fatalf("Parse browser URL error = %v", err)
	}
	query := parsed.Query()
	redirectURI := query.Get("redirect_uri")
	if redirectURI == "" {
		t.Fatalf("browser URL missing redirect_uri: %s", target)
	}
	callbackValues := url.Values{}
	for key, values := range extra {
		for _, value := range values {
			callbackValues.Add(key, value)
		}
	}
	if state := query.Get("state"); state != "" {
		callbackValues.Set("state", state)
	}
	separator := "?"
	if strings.Contains(redirectURI, "?") {
		separator = "&"
	}
	return redirectURI + separator + callbackValues.Encode()
}

func writeMCPCLIJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("Encode JSON error = %v", err)
	}
}

func cloneURLValues(values url.Values) url.Values {
	cloned := url.Values{}
	for key, list := range values {
		cloned[key] = append([]string(nil), list...)
	}
	return cloned
}
