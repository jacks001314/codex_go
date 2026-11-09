package gitutil

import "testing"

// Rust #60007 git-utils canonicalize_git_remote_url: GitHub variants collapse
// to one credential-free host/path with a lowercase path.
func TestCanonicalizeGitRemoteURLNormalizesGitHubVariantsLikeRust(t *testing.T) {
	for _, remote := range []string{
		"git@github.com:OpenAI/Codex.git",
		"ssh://git@github.com/openai/codex.git",
		"ssh://git@github.com:22/OpenAI/Codex.git",
		"https://github.com/openai/codex.git",
		"https://github.com:443/openai/codex.git",
		"https://token@github.com/openai/codex/",
		"github.com/OpenAI/Codex.git",
	} {
		got, ok := CanonicalizeGitRemoteURL(remote)
		if !ok || got != "github.com/openai/codex" {
			t.Fatalf("CanonicalizeGitRemoteURL(%q) = %q,%t", remote, got, ok)
		}
	}
}

// Non-GitHub hosts keep their path case; a non-default SSH port survives.
func TestCanonicalizeGitRemoteURLChecksGHELikeRust(t *testing.T) {
	for remote, want := range map[string]string{
		"git@ghe.company.com:Org/Repo.git":            "ghe.company.com/Org/Repo",
		"ssh://git@ghe.company.com:2222/Org/Repo.git": "ghe.company.com:2222/Org/Repo",
	} {
		got, ok := CanonicalizeGitRemoteURL(remote)
		if !ok || got != want {
			t.Fatalf("CanonicalizeGitRemoteURL(%q) = %q,%t, want %q", remote, got, ok, want)
		}
	}
}

func TestCanonicalizeGitRemoteURLRejectsNonRepositoryValuesLikeRust(t *testing.T) {
	for _, remote := range []string{
		"",
		"file:///tmp/repo",
		"github.com/openai",
		"/tmp/repo",
		"https://example.com?target=@github.com/org/wrong",
	} {
		if got, ok := CanonicalizeGitRemoteURL(remote); ok {
			t.Fatalf("CanonicalizeGitRemoteURL(%q) = %q, want rejection", remote, got)
		}
	}
}

// Rust #60007 sanitized_git_url: HTTP(S) authorities stop at '?' or '#' so a
// query string cannot be mistaken for userinfo.
func TestSanitizeGitURLStopsHTTPAuthorityAtQueryLikeRust(t *testing.T) {
	got, err := SanitizeGitURL("https://user:password@github.com/Org/Repo.git?token=secret#fragment")
	if err != nil {
		t.Fatalf("SanitizeGitURL error = %v", err)
	}
	if want := "https://github.com/Org/Repo.git?token=secret#fragment"; got != want {
		t.Fatalf("SanitizeGitURL = %q, want %q", got, want)
	}
	// A misleading query target is not treated as an authority with userinfo.
	got, err = SanitizeGitURL("https://example.com?target=@github.com/org/wrong")
	if err != nil {
		t.Fatalf("SanitizeGitURL error = %v", err)
	}
	if want := "https://example.com?target=@github.com/org/wrong"; got != want {
		t.Fatalf("SanitizeGitURL = %q, want %q", got, want)
	}
}
