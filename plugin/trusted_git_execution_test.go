package plugin

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/envutil"
)

func TestAutomaticGitUsesTrustedRepositoryAndCleansUp(t *testing.T) {
	if _, ok := envutil.TrustedExecutable("git"); !ok {
		t.Skip("trusted Git unavailable")
	}
	home := t.TempDir()
	output, err := runMarketplaceGitOutputEnvWithHome(nil, true, home, nil, "rev-parse", "--absolute-git-dir")
	if err != nil {
		t.Fatal(err)
	}
	selected := strings.TrimSpace(output)
	rel, err := filepath.Rel(filepath.Join(home, ".tmp", ".staging"), selected)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || filepath.IsAbs(rel) {
		t.Fatalf("untrusted Git directory %q", selected)
	}
	if _, err := os.Stat(selected); !os.IsNotExist(err) {
		t.Fatalf("repository was not cleaned after Git exit: %v", err)
	}
}

func TestAutomaticCloneUsesTrustedHome(t *testing.T) {
	git, ok := envutil.TrustedExecutable("git")
	if !ok {
		t.Skip("trusted Git unavailable")
	}
	source := t.TempDir()
	command := exec.Command(git, "init", source)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("init: %s %v", output, err)
	}
	home := t.TempDir()
	destination := filepath.Join(t.TempDir(), "checkout")
	materializer := &GitMarketplaceMaterializer{Automatic: true, CodexHome: home}
	if err := materializer.MaterializeMarketplace(&ParsedMarketplaceSource{Kind: MarketplaceSourceGit, URL: source}, nil, destination); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(destination, ".git", "HEAD")); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(filepath.Join(home, ".tmp", marketplaceStagingDirName))
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("trusted repository remains after clone: %v", entries)
	}
}

func TestAutomaticGitRequiresHomeExceptVersionProbe(t *testing.T) {
	if _, ok := envutil.TrustedExecutable("git"); !ok {
		t.Skip("trusted Git unavailable")
	}
	output, err := runMarketplaceGitOutput(nil, true, "--version")
	if err != nil || !strings.HasPrefix(output, "git version ") {
		t.Fatalf("version: %q %v", output, err)
	}
	for _, home := range []string{"", "   "} {
		_, err := runMarketplaceGitOutputEnvWithHome(nil, true, home, nil, "rev-parse", "--absolute-git-dir")
		if err == nil || !strings.Contains(err.Error(), "Codex home is required") {
			t.Fatalf("home=%q: %v", home, err)
		}
	}
}
