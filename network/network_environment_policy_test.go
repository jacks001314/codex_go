package network

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestEnvironmentNetworkPolicyApplyToPreservesInheritedDenials(t *testing.T) {
	controller := &Config{
		Enabled: true,
		Domains: map[string]DomainPermission{
			"blocked.example": DomainDeny,
			"allowed.example": DomainAllow,
		},
	}
	owner := &EnvironmentNetworkPolicy{
		Domains: map[string]DomainPermission{
			"owner.example": DomainAllow,
		},
	}
	owner.ApplyTo(controller)
	if controller.Domains["owner.example"] != DomainAllow {
		t.Fatalf("owner domain not applied: %+v", controller.Domains)
	}
	if controller.Domains["blocked.example"] != DomainDeny {
		t.Fatalf("inherited denial not preserved: %+v", controller.Domains)
	}
}

func TestNewSpecForEnvironmentRejectsDisabledManagedNetwork(t *testing.T) {
	_, err := NewSpecForEnvironment(Config{}, nil, PermissionDisabled, &EnvironmentNetworkPolicy{}, nil)
	if err == nil {
		t.Fatal("disabled managed network should be rejected")
	}

	spec, err := NewSpecForEnvironment(Config{Enabled: true}, nil, PermissionManaged, &EnvironmentNetworkPolicy{
		Domains: map[string]DomainPermission{"owner.example": DomainAllow},
	}, []NetworkRule{{Host: "decided.example", Decision: DomainAllow}})
	if err != nil {
		t.Fatalf("NewSpecForEnvironment error = %v", err)
	}
	if spec.Config().Domains["owner.example"] != DomainAllow {
		t.Fatalf("owner domain missing: %+v", spec.Config().Domains)
	}
	if spec.Config().Domains["decided.example"] != DomainAllow {
		t.Fatalf("exec rule domain missing: %+v", spec.Config().Domains)
	}
}

func boolTestPtr(value bool) *bool { return &value }

// Mirrors Rust network_proxy_spec_tests.rs
// ::attachment_socket_grants_respect_configured_restrictions_at_remote_launch
// (#46004): an omitted controller socket policy defers to the attachment while
// an explicit deny, finite grants, an explicitly empty map, or a managed
// requirement all narrow (or block) the attachment's allow-all.
func TestAttachmentSocketGrantsRespectConfiguredRestrictionsLikeRust(t *testing.T) {
	for _, tc := range []struct {
		name         string
		controller   Config
		required     *bool
		wantComposed bool
	}{
		{
			name:         "omitted",
			controller:   Config{Enabled: true},
			wantComposed: true,
		},
		{
			name:         "explicit deny",
			controller:   Config{Enabled: true, DangerouslyAllowAllUnixSockets: boolTestPtr(false)},
			wantComposed: false,
		},
		{
			name:         "explicit allow",
			controller:   Config{Enabled: true, DangerouslyAllowAllUnixSockets: boolTestPtr(true)},
			wantComposed: true,
		},
		{
			name: "finite",
			controller: Config{Enabled: true, UnixSockets: map[string]UnixSocketPermission{
				"/tmp/allowed.sock": UnixSocketAllow,
				"/tmp/denied.sock":  UnixSocketDeny,
			}},
			wantComposed: false,
		},
		{
			name:         "empty",
			controller:   Config{Enabled: true, UnixSockets: map[string]UnixSocketPermission{}},
			wantComposed: false,
		},
		{
			name:         "managed deny",
			controller:   Config{Enabled: true, DangerouslyAllowAllUnixSockets: boolTestPtr(true)},
			required:     boolTestPtr(false),
			wantComposed: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			controller := tc.controller
			if tc.required != nil {
				spec, err := NewSpec(controller, &Requirements{
					Enabled:                        boolTestPtr(true),
					DangerouslyAllowAllUnixSockets: tc.required,
				}, PermissionManaged)
				if err != nil {
					t.Fatalf("NewSpec() error = %v", err)
				}
				controller = spec.Config()
			}
			// The owner (attachment) grants allow-all sockets.
			owner := EnvironmentNetworkPolicyFromConfig(&Config{
				DangerouslyAllowAllUnixSockets: boolTestPtr(true),
			}, false)
			owner.ApplyTo(&controller)
			if got := boolPointerValue(controller.DangerouslyAllowAllUnixSockets); got != tc.wantComposed {
				t.Fatalf("composed allow-all = %v, want %v (unix sockets=%#v)", got, tc.wantComposed, controller.UnixSockets)
			}
			if tc.name == "finite" {
				if len(controller.UnixSockets) != 2 ||
					controller.UnixSockets["/tmp/allowed.sock"] != UnixSocketAllow ||
					controller.UnixSockets["/tmp/denied.sock"] != UnixSocketDeny {
					t.Fatalf("finite grants not preserved: %#v", controller.UnixSockets)
				}
			}
		})
	}
}

// Mirrors Rust #46004: capturing a policy for an attachment resolves an
// omitted flag to false, so controller-only commands never inherit grants.
func TestEnvironmentNetworkPolicyFromConfigResolvesOmittedFlagLikeRust(t *testing.T) {
	policy := EnvironmentNetworkPolicyFromConfig(&Config{Enabled: true}, false)
	if policy.DangerouslyAllowAllUnixSockets {
		t.Fatal("omitted controller flag must resolve to false for the portable policy")
	}
	// Rust #48198: the owner's enabled proxy configuration is captured as an
	// explicit proxy requirement, so restricted commands are not left offline.
	if !policy.RequiresProxy {
		t.Fatal("an enabled owner proxy must be captured as a proxy requirement")
	}
	disabled := EnvironmentNetworkPolicyFromConfig(&Config{}, false)
	if disabled.RequiresProxy {
		t.Fatal("a disabled owner proxy must not require proxy routing")
	}
	explicit := EnvironmentNetworkPolicyFromConfig(&Config{
		Enabled:                        true,
		DangerouslyAllowAllUnixSockets: boolTestPtr(true),
	}, false)
	if !explicit.DangerouslyAllowAllUnixSockets {
		t.Fatal("explicit controller allow-all must be captured")
	}
}

// Mirrors Rust #48198's wire shape: an explicit proxy requirement is serialized
// and a policy without one omits the field.
func TestEnvironmentNetworkPolicyRequiresProxyWireLikeRust(t *testing.T) {
	json := func(policy EnvironmentNetworkPolicy) string {
		t.Helper()
		encoded, err := json.Marshal(policy)
		if err != nil {
			t.Fatalf("marshal environment network policy: %v", err)
		}
		return string(encoded)
	}
	if got := json(EnvironmentNetworkPolicy{RequiresProxy: true}); !strings.Contains(got, `"requiresProxy":true`) {
		t.Fatalf("required proxy policy = %s", got)
	}
	if got := json(EnvironmentNetworkPolicy{}); strings.Contains(got, "requiresProxy") {
		t.Fatalf("policy without a proxy requirement = %s", got)
	}
}
