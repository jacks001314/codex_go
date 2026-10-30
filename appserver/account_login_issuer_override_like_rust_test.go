package appserver

import (
	"testing"

	"codex_go/auth"
)

// TestAccountOAuthOptionsHonorPackagedLoginIssuerOverrideLikeRust covers Rust
// #42811: CODEX_APP_SERVER_LOGIN_ISSUER applies to packaged builds, not only
// debug builds, while blank values leave the configured issuer alone.
func TestAccountOAuthOptionsHonorPackagedLoginIssuerOverrideLikeRust(t *testing.T) {
	t.Setenv(auth.LoginIssuerEnvOverride, "https://staging-auth.example")
	router := NewRuntimeRouter(RuntimeServices{DefaultCWD: t.TempDir()})
	t.Cleanup(func() { _ = router.Close() })
	if got := router.accountOAuthOptions().Issuer; got != "https://staging-auth.example" {
		t.Fatalf("account OAuth issuer = %q, want the staging override", got)
	}

	router = NewRuntimeRouter(RuntimeServices{
		DefaultCWD:          t.TempDir(),
		AccountOAuthOptions: &auth.OAuthOptions{Issuer: "https://auth.openai.com", ClientID: "client"},
	})
	t.Cleanup(func() { _ = router.Close() })
	options := router.accountOAuthOptions()
	if options.Issuer != "https://staging-auth.example" || options.ClientID != "client" {
		t.Fatalf("caller-supplied options = issuer %q client %q", options.Issuer, options.ClientID)
	}

	t.Setenv(auth.LoginIssuerEnvOverride, "   ")
	router = NewRuntimeRouter(RuntimeServices{
		DefaultCWD:          t.TempDir(),
		AccountOAuthOptions: &auth.OAuthOptions{Issuer: "https://auth.openai.com"},
	})
	t.Cleanup(func() { _ = router.Close() })
	if got := router.accountOAuthOptions().Issuer; got != "https://auth.openai.com" {
		t.Fatalf("blank override changed the issuer to %q", got)
	}
}
