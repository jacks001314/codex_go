package doctor

import (
	"os"
	"strings"
	"testing"

	"codex_go/cli"
	"codex_go/config"
	"codex_go/model"
)

// Rust parity: codex-rs/cli/tests/doctor_enterprise_network.rs (#57606).
// TestNetworkOnlyDoctorRunsFocusedChecksLikeRust pins the network-only check
// selection: effective configuration, network environment, Responses WebSocket
// and provider HTTP reachability, with no database, MCP or other scans.
func TestNetworkOnlyDoctorRunsFocusedChecksLikeRust(t *testing.T) {
	builder := NewBuilder()
	builder.httpClient = localOnlyHTTPClient(t)
	report := builder.Build(&Options{
		CodexHome:   t.TempDir(),
		NetworkOnly: true,
		Root: cli.RootOptions{
			Shared: cli.SharedOptions{OSSProvider: model.OllamaOSSProviderID},
		},
	})
	wantIDs := []string{
		"config.load",
		"network.env",
		"network.websocket_reachability",
		"network.provider_reachability",
	}
	if len(report.Checks) != len(wantIDs) {
		t.Fatalf("checks = %d (%v), want %d", len(report.Checks), checkIDs(report), len(wantIDs))
	}
	for index, want := range wantIDs {
		if got := report.Checks[index].ID; got != want {
			t.Fatalf("check %d = %q, want %q (%v)", index, got, want, checkIDs(report))
		}
	}
	if !containsDetail(report.Checks[0], "configuration load ms: ") {
		t.Fatalf("config check lost the load timing: %#v", report.Checks[0].Details)
	}
	for _, unwanted := range []string{"state.paths", "mcp.", "auth.", "installation", "system.disk"} {
		if hasCheckIDPrefix(report, unwanted) {
			t.Fatalf("network-only report included %q: %v", unwanted, checkIDs(report))
		}
	}
}

// TestNetworkOnlyDoctorStopsOnConfigFailureLikeRust pins the config failure
// arm: the redacted config error is reported with the network diagnostics
// note, the report stops after that single check, and no configuration value
// leaks into it.
func TestNetworkOnlyDoctorStopsOnConfigFailureLikeRust(t *testing.T) {
	const credential = "doctor-network-only-credential"
	home := t.TempDir()
	body := "custom_header = \"" + credential + "\" trailing\n"
	if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	report := NewBuilder().Build(&Options{
		CodexHome:   home,
		NetworkOnly: true,
		Root:        cli.RootOptions{},
	})
	if len(report.Checks) != 1 {
		t.Fatalf("checks = %v, want only the config check", checkIDs(report))
	}
	check := report.Checks[0]
	if check.ID != "config.load" || check.Status != CheckStatusFail {
		t.Fatalf("check = %s/%s, want config.load/fail", check.ID, check.Status)
	}
	if !containsDetail(check, "network diagnostics: effective network configuration is unavailable") {
		t.Fatalf("config check lost the network-only note: %#v", check.Details)
	}
	joined := strings.Join(check.Details, "\n")
	if strings.Contains(joined, credential) {
		t.Fatalf("config check leaked a configuration value:\n%s", joined)
	}
	if !containsDetail(check, "error: invalid configuration") {
		t.Fatalf("config check lost its typed error: %#v", check.Details)
	}
}

func hasCheckIDPrefix(report *Report, prefix string) bool {
	for _, check := range report.Checks {
		if strings.HasPrefix(check.ID, prefix) {
			return true
		}
	}
	return false
}
