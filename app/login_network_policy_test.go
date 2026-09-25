package app

import (
	"errors"
	"testing"

	"codex_go/cli"
	"codex_go/config"
	"codex_go/network"
)

// TestChatGPTLoginOAuthOptionsBindTheLocalPolicyLikeRust covers Rust #47411's
// bootstrap-auth binding: the CLI's login clients carry the application policy
// composed from the loaded (pre-cloud) requirements, so login traffic is limited
// by the rules administrators set locally, and an unrestricted policy leaves the
// caller's client shared.
func TestChatGPTLoginOAuthOptionsBindTheLocalPolicyLikeRust(t *testing.T) {
	restricted := &config.Config{Requirements: &config.ConfigRequirements{Application: &config.ApplicationRequirements{
		Network: &config.ApplicationNetworkRequirements{
			Enabled: true,
			Domains: map[string]config.NetworkPermission{"127.0.0.1": config.NetworkAllow},
		},
	}}}
	options := chatGPTLoginOAuthOptions(t.TempDir(), restricted, cli.LoginOptions{IssuerBaseURL: "https://login.example"}, nil)
	if options.HTTPClient == nil {
		t.Fatal("login options have no HTTP client")
	}
	if _, ok := options.HTTPClient.Transport.(*network.PolicyRoundTripper); !ok {
		t.Fatalf("login client transport = %#v, want the policy round tripper", options.HTTPClient.Transport)
	}
	// The local allow-list lets the login flow reach its own destination (the
	// connection then fails for transport reasons, not for policy) and denies
	// anything else before connecting.
	if _, err := options.HTTPClient.Get("https://127.0.0.1:1/token"); err == nil || network.IsPolicyError(err) {
		t.Fatalf("allowed login destination error = %v, want a transport failure", err)
	}
	if _, err := options.HTTPClient.Get("https://denied.example/token"); !errors.Is(err, network.ErrNetworkPolicyDestination) {
		t.Fatalf("denied login destination error = %v, want the destination denial", err)
	}

	plain := chatGPTLoginOAuthOptions(t.TempDir(), &config.Config{}, cli.LoginOptions{}, nil)
	if _, ok := plain.HTTPClient.Transport.(*network.PolicyRoundTripper); ok {
		t.Fatal("an unrestricted login client was wrapped")
	}
}
