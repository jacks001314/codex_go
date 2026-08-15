package windowssandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobScanPlanUsesLiteralPrefixBeforeGlob(t *testing.T) {
	plan := GlobScanPlanForPattern(`/tmp/work/**/*.env`, nil)
	if filepath.ToSlash(plan.Root) != "/tmp/work" {
		t.Fatalf("root = %q", plan.Root)
	}
	if plan.MaxDepth != nil {
		t.Fatalf("maxDepth = %v, want nil for recursive glob", *plan.MaxDepth)
	}
	plan = GlobScanPlanForPattern(`/tmp/work/*.env`, nil)
	if plan.MaxDepth == nil || *plan.MaxDepth != 1 {
		t.Fatalf("maxDepth = %v, want 1", plan.MaxDepth)
	}
}

func TestResolveWindowsDenyReadPolicyPathsExpandsExistingGlobMatches(t *testing.T) {
	tmp := t.TempDir()
	rootEnv := filepath.Join(tmp, ".env")
	nestedEnv := filepath.Join(tmp, "app", ".env")
	notes := filepath.Join(tmp, "app", "notes.txt")
	if err := os.MkdirAll(filepath.Dir(nestedEnv), 0o700); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	for _, path := range []string{rootEnv, nestedEnv, notes} {
		if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
			t.Fatalf("WriteFile(%s) error = %v", path, err)
		}
	}
	got, err := ResolveWindowsDenyReadPolicyPaths(DenyReadPolicy{
		Globs: []string{filepath.Join(tmp, "**", "*.env")},
	}, tmp)
	if err != nil {
		t.Fatalf("ResolveWindowsDenyReadPolicyPaths() error = %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("paths = %#v, want two env files", got)
	}
}

func TestResolveWindowsDenyReadPolicyPathsPreservesMissingExactPaths(t *testing.T) {
	tmp := t.TempDir()
	missing := filepath.Join(tmp, "missing.env")
	got, err := ResolveWindowsDenyReadPolicyPaths(DenyReadPolicy{Paths: []string{missing}}, tmp)
	if err != nil {
		t.Fatalf("ResolveWindowsDenyReadPolicyPaths() error = %v", err)
	}
	if len(got) != 1 || got[0] != cleanWindowsSandboxAbs(missing) {
		t.Fatalf("paths = %#v", got)
	}
}

func TestResolveWindowsDenyReadPolicyPathsRejectsUnboundedRootGlobsLikeRust(t *testing.T) {
	tmp := t.TempDir()
	root := filesystemRootForTest(t, tmp)
	pattern := filepath.Join(root, "**", "*.env")
	_, err := ResolveWindowsDenyReadPolicyPaths(DenyReadPolicy{Globs: []string{pattern}}, tmp)
	if err == nil || !strings.Contains(err.Error(), "cannot be safely expanded from a filesystem root without `glob_scan_max_depth`") {
		t.Fatalf("error = %v, want fail-closed unbounded root glob", err)
	}
}

func filesystemRootForTest(t *testing.T, path string) string {
	t.Helper()
	absolute, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("Abs(%s) error = %v", path, err)
	}
	for {
		parent := filepath.Dir(absolute)
		if parent == absolute {
			return absolute
		}
		absolute = parent
	}
}
