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
	all  []recordingTUISinkCall
}

type recordingTUISinkCall struct {
	name string
	inc  int
	tags map[string]string
}

func (s *recordingTUISink) Counter(name string, inc int, tags map[string]string) {
	s.name = name
	s.inc = inc
	s.tags = tags
	s.all = append(s.all, recordingTUISinkCall{name: name, inc: inc, tags: cloneTUITelemetryTags(tags)})
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
	var start *recordingTUISinkCall
	for index := range sink.all {
		if sink.all[index].name == codextui.TUIStartMetric {
			start = &sink.all[index]
		}
	}
	if start == nil || start.inc != 1 {
		t.Fatalf("start observation = %#v, want one codex.tui.start", start)
	}
	for _, key := range []string{
		"auto_update", "auto_update_setting", "update_interval_setting", "shutdown_grace_setting",
		codextui.TerminalColumnsTag, codextui.TerminalRowsTag,
		"daemon_selection_reason", "daemon_auto_start", "app_server_mode", "terminal_name", "multiplexer",
	} {
		if _, ok := start.tags[key]; !ok {
			t.Fatalf("start tags missing %q: %#v", key, start.tags)
		}
	}
	if start.tags["daemon_selection_reason"] != "auto_start_disabled" || start.tags["app_server_mode"] != "in_process" {
		t.Fatalf("start tags = %#v", start.tags)
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
	var embedded *recordingTUISinkCall
	for index := range sink.all {
		if sink.all[index].name == codextui.TUIEmbeddedStartMetric {
			embedded = &sink.all[index]
		}
	}
	if embedded == nil || embedded.inc != 1 || embedded.tags["reason"] != "auto_start_disabled_no_daemon" {
		t.Fatalf("embedded start observation = %#v, want reason auto_start_disabled_no_daemon", embedded)
	}
	if telemetry.exporter.Kind != config.OtelExporterKindOtlpHTTP {
		t.Fatalf("exporter = %#v", telemetry.exporter)
	}
	if telemetry.analyticsEnabled == nil || *telemetry.analyticsEnabled {
		t.Fatalf("analytics = %#v", telemetry.analyticsEnabled)
	}
	telemetry.close()
}

// TestEmbeddedTUIStartReasonLikeRust pins Rust #57268's bounded reason tag,
// including the --search-only distinction and embedded starts never carrying a
// reason when the connection is shared.
func TestEmbeddedTUIStartReasonLikeRust(t *testing.T) {
	previousAutoStart := daemonAutoStartFeature
	previousExclusion := daemonAutoStartExclusionReason
	previousElevation := daemonElevationDetector
	t.Cleanup(func() {
		daemonAutoStartFeature = previousAutoStart
		daemonAutoStartExclusionReason = previousExclusion
		daemonElevationDetector = previousElevation
	})
	daemonElevationDetector = func() bool { return false }
	daemonAutoStartFeature = func(*cli.RootOptions) bool { return true }
	cases := []struct {
		name      string
		root      *cli.RootOptions
		exclusion string
		selection string
		elevated  bool
		want      string
	}{
		{name: "no daemon", root: &cli.RootOptions{Shared: cli.SharedOptions{NoDaemon: true}}, selection: "explicit_no_daemon", want: "explicit_no_daemon"},
		{name: "profile", root: &cli.RootOptions{Shared: cli.SharedOptions{Profile: "work"}}, selection: "incompatible_option", want: "profile"},
		{name: "oss", root: &cli.RootOptions{Shared: cli.SharedOptions{OSS: true}}, selection: "incompatible_option", want: "oss"},
		{name: "strict config", root: &cli.RootOptions{StrictConfig: true}, selection: "incompatible_option", want: "strict_config"},
		{name: "search only", root: &cli.RootOptions{Shared: cli.SharedOptions{Search: true}}, selection: "incompatible_option", want: "search"},
		{name: "search and override", root: &cli.RootOptions{ConfigOverrides: []string{"model=\"o3\""}, Shared: cli.SharedOptions{Search: true}}, selection: "incompatible_option", want: "config_override"},
		{name: "wsl drvfs", exclusion: wslDrvfsExclusion, selection: "auto_start", want: "wsl_drvfs"},
		{name: "detach restricted", exclusion: detachedLaunchRestrictionExclusion, selection: "auto_start", want: "windows_detach_restricted"},
		{name: "elevated", elevated: true, selection: "elevated_windows", want: "windows_elevated"},
		{name: "auto start disabled", selection: "auto_start_disabled", want: "auto_start_disabled_no_daemon"},
		{name: "daemon failed", selection: "auto_start", want: "daemon_connection_failed"},
		{name: "agents", selection: "agents", want: "agents_no_daemon_discovered"},
		{name: "unknown", selection: "other", want: "unclassified"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			daemonAutoStartExclusionReason = tc.exclusion
			if tc.elevated {
				daemonElevationDetector = func() bool { return true }
			} else {
				daemonElevationDetector = func() bool { return false }
			}
			t.Cleanup(func() { daemonElevationDetector = func() bool { return false } })
			if got := interactiveEmbeddedTUIStartReason(tc.root, tc.selection); got != tc.want {
				t.Fatalf("reason = %q, want %q", got, tc.want)
			}
		})
	}
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
