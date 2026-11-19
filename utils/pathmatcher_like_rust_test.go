package utils

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

// Rust normalizes a query's cwd filters once and then compares canonical
// strings (thread-store list_threads -> normalize_cwd_for_state_db), so the
// matcher must agree with paths_match_after_normalization while only
// canonicalizing each side once.
func TestPathMatcherMatchesAfterNormalization(t *testing.T) {
	dir := t.TempDir()
	child := filepath.Join(dir, "workspace")
	if err := os.MkdirAll(child, 0o755); err != nil {
		t.Fatal(err)
	}
	unnormalized := filepath.Join(child, "..", "workspace")

	matcher := NewPathMatcher([]string{child})
	if matcher.Len() != 1 {
		t.Fatalf("matcher len = %d, want 1", matcher.Len())
	}
	for _, candidate := range []string{child, unnormalized, filepath.ToSlash(child)} {
		if !matcher.Matches(candidate) {
			t.Fatalf("matcher.Matches(%q) = false, want the canonical workspace", candidate)
		}
		if !PathsMatchAfterNormalization(candidate, child) {
			t.Fatalf("PathsMatchAfterNormalization(%q, %q) = false", candidate, child)
		}
	}
	if matcher.Matches(dir) {
		t.Fatalf("matcher.Matches(%q) = true, want a different directory rejected", dir)
	}
}

func TestPathMatcherFallsBackToRawEquality(t *testing.T) {
	// Neither side exists, so canonicalization fails and the comparison falls
	// back to exact path equality instead of cleaning separators.
	missing := filepath.Join(t.TempDir(), "missing")
	missingSlash := filepath.ToSlash(missing)
	matcher := NewPathMatcher([]string{missingSlash})
	if !matcher.Matches(missingSlash) {
		t.Fatalf("matcher should keep the raw match for a missing path")
	}
	if runtime.GOOS == "windows" && matcher.Matches(missing) {
		t.Fatalf("matcher must not normalize the separators of a missing path")
	}
	if (PathMatcher{}).Matches(missingSlash) {
		t.Fatalf("the zero-value matcher matches nothing")
	}
}
