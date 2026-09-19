package network

import "testing"

// TestProxySettingsPreserveOmittedAndEmptyUnixSocketPolicyLikeRust mirrors Rust
// #46004 at the parsed-config layer: omission of
// `dangerously_allow_all_unix_sockets` is distinct from an explicit false, and
// an explicitly empty `unix_sockets` table survives as a restrictive ceiling.
func TestProxySettingsPreserveOmittedAndEmptyUnixSocketPolicyLikeRust(t *testing.T) {
	omitted, err := ProxyConfigFromConfigValues(map[string]any{
		"features": map[string]any{"network_proxy": map[string]any{"enabled": true}},
	})
	if err != nil {
		t.Fatalf("ProxyConfigFromConfigValues(omitted) error = %v", err)
	}
	if omitted.Network.DangerouslyAllowAllUnixSockets != nil {
		t.Fatalf("omitted flag should stay absent: %#v", omitted.Network.DangerouslyAllowAllUnixSockets)
	}
	if omitted.Network.UnixSockets != nil {
		t.Fatalf("omitted socket table should stay absent: %#v", omitted.Network.UnixSockets)
	}

	explicitFalse, err := ProxyConfigFromConfigValues(map[string]any{
		"features": map[string]any{"network_proxy": map[string]any{
			"enabled":                            true,
			"dangerously_allow_all_unix_sockets": false,
		}},
	})
	if err != nil {
		t.Fatalf("ProxyConfigFromConfigValues(explicit false) error = %v", err)
	}
	if explicitFalse.Network.DangerouslyAllowAllUnixSockets == nil || *explicitFalse.Network.DangerouslyAllowAllUnixSockets {
		t.Fatalf("explicit false must stay present: %#v", explicitFalse.Network.DangerouslyAllowAllUnixSockets)
	}

	emptySockets, err := ProxyConfigFromConfigValues(map[string]any{
		"features": map[string]any{"network_proxy": map[string]any{
			"enabled":      true,
			"unix_sockets": map[string]any{},
		}},
	})
	if err != nil {
		t.Fatalf("ProxyConfigFromConfigValues(empty sockets) error = %v", err)
	}
	if emptySockets.Network.UnixSockets == nil {
		t.Fatal("explicitly empty socket table must be retained")
	}
	if len(emptySockets.Network.UnixSockets.Entries) != 0 {
		t.Fatalf("empty socket table should carry no entries: %#v", emptySockets.Network.UnixSockets.Entries)
	}
}

// Mirrors Rust #46004's `set_allow_unix_sockets`: an explicitly empty list is a
// present-but-empty ceiling rather than an omitted policy.
func TestSetAllowUnixSocketsRetainsExplicitlyEmptyCeilingLikeRust(t *testing.T) {
	settings := DefaultProxySettings()
	settings.SetAllowUnixSockets(nil)
	if settings.UnixSockets == nil {
		t.Fatal("explicit empty allow list must be retained")
	}
	if len(settings.UnixSockets.Entries) != 0 {
		t.Fatalf("empty allow list should carry no entries: %#v", settings.UnixSockets.Entries)
	}

	settings.SetAllowUnixSockets([]string{"/tmp/allowed.sock"})
	if len(settings.UnixSockets.Entries) != 1 || settings.UnixSockets.Entries["/tmp/allowed.sock"] != ProxyUnixSocketAllow {
		t.Fatalf("allow list not applied: %#v", settings.UnixSockets.Entries)
	}
}
