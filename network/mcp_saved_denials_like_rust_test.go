package network

import "testing"

// Rust #60046: a saved grant that no longer fits the managed constraints is
// dropped, while the denials from the same batch keep applying.
func TestStaleGrantKeepsSavedDenialsLikeRust(t *testing.T) {
	spec, err := NewSpec(Config{}, &Requirements{
		AllowedDomains:            []string{"managed.example.com"},
		ManagedAllowedDomainsOnly: true,
	}, PermissionManaged)
	if err != nil {
		t.Fatalf("NewSpec() error = %v", err)
	}

	next, err := spec.WithExecPolicyNetworkRules([]NetworkRule{
		{Host: "stale.example.com", Decision: DomainAllow},
		{Host: "Blocked.Example.com.", Decision: DomainDeny},
	})
	if err != nil {
		t.Fatalf("WithExecPolicyNetworkRules() error = %v", err)
	}
	domains := next.Config().Domains
	if domains["stale.example.com"] == DomainAllow {
		t.Fatalf("stale grant survived the constraint check: %v", domains)
	}
	if domains["blocked.example.com"] != DomainDeny {
		t.Fatalf("saved denial dropped with the stale grant: %v", domains)
	}
	if domains["managed.example.com"] != DomainAllow {
		t.Fatalf("managed allowlist changed: %v", domains)
	}
	if !next.HardDenyAllowlistMisses() {
		t.Fatal("HardDenyAllowlistMisses() = false, want the managed restriction kept")
	}
}

// Rules that normalize to nothing cannot take the valid denials down with them.
func TestSavedDenialsSurviveMalformedRuleHostsLikeRust(t *testing.T) {
	spec, err := NewSpec(DefaultConfig(), nil, PermissionDisabled)
	if err != nil {
		t.Fatalf("NewSpec() error = %v", err)
	}

	next, err := spec.WithExecPolicyNetworkRules([]NetworkRule{
		{Host: "   ", Decision: DomainDeny},
		{Host: "saved-deny.example.com", Decision: DomainDeny},
	})
	if err != nil {
		t.Fatalf("WithExecPolicyNetworkRules() error = %v", err)
	}
	domains := next.Config().Domains
	if domains["saved-deny.example.com"] != DomainDeny {
		t.Fatalf("valid denial missing: %v", domains)
	}
	for host := range domains {
		if host == "" {
			t.Fatalf("malformed host was stored: %v", domains)
		}
	}
}

// Denials win over grants for the same host whatever order the file uses, and
// grants that fit the constraints still apply.
func TestSavedRulesApplyGrantsAndDenialsLikeRust(t *testing.T) {
	spec, err := NewSpec(DefaultConfig(), nil, PermissionDisabled)
	if err != nil {
		t.Fatalf("NewSpec() error = %v", err)
	}

	next, err := spec.WithExecPolicyNetworkRules([]NetworkRule{
		{Host: "api.example.com", Decision: DomainDeny},
		{Host: "API.example.com", Decision: DomainAllow},
		{Host: "docs.example.com", Decision: DomainAllow},
	})
	if err != nil {
		t.Fatalf("WithExecPolicyNetworkRules() error = %v", err)
	}
	domains := next.Config().Domains
	if domains["api.example.com"] != DomainDeny {
		t.Fatalf("grant overrode a saved denial: %v", domains)
	}
	if domains["docs.example.com"] != DomainAllow {
		t.Fatalf("fitting grant missing: %v", domains)
	}
}
