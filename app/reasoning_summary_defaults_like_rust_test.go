package app

import (
	"os"
	"testing"

	"codex_go/cli"
	"codex_go/config"
	codextui "codex_go/tui"
	codextea "codex_go/tui/tea"
)

// TestReasoningSummaryFromValuesLikeRust mirrors Rust #62904
// app_server_session::reasoning_summary_from_config: the configured
// model_reasoning_summary wins, and the OpenAI provider falls back to auto.
func TestReasoningSummaryFromValuesLikeRust(t *testing.T) {
	openAI := map[string]any{}
	if got := reasoningSummaryFromValues(openAI, "openai"); got != "auto" {
		t.Fatalf("unset OpenAI summary = %q, want auto", got)
	}
	explicit := map[string]any{"model_reasoning_summary": " Detailed "}
	if got := reasoningSummaryFromValues(explicit, "openai"); got != "detailed" {
		t.Fatalf("explicit summary = %q, want detailed", got)
	}
	custom := map[string]any{
		"model_providers": map[string]any{
			"acme": map[string]any{"name": "Acme", "base_url": "https://acme.example/v1", "wire_api": "responses"},
		},
	}
	if got := reasoningSummaryFromValues(custom, "acme"); got != "" {
		t.Fatalf("unset non-OpenAI summary = %q, want empty", got)
	}
	if got := reasoningSummaryFromValues(custom, "openai"); got != "auto" {
		t.Fatalf("OpenAI summary next to a custom provider = %q, want auto", got)
	}
}

// TestRemoteTurnReasoningSummaryPrefersLaunchAndEffectiveLikeRust pins the
// precedence Rust #62904 relies on: an explicit launch choice wins, then the
// connected server effective value, then the OpenAI auto default.
func TestRemoteTurnReasoningSummaryPrefersLaunchAndEffectiveLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	state := codextui.NewState(nil)
	state.Provider = "openai"
	state.ReasoningSummary = "detailed"
	state.ReasoningSummaryAvailable = true
	if got := remoteTurnReasoningSummary(&cli.RootOptions{}, state); got != "detailed" {
		t.Fatalf("effective summary = %q, want detailed", got)
	}

	root := &cli.RootOptions{ConfigOverrides: []string{"model_reasoning_summary=concise"}}
	if got := remoteTurnReasoningSummary(root, state); got != "concise" {
		t.Fatalf("launch summary = %q, want concise", got)
	}

	unset := codextui.NewState(nil)
	unset.Provider = "openai"
	unset.ReasoningSummaryAvailable = true
	if got := remoteTurnReasoningSummary(&cli.RootOptions{}, unset); got != "auto" {
		t.Fatalf("unset OpenAI summary = %q, want auto", got)
	}

	custom := codextui.NewState(nil)
	custom.Provider = "acme"
	custom.ReasoningSummaryAvailable = true
	if err := os.WriteFile(config.ConfigPath(home), []byte("model_provider = \"acme\"\n[model_providers.acme]\nname = \"Acme\"\nbase_url = \"https://acme.example/v1\"\nwire_api = \"responses\"\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if got := remoteTurnReasoningSummary(&cli.RootOptions{}, custom); got != "" {
		t.Fatalf("unset non-OpenAI summary = %q, want empty", got)
	}
}

// TestEmbeddedPrewarmUsesTurnSummaryLikeRust covers Rust #62904
// apply_prewarm_reasoning_summary: only the embedded (in-process) thread/start
// carries the summary style the first turn will use.
func TestEmbeddedPrewarmUsesTurnSummaryLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	state := codextui.NewState(nil)
	state.Provider = "openai"
	state.ReasoningSummaryAvailable = true
	embedded, err := remoteThreadStartParamsForMode(ThreadParamsModeEmbedded, &cli.RootOptions{}, state)
	if err != nil {
		t.Fatalf("embedded thread/start params: %v", err)
	}
	if got, _ := embedded.Config["model_reasoning_summary"].(string); got != "auto" {
		t.Fatalf("embedded prewarm summary = %q, want auto", got)
	}

	daemon, err := remoteThreadStartParamsForMode(ThreadParamsModeRemote, &cli.RootOptions{}, state)
	if err != nil {
		t.Fatalf("remote thread/start params: %v", err)
	}
	if _, present := daemon.Config["model_reasoning_summary"]; present {
		t.Fatalf("remote prewarm config = %#v, want no summary", daemon.Config)
	}

	// A configured summary travels unchanged and is never replaced by auto.
	configured := codextui.NewState(nil)
	configured.Provider = "openai"
	configured.ReasoningSummary = "concise"
	configured.ReasoningSummaryAvailable = true
	params, err := remoteThreadStartParamsForMode(ThreadParamsModeEmbedded, &cli.RootOptions{}, configured)
	if err != nil {
		t.Fatalf("configured thread/start params: %v", err)
	}
	if got, _ := params.Config["model_reasoning_summary"].(string); got != "concise" {
		t.Fatalf("configured prewarm summary = %q, want concise", got)
	}
}

// TestRemoteTurnStartCarriesSummaryLikeRust mirrors Rust #62904
// chatwidget input_submission: a client-owned turn/start always submits the
// resolved summary, while a server-owned conversation keeps its settings.
func TestRemoteTurnStartCarriesSummaryLikeRust(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CODEX_HOME", home)

	state := codextui.NewState(nil)
	state.Provider = "openai"
	state.ReasoningSummaryAvailable = true
	request := codextea.SubmitRequest{Prompt: "hello"}
	params, err := remoteTurnStartParamsForMode(ThreadParamsModeEmbedded, &cli.RootOptions{}, state, "thread-1", request)
	if err != nil {
		t.Fatalf("turn/start params: %v", err)
	}
	if params.Summary == nil || *params.Summary != "auto" {
		t.Fatalf("turn summary = %v, want auto", params.Summary)
	}

	cloud, err := remoteTurnStartParamsForMode(ThreadParamsModeCloud, &cli.RootOptions{}, state, "thread-1", request)
	if err != nil {
		t.Fatalf("cloud turn/start params: %v", err)
	}
	if cloud.Summary != nil {
		t.Fatalf("server-owned turn summary = %v, want unset", *cloud.Summary)
	}
}

// TestRemoteSettingsReadCWDMirrorsStartupConfigCwdLikeRust covers Rust
// #62904/#62932 startup_config_cwd: a remote read falls back to the resumed
// thread directory, while --cd always wins and Cloud never borrows it.
func TestRemoteSettingsReadCWDMirrorsStartupConfigCwdLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	state.CWD = "/resumed/repo"
	if got := remoteSettingsReadCWD(ThreadParamsModeRemote, &cli.RootOptions{}, state); got != "/resumed/repo" {
		t.Fatalf("remote resumed cwd = %q, want /resumed/repo", got)
	}
	override := &cli.RootOptions{Shared: cli.SharedOptions{CWD: "/override"}}
	if got := remoteSettingsReadCWD(ThreadParamsModeRemote, override, state); got != "/override" {
		t.Fatalf("remote override cwd = %q, want /override", got)
	}
	if got := remoteSettingsReadCWD(ThreadParamsModeCloud, override, state); got != "/override" {
		t.Fatalf("cloud override cwd = %q, want /override", got)
	}
	if got := remoteSettingsReadCWD(ThreadParamsModeCloud, &cli.RootOptions{}, state); got != "" {
		t.Fatalf("cloud resumed cwd = %q, want empty", got)
	}
	if got := remoteSettingsReadCWD(ThreadParamsModeEmbedded, &cli.RootOptions{}, state); got != "" {
		t.Fatalf("embedded cwd = %q, want the launch default", got)
	}
}
