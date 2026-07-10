package appserver

import (
	"testing"

	"codex_go/internal/telemetry"
)

func TestCanonicalizeAcceptedLineGitRemoteURLMatchesRust(t *testing.T) {
	cases := []struct {
		remote string
		want   string
	}{
		{remote: "git@github.com:OpenAI/Codex.git", want: "github.com/openai/codex"},
		{remote: "https://github.com/OpenAI/Codex.git", want: "github.com/openai/codex"},
		{remote: "ssh://git@ghe.company.com:22/Org/Repo.git", want: "ghe.company.com/Org/Repo"},
		{remote: "ssh://git@ghe.company.com:2222/Org/Repo.git", want: "ghe.company.com:2222/Org/Repo"},
	}
	for _, tc := range cases {
		t.Run(tc.remote, func(t *testing.T) {
			if got := canonicalizeAcceptedLineGitRemoteURL(tc.remote); got != tc.want {
				t.Fatalf("canonical remote = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestAcceptedLineRepoHashFromRemoteURLUsesCanonicalRemote(t *testing.T) {
	hash := acceptedLineRepoHashFromRemoteURL("git@github.com:OpenAI/Codex.git")
	want := telemetry.FingerprintHash("repo", "github.com/openai/codex")
	if hash == nil || *hash != want {
		t.Fatalf("repo hash = %#v, want %q", hash, want)
	}
}

func TestAcceptedLineParseGitRemoteURLs(t *testing.T) {
	remotes := acceptedLineParseGitRemoteURLs("origin\tgit@github.com:OpenAI/Codex.git (fetch)\norigin\tgit@github.com:OpenAI/Codex.git (push)\nupstream https://github.com/openai/codex.git (fetch)\n")
	if remotes["origin"] != "git@github.com:OpenAI/Codex.git" ||
		remotes["upstream"] != "https://github.com/openai/codex.git" ||
		len(remotes) != 2 {
		t.Fatalf("remotes = %#v", remotes)
	}
}
