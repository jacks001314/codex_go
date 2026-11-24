package network

import (
	"reflect"
	"testing"
)

// Mirrors Rust #60046 network-proxy/src/config.rs::apply_saved_domain_rules:
// a saved denial replaces a stale saved grant for the same selector even when
// the grant arrives later in the same rule batch.
func TestProxySettingsApplySavedDomainRulesDenialOverridesStaleGrantLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.SetAllowedDomains([]string{"stale.example.com"})
	settings.ApplySavedDomainRules(
		[]string{"stale.example.com", "granted.example.com"},
		[]string{"STALE.example.com"},
	)
	if got := settings.AllowedDomains(); !reflect.DeepEqual(got, []string{"granted.example.com"}) {
		t.Fatalf("allowed domains = %v", got)
	}
	if got := settings.DeniedDomains(); !reflect.DeepEqual(got, []string{"STALE.example.com"}) {
		t.Fatalf("denied domains = %v", got)
	}
	deny, err := CompileProxyDomainMatcher(settings.DeniedDomains(), true)
	if err != nil {
		t.Fatalf("CompileProxyDomainMatcher() error = %v", err)
	}
	if !deny.Match("stale.example.com") {
		t.Fatal("saved denial does not cover the stale grant host")
	}
}

// Rust keeps valid saved denials when one entry makes the deny batch malformed,
// while every grant from that batch is dropped.
func TestProxySettingsApplySavedDomainRulesKeepsValidDenialsForMalformedBatchLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.ApplySavedDomainRules(
		[]string{"granted.example.com"},
		[]string{"*", "bad{pattern.example.com", "blocked.example.com"},
	)
	if got := settings.AllowedDomains(); got != nil {
		t.Fatalf("malformed deny batch must invalidate grants, got %v", got)
	}
	if got := settings.DeniedDomains(); !reflect.DeepEqual(got, []string{"blocked.example.com"}) {
		t.Fatalf("denied domains = %v", got)
	}
}

// A malformed allow batch drops only the grants; valid denials still apply.
func TestProxySettingsApplySavedDomainRulesKeepsDenialsForMalformedAllowBatchLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.ApplySavedDomainRules(
		[]string{"*.", "granted.example.com"},
		[]string{"blocked.example.com"},
	)
	if got := settings.AllowedDomains(); got != nil {
		t.Fatalf("malformed allow batch must invalidate grants, got %v", got)
	}
	if got := settings.DeniedDomains(); !reflect.DeepEqual(got, []string{"blocked.example.com"}) {
		t.Fatalf("denied domains = %v", got)
	}
}

// The managed-proxy rule overlay shares the same ordering guarantee.
func TestProxySpecWithNetworkRulesKeepsSavedDenialOverStaleGrantLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.SetAllowedDomains([]string{"stale.example.com"})
	requirements := &ProxyRequirements{Domains: &ProxyDomainPermissions{Entries: []ProxyDomainPermissionEntry{{Pattern: "stale.example.com", Permission: ProxyDomainAllow}}}}
	spec, err := NewProxySpec(ProxyConfig{Network: settings}, requirements, true)
	if err != nil {
		t.Fatalf("NewProxySpec() error = %v", err)
	}
	withRules, err := spec.WithNetworkRules([]ProxyNetworkRule{
		{Host: "stale.example.com", Permission: ProxyDomainAllow},
		{Host: "stale.example.com", Permission: ProxyDomainDeny},
	})
	if err != nil {
		t.Fatalf("WithNetworkRules() error = %v", err)
	}
	config := withRules.Config()
	if got := config.Network.AllowedDomains(); got != nil {
		t.Fatalf("allowed domains = %v, want the saved denial to win", got)
	}
	if got := config.Network.DeniedDomains(); !reflect.DeepEqual(got, []string{"stale.example.com"}) {
		t.Fatalf("denied domains = %v", got)
	}
}
