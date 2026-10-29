package execserver

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestManagedNetworkSandboxContextUnixSocketWireLikeRust mirrors Rust #45534's
// protocol round trip and legacy default: the Unix socket policy travels in the
// managed-network context, and a context serialized before the fields existed
// deserializes to the restrictive values.
func TestManagedNetworkSandboxContextUnixSocketWireLikeRust(t *testing.T) {
	encoded, err := json.Marshal(ManagedNetworkSandboxContext{
		LoopbackPorts:                  []uint16{43123, 48081},
		AllowLocalBinding:              false,
		AllowUnixSockets:               []string{"/tmp/allowed.sock"},
		DangerouslyAllowAllUnixSockets: true,
	})
	if err != nil {
		t.Fatalf("Marshal(context) error = %v", err)
	}
	want := `{"loopbackPorts":[43123,48081],"allowLocalBinding":false,"allowUnixSockets":["/tmp/allowed.sock"],"dangerouslyAllowAllUnixSockets":true}`
	if string(encoded) != want {
		t.Fatalf("context wire form = %s, want %s", encoded, want)
	}

	var legacy ManagedNetworkSandboxContext
	if err := json.Unmarshal([]byte(`{"loopbackPorts":[43123],"allowLocalBinding":true}`), &legacy); err != nil {
		t.Fatalf("Unmarshal(legacy context) error = %v", err)
	}
	if len(legacy.AllowUnixSockets) != 0 || legacy.DangerouslyAllowAllUnixSockets {
		t.Fatalf("legacy context = %#v, want restrictive unix socket defaults", legacy)
	}
}

// TestManagedNetworkSandboxContextJSONRequiresEnforcementLikeRust pins the
// gate: only a proxy-routed launch hands the context to the sandbox helper.
func TestManagedNetworkSandboxContextJSONRequiresEnforcementLikeRust(t *testing.T) {
	context := &ManagedNetworkSandboxContext{
		LoopbackPorts:                  []uint16{3128},
		AllowUnixSockets:               []string{"/tmp/allowed.sock"},
		DangerouslyAllowAllUnixSockets: true,
	}
	if got, err := managedNetworkSandboxContextJSON(&ExecParams{ManagedNetwork: context}); err != nil || got != "" {
		t.Fatalf("unenforced launch = %q, %v; want an empty context", got, err)
	}
	got, err := managedNetworkSandboxContextJSON(&ExecParams{EnforceManagedNetwork: true, ManagedNetwork: context})
	if err != nil {
		t.Fatalf("managedNetworkSandboxContextJSON() error = %v", err)
	}
	if !strings.Contains(got, `"dangerouslyAllowAllUnixSockets":true`) || !strings.Contains(got, `"allowUnixSockets":["/tmp/allowed.sock"]`) {
		t.Fatalf("context JSON = %q", got)
	}
}
