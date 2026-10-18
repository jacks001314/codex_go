package app

import (
	"os"
	"testing"

	"codex_go/cli"
	"codex_go/config"
	codextui "codex_go/tui"
)

// Rust #49858/#51207: a remote fresh thread carries the client's configured
// Daybreak default only when cli_daybreak is enabled.
func TestRemoteThreadStartStagesDaybreakDefaultLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)
	if err := os.WriteFile(config.ConfigPath(home), []byte("daybreak = true\nfeatures.cli_daybreak = true\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	params, err := remoteThreadStartParams(&cli.RootOptions{}, codextui.NewState(nil))
	if err != nil {
		t.Fatalf("remoteThreadStartParams: %v", err)
	}
	if params.DaybreakEnabled == nil || !*params.DaybreakEnabled {
		t.Fatalf("thread/start daybreakEnabled = %v, want true", params.DaybreakEnabled)
	}

	disabled := t.TempDir()
	t.Setenv("CODEX_HOME", disabled)
	if err := os.WriteFile(config.ConfigPath(disabled), []byte("daybreak = true\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	params, err = remoteThreadStartParams(&cli.RootOptions{}, codextui.NewState(nil))
	if err != nil {
		t.Fatalf("remoteThreadStartParams without feature: %v", err)
	}
	if params.DaybreakEnabled != nil {
		t.Fatalf("thread/start daybreakEnabled = %v, want unset without cli_daybreak", params.DaybreakEnabled)
	}
}
