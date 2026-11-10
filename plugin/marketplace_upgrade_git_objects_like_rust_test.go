package plugin

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Rust #60528: upgrading a Git marketplace borrows the installed checkout as a
// clone reference so already-fetched objects are reused, and `--dissociate`
// keeps the upgraded checkout self-contained.
func TestMarketplaceUpgradeReusesInstalledGitObjectsLikeRust(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git is not installed")
	}
	root := t.TempDir()
	sourceRepo := filepath.Join(root, "source")
	initMarketplaceGitRepo(t, sourceRepo, "marketplace.json", `{"name":"debug"}`)
	installRoot := filepath.Join(root, "marketplaces")
	destination := filepath.Join(installRoot, "debug")

	materializer := &GitMarketplaceMaterializer{}
	source := &ParsedMarketplaceSource{Kind: MarketplaceSourceGit, URL: sourceRepo}
	if err := materializer.MaterializeMarketplace(source, nil, destination); err != nil {
		t.Fatalf("MaterializeMarketplace() error = %v", err)
	}
	firstRevision := marketplaceGitRevision(t, destination)

	if err := os.WriteFile(filepath.Join(sourceRepo, "marketplace.json"), []byte(`{"name":"debug","plugins":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	marketplaceGit(t, sourceRepo, "add", "marketplace.json")
	marketplaceGit(t, sourceRepo, "commit", "-m", "upgrade")
	secondRevision := marketplaceGitRevision(t, sourceRepo)
	if firstRevision == secondRevision {
		t.Fatal("source revision did not advance")
	}

	if err := materializer.UpgradeMarketplace(source, nil, destination); err != nil {
		t.Fatalf("UpgradeMarketplace() error = %v", err)
	}
	if got := marketplaceGitRevision(t, destination); got != secondRevision {
		t.Fatalf("upgraded revision = %s, want %s", got, secondRevision)
	}
	if _, err := os.Stat(filepath.Join(destination, ".git", "objects", "info", "alternates")); !os.IsNotExist(err) {
		t.Fatalf("upgraded checkout keeps an alternate object store: %v", err)
	}
	// Dropping the reference repository proves the dissociated objects are local.
	if err := os.RemoveAll(sourceRepo); err != nil {
		t.Fatal(err)
	}
	if got := marketplaceGitRevision(t, destination); got != secondRevision {
		t.Fatalf("revision after removing the reference = %s, want %s", got, secondRevision)
	}
}

// Rust #60528 only enables reference cloning for a recognized Git release of
// 2.20 or newer.
func TestParseMarketplaceGitReleaseLikeRust(t *testing.T) {
	cases := []struct {
		output string
		major  int
		minor  int
		ok     bool
	}{
		{"git version 2.43.0", 2, 43, true},
		{"git version 2.43.0.windows.1", 2, 43, true},
		{"git version 2.43.0 (Apple Git-146)", 2, 43, true},
		{"git version 2.20.0.rc1", 0, 0, false},
		{"git version 2.19.9", 2, 19, true},
		{"not git", 0, 0, false},
	}
	for _, test := range cases {
		major, minor, ok := parseMarketplaceGitRelease(test.output)
		if ok != test.ok || (ok && (major != test.major || minor != test.minor)) {
			t.Fatalf("parseMarketplaceGitRelease(%q) = %d.%d,%v want %d.%d,%v", test.output, major, minor, ok, test.major, test.minor, test.ok)
		}
	}
}

func initMarketplaceGitRepo(t *testing.T, dir string, filename string, content string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	marketplaceGit(t, dir, "init", "-b", "main")
	marketplaceGit(t, dir, "config", "user.email", "test@example.com")
	marketplaceGit(t, dir, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, filename), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	marketplaceGit(t, dir, "add", filename)
	marketplaceGit(t, dir, "commit", "-m", "initial")
}

func marketplaceGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	command := exec.Command("git", append([]string{"-C", dir}, args...)...)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, output)
	}
	return string(output)
}

func marketplaceGitRevision(t *testing.T, dir string) string {
	t.Helper()
	return strings.TrimSpace(marketplaceGit(t, dir, "rev-parse", "HEAD"))
}
