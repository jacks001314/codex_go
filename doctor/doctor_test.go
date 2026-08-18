package doctor

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"database/sql"
	"encoding/json"
	"encoding/pem"
	"errors"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"codex_go/appserverdaemon"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/codexapi"
	"codex_go/config"
	"codex_go/install"
	"codex_go/mcp"
	"codex_go/model"
	"codex_go/rollout"
	"codex_go/shell"

	"github.com/coder/websocket"
)

func TestReportBuildsLocalChecks(t *testing.T) {
	builder := NewBuilder()
	builder.httpClient = localOnlyHTTPClient(t)
	report := builder.Build(&Options{
		CodexHome: t.TempDir(),
		Root: cli.RootOptions{
			Shared: cli.SharedOptions{OSSProvider: model.OllamaOSSProviderID},
		},
	})
	if report.SchemaVersion != 1 || len(report.Checks) == 0 {
		t.Fatalf("report = %+v", report)
	}
	if report.OverallStatus == "" {
		t.Fatalf("overall status missing")
	}
	if !hasCategory(report, "system") || !hasCategory(report, "auth") || !hasCategory(report, "state") {
		t.Fatalf("missing expected categories: %+v", report.Checks)
	}
	if !hasCheckID(report, "installation") || hasCheckID(report, "install.identity") {
		t.Fatalf("installation check id mismatch: %+v", report.Checks)
	}
	wantChecks := []struct {
		id       string
		category string
	}{
		{"system.environment", "system"},
		{"system.disk", "disk"},
		{"security.endpoint", "security"},
		{"installation", "install"},
		{"runtime.provenance", "runtime"},
		{"runtime.search", "search"},
		{"config.load", "config"},
		{"auth.credentials", "auth"},
		{"updates.status", "updates"},
		{"network.env", "network"},
		{"network.websocket_reachability", "websocket"},
		{"mcp.config", "mcp"},
		{"sandbox.helpers", "sandbox"},
		{"terminal.env", "terminal"},
		{"git.environment", "git"},
		{"terminal.title", "title"},
		{"state.paths", "state"},
		{"state.rollout_db_parity", "threads"},
		{"app_server.status", "app-server"},
		{"network.provider_reachability", "reachability"},
		{"desktop.app.version", "desktop"},
		{"desktop.security", "desktop"},
		{"desktop.updates", "desktop"},
		{"git.worktree.dev_drive", "git"},
	}
	if len(report.Checks) != len(wantChecks) {
		t.Fatalf("check IDs = %v, want %d checks", checkIDs(report), len(wantChecks))
	}
	for i, want := range wantChecks {
		got := report.Checks[i]
		if got.ID != want.id || got.Category != want.category {
			t.Fatalf("check[%d] = %s/%s, want %s/%s; all IDs = %v", i, got.ID, got.Category, want.id, want.category, checkIDs(report))
		}
	}
}

func TestSystemCheckReportsLocaleEditorAndPagerDetails(t *testing.T) {
	t.Setenv("LC_ALL", "")
	t.Setenv("LC_CTYPE", "")
	t.Setenv("LANG", "en_US.UTF-8")
	t.Setenv("EDITOR", "vim")
	t.Setenv("VISUAL", "")
	t.Setenv("PAGER", "less -R")

	check := systemCheck()
	if check.Status != CheckStatusOK || check.Summary != "OS language en-US" {
		t.Fatalf("check = %+v", check)
	}
	for _, want := range []string{"os type: " + runtime.GOOS, "os language: en-US", "VISUAL: not set", "EDITOR: vim", "PAGER: less -R"} {
		if !containsDetail(check, want) {
			t.Fatalf("missing detail %q in %#v", want, check.Details)
		}
	}
}

func TestRuntimeCheckReportsBuildCommitAndInstallMethod(t *testing.T) {
	t.Setenv("CODEX_BUILD_COMMIT", "commit-123")

	check := runtimeCheck()
	if check.Status != CheckStatusOK {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "commit: commit-123") || !detailHasPrefix(check, "install method: ") {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestInstallCheckReportsNPMRootMatchLikeRust(t *testing.T) {
	home := t.TempDir()
	npmRoot := filepath.Join(home, "npm")
	packageRoot := filepath.Join(npmRoot, "@jacks001314", "codex-go")
	t.Setenv("CODEX_MANAGED_BY_NPM", "1")
	unsetEnvForDoctor(t, "CODEX_MANAGED_BY_BUN")
	t.Setenv("CODEX_MANAGED_PACKAGE_ROOT", packageRoot)
	withNPMRootCommandForDoctor(t, func() (string, error) {
		return npmRoot + "\n", nil
	})
	withCodexPathEntriesCommandForDoctor(t, func() (string, error) {
		return filepath.Join(home, "bin", "codex") + "\n" + filepath.Join(home, "other", "codex") + "\n", nil
	})

	check := installCheck(home, true, func() (string, error) {
		return filepath.Join(home, "bin", "codex"), nil
	})
	if check.Status != CheckStatusOK || check.Summary != "installation looks consistent" {
		t.Fatalf("check = %+v", check)
	}
	for _, want := range []string{
		"install context: npm",
		"managed by npm: true",
		"managed by bun: false",
		"managed package root: " + packageRoot,
		"PATH codex entries: 2",
		"PATH codex #1: " + filepath.Join(home, "bin", "codex"),
		"npm update target: " + packageRoot,
	} {
		if !containsDetail(check, want) {
			t.Fatalf("missing detail %q in %#v", want, check.Details)
		}
	}
}

func TestInstallCheckReportsNPMRootMismatchLikeRust(t *testing.T) {
	home := t.TempDir()
	runningRoot := filepath.Join(home, "running", "@jacks001314", "codex-go")
	npmRoot := filepath.Join(home, "npm")
	npmPackageRoot := filepath.Join(npmRoot, "@jacks001314", "codex-go")
	t.Setenv("CODEX_MANAGED_BY_NPM", "1")
	unsetEnvForDoctor(t, "CODEX_MANAGED_BY_BUN")
	t.Setenv("CODEX_MANAGED_PACKAGE_ROOT", runningRoot)
	withNPMRootCommandForDoctor(t, func() (string, error) {
		return npmRoot + "\n", nil
	})
	withCodexPathEntriesCommandForDoctor(t, func() (string, error) {
		return "", nil
	})

	check := installCheck(home, false, func() (string, error) {
		return filepath.Join(home, "bin", "codex"), nil
	})
	if check.Status != CheckStatusFail || check.Summary != "npm install -g @jacks001314/codex-go@latest would update a different install" {
		t.Fatalf("check = %+v", check)
	}
	for _, want := range []string{
		"running package root: " + runningRoot,
		"npm package root: " + npmPackageRoot,
	} {
		if !containsDetail(check, want) {
			t.Fatalf("missing detail %q in %#v", want, check.Details)
		}
	}
	if check.Remediation == nil || !strings.Contains(*check.Remediation, "Fix PATH or npm prefix") || !strings.Contains(*check.Remediation, runningRoot) || !strings.Contains(*check.Remediation, npmPackageRoot) {
		t.Fatalf("remediation = %#v", check.Remediation)
	}
}

func TestInstallCheckReportsMissingNPMPackageRootLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_MANAGED_BY_NPM", "1")
	unsetEnvForDoctor(t, "CODEX_MANAGED_BY_BUN")
	unsetEnvForDoctor(t, "CODEX_MANAGED_PACKAGE_ROOT")
	withNPMRootCommandForDoctor(t, func() (string, error) {
		t.Fatal("npm root should not be called without CODEX_MANAGED_PACKAGE_ROOT")
		return "", nil
	})
	withCodexPathEntriesCommandForDoctor(t, func() (string, error) {
		return "", nil
	})

	check := installCheck(home, false, func() (string, error) {
		return filepath.Join(home, "bin", "codex"), nil
	})
	if check.Status != CheckStatusWarning || check.Summary != "npm-managed launch is missing package-root provenance" {
		t.Fatalf("check = %+v", check)
	}
	if check.Remediation == nil || *check.Remediation != "Reinstall or update Codex so the JS shim provides CODEX_MANAGED_PACKAGE_ROOT." {
		t.Fatalf("remediation = %#v", check.Remediation)
	}
}

func TestInstallCheckIgnoresInheritedManagedEnvForCargoBinaryLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_MANAGED_BY_NPM", "1")
	t.Setenv("CODEX_MANAGED_BY_BUN", "")
	t.Setenv("CODEX_MANAGED_PACKAGE_ROOT", filepath.Join(home, "npm", "@jacks001314", "codex-go"))
	withNPMRootCommandForDoctor(t, func() (string, error) {
		t.Fatal("npm root should not be called for target/release binary")
		return "", nil
	})
	withCodexPathEntriesCommandForDoctor(t, func() (string, error) {
		return "", nil
	})

	check := installCheck(home, false, func() (string, error) {
		return filepath.Join(home, "target", "release", "codex"), nil
	})
	if check.Status != CheckStatusOK || check.Summary != "installation looks consistent" {
		t.Fatalf("check = %+v", check)
	}
	for _, want := range []string{
		"install context: other",
		"ignored inherited package-manager launch env for cargo-built binary",
		"managed by npm: false",
		"managed by bun: true",
	} {
		if !containsDetail(check, want) {
			t.Fatalf("missing detail %q in %#v", want, check.Details)
		}
	}
}

func TestSearchProviderForDoctorDetectsBundledPath(t *testing.T) {
	root := t.TempDir()
	pathDir := filepath.Join(root, "codex-path")
	command := filepath.Join(pathDir, "rg")
	ctx := &install.InstallContext{PackageLayout: &install.CodexPackageLayout{PathDir: &pathDir}}
	if got := searchProviderForDoctor(ctx, command); got != "bundled" {
		t.Fatalf("searchProviderForDoctor() = %q, want bundled", got)
	}
	if got := searchProviderForDoctor(ctx, "rg"); got != "system" {
		t.Fatalf("searchProviderForDoctor(system) = %q", got)
	}
}

func TestSearchCommandPathReadinessRequiresExecutableFile(t *testing.T) {
	command := filepath.Join(t.TempDir(), "rg")
	if runtime.GOOS == "windows" {
		command += ".exe"
	}
	if err := os.WriteFile(command, []byte(""), 0o600); err != nil {
		t.Fatalf("write command: %v", err)
	}

	readiness, err := searchCommandPathReadiness(command)
	if runtime.GOOS == "windows" {
		if err != nil {
			t.Fatalf("searchCommandPathReadiness() error = %v", err)
		}
		if !strings.Contains(readiness, "file exists") {
			t.Fatalf("readiness = %q", readiness)
		}
		return
	}
	if err == nil || !strings.Contains(err.Error(), "not executable") {
		t.Fatalf("error = %v, want not executable", err)
	}

	if err := os.Chmod(command, 0o700); err != nil {
		t.Fatalf("chmod command: %v", err)
	}
	readiness, err = searchCommandPathReadiness(command)
	if err != nil {
		t.Fatalf("searchCommandPathReadiness() error = %v", err)
	}
	if !strings.Contains(readiness, "executable") {
		t.Fatalf("readiness = %q", readiness)
	}
}

func TestSearchCommandPathReadinessRejectsDirectory(t *testing.T) {
	_, err := searchCommandPathReadiness(t.TempDir())
	if err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("error = %v, want directory", err)
	}
}

func TestAuthCheckUsesConfiguredKeyringStore(t *testing.T) {
	clearDoctorAuthEnv(t)
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`cli_auth_credentials_store = "keyring"`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if err := auth.NewStoreWithOptions(home, auth.StoreOptionsFromConfig("keyring", false)).Save(auth.FromAPIKey("sk-keyring")); err != nil {
		t.Fatalf("save keyring auth: %v", err)
	}

	check := authCheck(home, &Options{})
	if check.Status != CheckStatusOK {
		t.Fatalf("status = %s details=%v", check.Status, check.Details)
	}
	for _, want := range []string{
		"auth storage mode: Keyring",
		"auth file: " + filepath.Join(home, "auth.json"),
		"stored auth mode: api_key",
		"stored API key: true",
	} {
		if !containsDetail(check, want) {
			t.Fatalf("missing auth detail %q in %#v", want, check.Details)
		}
	}
}

func TestAuthCheckNoCredentialsFailsLikeRust(t *testing.T) {
	clearDoctorAuthEnv(t)
	check := authCheck(t.TempDir(), &Options{})
	if check.Status != CheckStatusFail || check.Summary != "no Codex credentials were found" {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "auth storage mode: File") || !containsDetail(check, "auth file:") {
		t.Fatalf("details = %#v", check.Details)
	}
	if check.Remediation == nil || *check.Remediation != "Run codex login or provide an API key through a supported auth env var." {
		t.Fatalf("remediation = %v", check.Remediation)
	}
}

func TestAuthCheckReportsStoredAuthIssuesLikeRust(t *testing.T) {
	clearDoctorAuthEnv(t)
	home := t.TempDir()
	if err := auth.NewStore(home).Save(auth.AuthDotJSON{}); err != nil {
		t.Fatalf("save auth: %v", err)
	}

	check := authCheck(home, &Options{})
	if check.Status != CheckStatusFail || check.Summary != "stored credentials are incomplete" {
		t.Fatalf("check = %+v", check)
	}
	for _, want := range []string{
		"stored auth mode: chatgpt",
		"stored auth issue: ChatGPT auth is missing token data",
		"stored auth issue: ChatGPT auth is missing refresh metadata",
	} {
		if !containsDetail(check, want) {
			t.Fatalf("missing detail %q in %#v", want, check.Details)
		}
	}
	if check.Remediation == nil || *check.Remediation != "Run codex login again or provide a supported auth env var." {
		t.Fatalf("remediation = %v", check.Remediation)
	}

	t.Setenv(auth.OpenAIAPIKeyEnv, "sk-env")
	warn := authCheck(home, &Options{})
	if warn.Status != CheckStatusWarning || warn.Summary != "auth is provided by environment, but stored credentials are incomplete" {
		t.Fatalf("warn = %+v", warn)
	}
}

func TestStoredAuthIssuesMatchRustModes(t *testing.T) {
	apiKey := &auth.AuthDotJSON{AuthMode: "apikey"}
	if got := storedAuthModeForDoctor(apiKey); got != "api_key" {
		t.Fatalf("api key mode = %q", got)
	}
	if got := storedAuthIssuesForDoctor(apiKey, func(string) bool { return false }); !sameStringSlice(got, []string{"API key auth is missing an API key"}) {
		t.Fatalf("api key issues = %#v", got)
	}
	if got := storedAuthIssuesForDoctor(apiKey, func(key string) bool { return key == auth.OpenAIAPIKeyEnv }); len(got) != 0 {
		t.Fatalf("api key issues with env = %#v", got)
	}

	external := &auth.AuthDotJSON{
		AuthMode: "chatgptAuthTokens",
		Tokens:   map[string]any{"access_token": "external-token"},
	}
	if got := storedAuthIssuesForDoctor(external, nil); !sameStringSlice(got, []string{
		"external ChatGPT auth is missing a ChatGPT account id",
		"external ChatGPT auth is missing refresh metadata",
	}) {
		t.Fatalf("external issues = %#v", got)
	}

	agentIdentity := &auth.AuthDotJSON{AuthMode: "agent-identity", AgentIdentity: map[string]any{"account_id": "acct"}}
	if got := storedAuthIssuesForDoctor(agentIdentity, nil); !sameStringSlice(got, []string{"agent identity auth is missing an agent identity token"}) {
		t.Fatalf("agent identity issues = %#v", got)
	}
	agentIdentity.AgentIdentity = map[string]any{"agent_runtime_id": "runtime", "agent_private_key": "private"}
	if got := storedAuthIssuesForDoctor(agentIdentity, nil); len(got) != 0 {
		t.Fatalf("agent identity issues with material = %#v", got)
	}

	pat := &auth.AuthDotJSON{AuthMode: "personal-access-token"}
	if got := storedAuthIssuesForDoctor(pat, nil); !sameStringSlice(got, []string{"personal access token auth is missing a personal access token"}) {
		t.Fatalf("pat issues = %#v", got)
	}
	bedrock := &auth.AuthDotJSON{AuthMode: "bedrock-api-key"}
	if got := storedAuthIssuesForDoctor(bedrock, nil); !sameStringSlice(got, []string{"Bedrock API key auth is missing a Bedrock API key"}) {
		t.Fatalf("bedrock issues = %#v", got)
	}
}

func TestAuthCheckProviderEnvKeyLikeRust(t *testing.T) {
	clearDoctorAuthEnv(t)
	home := t.TempDir()
	body := `
model_provider = "custom"

[model_providers.custom]
name = "Custom"
base_url = "https://example.test/v1"
env_key = "CUSTOM_API_KEY"
env_key_instructions = "Set CUSTOM_API_KEY before running Codex."
wire_api = "responses"
`
	if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	t.Setenv("CUSTOM_API_KEY", "")
	missing := authCheck(home, &Options{})
	if missing.Status != CheckStatusFail || missing.Summary != "active model provider auth env var is missing" {
		t.Fatalf("missing = %+v", missing)
	}
	if !containsDetail(missing, "model provider requires OpenAI auth: false") ||
		!containsDetail(missing, "provider auth env var: CUSTOM_API_KEY (missing)") {
		t.Fatalf("missing details = %#v", missing.Details)
	}
	if missing.Remediation == nil || *missing.Remediation != "Set CUSTOM_API_KEY before running Codex." {
		t.Fatalf("missing remediation = %v", missing.Remediation)
	}

	t.Setenv("CUSTOM_API_KEY", "custom-key")
	present := authCheck(home, &Options{})
	if present.Status != CheckStatusOK || present.Summary != "auth is provided by the active model provider" {
		t.Fatalf("present = %+v", present)
	}
	if !containsDetail(present, "provider auth env var: CUSTOM_API_KEY (present)") {
		t.Fatalf("present details = %#v", present.Details)
	}
}

func TestNetworkCheckAggregatesProxyEnvVarsLikeRust(t *testing.T) {
	clearDoctorProxyEnv(t)
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:8080")
	t.Setenv("NO_PROXY", "localhost")

	check := networkCheck(t.TempDir(), &Options{})
	if check.Status != CheckStatusOK || check.Summary != "network-related environment looks readable" {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "proxy env vars present: HTTP_PROXY, NO_PROXY") {
		t.Fatalf("details = %#v", check.Details)
	}
	if detailHasPrefix(check, "HTTP_PROXY:") || detailHasPrefix(check, "network_proxy.") {
		t.Fatalf("Go-only network details should not be emitted: %#v", check.Details)
	}
}

func TestNetworkCheckReportsCustomCAEnvFiles(t *testing.T) {
	clearDoctorProxyEnv(t)
	home := t.TempDir()
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, []byte("cert"), 0o600); err != nil {
		t.Fatalf("write ca file: %v", err)
	}
	t.Setenv("CODEX_CA_CERTIFICATE", caPath)

	check := networkCheck(home, &Options{})
	if check.Status != CheckStatusOK {
		t.Fatalf("status = %s details=%v", check.Status, check.Details)
	}
	if !containsDetail(check, "CODEX_CA_CERTIFICATE: readable file "+caPath) {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestNetworkCheckWarnsWhenCustomCAEnvIsNotFile(t *testing.T) {
	clearDoctorProxyEnv(t)
	home := t.TempDir()
	dir := t.TempDir()
	t.Setenv("SSL_CERT_FILE", dir)

	check := networkCheck(home, &Options{})
	if check.Status != CheckStatusWarning {
		t.Fatalf("status = %s details=%v", check.Status, check.Details)
	}
	if check.Summary != "custom CA env var does not point at a file" || !containsDetail(check, "SSL_CERT_FILE: not a file "+dir) {
		t.Fatalf("check = %+v", check)
	}
}

func TestNetworkCheckReportsProxyPolicyDetailsLikeRust(t *testing.T) {
	clearDoctorProxyEnv(t)
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`
[features]
respect_system_proxy = true

[permissions.network]
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	check := networkCheck(home, &Options{})
	if check.Status != CheckStatusOK {
		t.Fatalf("status = %s details=%v", check.Status, check.Details)
	}
	if !containsDetail(check, "respect system proxy: enabled") {
		t.Fatalf("missing respect system proxy detail: %#v", check.Details)
	}
	if !containsDetail(check, "managed proxy: configured") {
		t.Fatalf("missing managed proxy detail: %#v", check.Details)
	}

	plainHome := t.TempDir()
	plain := networkCheck(plainHome, &Options{})
	if !containsDetail(plain, "respect system proxy: disabled") || !containsDetail(plain, "managed proxy: not configured") {
		t.Fatalf("default proxy policy details = %#v", plain.Details)
	}
}

func TestClassifyHTTPProbeErrorClassifiesFailuresLikeRust(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{name: "nil", err: nil, want: ""},
		{name: "tls certificate verification", err: &tls.CertificateVerificationError{}, want: "TLS handshake or certificate validation failed"},
		{name: "x509 unknown authority", err: x509.UnknownAuthorityError{}, want: "TLS handshake or certificate validation failed"},
		{name: "tls record header", err: &tls.RecordHeaderError{Msg: "first record does not look like a TLS handshake"}, want: "TLS handshake or certificate validation failed"},
		{name: "proxy http to https", err: errors.New(`Get "https://example.com": proxyconnect tcp: localhost:3128: server gave HTTP response to HTTPS client`), want: "TLS handshake or certificate validation failed"},
		{name: "deadline exceeded", err: context.DeadlineExceeded, want: "request timed out"},
		{name: "url timeout", err: &url.Error{Op: "Get", URL: "https://example.com", Err: context.DeadlineExceeded}, want: "request timed out"},
		{name: "proxy auth 407", err: errors.New("proxyconnect tcp: proxy.example:3128: 407 Proxy Authentication Required"), want: "proxy authentication required"},
		{name: "invalid proxy url", err: errors.New(`Get "https://example.com": http: invalid proxy URL`), want: "invalid proxy configuration"},
		{name: "malformed proxy", err: errors.New("proxyconnect tcp: malformed proxy address"), want: "invalid proxy configuration"},
		{name: "unsupported proxy scheme", err: errors.New("proxyconnect tcp: unsupported protocol scheme \"socks5\""), want: "unsupported proxy configuration"},
		{name: "proxy resolution", err: errors.New("proxyconnect tcp: dial tcp: lookup proxy.example: no such host"), want: "proxy resolution failed"},
		{name: "connect refused", err: errors.New(`Get "http://127.0.0.1:9": dial tcp 127.0.0.1:9: connect: connection refused`), want: "connect failed"},
		{name: "generic", err: errors.New("boom"), want: "request failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyHTTPProbeError(tc.err)
			gotMessage := ""
			if got != nil {
				gotMessage = got.Error()
			}
			if gotMessage != tc.want {
				t.Fatalf("classifyHTTPProbeError(%v) = %q, want %q", tc.err, gotMessage, tc.want)
			}
		})
	}
}

func TestRouteAwareProbeHTTPClientHonorsSystemProxyPolicy(t *testing.T) {
	clearDoctorProxyEnv(t)
	disabled := routeAwareProbeHTTPClient(&config.Config{Values: map[string]any{}})
	transport, ok := disabled.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport = %T", disabled.Transport)
	}
	if transport.Proxy != nil {
		t.Fatal("Proxy set, want nil when respect_system_proxy is disabled")
	}

	enabled := routeAwareProbeHTTPClient(&config.Config{Values: map[string]any{"features": map[string]any{"respect_system_proxy": true}}})
	transport, ok = enabled.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport = %T", enabled.Transport)
	}
	if transport.Proxy == nil {
		t.Fatal("Proxy nil, want ProxyFromEnvironment when respect_system_proxy is enabled")
	}
}

func TestRouteAwareProbeHTTPClientExtendsRootCAsAndFallsBack(t *testing.T) {
	clearDoctorProxyEnv(t)
	certPEM := testSelfSignedCACertPEM(t)
	caPath := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(caPath, certPEM, 0o600); err != nil {
		t.Fatalf("write ca file: %v", err)
	}
	t.Setenv("CODEX_CA_CERTIFICATE", caPath)
	client := routeAwareProbeHTTPClient(&config.Config{Values: map[string]any{}})
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil || transport.TLSClientConfig.RootCAs == nil {
		t.Fatalf("route-aware client missing root pool: transport=%T tls=%#v", client.Transport, transport)
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		t.Fatal("generated CA PEM did not decode")
	}
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse generated CA: %v", err)
	}
	if _, err := ca.Verify(x509.VerifyOptions{Roots: transport.TLSClientConfig.RootCAs}); err != nil {
		t.Fatalf("custom CA not trusted by probe root pool: %v", err)
	}

	// An invalid custom CA bundle must not replace the system roots.
	t.Setenv("CODEX_CA_CERTIFICATE", "")
	badPath := filepath.Join(t.TempDir(), "bad.pem")
	if err := os.WriteFile(badPath, []byte("not a certificate"), 0o600); err != nil {
		t.Fatalf("write bad ca file: %v", err)
	}
	t.Setenv("SSL_CERT_FILE", badPath)
	fallback := routeAwareProbeHTTPClient(&config.Config{Values: map[string]any{}})
	transport, ok = fallback.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil || transport.TLSClientConfig.RootCAs == nil {
		t.Fatalf("invalid custom CA must fall back to a usable system root pool")
	}
}

func testSelfSignedCACertPEM(t *testing.T) []byte {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate rsa key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "codex doctor test CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestTerminalCheckFailsForDumbTerminal(t *testing.T) {
	check := terminalCheck(map[string]string{"TERM": "dumb"}, &Options{})
	if check.Status != CheckStatusFail || check.Summary != "TERM=dumb - colors and cursor control are disabled" {
		t.Fatalf("check = %+v", check)
	}
	for _, want := range []string{
		"terminal size: unavailable (not detected)",
		"color output: disabled (TERM=dumb)",
	} {
		if !containsDetail(check, want) {
			t.Fatalf("missing detail %q in %#v", want, check.Details)
		}
	}
	for _, prefix := range []string{"stdin is terminal:", "stdout is terminal:", "stderr is terminal:"} {
		if !detailHasPrefix(check, prefix) {
			t.Fatalf("missing detail prefix %q in %#v", prefix, check.Details)
		}
	}
	if len(check.Issues) != 1 || check.Issues[0].Remedy == nil || *check.Issues[0].Remedy != "set TERM to a real value, for example xterm-256color" {
		t.Fatalf("issues = %#v", check.Issues)
	}
}

func TestTerminalCheckReportsRustShapeDetails(t *testing.T) {
	check := terminalCheck(map[string]string{
		"TERM":        "xterm-256color",
		"COLUMNS":     "100",
		"LINES":       "40",
		"NO_COLOR":    "",
		"FORCE_COLOR": "1",
		"SSH_TTY":     "",
	}, &Options{})
	if check.Status != CheckStatusOK {
		t.Fatalf("check = %+v", check)
	}
	for _, want := range []string{
		"COLUMNS: 100",
		"LINES: 40",
		"color output: disabled (NO_COLOR)",
		"NO_COLOR: present",
		"FORCE_COLOR: 1",
		"SSH_TTY: present",
	} {
		if !containsDetail(check, want) {
			t.Fatalf("missing detail %q in %#v", want, check.Details)
		}
	}
}

func TestTerminalCheckWarnsForNarrowColumnsAndLocale(t *testing.T) {
	check := terminalCheck(map[string]string{"TERM": "xterm-256color", "COLUMNS": "60", "LANG": "C"}, &Options{})
	if check.Status != CheckStatusWarning {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "COLUMNS: 60") || !containsDetail(check, "effective locale: C") {
		t.Fatalf("details = %#v", check.Details)
	}
	if len(check.Issues) != 2 {
		t.Fatalf("issues = %#v", check.Issues)
	}
}

func TestTerminalCheckWarnsForNarrowRowsLikeRust(t *testing.T) {
	check := terminalCheck(map[string]string{"TERM": "xterm-256color", "LINES": "20"}, &Options{})
	if check.Status != CheckStatusWarning || check.Summary != "LINES=20 - content may scroll off (recommended >=24)" {
		t.Fatalf("check = %+v", check)
	}
	if len(check.Issues) != 1 || check.Issues[0].Expected == nil || *check.Issues[0].Expected != ">= 24 rows" {
		t.Fatalf("issues = %#v", check.Issues)
	}
}

func TestTerminalCheckRedactsRemoteIndicatorsAndWarnsOnTerminfo(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing-terminfo")
	check := terminalCheck(map[string]string{
		"TERM":           "xterm-256color",
		"SSH_CONNECTION": "10.0.0.1 1 10.0.0.2 22",
		"TERMINFO":       missing,
	}, &Options{})
	if check.Status != CheckStatusFail {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "SSH_CONNECTION: present") || containsDetail(check, "10.0.0.1") || !containsDetail(check, "TERMINFO: "+missing+" (missing)") {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestTerminalCheckReportsDetectedSizeIssuesLikeRust(t *testing.T) {
	term := "xterm-256color"
	check := terminalCheckFromInputs(&terminalCheckInputs{
		Info:                &shell.TerminalInfo{Name: shell.TerminalUnknown, Term: &term},
		Env:                 map[string]string{"TERM": term},
		StdoutIsTerminal:    true,
		StreamSupportsColor: true,
		TerminalSize:        terminalSizeProbe{Columns: 79, Rows: 20},
	})
	if check.Status != CheckStatusWarning || check.Summary != "width 79 cols - output may wrap (recommended >=80)" {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "terminal size: 79x20") {
		t.Fatalf("details = %#v", check.Details)
	}
	if len(check.Issues) != 2 || check.Issues[0].Fields[0] != "terminal size" || check.Issues[1].Fields[0] != "terminal size" {
		t.Fatalf("issues = %#v", check.Issues)
	}
}

func TestTerminalCheckReportsColorSupportReasonLikeRust(t *testing.T) {
	term := "unknown-terminal"
	check := terminalCheckFromInputs(&terminalCheckInputs{
		Info:                &shell.TerminalInfo{Name: shell.TerminalUnknown, Term: &term},
		Env:                 map[string]string{"TERM": term},
		StdoutIsTerminal:    true,
		TerminalSize:        terminalSizeProbe{Err: "not detected"},
		StreamSupportsColor: false,
	})
	if check.Status != CheckStatusOK {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "color output: disabled (terminal color support not detected)") {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestTerminalCheckIncludesTmuxAndWindowsDetailsLikeRust(t *testing.T) {
	term := "xterm-256color"
	check := terminalCheckFromInputs(&terminalCheckInputs{
		Info: &shell.TerminalInfo{
			Name:        shell.TerminalUnknown,
			Term:        &term,
			Multiplexer: &shell.Multiplexer{Name: shell.MultiplexerTmux},
		},
		Env:                 map[string]string{"TERM": term},
		StdoutIsTerminal:    true,
		StreamSupportsColor: true,
		TerminalSize:        terminalSizeProbe{Columns: 100, Rows: 40},
		TmuxDetails: []string{
			"tmux client termtype: xterm-256color",
			"tmux extended-keys: unavailable",
		},
		WindowsConsoleDetails: []string{
			"console input code page: 65001",
			"stdout console mode: 0x00000004 (VT processing: true)",
		},
	})
	if check.Status != CheckStatusOK {
		t.Fatalf("check = %+v", check)
	}
	for _, want := range []string{
		"multiplexer: tmux",
		"tmux client termtype: xterm-256color",
		"tmux extended-keys: unavailable",
		"console input code page: 65001",
		"stdout console mode: 0x00000004 (VT processing: true)",
	} {
		if !containsDetail(check, want) {
			t.Fatalf("missing detail %q in %#v", want, check.Details)
		}
	}
}

func TestGitCheckWarnsWhenRepoHasNoGitExecutable(t *testing.T) {
	check := gitCheckFromInputs(&gitCheckInputs{
		RepoRoot: "/repo",
	})
	if check.Status != CheckStatusWarning || check.Summary != "Git repository detected but git executable was not found" {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "selected git: not found") || !containsDetail(check, "repo detected: true") {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestGitCheckWarnsWhenSelectedGitCannotReportVersion(t *testing.T) {
	check := gitCheckFromInputs(&gitCheckInputs{
		SelectedGit: "/usr/bin/git",
		RepoRoot:    "/repo",
	})
	if check.Status != CheckStatusWarning || check.Summary != "Git executable found but could not be run" {
		t.Fatalf("check = %+v", check)
	}
	if len(check.Issues) != 1 || check.Issues[0].Expected == nil || *check.Issues[0].Expected != "git --version succeeds" {
		t.Fatalf("issues = %#v", check.Issues)
	}
}

func TestGitCheckReportsCandidatesAndRepoMetadata(t *testing.T) {
	check := gitCheckFromInputs(&gitCheckInputs{
		SelectedGit:     "/usr/bin/git",
		GitCandidates:   []string{"/usr/bin/git", "/opt/bin/git"},
		GitVersion:      "git version 2.54.0",
		GitExecPath:     "/usr/libexec/git-core",
		GitBuildOptions: "cpu: x86_64",
		RepoRoot:        "/repo",
		GitEntry:        "directory",
		Branch:          "main",
		CoreFSMonitor:   "false",
	})
	if check.Status != CheckStatusOK {
		t.Fatalf("check = %+v", check)
	}
	for _, want := range []string{
		"PATH git entries: 2",
		"PATH git #1: /usr/bin/git",
		"git version: git version 2.54.0",
		"repo detected: true",
		"git branch: main",
		"core.fsmonitor: false",
	} {
		if !containsDetail(check, want) {
			t.Fatalf("missing detail %q in %#v", want, check.Details)
		}
	}
}

func TestGitCheckNormalizesDetachedHead(t *testing.T) {
	check := gitCheckFromInputs(&gitCheckInputs{
		SelectedGit: "/usr/bin/git",
		GitVersion:  "git version 2.54.0",
		Branch:      "HEAD",
	})
	if !containsDetail(check, "git branch: detached HEAD") {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestParseGitVersionForWindows(t *testing.T) {
	parsed, ok := parseGitVersion("git version 2.34.1.windows.1")
	if !ok || parsed.Major != 2 || parsed.Minor != 34 || parsed.Patch != 1 {
		t.Fatalf("parsed = %+v ok=%t", parsed, ok)
	}
	parsed, ok = parseGitVersion("git version 2.54.0.windows.1")
	if !ok || parsed.Major != 2 || parsed.Minor != 54 || parsed.Patch != 0 {
		t.Fatalf("parsed = %+v ok=%t", parsed, ok)
	}
}

func TestOldWindowsGitWarning(t *testing.T) {
	if got := oldWindowsGitWarning("git version 2.34.1.windows.1", true); got != "old Git for Windows may corrupt Windows TUI rendering" {
		t.Fatalf("warning = %q", got)
	}
	if got := oldWindowsGitWarning("git version 2.54.0.windows.1", true); got != "" {
		t.Fatalf("warning = %q", got)
	}
	if got := oldWindowsGitWarning("git version 2.34.1.windows.1", false); got != "" {
		t.Fatalf("warning = %q", got)
	}
}

func TestTerminalTitleReportsDefaultItemsAndGitProjectName(t *testing.T) {
	check := terminalTitleCheckFromInputs(&terminalTitleInputs{
		CWD: "/repo/subdir",
		ProjectRoot: &projectTitleRoot{
			Source: "git repo root",
			Path:   "/repo",
		},
	})
	if check.Summary != "terminal title default" {
		t.Fatalf("check = %+v", check)
	}
	for _, want := range []string{
		"terminal title items: activity, project-name",
		"terminal title project source: git repo root",
		"terminal title project value: repo",
	} {
		if !containsDetail(check, want) {
			t.Fatalf("missing detail %q in %#v", want, check.Details)
		}
	}
}

func TestTerminalTitleReportsDisabledConfiguration(t *testing.T) {
	items := []string{}
	check := terminalTitleCheckFromInputs(&terminalTitleInputs{
		ConfiguredItems: &items,
		CWD:             "/workspace",
	})
	if check.Summary != "terminal title disabled" {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "terminal title items: none") || !containsDetail(check, "terminal title activity: false") {
		t.Fatalf("details = %#v", check.Details)
	}
	if containsDetail(check, "terminal title project source:") {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestTerminalTitleReportsProjectConfigFallback(t *testing.T) {
	items := []string{"project"}
	check := terminalTitleCheckFromInputs(&terminalTitleInputs{
		ConfiguredItems: &items,
		CWD:             "/workspace/project/subdir",
		ProjectRoot: &projectTitleRoot{
			Source: "project config",
			Path:   "/workspace/project",
		},
	})
	if check.Summary != "terminal title configured" {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "terminal title project source: project config") || !containsDetail(check, "terminal title project value: project") {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestTerminalTitleCheckUsesProjectLayerWithoutConfigLikeRust(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(project, ".codex"), 0o755); err != nil {
		t.Fatalf("mkdir .codex: %v", err)
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte("model = \"gpt-user\"\n"+trustedProjectConfigForDoctor(project)+"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	check := terminalTitleCheck(home, &Options{Root: cli.RootOptions{Shared: cli.SharedOptions{CWD: project}}})
	if check.Status != CheckStatusOK || check.Summary != "terminal title default" {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "terminal title project source: project config") || !containsDetail(check, "terminal title project value: project") {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestTerminalTitleCheckSkipsUntrustedProjectLayerLikeRust(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "project")
	if err := os.MkdirAll(filepath.Join(project, ".codex"), 0o755); err != nil {
		t.Fatalf("mkdir .codex: %v", err)
	}
	if err := os.WriteFile(config.ConfigPath(home), []byte("model = \"gpt-user\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	check := terminalTitleCheck(home, &Options{Root: cli.RootOptions{Shared: cli.SharedOptions{CWD: project}}})
	if check.Status != CheckStatusOK {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "terminal title project source: cwd") || containsDetail(check, "terminal title project source: project config") {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestTerminalTitleWarnsForInvalidConfiguredItems(t *testing.T) {
	items := []string{"project", "bogus", "activity", "bogus"}
	check := terminalTitleCheckFromInputs(&terminalTitleInputs{
		ConfiguredItems: &items,
		CWD:             "/workspace/project",
		ProjectRoot: &projectTitleRoot{
			Source: "project config",
			Path:   "/workspace/project",
		},
	})
	if check.Status != CheckStatusWarning || check.Summary != "terminal title configured with invalid items" {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "terminal title items: project-name, activity") || !containsDetail(check, `terminal title invalid items: "bogus"`) {
		t.Fatalf("details = %#v", check.Details)
	}
	if len(check.Issues) != 1 {
		t.Fatalf("issues = %#v", check.Issues)
	}
}

func TestTerminalTitleProjectValueUsesTUITruncationShape(t *testing.T) {
	items := []string{"project"}
	check := terminalTitleCheckFromInputs(&terminalTitleInputs{
		ConfiguredItems: &items,
		CWD:             "/workspace/abcdefghijklmnopqrstuvwxyz",
		ProjectRoot: &projectTitleRoot{
			Source: "project config",
			Path:   "/workspace/abcdefghijklmnopqrstuvwxyz",
		},
	})
	if !containsDetail(check, "terminal title project value: abcdefghijklmnopqrstu...") {
		t.Fatalf("details = %#v", check.Details)
	}
}

func trustedProjectConfigForDoctor(path string) string {
	key := strings.ReplaceAll(filepath.Clean(path), `\`, `\\`)
	return "\n[projects.\"" + key + "\"]\ntrust_level = \"trusted\"\n"
}

func TestUpdatesCheckReportsCacheAndProbeWarning(t *testing.T) {
	home := t.TempDir()
	unsetEnvForDoctor(t, "CODEX_MANAGED_BY_NPM")
	unsetEnvForDoctor(t, "CODEX_MANAGED_BY_BUN")
	unsetEnvForDoctor(t, "CODEX_MANAGED_PACKAGE_ROOT")
	if err := os.WriteFile(filepath.Join(home, versionCacheFileName), []byte(`{
		"latest_version": "1.2.3",
		"last_checked_at": "2026-06-30T00:00:00Z",
		"dismissed_version": "1.2.2"
	}`), 0o600); err != nil {
		t.Fatalf("write version cache: %v", err)
	}
	builder := NewBuilder()
	builder.httpClient = localOnlyHTTPClient(t)
	check := builder.updatesCheck(home, &Options{})
	if check.Status != CheckStatusWarning {
		t.Fatalf("check = %+v", check)
	}
	for _, want := range []string{
		"check for update on startup: true",
		"update action: manual or unknown",
		"cached latest version: 1.2.3",
		"last checked at: 2026-06-30T00:00:00Z",
		"dismissed version: 1.2.2",
		"latest version probe:",
	} {
		if !containsDetail(check, want) {
			t.Fatalf("missing detail %q in %#v", want, check.Details)
		}
	}
}

func TestUpdatesCheckReportsNPMRootMatchLikeRust(t *testing.T) {
	home := t.TempDir()
	npmRoot := filepath.Join(home, "npm")
	packageRoot := filepath.Join(npmRoot, "@jacks001314", "codex-go")
	t.Setenv("CODEX_MANAGED_BY_NPM", "1")
	unsetEnvForDoctor(t, "CODEX_MANAGED_BY_BUN")
	t.Setenv("CODEX_MANAGED_PACKAGE_ROOT", packageRoot)
	withNPMRootCommandForDoctor(t, func() (string, error) {
		return npmRoot + "\n", nil
	})
	builder := NewBuilder()
	builder.currentExe = func() (string, error) {
		return filepath.Join(home, "bin", "codex"), nil
	}
	builder.httpClient = localOnlyHTTPClient(t)

	check := builder.updatesCheck(home, &Options{})
	if check.Status != CheckStatusWarning || check.Summary != "update configuration is locally consistent" {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "npm update target: "+packageRoot) {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestUpdatesCheckReportsNPMRootMismatchLikeRust(t *testing.T) {
	home := t.TempDir()
	runningRoot := filepath.Join(home, "running", "@jacks001314", "codex-go")
	npmRoot := filepath.Join(home, "npm")
	npmPackageRoot := filepath.Join(npmRoot, "@jacks001314", "codex-go")
	t.Setenv("CODEX_MANAGED_BY_NPM", "1")
	unsetEnvForDoctor(t, "CODEX_MANAGED_BY_BUN")
	t.Setenv("CODEX_MANAGED_PACKAGE_ROOT", runningRoot)
	withNPMRootCommandForDoctor(t, func() (string, error) {
		return npmRoot + "\n", nil
	})
	builder := NewBuilder()
	builder.currentExe = func() (string, error) {
		return filepath.Join(home, "bin", "codex"), nil
	}
	builder.httpClient = localOnlyHTTPClient(t)

	check := builder.updatesCheck(home, &Options{})
	if check.Status != CheckStatusFail || check.Summary != "update would target a different npm install" {
		t.Fatalf("check = %+v", check)
	}
	for _, want := range []string{
		"running package root: " + runningRoot,
		"npm package root: " + npmPackageRoot,
		"latest version probe:",
	} {
		if !containsDetail(check, want) {
			t.Fatalf("missing detail %q in %#v", want, check.Details)
		}
	}
	if check.Remediation == nil || !strings.Contains(*check.Remediation, "Fix PATH or npm prefix") || !strings.Contains(*check.Remediation, runningRoot) || !strings.Contains(*check.Remediation, npmPackageRoot) {
		t.Fatalf("remediation = %#v", check.Remediation)
	}
}

func TestUpdatesCheckReportsMissingNPMPackageRootLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_MANAGED_BY_NPM", "1")
	unsetEnvForDoctor(t, "CODEX_MANAGED_BY_BUN")
	unsetEnvForDoctor(t, "CODEX_MANAGED_PACKAGE_ROOT")
	withNPMRootCommandForDoctor(t, func() (string, error) {
		t.Fatal("npm root should not be called without CODEX_MANAGED_PACKAGE_ROOT")
		return "", nil
	})
	builder := NewBuilder()
	builder.currentExe = func() (string, error) {
		return filepath.Join(home, "bin", "codex"), nil
	}
	builder.httpClient = localOnlyHTTPClient(t)

	check := builder.updatesCheck(home, &Options{})
	if check.Status != CheckStatusWarning || check.Summary != "npm update target could not be proven" {
		t.Fatalf("check = %+v", check)
	}
	if check.Remediation == nil || *check.Remediation != "Reinstall or update Codex so the JS shim provides CODEX_MANAGED_PACKAGE_ROOT." {
		t.Fatalf("remediation = %#v", check.Remediation)
	}
}

func TestUpdatesCheckIgnoresInheritedNPMEnvForCargoBinaryLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_MANAGED_BY_NPM", "1")
	unsetEnvForDoctor(t, "CODEX_MANAGED_BY_BUN")
	t.Setenv("CODEX_MANAGED_PACKAGE_ROOT", filepath.Join(home, "npm", "@jacks001314", "codex-go"))
	withNPMRootCommandForDoctor(t, func() (string, error) {
		t.Fatal("npm root should not be called for target/debug binary")
		return "", nil
	})
	builder := NewBuilder()
	builder.currentExe = func() (string, error) {
		return filepath.Join(home, "target", "debug", "codex"), nil
	}
	builder.httpClient = localOnlyHTTPClient(t)

	check := builder.updatesCheck(home, &Options{})
	if containsDetail(check, "npm update target:") || containsDetail(check, "running package root:") {
		t.Fatalf("npm details should be skipped for inherited cargo env: %#v", check.Details)
	}
	if !containsDetail(check, "update action: manual or unknown") {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestConfigCheckReportsRustFeatureDetailLabels(t *testing.T) {
	home := t.TempDir()
	check := configCheck(home, &Options{
		Root: cli.RootOptions{
			EnableFeatures:  []string{"memories"},
			DisableFeatures: []string{"shell_tool"},
		},
	})
	if check.ID != "config.load" {
		t.Fatalf("check = %+v", check)
	}
	for _, want := range []string{
		"CODEX_HOME: " + home,
		"model: <default>",
		"model provider: openai",
		"mcp servers: 0",
		"config.toml: " + config.ConfigPath(home),
		"config.toml: missing",
	} {
		if !containsDetail(check, want) {
			t.Fatalf("missing Rust config detail %q in %#v", want, check.Details)
		}
	}
	for _, want := range []string{
		"feature flags enabled:",
		"enabled feature flags:",
		"feature flag overrides:",
	} {
		if !containsDetail(check, want) {
			t.Fatalf("missing Rust feature detail %q in %#v", want, check.Details)
		}
	}
	if detailHasPrefix(check, "default enabled:") {
		t.Fatalf("legacy feature detail should not be emitted: %#v", check.Details)
	}
	for _, stale := range []string{"config path:", "top-level keys:", "CLI overrides:"} {
		if detailHasPrefix(check, stale) {
			t.Fatalf("Go-only config detail %q should not be emitted: %#v", stale, check.Details)
		}
	}
	if !containsDetail(check, "feature flag overrides: memories=true, shell_tool=false") {
		t.Fatalf("feature overrides should be based on effective config: %#v", check.Details)
	}
}

func TestConfigCheckReportsLegacyFeatureUsage(t *testing.T) {
	home := t.TempDir()
	body := "[features]\ncodex_hooks = false\nweb_search_request = true\nuse_legacy_landlock = true\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	check := configCheck(home, &Options{})
	for _, want := range []string{
		"legacy feature flag: codex_hooks -> hooks",
		"legacy feature flag: features.use_legacy_landlock -> use_legacy_landlock",
		"legacy feature flag: features.web_search_request -> web_search_request",
	} {
		if !containsDetail(check, want) {
			t.Fatalf("missing legacy usage %q in %#v", want, check.Details)
		}
	}
}

func TestUpdateActionLabelAndStatusText(t *testing.T) {
	if got := updateActionLabel(&install.InstallContext{Method: install.InstallMethod{Kind: install.InstallNPM}}); got != "npm install -g @jacks001314/codex-go@latest" {
		t.Fatalf("updateActionLabel npm = %q", got)
	}
	if got := updateActionLabel(&install.InstallContext{Method: install.InstallMethod{Kind: install.InstallOther}}); got != "manual or unknown" {
		t.Fatalf("updateActionLabel other = %q", got)
	}
	if got := latestVersionStatusText(install.UpdateStatusUpdateAvailable); got != "newer version is available" {
		t.Fatalf("latestVersionStatusText = %q", got)
	}
	if got := latestVersionStatusText(install.UpdateStatusUpToDate); got != "current version is not older" {
		t.Fatalf("latestVersionStatusText = %q", got)
	}
}

func TestBackgroundServerCheckNotRunningStaysOK(t *testing.T) {
	home := t.TempDir()
	check := backgroundServerCheck(home)
	if check.Status != CheckStatusOK || check.Summary != "background server is not running" {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "status: not running") || !containsDetail(check, "mode: ephemeral") {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestBackgroundServerCheckReportsPersistentSettings(t *testing.T) {
	home := t.TempDir()
	paths := appserverDaemonPathsForTest(home)
	if err := os.MkdirAll(filepath.Dir(paths.SettingsFile), 0o700); err != nil {
		t.Fatalf("mkdir settings dir: %v", err)
	}
	if err := os.WriteFile(paths.SettingsFile, []byte(`{"remoteControlEnabled":true}`), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	check := backgroundServerCheck(home)
	if !containsDetail(check, "settings: "+paths.SettingsFile+" (file)") || !containsDetail(check, "mode: persistent") {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestBackgroundServerCheckReportsRunningVersion(t *testing.T) {
	home := t.TempDir()
	paths := appserverDaemonPathsForTest(home)
	if err := os.MkdirAll(filepath.Dir(paths.SocketPath), 0o700); err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}
	if err := os.WriteFile(paths.SocketPath, []byte("placeholder"), 0o600); err != nil {
		t.Fatalf("write socket placeholder: %v", err)
	}
	oldProbe := probeBackgroundAppServerVersion
	probeBackgroundAppServerVersion = func(path string, timeout time.Duration) (string, error) {
		if path != paths.SocketPath {
			t.Fatalf("probe path = %q, want %q", path, paths.SocketPath)
		}
		if timeout != appserverdaemon.ControlSocketProbeTimeout {
			t.Fatalf("probe timeout = %v, want %v", timeout, appserverdaemon.ControlSocketProbeTimeout)
		}
		return "1.2.3", nil
	}
	t.Cleanup(func() { probeBackgroundAppServerVersion = oldProbe })

	check := backgroundServerCheck(home)
	if check.Status != CheckStatusOK || check.Summary != "background server is running" {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "status: running") || !containsDetail(check, "app-server version: 1.2.3") {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestBackgroundServerCheckWarnsForStaleSocketPlaceholder(t *testing.T) {
	home := t.TempDir()
	paths := appserverDaemonPathsForTest(home)
	if err := os.MkdirAll(filepath.Dir(paths.SocketPath), 0o700); err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}
	if err := os.WriteFile(paths.SocketPath, []byte("placeholder"), 0o600); err != nil {
		t.Fatalf("write socket placeholder: %v", err)
	}
	check := backgroundServerCheck(home)
	if check.Status != CheckStatusWarning || check.Summary != "background server socket is stale or unreachable" {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "status: stale or unreachable") {
		t.Fatalf("details = %#v", check.Details)
	}
	if !detailHasPrefix(check, "app-server version: unavailable (") {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestBackgroundProbeErrorIsConciseAndRedactsSocketPath(t *testing.T) {
	socketPath := filepath.Join(t.TempDir(), "codex.sock")
	err := errors.New("dial " + socketPath + " " + strings.Repeat("x", 200))
	got := conciseBackgroundProbeError(err, socketPath)
	if strings.Contains(got, socketPath) || !strings.Contains(got, "control socket") {
		t.Fatalf("concise error = %q", got)
	}
	if len([]rune(got)) > maxBackgroundProbeErrorChars+3 {
		t.Fatalf("concise error too long: %q", got)
	}
}

func TestMCPCheckReportsNoServers(t *testing.T) {
	check := mcpCheckFromServers(nil, nil, "")
	if check.Status != CheckStatusOK || check.Summary != "no MCP servers configured" {
		t.Fatalf("check = %+v", check)
	}
}

func withMCPHTTPProbe(t *testing.T, probe func(string) (string, error)) {
	t.Helper()
	previous := mcpHTTPProbeURL
	mcpHTTPProbeURL = probe
	t.Cleanup(func() {
		mcpHTTPProbeURL = previous
	})
}

func TestMCPCheckWarnsForOptionalMissingInputs(t *testing.T) {
	check := mcpCheckFromServers(map[string]mcp.ServerConfig{
		"fs": {Command: "", Enabled: true},
	}, map[string]string{}, t.TempDir())
	if check.Status != CheckStatusWarning || check.Summary != "MCP configuration has optional issues" {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "configured servers: 1") || !containsDetail(check, "fs: stdio command is empty") {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestMCPCheckFailsRequiredMissingInputs(t *testing.T) {
	withMCPHTTPProbe(t, func(string) (string, error) {
		return "HTTP 200", nil
	})
	check := mcpCheckFromServers(map[string]mcp.ServerConfig{
		"docs": {URL: "https://mcp.example.test/mcp", BearerTokenEnvVar: "MCP_TOKEN", Enabled: true, Required: true},
	}, map[string]string{}, t.TempDir())
	if check.Status != CheckStatusFail || check.Summary != "MCP configuration has failing required inputs or reachability" {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "docs: bearer token env var MCP_TOKEN is not set") {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestMCPCheckWarnsForOptionalHTTPReachabilityLikeRust(t *testing.T) {
	withMCPHTTPProbe(t, func(string) (string, error) {
		return "", errors.New("HEAD connect failed; GET connect failed")
	})
	check := mcpCheckFromServers(map[string]mcp.ServerConfig{
		"docs": {URL: "http://127.0.0.1:9/mcp", Enabled: true},
	}, map[string]string{}, t.TempDir())
	if check.Status != CheckStatusWarning || check.Summary != "MCP configuration has optional issues" {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "optional reachability failed: docs: http://127.0.0.1:9/mcp (HEAD connect failed; GET connect failed)") {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestMCPCheckFailsRequiredHTTPReachabilityLikeRust(t *testing.T) {
	withMCPHTTPProbe(t, func(string) (string, error) {
		return "", errors.New("HEAD connect failed; GET connect failed")
	})
	check := mcpCheckFromServers(map[string]mcp.ServerConfig{
		"docs": {URL: "http://127.0.0.1:9/mcp", Enabled: true, Required: true},
	}, map[string]string{}, t.TempDir())
	if check.Status != CheckStatusFail || check.Summary != "MCP configuration has failing required inputs or reachability" {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "required reachability failed: docs: http://127.0.0.1:9/mcp (HEAD connect failed; GET connect failed)") {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestMCPCheckReportsHeaderEnvVarsAndDisabledReasonLikeRust(t *testing.T) {
	probed := []string{}
	withMCPHTTPProbe(t, func(rawURL string) (string, error) {
		probed = append(probed, rawURL)
		return "HTTP 200", nil
	})
	check := mcpCheckFromServers(map[string]mcp.ServerConfig{
		"docs": {URL: "https://mcp.example.test/mcp", EnvHTTPHeaders: map[string]string{"X-Token": "DOCS_TOKEN"}, Enabled: true},
		"off":  {URL: "https://off.example.test/mcp", EnvHTTPHeaders: map[string]string{"X-Off": "OFF_TOKEN"}, Enabled: true, DisabledReason: "disabled by policy"},
	}, map[string]string{}, t.TempDir())
	if check.Status != CheckStatusWarning || check.Summary != "MCP configuration has optional issues" {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "disabled servers: 1") || !containsDetail(check, "streamable_http servers: 2") {
		t.Fatalf("details = %#v", check.Details)
	}
	if !containsDetail(check, "docs: header env var DOCS_TOKEN is not set") {
		t.Fatalf("details = %#v", check.Details)
	}
	for _, detail := range check.Details {
		if strings.Contains(detail, "OFF_TOKEN") {
			t.Fatalf("disabled server should not report header env var: %#v", check.Details)
		}
	}
	if len(probed) != 1 || probed[0] != "https://mcp.example.test/mcp" {
		t.Fatalf("probed = %#v", probed)
	}
}

func TestMCPCheckStdioEnvVarsAndRemoteCWDLikeRust(t *testing.T) {
	check := mcpCheckFromServers(map[string]mcp.ServerConfig{
		"local": {
			Command: "helper",
			Enabled: true,
			EnvVars: []mcp.EnvVar{
				{Name: "APP_TOKEN"},
				{Name: "REMOTE_TOKEN", Source: "remote"},
			},
		},
		"remote": {
			Command:       "remote-helper",
			Enabled:       true,
			Required:      true,
			EnvironmentID: "env-1",
			CWD:           "relative",
			EnvVars:       []mcp.EnvVar{{Name: "LOCAL_TOKEN"}},
		},
	}, map[string]string{}, t.TempDir())
	if check.Status != CheckStatusFail {
		t.Fatalf("check = %+v", check)
	}
	for _, want := range []string{
		"local: env var APP_TOKEN is not set",
		"local: env_vars entry `REMOTE_TOKEN` uses source `remote`, which requires remote MCP stdio",
		"remote: remote stdio cwd is not absolute (relative)",
		"remote: env var LOCAL_TOKEN is not set",
	} {
		if !containsDetail(check, want) {
			t.Fatalf("missing %q in %#v", want, check.Details)
		}
	}
}

func TestMCPRemoteCWDUsesInferredPathURIShapeLikeRust(t *testing.T) {
	for _, tt := range []struct {
		cwd  string
		want bool
	}{
		{cwd: `C:\workspace\server`, want: true},
		{cwd: "C:/workspace/server", want: true},
		{cwd: `\\server\share`, want: true},
		{cwd: "/workspace/server", want: true},
		{cwd: "//server/share", want: true},
		{cwd: "", want: false},
		{cwd: ".", want: false},
		{cwd: "relative", want: false},
		{cwd: `subdir\file`, want: false},
		{cwd: `C:file`, want: false},
		{cwd: `\rooted-without-drive`, want: false},
		{cwd: "file:///workspace/server", want: false},
	} {
		if got := mcpRemoteCWDIsAbsolute(tt.cwd); got != tt.want {
			t.Fatalf("mcpRemoteCWDIsAbsolute(%q) = %t, want %t", tt.cwd, got, tt.want)
		}
	}
}

func TestMCPCheckTreatsLocalEnvironmentIDAsLocalLikeRust(t *testing.T) {
	check := mcpCheckFromServers(map[string]mcp.ServerConfig{
		"fs": {
			Command:       "",
			Enabled:       true,
			EnvironmentID: "local",
			EnvVars:       []mcp.EnvVar{{Name: "REMOTE_TOKEN", Source: "remote"}},
		},
	}, map[string]string{}, t.TempDir())
	if check.Status != CheckStatusWarning {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "fs: env_vars entry `REMOTE_TOKEN` uses source `remote`, which requires remote MCP stdio") {
		t.Fatalf("details = %#v", check.Details)
	}
	for _, detail := range check.Details {
		if strings.Contains(detail, "remote stdio requires an explicit cwd") {
			t.Fatalf("local environment_id should not use remote cwd validation: %#v", check.Details)
		}
	}
}

func TestMCPCheckResolvesStdioCommandWithServerEnvPathLikeRust(t *testing.T) {
	dir := t.TempDir()
	command := "doctor-mcp-helper"
	if runtime.GOOS == "windows" {
		command += ".cmd"
	}
	if err := os.WriteFile(filepath.Join(dir, command), []byte(""), 0o700); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	check := mcpCheckFromServers(map[string]mcp.ServerConfig{
		"fs": {Command: strings.TrimSuffix(command, filepath.Ext(command)), Env: map[string]string{"PATH": dir}, Enabled: true},
	}, map[string]string{}, t.TempDir())
	if check.Status != CheckStatusOK {
		t.Fatalf("check = %+v", check)
	}
}

func TestMCPServersFromConfigParsesTable(t *testing.T) {
	cfg := &config.Config{Values: map[string]any{
		"mcp_servers": map[string]any{
			"docs": map[string]any{
				"url":                  "https://mcp.example.test/mcp",
				"bearer_token_env_var": "MCP_TOKEN",
				"env_http_headers":     map[string]any{"X-Token": "DOCS_TOKEN"},
				"disabled_reason":      "maintenance",
				"required":             true,
			},
			"fs": map[string]any{
				"command": "mcp-fs",
				"args":    []any{"--root", "."},
				"env":     map[string]any{"ROOT": "."},
				"env_vars": []any{
					"APP_TOKEN",
					map[string]any{"name": "REMOTE_TOKEN", "source": "remote"},
				},
				"cwd":     "/workspace",
				"enabled": false,
			},
		},
	}}
	servers := mcpServersFromConfig(cfg)
	if !servers["docs"].Required || servers["docs"].BearerTokenEnvVar != "MCP_TOKEN" || servers["docs"].DisabledReason != "maintenance" || servers["fs"].Enabled {
		t.Fatalf("servers = %#v", servers)
	}
	if servers["docs"].EnvHTTPHeaders["X-Token"] != "DOCS_TOKEN" {
		t.Fatalf("docs server = %#v", servers["docs"])
	}
	if len(servers["fs"].Args) != 2 || servers["fs"].Env["ROOT"] != "." {
		t.Fatalf("fs server = %#v", servers["fs"])
	}
	if servers["fs"].CWD != "/workspace" || len(servers["fs"].EnvVars) != 2 || servers["fs"].EnvVars[1].Source != "remote" {
		t.Fatalf("fs server = %#v", servers["fs"])
	}
}

func TestMCPServersFromConfigAppliesRequirementsLikeRust(t *testing.T) {
	cfg := &config.Config{Values: map[string]any{
		"mcp_servers": map[string]any{
			"matched": map[string]any{
				"command": "company-cli",
				"args":    []any{"mcp", "https://pricing.example.com"},
			},
			"mismatched": map[string]any{
				"command": "company-cli",
				"args":    []any{"rejected"},
			},
			"docs": map[string]any{
				"url": "https://mcp.example.com/docs",
			},
			"unlisted": map[string]any{
				"command": "other-cli",
			},
		},
		"requirements": map[string]any{
			"mcp_servers": map[string]any{
				"matched": map[string]any{
					"identity": map[string]any{
						"command": map[string]any{
							"executable": "company-cli",
							"args": []any{
								map[string]any{"match": "exact", "value": "mcp"},
								map[string]any{"match": "regex", "expression": `https://[a-z]+\.example\.com`},
							},
						},
					},
				},
				"mismatched": map[string]any{
					"identity": map[string]any{
						"command": map[string]any{
							"executable": "company-cli",
							"args":       []any{map[string]any{"match": "exact", "value": "approved"}},
						},
					},
				},
				"docs": map[string]any{
					"identity": map[string]any{
						"url": map[string]any{"match": "prefix", "value": "https://mcp.example.com/"},
					},
				},
			},
		},
	}}

	servers := mcpServersFromConfig(cfg)

	if !servers["matched"].Enabled || servers["matched"].DisabledReason != "" {
		t.Fatalf("matched server = %#v", servers["matched"])
	}
	if !servers["docs"].Enabled || servers["docs"].DisabledReason != "" {
		t.Fatalf("docs server = %#v", servers["docs"])
	}
	for _, name := range []string{"mismatched", "unlisted"} {
		if servers[name].Enabled || servers[name].DisabledReason != "requirements (config)" {
			t.Fatalf("%s server = %#v", name, servers[name])
		}
	}
}

func TestMCPCheckSkipsServersDisabledByRequirementsLikeRust(t *testing.T) {
	probed := []string{}
	withMCPHTTPProbe(t, func(rawURL string) (string, error) {
		probed = append(probed, rawURL)
		return "", errors.New("should not probe disabled servers")
	})
	cfg := &config.Config{Values: map[string]any{
		"mcp_servers": map[string]any{
			"required_stdio": map[string]any{
				"command":  "",
				"required": true,
			},
			"required_http": map[string]any{
				"url":      "https://mcp.example.test/mcp",
				"required": true,
			},
		},
		"requirements": map[string]any{
			"mcp_servers": map[string]any{},
		},
	}}

	check := mcpCheckFromServers(mcpServersFromConfig(cfg), map[string]string{}, t.TempDir())

	if check.Status != CheckStatusOK || check.Summary != "MCP configuration is locally consistent" {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "disabled servers: 2") {
		t.Fatalf("details = %#v", check.Details)
	}
	for _, detail := range check.Details {
		if strings.Contains(detail, "stdio command is empty") ||
			strings.Contains(detail, "required reachability failed") {
			t.Fatalf("disabled requirements server should not be diagnosed: %#v", check.Details)
		}
	}
	if len(probed) != 0 {
		t.Fatalf("probed disabled servers: %#v", probed)
	}
}

func TestSandboxCheckReportsConfigAndHelpers(t *testing.T) {
	dir := t.TempDir()
	linuxHelper := filepath.Join(dir, cli.DispatchLinuxSandboxArg0)
	execveWrapper := filepath.Join(dir, cli.DispatchExecveWrapperArg0)
	for _, path := range []string{linuxHelper, execveWrapper} {
		if err := os.WriteFile(path, []byte(""), 0o700); err != nil {
			t.Fatalf("write helper %s: %v", path, err)
		}
	}
	cfg := &config.Config{Values: map[string]any{
		"approval_policy": "never",
		"sandbox_mode":    "workspace-write",
		"sandbox_workspace_write": map[string]any{
			"writable_roots": []any{"/tmp/work"},
			"network_access": true,
		},
	}}
	check := sandboxCheckFromConfig(cfg, &cli.DispatchPaths{
		CodexLinuxSandboxExe: linuxHelper,
		MainExecveWrapperExe: execveWrapper,
	})
	if check.Status != CheckStatusOK {
		t.Fatalf("check = %+v", check)
	}
	for _, want := range []string{
		"approval policy: Never",
		"filesystem sandbox: workspace-write",
		"network sandbox: enabled",
		"codex-linux-sandbox helper: " + linuxHelper,
		"execve wrapper helper: " + execveWrapper,
	} {
		if !containsDetail(check, want) {
			t.Fatalf("missing detail %q in %#v", want, check.Details)
		}
	}
	for _, detail := range check.Details {
		if strings.HasPrefix(detail, "workspace writable") {
			t.Fatalf("workspace writable details should not be in Rust sandbox check: %#v", check.Details)
		}
	}
}

func TestSandboxCheckDoesNotWarnForMissingExecveWrapperLikeRust(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, cli.DispatchLinuxSandboxArg0)
	if err := os.WriteFile(path, []byte(""), 0o700); err != nil {
		t.Fatalf("write helper: %v", err)
	}
	cfg := &config.Config{Values: map[string]any{"sandbox_mode": "read-only"}}
	check := sandboxCheckFromConfig(cfg, &cli.DispatchPaths{CodexLinuxSandboxExe: path})
	if check.Status != CheckStatusOK || !containsDetail(check, "execve wrapper helper: none") {
		t.Fatalf("check = %+v", check)
	}
}

func TestSandboxCheckWarnsForMissingLinuxHelper(t *testing.T) {
	cfg := &config.Config{Values: map[string]any{"sandbox_mode": "read-only"}}
	missing := filepath.Join(t.TempDir(), cli.DispatchLinuxSandboxArg0)
	check := sandboxCheckFromConfig(cfg, &cli.DispatchPaths{CodexLinuxSandboxExe: missing})
	if check.Status != CheckStatusWarning || !containsDetail(check, "codex-linux-sandbox helper: "+missing) {
		t.Fatalf("check = %+v", check)
	}
}

func TestSandboxCheckUsesDispatchPathsInsteadOfPATHLikeRust(t *testing.T) {
	dir := t.TempDir()
	pathHelper := filepath.Join(dir, cli.DispatchLinuxSandboxArg0)
	if err := os.WriteFile(pathHelper, []byte(""), 0o700); err != nil {
		t.Fatalf("write PATH helper: %v", err)
	}
	t.Setenv("PATH", dir)
	cfg := &config.Config{Values: map[string]any{"sandbox_mode": "read-only"}}
	missing := filepath.Join(t.TempDir(), cli.DispatchLinuxSandboxArg0)
	check := sandboxCheckFromConfig(cfg, &cli.DispatchPaths{CodexLinuxSandboxExe: missing})
	if check.Status != CheckStatusWarning || !containsDetail(check, "codex-linux-sandbox helper: "+missing) {
		t.Fatalf("check = %+v", check)
	}
	if containsDetail(check, "codex-linux-sandbox helper: "+pathHelper) {
		t.Fatalf("sandbox helper should come from dispatch paths, not PATH: %#v", check.Details)
	}
}

func TestWebsocketReachabilitySkipsUnsupportedProvider(t *testing.T) {
	provider := model.CreateOSSProviderWithBaseURL("http://localhost:11434/v1", model.WireAPIResponses)
	check := websocketReachabilityCheckFromProvider(model.OllamaOSSProviderID, &provider, nil)
	if check.Status != CheckStatusOK || check.Summary != "Responses WebSocket is not enabled for the active provider" {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "supports websockets: false") {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestWebsocketReachabilityReportsEndpointForSupportedProvider(t *testing.T) {
	timeout := uint64(500)
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		if gotBeta := r.Header.Get(codexapi.ClientOpenAIBetaHeader); gotBeta != responsesWebsocketsV2BetaHeaderValue {
			t.Fatalf("OpenAI-Beta = %q", gotBeta)
		}
		if r.URL.Path != "/v1/responses" || r.URL.Query().Get("api-version") != "2026-01-01" {
			t.Fatalf("request url = %s", r.URL.String())
		}
		w.Header().Set("x-reasoning-included", "true")
		w.Header().Set("x-models-etag", `"etag-1"`)
		w.Header().Set("openai-model", "gpt-test")
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Fatalf("websocket accept error = %v", err)
		}
		time.Sleep(350 * time.Millisecond)
		_ = conn.Close(websocket.StatusNormalClosure, "ok")
	}))
	defer server.Close()
	provider := model.ProviderInfo{
		Name:                      "Custom",
		BaseURL:                   server.URL + "/v1",
		WireAPI:                   model.WireAPIResponses,
		QueryParams:               map[string]string{"api-version": "2026-01-01"},
		ExperimentalBearerToken:   "probe-token",
		SupportsWebsockets:        true,
		WebsocketConnectTimeoutMS: &timeout,
	}
	check := websocketReachabilityCheckFromProvider("custom", &provider, nil)
	if check.Status != CheckStatusOK || check.Summary != "Responses WebSocket handshake succeeded" {
		t.Fatalf("check = %+v", check)
	}
	endpoint := strings.Replace(server.URL, "http://", "ws://", 1) + "/v1/responses?api-version=2026-01-01"
	for _, want := range []string{
		"connect timeout: 500 ms",
		"endpoint: " + endpoint,
		"DNS: 1 IPv4, 0 IPv6, first IPv4",
		"handshake result: HTTP 101",
		"reasoning header: true",
		"server model present: true",
	} {
		if !containsDetail(check, want) {
			t.Fatalf("missing detail %q in %#v", want, check.Details)
		}
	}
	if detailHasPrefix(check, "handshake:") {
		t.Fatalf("details = %#v", check.Details)
	}
	if gotAuth != "Bearer probe-token" {
		t.Fatalf("Authorization = %q", gotAuth)
	}
}

func TestWebsocketReachabilityWarnsOnImmediateCloseLikeRust(t *testing.T) {
	timeout := uint64(500)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := websocket.Accept(w, r, nil)
		if err != nil {
			t.Fatalf("websocket accept error = %v", err)
		}
		_ = conn.Close(websocket.StatusPolicyViolation, "blocked")
	}))
	defer server.Close()
	provider := model.ProviderInfo{
		Name:                      "Custom",
		BaseURL:                   server.URL + "/v1",
		WireAPI:                   model.WireAPIResponses,
		SupportsWebsockets:        true,
		WebsocketConnectTimeoutMS: &timeout,
	}
	check := websocketReachabilityCheckFromProvider("custom", &provider, nil)
	if check.Status != CheckStatusWarning || check.Summary != "Responses WebSocket closed immediately after handshake" {
		t.Fatalf("check = %+v", check)
	}
	for _, want := range []string{
		"handshake result: HTTP 101",
		"immediate close code: 1008",
		"immediate close reason: blocked",
	} {
		if !containsDetail(check, want) {
			t.Fatalf("missing detail %q in %#v", want, check.Details)
		}
	}
}

func TestWebsocketReachabilityReportsHandshakeFailure(t *testing.T) {
	timeout := uint64(50)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no websocket", http.StatusTeapot)
	}))
	defer server.Close()
	provider := model.ProviderInfo{
		Name:                      "Custom",
		BaseURL:                   server.URL + "/v1",
		WireAPI:                   model.WireAPIResponses,
		SupportsWebsockets:        true,
		WebsocketConnectTimeoutMS: &timeout,
	}
	check := websocketReachabilityCheckFromProvider("custom", &provider, nil)
	if check.Status != CheckStatusWarning || check.Summary != "Responses WebSocket failed; HTTPS fallback may still work" {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "handshake API error: 418") {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestRolloutDBParityCheckOKWhenStoreMatchesRolloutLocations(t *testing.T) {
	home := t.TempDir()
	dbPath := createDoctorStateDB(t, home)
	activePath := writeDoctorRollout(t, home, "thread-active", false)
	archivedPath := writeDoctorRollout(t, home, "thread-archived", true)
	insertDoctorThreadRow(t, dbPath, "thread-active", activePath, false, "cli", "openai")
	insertDoctorThreadRow(t, dbPath, "thread-archived", archivedPath, true, "cli", "openai")

	check := rolloutDBParityCheck(home, &Options{})
	if check.Status != CheckStatusOK || check.Summary != "rollout files and state DB thread inventory agree" {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "rollout DB missing active rows: 0") ||
		!containsDetail(check, "rollout DB archive mismatches: 0") ||
		!containsDetail(check, "rollout DB duplicate DB paths: 0") ||
		!containsDetail(check, "rollout DB model providers: openai=2") ||
		!containsDetail(check, "rollout DB sources: cli=2") {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestStateCheckReportsRustPathDBAndRolloutDetails(t *testing.T) {
	home := t.TempDir()
	sqliteHome := t.TempDir()
	t.Setenv("CODEX_SQLITE_HOME", sqliteHome)
	if err := os.MkdirAll(filepath.Join(home, "log"), 0o700); err != nil {
		t.Fatalf("mkdir log: %v", err)
	}
	rolloutDir := filepath.Join(home, "sessions", "2026", "07", "06")
	if err := os.MkdirAll(rolloutDir, 0o700); err != nil {
		t.Fatalf("mkdir rollout dir: %v", err)
	}
	rolloutPath := filepath.Join(rolloutDir, "rollout-2026-07-06T00-00-00-thread.jsonl")
	if err := os.WriteFile(rolloutPath, []byte("abc"), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	db, err := sql.Open("sqlite", filepath.Join(sqliteHome, "state_5.sqlite"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	if _, err := db.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY)"); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close db: %v", err)
	}

	check := stateCheck(home, &Options{})
	if check.Status != CheckStatusOK || check.Summary != "state paths and databases are inspectable" {
		t.Fatalf("check = %+v", check)
	}
	for _, want := range []string{
		"CODEX_HOME: " + home + " (dir)",
		"log dir: " + filepath.Join(home, "log") + " (dir)",
		"sqlite home: " + sqliteHome + " (dir)",
		"state DB: " + filepath.Join(sqliteHome, "state_5.sqlite") + " (file)",
		"state DB integrity: ok",
		"log DB integrity: skipped (missing)",
		"active rollout files: 1 files, 3 total bytes, 3 average bytes",
		"archived rollout files: 0 files, 0 total bytes, 0 average bytes",
	} {
		if !containsDetail(check, want) {
			t.Fatalf("missing detail %q in %#v", want, check.Details)
		}
	}
}

func TestRolloutDBParityCheckWarnsForMissingAndArchiveMismatch(t *testing.T) {
	home := t.TempDir()
	dbPath := createDoctorStateDB(t, home)
	writeDoctorRollout(t, home, "thread-missing-record", false)
	mismatchPath := writeDoctorRollout(t, home, "thread-mismatch", true)
	insertDoctorThreadRow(t, dbPath, "thread-mismatch", mismatchPath, false, "cli", "openai")
	insertDoctorThreadRow(t, dbPath, "thread-stale-record", filepath.Join(home, "sessions", "missing.jsonl"), false, "cli", "openai")

	check := rolloutDBParityCheck(home, &Options{})
	if check.Status != CheckStatusWarning || check.Summary != "rollout files and state DB thread inventory differ" {
		t.Fatalf("check = %+v", check)
	}
	for _, want := range []string{
		"rollout DB missing active rows: 1",
		"rollout DB stale rows: 1",
		"rollout DB archive mismatches: 1",
	} {
		if !containsDetail(check, want) {
			t.Fatalf("missing detail %q in %#v", want, check.Details)
		}
	}
	if len(check.Issues) < 3 {
		t.Fatalf("issues = %#v", check.Issues)
	}
}

func TestRolloutDBParityCheckReportsMissingStateDBLikeRust(t *testing.T) {
	home := t.TempDir()
	writeDoctorRollout(t, home, "thread-active", false)

	check := rolloutDBParityCheck(home, &Options{})
	if check.Status != CheckStatusWarning || check.Summary != "state DB is missing while rollout files exist" {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "rollout DB rows: skipped (state DB missing)") {
		t.Fatalf("details = %#v", check.Details)
	}
	if check.Remediation == nil || !strings.Contains(*check.Remediation, "startup backfill") {
		t.Fatalf("remediation = %#v", check.Remediation)
	}
}

func TestRolloutDBParitySkipsStaleAndArchiveMismatchWhenScanCapReached(t *testing.T) {
	home := t.TempDir()
	missingPath := filepath.Join(home, "sessions", "missing.jsonl")
	record := &rolloutParityRecord{
		ID:          "thread-stale",
		RolloutPath: missingPath,
		Key:         rolloutParityPathKey(missingPath),
		Archived:    false,
		Source:      "cli",
	}
	scan := &rolloutParityScan{ReachedScanCap: true}

	check := rolloutParityCheckFromScanAndRecords(home, scan, []*rolloutParityRecord{record}, nil)
	if check.Status != CheckStatusWarning {
		t.Fatalf("check = %+v", check)
	}
	if !containsDetail(check, "rollout DB stale rows: skipped (scan cap reached)") ||
		!containsDetail(check, "rollout DB archive mismatches: skipped (scan cap reached)") {
		t.Fatalf("details = %#v", check.Details)
	}
	for _, issue := range check.Issues {
		if strings.Contains(issue.Cause, "stale") || strings.Contains(issue.Cause, "archive flags") {
			t.Fatalf("unexpected issue when scan cap reached: %#v", check.Issues)
		}
	}
}

func TestRolloutParityPathKeyUsesRustNormalizeFallback(t *testing.T) {
	dir := t.TempDir()
	existing := filepath.Join(dir, "rollout-2026-07-06T00-00-00-thread.jsonl")
	if err := os.WriteFile(existing, []byte("{}"), 0o600); err != nil {
		t.Fatalf("write rollout: %v", err)
	}
	gotExisting := rolloutParityPathKey(existing)
	if !filepath.IsAbs(gotExisting) {
		t.Fatalf("existing key = %q, want absolute canonical path", gotExisting)
	}

	missingRelative := filepath.Join("missing", "..", "rollout-2026-07-06T00-00-00-thread.jsonl")
	if got := rolloutParityPathKey(missingRelative); got != missingRelative {
		t.Fatalf("missing relative key = %q, want original %q", got, missingRelative)
	}

	if runtime.GOOS == "windows" {
		missingWindows := `C:\Codex\Sessions\Missing.JSONL`
		if got := rolloutParityPathKey(missingWindows); got != missingWindows {
			t.Fatalf("missing windows key = %q, want original case %q", got, missingWindows)
		}
	}
}

func TestRolloutDBParitySourceCategoryAndCountSummary(t *testing.T) {
	if got := rolloutParitySourceCategory("cli"); got != "cli" {
		t.Fatalf("source category = %q", got)
	}
	if got := rolloutParitySourceCategory(`{"subagent":"memory_consolidation"}`); got != "subagent:memory_consolidation" {
		t.Fatalf("source category = %q", got)
	}
	if got := rolloutParitySourceCategory(`{"subagent":{"thread_spawn":{"parent_thread_id":"00000000-0000-0000-0000-000000000001","depth":2}}}`); got != "subagent:thread_spawn" {
		t.Fatalf("source category = %q", got)
	}
	summary := rolloutParityCountSummary([]string{"a", "b", "c", "d", "e", "f", "g", "h", "i"})
	if summary != "a=1, b=1, c=1, d=1, e=1, f=1, g=1, h=1, other=1 across 1 categories" {
		t.Fatalf("summary = %q", summary)
	}
}

func writeDoctorRollout(t *testing.T, home string, threadID string, archived bool) string {
	t.Helper()
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:     home,
		ThreadID:      threadID,
		Source:        "cli",
		CWD:           "/repo",
		ModelProvider: "openai",
		HistoryMode:   "legacy",
		Now:           time.Date(2026, 6, 29, 1, 2, 3, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("NewRecorder(%s) error = %v", threadID, err)
	}
	if err := recorder.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	if archived {
		if _, err := rollout.Archive(recorder.Path(), home); err != nil {
			t.Fatalf("Archive(%s) error = %v", threadID, err)
		}
		return filepath.Join(home, rollout.ArchivedSessionsSubdir, filepath.Base(recorder.Path()))
	}
	return recorder.Path()
}

func createDoctorStateDB(t *testing.T, sqliteHome string) string {
	t.Helper()
	dbPath := filepath.Join(sqliteHome, "state_5.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open state DB: %v", err)
	}
	defer db.Close()
	_, err = db.Exec(`
CREATE TABLE threads (
    id TEXT PRIMARY KEY,
    rollout_path TEXT NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    source TEXT NOT NULL,
    model_provider TEXT NOT NULL,
    cwd TEXT NOT NULL,
    title TEXT NOT NULL,
    sandbox_policy TEXT NOT NULL,
    approval_mode TEXT NOT NULL,
    tokens_used INTEGER NOT NULL DEFAULT 0,
    has_user_event INTEGER NOT NULL DEFAULT 0,
    archived INTEGER NOT NULL DEFAULT 0,
    archived_at INTEGER,
    git_sha TEXT,
    git_branch TEXT,
    git_origin_url TEXT
);
`)
	if err != nil {
		t.Fatalf("create threads table: %v", err)
	}
	return dbPath
}

func insertDoctorThreadRow(t *testing.T, dbPath string, threadID string, rolloutPath string, archived bool, source string, provider string) {
	t.Helper()
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open state DB: %v", err)
	}
	defer db.Close()
	_, err = db.Exec(`
INSERT INTO threads (
    id,
    rollout_path,
    created_at,
    updated_at,
    source,
    model_provider,
    cwd,
    title,
    sandbox_policy,
    approval_mode,
    archived,
    archived_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
`, threadID, rolloutPath, int64(1), int64(1), source, provider, filepath.Dir(rolloutPath), "test title", "read-only", "on-request", boolToSQLiteInt(archived), optionalArchivedAtForDoctor(archived))
	if err != nil {
		t.Fatalf("insert thread row: %v", err)
	}
}

func boolToSQLiteInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}

func optionalArchivedAtForDoctor(archived bool) any {
	if archived {
		return int64(1)
	}
	return nil
}

func TestProviderReachabilityPlanAddsModelsRouteForOpenAICompatibleBaseURL(t *testing.T) {
	routeProbe := providerRouteProbeURL(
		providerAuthReachabilityNotRequired,
		"Custom",
		"https://example.com/openai/v1/",
		map[string]string{"api-version": "2026-01-01"},
		false,
	)
	if routeProbe == nil {
		t.Fatal("routeProbe = nil")
	}
	want := "https://example.com/openai/v1/models?api-version=2026-01-01"
	if *routeProbe != want {
		t.Fatalf("routeProbe = %q, want %q", *routeProbe, want)
	}
}

func TestProviderReachabilityRouteProbeKeepsRawQueryParamsLikeRust(t *testing.T) {
	got := providerURLForPath(
		"https://example.com/openai/v1/",
		"/models",
		map[string]string{"api-version": "2026 01 01"},
	)
	want := "https://example.com/openai/v1/models?api-version=2026 01 01"
	if got != want {
		t.Fatalf("providerURLForPath = %q, want %q", got, want)
	}
}

func TestProviderReachabilitySkipsModelsRouteForBedrock(t *testing.T) {
	plan := providerReachabilityPlanFromParts(
		providerAuthReachabilityNotRequired,
		model.AmazonBedrockProviderID,
		model.AmazonBedrockProviderName,
		"https://bedrock-runtime.us-east-1.amazonaws.com/openai/v1",
		nil,
		true,
		"https://chatgpt.com/backend-api/",
	)
	if len(plan.Endpoints) != 1 {
		t.Fatalf("endpoints = %#v", plan.Endpoints)
	}
	if plan.Endpoints[0].RouteProbeURL != nil {
		t.Fatalf("RouteProbeURL = %q", *plan.Endpoints[0].RouteProbeURL)
	}
}

func TestProviderReachabilityPlanUsesBedrockConfiguredRegion(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`
model_provider = "amazon-bedrock"

[model_providers.amazon-bedrock.aws]
region = "us-west-2"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	plan, err := providerReachabilityPlan(home, &Options{})
	if err != nil {
		t.Fatalf("providerReachabilityPlan error = %v", err)
	}
	if len(plan.Endpoints) != 1 {
		t.Fatalf("endpoints = %#v", plan.Endpoints)
	}
	want := "https://bedrock-mantle.us-west-2.api.aws/openai/v1"
	if plan.Endpoints[0].URL != want {
		t.Fatalf("URL = %q, want %q", plan.Endpoints[0].URL, want)
	}
	if plan.Endpoints[0].RouteProbeURL != nil {
		t.Fatalf("RouteProbeURL = %q", *plan.Endpoints[0].RouteProbeURL)
	}
}

func TestProviderReachabilityPlanRejectsUnsupportedBedrockRegion(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte(`
model_provider = "amazon-bedrock"

[model_providers.amazon-bedrock.aws]
region = "us-west-1"
`), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := providerReachabilityPlan(home, &Options{})
	if err == nil {
		t.Fatal("providerReachabilityPlan returned nil error, want unsupported region failure")
	}
	if !strings.Contains(err.Error(), "Amazon Bedrock Mantle does not support region `us-west-1`") {
		t.Fatalf("error = %v", err)
	}
}

func TestProviderReachabilityAPIKeyUsesOpenAIEndpoint(t *testing.T) {
	plan := providerReachabilityPlanFromParts(
		providerAuthReachabilityAPIKey,
		model.OpenAIProviderID,
		model.OpenAIProviderName,
		"",
		nil,
		false,
		"https://chatgpt.com/backend-api/",
	)
	if len(plan.Endpoints) != 1 {
		t.Fatalf("endpoints = %#v", plan.Endpoints)
	}
	endpoint := plan.Endpoints[0]
	if endpoint.URL != "https://api.openai.com/v1" || endpoint.RouteProbeURL == nil || *endpoint.RouteProbeURL != "https://api.openai.com/v1/models" {
		t.Fatalf("endpoint = %#v", endpoint)
	}
}

func TestProviderReachabilityRoute404FailsBadBaseURLPath(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	plan := providerReachabilityPlanFromParts(
		providerAuthReachabilityAPIKey,
		model.OpenAIProviderID,
		model.OpenAIProviderName,
		server.URL+"/xxxx",
		nil,
		false,
		"https://chatgpt.com/backend-api/",
	)
	builder := NewBuilder()
	builder.httpClient = server.Client()

	check := builder.runProviderReachabilityCheck(plan)
	if check.Status != CheckStatusFail {
		t.Fatalf("status = %s details=%v issues=%v", check.Status, check.Details, check.Issues)
	}
	if len(check.Issues) != 1 || check.Issues[0].Remedy == nil || *check.Issues[0].Remedy != "Set base_url to the provider API root, for example https://api.openai.com/v1" {
		t.Fatalf("issues = %#v", check.Issues)
	}
}

func TestProviderReachabilityRoute401KeepsReachabilityOK(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/models") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(server.Close)
	plan := providerReachabilityPlanFromParts(
		providerAuthReachabilityAPIKey,
		model.OpenAIProviderID,
		model.OpenAIProviderName,
		server.URL+"/v1",
		nil,
		false,
		"https://chatgpt.com/backend-api/",
	)
	builder := NewBuilder()
	builder.httpClient = server.Client()

	check := builder.runProviderReachabilityCheck(plan)
	if check.Status != CheckStatusOK {
		t.Fatalf("status = %s details=%v issues=%v", check.Status, check.Details, check.Issues)
	}
	if !containsDetail(check, "route exists (HTTP 401)") {
		t.Fatalf("details = %#v", check.Details)
	}
}

func TestProviderReachabilityModeUsesAPIKeyAuth(t *testing.T) {
	t.Setenv(auth.OpenAIAPIKeyEnv, "")
	mode := providerAuthReachabilityModeFromAuth(
		true,
		func(key string) bool { return key == auth.OpenAIAPIKeyEnv },
		nil,
	)
	if mode != providerAuthReachabilityAPIKey {
		t.Fatalf("mode = %s", mode)
	}
	mode = providerAuthReachabilityModeFromAuth(
		true,
		func(string) bool { return false },
		&auth.ResolvedAuth{Auth: auth.FromAPIKey("sk-test"), Source: "file"},
	)
	if mode != providerAuthReachabilityAPIKey {
		t.Fatalf("mode = %s", mode)
	}
}

func TestRenderHumanSummaryASCII(t *testing.T) {
	report := &Report{
		SchemaVersion: 1,
		CodexVersion:  "test",
		OverallStatus: CheckStatusWarning,
		Checks: []*DoctorCheck{
			NewCheck("system.environment", "system", CheckStatusOK, "system ok").Detail("os: test"),
			NewCheck("auth.credentials", "auth", CheckStatusWarning, "no credentials found").Remediate("Run codex login."),
		},
	}
	rendered := RenderHuman(report, &Options{Summary: true, ASCII: true})
	if !strings.Contains(rendered, "[ok] system") || !strings.Contains(rendered, "[!!] auth") {
		t.Fatalf("rendered = %s", rendered)
	}
	if strings.Contains(rendered, "os                       test") {
		t.Fatalf("summary should omit details: %s", rendered)
	}
	if strings.Contains(rendered, "warning\n\nRun codex doctor") {
		t.Fatalf("summary line has an extra blank line: %s", rendered)
	}
	if !strings.Contains(rendered, "1 ok | 1 notes | 1 warn | 0 fail degraded") {
		t.Fatalf("summary line does not use Rust degraded label: %s", rendered)
	}
	if !strings.Contains(rendered, "Run codex doctor without --summary for detailed diagnostics.") || !strings.Contains(rendered, "--all expand truncated lists") || !strings.Contains(rendered, "--json redacted report") {
		t.Fatalf("summary footer mismatch: %s", rendered)
	}
}

func TestRenderHumanHeaderIncludesPlatformLikeRust(t *testing.T) {
	report := &Report{
		SchemaVersion: 1,
		CodexVersion:  "test",
		OverallStatus: CheckStatusOK,
		Checks: []*DoctorCheck{
			NewCheck("runtime.provenance", "runtime", CheckStatusOK, "runtime ok").
				Detail("platform: windows-x86_64"),
		},
	}
	rendered := RenderHuman(report, &Options{Summary: true, ASCII: true})
	if !strings.HasPrefix(rendered, "Codex Doctor vtest | windows-x86_64\n\n") {
		t.Fatalf("header mismatch:\n%s", rendered)
	}
}

func TestRenderHumanDetailedFooterMatchesRustHints(t *testing.T) {
	report := &Report{
		SchemaVersion: 1,
		CodexVersion:  "test",
		OverallStatus: CheckStatusOK,
		Checks: []*DoctorCheck{
			NewCheck("system.environment", "system", CheckStatusOK, "system ok").Detail("os: test"),
		},
	}
	rendered := RenderHuman(report, &Options{ASCII: true})
	if !strings.Contains(rendered, "--summary compact output") || !strings.Contains(rendered, "--all expand truncated lists") || !strings.Contains(rendered, "--json redacted report") {
		t.Fatalf("detailed footer mismatch: %s", rendered)
	}
}

func TestRenderHumanPromotesNotesAndIdleLikeRust(t *testing.T) {
	report := &Report{
		SchemaVersion: 1,
		CodexVersion:  "0.0.0",
		OverallStatus: CheckStatusWarning,
		Checks: []*DoctorCheck{
			NewCheck("updates.status", "updates", CheckStatusOK, "update configuration is locally consistent").
				Detail("latest version status: newer version is available").
				Detail("latest version: 0.130.0").
				Detail("dismissed version: 0.128.0"),
			NewCheck("state.paths", "state", CheckStatusOK, "state paths inspectable").
				Detail("active rollout files: 1515 files, 2702146365 total bytes, 1783594 average bytes"),
			NewCheck("sandbox.helpers", "sandbox", CheckStatusOK, "sandbox configuration is readable").
				Detail("filesystem sandbox: danger-full-access").
				Detail("network sandbox: restricted").
				Detail("approval policy: Never"),
			NewCheck("mcp.config", "mcp", CheckStatusWarning, "MCP configuration has optional issues"),
			NewCheck("network.websocket_reachability", "websocket", CheckStatusOK, "Responses WebSocket handshake succeeded").
				Detail("auth mode: chatgpt"),
			NewCheck("network.provider_reachability", "reachability", CheckStatusOK, "active provider endpoints are reachable over HTTP").
				Detail("reachability mode: API key auth"),
			NewCheck("app_server.status", "app-server", CheckStatusOK, "background server is not running").
				Detail("status: not running").
				Detail("mode: ephemeral"),
		},
	}

	rendered := RenderHuman(report, &Options{Summary: true, ASCII: true})
	for _, want := range []string{
		"Notes\n",
		"[up] updates",
		"0.130.0 available (current 0.0.0, dismissed 0.128.0)",
		"[!!] rollouts",
		"1,515 active files | 2.52 GB on disk",
		"[!!] sandbox",
		"[!!] mcp",
		"[!!] auth",
		"[--] app-server   not running (ephemeral mode)",
		"5 ok | 1 idle | 5 notes | 1 warn | 0 fail degraded",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("missing %q in:\n%s", want, rendered)
		}
	}
	assertBefore(t, rendered, "Notes\n", "Environment\n")
	assertBefore(t, rendered, "Configuration\n", "\n  [!!] mcp")
}

func TestRenderHumanRowDescriptionsMatchRustSummaries(t *testing.T) {
	report := &Report{
		SchemaVersion: 1,
		CodexVersion:  "test",
		OverallStatus: CheckStatusOK,
		Checks: []*DoctorCheck{
			NewCheck("system.environment", "system", CheckStatusOK, "system ok").
				Detail("os language: en-US"),
			NewCheck("runtime.provenance", "runtime", CheckStatusOK, "runtime ok").
				Detail("current executable: /repo/target/debug/codex").
				Detail("install method: cargo"),
			NewCheck("installation", "install", CheckStatusOK, "installation identity is available"),
			NewCheck("runtime.search", "search", CheckStatusOK, "search is OK (ripgrep)").
				Detail("search provider: ripgrep").
				Detail("search command: rg").
				Detail("search command readiness: rg 14.1.0"),
			NewCheck("git.environment", "git", CheckStatusOK, "git ok").
				Detail("git version: git version 2.45.0"),
			NewCheck("terminal.env", "terminal", CheckStatusOK, "terminal ok").
				Detail("terminal: Ghostty").
				Detail("terminal version: 1.3.1").
				Detail("multiplexer: tmux").
				Detail("TERM: xterm-256color"),
			NewCheck("terminal.title", "title", CheckStatusOK, "terminal title configured").
				Detail("terminal title source: config").
				Detail("terminal title project value: repo"),
			NewCheck("state.paths", "state", CheckStatusOK, "state paths inspectable").
				Detail("state DB integrity: ok").
				Detail("log DB integrity: ok").
				Detail("goals DB integrity: ok").
				Detail("memories DB integrity: ok"),
			NewCheck("config.load", "config", CheckStatusOK, "config loaded"),
			NewCheck("mcp.config", "mcp", CheckStatusOK, "mcp ok").
				Detail("configured servers: 2").
				Detail("disabled servers: 1").
				Detail("stdio servers: 1").
				Detail("http servers: 1"),
			NewCheck("sandbox.helpers", "sandbox", CheckStatusOK, "sandbox configuration is readable").
				Detail("filesystem sandbox: restricted").
				Detail("network sandbox: true").
				Detail("approval policy: on-request"),
			NewCheck("network.env", "network", CheckStatusOK, "network ok").
				Detail("proxy env vars: none"),
			NewCheck("network.websocket_reachability", "websocket", CheckStatusOK, "Responses WebSocket handshake succeeded").
				Detail("handshake result: 101 Switching Protocols").
				Detail("connect timeout: 5000 ms"),
			NewCheck("app_server.status", "app-server", CheckStatusOK, "background server is running").
				Detail("status: running").
				Detail("mode: daemon"),
		},
	}

	rendered := RenderHuman(report, &Options{Summary: true, ASCII: true})
	for _, want := range []string{
		"[ok] system       en-US",
		"[ok] runtime      local debug build",
		"[ok] install      consistent",
		"[ok] search       rg 14.1.0 (ripgrep, `rg`)",
		"[ok] git          git version 2.45.0",
		"[ok] terminal     Ghostty 1.3.1 | tmux | TERM=xterm-256color",
		"[ok] title        config | project repo",
		"[ok] state        databases healthy",
		"[ok] config       loaded",
		"[ok] mcp          2 server (1 stdio, 1 http) | 1 disabled",
		"[ok] sandbox      restricted fs + restricted network | approval on-request",
		"[ok] network      no proxy env vars",
		"[ok] websocket    connected (101 Switching Protocols) | 5s timeout",
		"[ok] app-server   running (daemon mode)",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("missing %q in:\n%s", want, rendered)
		}
	}
}

func TestRenderHumanDetailsAttachIssueMetadataLikeRust(t *testing.T) {
	report := &Report{
		SchemaVersion: 1,
		CodexVersion:  "test",
		OverallStatus: CheckStatusWarning,
		Checks: []*DoctorCheck{
			NewCheck("terminal.env", "terminal", CheckStatusWarning, "terminal warning").
				Detail("COLUMNS: 60").
				Detail("effective locale: C").
				Issue(NewIssue(CheckStatusWarning, "COLUMNS=60 - output may wrap (recommended >=80)").
					WithMeasured("60 columns").
					WithExpected(">= 80 columns").
					WithRemedy("resize the window to at least 80 columns").
					WithField("COLUMNS")).
				Issue(NewIssue(CheckStatusWarning, "locale is not UTF-8 - unicode glyphs may render incorrectly").
					WithMeasured("C").
					WithExpected("UTF-8 locale, for example en_US.UTF-8").
					WithRemedy("resize the window to at least 80 columns").
					WithField("effective locale")),
		},
	}

	rendered := RenderHuman(report, &Options{ASCII: true})
	for _, want := range []string{
		"> COLUMNS                  60 columns (expected >= 80 columns)",
		"> effective locale         C (expected UTF-8 locale, for example en_US.UTF-8)",
		"-> resize the window to at least 80 columns",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("missing %q in:\n%s", want, rendered)
		}
	}
	if strings.Count(rendered, "-> resize the window to at least 80 columns") != 1 {
		t.Fatalf("remedy was not de-duplicated:\n%s", rendered)
	}
	if strings.Contains(rendered, "issue                    ") || strings.Contains(rendered, "expected                 ") {
		t.Fatalf("legacy issue detail rows should not be rendered:\n%s", rendered)
	}
}

func TestRenderHumanDetailsHumanizePathAndTimestampLikeRust(t *testing.T) {
	t.Setenv("HOME", "/home/alice")
	report := &Report{
		SchemaVersion: 1,
		CodexVersion:  "test",
		OverallStatus: CheckStatusOK,
		Checks: []*DoctorCheck{
			NewCheck("state.paths", "state", CheckStatusOK, "state paths inspectable").
				Detail("codex home: /home/alice/codex").
				Detail("long path: /home/alice/projects/this/path/is/very/very/long/for/codex/file.txt").
				Detail("last checked at: 2026-07-06T09:15:30Z"),
		},
	}

	rendered := RenderHuman(report, &Options{ASCII: true})
	for _, want := range []string{
		"codex home               ~/codex",
		"last checked at          2026-07-06 09:15 UTC",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("missing %q in:\n%s", want, rendered)
		}
	}
	if !strings.Contains(rendered, "long path") || !strings.Contains(rendered, "~/projects/") || !strings.Contains(rendered, "...") || !strings.Contains(rendered, "file.txt") {
		t.Fatalf("long path was not shortened like Rust:\n%s", rendered)
	}
}

func TestRenderHumanPathEntriesRespectAllLikeRust(t *testing.T) {
	report := &Report{
		SchemaVersion: 1,
		CodexVersion:  "test",
		OverallStatus: CheckStatusOK,
		Checks: []*DoctorCheck{
			NewCheck("git.environment", "git", CheckStatusOK, "git ok").
				Detail("selected git: /bin/git").
				Detail("PATH git entries: 5").
				Detail("PATH git #1: /opt/git-1").
				Detail("PATH git #2: /opt/git-2").
				Detail("PATH git #3: /opt/git-3").
				Detail("PATH git #4: /opt/git-4").
				Detail("PATH git #5: /opt/git-5"),
		},
	}

	compact := RenderHuman(report, &Options{ASCII: true})
	for _, want := range []string{
		"PATH entries (5)",
		"/opt/git-1",
		"/opt/git-2",
		"/opt/git-3",
		"... (full list with --all)",
	} {
		if !strings.Contains(compact, want) {
			t.Fatalf("compact output missing %q in:\n%s", want, compact)
		}
	}
	if strings.Contains(compact, "/opt/git-4") || strings.Contains(compact, "/opt/git-5") {
		t.Fatalf("compact output should truncate PATH entries:\n%s", compact)
	}

	expanded := RenderHuman(report, &Options{ASCII: true, All: true})
	if strings.Contains(expanded, "... (full list with --all)") {
		t.Fatalf("--all output should not include truncation hint:\n%s", expanded)
	}
	for _, want := range []string{"/opt/git-1", "/opt/git-2", "/opt/git-3", "/opt/git-4", "/opt/git-5"} {
		if !strings.Contains(expanded, want) {
			t.Fatalf("--all output missing %q in:\n%s", want, expanded)
		}
	}
}

func TestRenderHumanFeatureFlagsRespectAllLikeRust(t *testing.T) {
	report := &Report{
		SchemaVersion: 1,
		CodexVersion:  "test",
		OverallStatus: CheckStatusOK,
		Checks: []*DoctorCheck{
			NewCheck("config.load", "config", CheckStatusOK, "config loaded").
				Detail("feature flags enabled: 9").
				Detail("enabled feature flags: alpha, beta, gamma, delta, epsilon, zeta, eta, theta, iota").
				Detail("feature flag overrides: beta=true, gamma=false"),
		},
	}

	compact := RenderHuman(report, &Options{ASCII: true})
	for _, want := range []string{
		"feature flags            9 enabled | 2 overridden (full list with --all)",
		"overrides                beta, gamma",
	} {
		if !strings.Contains(compact, want) {
			t.Fatalf("compact output missing %q in:\n%s", want, compact)
		}
	}
	if strings.Contains(compact, "enabled flags") {
		t.Fatalf("compact output should not show enabled flags list:\n%s", compact)
	}

	expanded := RenderHuman(report, &Options{ASCII: true, All: true})
	if strings.Contains(expanded, "full list with --all") {
		t.Fatalf("--all output should not include feature truncation hint:\n%s", expanded)
	}
	if !strings.Contains(expanded, "enabled flags") || !strings.Contains(expanded, "alpha, beta, gamma, delta, epsilon, zeta, eta, theta, iota") {
		t.Fatalf("--all output missing enabled flags list:\n%s", expanded)
	}
}

func TestRenderHumanStateDetailsMatchRustSummaries(t *testing.T) {
	report := &Report{
		SchemaVersion: 1,
		CodexVersion:  "test",
		OverallStatus: CheckStatusOK,
		Checks: []*DoctorCheck{
			NewCheck("state.paths", "state", CheckStatusOK, "state paths inspectable").
				Detail("state DB: /tmp/state.sqlite").
				Detail("state DB integrity: ok").
				Detail("log DB: /tmp/log.sqlite").
				Detail("log DB integrity: ok").
				Detail("active rollout files: 1515 files, 2702146365 total bytes, 1783594 average bytes").
				Detail("archived rollout files: 2 files, 2048 total bytes, 1024 average bytes"),
		},
	}

	rendered := RenderHuman(report, &Options{ASCII: true})
	for _, want := range []string{
		"state DB                 /tmp/state.sqlite | integrity ok",
		"log DB                   /tmp/log.sqlite | integrity ok",
		"active rollouts          1,515 files | 2.52 GB (avg 1.70 MB)",
		"archived rollouts        2 files | 2.00 KB (avg 1.00 KB)",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("missing %q in:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "state DB integrity") || strings.Contains(rendered, "active rollout files") {
		t.Fatalf("raw state detail rows should be folded:\n%s", rendered)
	}
}

func TestRenderHumanUsesRustDoctorGroups(t *testing.T) {
	report := &Report{
		SchemaVersion: 1,
		CodexVersion:  "test",
		OverallStatus: CheckStatusOK,
		Checks: []*DoctorCheck{
			NewCheck("system.environment", "system", CheckStatusOK, "system ok"),
			NewCheck("mcp.config", "mcp", CheckStatusOK, "mcp ok"),
			NewCheck("sandbox.config", "sandbox", CheckStatusOK, "sandbox ok"),
			NewCheck("updates.status", "updates", CheckStatusOK, "updates ok"),
			NewCheck("network.env", "network", CheckStatusOK, "network ok"),
			NewCheck("background.server", "app-server", CheckStatusOK, "server ok"),
		},
	}
	rendered := RenderHuman(report, &Options{Summary: true, ASCII: true})
	for _, title := range []string{"Environment", "Configuration", "Updates", "Connectivity", "Background Server"} {
		if !strings.Contains(rendered, title+"\n") {
			t.Fatalf("missing group %q in:\n%s", title, rendered)
		}
	}
	assertBefore(t, rendered, "Configuration\n", "[ok] mcp")
	assertBefore(t, rendered, "[ok] sandbox", "Updates\n")
	assertBefore(t, rendered, "Updates\n", "[ok] updates")
	assertBefore(t, rendered, "[ok] updates", "Connectivity\n")
	assertBefore(t, rendered, "Background Server\n", "[ok] app-server")
}

func TestRenderHumanShowsUnknownCategories(t *testing.T) {
	report := &Report{
		SchemaVersion: 1,
		CodexVersion:  "test",
		OverallStatus: CheckStatusWarning,
		Checks: []*DoctorCheck{
			NewCheck("system.environment", "system", CheckStatusOK, "system ok"),
			NewCheck("future.check", "future", CheckStatusWarning, "future warning"),
		},
	}
	rendered := RenderHuman(report, &Options{Summary: true, ASCII: true})
	if !strings.Contains(rendered, "Other\n") || !strings.Contains(rendered, "[!!] future") {
		t.Fatalf("unknown category was not rendered:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Other\n  [!!] future") {
		t.Fatalf("unknown category row was not rendered in Other:\n%s", rendered)
	}
}

func TestJSONReportShapeStructuresAndSanitizesDetails(t *testing.T) {
	remedy := "check editor"
	report := &Report{
		SchemaVersion: 1,
		CodexVersion:  "test",
		OverallStatus: CheckStatusOK,
		Checks: []*DoctorCheck{
			NewCheck("system.environment", "system", CheckStatusOK, "system ok").
				Detail("os: test").
				Detail("os: duplicate").
				Detail("EDITOR: vim --cmd secret").
				Detail("reachability failed: https://user:pass@example.com/mcp?x=abc#frag (connect failed)").
				Detail("unstructured detail").
				Issue(NewIssue(CheckStatusWarning, "authorization: Bearer abc123").WithRemedy(remedy)),
			NewCheck("config.load", "config", CheckStatusOK, "config ok"),
		},
	}
	data, err := json.Marshal(JSONReportFromReport(report))
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(data), `"system.environment"`) || !strings.Contains(string(data), `"os":["test","duplicate"]`) || !strings.Contains(string(data), `"EDITOR":"set"`) || !strings.Contains(string(data), `"notes":["unstructured detail"]`) {
		t.Fatalf("json = %s", data)
	}
	if !strings.Contains(string(data), `"remediation":null`) || !strings.Contains(string(data), `"measured":null`) || !strings.Contains(string(data), `"expected":null`) || !strings.Contains(string(data), `"fields":[]`) {
		t.Fatalf("json missing Rust fixed null/empty fields: %s", data)
	}
	var payload struct {
		Checks map[string]struct {
			Issues *[]json.RawMessage `json:"issues"`
		} `json:"checks"`
	}
	if err := json.Unmarshal(data, &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	noIssueCheck := payload.Checks["config.load"]
	if noIssueCheck.Issues == nil || len(*noIssueCheck.Issues) != 0 {
		t.Fatalf("config.load issues = %#v, want present empty array in %s", noIssueCheck.Issues, data)
	}
	if strings.Contains(string(data), "user:pass") || strings.Contains(string(data), "?x=abc") || strings.Contains(string(data), "Bearer abc123") {
		t.Fatalf("json was not redacted: %s", data)
	}
	if !strings.Contains(string(data), `https://example.com/mcp`) || !strings.Contains(string(data), `"cause":"authorization: \u003credacted\u003e"`) {
		t.Fatalf("json redaction mismatch: %s", data)
	}
}

func TestRenderHumanRedactsDoctorDetails(t *testing.T) {
	remediation := "see https://user:pass@example.com/mcp/abc123xyz"
	report := &Report{
		SchemaVersion: 1,
		CodexVersion:  "test",
		OverallStatus: CheckStatusWarning,
		Checks: []*DoctorCheck{
			NewCheck("network.provider_reachability", "reachability", CheckStatusWarning, "warning").
				Detail("authorization header: Bearer abc123").
				Issue(NewIssue(CheckStatusWarning, "token problem: abc123").WithRemedy(remediation)).
				Remediate(remediation),
		},
	}
	rendered := RenderHuman(report, &Options{ASCII: true})
	if strings.Contains(rendered, "Bearer abc123") || strings.Contains(rendered, "abc123xyz") || strings.Contains(rendered, "user:pass") {
		t.Fatalf("rendered human report was not redacted:\n%s", rendered)
	}
	if !strings.Contains(rendered, "<redacted>") || !strings.Contains(rendered, "https://example.com/mcp/<redacted>") {
		t.Fatalf("rendered human report redaction mismatch:\n%s", rendered)
	}
}

func TestRunJSONUsesStructuredReport(t *testing.T) {
	var out strings.Builder
	_, err := Run(&Options{
		JSON:      true,
		CodexHome: t.TempDir(),
		Root: cli.RootOptions{
			Shared: cli.SharedOptions{OSSProvider: model.OllamaOSSProviderID},
		},
	}, &out)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var payload struct {
		Checks map[string]json.RawMessage `json:"checks"`
	}
	if err := json.Unmarshal([]byte(out.String()), &payload); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if _, ok := payload.Checks["system.environment"]; !ok {
		t.Fatalf("checks = %#v", payload.Checks)
	}
}

func hasCategory(report *Report, category string) bool {
	for _, check := range report.Checks {
		if check.Category == category {
			return true
		}
	}
	return false
}

func hasCheckID(report *Report, id string) bool {
	for _, check := range report.Checks {
		if check.ID == id {
			return true
		}
	}
	return false
}

func checkIDs(report *Report) []string {
	ids := make([]string, 0, len(report.Checks))
	for _, check := range report.Checks {
		ids = append(ids, check.ID)
	}
	return ids
}

func clearDoctorAuthEnv(t *testing.T) {
	t.Helper()
	t.Setenv(auth.OpenAIAPIKeyEnv, "")
	t.Setenv(auth.CodexAPIKeyEnv, "")
	t.Setenv(auth.CodexAccessTokenEnv, "")
}

func clearDoctorProxyEnv(t *testing.T) {
	t.Helper()
	for _, key := range []string{"HTTP_PROXY", "HTTPS_PROXY", "ALL_PROXY", "NO_PROXY", "http_proxy", "https_proxy", "all_proxy", "no_proxy"} {
		t.Setenv(key, "")
	}
}

func unsetEnvForDoctor(t *testing.T, key string) {
	t.Helper()
	old, ok := os.LookupEnv(key)
	if err := os.Unsetenv(key); err != nil {
		t.Fatalf("unset %s: %v", key, err)
	}
	t.Cleanup(func() {
		if ok {
			_ = os.Setenv(key, old)
		} else {
			_ = os.Unsetenv(key)
		}
	})
}

func withNPMRootCommandForDoctor(t *testing.T, fn func() (string, error)) {
	t.Helper()
	old := runNPMRootCommandForDoctor
	runNPMRootCommandForDoctor = fn
	t.Cleanup(func() {
		runNPMRootCommandForDoctor = old
	})
}

func withCodexPathEntriesCommandForDoctor(t *testing.T, fn func() (string, error)) {
	t.Helper()
	old := runCodexPathEntriesCommandForDoctor
	runCodexPathEntriesCommandForDoctor = fn
	t.Cleanup(func() {
		runCodexPathEntriesCommandForDoctor = old
	})
}

func containsDetail(check *DoctorCheck, needle string) bool {
	for _, detail := range check.Details {
		if strings.Contains(detail, needle) {
			return true
		}
	}
	return false
}

func detailHasPrefix(check *DoctorCheck, prefix string) bool {
	for _, detail := range check.Details {
		if strings.HasPrefix(detail, prefix) {
			return true
		}
	}
	return false
}

func sameStringSlice(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func assertBefore(t *testing.T, haystack string, first string, second string) {
	t.Helper()
	firstIndex := strings.Index(haystack, first)
	secondIndex := strings.Index(haystack, second)
	if firstIndex < 0 || secondIndex < 0 || firstIndex >= secondIndex {
		t.Fatalf("expected %q before %q in:\n%s", first, second, haystack)
	}
}

func appserverDaemonPathsForTest(codexHome string) *appserverdaemon.Paths {
	return appserverdaemon.PathsForCodexHome(codexHome)
}

func localOnlyHTTPClient(t *testing.T) *http.Client {
	t.Helper()
	dialer := &net.Dialer{Timeout: 50 * time.Millisecond}
	return &http.Client{
		Timeout: 100 * time.Millisecond,
		Transport: &http.Transport{
			DialContext: func(ctx context.Context, networkName string, address string) (net.Conn, error) {
				host, _, err := net.SplitHostPort(address)
				if err != nil {
					return nil, err
				}
				ip := net.ParseIP(host)
				if ip == nil || !ip.IsLoopback() {
					return nil, &net.OpError{Op: "dial", Net: networkName, Addr: nil, Err: os.ErrPermission}
				}
				return dialer.DialContext(ctx, networkName, address)
			},
		},
	}
}
