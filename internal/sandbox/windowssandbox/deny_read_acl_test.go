package windowssandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlanDenyReadACLPathsPreservesMissingPath(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "future-secret")
	got := PlanDenyReadACLPaths([]string{missing})
	if len(got) != 1 || got[0] != filepath.Clean(missing) {
		t.Fatalf("PlanDenyReadACLPaths() = %#v, want missing path preserved", got)
	}
}

func TestPlanDenyReadACLPathsIncludesExistingCanonicalTarget(t *testing.T) {
	path := filepath.Join(t.TempDir(), "secret.txt")
	if err := os.WriteFile(path, []byte("secret"), 0o600); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	got := PlanDenyReadACLPaths([]string{path})
	if len(got) == 0 {
		t.Fatalf("PlanDenyReadACLPaths() returned empty result")
	}
	if !containsLexicalPath(got, path) {
		t.Fatalf("PlanDenyReadACLPaths() = %#v, want lexical path %q", got, path)
	}
	canonical, err := CanonicalizePath(path)
	if err != nil {
		t.Fatalf("CanonicalizePath() error = %v", err)
	}
	if !containsLexicalPath(got, canonical) {
		t.Fatalf("PlanDenyReadACLPaths() = %#v, want canonical path %q", got, canonical)
	}
}

func containsLexicalPath(paths []string, path string) bool {
	want := lexicalPathKey(filepath.Clean(path))
	for _, candidate := range paths {
		if lexicalPathKey(filepath.Clean(candidate)) == want {
			return true
		}
	}
	return false
}
