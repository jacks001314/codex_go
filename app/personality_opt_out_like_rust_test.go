package app

import (
	"testing"

	"codex_go/cli"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// Rust #49876: the TUI no longer derives personality from local state, so
// thread/start and turn/start leave personality unset for every local value.
func TestTUIRequestsDoNotForwardLocalPersonalityLikeRust(t *testing.T) {
	for _, personality := range []string{"friendly", "pragmatic", "none", ""} {
		state := &codextui.State{Personality: personality}
		start, err := remoteThreadStartParams(&cli.RootOptions{}, state)
		if err != nil {
			t.Fatalf("%q thread start: %v", personality, err)
		}
		if start.Personality != nil {
			t.Fatalf("%q thread start personality = %q, want unset", personality, *start.Personality)
		}
		turn, err := remoteTurnStartParams(&cli.RootOptions{}, state, "thread-1", codextea.SubmitRequest{Prompt: "hello"})
		if err != nil {
			t.Fatalf("%q turn start: %v", personality, err)
		}
		if turn.Personality != nil || turn.PersonalitySet {
			t.Fatalf("%q turn personality = %#v set=%v, want unset", personality, turn.Personality, turn.PersonalitySet)
		}
	}
}

// Rust #49876 forwards personality through session configuration only when a
// CLI override supplied it, and then verbatim rather than opt-out-only.
func TestRemoteConfigForwardsPersonalityOnlyFromCLIOverridesLikeRust(t *testing.T) {
	valuesFor := func(overrides ...string) map[string]any {
		t.Helper()
		values, err := remoteConfigValues(&cli.RootOptions{ConfigOverrides: overrides}, cli.SharedOptions{})
		if err != nil {
			t.Fatalf("remoteConfigValues(%v): %v", overrides, err)
		}
		return values
	}
	if values := valuesFor(); values["personality"] != nil {
		t.Fatalf("implicit personality forwarded: %#v", values["personality"])
	}
	if values := valuesFor(`personality="none"`); values["personality"] != "none" {
		t.Fatalf("none personality override = %#v, want none", values["personality"])
	}
	if values := valuesFor(`personality="friendly"`); values["personality"] != "friendly" {
		t.Fatalf("explicit friendly personality override = %#v, want friendly", values["personality"])
	}

	root := &cli.RootOptions{ConfigOverrides: []string{`personality="none"`}}
	state := &codextui.State{Personality: "friendly"}
	start, err := remoteThreadStartParams(root, state)
	if err != nil {
		t.Fatalf("thread start: %v", err)
	}
	if start.Personality != nil || start.Config["personality"] != "none" {
		t.Fatalf("thread start personality = %#v config = %#v", start.Personality, start.Config["personality"])
	}
	turn, err := remoteTurnStartParams(root, state, "thread-1", codextea.SubmitRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("turn start: %v", err)
	}
	if turn.Personality != nil || turn.PersonalitySet || turn.Config["personality"] != "none" {
		t.Fatalf("turn start personality = %#v set=%v config = %#v", turn.Personality, turn.PersonalitySet, turn.Config["personality"])
	}
}
