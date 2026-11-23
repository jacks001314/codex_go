package network

import (
	"reflect"
	"sort"
	"testing"
)

// TestProxySpecCarriesEquivalentDeniedSelectorSpellingsLikeRust mirrors Rust
// #62882: a managed allowlist entry that spells a denied selector differently is
// added to the effective deny list.
func TestProxySpecCarriesEquivalentDeniedSelectorSpellingsLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.SetDeniedDomains([]string{"blocked.example.com"})
	requirements := &ProxyRequirements{Domains: &ProxyDomainPermissions{Entries: []ProxyDomainPermissionEntry{
		{Pattern: "2001:db8::1", Permission: ProxyDomainDeny},
		{Pattern: "2001:0db8:0:0:0:0:0:1", Permission: ProxyDomainAllow},
		{Pattern: "api.example.com", Permission: ProxyDomainAllow},
	}}}
	spec, err := NewProxySpec(ProxyConfig{Network: settings}, requirements, true)
	if err != nil {
		t.Fatalf("NewProxySpec() error = %v", err)
	}
	config := spec.Config()
	denied := config.Network.DeniedDomains()
	want := []string{"2001:db8::1", "2001:0db8:0:0:0:0:0:1", "blocked.example.com"}
	sort.Strings(denied)
	sort.Strings(want)
	if !reflect.DeepEqual(denied, want) {
		t.Fatalf("denied domains = %#v, want %#v", denied, want)
	}
}
