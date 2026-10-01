package appserverdaemon

import (
	"context"
	"errors"
	"testing"
)

// TestEnsureNonElevatedRefusesDaemonLifecycleLikeRust pins every entry point the
// Windows daemon gates on a non-elevated launcher (Rust
// backend::windows::ensure_not_elevated call sites), and that stop/version stay
// usable because Rust's run(command) gates only Start|Restart.
func TestEnsureNonElevatedRefusesDaemonLifecycleLikeRust(t *testing.T) {
	original := currentProcessElevated
	currentProcessElevated = func() bool { return true }
	t.Cleanup(func() { currentProcessElevated = original })

	home := t.TempDir()
	runner := NewLifecycleRunnerForCodexHome(home, "")
	for _, testCase := range []struct {
		name string
		run  func() error
	}{
		{"run(start)", func() error { _, err := runner.Run(LifecycleStart); return err }},
		{"run(restart)", func() error { _, err := runner.Run(LifecycleRestart); return err }},
		{"bootstrap", func() error { _, err := runner.Bootstrap(nil); return err }},
		{"start-with-features", func() error { _, err := runner.StartWithFeatures(nil); return err }},
		{"restart-with-features", func() error { _, err := runner.RestartWithFeatures(nil); return err }},
		{"ensure-remote-control-ready", func() error { _, err := runner.EnsureRemoteControlStarted(); return err }},
		{"set-remote-control", func() error { _, err := runner.SetRemoteControl(RemoteControlEnabled); return err }},
		{"pid-update-loop", func() error { return RunPIDUpdateLoop(context.Background(), runner, nil) }},
		{"request-manual-update", func() error { _, err := RequestManualUpdate(context.Background(), home, nil); return err }},
		{"update-from-cli", func() error { _, err := UpdateFromCLI(home, nil); return err }},
	} {
		if err := testCase.run(); !errors.Is(err, ErrElevatedDaemonLauncher) {
			t.Errorf("%s error = %v, want the elevation refusal", testCase.name, err)
		}
	}

	for _, testCase := range []struct {
		name string
		run  func() error
	}{
		{"run(version)", func() error { _, err := runner.Run(LifecycleVersion); return err }},
		{"run(stop)", func() error { _, err := runner.Run(LifecycleStop); return err }},
	} {
		if err := testCase.run(); errors.Is(err, ErrElevatedDaemonLauncher) {
			t.Errorf("%s error = %v, want no elevation refusal", testCase.name, err)
		}
	}
}

// TestEnsureNonElevatedAllowsANonElevatedLauncher pins the accept path.
func TestEnsureNonElevatedAllowsANonElevatedLauncher(t *testing.T) {
	original := currentProcessElevated
	currentProcessElevated = func() bool { return false }
	t.Cleanup(func() { currentProcessElevated = original })
	if err := EnsureNonElevated(); err != nil {
		t.Fatalf("EnsureNonElevated() error = %v, want nil", err)
	}
}
