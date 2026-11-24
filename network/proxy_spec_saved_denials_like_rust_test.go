package network

import (
	"reflect"
	"strings"
	"testing"
)

// managedAllowOnlySpec builds a proxy spec whose managed allow-list cannot
// expand, so a saved grant outside it violates the managed constraints.
func managedAllowOnlySpec(t *testing.T) *ProxySpec {
	t.Helper()
	settings := DefaultProxySettings()
	// ManagedAllowedDomainsOnly pins the allow-list while the managed sandbox
	// keeps saved denials expandable, the combination Rust #60046 covers.
	requirements := &ProxyRequirements{
		ManagedAllowedDomainsOnly: true,
		Domains: &ProxyDomainPermissions{Entries: []ProxyDomainPermissionEntry{
			{Pattern: "managed.example.com", Permission: ProxyDomainAllow},
			{Pattern: "managed-blocked.example.com", Permission: ProxyDomainDeny},
		}},
	}
	spec, err := NewProxySpec(ProxyConfig{Network: settings}, requirements, true)
	if err != nil {
		t.Fatalf("NewProxySpec() error = %v", err)
	}
	return spec
}

// TestWithNetworkRulesKeepsSavedDenialsWhenGrantViolatesConstraintsLikeRust
// mirrors Rust #60046: a stale saved grant must not invalidate the saved
// denials in the same batch.
func TestWithNetworkRulesKeepsSavedDenialsWhenGrantViolatesConstraintsLikeRust(t *testing.T) {
	spec := managedAllowOnlySpec(t)
	next, err := spec.WithNetworkRules([]ProxyNetworkRule{
		{Host: "stale.example.com", Permission: ProxyDomainAllow},
		{Host: "saved-blocked.example.com", Permission: ProxyDomainDeny},
	})
	if err != nil {
		t.Fatalf("WithNetworkRules() error = %v, want the denials kept", err)
	}
	got := next.Config().Network
	if !reflect.DeepEqual(got.AllowedDomains(), []string{"managed.example.com"}) {
		t.Fatalf("allowed domains = %v, want only the managed allow-list", got.AllowedDomains())
	}
	denied := got.DeniedDomains()
	for _, want := range []string{"managed-blocked.example.com", "saved-blocked.example.com"} {
		if !containsDomain(denied, want) {
			t.Fatalf("denied domains = %v, want %q", denied, want)
		}
	}
	if containsDomain(denied, "stale.example.com") {
		t.Fatalf("denied domains = %v, want no denial for the dropped grant", denied)
	}
	if containsDomain(spec.config.Network.DeniedDomains(), "saved-blocked.example.com") {
		t.Fatalf("WithNetworkRules mutated the receiver: %#v", spec.config.Network.Domains)
	}
}

// TestWithNetworkRulesStillRejectsUnfixableConstraintsLikeRust mirrors Rust
// #60046: when even the denials cannot satisfy the managed constraints the
// update still fails.
func TestWithNetworkRulesStillRejectsUnfixableConstraintsLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.SetDeniedDomains([]string{"extra.example.com"})
	fixedDenials := false
	// ManagedAllowedDomainsOnly pins the allow-list while the managed sandbox
	// keeps saved denials expandable, the combination Rust #60046 covers.
	requirements := &ProxyRequirements{
		ManagedAllowedDomainsOnly: true,
		Domains: &ProxyDomainPermissions{Entries: []ProxyDomainPermissionEntry{
			{Pattern: "managed.example.com", Permission: ProxyDomainAllow},
			{Pattern: "managed-blocked.example.com", Permission: ProxyDomainDeny},
		}},
	}
	spec, err := NewProxySpec(ProxyConfig{Network: settings}, requirements, true)
	if err != nil {
		t.Fatalf("NewProxySpec() error = %v", err)
	}
	spec.constraints.DenylistExpansionEnabled = &fixedDenials
	if _, err := spec.WithNetworkRules([]ProxyNetworkRule{
		{Host: "another.example.com", Permission: ProxyDomainDeny},
	}); err == nil || !strings.Contains(err.Error(), "denied_domains") {
		t.Fatalf("WithNetworkRules() error = %v, want a denied_domains constraint failure", err)
	}
}

func containsDomain(domains []string, want string) bool {
	for _, domain := range domains {
		if domain == want {
			return true
		}
	}
	return false
}
