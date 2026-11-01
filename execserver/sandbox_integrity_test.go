package execserver

import (
	"runtime"
	"slices"
	"sync"
	"testing"
	"time"

	"codex_go/metrics"
	"codex_go/sandbox"
)

type integrityMetricCapture struct {
	mu         sync.Mutex
	counters   []string
	durations  []string
	histograms []string
}

func (c *integrityMetricCapture) Counter(name string, _ int, _ map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.counters = append(c.counters, name)
}

func (c *integrityMetricCapture) RecordDuration(name string, _ time.Duration, _ map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.durations = append(c.durations, name)
}

func (c *integrityMetricCapture) Histogram(name string, _ int, _ map[string]string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.histograms = append(c.histograms, name)
}

// snapshot copies the recorded names: requests running in other tests keep
// reporting metrics through the globally installed capture.
func (c *integrityMetricCapture) snapshot() (counters, durations []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.counters), slices.Clone(c.durations)
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
	runSandboxIntegrityChecks(plan, "", cwd, plan.Command, nil)
	counters, durations := capture.snapshot()
	if len(durations) == 0 && len(counters) == 0 {
		t.Fatalf("integrity runner emitted no metrics; counters=%v durations=%v", counters, durations)
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
	runSandboxIntegrityChecks(plan, "", cwd, plan.Command, nil)
	counters, durations := capture.snapshot()
	if len(durations) != 0 || len(counters) != 0 {
		t.Fatalf("full-access request emitted metrics; counters=%v durations=%v", counters, durations)
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
