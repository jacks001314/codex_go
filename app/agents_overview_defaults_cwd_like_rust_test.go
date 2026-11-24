package app

import (
	"testing"

	"codex_go/cli"
	codextui "codex_go/tui"
)

// TestAgentsOverviewDefaultsCWDLikeRust mirrors Rust agents_overview.rs
// defaults_cwd (#57267): an embedded dashboard leaves the directory to its own
// app server, while a remote or server-owned workspace starts a task in the
// --cd override, then the displayed thread's directory.
func TestAgentsOverviewDefaultsCWDLikeRust(t *testing.T) {
	state := codextui.NewState(&codextui.Options{CWD: "/thread/cwd"})
	override := &cli.RootOptions{Shared: cli.SharedOptions{CWD: "/launch/cwd"}}

	if got := agentsOverviewDefaultsCWD(ThreadParamsModeEmbedded, override, state); got != "" {
		t.Fatalf("embedded defaults cwd = %q, want empty", got)
	}
	if got := agentsOverviewDefaultsCWD(ThreadParamsModeRemote, override, state); got != "/launch/cwd" {
		t.Fatalf("remote defaults cwd with --cd = %q, want /launch/cwd", got)
	}
	if got := agentsOverviewDefaultsCWD(ThreadParamsModeRemote, &cli.RootOptions{}, state); got != "/thread/cwd" {
		t.Fatalf("remote defaults cwd from thread = %q, want /thread/cwd", got)
	}
	if got := agentsOverviewDefaultsCWD(ThreadParamsModeCloud, &cli.RootOptions{}, codextui.NewState(nil)); got != "." {
		t.Fatalf("cloud defaults cwd without a workspace = %q, want .", got)
	}
}
