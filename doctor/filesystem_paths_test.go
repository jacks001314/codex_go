package doctor

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"codex_go/config"
)

// Mirrors Rust #46543 filesystem_paths::probe_exit_code: a resolvable path exits
// 0, a missing path exits 2, and the helper never touches the filesystem on
// Windows (even drive-qualified paths can redirect to network shares).
func TestProbeFilesystemPathExitCodeLikeRust(t *testing.T) {
	dir := t.TempDir()
	present := filepath.Join(dir, "present")
	if err := os.WriteFile(present, []byte("x"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	missing := filepath.Join(dir, "missing")

	if runtime.GOOS == "windows" {
		for _, path := range []string{present, missing} {
			if got := ProbeFilesystemPathExitCode(path); got != filesystemProbeWindows {
				t.Fatalf("windows probe for %q = %d, want %d", path, got, filesystemProbeWindows)
			}
		}
		return
	}
	if got := ProbeFilesystemPathExitCode(present); got != filesystemProbeResolved {
		t.Fatalf("present probe = %d, want %d", got, filesystemProbeResolved)
	}
	if got := ProbeFilesystemPathExitCode(missing); got != filesystemProbeMissing {
		t.Fatalf("missing probe = %d, want %d", got, filesystemProbeMissing)
	}
}

// Mirrors Rust #46543 literal_paths: only literal grants are reported, deny
// rules and globs are excluded, and access modes merge per path.
func TestLiteralFilesystemProbeTargetsLikeRust(t *testing.T) {
	root := t.TempDir()
	readPath := filepath.Join(root, "readable")
	writePath := filepath.Join(root, "writable")
	deniedPath := filepath.Join(root, "denied")
	globPath := filepath.Join(root, "logs", "**")
	cfg := &config.Config{Values: map[string]any{
		"default_permissions": "dev",
		"permissions": map[string]any{
			"dev": map[string]any{
				"filesystem": map[string]any{
					readPath:   "read",
					writePath:  "write",
					deniedPath: "deny",
					globPath:   "deny",
				},
			},
		},
	}}

	targets, profileID, readRestricted := literalFilesystemProbeTargets(cfg, root)
	if profileID != "dev" {
		t.Fatalf("profile id = %q, want dev", profileID)
	}
	if !readRestricted {
		t.Fatal("a deny rule must mark the profile read restricted")
	}
	if len(targets) != 2 {
		t.Fatalf("targets = %#v, want the two literal grants", targets)
	}
	if targets[0].Path != filepath.Clean(readPath) || strings.Join(targets[0].Accesses, ",") != "read" {
		t.Fatalf("first target = %#v", targets[0])
	}
	if targets[1].Path != filepath.Clean(writePath) || strings.Join(targets[1].Accesses, ",") != "write" {
		t.Fatalf("second target = %#v", targets[1])
	}
}

// Mirrors Rust #46543's check: configured paths are listed with their access
// modes, budgets and provenance, and read restrictions or Windows keep the check
// to listing only. Missing paths alone never warn.
func TestFilesystemPathsCheckListsWithoutProbingLikeRust(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	body := "default_permissions = \"dev\"\n\n[permissions.dev.filesystem]\n" +
		quoteDoctorTOML(filepath.Join(root, "data")) + " = \"read\"\n"
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	opts := &Options{CodexHome: home}
	check := filesystemPathsCheck(home, opts)
	if check == nil || check.ID != "sandbox.filesystem_paths" || check.Category != "sandbox" {
		t.Fatalf("check = %#v", check)
	}
	details := strings.Join(check.Details, "\n")
	for _, want := range []string{
		"permission profile: dev",
		"operation: resolve paths only; read/write access is not tested",
		"probe budgets: 2 seconds per path, 8 seconds total, at most 32 paths",
		"source: effective filesystem policy (entry provenance unavailable)",
		"paths checked: 1 of 1",
	} {
		if !strings.Contains(details, want) {
			t.Fatalf("details missing %q:\n%s", want, details)
		}
	}
	if !strings.Contains(details, "(read)") {
		t.Fatalf("access mode missing:\n%s", details)
	}
	if runtime.GOOS == "windows" {
		if check.Summary != "configured filesystem paths listed; probes disabled on Windows" {
			t.Fatalf("windows summary = %q", check.Summary)
		}
		if !strings.Contains(details, "not probed on Windows (network authentication risk)") {
			t.Fatalf("windows probe detail missing:\n%s", details)
		}
	}
	if check.Status != CheckStatusOK || len(check.Issues) != 0 {
		t.Fatalf("check status = %q issues = %#v", check.Status, check.Issues)
	}
}

func quoteDoctorTOML(value string) string {
	return "\"" + strings.ReplaceAll(value, "\\", "\\\\") + "\""
}

// filesystemTestClock advances by step on every read so the per-path budget,
// total budget, and slow-probe rule stay deterministic.
func filesystemTestClock(step time.Duration) func() time.Time {
	now := time.Unix(0, 0)
	return func() time.Time {
		current := now
		now = now.Add(step)
		return current
	}
}

// writeFilesystemDoctorConfig points the profile at one literal read grant per
// name so tests can drive many probe targets.
func writeFilesystemDoctorConfig(t *testing.T, home string, root string, names []string) {
	t.Helper()
	body := "default_permissions = \"dev\"\n\n[permissions.dev.filesystem]\n"
	for _, name := range names {
		body += quoteDoctorTOML(filepath.Join(root, name)) + " = \"read\"\n"
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(body), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
}

// stubFilesystemProbes replaces the probe seams for one test.
func stubFilesystemProbes(t *testing.T, step time.Duration, runner func(string) filesystemProbeOutcome) {
	t.Helper()
	originalSupported := filesystemProbeSupported
	originalRunner := filesystemProbeRunner
	originalClock := filesystemProbeClock
	t.Cleanup(func() {
		filesystemProbeSupported = originalSupported
		filesystemProbeRunner = originalRunner
		filesystemProbeClock = originalClock
	})
	filesystemProbeSupported = func() bool { return true }
	filesystemProbeClock = filesystemTestClock(step)
	filesystemProbeRunner = func(path string, _ time.Duration) filesystemProbeOutcome {
		return runner(path)
	}
}

// Mirrors Rust #46543's probe outcomes: a timed-out helper warns, a denied path
// warns, a missing path is reported without a warning, and a resolved path
// stays clean.
func TestFilesystemPathsCheckProbeOutcomesLikeRust(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	names := []string{"blocked", "missing", "denied", "resolved"}
	writeFilesystemDoctorConfig(t, home, root, names)
	outcomes := map[string]filesystemProbeOutcome{
		filepath.Join(root, "blocked"):  filesystemOutcomeTimedOut,
		filepath.Join(root, "missing"):  filesystemOutcomeMissing,
		filepath.Join(root, "denied"):   filesystemOutcomeDenied,
		filepath.Join(root, "resolved"): filesystemOutcomeResolved,
	}
	stubFilesystemProbes(t, 0, func(path string) filesystemProbeOutcome {
		outcome, ok := outcomes[path]
		if !ok {
			t.Fatalf("unexpected probe target %q", path)
		}
		return outcome
	})

	check := filesystemPathsCheck(home, &Options{CodexHome: home})
	details := strings.Join(check.Details, "\n")
	for _, want := range []string{
		"path probe timed out",
		"missing (may be intentional)",
		"access denied to doctor",
		"path resolved (read/write access not tested)",
		"paths checked: 4 of 4",
	} {
		if !strings.Contains(details, want) {
			t.Fatalf("details missing %q:\n%s", want, details)
		}
	}
	if check.Status != CheckStatusWarning || check.Summary != "some configured filesystem paths are slow or could not be checked" {
		t.Fatalf("check status = %q summary = %q", check.Status, check.Summary)
	}
	if len(check.Issues) != 2 {
		t.Fatalf("issues = %#v, want one warning each for the blocked and denied paths", check.Issues)
	}
	for _, issue := range check.Issues {
		if strings.Contains(issue.Cause, "missing") {
			t.Fatalf("a missing path produced a warning: %#v", issue)
		}
		if issue.Remedy == nil || !strings.Contains(*issue.Remedy, "mount and host permissions") {
			t.Fatalf("issue remedy = %#v", issue.Remedy)
		}
	}
}

// Mirrors Rust #46543's probe ceiling: at most 32 paths are checked and the rest
// are reported as unchecked.
func TestFilesystemPathsCheckStopsAtProbeLimitLikeRust(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	names := make([]string, 0, maxFilesystemPaths+1)
	for index := 0; index <= maxFilesystemPaths; index++ {
		names = append(names, "path-"+time.Duration(index).String())
	}
	writeFilesystemDoctorConfig(t, home, root, names)
	stubFilesystemProbes(t, 0, func(string) filesystemProbeOutcome { return filesystemOutcomeResolved })

	check := filesystemPathsCheck(home, &Options{CodexHome: home})
	details := strings.Join(check.Details, "\n")
	if !strings.Contains(details, "paths checked: 32 of 33") {
		t.Fatalf("details = %s", details)
	}
	if !strings.Contains(details, "unchecked paths: 1 (probe budget exhausted)") {
		t.Fatalf("details = %s", details)
	}
	if check.Status != CheckStatusWarning || check.Summary != "filesystem path check is incomplete" {
		t.Fatalf("check status = %q summary = %q", check.Status, check.Summary)
	}
}

// Mirrors Rust #46543's total budget: a slow probe consumes the eight-second
// budget and the remaining paths stay unchecked.
func TestFilesystemPathsCheckStopsWhenTotalBudgetIsExhaustedLikeRust(t *testing.T) {
	home := t.TempDir()
	root := t.TempDir()
	writeFilesystemDoctorConfig(t, home, root, []string{"first", "second"})
	stubFilesystemProbes(t, 3*time.Second, func(string) filesystemProbeOutcome { return filesystemOutcomeResolved })

	check := filesystemPathsCheck(home, &Options{CodexHome: home})
	details := strings.Join(check.Details, "\n")
	if !strings.Contains(details, "paths checked: 1 of 2") {
		t.Fatalf("details = %s", details)
	}
	if !strings.Contains(details, "unchecked paths: 1 (probe budget exhausted)") {
		t.Fatalf("details = %s", details)
	}
	if !strings.Contains(details, "latency: over 1 second") {
		t.Fatalf("slow probe detail missing:\n%s", details)
	}
	if check.Summary != "filesystem path check is incomplete" {
		t.Fatalf("summary = %q", check.Summary)
	}
}
