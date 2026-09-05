package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/sandbox"
)

func TestLoadRequirementsFileParsesRustStyleTOML(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requirements.toml")
	body := `
allowed_approval_policies = ["on-request"]
allowed_approvals_reviewers = ["user"]
allowed_sandbox_modes = ["read-only", "workspace-write"]
allowed_web_search_modes = []
allow_managed_hooks_only = true
allow_appshots = false
allow_remote_control = true
enforce_residency = "us"

[browser_use]
disable_auto_review = true

[feature_requirements]
shell_tool = false
web_search = true

[hooks]
managed_dir = "/managed/hooks"
windows_managed_dir = "C:\\managed\\hooks"

[[hooks.PreToolUse]]
matcher = "shell"
hooks = [{ type = "command", command = "echo ok", timeout_sec = 5, async = true, status_message = "checking" }]

[experimental_network]
enabled = true
http_port = 1234
socks_port = 5678
allow_upstream_proxy = true
dangerously_allow_non_loopback_proxy = false
dangerously_allow_all_unix_sockets = true
managed_allowed_domains_only = false
allow_local_binding = true

[experimental_network.domains]
"a.example" = "allow"
"b.example" = "deny"

[experimental_network.unix_sockets]
"/tmp/a.sock" = "allow"
"/tmp/b.sock" = "deny"

[models.new_thread]
model = "gpt-5"
model_reasoning_effort = "high"
service_tier = "auto"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile requirements error = %v", err)
	}

	requirements, err := LoadRequirementsFile(path)
	if err != nil {
		t.Fatalf("LoadRequirementsFile returned error: %v", err)
	}
	if requirements == nil {
		t.Fatal("LoadRequirementsFile returned nil requirements")
	}
	if len(requirements.AllowedApprovalPolicies) != 1 || requirements.AllowedApprovalPolicies[0] != sandbox.ApprovalOnRequest {
		t.Fatalf("AllowedApprovalPolicies = %#v", requirements.AllowedApprovalPolicies)
	}
	if len(requirements.AllowedSandboxModes) != 2 || requirements.AllowedSandboxModes[1] != sandbox.SandboxWorkspaceWrite {
		t.Fatalf("AllowedSandboxModes = %#v", requirements.AllowedSandboxModes)
	}
	if requirements.AllowedWebSearchModes == nil || len(requirements.AllowedWebSearchModes) != 0 {
		t.Fatalf("AllowedWebSearchModes = %#v, want explicit empty slice", requirements.AllowedWebSearchModes)
	}
	if requirements.AllowManagedHooksOnly == nil || !*requirements.AllowManagedHooksOnly {
		t.Fatalf("AllowManagedHooksOnly = %#v", requirements.AllowManagedHooksOnly)
	}
	if requirements.AllowAppshots == nil || *requirements.AllowAppshots {
		t.Fatalf("AllowAppshots = %#v", requirements.AllowAppshots)
	}
	if requirements.BrowserUse == nil || requirements.BrowserUse.DisableAutoReview == nil || !*requirements.BrowserUse.DisableAutoReview {
		t.Fatalf("BrowserUse = %#v", requirements.BrowserUse)
	}
	if requirements.FeatureRequirements["shell_tool"] || !requirements.FeatureRequirements["web_search"] {
		t.Fatalf("FeatureRequirements = %#v", requirements.FeatureRequirements)
	}
	if requirements.Hooks == nil || requirements.Hooks.ManagedDir == nil || *requirements.Hooks.ManagedDir != "/managed/hooks" {
		t.Fatalf("Hooks = %#v", requirements.Hooks)
	}
	if len(requirements.Hooks.PreToolUse) != 1 || len(requirements.Hooks.PreToolUse[0].Hooks) != 1 || requirements.Hooks.PreToolUse[0].Hooks[0].TimeoutSec == nil || *requirements.Hooks.PreToolUse[0].Hooks[0].TimeoutSec != 5 {
		t.Fatalf("Hooks.PreToolUse = %#v", requirements.Hooks.PreToolUse)
	}
	if requirements.Network == nil || requirements.Network.HTTPPort == nil || *requirements.Network.HTTPPort != 1234 {
		t.Fatalf("Network = %#v", requirements.Network)
	}
	if requirements.Network.Domains["a.example"] != NetworkAllow || requirements.Network.UnixSockets["/tmp/b.sock"] != NetworkDeny {
		t.Fatalf("Network permissions = domains %#v sockets %#v", requirements.Network.Domains, requirements.Network.UnixSockets)
	}
	if requirements.Models == nil || requirements.Models.NewThread == nil || requirements.Models.NewThread.Model == nil || *requirements.Models.NewThread.Model != "gpt-5" {
		t.Fatalf("Models = %#v", requirements.Models)
	}
}

func TestLoadRequirementsFileParsesManagedMCPAndPluginMatchersLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requirements.toml")
	body := `
[mcp_servers.docs.identity]
url = "https://docs.example/mcp"

[mcp_servers.shell.identity.command]
executable = "company-cli"
args = [
  { match = "exact", value = "approved" },
  { match = "prefix", value = "tenant-" },
  { match = "regex", expression = "[a-z]+-[0-9]+" },
]

[plugins."no-allowlist@test"]

[plugins."empty@test".mcp_servers]

[plugins."sample@test".mcp_servers.plugin_docs.identity]
url = "https://plugin.example/mcp"
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile requirements error = %v", err)
	}
	requirements, err := LoadRequirementsFile(path)
	if err != nil {
		t.Fatalf("LoadRequirementsFile() error = %v", err)
	}
	if requirements == nil || len(requirements.MCPServers) != 2 || len(requirements.Plugins) != 3 {
		t.Fatalf("requirements = %#v", requirements)
	}
	if !requirements.MCPServers["docs"].Matches("", nil, "https://docs.example/mcp") {
		t.Fatal("exact URL requirement did not match")
	}
	if !requirements.MCPServers["shell"].Matches("company-cli", []string{"approved", "tenant-one", "alpha-42"}, "") {
		t.Fatal("command matcher requirement did not match")
	}
	if requirements.MCPServers["shell"].Matches("company-cli", []string{"approved", "tenant-one", "prefix-alpha-42-suffix"}, "") {
		t.Fatal("regex matcher was not full-value anchored")
	}
	if requirements.Plugins["no-allowlist@test"].MCPServers != nil {
		t.Fatal("absent plugin allowlist became configured")
	}
	if allowlist := requirements.Plugins["empty@test"].MCPServers; allowlist == nil || len(*allowlist) != 0 {
		t.Fatalf("explicit empty plugin allowlist = %#v", allowlist)
	}
}

func TestLoadRequirementsFileParsesInAppBrowserImportPolicyLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requirements.toml")
	body := `
[in_app_browser]
allow_external_browser_settings_import = true
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile requirements error = %v", err)
	}
	requirements, err := LoadRequirementsFile(path)
	if err != nil {
		t.Fatalf("LoadRequirementsFile() error = %v", err)
	}
	if requirements == nil || requirements.InAppBrowser == nil ||
		requirements.InAppBrowser.AllowExternalBrowserSettingsImport == nil ||
		!*requirements.InAppBrowser.AllowExternalBrowserSettingsImport {
		t.Fatalf("in_app_browser requirements = %#v", requirements)
	}
	encoded, err := json.Marshal(requirements)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if !strings.Contains(string(encoded), `"inAppBrowser":{"allowExternalBrowserSettingsImport":true}`) {
		t.Fatalf("requirements JSON missing inAppBrowser policy: %s", encoded)
	}
}

func TestLoadRequirementsFileRejectsInvalidMCPMatcher(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requirements.toml")
	body := `[mcp_servers.docs.identity.url]
match = "regex"
expression = "["
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile requirements error = %v", err)
	}
	if _, err := LoadRequirementsFile(path); err == nil || !strings.Contains(err.Error(), "invalid regex") {
		t.Fatalf("LoadRequirementsFile() error = %v", err)
	}
}

func TestLoadRequirementsFileMissingReturnsNil(t *testing.T) {
	requirements, err := LoadRequirementsFile(filepath.Join(t.TempDir(), "requirements.toml"))
	if err != nil {
		t.Fatalf("LoadRequirementsFile missing returned error: %v", err)
	}
	if requirements != nil {
		t.Fatalf("requirements = %#v, want nil", requirements)
	}
}

func TestLoadRequirementsFileRejectsMixedCanonicalAndLegacyNetworkShapesLikeRust(t *testing.T) {
	tests := []struct {
		name string
		body string
		want string
	}{
		{
			name: "domains",
			body: `[experimental_network]
allowed_domains = ["api.example.com"]

[experimental_network.domains]
"*.openai.com" = "allow"
`,
			want: "`experimental_network.domains` cannot be combined",
		},
		{
			name: "unix sockets",
			body: `[experimental_network]
allow_unix_sockets = ["/tmp/example.sock"]

[experimental_network.unix_sockets]
"/tmp/another.sock" = "allow"
`,
			want: "`experimental_network.unix_sockets` cannot be combined",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "requirements.toml")
			if err := os.WriteFile(path, []byte(test.body), 0o600); err != nil {
				t.Fatal(err)
			}
			_, err := LoadRequirementsFile(path)
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("LoadRequirementsFile() error = %v, want %q", err, test.want)
			}
		})
	}
}

func TestLoadRequirementsFileRejectsInvalidNetworkValuesLikeRust(t *testing.T) {
	tests := []string{
		`[experimental_network]\nenabled = "true"`,
		`[experimental_network]\nhttp_port = 70000`,
		`[experimental_network.domains]\n"api.example.com" = "ALLOW"`,
		`[experimental_network.unix_sockets]\n"/tmp/example.sock" = 1`,
	}
	for _, body := range tests {
		path := filepath.Join(t.TempDir(), "requirements.toml")
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadRequirementsFile(path); err == nil {
			t.Fatalf("LoadRequirementsFile() accepted invalid network requirements: %s", body)
		}
	}
}

func TestLoadRequirementsFileNormalizesLegacyNetworkListsLikeRust(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requirements.toml")
	body := `[experimental_network]
allowed_domains = ["api.example.com", "same.example.com"]
denied_domains = ["blocked.example.com", "same.example.com"]
allow_unix_sockets = ["/tmp/example.sock"]
`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	requirements, err := LoadRequirementsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if requirements == nil || requirements.Network == nil {
		t.Fatalf("requirements = %#v", requirements)
	}
	network := requirements.Network
	if network.Domains["api.example.com"] != NetworkAllow || network.Domains["blocked.example.com"] != NetworkDeny || network.Domains["same.example.com"] != NetworkDeny {
		t.Fatalf("domains = %#v", network.Domains)
	}
	if network.UnixSockets["/tmp/example.sock"] != NetworkAllow {
		t.Fatalf("unix sockets = %#v", network.UnixSockets)
	}
	if network.AllowedDomains != nil || network.DeniedDomains != nil || network.AllowUnixSockets != nil {
		t.Fatalf("legacy fields were not normalized: %#v", network)
	}
}

func TestLoadRequirementsFileIgnoresNonRustNetworkTable(t *testing.T) {
	path := filepath.Join(t.TempDir(), "requirements.toml")
	if err := os.WriteFile(path, []byte("[network]\nenabled = true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	requirements, err := LoadRequirementsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if requirements != nil && requirements.Network != nil {
		t.Fatalf("network = %#v, want Rust-compatible ignored table", requirements.Network)
	}
}

func TestApplicationNetworkRequirementsParseAndValidate(t *testing.T) {
	requirements, err := ParseRequirementsTOML([]byte("[application.network]\nenabled = true\ndomains = { \"Example.COM.\" = \"allow\", \"other.example\" = \"deny\" }\n"))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML error = %v", err)
	}
	if requirements == nil || requirements.Application == nil || requirements.Application.Network == nil {
		t.Fatalf("application.network = %#v", requirements)
	}
	network := requirements.Application.Network
	if !network.Enabled {
		t.Fatalf("enabled = false, want true")
	}
	if network.Domains["example.com"] != NetworkAllow || network.Domains["other.example"] != NetworkDeny {
		t.Fatalf("domains = %#v", network.Domains)
	}

	for _, invalid := range []string{
		"[application.network]\ndomains = { \"*.example.com\" = \"allow\" }\n",
		"[application.network]\ndomains = { \"https://example.com\" = \"allow\" }\n",
		"[application.network]\ndomains = { \"Example.COM\" = \"allow\", \"example.com.\" = \"deny\" }\n",
	} {
		if _, err := ParseRequirementsTOML([]byte(invalid)); err == nil {
			t.Fatalf("invalid application.network should fail: %s", invalid)
		}
	}

	encoded, err := json.Marshal(requirements)
	if err != nil {
		t.Fatalf("marshal requirements: %v", err)
	}
	if !strings.Contains(string(encoded), `"application":{"network":{"enabled":true,"domains":{"example.com":"allow","other.example":"deny"}}}`) {
		t.Fatalf("application JSON = %s", encoded)
	}

	empty, err := ParseRequirementsTOML([]byte("[application]\n"))
	if err != nil {
		t.Fatalf("empty application parse error = %v", err)
	}
	if empty != nil {
		t.Fatalf("empty application section should yield nil requirements, got %#v", empty)
	}
}

func TestBrowserUseAllowWebmcpRequirement(t *testing.T) {
	requirements, err := ParseRequirementsTOML([]byte("[browser_use]\nallow_webmcp = false\n"))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML error = %v", err)
	}
	if requirements == nil || requirements.BrowserUse == nil || requirements.BrowserUse.AllowWebmcp == nil || *requirements.BrowserUse.AllowWebmcp {
		t.Fatalf("allowWebmcp = %#v", requirements)
	}

	absent, err := ParseRequirementsTOML([]byte("[browser_use]\ndisable_auto_review = true\n"))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML error = %v", err)
	}
	if absent == nil || absent.BrowserUse == nil || absent.BrowserUse.AllowWebmcp != nil {
		t.Fatalf("absent allowWebmcp = %#v", absent)
	}
	encoded, err := json.Marshal(absent)
	if err != nil {
		t.Fatalf("marshal browser_use requirements: %v", err)
	}
	if !strings.Contains(string(encoded), `"browserUse":{"allowWebmcp":null`) {
		t.Fatalf("allowWebmcp should serialize as null: %s", encoded)
	}
}

func TestNetworkHeaderInjectionsParse(t *testing.T) {
	requirements, err := ParseRequirementsTOML([]byte(`
[experimental_network]
enabled = true

[[experimental_network.header_injections]]
host = "api.example.com"
methods = ["POST"]
path_prefixes = ["/console/v1"]

[experimental_network.header_injections.headers]
"x-statsig-change-source" = "codex"
`))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML error = %v", err)
	}
	if requirements == nil || requirements.Network == nil || len(requirements.Network.HeaderInjections) != 1 {
		t.Fatalf("headerInjections = %#v", requirements)
	}
	injection := requirements.Network.HeaderInjections[0]
	if injection.Host != "api.example.com" || len(injection.Methods) != 1 || injection.Methods[0] != "POST" || len(injection.PathPrefixes) != 1 || injection.PathPrefixes[0] != "/console/v1" {
		t.Fatalf("injection = %#v", injection)
	}
	if injection.Headers["x-statsig-change-source"] != "codex" {
		t.Fatalf("headers = %#v", injection.Headers)
	}
}
