package mxc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/sandbox"
	"codex_go/sandbox/windowssandbox"
)

// TestExpandVolumeRootPathsSkipsBrokenLinksLikeRust pins Rust #57515: only
// symbolic links are probed, unresolvable targets are skipped, and ordinary
// children plus valid links keep their generated grants.
func TestExpandVolumeRootPathsSkipsBrokenLinksLikeRust(t *testing.T) {
	root := t.TempDir()
	readable := filepath.Join(root, "readable")
	if err := os.WriteFile(readable, []byte("readable"), 0o600); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(readable, alias); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	dangling := filepath.Join(root, "dangling")
	if err := os.Symlink(filepath.Join(root, "missing"), dangling); err != nil {
		t.Skipf("symbolic links are unavailable: %v", err)
	}
	loop := filepath.Join(root, "loop")
	if err := os.Symlink(loop, loop); err != nil {
		t.Skipf("self-referential links are unavailable: %v", err)
	}

	expanded := expandVolumeRootPaths([]string{root}, []string{root})
	for _, path := range []string{root, readable, alias} {
		if _, ok := expanded[windowssandbox.CanonicalPathKey(path)]; !ok {
			t.Fatalf("expandVolumeRootPaths() dropped %q: %#v", path, expanded)
		}
	}
	for _, path := range []string{dangling, loop} {
		if _, ok := expanded[windowssandbox.CanonicalPathKey(path)]; ok {
			t.Fatalf("expandVolumeRootPaths() kept unresolvable link %q: %#v", path, expanded)
		}
	}
}

func TestPrepareFileSystemPolicySnapshotsDenyGlobsLikeRust(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	temp := filepath.Join(root, "temp")
	for _, path := range []string{workspace, temp} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(temp, "secret.key")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	profile := sandbox.WorkspaceWritePermissionProfile()
	profile.SandboxPolicy.WritableRoots = append(profile.SandboxPolicy.WritableRoots, temp)
	profile.DeniedReadEntries = []sandbox.FileSystemSandboxEntry{{
		Path:   sandbox.FileSystemPath{Type: "glob_pattern", Pattern: filepath.Join(temp, "*.key")},
		Access: sandbox.FileSystemAccessDeny,
	}}
	policy, err := PrepareFileSystemPolicy(&profile, workspace, map[string]string{
		"TEMP": temp,
		"TMP":  temp,
	}, workspace)
	if err != nil {
		t.Fatalf("PrepareFileSystemPolicy() error = %v", err)
	}
	foundDeny := false
	for _, path := range policy.DeniedPaths {
		if strings.EqualFold(path, secret) {
			foundDeny = true
		}
	}
	if !foundDeny {
		t.Fatalf("deny snapshot = %#v, want %q", policy.DeniedPaths, secret)
	}
	for _, path := range append(append([]string(nil), policy.ReadWritePaths...), policy.ReadOnlyPaths...) {
		if strings.EqualFold(path, secret) {
			t.Fatalf("denied path remained readable/writable: %#v", policy)
		}
	}
}

func TestPrepareEntriesPolicyUsesCommandEnvironmentAndSnapshotsDenies(t *testing.T) {
	root := t.TempDir()
	workspace := filepath.Join(root, "workspace")
	temp := filepath.Join(root, "temp")
	for _, path := range []string{workspace, temp} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	secret := filepath.Join(temp, "secret.env")
	if err := os.WriteFile(secret, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	prepareCWD := filepath.Join(root, "policy")
	if err := os.MkdirAll(prepareCWD, 0o755); err != nil {
		t.Fatal(err)
	}
	entries := []sandbox.FileSystemSandboxEntry{
		{
			Path:   sandbox.FileSystemPath{Type: "path", Path: workspace},
			Access: sandbox.FileSystemAccessWrite,
		},
		{
			Path:   sandbox.FileSystemPath{Type: "special", Value: &sandbox.FileSystemSpecialPath{Kind: "tmpdir"}},
			Access: sandbox.FileSystemAccessWrite,
		},
		{
			Path:   sandbox.FileSystemPath{Type: "glob_pattern", Pattern: filepath.Join(temp, "*.env")},
			Access: sandbox.FileSystemAccessDeny,
		},
	}
	policy, err := PrepareEntriesPolicy(entries, prepareCWD, map[string]string{
		"TEMP": temp,
		"TMP":  temp,
	}, workspace)
	if err != nil {
		t.Fatalf("PrepareEntriesPolicy() error = %v", err)
	}
	foundDeny := false
	for _, path := range policy.DeniedPaths {
		if strings.EqualFold(path, secret) {
			foundDeny = true
		}
	}
	if !foundDeny {
		t.Fatalf("deny snapshot = %#v, want %q", policy.DeniedPaths, secret)
	}
	foundWriteTemp := false
	for _, path := range policy.ReadWritePaths {
		if strings.EqualFold(path, secret) {
			t.Fatalf("denied path remained writable: %#v", policy)
		}
		if strings.EqualFold(path, temp) {
			foundWriteTemp = true
		}
	}
	if !foundWriteTemp {
		t.Fatalf("TEMP was not materialized as writable: %#v", policy.ReadWritePaths)
	}
}

// Mirrors Rust #44620: TEMP and TMP are resolved case-insensitively from the
// filtered command environment, /tmp contributes no Windows grant, and invalid
// temporary paths leave explicit denies intact.
func TestPrepareEntriesPolicyWindowsTemporaryRootsLikeRust(t *testing.T) {
	root := t.TempDir()
	first := filepath.Join(root, "temp", "~")
	second := filepath.Join(root, "tmp with spaces")
	denied := filepath.Join(first, "secret")
	for _, path := range []string{first, second} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(denied, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	prepareCWD := filepath.Join(root, "policy")
	if err := os.MkdirAll(prepareCWD, 0o755); err != nil {
		t.Fatal(err)
	}
	entries := []sandbox.FileSystemSandboxEntry{
		{
			Path:   sandbox.FileSystemPath{Type: "special", Value: &sandbox.FileSystemSpecialPath{Kind: "tmpdir"}},
			Access: sandbox.FileSystemAccessWrite,
		},
		{
			Path:   sandbox.FileSystemPath{Type: "special", Value: &sandbox.FileSystemSpecialPath{Kind: "slash_tmp"}},
			Access: sandbox.FileSystemAccessWrite,
		},
		{
			Path:   sandbox.FileSystemPath{Type: "path", Path: denied},
			Access: sandbox.FileSystemAccessDeny,
		},
	}

	policy, err := PrepareEntriesPolicy(entries, prepareCWD, map[string]string{
		"temp": first,
		"Tmp":  second,
	}, prepareCWD)
	if err != nil {
		t.Fatalf("PrepareEntriesPolicy() error = %v", err)
	}
	for _, path := range []string{first, second} {
		if !policyContainsPath(policy.ReadWritePaths, path) {
			t.Fatalf("temporary path %q was not materialized as writable: %#v", path, policy.ReadWritePaths)
		}
	}
	if !policyContainsPath(policy.DeniedPaths, denied) {
		t.Fatalf("explicit deny was not preserved: %#v", policy.DeniedPaths)
	}

	for _, test := range []struct {
		name string
		env  map[string]string
	}{
		{name: "missing"},
		{name: "empty", env: map[string]string{"TEMP": "", "TMP": ""}},
		{name: "relative", env: map[string]string{"TEMP": "relative", "TMP": "relative"}},
		{name: "home", env: map[string]string{"TEMP": "~/scratch", "TMP": "~/scratch"}},
		{name: "windows home", env: map[string]string{"TEMP": `~\scratch`, "TMP": `~\scratch`}},
		{name: "drive relative", env: map[string]string{"TEMP": "C:temp", "TMP": "C:temp"}},
		{name: "root relative", env: map[string]string{"TEMP": `\temp`, "TMP": `\temp`}},
	} {
		t.Run(test.name, func(t *testing.T) {
			policy, err := PrepareEntriesPolicy(entries, prepareCWD, test.env, prepareCWD)
			if err != nil {
				t.Fatalf("PrepareEntriesPolicy() error = %v", err)
			}
			for _, path := range []string{first, second} {
				if policyContainsPath(policy.ReadWritePaths, path) {
					t.Fatalf("invalid temporary path %q became writable: %#v", path, policy.ReadWritePaths)
				}
			}
			if !policyContainsPath(policy.DeniedPaths, denied) {
				t.Fatalf("explicit deny was not preserved: %#v", policy.DeniedPaths)
			}
		})
	}
}

// Mirrors Rust #44620: only a readable `minimal` entry opts into platform read
// roots, and explicit denies still win.
func TestPrepareEntriesPolicyMinimalPlatformRootsLikeRust(t *testing.T) {
	root := t.TempDir()
	allowed := filepath.Join(root, "platform")
	denied := filepath.Join(root, "secret")
	for _, path := range []string{allowed, denied} {
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	prepareCWD := filepath.Join(root, "policy")
	if err := os.MkdirAll(prepareCWD, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SystemRoot", allowed)
	for _, key := range []string{"ProgramFiles", "ProgramFiles(x86)", "ProgramData"} {
		t.Setenv(key, "")
	}

	for _, includeDefaults := range []bool{false, true} {
		entries := []sandbox.FileSystemSandboxEntry{{
			Path:   sandbox.FileSystemPath{Type: "path", Path: denied},
			Access: sandbox.FileSystemAccessDeny,
		}}
		if includeDefaults {
			entries = append(entries, sandbox.FileSystemSandboxEntry{
				Path:   sandbox.FileSystemPath{Type: "special", Value: &sandbox.FileSystemSpecialPath{Kind: "minimal"}},
				Access: sandbox.FileSystemAccessRead,
			})
		}
		policy, err := PrepareEntriesPolicy(entries, prepareCWD, nil, prepareCWD)
		if err != nil {
			t.Fatalf("PrepareEntriesPolicy(include defaults=%v) error = %v", includeDefaults, err)
		}
		if got := policyContainsPath(policy.ReadOnlyPaths, allowed); got != includeDefaults {
			t.Fatalf("platform root presence = %v, want %v: %#v", got, includeDefaults, policy.ReadOnlyPaths)
		}
		for _, paths := range [][]string{policy.ReadWritePaths, policy.ReadOnlyPaths} {
			if policyContainsPath(paths, denied) {
				t.Fatalf("denied path remained readable/writable: %#v", policy)
			}
		}
		if !policyContainsPath(policy.DeniedPaths, denied) {
			t.Fatalf("explicit deny was not preserved: %#v", policy.DeniedPaths)
		}
	}
}

func policyContainsPath(paths []string, want string) bool {
	for _, path := range paths {
		if strings.EqualFold(filepath.Clean(path), filepath.Clean(want)) {
			return true
		}
	}
	return false
}
