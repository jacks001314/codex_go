package execserver

import (
	"context"
	"testing"

	"codex_go/network"
)

// Rust #62386: when the caller already supplied a prepared managed-network
// route, the executor passes that route and its environment through to the
// child instead of starting another proxy that would overwrite the settings
// bound to the command.
func TestPrepareExecutorNetworkProxyKeepsPreparedRouteLikeRust(t *testing.T) {
	server := NewServer()
	managed := &ManagedNetworkSandboxContext{
		LoopbackPorts:     []uint16{41234},
		AllowLocalBinding: true,
		AllowUnixSockets:  []string{"/tmp/agent.sock"},
	}
	boundEnv := map[string]string{
		"HTTP_PROXY":                         "http://127.0.0.1:41234",
		network.ProxyActiveEnvKey:            "1",
		network.CredentialBrokerActiveEnvKey: "1",
		"SHELL_TOKEN":                        "bound-for-shell",
	}
	params := &ExecParams{
		ProcessID:             "prepared-route",
		Env:                   boundEnv,
		EnforceManagedNetwork: true,
		ManagedNetwork:        managed,
	}
	prepared, proxy, policyCancel, err := server.prepareExecutorNetworkProxy(context.Background(), params)
	if err != nil {
		t.Fatal(err)
	}
	if proxy != nil || policyCancel != nil {
		t.Fatalf("caller-prepared route started another proxy: %#v cancel=%v", proxy, policyCancel)
	}
	for key, want := range boundEnv {
		if prepared.Env[key] != want {
			t.Fatalf("prepared env[%q] = %q, want %q", key, prepared.Env[key], want)
		}
	}
	if prepared.ManagedNetwork == nil || len(prepared.ManagedNetwork.LoopbackPorts) != 1 || prepared.ManagedNetwork.LoopbackPorts[0] != 41234 ||
		!prepared.ManagedNetwork.AllowLocalBinding || len(prepared.ManagedNetwork.AllowUnixSockets) != 1 || !prepared.EnforceManagedNetwork {
		t.Fatalf("prepared managed network = %#v", prepared)
	}
}
