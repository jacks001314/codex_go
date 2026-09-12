package config

import (
	"os"
	"strings"
	"testing"
)

// TestValidateMCPOAuthCredentialsStoreModeLikeRust covers Rust's enum
// deserialization: only auto/file/keyring are accepted.
func TestValidateMCPOAuthCredentialsStoreModeLikeRust(t *testing.T) {
	// Go trims and lowercases enum values (its config layer convention);
	// unrecognized values still fail like Rust's serde enum.
	for _, valid := range []any{nil, "auto", "file", "keyring", " FILE ", "keyring "} {
		values := map[string]any{}
		if valid != nil {
			values["mcp_oauth_credentials_store"] = valid
		}
		if err := ValidateMCPOAuthCredentialsStoreMode(values); err != nil {
			t.Fatalf("ValidateMCPOAuthCredentialsStoreMode(%#v) error = %v", values, err)
		}
	}
	for _, invalid := range []any{"secrets", "nope", 3, true} {
		if err := ValidateMCPOAuthCredentialsStoreMode(map[string]any{"mcp_oauth_credentials_store": invalid}); err == nil {
			t.Fatalf("ValidateMCPOAuthCredentialsStoreMode(%#v) = nil, want an error", invalid)
		}
	}
	if err := ValidateMCPOAuthCredentialsStoreMode(nil); err != nil {
		t.Fatalf("nil values error = %v", err)
	}
}

// TestMCPOAuthCredentialsStoreModeAccessorLikeRust covers the resolved mode.
func TestMCPOAuthCredentialsStoreModeAccessorLikeRust(t *testing.T) {
	tests := []struct {
		values map[string]any
		want   string
	}{
		{values: nil, want: "auto"},
		{values: map[string]any{}, want: "auto"},
		{values: map[string]any{"mcp_oauth_credentials_store": "file"}, want: "file"},
		{values: map[string]any{"mcp_oauth_credentials_store": "KEYRING"}, want: "keyring"},
		{values: map[string]any{"mcp_oauth_credentials_store": "auto"}, want: "auto"},
	}
	for _, testCase := range tests {
		cfg := &Config{Values: testCase.values}
		if got := cfg.MCPOAuthCredentialsStoreMode(); got != testCase.want {
			t.Fatalf("MCPOAuthCredentialsStoreMode(%#v) = %q, want %q", testCase.values, got, testCase.want)
		}
	}
	var nilConfig *Config
	if got := nilConfig.MCPOAuthCredentialsStoreMode(); got != "auto" {
		t.Fatalf("nil config = %q, want auto", got)
	}
}

// TestLoadRejectsInvalidMCPOAuthCredentialsStoreModeLikeRust pins that an
// unrecognized value fails the effective config load, like Rust's enum parse.
func TestLoadRejectsInvalidMCPOAuthCredentialsStoreModeLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(ConfigPath(home), []byte("mcp_oauth_credentials_store = \"secrets\"\n"), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	_, err := LoadEffectiveWithOptions(home, nil)
	if err == nil || !strings.Contains(err.Error(), "invalid mcp_oauth_credentials_store") {
		t.Fatalf("error = %v, want an invalid-mode rejection", err)
	}

	if err := os.WriteFile(ConfigPath(home), []byte("mcp_oauth_credentials_store = \"file\"\n"), 0o600); err != nil {
		t.Fatalf("write config error = %v", err)
	}
	cfg, err := LoadEffectiveWithOptions(home, nil)
	if err != nil {
		t.Fatalf("LoadEffectiveWithOptions() error = %v", err)
	}
	if got := cfg.MCPOAuthCredentialsStoreMode(); got != "file" {
		t.Fatalf("mode = %q, want file", got)
	}
}
