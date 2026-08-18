package app

import (
	"strings"
	"testing"

	"codex_go/cli"
	"codex_go/session"
)

func TestParseAgentsCommandLikeRust(t *testing.T) {
	parsed, err := cli.Parse([]string{"agents"})
	if err != nil {
		t.Fatalf("Parse(agents) error = %v", err)
	}
	if parsed.Command != cli.CommandAgents {
		t.Fatalf("Command = %q, want %q", parsed.Command, cli.CommandAgents)
	}

	parsed, err = cli.Parse([]string{"agents", "--remote", "ws://127.0.0.1:1234", "--remote-auth-token-env", "TOKEN"})
	if err != nil {
		t.Fatalf("Parse(agents --remote) error = %v", err)
	}
	if parsed.Agents.Remote != "ws://127.0.0.1:1234" || parsed.Agents.RemoteAuthEnv != "TOKEN" {
		t.Fatalf("Agents options = %#v", parsed.Agents)
	}
}

func TestParseAgentsCommandRejectsInvocationOverridesLikeRust(t *testing.T) {
	for _, args := range [][]string{
		{"agents", "positional"},
		{"agents", "--model", "gpt-5.6-sol"},
		{"agents", "--bogus"},
		{"agents", "--prompt", "hello"},
	} {
		if _, err := cli.Parse(args); err == nil {
			t.Fatalf("Parse(%v) succeeded, want rejection of invocation-specific overrides", args)
		}
	}
}

func TestRunLocalAgentsOverviewWithStoreLikeRust(t *testing.T) {
	store := session.NewStore(t.TempDir())
	for _, record := range []*session.Record{
		{ID: "11111111-1111-1111-1111-111111111111", SessionID: "s-1", Title: "alpha", Metadata: session.Metadata{HistoryMode: "legacy", Source: "cli"}},
		{ID: "22222222-2222-2222-2222-222222222222", SessionID: "s-2", Title: "beta", Metadata: session.Metadata{HistoryMode: "legacy", Source: "vscode"}},
	} {
		if err := store.Create(record); err != nil {
			t.Fatalf("Create() error = %v", err)
		}
	}
	var stdout strings.Builder
	if err := runLocalAgentsOverviewWithStore(store, &stdout); err != nil {
		t.Fatalf("runLocalAgentsOverviewWithStore() error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"Active sessions:", "11111111-1111-1111-1111-111111111111", "alpha", "22222222-2222-2222-2222-222222222222", "beta"} {
		if !strings.Contains(out, want) {
			t.Fatalf("overview missing %q:\n%s", want, out)
		}
	}
}
