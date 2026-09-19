package doctor

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

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
