package network

import (
	"reflect"
	"testing"
)

func mcpManagedController(t *testing.T, enabled bool, requirements *Requirements) *Spec {
	t.Helper()
	spec, err := NewSpec(Config{
		Enabled:       enabled,
		Mode:          ModeLimited,
		ReviewDomains: []string{"allowed2.example.com"},
	}, requirements, PermissionManaged)
	if err != nil {
		t.Fatalf("NewSpec() error = %v", err)
	}
	return spec
}

func mcpManagedRequirements(enabled *bool) *Requirements {
	allow := true
	return &Requirements{
		Enabled:                   enabled,
		ManagedAllowedDomainsOnly: true,
		AllowLocalBinding:         &allow,
		AllowedDomains:            []string{"allowed?.example.com"},
		DeniedDomains:             []string{"blocked.example.com"},
	}
}

func mcpSavedRules() []NetworkRule {
	return []NetworkRule{
		{Host: "previously-allowed.example.com", Decision: DomainAllow},
		{Host: "allowed1.example.com", Decision: DomainDeny},
	}
}

// Rust #60046 mcp_network_policy: the managed restrictions decide whether the
// call keeps a proxy, a direct route, or no network at all.
func TestMCPNetworkPolicyKeepsManagedRestrictionsLikeRust(t *testing.T) {
	disable := false
	enable := true
	cases := []struct {
		name            string
		controller      func(t *testing.T) *Spec
		expectedEnabled bool
		expectedMode    Mode
		expectedAllowed []string
		expectedDenied  []string
	}{
		{
			name:            "restricted offline",
			controller:      func(t *testing.T) *Spec { return mcpManagedController(t, false, mcpManagedRequirements(nil)) },
			expectedEnabled: false,
			expectedMode:    ModeFull,
			expectedAllowed: []string{"allowed?.example.com"},
			expectedDenied:  []string{"blocked.example.com", "allowed2.example.com"},
		},
		{
			name:            "controller proxy",
			controller:      func(t *testing.T) *Spec { return mcpManagedController(t, true, mcpManagedRequirements(nil)) },
			expectedEnabled: true,
			expectedMode:    ModeLimited,
			expectedAllowed: []string{"allowed?.example.com"},
			expectedDenied:  []string{"allowed1.example.com", "blocked.example.com", "allowed2.example.com"},
		},
		{
			name:            "managed proxy",
			controller:      func(t *testing.T) *Spec { return mcpManagedController(t, false, mcpManagedRequirements(&enable)) },
			expectedEnabled: true,
			expectedMode:    ModeLimited,
			expectedAllowed: []string{"allowed?.example.com"},
			expectedDenied:  []string{"allowed1.example.com", "blocked.example.com", "allowed2.example.com"},
		},
		{
			name:            "managed disable",
			controller:      func(t *testing.T) *Spec { return mcpManagedController(t, true, mcpManagedRequirements(&disable)) },
			expectedEnabled: false,
			expectedMode:    ModeFull,
			expectedAllowed: []string{"allowed?.example.com"},
			expectedDenied:  []string{"blocked.example.com", "allowed2.example.com"},
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			controller := testCase.controller(t)
			meta, err := MCPNetworkPolicy(controller, PermissionManaged,
				MCPNetworkRoute{PrimaryAllowsProxy: true},
				PermissionManaged, false, false, mcpSavedRules())
			if err != nil {
				t.Fatalf("MCPNetworkPolicy() error = %v", err)
			}
			if meta.Enabled != testCase.expectedEnabled {
				t.Fatalf("enabled = %t, want %t", meta.Enabled, testCase.expectedEnabled)
			}
			if meta.Mode != testCase.expectedMode {
				t.Fatalf("mode = %q, want %q", meta.Mode, testCase.expectedMode)
			}
			if !reflect.DeepEqual(meta.AllowedDomains, testCase.expectedAllowed) {
				t.Fatalf("allowed = %#v, want %#v", meta.AllowedDomains, testCase.expectedAllowed)
			}
			if !reflect.DeepEqual(meta.DeniedDomains, testCase.expectedDenied) {
				t.Fatalf("denied = %#v, want %#v", meta.DeniedDomains, testCase.expectedDenied)
			}
			if !meta.HardDenyAllowlistMisses {
				t.Fatal("hard deny allowlist misses = false, want the managed restriction")
			}
		})
	}
}

// A profile that already allows direct network keeps working without a proxy.
func TestMCPNetworkPolicyDirectProfileLikeRust(t *testing.T) {
	meta, err := MCPNetworkPolicy(nil, PermissionExternal,
		MCPNetworkRoute{PrimaryAllowsProxy: true},
		PermissionExternal, true, false, nil)
	if err != nil {
		t.Fatalf("MCPNetworkPolicy() error = %v", err)
	}
	if !meta.Enabled || meta.Mode != ModeFull {
		t.Fatalf("metadata = %#v, want an enabled full-mode direct policy", meta)
	}
	if len(meta.AllowedDomains) != 0 || len(meta.DeniedDomains) != 0 || meta.HardDenyAllowlistMisses {
		t.Fatalf("direct policy = %#v, want no domain restrictions", meta)
	}

	disabled, err := MCPNetworkPolicy(nil, PermissionDisabled,
		MCPNetworkRoute{PrimaryAllowsProxy: true},
		PermissionDisabled, true, false, nil)
	if err != nil {
		t.Fatalf("MCPNetworkPolicy(disabled) error = %v", err)
	}
	if !disabled.Enabled {
		t.Fatalf("disabled sandbox metadata = %#v, want direct network enabled", disabled)
	}
}

// Review domains and MITM hook hosts cannot be enforced by a domain-only MCP
// receiver, so they become denials; restricted local binding keeps IP grants
// only because the receiver cannot check DNS answers.
func TestMCPNetworkPolicyDeniesReviewHooksAndNonIPGrantsLikeRust(t *testing.T) {
	controller, err := NewSpec(Config{
		Enabled:       true,
		Mode:          ModeLimited,
		ReviewDomains: []string{"review.profile.test"},
		Domains: map[string]DomainPermission{
			"**.profile.test": DomainAllow,
			"blocked.example": DomainDeny,
			"1.2.3.4":         DomainAllow,
			"api.example.com": DomainAllow,
		},
		MITMHooks: []MITMHook{{Host: "Hook.Profile.Test.:443"}},
	}, nil, PermissionManaged)
	if err != nil {
		t.Fatalf("NewSpec() error = %v", err)
	}
	meta, err := MCPNetworkPolicy(controller, PermissionManaged,
		MCPNetworkRoute{PrimaryAllowsProxy: true},
		PermissionManaged, false, false, nil)
	if err != nil {
		t.Fatalf("MCPNetworkPolicy() error = %v", err)
	}
	if !reflect.DeepEqual(meta.AllowedDomains, []string{"1.2.3.4"}) {
		t.Fatalf("allowed = %#v, want only the IP literal grant", meta.AllowedDomains)
	}
	wantDenied := []string{"blocked.example", "review.profile.test", "hook.profile.test"}
	if !reflect.DeepEqual(meta.DeniedDomains, wantDenied) {
		t.Fatalf("denied = %#v, want %#v", meta.DeniedDomains, wantDenied)
	}
	if !meta.HardDenyAllowlistMisses {
		t.Fatal("hard deny allowlist misses = false, want misses denied")
	}

	allowBinding := true
	generous, err := NewSpec(Config{
		Enabled:           true,
		Mode:              ModeLimited,
		AllowLocalBinding: &allowBinding,
		ReviewDomains:     []string{"review.profile.test"},
		Domains: map[string]DomainPermission{
			"**.profile.test": DomainAllow,
			"1.2.3.4":         DomainAllow,
			"api.example.com": DomainAllow,
		},
		MITMHooks: []MITMHook{{Host: "Hook.Profile.Test.:443"}},
	}, &Requirements{AllowedDomains: []string{"**.profile.test"}}, PermissionManaged)
	if err != nil {
		t.Fatalf("NewSpec(allow local binding) error = %v", err)
	}
	meta, err = MCPNetworkPolicy(generous, PermissionManaged,
		MCPNetworkRoute{PrimaryAllowsProxy: true},
		PermissionManaged, false, false, nil)
	if err != nil {
		t.Fatalf("MCPNetworkPolicy(allow local binding) error = %v", err)
	}
	wantAllowed := []string{"**.profile.test", "1.2.3.4", "api.example.com"}
	if !reflect.DeepEqual(meta.AllowedDomains, wantAllowed) {
		t.Fatalf("allowed = %#v, want %#v", meta.AllowedDomains, wantAllowed)
	}
	if meta.HardDenyAllowlistMisses {
		t.Fatal("hard deny allowlist misses = true with a reviewable binding")
	}
}

// An owner can supply its own proxy when no controller exists; an owner that
// does not require one leaves a restricted call offline.
func TestMCPNetworkPolicyOwnerRouteLikeRust(t *testing.T) {
	owner := &EnvironmentNetworkPolicy{RequiresProxy: true}
	meta, err := MCPNetworkPolicy(nil, PermissionManaged,
		MCPNetworkRoute{Owner: owner},
		PermissionManaged, false, false, nil)
	if err != nil {
		t.Fatalf("MCPNetworkPolicy(owner proxy) error = %v", err)
	}
	if !meta.Enabled {
		t.Fatalf("owner proxy metadata = %#v, want network enabled", meta)
	}

	offline := &EnvironmentNetworkPolicy{}
	meta, err = MCPNetworkPolicy(nil, PermissionManaged,
		MCPNetworkRoute{Owner: offline},
		PermissionManaged, false, false, nil)
	if err != nil {
		t.Fatalf("MCPNetworkPolicy(offline owner) error = %v", err)
	}
	if meta.Enabled {
		t.Fatalf("offline owner metadata = %#v, want network disabled", meta)
	}
}
