package app

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/appserverdaemon"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	codextui "codex_go/tui"
)

type recordingTUISink struct {
	name string
	inc  int
	tags map[string]string
}

func (s *recordingTUISink) Counter(name string, inc int, tags map[string]string) {
	s.name = name
	s.inc = inc
	s.tags = tags
}

func writeTUITelemetryConfig(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	content := "[analytics]\nenabled = false\n\n[otel.metrics_exporter.otlp-http]\nendpoint = \"http://127.0.0.1:4318/v1/metrics\"\nprotocol = \"json\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(content), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
}

// TestInteractiveTUIStartTelemetryLikeRust mirrors Rust #53859's launch
// observation: codex.tui.start carries the bounded daemon settings, terminal
// size buckets, selection reason and terminal categories, and the returned
// context drops only the size buckets.
func TestInteractiveTUIStartTelemetryLikeRust(t *testing.T) {
	if got := auth.DefaultCodexHome(); got == "" {
		t.Fatal("DefaultCodexHome() is empty")
	}
	writeTUITelemetryConfig(t)
	sink := &recordingTUISink{}
	previousSink := openTUIMetricSink
	openTUIMetricSink = func(cfg *config.Config) (tuiMetricSink, func(), bool) {
		if cfg == nil {
			t.Fatal("nil config")
		}
		return sink, func() {}, true
	}
	t.Cleanup(func() { openTUIMetricSink = previousSink })

	telemetry := startInteractiveTUITelemetry(&cli.RootOptions{}, "auto_start_disabled", "in_process")
	if sink.name != codextui.TUIStartMetric || sink.inc != 1 {
		t.Fatalf("start observation = %q x%d", sink.name, sink.inc)
	}
	for _, key := range []string{
		"auto_update", "auto_update_setting", "update_interval_setting", "shutdown_grace_setting",
		codextui.TerminalColumnsTag, codextui.TerminalRowsTag,
		"daemon_selection_reason", "daemon_auto_start", "app_server_mode", "terminal_name", "multiplexer",
	} {
		if _, ok := sink.tags[key]; !ok {
			t.Fatalf("start tags missing %q: %#v", key, sink.tags)
		}
	}
	if sink.tags["daemon_selection_reason"] != "auto_start_disabled" || sink.tags["app_server_mode"] != "in_process" {
		t.Fatalf("start tags = %#v", sink.tags)
	}
	if telemetry.context == nil {
		t.Fatal("launch context is nil")
	}
	for _, key := range []string{codextui.TerminalColumnsTag, codextui.TerminalRowsTag} {
		if _, ok := telemetry.context[key]; ok {
			t.Fatalf("launch context kept %q: %#v", key, telemetry.context)
		}
	}
	if telemetry.context["app_server_mode"] != "in_process" {
		t.Fatalf("launch context = %#v", telemetry.context)
	}
	if telemetry.exporter.Kind != config.OtelExporterKindOtlpHTTP {
		t.Fatalf("exporter = %#v", telemetry.exporter)
	}
	if telemetry.analyticsEnabled == nil || *telemetry.analyticsEnabled {
		t.Fatalf("analytics = %#v", telemetry.analyticsEnabled)
	}
	telemetry.close()
}

// TestInteractiveDaemonSelectionReasonLikeRust pins the startup_orchestration
// selection_reason match.
func TestInteractiveDaemonSelectionReasonLikeRust(t *testing.T) {
	previousAutoStart := daemonAutoStartFeature
	previousExclusion := daemonAutoStartExclusionReason
	previousElevation := daemonElevationDetector
	t.Cleanup(func() {
		daemonAutoStartFeature = previousAutoStart
		daemonAutoStartExclusionReason = previousExclusion
		daemonElevationDetector = previousElevation
	})
	daemonElevationDetector = func() bool { return false }
	endpoint := appserverdaemon.NewUnixSocketEndpoint("/tmp/codex.sock")
	cases := []struct {
		name      string
		endpoint  *appserverdaemon.RemoteAppServerEndpoint
		explicit  bool
		elevated  bool
		autoStart bool
		exclusion string
		noDaemon  bool
		want      string
	}{
		{name: "explicit remote", endpoint: endpoint, explicit: true, want: "explicit_remote"},
		{name: "elevated", elevated: true, want: "elevated_windows"},
		{name: "no daemon", noDaemon: true, exclusion: "--no-daemon", want: "explicit_no_daemon"},
		{name: "incompatible", exclusion: "feature settings", want: "incompatible_option"},
		{name: "auto start", autoStart: true, want: "auto_start"},
		{name: "existing daemon", endpoint: endpoint, want: "existing_daemon"},
		{name: "embedded", want: "auto_start_disabled"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := &cli.RootOptions{}
			if tc.noDaemon {
				root.Shared.NoDaemon = true
			}
			daemonAutoStartFeature = func(*cli.RootOptions) bool { return tc.autoStart }
			daemonAutoStartExclusionReason = tc.exclusion
			if tc.elevated {
				daemonElevationDetector = func() bool { return true }
			} else {
				daemonElevationDetector = func() bool { return false }
			}
			if got := interactiveDaemonSelectionReason(root, tc.endpoint, tc.explicit); got != tc.want {
				t.Fatalf("selection reason = %q, want %q", got, tc.want)
			}
		})
	}
}
