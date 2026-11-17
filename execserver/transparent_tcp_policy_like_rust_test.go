package execserver

import (
	"encoding/json"
	"runtime"
	"strings"
	"testing"

	"codex_go/network"
)

// TestEnvironmentCapabilitiesTransparentTCPLikeRust pins Rust #61851: only a
// Linux executor advertises transparent TCP capture.
func TestEnvironmentCapabilitiesTransparentTCPLikeRust(t *testing.T) {
	info := localEnvironmentInfo()
	if info == nil {
		t.Fatal("localEnvironmentInfo() = nil")
	}
	want := runtime.GOOS == "linux"
	if info.Capabilities.NetworkProxyTransparentTCP != want {
		t.Fatalf("networkProxyTransparentTCP = %v, want %v", info.Capabilities.NetworkProxyTransparentTCP, want)
	}
	encoded, err := json.Marshal(info.Capabilities)
	if err != nil {
		t.Fatalf("marshal capabilities: %v", err)
	}
	if want != strings.Contains(string(encoded), `"networkProxyTransparentTCP":true`) {
		t.Fatalf("wire capabilities = %s", encoded)
	}
}

// TestExecParamsRequestsTransparentTCPLikeRust pins both request sources.
func TestExecParamsRequestsTransparentTCPLikeRust(t *testing.T) {
	if (&ExecParams{}).requestsTransparentTCP() {
		t.Fatal("empty params requested transparent TCP")
	}
	if !(&ExecParams{ManagedNetwork: &ManagedNetworkSandboxContext{TransparentTCP: true}}).requestsTransparentTCP() {
		t.Fatal("managed network context did not request transparent TCP")
	}
	if !(&ExecParams{NetworkProxy: &RemoteNetworkProxyLaunchConfig{Proxy: RemoteNetworkProxyConfig{TransparentTCP: true}}}).requestsTransparentTCP() {
		t.Fatal("proxy launch did not request transparent TCP")
	}
}

// TestPrepareExecProcessRejectsUnsupportedTransparentTCPLikeRust pins the
// Rust #61851 launch guard.
func TestPrepareExecProcessRejectsUnsupportedTransparentTCPLikeRust(t *testing.T) {
	_, _, _, err := prepareExecProcessWithTransparentTCP(&ExecParams{
		Argv:           []string{"true"},
		ManagedNetwork: &ManagedNetworkSandboxContext{TransparentTCP: true},
	}, "", false)
	if err == nil || !strings.Contains(err.Error(), "transparent TCP requires a managed Linux sandbox") {
		t.Fatalf("error = %v, want the managed Linux sandbox requirement", err)
	}
	if runtime.GOOS != "linux" {
		_, _, _, err = prepareExecProcessWithTransparentTCP(&ExecParams{
			Argv:           []string{"true"},
			Sandbox:        json.RawMessage(`{"permissions":{"type":"managed","file_system":{"type":"unrestricted"},"network":"enabled"}}`),
			ManagedNetwork: &ManagedNetworkSandboxContext{TransparentTCP: true},
		}, "", false)
		if err == nil || !strings.Contains(err.Error(), "transparent TCP requires a managed Linux sandbox") {
			t.Fatalf("sandboxed error = %v, want the managed Linux sandbox requirement", err)
		}
	}
}

// TestRemoteNetworkProxyTransparentTCPRoundTripLikeRust pins the remote wire
// field added by Rust #61851.
func TestRemoteNetworkProxyTransparentTCPRoundTripLikeRust(t *testing.T) {
	config, err := RemoteNetworkProxyConfigFromProxyConfig(proxyConfigWithTransparentTCPForTest())
	if err != nil {
		t.Fatalf("RemoteNetworkProxyConfigFromProxyConfig() error = %v", err)
	}
	if !config.TransparentTCP {
		t.Fatal("remote config dropped transparent_tcp")
	}
	encoded, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal remote config: %v", err)
	}
	if !strings.Contains(string(encoded), `"transparentTcp":true`) {
		t.Fatalf("remote config wire = %s", encoded)
	}
	roundTrip, err := config.ProxyConfig()
	if err != nil {
		t.Fatalf("ProxyConfig() error = %v", err)
	}
	if !roundTrip.Network.TransparentTCP {
		t.Fatal("executor-side config lost transparent_tcp")
	}
}

func proxyConfigWithTransparentTCPForTest() network.ProxyConfig {
	settings := network.DefaultProxySettings()
	settings.Enabled = true
	settings.TransparentTCP = true
	return network.ProxyConfig{Network: settings}
}
