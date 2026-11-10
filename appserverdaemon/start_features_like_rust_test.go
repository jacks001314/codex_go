package appserverdaemon

import (
	"testing"
	"time"
)

// TestLifecycleRunnerStartApplyingFeaturesLikeRust mirrors Rust #60489: a
// fresh `start` saves the launch's feature toggles, and a later start of the
// already running daemon keeps the process and its saved overrides.
func TestLifecycleRunnerStartApplyingFeaturesLikeRust(t *testing.T) {
	stub := stubLifecycleManagedDaemon(t)
	home := t.TempDir()
	daemon := NewDaemonForCodexHome(home, "codex-go-test")
	runner := NewLifecycleRunner(daemon)
	runner.Now = func() time.Time { return fixedDaemonTime() }
	if err := SaveSettings(daemon.Paths.SettingsFile, &DaemonSettings{FeatureOverrides: map[string]bool{"connectors": true}}); err != nil {
		t.Fatalf("SaveSettings() error = %v", err)
	}
	stub.appRunning = false
	stub.socketReady = false

	if _, err := runner.StartApplyingFeatures(map[string]bool{"apps": true, "auth_elicitation": false}); err != nil {
		t.Fatalf("StartApplyingFeatures() error = %v", err)
	}
	if !stub.appRunning {
		t.Fatal("start did not launch the managed backend")
	}
	settings, err := LoadSettings(daemon.Paths.SettingsFile)
	if err != nil {
		t.Fatalf("LoadSettings() error = %v", err)
	}
	want := map[string]bool{"apps": true, "auth_elicitation": false}
	if !FeatureOverridesEqual(settings.FeatureOverrides, want) {
		t.Fatalf("stored overrides = %#v, want %#v", settings.FeatureOverrides, want)
	}

	stopsBefore := stub.appStops
	if _, err := runner.StartApplyingFeatures(map[string]bool{"apps": false}); err != nil {
		t.Fatalf("second StartApplyingFeatures() error = %v", err)
	}
	if stub.appStops != stopsBefore {
		t.Fatalf("start reused the running daemon but restarted it: stops = %d, want %d", stub.appStops, stopsBefore)
	}
	settings, err = LoadSettings(daemon.Paths.SettingsFile)
	if err != nil {
		t.Fatalf("LoadSettings() error = %v", err)
	}
	if !FeatureOverridesEqual(settings.FeatureOverrides, want) {
		t.Fatalf("running daemon overrides changed to %#v, want %#v", settings.FeatureOverrides, want)
	}
}

// TestLifecycleRunnerStartRejectsNonToggleFeatureLikeRust mirrors Rust #60489:
// start and restart reject keys that are not boolean feature toggles.
func TestLifecycleRunnerStartRejectsNonToggleFeatureLikeRust(t *testing.T) {
	stub := stubLifecycleManagedDaemon(t)
	home := t.TempDir()
	daemon := NewDaemonForCodexHome(home, "codex-go-test")
	runner := NewLifecycleRunner(daemon)
	runner.Now = func() time.Time { return fixedDaemonTime() }
	stub.appRunning = false
	stub.socketReady = false
	if err := SaveSettings(daemon.Paths.SettingsFile, &DaemonSettings{FeatureOverrides: map[string]bool{"apps": true}}); err != nil {
		t.Fatalf("SaveSettings() error = %v", err)
	}

	if _, err := runner.StartApplyingFeatures(map[string]bool{"not_a_feature": true}); err == nil {
		t.Fatal("StartApplyingFeatures() accepted a non-toggle feature key")
	}
	if stub.appRunning {
		t.Fatal("rejected start launched the managed backend")
	}
	settings, err := LoadSettings(daemon.Paths.SettingsFile)
	if err != nil {
		t.Fatalf("LoadSettings() error = %v", err)
	}
	if !FeatureOverridesEqual(settings.FeatureOverrides, map[string]bool{"apps": true}) {
		t.Fatalf("rejected start changed saved overrides: %#v", settings.FeatureOverrides)
	}
}
