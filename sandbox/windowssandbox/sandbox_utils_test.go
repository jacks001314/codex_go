package windowssandbox

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInjectGitSafeDirectoryForGitDirectory(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	nested := filepath.Join(repo, "nested")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatalf("MkdirAll(.git) error = %v", err)
	}
	if err := os.MkdirAll(nested, 0o700); err != nil {
		t.Fatalf("MkdirAll(nested) error = %v", err)
	}
	env := map[string]string{}
	InjectGitSafeDirectory(env, nested)
	if env["GIT_CONFIG_COUNT"] != "1" || env["GIT_CONFIG_KEY_0"] != "safe.directory" {
		t.Fatalf("env = %#v", env)
	}
	if env["GIT_CONFIG_VALUE_0"] == "" {
		t.Fatalf("safe.directory value missing: %#v", env)
	}
}

func TestInjectGitSafeDirectoryAppendsExistingConfig(t *testing.T) {
	tmp := t.TempDir()
	repo := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o700); err != nil {
		t.Fatalf("MkdirAll(.git) error = %v", err)
	}
	env := map[string]string{"GIT_CONFIG_COUNT": "1", "GIT_CONFIG_KEY_0": "core.autocrlf", "GIT_CONFIG_VALUE_0": "false"}
	InjectGitSafeDirectory(env, repo)
	if env["GIT_CONFIG_COUNT"] != "2" || env["GIT_CONFIG_KEY_1"] != "safe.directory" {
		t.Fatalf("env = %#v", env)
	}
}
