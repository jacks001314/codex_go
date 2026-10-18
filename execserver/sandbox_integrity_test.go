package execserver

import (
	"runtime"
	"testing"
	"time"

	"codex_go/metrics"
	"codex_go/sandbox"
)

type integrityMetricCapture struct {
	counters   []string
	durations  []string
	histograms []string
}

func (c *integrityMetricCapture) Counter(name string, inc int, tags map[string]string) {
	c.counters = append(c.counters, name)
}

func (c *integrityMetricCapture) RecordDuration(name string, duration time.Duration, tags map[string]string) {
	c.durations = append(c.durations, name)
}

func (c *integrityMetricCapture) Histogram(name string, value int, tags map[string]string) {
	c.histograms = append(c.histograms, name)
}

// Mirrors Rust #51843: exec-server preparation observes sandbox integrity for
// eligible restricted profiles without returning an error or changing the plan.
func TestRunSandboxIntegrityChecksObservesPreparationLikeRust(t *testing.T) {
	capture := &integrityMetricCapture{}
	previous := metrics.InstallGlobal(capture)
	t.Cleanup(func() { metrics.InstallGlobal(previous) })

	cwd := t.TempDir()
	profile := sandbox.PermissionProfile{SandboxPolicy: sandbox.NewWorkspaceWritePolicy()}
	plan := &sandbox.CommandRunPlan{
		SandboxType:       execserverPlatformSandboxType(),
		PermissionProfile: &profile,
		CWD:               cwd,
		Command:           []string{"echo", "integrity-ok"},
	}
	runSandboxIntegrityChecks(plan, "", cwd, plan.Command)
	if len(capture.durations) == 0 && len(capture.counters) == 0 {
		t.Fatalf("integrity runner emitted no metrics; counters=%v durations=%v", capture.counters, capture.durations)
	}
}

func TestRunSandboxIntegrityChecksSkipsFullAccessLikeRust(t *testing.T) {
	capture := &integrityMetricCapture{}
	previous := metrics.InstallGlobal(capture)
	t.Cleanup(func() { metrics.InstallGlobal(previous) })

	cwd := t.TempDir()
	profile := sandbox.FullAccessPermissionProfile()
	plan := &sandbox.CommandRunPlan{
		SandboxType:       execserverPlatformSandboxType(),
		PermissionProfile: &profile,
		CWD:               cwd,
		Command:           []string{"echo", "integrity-ok"},
	}
	runSandboxIntegrityChecks(plan, "", cwd, plan.Command)
	if len(capture.durations) != 0 || len(capture.counters) != 0 {
		t.Fatalf("full-access request emitted metrics: %#v", capture)
	}
}

func execserverPlatformSandboxType() sandbox.SandboxType {
	switch runtime.GOOS {
	case "linux":
		return sandbox.SandboxTypeLinuxSeccomp
	case "darwin":
		return sandbox.SandboxTypeMacosSeatbelt
	case "windows":
		return sandbox.SandboxTypeWindowsRestrictedToken
	default:
		return sandbox.SandboxTypeNone
	}
}
