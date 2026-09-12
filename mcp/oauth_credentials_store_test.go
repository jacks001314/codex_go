package mcp

import (
	"strings"
	"testing"
)

// TestParseOAuthCredentialsStoreModeLikeRust covers Rust's
// OAuthCredentialsStoreMode enum values (auto/file/keyring).
func TestParseOAuthCredentialsStoreModeLikeRust(t *testing.T) {
	tests := []struct {
		input string
		want  OAuthCredentialsStoreMode
		valid bool
	}{
		{input: "", want: OAuthCredentialsStoreAuto, valid: true},
		{input: "auto", want: OAuthCredentialsStoreAuto, valid: true},
		{input: " AUTO ", want: OAuthCredentialsStoreAuto, valid: true},
		{input: "file", want: OAuthCredentialsStoreFile, valid: true},
		{input: "File", want: OAuthCredentialsStoreFile, valid: true},
		{input: "keyring", want: OAuthCredentialsStoreKeyring, valid: true},
		{input: "secrets", valid: false},
		{input: "nope", valid: false},
	}
	for _, testCase := range tests {
		t.Run(testCase.input, func(t *testing.T) {
			got, valid := ParseOAuthCredentialsStoreMode(testCase.input)
			if valid != testCase.valid {
				t.Fatalf("ParseOAuthCredentialsStoreMode(%q) valid = %v, want %v", testCase.input, valid, testCase.valid)
			}
			if valid && got != testCase.want {
				t.Fatalf("ParseOAuthCredentialsStoreMode(%q) = %q, want %q", testCase.input, got, testCase.want)
			}
		})
	}
}

// TestOAuthStoreModesLikeRust covers the store resolution: auto falls back to
// the credentials file when no keyring backend is available (Rust's documented
// auto behavior), file always uses the file, and keyring fails like Rust does
// when the keyring cannot be used.
func TestOAuthStoreModesLikeRust(t *testing.T) {
	home := t.TempDir()
	tests := []struct {
		name          string
		mode          OAuthCredentialsStoreMode
		wantEffective OAuthCredentialsStoreMode
		wantErr       bool
	}{
		{name: "auto falls back to file", mode: OAuthCredentialsStoreAuto, wantEffective: OAuthCredentialsStoreFile},
		{name: "file", mode: OAuthCredentialsStoreFile, wantEffective: OAuthCredentialsStoreFile},
		{name: "keyring", mode: OAuthCredentialsStoreKeyring, wantEffective: OAuthCredentialsStoreKeyring, wantErr: true},
		{name: "invalid normalizes to auto", mode: OAuthCredentialsStoreMode("bogus"), wantEffective: OAuthCredentialsStoreFile},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			store := NewOAuthStoreWithMode(home, testCase.mode)
			if got := store.effectiveMode(); got != testCase.wantEffective {
				t.Fatalf("effectiveMode() = %q, want %q", got, testCase.wantEffective)
			}
			if got := store.KeyringUnavailable(); got != testCase.wantErr {
				t.Fatalf("KeyringUnavailable() = %v, want %v", got, testCase.wantErr)
			}
			tokens := &OAuthTokenSet{
				ServerName:  "docs",
				ServerURL:   "https://example.com/mcp",
				ClientID:    "client",
				AccessToken: "token",
			}
			err := store.Save(tokens)
			if testCase.wantErr {
				if err == nil || !strings.Contains(err.Error(), MCPOAuthKeyringUnavailableError) {
					t.Fatalf("Save() error = %v, want the keyring-unavailable error", err)
				}
				if _, err := store.Load("docs", "https://example.com/mcp"); err == nil {
					t.Fatal("Load() must fail while keyring storage is required")
				}
				return
			}
			if err != nil {
				t.Fatalf("Save() error = %v", err)
			}
			loaded, err := store.Load("docs", "https://example.com/mcp")
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if loaded == nil || loaded.AccessToken != "token" {
				t.Fatalf("loaded = %#v", loaded)
			}
		})
	}
}

// TestRuntimeConfigCarriesOAuthCredentialsStoreModeLikeRust covers the value
// flowing from config into the runtime and onto each server config.
func TestRuntimeConfigCarriesOAuthCredentialsStoreModeLikeRust(t *testing.T) {
	runtime := RuntimeConfigFromValuesWithAuthAndRequirements(map[string]any{
		"mcp_oauth_credentials_store": "file",
		"mcp_servers": map[string]any{
			"docs": map[string]any{"url": "https://example.com/mcp"},
		},
	}, t.TempDir(), nil, nil)
	if runtime.OAuthCredentialsStoreMode != OAuthCredentialsStoreFile {
		t.Fatalf("runtime mode = %q, want file", runtime.OAuthCredentialsStoreMode)
	}
	server, ok := runtime.Servers["docs"]
	if !ok || server.Config.OAuthCredentialsStoreMode != OAuthCredentialsStoreFile {
		t.Fatalf("server mode = %#v", server.Config.OAuthCredentialsStoreMode)
	}

	defaulted := RuntimeConfigFromValuesWithAuthAndRequirements(map[string]any{
		"mcp_servers": map[string]any{"docs": map[string]any{"url": "https://example.com/mcp"}},
	}, t.TempDir(), nil, nil)
	if defaulted.OAuthCredentialsStoreMode != OAuthCredentialsStoreAuto {
		t.Fatalf("default mode = %q, want auto", defaulted.OAuthCredentialsStoreMode)
	}
	if defaulted.Servers["docs"].Config.OAuthCredentialsStoreMode != OAuthCredentialsStoreAuto {
		t.Fatalf("default server mode = %q, want auto", defaulted.Servers["docs"].Config.OAuthCredentialsStoreMode)
	}
}
