package mxc

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/sandbox"
)

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
