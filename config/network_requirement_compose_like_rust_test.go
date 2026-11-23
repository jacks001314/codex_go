package config

import (
	"strings"
	"testing"
)

func testManagedNetworkLayer(t *testing.T, name, document string) managedNetworkRequirementLayer {
	t.Helper()
	values, err := parseRequirementsTOMLValues([]byte(document))
	if err != nil {
		t.Fatalf("%s: parse requirements: %v", name, err)
	}
	return managedNetworkRequirementLayer{Name: name, Values: values}
}

func testComposedNetworkDomains(t *testing.T, composed map[string]any) map[string]string {
	t.Helper()
	out := map[string]string{}
	if domains, ok := composed["domains"].(map[string]any); ok {
		for pattern, raw := range domains {
			out[pattern] = raw.(string)
		}
	}
	return out
}

func assertComposedNetworkDomains(t *testing.T, composed map[string]any, want map[string]string) {
	t.Helper()
	got := testComposedNetworkDomains(t, composed)
	if len(got) != len(want) {
		t.Fatalf("composed domains = %#v, want %#v", got, want)
	}
	for pattern, permission := range want {
		if got[pattern] != permission {
			t.Fatalf("composed domains = %#v, want %#v", got, want)
		}
	}
}

// TestComposeManagedNetworkListReplacementsLikeRust mirrors Rust #62882
// stack_tests.rs::list_replacements_preserve_unrelated_keyed_hosts.
func TestComposeManagedNetworkListReplacementsLikeRust(t *testing.T) {
	old := "allowed_domains = ['old.test']\ndenied_domains = ['retired.test']"
	mirror := "[network_proxy]\n" + old + "\n[permissions.network]\n" + old
	cloud := "[permissions.network]\ndangerously_allow_all_unix_sockets = true"
	device := "[experimental_network]\nallowed_domains = ['new.test']\ndenied_domains = ['shared.test']"
	for _, test := range []struct {
		name    string
		domains string
		want    map[string]string
	}{
		{
			name:    "empty keyed map",
			domains: "{}",
			want:    map[string]string{"new.test": "allow", "shared.test": "deny"},
		},
		{
			name:    "keyed updates",
			domains: "{ 'cloud.test' = 'allow', 'shared.test' = 'allow', 'NEW.TEST.' = 'deny' }",
			want:    map[string]string{"cloud.test": "allow", "new.test": "allow", "shared.test": "deny"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			composed, err := composeManagedNetworkRequirementLayers([]managedNetworkRequirementLayer{
				testManagedNetworkLayer(t, "Old", mirror),
				testManagedNetworkLayer(t, "Cloud", cloud+"\ndomains = "+test.domains),
				testManagedNetworkLayer(t, "Device", device),
			})
			if err != nil {
				t.Fatalf("compose: %v", err)
			}
			assertComposedNetworkDomains(t, composed, test.want)
			if got := composed["dangerously_allow_all_unix_sockets"]; got != true {
				t.Fatalf("dangerously_allow_all_unix_sockets = %#v, want true", got)
			}
		})
	}
}

// TestComposeManagedNetworkClearLikeRust mirrors Rust's
// only_a_surviving_empty_allowlist_clears_keyed_grants.
func TestComposeManagedNetworkClearLikeRust(t *testing.T) {
	layers := []managedNetworkRequirementLayer{
		testManagedNetworkLayer(t, "Deny", "[network_proxy.domains]\n'a.test' = 'deny'"),
		testManagedNetworkLayer(t, "Grant", "[permissions.network.domains]\n'a.test' = 'allow'\n'B.TEST.' = 'allow'\n'b.test' = 'deny'"),
		testManagedNetworkLayer(t, "Clear", "[network_proxy]\nallowed_domains = []\n[permissions.network]\nallowed_domains = []"),
	}
	composed, err := composeManagedNetworkRequirementLayers(layers)
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	assertComposedNetworkDomains(t, composed, map[string]string{"a.test": "deny", "b.test": "deny"})

	layers = append(layers, testManagedNetworkLayer(t, "Replacement",
		"[network_proxy]\nallowed_domains = ['new.test']\ndenied_domains = []"))
	composed, err = composeManagedNetworkRequirementLayers(layers)
	if err != nil {
		t.Fatalf("compose replacement: %v", err)
	}
	assertComposedNetworkDomains(t, composed, map[string]string{
		"a.test":   "allow",
		"B.TEST.":  "allow",
		"new.test": "allow",
		"b.test":   "deny",
	})
}

// TestComposeManagedNetworkWildcardPortsLikeRust mirrors Rust's
// wildcard_ports_revoke_local_exceptions_and_respect_the_surviving_deny_list.
func TestComposeManagedNetworkWildcardPortsLikeRust(t *testing.T) {
	composed, err := composeManagedNetworkRequirementLayers([]managedNetworkRequirementLayer{
		testManagedNetworkLayer(t, "Deny", "[network_proxy]\ndenied_domains = ['127.0.0.1']"),
		testManagedNetworkLayer(t, "Grants", "[permissions.network.domains]\n'127.0.0.1' = 'allow'\n'localhost' = 'allow'\n'::1' = 'allow'"),
		testManagedNetworkLayer(t, "Wildcards", "[network_proxy]\nallowed_domains = ['127.0.0.1:*', 'localhost:*', '[::01]:*']"),
	})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	assertComposedNetworkDomains(t, composed, map[string]string{
		"127.0.0.1:*": "allow",
		"localhost:*": "allow",
		"[::1]:*":     "allow",
		"[::01]:*":    "allow",
		"127.0.0.1":   "deny",
	})
}

// TestComposeManagedNetworkIPv6SpellingsLikeRust mirrors Rust's
// ipv6_grants_and_denials_preserve_published_spellings.
func TestComposeManagedNetworkIPv6SpellingsLikeRust(t *testing.T) {
	composed, err := composeManagedNetworkRequirementLayers([]managedNetworkRequirementLayer{
		testManagedNetworkLayer(t, "Low", "[network_proxy.domains]\n'2001:db8::1' = 'allow'\n'2001:db8::2' = 'deny'"),
		testManagedNetworkLayer(t, "Grant", "[network_proxy.domains]\n'[2001:db8::2]' = 'allow'"),
		testManagedNetworkLayer(t, "High", "[permissions.network.domains]\n'2001:0db8::1' = 'allow'\n'2001:0db8::2' = 'deny'"),
	})
	if err != nil {
		t.Fatalf("compose: %v", err)
	}
	assertComposedNetworkDomains(t, composed, map[string]string{
		"2001:db8::1":   "allow",
		"2001:0db8::1":  "allow",
		"[2001:db8::2]": "allow",
		"2001:db8::2":   "deny",
		"2001:0db8::2":  "deny",
	})
}

// TestComposeManagedNetworkRejectsLayeredMirrorDisagreementLikeRust mirrors
// Rust's layered_mirrors_must_agree_on_replacements_and_the_effective_allowlist.
func TestComposeManagedNetworkRejectsLayeredMirrorDisagreementLikeRust(t *testing.T) {
	policy := "[permissions.network]\nallowed_domains = ['a.test']\ndenied_domains = ['b.test']"
	cloud := testManagedNetworkLayer(t, "Cloud", policy)
	for _, device := range []string{
		"[network_proxy]\n[permissions.network]\nallowed_domains = []",
		"[network_proxy]\n[permissions.network]\ndenied_domains = []",
		"[network_proxy]\nallowed_domains = ['c.test']\n[permissions.network]\ndomains.'c.test' = 'allow'",
		"[network_proxy]\ndenied_domains = ['a.test']\n[permissions.network]\ndenied_domains = ['A.TEST.']",
	} {
		_, err := composeManagedNetworkRequirementLayers([]managedNetworkRequirementLayer{
			cloud,
			testManagedNetworkLayer(t, "Device", device),
		})
		if err == nil {
			t.Fatalf("expected a layered mirror error for %q", device)
		}
		if !strings.Contains(err.Error(), "Device") {
			t.Fatalf("error = %v, want the Device source", err)
		}
	}
}

// TestComposeManagedNetworkRejectsMixedSocketSpellingsLikeRust mirrors Rust's
// mixed_network_tables_reject_socket_path_rules.
func TestComposeManagedNetworkRejectsMixedSocketSpellingsLikeRust(t *testing.T) {
	cloud := testManagedNetworkLayer(t, "Cloud", "[network_proxy]")
	for _, device := range []string{
		"[permissions.network.unix_sockets]\n'/tmp/managed.sock' = 'deny'",
		"[experimental_network]\n[permissions.network]\nallow_unix_sockets = []",
	} {
		_, err := composeManagedNetworkRequirementLayers([]managedNetworkRequirementLayer{
			cloud,
			testManagedNetworkLayer(t, "Device", device),
		})
		if err == nil {
			t.Fatalf("expected a socket spelling error for %q", device)
		}
		message := err.Error()
		if !strings.Contains(message, "Unix socket path rules") || !strings.Contains(message, "Device") || !strings.Contains(message, "Cloud") {
			t.Fatalf("error = %v", err)
		}
	}
}

// TestComposeManagedNetworkEmptyAllowlistClearsLikeRust mirrors Rust's
// empty_network_allowlist_clears_inherited_allows.
func TestComposeManagedNetworkEmptyAllowlistClearsLikeRust(t *testing.T) {
	for _, table := range []string{"experimental_network", "permissions.network"} {
		low := "[" + table + "]\nallowed_domains = ['inherited.test']\ndenied_domains = ['blocked.test']"
		clear := "[" + table + "]\nallowed_domains = []"
		composed, err := composeManagedNetworkRequirementLayers([]managedNetworkRequirementLayer{
			testManagedNetworkLayer(t, "Low", low),
			testManagedNetworkLayer(t, "High", clear),
		})
		if err != nil {
			t.Fatalf("%s: compose: %v", table, err)
		}
		assertComposedNetworkDomains(t, composed, map[string]string{"blocked.test": "deny"})
	}
}

// TestComposeManagedNetworkShadowedAllowsLikeRust mirrors Rust's
// shadowed_allows_agree_across_network_spellings.
func TestComposeManagedNetworkShadowedAllowsLikeRust(t *testing.T) {
	legacy := "[experimental_network]\nallowed_domains = ['a.test', 'b.test']\ndenied_domains = ['a.test', 'b.test']"
	for _, nested := range []string{
		"[permissions.network]\nallowed_domains = ['a.test']\ndenied_domains = ['a.test', 'b.test']",
		"[permissions.network.domains]\n'a.test' = 'deny'\n'b.test' = 'deny'",
	} {
		composed, err := composeManagedNetworkRequirementLayers([]managedNetworkRequirementLayer{
			testManagedNetworkLayer(t, "Same", legacy+"\n"+nested),
		})
		if err != nil {
			t.Fatalf("compose: %v", err)
		}
		assertComposedNetworkDomains(t, composed, map[string]string{"a.test": "deny", "b.test": "deny"})
	}
}

// TestComposeManagedNetworkSingleLayerEmptyAllowlistLikeRust pins that a
// surviving empty allowlist publishes an empty managed domains map.
func TestComposeManagedNetworkSingleLayerEmptyAllowlistLikeRust(t *testing.T) {
	for _, table := range []string{"network_proxy", "experimental_network"} {
		composed, err := composeManagedNetworkRequirementLayers([]managedNetworkRequirementLayer{
			testManagedNetworkLayer(t, "Device", "["+table+"]\nallowed_domains = []"),
		})
		if err != nil {
			t.Fatalf("%s: compose: %v", table, err)
		}
		domains, ok := composed["domains"].(map[string]any)
		if !ok || len(domains) != 0 {
			t.Fatalf("%s: composed domains = %#v, want an empty declaration", table, composed["domains"])
		}
	}
}
