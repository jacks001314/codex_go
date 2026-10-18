package integrity

import (
	"os"
	"path/filepath"
	"testing"

	"codex_go/sandbox"
	"codex_go/utils"
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

// Mirrors Rust #41209: a candidate whose path convention disagrees with the
// policy context fails closed instead of being evaluated as another spelling.
func TestReadDenyMatcherFailsClosedOnIncompatiblePathConventionLikeRust(t *testing.T) {
	windowsPolicy, err := PreparePolicy([]sandbox.FileSystemSandboxEntry{{
		Path:   sandbox.FileSystemPath{Type: "glob_pattern", Pattern: `C:\repo\**\*.key`},
		Access: sandbox.FileSystemAccessDeny,
	}}, `C:\repo`)
	if err != nil {
		t.Fatalf("PreparePolicy(windows) error = %v", err)
	}
	if !windowsPolicy.GlobDenials.IsLocalPathReadDenied("/posix/secret.key") {
		t.Fatal("POSIX candidate under a Windows policy did not fail closed")
	}
	posixPolicy, err := PreparePolicy([]sandbox.FileSystemSandboxEntry{{
		Path:   sandbox.FileSystemPath{Type: "glob_pattern", Pattern: "/repo/**/*.key"},
		Access: sandbox.FileSystemAccessDeny,
	}}, "/repo")
	if err != nil {
		t.Fatalf("PreparePolicy(posix) error = %v", err)
	}
	if !posixPolicy.GlobDenials.IsLocalPathReadDenied(`C:\secret.key`) {
		t.Fatal("Windows candidate under a POSIX policy did not fail closed")
	}
}

// Rust #41209 also fails closed when neither the policy cwd nor the candidate
// has an inferable convention.
func TestReadDenyMatcherFailsClosedOnUnknownConventionLikeRust(t *testing.T) {
	matcher := &ReadDenyMatcher{
		entries: []sandbox.FileSystemSandboxEntry{{
			Path:   sandbox.FileSystemPath{Type: "glob_pattern", Pattern: "/workspace/*.env"},
			Access: sandbox.FileSystemAccessDeny,
		}},
		cwd: "/workspace",
	}
	if !matcher.IsLocalPathReadDenied("/workspace/secret.env") {
		t.Fatal("unknown policy convention must fail closed")
	}
	matcher.convention = utils.ConventionPosix
	if !matcher.IsLocalPathReadDenied("relative/secret.env") {
		t.Fatal("unknown candidate convention must fail closed")
	}
}
