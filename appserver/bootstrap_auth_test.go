package appserver

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"

	"codex_go/config"
	"codex_go/network"
	"codex_go/session"
)

// TestAccountOAuthOptionsBindTheLocalPolicyLikeRust covers Rust #47411's
// bootstrap-auth binding for the app-server: login runs before the effective
// policy exists, so the client is limited by the requirements the home's own file
// supplied - not by the effective requirements a host installs afterwards, which
// may depend on a cloud fetch that has not happened yet.
func TestAccountOAuthOptionsBindTheLocalPolicyLikeRust(t *testing.T) {
	home := t.TempDir()
	local := "[application.network]\nenabled = true\ndomains = { \"127.0.0.1\" = \"allow\" }\n"
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(local), 0o600); err != nil {
		t.Fatalf("write requirements error = %v", err)
	}
	configService := config.NewConfigService(home)
	// The host's effective requirements differ from the local file: they allow a
	// host the local file denies, and deny the one it allows.
	configService.SetRequirements(&config.ConfigRequirements{Application: &config.ApplicationRequirements{
		Network: &config.ApplicationNetworkRequirements{
			Enabled: true,
			Domains: map[string]config.NetworkPermission{"effective.example": config.NetworkAllow},
		},
	}})
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(t.TempDir())),
		Config:       configService,
	})
	t.Cleanup(func() { _ = router.Close() })

	policy, composed := router.refreshLocalApplicationNetworkPolicy()
	_ = policy
	if !composed.IsRestricted() || len(composed.AllowedHosts()) != 1 || composed.AllowedHosts()[0] != "127.0.0.1" {
		t.Fatalf("local policy = %#v/%#v", composed.AllowedHosts(), composed)
	}
	options := router.accountOAuthOptions()
	if options.HTTPClient == nil {
		t.Fatal("account login options have no HTTP client")
	}
	if _, ok := options.HTTPClient.Transport.(*network.PolicyRoundTripper); !ok {
		t.Fatalf("login client transport = %#v, want the policy round tripper", options.HTTPClient.Transport)
	}
	// A host the effective (only) requirements allow is still denied: the login
	// client carries the local policy.
	if _, err := options.HTTPClient.Get("https://effective.example/token"); !errors.Is(err, network.ErrNetworkPolicyDestination) {
		t.Fatalf("effective-only destination error = %v, want the destination denial", err)
	}
	// A host the local file allows reaches the transport instead of being denied.
	if _, err := options.HTTPClient.Get("https://127.0.0.1:1/token"); err == nil || network.IsPolicyError(err) {
		t.Fatalf("locally allowed destination error = %v, want a transport failure", err)
	}
}

// A home without local application requirements leaves the login client shared,
// so a host that enforces nothing locally keeps its existing clients.
func TestAccountOAuthOptionsLeaveClientsSharedWithoutLocalRequirements(t *testing.T) {
	configService := config.NewConfigService(t.TempDir())
	router := NewRuntimeRouter(RuntimeServices{
		ThreadRouter: NewRouter(session.NewStore(t.TempDir())),
		Config:       configService,
		HTTPClient:   &http.Client{},
	})
	t.Cleanup(func() { _ = router.Close() })
	options := router.accountOAuthOptions()
	if options.HTTPClient == nil {
		t.Fatal("account login options have no HTTP client")
	}
	if _, ok := options.HTTPClient.Transport.(*network.PolicyRoundTripper); ok {
		t.Fatal("a login client was wrapped without local requirements")
	}
	if _, composed := router.refreshLocalApplicationNetworkPolicy(); composed.IsRestricted() {
		t.Fatalf("local policy = %#v, want unrestricted", composed)
	}
}
