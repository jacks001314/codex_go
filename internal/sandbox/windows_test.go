package sandbox

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestManagerSetupLifecycle(t *testing.T) {
	manager := NewWindowsManager("")
	if manager.Readiness().Status != WindowsReadinessNotConfigured {
		t.Fatalf("initial readiness = %s", manager.Readiness().Status)
	}
	cwd := filepath.Clean(t.TempDir())
	started, err := manager.StartSetup(&WindowsSetupStartParams{Mode: WindowsSetupElevated, CWD: &cwd})
	if err != nil {
		t.Fatalf("StartSetup() error = %v", err)
	}
	if !started.Started {
		t.Fatalf("Started = false, want true")
	}
	started, err = manager.StartSetup(&WindowsSetupStartParams{Mode: WindowsSetupUnelevated})
	if err != nil {
		t.Fatalf("StartSetup(second) error = %v", err)
	}
	if started.Started {
		t.Fatalf("second Started = true, want false")
	}
	completed, err := manager.CompleteSetup(true, "")
	if err != nil {
		t.Fatalf("CompleteSetup() error = %v", err)
	}
	if completed.Mode != WindowsSetupElevated || !completed.Success || manager.Readiness().Status != WindowsReadinessReady {
		t.Fatalf("completed = %+v readiness = %+v", completed, manager.Readiness())
	}
}

func TestManagerValidation(t *testing.T) {
	manager := NewWindowsManager(WindowsReadinessNotConfigured)
	if _, err := manager.StartSetup(nil); !errors.Is(err, ErrInvalidWindowsSandboxRequest) {
		t.Fatalf("StartSetup(nil) error = %v, want ErrInvalidWindowsSandboxRequest", err)
	}
	if _, err := manager.StartSetup(&WindowsSetupStartParams{Mode: "bad"}); !errors.Is(err, ErrInvalidWindowsSandboxRequest) {
		t.Fatalf("StartSetup(bad mode) error = %v, want ErrInvalidWindowsSandboxRequest", err)
	}
	cwd := "relative"
	if _, err := manager.StartSetup(&WindowsSetupStartParams{Mode: WindowsSetupElevated, CWD: &cwd}); !errors.Is(err, ErrInvalidWindowsSandboxRequest) {
		t.Fatalf("StartSetup(relative cwd) error = %v, want ErrInvalidWindowsSandboxRequest", err)
	}
	if _, err := manager.CompleteSetup(false, "no active"); !errors.Is(err, ErrInvalidWindowsSandboxRequest) {
		t.Fatalf("CompleteSetup(no active) error = %v, want ErrInvalidWindowsSandboxRequest", err)
	}
}

func TestWarningTruncatesSamples(t *testing.T) {
	warning := WindowsSandboxWarning([]string{"a", "b", "c"}, true, 2)
	if len(warning.SamplePaths) != 2 || warning.ExtraCount != 1 || !warning.FailedScan {
		t.Fatalf("warning = %+v", warning)
	}
	warning.SamplePaths[0] = "mutated"
	again := WindowsSandboxWarning([]string{"a", "b"}, false, 10)
	if again.SamplePaths[0] != "a" {
		t.Fatalf("WindowsSandboxWarning leaked mutation")
	}
}
