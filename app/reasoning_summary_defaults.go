package app

// Rust parity: codex-rs/tui/src/app_server_session.rs
// `reasoning_summary_from_config` and `apply_prewarm_reasoning_summary`
// (#62904), plus the "`/config` reasoning summary" effective-value read
// (#62905/#62932). A TUI turn sends the configured `model_reasoning_summary`
// and defaults to `auto` for the OpenAI provider, so the first embedded
// WebSocket turn reuses the prewarm response instead of asking for a summary
// style the warmup never used.

import (
	"strings"

	"codex_go/appserver"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	modelpkg "codex_go/model"
	codextui "codex_go/tui"
)

// reasoningSummaryProviderIsOpenAI resolves whether the turn's provider is the
// OpenAI provider, mirroring Rust `ModelProviderInfo::is_openai` on
// `config.model_providers[config.model_provider_id]`. An empty provider id
// resolves to the built-in OpenAI provider, exactly like provider selection.
func reasoningSummaryProviderIsOpenAI(values map[string]any, providerID string) bool {
	providerID = strings.TrimSpace(providerID)
	if providerID == "" && values != nil {
		if configured, ok := values["model_provider"].(string); ok {
			providerID = strings.TrimSpace(configured)
		}
	}
	openAIBaseURL := ""
	if values != nil {
		if configured, ok := values["openai_base_url"].(string); ok {
			openAIBaseURL = strings.TrimSpace(configured)
		}
	}
	info, err := modelpkg.ProviderForConfigID(values, providerID, openAIBaseURL)
	return err == nil && info != nil && info.IsOpenAI()
}

// reasoningSummaryFromValues mirrors Rust `reasoning_summary_from_config`: an
// explicit `model_reasoning_summary` wins; otherwise the OpenAI provider
// defaults to `auto`. A non-OpenAI provider without an explicit setting
// returns "" so the server keeps its own default.
func reasoningSummaryFromValues(values map[string]any, providerID string) string {
	if values != nil {
		if raw, ok := values["model_reasoning_summary"].(string); ok {
			if value := strings.ToLower(strings.TrimSpace(raw)); value != "" {
				return value
			}
		}
	}
	if reasoningSummaryProviderIsOpenAI(values, providerID) {
		return "auto"
	}
	return ""
}

// remoteTurnReasoningSummary resolves the summary a TUI turn submits
// (Rust #62904 chatwidget input_submission: `reasoning_summary_from_config`).
// Launch-scoped choices win, then the connected server's effective value, then
// the OpenAI `auto` default.
func remoteTurnReasoningSummary(root *cli.RootOptions, state *codextui.State) string {
	for _, override := range launchReasoningOverrides(root) {
		if override.Path != "model_reasoning_summary" {
			continue
		}
		if value, ok := override.Value.(string); ok {
			if value = strings.ToLower(strings.TrimSpace(value)); value != "" {
				return value
			}
		}
	}
	if state != nil {
		if summary := strings.ToLower(strings.TrimSpace(state.ReasoningSummary)); summary != "" {
			return summary
		}
	}
	values := map[string]any{}
	if loaded, err := config.LoadEffectiveWithOptions(auth.DefaultCodexHome(), interactiveKeymapLoadOptions(root)); err == nil && loaded != nil {
		values = loaded.Values
	}
	providerID := ""
	if state != nil {
		providerID = strings.TrimSpace(state.Provider)
	}
	return reasoningSummaryFromValues(values, providerID)
}

// remoteSettingsReadCWD mirrors Rust `startup_config_cwd` (#62904/#62932):
// the directory a config/read uses for the effective new-chat settings. A
// remote session prefers the explicit --cd override and otherwise the resumed
// thread's working directory; Embedded and Cloud stay with the launch
// directory (Cloud never borrows the resumed cwd).
func remoteSettingsReadCWD(mode ThreadParamsMode, root *cli.RootOptions, state *codextui.State) string {
	override := ""
	if root != nil {
		override = strings.TrimSpace(root.Shared.CWD)
	}
	if override != "" {
		return override
	}
	if mode == ThreadParamsModeRemote && state != nil {
		return strings.TrimSpace(state.CWD)
	}
	return ""
}

// applyEmbeddedPrewarmReasoningSummary mirrors Rust
// `apply_prewarm_reasoning_summary`: only an in-process app server prewarms a
// response, so only the embedded thread/start carries the summary style and
// only when the effective configuration does not already set one.
func applyEmbeddedPrewarmReasoningSummary(params *appserver.ThreadStartParams, mode ThreadParamsMode, root *cli.RootOptions, state *codextui.State) {
	if params == nil || mode != ThreadParamsModeEmbedded {
		return
	}
	if params.Config != nil {
		if _, explicit := params.Config["model_reasoning_summary"]; explicit {
			return
		}
	}
	var summary string
	if state != nil && state.ReasoningSummaryAvailable {
		summary = strings.ToLower(strings.TrimSpace(state.ReasoningSummary))
	}
	if summary == "" {
		// An unset setting still lands on the OpenAI auto default, exactly as
		// reasoning_summary_from_config resolves it for the first turn.
		summary = remoteTurnReasoningSummary(root, state)
	}
	if summary == "" {
		return
	}
	if params.Config == nil {
		params.Config = map[string]any{}
	}
	params.Config["model_reasoning_summary"] = summary
}
