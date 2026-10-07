package appserver

import (
	"testing"
	"time"

	"codex_go/execserver"
	"codex_go/turn"
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

// TestProvisionedEnvironmentTurnDialSitesLikeRust covers Rust #48575
// (`985cf47a4e`) at the remaining provisioned Noise dial sites: the turn's
// environment snapshot carries the record's provisioned flag, so the tools that
// later dial that environment (unified exec, the environment filesystem and
// remote skills discovery) all give a resuming executor the fixed five-minute
// window instead of the ordinary retry limits. Ordinary records keep the
// caller's budget verbatim.
//
// Rust reaches every one of these sites through the same Deferred transport
// (`ExecServerClient::connect_for_transport`), so the flag is a property of the
// environment, not of one call site.
func TestProvisionedEnvironmentTurnDialSitesLikeRust(t *testing.T) {
	manager := NewEnvironmentManager(EnvironmentShellInfo{Name: "sh", Path: "/bin/sh"}, "/workspace")
	registration, err := manager.RegisterDeferredNoiseEnvironment("tools", &failingNoiseProvider{})
	if err != nil {
		t.Fatalf("RegisterDeferredNoiseEnvironment() error = %v", err)
	}
	if err := registration.CompleteReady(provisionedReadyInfo("root", "tools")); err != nil {
		t.Fatalf("CompleteReady() error = %v", err)
	}
	ordinary := EnvironmentRecord{EnvironmentID: "plain"}
	manager.records["plain"] = ordinary

	router := NewRuntimeRouter(RuntimeServices{Environment: manager})
	environments := router.unifiedExecEnvironmentsForTurn(&turn.TurnStartParams{
		Environments: []map[string]any{{"environmentId": "tools"}},
	})
	if len(environments) != 1 {
		t.Fatalf("unified exec environments = %#v, want the provisioned environment", environments)
	}
	if !environments[0].Provisioned {
		t.Fatal("provisioned environment was not marked on the turn's environment snapshot")
	}
	if got := environments[0].ConnectBudget(time.Second); got != execserver.ProvisionedEnvironmentConnectTimeout() {
		t.Fatalf("provisioned connect budget = %v, want %v", got, execserver.ProvisionedEnvironmentConnectTimeout())
	}

	plain := router.unifiedExecEnvironmentsForTurn(&turn.TurnStartParams{
		Environments: []map[string]any{{"environmentId": "plain"}},
	})
	if len(plain) != 1 {
		t.Fatalf("unified exec environments = %#v, want the ordinary environment", plain)
	}
	if plain[0].Provisioned {
		t.Fatal("ordinary environment was marked provisioned")
	}
	if got := plain[0].ConnectBudget(time.Second); got != time.Second {
		t.Fatalf("ordinary connect budget = %v, want the caller's budget", got)
	}
}
