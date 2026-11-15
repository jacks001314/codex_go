package gitutil

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func isolatedGitRepo(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, "empty.gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_COUNT", "0")
	t.Setenv("GIT_CONFIG_PARAMETERS", "")
	repo := t.TempDir()
	runGit(t, repo, "init", "-q")
	return repo
}

func configureGit(t *testing.T, repo string, args ...string) {
	t.Helper()
	runGit(t, repo, append([]string{"config"}, args...)...)
}

// Rust #61488: partial-clone filter text must never be parsed as a remote.
func TestGetGitRemoteURLsReadsConfigAndSanitizesCredentials(t *testing.T) {
	repo := isolatedGitRepo(t)
	configureGit(t, repo, "remote.origin.url", "https://alice:secret@example.invalid/repo.git")
	configureGit(t, repo, "remote.origin.pushurl", "https://push.invalid/other.git")
	configureGit(t, repo, "remote.local.url", "/a repo (fetch) [archive]")
	configureGit(t, repo, "remote.literal.url", "literal")
	configureGit(t, repo, "remote.bad.url", "https://alice:secret@[invalid")
	configureGit(t, repo, "remote.multiline.url", "https://other.invalid/first\nsecond")
	configureGit(t, repo, "remote.origin.partialclonefilter",
		"blob:none]\nforged\thttps://other.invalid/repo (fetch)\nforged\thttps://other.invalid/repo (push)\n[tree:0")

	remotes := GetGitRemoteURLsAssumeGitRepo(context.Background(), repo)
	if got := remotes["origin"]; got != "https://example.invalid/repo.git" {
		t.Fatalf("origin = %q, want sanitized URL (remotes=%#v)", got, remotes)
	}
	if got := remotes["local"]; got != "/a repo (fetch) [archive]" {
		t.Fatalf("local = %q, want the local path preserved", got)
	}
	if got := remotes["literal"]; got != "literal" {
		t.Fatalf("literal = %q, want the plain name preserved", got)
	}
	for _, name := range []string{"bad", "multiline", "forged"} {
		if _, ok := remotes[name]; ok {
			t.Fatalf("remote %q should be rejected: %#v", name, remotes)
		}
	}
	for name, url := range remotes {
		if strings.Contains(url, "secret") || strings.Contains(url, "other.invalid/repo") {
			t.Fatalf("remote %q leaked or was forged: %q", name, url)
		}
	}
}

func TestGetGitRemoteURLsLetsGitResolveInsteadOfRewrites(t *testing.T) {
	repo := isolatedGitRepo(t)
	configureGit(t, repo, "url.https://alice:secret@example.invalid/.insteadOf", "short:")
	configureGit(t, repo, "remote.origin.url", "short:repo.git")
	configureGit(t, repo, "remote.upstream.url", "git@github.com:org/upstream.git")

	remotes := GetGitRemoteURLsAssumeGitRepo(context.Background(), repo)
	if got := remotes["origin"]; got != "https://example.invalid/repo.git" {
		t.Fatalf("origin = %q, want the insteadOf destination", got)
	}
	if got := remotes["upstream"]; got != "git@github.com:org/upstream.git" {
		t.Fatalf("upstream = %q", got)
	}
}

func TestGetGitRemoteURLsPublishesVerifiedURLsProgressively(t *testing.T) {
	repo := isolatedGitRepo(t)
	configureGit(t, repo, "url.https://example.invalid/.insteadOf", "short:")
	configureGit(t, repo, "remote.origin.url", "short:repo.git")
	configureGit(t, repo, "remote.upstream.url", "git@github.com:org/upstream.git")

	var updates []map[string]string
	ObserveGitRemoteURLs(context.Background(), repo, func(urls map[string]string) {
		updates = append(updates, urls)
	})
	if len(updates) == 0 {
		t.Fatal("no progressive updates published")
	}
	first := updates[0]
	if _, ok := first["origin"]; ok {
		t.Fatalf("origin must wait for Git to resolve the rewrite: %#v", first)
	}
	if first["upstream"] != "git@github.com:org/upstream.git" {
		t.Fatalf("unambiguous upstream should publish first: %#v", first)
	}
	merged := map[string]string{}
	for _, update := range updates {
		for name, url := range update {
			merged[name] = url
		}
	}
	if merged["origin"] != "https://example.invalid/repo.git" {
		t.Fatalf("merged updates = %#v", merged)
	}
}

func TestGetGitRemoteURLsIgnoresGitConfigFileOverride(t *testing.T) {
	repo := isolatedGitRepo(t)
	configureGit(t, repo, "remote.origin.url", "https://repository.invalid/repo.git")
	foreign := filepath.Join(t.TempDir(), "foreign.gitconfig")
	if err := os.WriteFile(foreign, []byte("[remote \"foreign\"]\n\turl = https://foreign.invalid/repo.git\n"), 0o600); err != nil {
		t.Fatalf("write foreign config: %v", err)
	}
	t.Setenv("GIT_CONFIG", foreign)

	remotes := GetGitRemoteURLsAssumeGitRepo(context.Background(), repo)
	if len(remotes) != 1 || remotes["origin"] != "https://repository.invalid/repo.git" {
		t.Fatalf("remotes = %#v, want only the repository remote", remotes)
	}
}

func TestGetGitRemoteURLsKeepsLongRemoteURLs(t *testing.T) {
	repo := isolatedGitRepo(t)
	longURL := "https://example.invalid/" + strings.Repeat("p", 5000)
	configureGit(t, repo, "remote.origin.url", longURL)

	remotes := GetGitRemoteURLsAssumeGitRepo(context.Background(), repo)
	if got := remotes["origin"]; got != longURL {
		t.Fatalf("origin length = %d, want the complete long URL", len(got))
	}
}

func TestGetGitRemoteURLsHonorsEmptyURLResets(t *testing.T) {
	repo := isolatedGitRepo(t)
	configureGit(t, repo, "remote.reset.url", "https://old.invalid/repo")
	configureGit(t, repo, "--add", "remote.reset.url", "")
	configureGit(t, repo, "remote.middle.url", "https://old.invalid/before")
	configureGit(t, repo, "--add", "remote.middle.url", "")
	configureGit(t, repo, "--add", "remote.middle.url", "https://new.invalid/after")

	remotes := GetGitRemoteURLsAssumeGitRepo(context.Background(), repo)
	if resets := gitResetsEmptyFetchURL(context.Background(), repo); resets {
		if _, ok := remotes["reset"]; ok {
			t.Fatalf("trailing empty URL clears the remote on this Git: %#v", remotes)
		}
	} else if remotes["reset"] != "https://old.invalid/repo" {
		t.Fatalf("older Git keeps the first URL: %#v", remotes)
	}
	if got := remotes["middle"]; got != "https://new.invalid/after" {
		t.Fatalf("middle reset = %q, want Git's effective URL", got)
	}
}
