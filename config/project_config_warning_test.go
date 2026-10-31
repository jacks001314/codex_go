package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// markGitCheckout gives dir the metadata a project root marker needs: Rust's
// #39629 rule counts a .git directory only when it contains HEAD.
func markGitCheckout(t *testing.T, dir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o600); err != nil {
		t.Fatalf("write .git/HEAD: %v", err)
	}
}

// Mirrors Rust's project_config_warning (codex-rs/app-server/src/lib.rs and
// codex-rs/tui/src/app/startup_prompts.rs): every project layer the trust
// decision disabled is listed once, in layer order, with its reason.
func TestProjectConfigWarningForCWD(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "repo")
	dotCodex := filepath.Join(project, ".gcode")
	if err := os.MkdirAll(dotCodex, 0o755); err != nil {
		t.Fatalf("mkdir .gcode: %v", err)
	}
	markGitCheckout(t, project)
	if err := os.WriteFile(filepath.Join(dotCodex, "config.toml"), []byte("model = \"project-model\"\n"), 0o600); err != nil {
		t.Fatalf("write project config: %v", err)
	}
	userConfig := filepath.Join(home, "config.toml")
	if err := os.WriteFile(userConfig, nil, 0o600); err != nil {
		t.Fatalf("write user config: %v", err)
	}

	service := NewConfigService(home)
	got := service.ProjectConfigWarningForCWD(project)
	// The undecided fallback quotes the project-root lookup key, which is the
	// normalized (host-convention) spelling Rust reports.
	trustKey := NormalizedProjectTrustKeys(project)[0]
	want := "Project-local config, hooks, and exec policies are disabled in the following folders until the project is trusted, but skills still load.\n" +
		"    1. " + dotCodex + "\n" +
		"       To load project-local config, hooks, and exec policies, add " + trustKey +
		" as a trusted project in " + userConfig + ".\n"
	if got != want {
		t.Fatalf("warning = %q, want %q", got, want)
	}
}

// An explicit untrusted decision keeps Rust's stable prefix, which the remote
// TUI parses to recover the trust target.
func TestProjectConfigWarningKeepsExplicitUntrustedPrefix(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(project, ".gcode"), 0o755); err != nil {
		t.Fatalf("mkdir .gcode: %v", err)
	}
	markGitCheckout(t, project)
	// A TOML literal key keeps the native path spelling Rust reports back.
	userConfig := fmt.Sprintf("[projects.'%s']\ntrust_level = \"untrusted\"\n", project)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(userConfig), 0o600); err != nil {
		t.Fatalf("write user config: %v", err)
	}

	service := NewConfigService(home)
	got := service.ProjectConfigWarningForCWD(project)
	want := project + " is marked as untrusted in the effective configuration. To load project-local config, hooks, and exec policies, update its trust setting. If that setting is managed by your organization, contact your administrator."
	if !strings.Contains(got, want) {
		t.Fatalf("warning = %q, want it to contain %q", got, want)
	}
}

func TestProjectConfigWarningSkipsTrustedFolders(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(filepath.Join(project, ".gcode"), 0o755); err != nil {
		t.Fatalf("mkdir .gcode: %v", err)
	}
	markGitCheckout(t, project)
	userConfig := fmt.Sprintf("[projects.'%s']\ntrust_level = \"trusted\"\n", project)
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte(userConfig), 0o600); err != nil {
		t.Fatalf("write user config: %v", err)
	}

	service := NewConfigService(home)
	if got := service.ProjectConfigWarningForCWD(project); got != "" {
		t.Fatalf("warning = %q, want none for a trusted project", got)
	}
}

func TestProjectConfigWarningListsNestedFoldersLowToHigh(t *testing.T) {
	home := t.TempDir()
	project := filepath.Join(t.TempDir(), "repo")
	nested := filepath.Join(project, "pkg")
	markGitCheckout(t, project)
	for _, dir := range []string{filepath.Join(project, ".gcode"), filepath.Join(nested, ".gcode")} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", dir, err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "config.toml"), nil, 0o600); err != nil {
		t.Fatalf("write user config: %v", err)
	}

	service := NewConfigService(home)
	got := service.ProjectConfigWarningForCWD(nested)
	rootEntry := "    1. " + filepath.Join(project, ".gcode") + "\n"
	nestedEntry := "    2. " + filepath.Join(nested, ".gcode") + "\n"
	if !strings.Contains(got, rootEntry) || !strings.Contains(got, nestedEntry) {
		t.Fatalf("warning = %q, want both entries %q and %q", got, rootEntry, nestedEntry)
	}
	if strings.Index(got, rootEntry) > strings.Index(got, nestedEntry) {
		t.Fatalf("warning = %q, want the root project layer first", got)
	}
}
