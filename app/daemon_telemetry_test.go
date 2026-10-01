package app

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"codex_go/appserverdaemon"
	"codex_go/cli"
)

// writeDaemonSettings writes a daemon settings snapshot the telemetry tags read.
func writeDaemonSettings(t *testing.T, codexHome string, contents string) {
	t.Helper()
	dir := filepath.Join(codexHome, appserverdaemon.StateDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, appserverdaemon.SettingsFileName), []byte(contents), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
}

// TestRecordDaemonStartTelemetryLikeRust pins the TUI auto-start counter, its
// initiation source, and the lifecycle-status outcome mapping (Rust
// tui/src/daemon_telemetry.rs).
func TestRecordDaemonStartTelemetryLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	// The TUI is the foreground observer, so a stray handoff marker must not
	// suppress its report.
	t.Setenv(appserverdaemon.TelemetryHandoffEnv, "1")
	sink, shutdown := stubDaemonMetricSink(t)

	recordDaemonStartTelemetry(nil, &appserverdaemon.LifecycleOutput{Status: appserverdaemon.StatusStarted}, nil)
	if len(sink.names) != 1 || sink.names[0] != daemonStartMetric || sink.counts[0] != 1 {
		t.Fatalf("emitted names=%v counts=%v, want one %s counter", sink.names, sink.counts, daemonStartMetric)
	}
	tags := sink.tags[0]
	if tags["initiation_source"] != daemonStartInitiationSource || tags["outcome"] != "started" {
		t.Fatalf("tags = %#v", tags)
	}
	if !*shutdown {
		t.Error("the short-lived provider was not shut down")
	}
}

// TestDaemonStartOutcomeLikeRust pins Rust's lifecycle-status mapping.
func TestDaemonStartOutcomeLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		output *appserverdaemon.LifecycleOutput
		err    error
		want   string
	}{
		{"started", &appserverdaemon.LifecycleOutput{Status: appserverdaemon.StatusStarted}, nil, "started"},
		{"already-running", &appserverdaemon.LifecycleOutput{Status: appserverdaemon.StatusAlreadyRunning}, nil, "already_running"},
		{"restarted", &appserverdaemon.LifecycleOutput{Status: appserverdaemon.StatusRestarted}, nil, "restarted"},
		{"stopped-is-unconfirmed", &appserverdaemon.LifecycleOutput{Status: appserverdaemon.StatusStopped}, nil, "unconfirmed"},
		{"not-running-is-unconfirmed", &appserverdaemon.LifecycleOutput{Status: appserverdaemon.StatusNotRunning}, nil, "unconfirmed"},
		{"running-is-unconfirmed", &appserverdaemon.LifecycleOutput{Status: appserverdaemon.StatusRunning}, nil, "unconfirmed"},
		{"failed", nil, errors.New("boom"), "failed"},
		{"missing-output", nil, nil, "unconfirmed"},
	} {
		if got := daemonStartOutcome(testCase.output, testCase.err); got != testCase.want {
			t.Errorf("%s outcome = %q, want %q", testCase.name, got, testCase.want)
		}
	}
}

type recordingDaemonMetricSink struct {
	names  []string
	counts []int
	tags   []map[string]string
}

func (s *recordingDaemonMetricSink) Counter(name string, inc int, tags map[string]string) {
	s.names = append(s.names, name)
	s.counts = append(s.counts, inc)
	clone := make(map[string]string, len(tags))
	for key, value := range tags {
		clone[key] = value
	}
	s.tags = append(s.tags, clone)
}

func stubDaemonMetricSink(t *testing.T) (*recordingDaemonMetricSink, *bool) {
	t.Helper()
	original := openDaemonMetricSink
	sink := &recordingDaemonMetricSink{}
	shutdown := false
	openDaemonMetricSink = func(*cli.RootOptions, bool, string) (daemonMetricSink, func(), bool) {
		return sink, func() { shutdown = true }, true
	}
	t.Cleanup(func() { openDaemonMetricSink = original })
	return sink, &shutdown
}

// TestRecordDaemonUpdateTelemetryLikeRust pins the counter, the bounded settings
// tags, the CLI initiation source, and the provider shutdown
// (Rust cli/src/daemon_telemetry.rs).
func TestRecordDaemonUpdateTelemetryLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv(appserverdaemon.TelemetryHandoffEnv, "")
	sink, shutdown := stubDaemonMetricSink(t)

	recordDaemonUpdateTelemetry(nil, false, daemonUpdateTargetPublicStable, &appserverdaemon.UpdateOutput{Status: appserverdaemon.UpdateUpdated}, nil)
	if len(sink.names) != 1 || sink.names[0] != daemonUpdateMetric || sink.counts[0] != 1 {
		t.Fatalf("emitted names=%v counts=%v, want one %s counter", sink.names, sink.counts, daemonUpdateMetric)
	}
	tags := sink.tags[0]
	for key, want := range map[string]string{
		"initiation_source":       "cli",
		"update_target":           daemonUpdateTargetPublicStable,
		"outcome":                 "updated",
		"auto_update":             "enabled",
		"auto_update_setting":     "default",
		"update_interval_setting": "default",
		"shutdown_grace_setting":  "default",
	} {
		if tags[key] != want {
			t.Errorf("tag %s = %q, want %q", key, tags[key], want)
		}
	}
	if !*shutdown {
		t.Error("the short-lived provider was not shut down")
	}
}

// TestRecordDaemonUpdateTelemetryReadsSettingsTagsLikeRust pins that a configured
// settings file reports "configured" presence.
func TestRecordDaemonUpdateTelemetryReadsSettingsTagsLikeRust(t *testing.T) {
	home := t.TempDir()
	writeDaemonSettings(t, home, `{"updater":{"autoUpdateEnabled":false,"updateIntervalMinutes":5},"shutdownGraceSeconds":10}`)
	t.Setenv("CODEX_HOME", home)
	t.Setenv(appserverdaemon.TelemetryHandoffEnv, "")
	sink, _ := stubDaemonMetricSink(t)

	recordDaemonUpdateTelemetry(nil, false, daemonUpdateTargetThisCLI, nil, errors.New("boom"))
	tags := sink.tags[0]
	for key, want := range map[string]string{
		"update_target":           daemonUpdateTargetThisCLI,
		"outcome":                 "unconfirmed",
		"auto_update":             "disabled",
		"auto_update_setting":     "configured",
		"update_interval_setting": "configured",
		"shutdown_grace_setting":  "configured",
	} {
		if tags[key] != want {
			t.Errorf("tag %s = %q, want %q", key, tags[key], want)
		}
	}
}

// TestRecordDaemonUpdateTelemetrySkipsWhenHandedOffLikeRust pins that the
// foreground handoff marker suppresses the child's report.
func TestRecordDaemonUpdateTelemetrySkipsWhenHandedOffLikeRust(t *testing.T) {
	t.Setenv("CODEX_HOME", t.TempDir())
	t.Setenv(appserverdaemon.TelemetryHandoffEnv, "1")
	sink, shutdown := stubDaemonMetricSink(t)

	recordDaemonUpdateTelemetry(nil, false, daemonUpdateTargetPublicStable, &appserverdaemon.UpdateOutput{Status: appserverdaemon.UpdateUpdated}, nil)
	if len(sink.names) != 0 {
		t.Fatalf("emitted %v despite the handoff marker", sink.names)
	}
	if *shutdown {
		t.Error("no provider should have been opened, so none should be shut down")
	}
}

// TestDaemonUpdateOutcomeLikeRust pins Rust's outcome classification.
func TestDaemonUpdateOutcomeLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		output *appserverdaemon.UpdateOutput
		err    error
		want   string
	}{
		{"updated", &appserverdaemon.UpdateOutput{Status: appserverdaemon.UpdateUpdated}, nil, "updated"},
		{"no-update", &appserverdaemon.UpdateOutput{Status: appserverdaemon.UpdateNoUpdate}, nil, "no_update"},
		{"unsupported", &appserverdaemon.UpdateOutput{Status: appserverdaemon.UpdateUnsupported}, nil, "unsupported"},
		{"cancelled", nil, nil, "cancelled"},
		{"unconfirmed", nil, errors.New("boom"), "unconfirmed"},
	} {
		if got := daemonUpdateOutcome(testCase.output, testCase.err); got != testCase.want {
			t.Errorf("%s outcome = %q, want %q", testCase.name, got, testCase.want)
		}
	}
}
