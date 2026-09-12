package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/envutil"
)

// TestCreateEnvForMCPServerHonorsOverridesLikeRust mirrors Rust
// utils.rs::create_env_honors_overrides: explicit env overrides win and
// launch-context variables are dropped case-insensitively.
func TestCreateEnvForMCPServerHonorsOverridesLikeRust(t *testing.T) {
	env, err := createEnvForMCPServer(map[string]string{
		"TZ":                         "custom",
		"openai_identity_token_file": "/run/identity-token",
	}, nil)
	if err != nil {
		t.Fatalf("createEnvForMCPServer() error = %v", err)
	}
	if env["TZ"] != "custom" {
		t.Fatalf("TZ = %q, want custom", env["TZ"])
	}
	if _, ok := env["openai_identity_token_file"]; ok {
		t.Fatal("launch-context variable must not reach the server")
	}
}

// TestCreateEnvForMCPServerIncludesListedVariablesLikeRust mirrors Rust
// utils.rs::create_env_includes_additional_whitelisted_variables.
func TestCreateEnvForMCPServerIncludesListedVariablesLikeRust(t *testing.T) {
	const customVar = "EXTRA_RMCP_ENV"
	t.Setenv(customVar, "from-env")
	env, err := createEnvForMCPServer(nil, []EnvVar{{Name: customVar}})
	if err != nil {
		t.Fatalf("createEnvForMCPServer() error = %v", err)
	}
	if env[customVar] != "from-env" {
		t.Fatalf("%s = %q, want from-env", customVar, env[customVar])
	}
}

// TestCreateEnvForMCPServerRejectsRemoteSourceLikeRust mirrors Rust
// utils.rs::create_local_env_rejects_remote_source_variables.
func TestCreateEnvForMCPServerRejectsRemoteSourceLikeRust(t *testing.T) {
	_, err := createEnvForMCPServer(nil, []EnvVar{{Name: "REMOTE", Source: "remote"}})
	if err == nil || !strings.Contains(err.Error(), "source `remote`") {
		t.Fatalf("error = %v, want a remote-source rejection", err)
	}
	if _, err := createEnvForMCPServer(nil, []EnvVar{{Name: "WEIRD", Source: "weird"}}); err == nil {
		t.Fatal("unsupported env_vars source must be rejected")
	}
	if _, err := createEnvForMCPServer(nil, []EnvVar{{Name: "LOCAL", Source: "local"}}); err != nil {
		t.Fatalf("local source must be accepted: %v", err)
	}
}

// TestCreateEnvForMCPServerClearsUnlistedVariables pins the behavior change
// behind this port: Rust spawns the server with a cleared environment, so an
// unlisted parent variable must not leak and a listed default must survive.
func TestCreateEnvForMCPServerClearsUnlistedVariables(t *testing.T) {
	const unlisted = "MCP_ENV_AUDIT_UNLISTED"
	t.Setenv(unlisted, "leaked")
	env, err := createEnvForMCPServer(nil, nil)
	if err != nil {
		t.Fatalf("createEnvForMCPServer() error = %v", err)
	}
	if _, ok := env[unlisted]; ok {
		t.Fatalf("unlisted variable reached the server: %#v", env)
	}
	if path, ok := os.LookupEnv("PATH"); ok {
		if env["PATH"] != path {
			t.Fatalf("PATH = %q, want the parent value %q", env["PATH"], path)
		}
	}
	for name := range env {
		if !mcpIsDefaultEnvVar(name) && !mcpIsCustomCAEnvKey(name) {
			t.Fatalf("unexpected variable in the cleared environment: %q", name)
		}
	}
}

// TestCreateEnvForMCPServerSkipsNonInheritableListedVariables covers Rust's
// non-inheritable filter inside local_stdio_env_var_names.
func TestCreateEnvForMCPServerSkipsNonInheritableListedVariables(t *testing.T) {
	const restricted = "OPENAI_IDENTITY_TOKEN_FILE"
	t.Setenv(restricted, "/run/identity-token")
	env, err := createEnvForMCPServer(nil, []EnvVar{{Name: restricted, Source: "local"}})
	if err != nil {
		t.Fatalf("createEnvForMCPServer() error = %v", err)
	}
	if _, ok := env[restricted]; ok {
		t.Fatal("non-inheritable variable must not be inherited even when listed")
	}
	if !envutil.IsNonInheritableEnvVar(restricted) {
		t.Fatalf("%s must be classified non-inheritable", restricted)
	}
}

// TestCreateEnvForMCPServerForwardsAbsoluteCustomCALikeRust covers the CA half
// of Rust create_env_for_mcp_server.
func TestCreateEnvForMCPServerForwardsAbsoluteCustomCALikeRust(t *testing.T) {
	relative := filepath.Join("certs", "bundle.pem")
	t.Setenv("SSL_CERT_FILE", relative)
	env, err := createEnvForMCPServer(nil, nil)
	if err != nil {
		t.Fatalf("createEnvForMCPServer() error = %v", err)
	}
	value, ok := env["SSL_CERT_FILE"]
	if !ok {
		t.Fatalf("SSL_CERT_FILE missing from %#v", env)
	}
	if !filepath.IsAbs(value) {
		t.Fatalf("SSL_CERT_FILE = %q, want an absolute path", value)
	}
}

// mcpIsDefaultEnvVar reports whether name is in the platform default list.
func mcpIsDefaultEnvVar(name string) bool {
	for _, candidate := range mcpStdioDefaultEnvVars() {
		if strings.EqualFold(candidate, name) {
			return true
		}
	}
	return false
}
