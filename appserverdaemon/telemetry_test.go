package appserverdaemon

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// TestDaemonSettingsTagsDistinguishPresenceFromDefaultsLikeRust mirrors Rust
// settings_tests::telemetry_distinguishes_presence_from_default_values.
func TestDaemonSettingsTagsDistinguishPresenceFromDefaultsLikeRust(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, StateDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	path := filepath.Join(dir, SettingsFileName)
	for _, testCase := range []struct {
		contents string
		presence string
	}{
		{`{}`, "default"},
		{`{"updater":{"autoUpdateEnabled":true,"updateIntervalMinutes":60},"shutdownGraceSeconds":60}`, "configured"},
	} {
		if err := os.WriteFile(path, []byte(testCase.contents), 0o600); err != nil {
			t.Fatalf("WriteFile error = %v", err)
		}
		tags := DaemonSettingsTags(home)
		values := make([]string, 0, len(tags))
		for _, tag := range tags {
			values = append(values, tag[1])
		}
		want := []string{"enabled", testCase.presence, testCase.presence, testCase.presence}
		if !reflect.DeepEqual(values, want) {
			t.Fatalf("DaemonSettingsTags(%s) values = %v, want %v", testCase.contents, values, want)
		}
	}
	// The tag keys are fixed and ordered, which is what the metric tags depend on.
	keys := make([]string, 0, 4)
	for _, tag := range DaemonSettingsTags(home) {
		keys = append(keys, tag[0])
	}
	wantKeys := []string{"auto_update", "auto_update_setting", "update_interval_setting", "shutdown_grace_setting"}
	if !reflect.DeepEqual(keys, wantKeys) {
		t.Fatalf("DaemonSettingsTags keys = %v, want %v", keys, wantKeys)
	}
}

// TestDaemonSettingsTagsUnknownWhenInvalidLikeRust pins the caller fallback: an
// unreadable or invalid snapshot reports the "unknown" placeholders instead of
// guessing.
func TestDaemonSettingsTagsUnknownWhenInvalidLikeRust(t *testing.T) {
	home := t.TempDir()
	dir := filepath.Join(home, StateDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("MkdirAll error = %v", err)
	}
	// A missing file means defaults, not unknown.
	for _, tag := range DaemonSettingsTags(home) {
		if tag[1] == unknownSettingTag {
			t.Fatalf("missing settings tag %q = unknown, want defaults", tag[0])
		}
	}
	path := filepath.Join(dir, SettingsFileName)
	if err := os.WriteFile(path, []byte(`{"updater":{"updateIntervalMinutes":0}}`), 0o600); err != nil {
		t.Fatalf("WriteFile error = %v", err)
	}
	for _, tag := range DaemonSettingsTags(home) {
		if tag[1] != unknownSettingTag {
			t.Fatalf("invalid settings tag %q = %q, want unknown", tag[0], tag[1])
		}
	}
}

// TestDaemonSettingsTagMapLikeRust pins the tag-map form used with a metric sink.
func TestDaemonSettingsTagMapLikeRust(t *testing.T) {
	home := t.TempDir()
	tags := DaemonSettingsTagMap(home)
	if len(tags) != 4 || tags["auto_update"] != "enabled" || tags["auto_update_setting"] != "default" {
		t.Fatalf("DaemonSettingsTagMap() = %#v", tags)
	}
}

// TestWithoutEnvVarLikeRust pins the handoff-marker removal, including that a
// different variable sharing the prefix survives.
func TestWithoutEnvVarLikeRust(t *testing.T) {
	env := []string{"A=1", TelemetryHandoffEnv + "=1", "B=2", TelemetryHandoffEnv + "=0"}
	want := []string{"A=1", "B=2"}
	if got := withoutEnvVar(env, TelemetryHandoffEnv); !reflect.DeepEqual(got, want) {
		t.Fatalf("withoutEnvVar() = %v, want %v", got, want)
	}
	prefixed := []string{TelemetryHandoffEnv + "_X=1"}
	if got := withoutEnvVar(prefixed, TelemetryHandoffEnv); !reflect.DeepEqual(got, prefixed) {
		t.Fatalf("withoutEnvVar(prefix-only) = %v, want %v", got, prefixed)
	}
	if got := withoutEnvVar(nil, TelemetryHandoffEnv); got != nil {
		t.Fatalf("withoutEnvVar(nil) = %v, want nil", got)
	}
}
