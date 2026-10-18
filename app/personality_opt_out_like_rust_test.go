package app

import (
	"testing"

	"codex_go/cli"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// Rust #44946 retires Friendly/Pragmatic overrides from TUI requests while
// preserving an explicit `personality = "none"` opt-out.
func TestTUIRequestsForwardPersonalityOptOutOnlyLikeRust(t *testing.T) {
	for _, personality := range []string{"friendly", "pragmatic", ""} {
		state := &codextui.State{Personality: personality}
		if overrides := interactiveTurnPersonalityOverrides(state); len(overrides) != 0 {
			t.Fatalf("%q turn overrides = %#v, want none", personality, overrides)
		}
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

	state := &codextui.State{Personality: "none"}
	overrides := interactiveTurnPersonalityOverrides(state)
	if len(overrides) != 1 || overrides[0] != "personality=none" {
		t.Fatalf("none turn overrides = %#v", overrides)
	}
	start, err := remoteThreadStartParams(&cli.RootOptions{}, state)
	if err != nil {
		t.Fatalf("none thread start: %v", err)
	}
	if start.Personality == nil || *start.Personality != "none" {
		t.Fatalf("none thread start personality = %#v", start.Personality)
	}
	turn, err := remoteTurnStartParams(&cli.RootOptions{}, state, "thread-1", codextea.SubmitRequest{Prompt: "hello"})
	if err != nil {
		t.Fatalf("none turn start: %v", err)
	}
	if turn.Personality == nil || *turn.Personality != "none" || !turn.PersonalitySet {
		t.Fatalf("none turn personality = %#v set=%v", turn.Personality, turn.PersonalitySet)
	}
}

func TestRemoteConfigDropsRetiredPersonalityOverridesLikeRust(t *testing.T) {
	valuesFor := func(value string) map[string]any {
		t.Helper()
		requirement := ""
		if value != "" {
			requirement = "personality=\"" + value + "\""
		}
		root := &cli.RootOptions{}
		if requirement != "" {
			root.ConfigOverrides = []string{requirement}
		}
		values, err := remoteConfigValues(root, cli.SharedOptions{})
		if err != nil {
			t.Fatalf("remoteConfigValues(%q): %v", value, err)
		}
		return values
	}
	for _, retired := range []string{"friendly", "pragmatic"} {
		if values := valuesFor(retired); values["personality"] != nil {
			t.Fatalf("%q personality override forwarded: %#v", retired, values["personality"])
		}
	}
	if values := valuesFor("none"); values["personality"] != "none" {
		t.Fatalf("none personality override = %#v, want none", values["personality"])
	}
}
