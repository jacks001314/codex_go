package appserver

import (
	"testing"

	"codex_go/execserver"
)

// TestProvisionedEnvironmentConnectWindowLikeRust covers Rust #48575
// (`985cf47a4e`) at the app-server boundary: a provisioned environment whose
// provisioning has completed marks its initial Noise connection so its executor
// gets the fixed five-minute `environment_offline` window, and the surrounding
// environment/info + environment/status request is bounded by that same window
// instead of the ordinary connect timeout (which would otherwise cancel it).
// Ordinary records keep the configured timeout verbatim.
//
// Rust does this in ExecServerClient::connect_for_transport by building a
// provisioning_deadline from the Deferred readiness handle
// (codex-rs/exec-server/src/client_transport.rs).
func TestProvisionedEnvironmentConnectWindowLikeRust(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "")
	provider := &failingNoiseProvider{}
	registration, err := manager.RegisterDeferredNoiseEnvironment("tools", provider)
	if err != nil {
		t.Fatalf("RegisterDeferredNoiseEnvironment() error = %v", err)
	}

	// Pending provisioning: not ready, so it keeps the ordinary timeout and is
	// not marked provisioned (Rust waits on readiness before connecting).
	pending := manager.records["tools"]
	if pending.provisionedReady() {
		t.Fatal("pending environment reported as provisioned ready")
	}
	if got := environmentConnectDeadlineForRecord(&pending); got != defaultEnvironmentConnectTimeout {
		t.Fatalf("pending deadline = %v, want %v", got, defaultEnvironmentConnectTimeout)
	}
	if options := provisionedNoiseDialOptions(&pending, "codex-go"); options.Provisioned {
		t.Fatal("pending environment marked its dial as provisioned")
	}

	if err := registration.CompleteReady(provisionedReadyInfo("root", "tools")); err != nil {
		t.Fatalf("CompleteReady() error = %v", err)
	}

	ready := manager.records["tools"]
	if !ready.provisionedReady() {
		t.Fatal("completed provisioning did not report the environment ready")
	}
	options := provisionedNoiseDialOptions(&ready, "codex-go")
	if !options.Provisioned {
		t.Fatal("provisioned-ready environment did not mark its initial connection")
	}
	if got := environmentConnectDeadlineForRecord(&ready); got != execserver.ProvisionedEnvironmentConnectTimeout() {
		t.Fatalf("provisioned deadline = %v, want %v", got, execserver.ProvisionedEnvironmentConnectTimeout())
	}
	if execserver.ProvisionedEnvironmentConnectTimeout() <= defaultEnvironmentConnectTimeout {
		t.Fatalf("provisioned window %v must exceed the ordinary timeout %v",
			execserver.ProvisionedEnvironmentConnectTimeout(), defaultEnvironmentConnectTimeout)
	}

	// An ordinary (non-provisioned) record keeps the existing behavior.
	ordinary := EnvironmentRecord{EnvironmentID: "remote"}
	if ordinary.provisionedReady() {
		t.Fatal("ordinary record reported as provisioned ready")
	}
	if got := environmentConnectDeadlineForRecord(&ordinary); got != defaultEnvironmentConnectTimeout {
		t.Fatalf("ordinary deadline = %v, want %v", got, defaultEnvironmentConnectTimeout)
	}
	if options := provisionedNoiseDialOptions(&ordinary, "codex-go"); options.Provisioned {
		t.Fatal("ordinary record marked its dial as provisioned")
	}
}
