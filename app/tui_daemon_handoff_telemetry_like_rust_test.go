package app

import (
	"context"
	"io"
	"os"
	"testing"

	"codex_go/appserverdaemon"
	codextui "codex_go/tui"
)

// recordingTUIHandoffSink captures the launch provider's counters so the TUI
// handoff observation can be pinned without an exporter.
type recordingTUIHandoffSink struct {
	names    []string
	tags     []map[string]string
	onRecord func()
}

func (s *recordingTUIHandoffSink) Counter(name string, inc int, tags map[string]string) {
	s.names = append(s.names, name)
	cloned := make(map[string]string, len(tags))
	for key, value := range tags {
		cloned[key] = value
	}
	s.tags = append(s.tags, cloned)
	if s.onRecord != nil {
		s.onRecord()
	}
}

// Rust #46126: the foreground TUI records one codex.daemon.update counter for a
// requested daemon update handoff, with the bounded settings tags, the handoff
// initiation source, the selected target and the unconfirmed-handoff outcome.
func TestTUIDaemonUpdateHandoffObservationsLikeRust(t *testing.T) {
	codexHome := t.TempDir()
	sink := &recordingTUIHandoffSink{}
	telemetry := interactiveTUITelemetry{
		sink:         sink,
		settingsTags: appserverdaemon.DaemonSettingsTagMap(codexHome),
	}

	recordTUIDaemonUpdateHandoff(telemetry, codextui.DaemonUpdateSourcePublicStable)
	recordTUIDaemonUpdateHandoff(telemetry, codextui.DaemonUpdateSourceThisCli)
	if len(sink.names) != 2 || sink.names[0] != daemonUpdateMetric || sink.names[1] != daemonUpdateMetric {
		t.Fatalf("emitted names = %v, want two %s counters", sink.names, daemonUpdateMetric)
	}
	for index, wantTarget := range []string{daemonUpdateTargetPublicStable, daemonUpdateTargetThisCLI} {
		tags := sink.tags[index]
		if tags["initiation_source"] != "tui_handoff" {
			t.Fatalf("tags[%d] initiation_source = %q, want tui_handoff", index, tags["initiation_source"])
		}
		if tags["outcome"] != "handoff_requested" {
			t.Fatalf("tags[%d] outcome = %q, want handoff_requested", index, tags["outcome"])
		}
		if tags["update_target"] != wantTarget {
			t.Fatalf("tags[%d] update_target = %q, want %q", index, tags["update_target"], wantTarget)
		}
		for _, key := range []string{"auto_update", "auto_update_setting", "update_interval_setting", "shutdown_grace_setting"} {
			if _, ok := tags[key]; !ok {
				t.Fatalf("tags[%d] is missing the bounded settings tag %q: %#v", index, key, tags)
			}
		}
		for _, key := range []string{"terminal_name", "multiplexer", "app_server_mode", "terminal_columns", "terminal_rows"} {
			if _, ok := tags[key]; ok {
				t.Fatalf("tags[%d] leaked the launch-only tag %q: %#v", index, key, tags)
			}
		}
	}
}

// Without a launch provider the TUI reports nothing instead of failing the
// update relaunch.
func TestTUIDaemonUpdateHandoffWithoutProviderIsSilentLikeRust(t *testing.T) {
	recordTUIDaemonUpdateHandoff(interactiveTUITelemetry{}, codextui.DaemonUpdateSourcePublicStable)
}

// The handoff observation is recorded before the CLI relaunch, so a failed
// relaunch still shows a requested handoff, and non-daemon actions report none.
func TestTUIDaemonUpdateHandoffPrecedesRelaunchLikeRust(t *testing.T) {
	order := []string{}
	sink := &recordingTUIHandoffSink{onRecord: func() { order = append(order, "telemetry") }}
	originalExecutable := daemonUpdateExecutable
	originalRunner := daemonUpdateRunner
	daemonUpdateExecutable = func() (string, error) { return "/usr/local/bin/codex", nil }
	daemonUpdateRunner = func(context.Context, string, []string, []string, io.Writer, io.Writer) error {
		order = append(order, "relaunch")
		return nil
	}
	t.Cleanup(func() {
		daemonUpdateExecutable = originalExecutable
		daemonUpdateRunner = originalRunner
	})
	telemetry := interactiveTUITelemetry{
		sink:         sink,
		settingsTags: appserverdaemon.DaemonSettingsTagMap(os.Getenv("CODEX_HOME")),
	}
	if err := runDaemonUpdateActionWithTelemetry(context.Background(), telemetry, codextui.UpdateActionDaemonThisCli, io.Discard, io.Discard); err != nil {
		t.Fatalf("runDaemonUpdateActionWithTelemetry error = %v", err)
	}
	if len(order) != 2 || order[0] != "telemetry" || order[1] != "relaunch" {
		t.Fatalf("order = %v, want telemetry before relaunch", order)
	}
	if len(sink.names) != 1 || sink.names[0] != daemonUpdateMetric {
		t.Fatalf("emitted names = %v, want one %s counter", sink.names, daemonUpdateMetric)
	}
	if err := runDaemonUpdateActionWithTelemetry(context.Background(), telemetry, codextui.UpdateActionNPMGlobalLatest, io.Discard, io.Discard); err != nil {
		t.Fatalf("runDaemonUpdateActionWithTelemetry(non-daemon) error = %v", err)
	}
	if len(sink.names) != 1 {
		t.Fatalf("a non-daemon action emitted %v", sink.names)
	}
	if len(order) != 2 {
		t.Fatalf("a non-daemon action relaunched the CLI: %v", order)
	}
}
