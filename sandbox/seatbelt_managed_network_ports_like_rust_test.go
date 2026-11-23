package sandbox

import (
	"encoding/json"
	"strings"
	"testing"
)

// Rust #62386 derives the Seatbelt proxy allowances from the prepared
// managed-network route: loopback proxy ports and local binding come from the
// context attached to this command, not from a live proxy or the profile.
func TestBuildSeatbeltPolicyUsesPreparedLoopbackPortsLikeRust(t *testing.T) {
	profile := WorkspaceWritePermissionProfile()
	profile.NetworkEnabled = true
	managed := &seatbeltManagedNetworkContext{
		LoopbackPorts:     []uint16{43123, 43124},
		AllowLocalBinding: true,
	}
	policy, _, err := buildSeatbeltPolicyWithManagedNetwork(t.TempDir(), &profile, nil, managed, "")
	if err != nil {
		t.Fatalf("buildSeatbeltPolicyWithManagedNetwork: %v", err)
	}
	for _, want := range []string{
		"(deny network*)",
		`(allow network-bind (local ip "*:*"))`,
		`(allow network-inbound (local ip "localhost:*"))`,
		`(allow network-outbound (remote ip "localhost:*"))`,
		`(allow network-outbound (remote ip "localhost:43123"))`,
		`(allow network-outbound (remote ip "localhost:43124"))`,
		seatbeltTLSTrustService,
	} {
		if !strings.Contains(policy, want) {
			t.Fatalf("policy missing %q:\n%s", want, policy)
		}
	}
	deny := strings.Index(policy, "(deny network*)")
	for _, allow := range []string{`(allow network-outbound (remote ip "localhost:43123"))`, `(allow network-bind (local ip "*:*"))`} {
		if index := strings.Index(policy, allow); index < deny {
			t.Fatalf("allowance %q precedes the network deny:\n%s", allow, policy)
		}
	}
}

// A prepared route without local binding still restricts egress to the proxy
// ports and never grants unrestricted inbound or outbound traffic.
func TestBuildSeatbeltPolicyPreparedPortsOnlyLikeRust(t *testing.T) {
	profile := WorkspaceWritePermissionProfile()
	profile.NetworkEnabled = true
	policy, _, err := buildSeatbeltPolicyWithManagedNetwork(t.TempDir(), &profile, nil, &seatbeltManagedNetworkContext{LoopbackPorts: []uint16{44001}}, "")
	if err != nil {
		t.Fatalf("buildSeatbeltPolicyWithManagedNetwork: %v", err)
	}
	if !strings.Contains(policy, `(allow network-outbound (remote ip "localhost:44001"))`) || !strings.Contains(policy, seatbeltTLSTrustService) {
		t.Fatalf("policy missing prepared proxy port allowance:\n%s", policy)
	}
	for _, forbidden := range []string{`(allow network-bind (local ip "*:*"))`, `(allow network-inbound (local ip "localhost:*"))`} {
		if strings.Contains(policy, forbidden) {
			t.Fatalf("policy granted %q without allow-local-binding:\n%s", forbidden, policy)
		}
	}
}

// An empty prepared route keeps IP networking denied so a managed-network
// command cannot fall back to unrestricted egress.
func TestBuildSeatbeltPolicyPreparedEmptyRouteDeniesNetworkLikeRust(t *testing.T) {
	profile := WorkspaceWritePermissionProfile()
	profile.NetworkEnabled = true
	policy, _, err := buildSeatbeltPolicyWithManagedNetwork(t.TempDir(), &profile, nil, &seatbeltManagedNetworkContext{}, "")
	if err != nil {
		t.Fatalf("buildSeatbeltPolicyWithManagedNetwork: %v", err)
	}
	if !strings.Contains(policy, "(deny network*)") {
		t.Fatalf("empty prepared route did not deny networking:\n%s", policy)
	}
	if strings.Contains(policy, "(allow network-outbound (remote ip") || strings.Contains(policy, `(allow network-bind (local ip`) {
		t.Fatalf("empty prepared route granted IP networking:\n%s", policy)
	}
	if strings.Contains(policy, seatbeltTLSTrustService) {
		t.Fatalf("empty prepared route granted TLS trust access:\n%s", policy)
	}
}

// A profile with networking but no managed route keeps the unrestricted grant
// the executor had before prepared routes existed.
func TestBuildSeatbeltPolicyNetworkWithoutManagedRouteStaysUnrestrictedLikeRust(t *testing.T) {
	profile := WorkspaceWritePermissionProfile()
	profile.NetworkEnabled = true
	policy, _, err := buildSeatbeltPolicyWithManagedNetwork(t.TempDir(), &profile, nil, nil, "")
	if err != nil {
		t.Fatalf("buildSeatbeltPolicyWithManagedNetwork: %v", err)
	}
	if strings.Contains(policy, "(deny network*)") || !strings.Contains(policy, seatbeltTLSTrustService) {
		t.Fatalf("unmanaged network policy changed:\n%s", policy)
	}
}

// The wire context from the executor carries loopback ports and local binding
// alongside the Unix-socket policy.
func TestParseSeatbeltManagedNetworkContextReadsPreparedRouteLikeRust(t *testing.T) {
	raw := `{"loopbackPorts":[43123],"allowLocalBinding":true,"allowUnixSockets":["/tmp/agent.sock"],"dangerouslyAllowAllUnixSockets":false}`
	context, err := parseSeatbeltManagedNetworkContext(raw)
	if err != nil {
		t.Fatalf("parseSeatbeltManagedNetworkContext: %v", err)
	}
	if context == nil || len(context.LoopbackPorts) != 1 || context.LoopbackPorts[0] != 43123 || !context.AllowLocalBinding || len(context.AllowUnixSockets) != 1 {
		t.Fatalf("parsed context = %#v", context)
	}
	roundTrip, err := json.Marshal(context)
	if err != nil {
		t.Fatalf("marshal context: %v", err)
	}
	if !strings.Contains(string(roundTrip), `"loopbackPorts":[43123]`) || !strings.Contains(string(roundTrip), `"allowLocalBinding":true`) {
		t.Fatalf("round-trip context = %s", roundTrip)
	}
}
