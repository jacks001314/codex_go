package network

import (
	"encoding/json"
	"reflect"
	"runtime"
	"testing"
)

func unixSocketSettings() ProxySettings {
	allowAll := true
	return ProxySettings{
		AllowLocalBinding:              true,
		DangerouslyAllowAllUnixSockets: &allowAll,
		UnixSockets: &ProxyUnixSocketPermissions{Entries: map[string]ProxyUnixSocketPermission{
			"/tmp/allowed.sock": ProxyUnixSocketAllow,
			"/tmp/denied.sock":  ProxyUnixSocketDeny,
		}},
	}
}

// TestPrepareProxyManagedNetworkCarriesUnixSocketPolicyLikeRust mirrors Rust
// #45534 `prepare_for_optional_environment_preserves_effective_unix_socket_permissions`:
// the prepared sandbox context carries the allowed paths (allow entries only,
// sorted) and the allow-all flag so the Linux helper can honour an explicit
// grant.
func TestPrepareProxyManagedNetworkCarriesUnixSocketPolicyLikeRust(t *testing.T) {
	settings := unixSocketSettings()
	prepared := PrepareProxyManagedNetwork(nil, MustProxyTCPAddr("127.0.0.1", 43128), MustProxyTCPAddr("127.0.0.1", 48081), true, settings)
	context := prepared.SandboxContext
	if !context.AllowLocalBinding {
		t.Fatalf("AllowLocalBinding = false, want true")
	}
	if !reflect.DeepEqual(context.LoopbackPorts, []uint16{43128, 48081}) {
		t.Fatalf("LoopbackPorts = %#v", context.LoopbackPorts)
	}
	if runtime.GOOS == "windows" {
		if len(context.AllowUnixSockets) != 0 || context.DangerouslyAllowAllUnixSockets {
			t.Fatalf("Windows context = %#v, want restrictive defaults", context)
		}
		return
	}
	if !reflect.DeepEqual(context.AllowUnixSockets, []string{"/tmp/allowed.sock"}) {
		t.Fatalf("AllowUnixSockets = %#v, want only the allow entry", context.AllowUnixSockets)
	}
	if !context.DangerouslyAllowAllUnixSockets {
		t.Fatalf("DangerouslyAllowAllUnixSockets = false, want true")
	}
}

// TestPrepareProxyManagedNetworkKeepsUnixSocketsDeniedByDefaultLikeRust pins
// the restrictive default: no policy (or path-only grants) never grants
// standalone AF_UNIX sockets.
func TestPrepareProxyManagedNetworkKeepsUnixSocketsDeniedByDefaultLikeRust(t *testing.T) {
	for name, settings := range map[string]ProxySettings{
		"no policy": {},
		"path-only grants": {
			UnixSockets: &ProxyUnixSocketPermissions{Entries: map[string]ProxyUnixSocketPermission{
				"/tmp/allowed.sock": ProxyUnixSocketAllow,
			}},
		},
	} {
		t.Run(name, func(t *testing.T) {
			prepared := PrepareProxyManagedNetwork(nil, MustProxyTCPAddr("127.0.0.1", 3128), MustProxyTCPAddr("127.0.0.1", 3128), false, settings)
			if prepared.SandboxContext.DangerouslyAllowAllUnixSockets {
				t.Fatalf("context = %#v, want allow-all to stay false", prepared.SandboxContext)
			}
			encoded, err := json.Marshal(prepared.SandboxContext)
			if err != nil {
				t.Fatalf("Marshal(context) error = %v", err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(encoded, &decoded); err != nil {
				t.Fatalf("Unmarshal(context) error = %v", err)
			}
			if decoded["dangerouslyAllowAllUnixSockets"] != false {
				t.Fatalf("wire context = %s, want dangerouslyAllowAllUnixSockets:false", encoded)
			}
		})
	}
}
