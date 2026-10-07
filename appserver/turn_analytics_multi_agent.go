package appserver

import (
	"codex_go/config"
)

// turnAnalyticsMultiAgentVersion resolves the multi-agent version the turn
// analytics event reports, mirroring Rust #51333 (`5ddd19e8a9`): core's
// `track_turn_resolved_config_analytics` copies `turn_context.multi_agent_version`
// into the resolved-config fact, and the event serializes it as
// `multi_agent_version` (`disabled` | `v1` | `v2`).
//
// The resolution is the same one the turn's collaboration tool surface uses
// (history/inherited version first, then the model's declared version, then the
// stable `multi_agent` feature), so a turn reports the version it actually ran
// with. An empty result means the multi-agent surface is disabled, which the
// telemetry layer serializes as Rust's `MultiAgentVersion::Disabled`.
func (r *RuntimeRouter) turnAnalyticsMultiAgentVersion(threadID string, cfg *config.Config) string {
	if r == nil || cfg == nil {
		return ""
	}
	agentsConfig, err := cfg.AgentsConfig(r.configBaseDirForAgents())
	if err != nil {
		// Rust resolves the same subset from the config layer stack; a broken
		// agents table falls back to the feature/model resolution.
		agentsConfig = nil
	}
	return string(r.runtimeMultiAgentVersionForTurn(threadID, cfg, agentsConfig))
}
