package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

type sandboxStateProbeStub struct {
	mu    sync.Mutex
	calls int
}

func (s *sandboxStateProbeStub) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls
}

func stubSandboxStateProbe(t *testing.T, fn func(string) bool) *sandboxStateProbeStub {
	t.Helper()
	previous := mcpSandboxStateProbe
	stub := &sandboxStateProbeStub{}
	mcpSandboxStateProbe = func(executable string) bool {
		stub.mu.Lock()
		stub.calls++
		stub.mu.Unlock()
		return fn(executable)
	}
	t.Cleanup(func() { mcpSandboxStateProbe = previous })
	return stub
}

// TestSupportsSandboxStateCachesProbePerPathLikeRust mirrors Rust's
// sandbox_executable::PROBES OnceCell map: a completed probe is cached
// process-wide by absolute executable path, so the same binary is spawned once.
func TestSupportsSandboxStateCachesProbePerPathLikeRust(t *testing.T) {
	stub := stubSandboxStateProbe(t, func(string) bool { return true })
	exe := filepath.Join(t.TempDir(), "codex-probe-cache.exe")

	if !supportsSandboxState(exe) {
		t.Fatal("first probe should report support")
	}
	if !supportsSandboxState(exe) {
		t.Fatal("cached probe should still report support")
	}
	if calls := stub.count(); calls != 1 {
		t.Fatalf("probe calls = %d, want 1 (Rust caches completed probes per path)", calls)
	}
}

func TestSupportsSandboxStateNotInferredFromNameOrPath(t *testing.T) {
	stub := stubSandboxStateProbe(t, func(string) bool { return false })
	exe := filepath.Join(t.TempDir(), "codex.exe")

	if supportsSandboxState(exe) {
		t.Fatal("a binary that fails the probe must not be advertised")
	}
	if supportsSandboxState("") {
		t.Fatal("an empty path must never be presented as a verified launcher")
	}
	if calls := stub.count(); calls != 1 {
		t.Fatalf("probe calls = %d, want 1 (empty path is rejected without probing)", calls)
	}
}

// TestSupportsSandboxStateNeverProbesItsOwnTestBinary documents the guard that
// keeps a Go test binary from re-entering its own test run: the test binary
// stops flag parsing at the first non-flag argument and would then execute the
// whole suite.
func TestSupportsSandboxStateNeverProbesItsOwnTestBinary(t *testing.T) {
	self, err := os.Executable()
	if err != nil || strings.TrimSpace(self) == "" {
		t.Skip("this process has no resolvable executable path")
	}
	stub := stubSandboxStateProbe(t, func(string) bool {
		t.Fatal("the probe must not spawn this process's own binary")
		return true
	})
	if supportsSandboxState(self) {
		t.Fatal("our own test binary must not be advertised as a sandbox launcher")
	}
	if calls := stub.count(); calls != 0 {
		t.Fatalf("probe calls = %d, want 0", calls)
	}
}

// TestSandboxLauncherExecutableEligibility mirrors
// PreparedMcpCall::sandbox_codex_executable (#52182): only a local stdio server
// with an absolute path to a probe-passing CLI receives a launcher. HTTP
// transports, remote environments, relative paths and failing probes all omit
// it rather than falling back to PATH.
func TestSandboxLauncherExecutableEligibility(t *testing.T) {
	stubSandboxStateProbe(t, func(exe string) bool { return filepath.Base(exe) == "supported.exe" })
	dir := t.TempDir()
	supported := filepath.Join(dir, "supported.exe")
	unsupported := filepath.Join(dir, "unsupported.exe")

	cases := []struct {
		name          string
		executable    string
		environmentID string
		localStdio    bool
		want          string
	}{
		{name: "local stdio with verified cli", executable: supported, environmentID: DefaultMCPServerEnvironmentID, localStdio: true, want: supported},
		{name: "empty environment defaults to local", executable: supported, environmentID: "", localStdio: true, want: supported},
		{name: "remote environment", executable: supported, environmentID: "attached-env", localStdio: true, want: ""},
		{name: "http transport shares no filesystem", executable: supported, environmentID: DefaultMCPServerEnvironmentID, localStdio: false, want: ""},
		{name: "relative path never qualifies", executable: "codex", environmentID: DefaultMCPServerEnvironmentID, localStdio: true, want: ""},
		{name: "binary without sandbox-state support", executable: unsupported, environmentID: DefaultMCPServerEnvironmentID, localStdio: true, want: ""},
		{name: "empty executable", executable: "", environmentID: DefaultMCPServerEnvironmentID, localStdio: true, want: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := sandboxLauncherExecutable(tc.executable, tc.environmentID, tc.localStdio); got != tc.want {
				t.Fatalf("sandboxLauncherExecutable(%q, %q, %t) = %q, want %q", tc.executable, tc.environmentID, tc.localStdio, got, tc.want)
			}
		})
	}
}

// TestProbeSandboxStateExecutableRejectsMissingBinary exercises the real probe
// against a path that cannot be executed: the capability is never assumed.
func TestProbeSandboxStateExecutableRejectsMissingBinary(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "definitely-missing-codex-binary")
	if probeSandboxStateExecutable(missing) {
		t.Fatal("a missing binary must not be reported as supporting sandbox state")
	}
}
