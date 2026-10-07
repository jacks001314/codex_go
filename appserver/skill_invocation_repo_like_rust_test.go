package appserver

import (
	"os"
	"path/filepath"
	"testing"
)

// Mirrors Rust #49076: skill analytics resolves only the sanitized origin URL, so
// credentials embedded in a remote never reach the skill invocation ID, and the
// unused commit hash / branch are not collected.
func TestSkillInvocationRepoSanitizesOriginURLLikeRust(t *testing.T) {
	repo := t.TempDir()
	gitDir := filepath.Join(repo, ".git")
	if err := os.MkdirAll(gitDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	// Only `.git/config` exists: the skill path must not need HEAD / refs.
	if err := os.WriteFile(filepath.Join(gitDir, "config"), []byte("[remote \"origin\"]\n  url = https://user:token@example.com/repo.git\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(config) error = %v", err)
	}
	skillPath := filepath.Join(repo, "skills", "example", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillPath), 0o755); err != nil {
		t.Fatalf("MkdirAll(skill) error = %v", err)
	}
	if err := os.WriteFile(skillPath, []byte("# skill\n"), 0o600); err != nil {
		t.Fatalf("WriteFile(skill) error = %v", err)
	}
	root, url := skillInvocationRepo(skillPath)
	if root != repo {
		t.Fatalf("skillInvocationRepo() root = %q, want %q", root, repo)
	}
	if url != "https://example.com/repo.git" {
		t.Fatalf("skillInvocationRepo() url = %q, want sanitized https://example.com/repo.git", url)
	}
}
