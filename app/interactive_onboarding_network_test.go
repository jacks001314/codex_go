package app

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/cli"
	"codex_go/network"
)

// TestInteractiveAuthOnboardingBindsTheLocalPolicyLikeRust covers Rust #47411's
// TUI half: the embedded login flow carries the policy composed from the locally
// loaded application requirements, so a host that restricts application traffic
// limits its login requests too.
func TestInteractiveAuthOnboardingBindsTheLocalPolicyLikeRust(t *testing.T) {
	clearInteractiveAuthEnvironment(t)
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	requirements := "[application.network]\nenabled = true\ndomains = { \"127.0.0.1\" = \"allow\" }\n"
	if err := os.WriteFile(filepath.Join(home, "requirements.toml"), []byte(requirements), 0o600); err != nil {
		t.Fatalf("write requirements error = %v", err)
	}
	options, show, err := interactiveAuthOnboardingOptions(&cli.RootOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !show || options == nil {
		t.Fatalf("onboarding options=%#v show=%v", options, show)
	}
	if options.HTTPClient == nil {
		t.Fatal("onboarding options have no HTTP client")
	}
	if _, ok := options.HTTPClient.Transport.(*network.PolicyRoundTripper); !ok {
		t.Fatalf("onboarding client transport = %#v, want the policy round tripper", options.HTTPClient.Transport)
	}
	if _, err := options.HTTPClient.Get("https://denied.example/token"); !network.IsPolicyError(err) {
		t.Fatalf("denied login destination error = %v, want a policy denial", err)
	}

	// A home without local application requirements keeps the plain client, so
	// nothing changes for hosts that enforce nothing locally.
	plainHome := t.TempDir()
	t.Setenv("CODEX_HOME", plainHome)
	plain, show, err := interactiveAuthOnboardingOptions(&cli.RootOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !show || plain == nil || plain.HTTPClient == nil {
		t.Fatalf("plain onboarding options=%#v show=%v", plain, show)
	}
	if _, ok := plain.HTTPClient.Transport.(*network.PolicyRoundTripper); ok {
		t.Fatal("an unrestricted onboarding client was wrapped")
	}
}
