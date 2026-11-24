package app

import (
	"testing"

	"codex_go/config"
)

func TestConfigServerOAuthSessionCarriesCredentialsStoreLikeRust(t *testing.T) {
	store := "file"
	session := configServerOAuthSessionConfig(&config.ConfigServerOAuth{
		AuthorizationURL: "https://gateway.example.com/authorize",
		TokenURL:         "https://gateway.example.com/token",
		ClientID:         "acme-client",
		CredentialsStore: &store,
	})
	if session.CredentialsStore != "file" {
		t.Fatalf("session credentials store = %q, want file", session.CredentialsStore)
	}

	session = configServerOAuthSessionConfig(&config.ConfigServerOAuth{
		AuthorizationURL: "https://gateway.example.com/authorize",
		TokenURL:         "https://gateway.example.com/token",
		ClientID:         "acme-client",
	})
	if session.CredentialsStore != "" {
		t.Fatalf("default session credentials store = %q, want the keyring default", session.CredentialsStore)
	}
}
