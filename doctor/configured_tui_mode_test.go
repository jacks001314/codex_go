package doctor

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"codex_go/cli"
	"codex_go/config"
)

// Mirrors Rust #50200 `doctor_reports_configured_tui_mode`
// (cli/tests/doctor_path_safety.rs): `tui.fullscreen_transcript` selects the
// reported TUI mode.
func TestConfigCheckReportsConfiguredTUIModeLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		setting  string
		expected string
	}{
		{"true", "fullscreen"},
		{"false", "scrollback"},
	} {
		home := t.TempDir()
		body := "[tui]\nfullscreen_transcript = " + testCase.setting + "\n"
		if err := os.WriteFile(config.ConfigPath(home), []byte(body), 0o600); err != nil {
			t.Fatalf("WriteFile returned error: %v", err)
		}
		check := configCheck(home, &Options{})
		if check.Status != CheckStatusOK {
			t.Fatalf("config.load status = %v, details = %#v", check.Status, check.Details)
		}
		if !containsDetail(check, "configured TUI mode: "+testCase.expected) {
			t.Fatalf("fullscreen_transcript=%s details = %#v, want configured TUI mode: %s", testCase.setting, check.Details, testCase.expected)
		}
	}
}

// Rust's `Tui::fullscreen_transcript` carries `#[serde(default =
// "default_true")]`, so an absent `[tui]` table or an absent key reports
// fullscreen.
func TestConfigCheckConfiguredTUIModeDefaultsToFullscreenLikeRust(t *testing.T) {
	home := t.TempDir()
	check := configCheck(home, &Options{})
	if !containsDetail(check, "configured TUI mode: fullscreen") {
		t.Fatalf("details = %#v, want the fullscreen default without a config", check.Details)
	}

	if err := os.WriteFile(config.ConfigPath(home), []byte("[tui]\nanimations = false\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	check = configCheck(home, &Options{})
	if !containsDetail(check, "configured TUI mode: fullscreen") {
		t.Fatalf("details = %#v, want the fullscreen default when the key is absent", check.Details)
	}
}

// The human report renders the new configuration row with the aligned label
// column, matching the Rust human snapshot that gained
// "configured TUI mode      fullscreen".
func TestRenderHumanShowsConfiguredTUIModeRowLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(config.ConfigPath(home), []byte("[tui]\nfullscreen_transcript = false\n"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	report := &Report{
		SchemaVersion: 1,
		CodexVersion:  "test",
		OverallStatus: CheckStatusOK,
		Checks:        []*DoctorCheck{configCheck(home, &Options{})},
	}
	rendered := RenderHuman(report, &Options{ASCII: true})
	want := fmt.Sprintf("      %-24s %s", "configured TUI mode", "scrollback")
	if !strings.Contains(rendered, want) {
		t.Fatalf("human report missing %q:\n%s", want, rendered)
	}
}

// Mirrors the Rust integration assertion exactly: the JSON report exposes the
// mode at checks.config.load.details["configured TUI mode"], including when the
// value arrives through a `-c` style configuration override.
func TestConfigCheckReportsConfiguredTUIModeInJSONLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		setting  string
		expected string
	}{
		{"true", "fullscreen"},
		{"false", "scrollback"},
	} {
		home := t.TempDir()
		check := configCheck(home, &Options{
			Root: cli.RootOptions{ConfigOverrides: []string{"tui.fullscreen_transcript=" + testCase.setting}},
		})
		raw, err := json.Marshal(JSONReportFromReport(&Report{
			SchemaVersion: 1,
			CodexVersion:  "0.0.0",
			OverallStatus: CheckStatusOK,
			Checks:        []*DoctorCheck{check},
		}))
		if err != nil {
			t.Fatalf("Marshal returned error: %v", err)
		}
		var decoded map[string]any
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatalf("Unmarshal returned error: %v", err)
		}
		checks, _ := decoded["checks"].(map[string]any)
		configLoad, _ := checks["config.load"].(map[string]any)
		details, _ := configLoad["details"].(map[string]any)
		if got := details["configured TUI mode"]; got != testCase.expected {
			t.Fatalf("checks.config.load.details[configured TUI mode] = %v, want %q", got, testCase.expected)
		}
	}
}
