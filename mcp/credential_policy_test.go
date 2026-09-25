package mcp

import (
	"strings"
	"testing"
)

// Mirrors Rust #48143: a registration that does not set a policy is host-owned
// configuration, and only an explicit executor policy changes the authority.
func TestEffectiveCredentialPolicyDefaultsToHostFallback(t *testing.T) {
	if got := EffectiveCredentialPolicy(""); got != CredentialPolicyHostFallbackAllowed {
		t.Fatalf("unset policy = %q", got)
	}
	if got := EffectiveCredentialPolicy(CredentialPolicyHostFallbackAllowed); got != CredentialPolicyHostFallbackAllowed {
		t.Fatalf("host policy = %q", got)
	}
	if got := EffectiveCredentialPolicy(CredentialPolicyExecutorOnly); got != CredentialPolicyExecutorOnly {
		t.Fatalf("executor policy = %q", got)
	}
}

// Mirrors Rust's McpServerRegistration::from_selected_plugin: HTTP credential
// authority comes from the plugin's selected root, not its execution
// environment.
func TestSelectedPluginCredentialPolicyMatchesRust(t *testing.T) {
	httpConfig := ServerConfig{URL: "https://example.test/mcp", Enabled: true}
	stdioConfig := ServerConfig{Command: "mcp-server", Enabled: true}
	for _, testCase := range []struct {
		name        string
		environment string
		config      ServerConfig
		want        CredentialPolicy
	}{
		{"default environment keeps host fallback", DefaultMCPServerEnvironmentID, httpConfig, CredentialPolicyHostFallbackAllowed},
		// Rust compares against the default environment id, so an unset id is a
		// non-default (and therefore executor-only) source for an HTTP plugin.
		{"unset environment is executor only for HTTP", "", httpConfig, CredentialPolicyExecutorOnly},
		{"non-default environment with HTTP is executor only", "sandbox-1", httpConfig, CredentialPolicyExecutorOnly},
		{"non-default environment with stdio keeps host fallback", "sandbox-1", stdioConfig, CredentialPolicyHostFallbackAllowed},
		{"non-default environment without a transport keeps host fallback", "sandbox-1", ServerConfig{Enabled: true}, CredentialPolicyHostFallbackAllowed},
	} {
		if got := SelectedPluginCredentialPolicy(testCase.environment, testCase.config); got != testCase.want {
			t.Fatalf("%s: policy = %q, want %q", testCase.name, got, testCase.want)
		}
	}
}

// Mirrors Rust's make_rmcp_client gates with Rust's messages.
func TestValidateExecutorDiscoveryCredentialPolicyMatchesRust(t *testing.T) {
	hostConfig := ServerConfig{URL: "https://example.test/mcp", CredentialPolicy: CredentialPolicyHostFallbackAllowed}
	if err := ValidateExecutorDiscoveryCredentialPolicy("host-server", &hostConfig); err != nil {
		t.Fatalf("host configuration must keep host credential resolution: %v", err)
	}
	if err := ValidateExecutorDiscoveryCredentialPolicy("host-server", nil); err != nil {
		t.Fatalf("nil configuration error = %v", err)
	}

	clean := ServerConfig{URL: "https://example.test/mcp", EnvironmentID: "sandbox-1", CredentialPolicy: CredentialPolicyExecutorOnly}
	if err := ValidateExecutorDiscoveryCredentialPolicy("exec-server", &clean); err != nil {
		t.Fatalf("a remote HTTP executor declaration without host credentials must pass: %v", err)
	}

	transportMessage := "requires a remote HTTP transport without host environment headers or helpers"
	for _, testCase := range []struct {
		name   string
		config ServerConfig
	}{
		{"default environment", ServerConfig{URL: "https://example.test/mcp"}},
		{"explicitly local environment", ServerConfig{EnvironmentID: DefaultMCPServerEnvironmentID, URL: "https://example.test/mcp"}},
		{"stdio transport", ServerConfig{Command: "mcp-server"}},
		{"no transport", ServerConfig{}},
		{"host environment headers", ServerConfig{URL: "https://example.test/mcp", EnvironmentID: "sandbox-1", EnvHTTPHeaders: map[string]string{"Authorization": "MCP_TOKEN"}}},
		{"host headers helper", ServerConfig{URL: "https://example.test/mcp", EnvironmentID: "sandbox-1", HTTPHeadersHelper: "helper"}},
	} {
		testCase.config.CredentialPolicy = CredentialPolicyExecutorOnly
		err := ValidateExecutorDiscoveryCredentialPolicy("exec-server", &testCase.config)
		if err == nil || !strings.Contains(err.Error(), transportMessage) {
			t.Fatalf("%s: error = %v, want the transport message", testCase.name, err)
		}
	}

	bearer := ServerConfig{URL: "https://example.test/mcp", EnvironmentID: "sandbox-1", BearerTokenEnvVar: "MCP_TOKEN", CredentialPolicy: CredentialPolicyExecutorOnly}
	err := ValidateExecutorDiscoveryCredentialPolicy("exec-server", &bearer)
	if err == nil || !strings.Contains(err.Error(), "requires executor-side environment credential resolution") ||
		!strings.Contains(err.Error(), "host fallback is disabled") {
		t.Fatalf("bearer fallback error = %v", err)
	}
}

// The registration's policy survives into the effective server the runtime uses.
func TestMCPServiceCarriesRegistrationCredentialPolicy(t *testing.T) {
	service := NewMCPService(&RuntimeConfig{Servers: map[string]ServerRegistration{
		"exec-server": {
			Name:             "exec-server",
			Config:           ServerConfig{URL: "https://example.test/mcp", Enabled: true},
			Source:           "config",
			CredentialPolicy: CredentialPolicyExecutorOnly,
		},
		"host-server": {
			Name:   "host-server",
			Config: ServerConfig{URL: "https://example.test/mcp", Enabled: true},
			Source: "config",
		},
	}})
	if got := service.configs["exec-server"].CredentialPolicy; got != CredentialPolicyExecutorOnly {
		t.Fatalf("executor server policy = %q", got)
	}
	if got := service.configs["host-server"].CredentialPolicy; got != CredentialPolicyHostFallbackAllowed {
		t.Fatalf("host server policy = %q", got)
	}
}

// Mirrors Rust #48143's cache identity rules: the policy is part of the
// connection identity, and an executor-only identity never reads host
// environment values for its catalog identity.
func TestCredentialPolicyIsolatesCacheIdentities(t *testing.T) {
	hostConfig := ServerConfig{URL: "https://example.test/mcp", BearerTokenEnvVar: "MCP_TEST_TOKEN", Enabled: true, CredentialPolicy: CredentialPolicyHostFallbackAllowed}
	executorConfig := hostConfig
	executorConfig.CredentialPolicy = CredentialPolicyExecutorOnly
	if mcpConnectionCacheKey(&hostConfig, false) == mcpConnectionCacheKey(&executorConfig, false) {
		t.Fatal("the credential policy must be part of the connection identity")
	}

	t.Setenv("MCP_TEST_TOKEN", "first")
	hostKey, hostEligible := mcpToolCatalogGraceKey("server", &hostConfig, false, false, false)
	executorKey, executorEligible := mcpToolCatalogGraceKey("server", &executorConfig, false, false, false)
	if !hostEligible || !executorEligible {
		t.Fatalf("catalog grace eligibility = %v / %v", hostEligible, executorEligible)
	}
	t.Setenv("MCP_TEST_TOKEN", "second")
	hostKeyAfter, _ := mcpToolCatalogGraceKey("server", &hostConfig, false, false, false)
	executorKeyAfter, _ := mcpToolCatalogGraceKey("server", &executorConfig, false, false, false)
	if hostKey == hostKeyAfter {
		t.Fatal("a host-fallback catalog identity must follow the host environment value")
	}
	if executorKey != executorKeyAfter {
		t.Fatal("an executor-only catalog identity must not read host environment values")
	}
	if hostKey == executorKey {
		t.Fatal("the two policies must not share a catalog identity")
	}
}

// The rejection happens before any host value is read or a connection made.
func TestListInventoryRejectsExecutorDiscoveryBeforeConnecting(t *testing.T) {
	service := NewMCPService(&RuntimeConfig{})
	config := &ServerConfig{
		URL:              "https://example.test/mcp",
		EnvironmentID:    "sandbox-1",
		EnvHTTPHeaders:   map[string]string{"Authorization": "MCP_TOKEN"},
		Enabled:          true,
		CredentialPolicy: CredentialPolicyExecutorOnly,
	}
	_, err := service.listInventoryForConfig("exec-server", config, "")
	if err == nil || !strings.Contains(err.Error(), "requires a remote HTTP transport without host environment headers or helpers") {
		t.Fatalf("inventory error = %v", err)
	}
}
