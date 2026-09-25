package config

import (
	"errors"
	"net/url"
	"testing"

	"codex_go/network"
)

func configTestURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q) error = %v", raw, err)
	}
	return parsed
}

// Rust parity: Config::application_network_policy carries the published policy,
// and EmbeddedNetworkPolicy::activate publishes the requirements into it.
func TestConfigApplicationNetworkPolicyCarrierLikeRust(t *testing.T) {
	// An unbound config reports an unmanaged policy.
	unbound := &Config{Values: map[string]any{}}
	if unbound.NetworkPolicy().IsScoped() {
		t.Fatal("an unbound config reports a policy scope")
	}

	// A host with no managed requirements publishes an unrestricted policy.
	if controller := unbound.BindApplicationNetworkPolicy(); controller == nil {
		t.Fatal("BindApplicationNetworkPolicy() returned no controller")
	}
	policy := unbound.NetworkPolicy()
	if !policy.IsScoped() {
		t.Fatal("a bound config reports no policy scope")
	}
	for _, raw := range []string{"https://example.com/path", "https://other.example/path"} {
		if _, err := policy.Acquire(configTestURL(t, raw)); err != nil {
			t.Fatalf("Acquire(%s) error = %v", raw, err)
		}
	}

	// An enabled requirement restricts the bound policy to its allowed domains.
	restricted := &Config{Requirements: &ConfigRequirements{Application: &ApplicationRequirements{
		Network: &ApplicationNetworkRequirements{
			Enabled: true,
			Domains: map[string]NetworkPermission{
				"allowed.example": NetworkAllow,
				"denied.example":  NetworkDeny,
			},
		},
	}}}
	restricted.BindApplicationNetworkPolicy()
	bounded := restricted.NetworkPolicy()
	if _, err := bounded.Acquire(configTestURL(t, "https://allowed.example/path")); err != nil {
		t.Fatalf("allowed domain error = %v", err)
	}
	if _, err := bounded.Acquire(configTestURL(t, "https://denied.example/path")); !errors.Is(err, network.ErrNetworkPolicyDestination) {
		t.Fatalf("denied domain error = %v", err)
	}
	if _, err := bounded.Acquire(configTestURL(t, "https://unlisted.example/path")); !errors.Is(err, network.ErrNetworkPolicyDestination) {
		t.Fatalf("unlisted domain error = %v", err)
	}
}
