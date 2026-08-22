package app

import (
	"bytes"
	"context"
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

func TestRunAgentsCommandWithIOFallsBackToTextOverviewOffTerminal(t *testing.T) {
	store := session.NewStore(t.TempDir())
	if err := store.Create(&session.Record{
		ID:        "33333333-3333-3333-3333-333333333333",
		SessionID: "s-3",
		Title:     "overview",
		Metadata:  session.Metadata{HistoryMode: "legacy", Source: "cli"},
	}); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	previous := newAgentsDashboardStore
	newAgentsDashboardStore = func() *session.Store { return store }
	defer func() { newAgentsDashboardStore = previous }()
	var stdout bytes.Buffer
	var stdin bytes.Buffer
	if err := runAgentsCommandWithIO(context.Background(), &cli.AgentsOptions{}, nil, &stdin, &stdout); err != nil {
		t.Fatalf("runAgentsCommandWithIO() error = %v", err)
	}
	out := stdout.String()
	for _, want := range []string{"Active sessions:", "33333333-3333-3333-3333-333333333333", "overview"} {
		if !strings.Contains(out, want) {
			t.Fatalf("text overview missing %q:\n%s", want, out)
		}
	}
}

func TestParseAgentsCommandAcceptsDashboardFlagsLikeRust(t *testing.T) {
	parsed, err := cli.Parse([]string{"agents", "--cd", "/workspace", "--no-alt-screen"})
	if err != nil {
		t.Fatalf("Parse(agents --cd --no-alt-screen) error = %v", err)
	}
	if parsed.Command != cli.CommandAgents {
		t.Fatalf("Command = %q, want agents", parsed.Command)
	}
	if parsed.Agents.Cwd != "/workspace" || !parsed.Agents.NoAltScreen {
		t.Fatalf("Agents options = %#v", parsed.Agents)
	}

	parsed, err = cli.Parse([]string{"agents", "-C", "/other", "--remote", "ws://127.0.0.1:1234"})
	if err != nil {
		t.Fatalf("Parse(agents -C --remote) error = %v", err)
	}
	if parsed.Agents.Cwd != "/other" || parsed.Agents.Remote != "ws://127.0.0.1:1234" {
		t.Fatalf("Agents options = %#v", parsed.Agents)
	}

	// Rust #39870: codex agents accepts invocation-specific session config
	// (model / approval / sandbox / search / cwd) but not prompt, images, or
	// local provider / add-dir (remote).
	parsed, err = cli.Parse([]string{"agents", "--model", "gpt-5.6-sol", "--search"})
	if err != nil {
		t.Fatalf("Parse(agents --model --search) error = %v", err)
	}
	if parsed.Agents.Shared.Model != "gpt-5.6-sol" || !parsed.Agents.Shared.Search {
		t.Fatalf("Agents shared options = %#v", parsed.Agents.Shared)
	}
}

func TestParseAgentsCommandRejectsInvocationOverridesLikeRust(t *testing.T) {
	for _, args := range [][]string{
		{"agents", "positional"},
		{"agents", "--bogus"},
		{"agents", "--prompt", "hello"},
		{"agents", "--image", "x.png"},
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
