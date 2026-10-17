package integrity

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/sandbox"
)

// Mirrors Rust #41209: `~` deny-read globs resolve against the executor home
// instead of failing closed, and the resulting matcher still denies the path.
func TestPreparePolicyExpandsHomeGlobLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	secrets := filepath.Join(home, "secrets")
	if err := os.MkdirAll(secrets, 0o700); err != nil {
		t.Fatal(err)
	}
	entries := []sandbox.FileSystemSandboxEntry{{
		Path:   sandbox.FileSystemPath{Type: "glob_pattern", Pattern: filepath.ToSlash("~/secrets/*.key")},
		Access: sandbox.FileSystemAccessDeny,
	}}
	policy, err := PreparePolicy(entries, home)
	if err != nil {
		t.Fatalf("PreparePolicy() error = %v", err)
	}
	if policy.GlobDenials == nil {
		t.Fatal("home-relative glob produced no deny matcher")
	}
	if !policy.GlobDenials.IsLocalPathReadDenied(filepath.Join(secrets, "server.key")) {
		t.Fatal("home-relative deny glob did not match the secret file")
	}
}

// Mirrors Rust #41209: a walker-provided canonical location is checked in
// addition to the alias, so denying either spelling denies the contents.
func TestReadDenyMatcherChecksAliasAndCanonicalLikeRust(t *testing.T) {
	cwd := t.TempDir()
	alias := filepath.Join(cwd, "alias", "file")
	canonical := filepath.Join(cwd, "real", "file")
	aliasPolicy, err := PreparePolicy([]sandbox.FileSystemSandboxEntry{{
		Path:   sandbox.FileSystemPath{Type: "glob_pattern", Pattern: filepath.ToSlash("alias/**")},
		Access: sandbox.FileSystemAccessDeny,
	}}, cwd)
	if err != nil {
		t.Fatalf("PreparePolicy(alias) error = %v", err)
	}
	if !aliasPolicy.GlobDenials.IsLocalPathReadDeniedWithCanonical(alias, canonical) {
		t.Fatal("deny rule matching the alias did not deny its canonical target")
	}
	if aliasPolicy.GlobDenials.IsLocalPathReadDenied(canonical) {
		t.Fatal("canonical target unexpectedly matched the alias-only rule")
	}
	canonicalPolicy, err := PreparePolicy([]sandbox.FileSystemSandboxEntry{{
		Path:   sandbox.FileSystemPath{Type: "glob_pattern", Pattern: filepath.ToSlash("real/**")},
		Access: sandbox.FileSystemAccessDeny,
	}}, cwd)
	if err != nil {
		t.Fatalf("PreparePolicy(canonical) error = %v", err)
	}
	if !canonicalPolicy.GlobDenials.IsLocalPathReadDeniedWithCanonical(alias, canonical) {
		t.Fatal("deny rule matching the canonical path did not deny the alias")
	}
}
