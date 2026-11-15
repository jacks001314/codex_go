package config

import (
	"fmt"
	"strings"
	"testing"
)

// TestPermissionsNetworkRequirementsLiftToManagedNetworkLikeRust mirrors Rust
// #58182: a `permissions.network` table in requirements.toml is shared network
// policy, not a permission profile named "network".
func TestPermissionsNetworkRequirementsLiftToManagedNetworkLikeRust(t *testing.T) {
	requirements, err := ParseRequirementsTOML([]byte(`
[permissions.network]
managed_allowed_domains_only = true
[permissions.network.domains]
"example.com" = "allow"
`))
	if err != nil {
		t.Fatalf("ParseRequirementsTOML() error = %v", err)
	}
	if requirements.Network == nil {
		t.Fatal("permissions.network was not lifted to the managed network requirement")
	}
	if got := requirements.Network.Domains["example.com"]; got != NetworkAllow {
		t.Fatalf("domains[example.com] = %q, want allow", got)
	}
	if requirements.Network.ManagedAllowedDomainsOnly == nil || !*requirements.Network.ManagedAllowedDomainsOnly {
		t.Fatalf("managed_allowed_domains_only = %#v", requirements.Network.ManagedAllowedDomainsOnly)
	}
	if _, namedProfile := requirements.Permissions["network"]; namedProfile {
		t.Fatalf("permissions kept a profile named network: %#v", requirements.Permissions)
	}
}

// TestPermissionsNetworkReservedProfileSpellingsRejectedLikeRust mirrors Rust
// #58182: profile-shaped `permissions.network` tables are rejected with the
// migration guidance, because the key now means shared network constraints.
func TestPermissionsNetworkReservedProfileSpellingsRejectedLikeRust(t *testing.T) {
	for _, field := range []string{
		"",
		"description = 'network profile'",
		"extends = ':workspace'",
		"workspace_roots = {}",
		"filesystem = {}",
		"network = {}",
	} {
		document := "[permissions.network]\n" + field
		_, err := ParseRequirementsTOML([]byte(document))
		if err == nil {
			t.Fatalf("%q was accepted as shared network requirements", document)
		}
		if !strings.Contains(err.Error(), permissionsNetworkReservedProfileError) {
			t.Fatalf("%q error = %v, want the reserved-profile guidance", document, err)
		}
	}
}

// TestPermissionsNetworkConflictsWithTopLevelNetworkLikeRust mirrors Rust
// #61391: both spellings may coexist only when they describe the same policy.
func TestPermissionsNetworkConflictsWithTopLevelNetworkLikeRust(t *testing.T) {
	_, err := ParseRequirementsTOML([]byte(`
[network_proxy]
enabled = true
[permissions.network]
managed_allowed_domains_only = true
`))
	if err == nil {
		t.Fatal("permissions.network plus top-level network_proxy was accepted")
	}
	if !strings.Contains(err.Error(), "same network policy") {
		t.Fatalf("conflict error = %v, want the shared-policy guidance", err)
	}
}

// TestDualNetworkSpellingsShareOnePolicyLikeRust mirrors Rust #61391: a source
// that publishes both spellings is accepted when the proxy selectors match,
// including equivalent IPv6 spellings.
func TestDualNetworkSpellingsShareOnePolicyLikeRust(t *testing.T) {
	for _, alias := range []string{"network_proxy", "experimental_network"} {
		requirements, err := ParseRequirementsTOML([]byte(fmt.Sprintf(`
[%s]
enabled = false
[%s.domains]
"2001:db8::1" = "deny"
[permissions.network]
enabled = false
[permissions.network.domains]
"2001:0db8:0:0:0:0:0:1" = "deny"
`, alias, alias)))
		if err != nil {
			t.Fatalf("%s dual spelling rejected: %v", alias, err)
		}
		if requirements.Network == nil || requirements.Network.Enabled == nil || *requirements.Network.Enabled {
			t.Fatalf("%s network = %#v", alias, requirements.Network)
		}
		if got := requirements.Network.Domains["2001:db8::1"]; got != NetworkDeny {
			t.Fatalf("%s domains = %#v", alias, requirements.Network.Domains)
		}
		if _, namedProfile := requirements.Permissions["network"]; namedProfile {
			t.Fatalf("%s kept a profile named network: %#v", alias, requirements.Permissions)
		}
	}
}

// TestDualNetworkSpellingsUsePermissionSpecificSelectorIdentitiesLikeRust
// mirrors Rust #61351: grant and restriction comparison keys differ, so an
// allow selector with a wildcard port is not the same grant as the bare literal.
func TestDualNetworkSpellingsUsePermissionSpecificSelectorIdentitiesLikeRust(t *testing.T) {
	accepted := map[string]string{
		"subdomain glob spelling": `
[network_proxy]
enabled = false
[network_proxy.domains]
"?*.EXAMPLE.com." = "allow"
[permissions.network]
enabled = false
[permissions.network.domains]
"*.example.com" = "allow"
`,
		"deny IPv6 spelling": `
[network_proxy]
enabled = false
[network_proxy.domains]
"2001:0db8:0:0:0:0:0:1" = "deny"
[permissions.network]
enabled = false
[permissions.network.domains]
"2001:db8::1" = "deny"
`,
		"review subdomain glob": `
[network_proxy]
enabled = false
review_domains = ["?*.Example.com."]
[permissions.network]
enabled = false
review_domains = ["*.example.com"]
`,
	}
	for name, document := range accepted {
		if _, err := ParseRequirementsTOML([]byte(document)); err != nil {
			t.Fatalf("%s rejected: %v", name, err)
		}
	}

	rejected := map[string]string{
		"allow wildcard port": `
[network_proxy]
enabled = false
[network_proxy.domains]
"[::1]:*" = "allow"
[permissions.network]
enabled = false
[permissions.network.domains]
"::1" = "allow"
`,
		"deny IPv4 spelling": `
[network_proxy]
enabled = false
[network_proxy.domains]
"127.000.000.001" = "deny"
[permissions.network]
enabled = false
[permissions.network.domains]
"127.0.0.1" = "deny"
`,
	}
	for name, document := range rejected {
		_, err := ParseRequirementsTOML([]byte(document))
		if err == nil {
			t.Fatalf("%s accepted, want a network policy mismatch", name)
		}
		if !strings.Contains(err.Error(), "same network policy") {
			t.Fatalf("%s error = %v, want the shared-policy guidance", name, err)
		}
	}
}
