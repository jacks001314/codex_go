package app

import (
	"os"
	"strings"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
)

// Local daemon launch policy (Rust tui/src/daemon_startup.rs and
// app_server_target_for_launch, #46088).
//
// An interactive launch may reuse the shared local background server only when
// the launch is eligible and a compatible daemon is reachable. An explicit
// --remote always wins, --no-daemon never probes, and ineligible launches stay
// embedded. Every automatic launch still falls back to embedded mode when no
// daemon answers (Rust `AppServerTarget::LocalDaemon { allow_embedded_fallback }`).

const (
	// daemonFailureHint mirrors Rust daemon_startup::FAILURE_HINT.
	daemonFailureHint = "To work without the background server, rerun the same command with --no-daemon (including resume or fork and its arguments)."
	// daemonOverviewHint mirrors the Rust #46088 agents-overview guidance.
	daemonOverviewHint = "The agents overview requires a shared server. Use codex --no-daemon to work without it."

	// daemonNoDaemonWithRemote mirrors the Rust rejection text.
	daemonNoDaemonWithRemote = "--no-daemon cannot be used with --remote."
	// daemonNoDaemonWithAgents mirrors the Rust rejection text.
	daemonNoDaemonWithAgents = "--no-daemon cannot be used with codex agents. The agents overview requires a shared server. Use codex --no-daemon to work without it."
	// daemonNoDaemonWithQueue mirrors the Rust rejection text.
	daemonNoDaemonWithQueue = "--no-daemon cannot be used with codex queue. Queuing must discover the shared server to avoid writing through a separate server."
)

// daemonServerFeatureKeys mirrors Rust daemon_startup::SERVER_FEATURES: shared
// services and threadless MCP operations need daemon compatibility checks.
var daemonServerFeatureKeys = map[string]bool{
	"api_key_model_discovery":        true,
	"code_mode_host":                 true,
	"auth_elicitation":               true,
	"mcp_oauth_refresh_coordination": true,
}

// daemonAllowedFeatureKeys mirrors Rust daemon_startup::allowed_feature: the
// feature keys a client may set on a shared-server launch.
var daemonAllowedFeatureKeys = map[string]bool{
	// Client gates and per-thread settings already forwarded in thread requests.
	"daemon_auto_start":     true,
	"worktrees":             true,
	"realtime_conversation": true,
	"standalone_web_search": true,
	// Shared services and threadless MCP operations need daemon compatibility checks.
	"api_key_model_discovery":        true,
	"code_mode_host":                 true,
	"auth_elicitation":               true,
	"mcp_oauth_refresh_coordination": true,
	// Removed flags still passed by older launch scripts.
	"remote_models":                      true,
	"request_rule":                       true,
	"responses_websockets_v2":            true,
	"workspace_owner_usage_nudge":        true,
	"tool_search_always_defer_mcp_tools": true,
	"remote_compaction_v2":               true,
	"multi_agent_mode":                   true,
	// Go's own aliases that older launch scripts may still pass.
	"use_legacy_landlock": true,
}

// daemonStartupExclusion reports why this launch must not reuse the shared
// background server, or "" when reuse is allowed (Rust
// daemon_startup::exclusion).
func daemonStartupExclusion(root *cli.RootOptions, agentsOverview bool) string {
	if root == nil {
		return ""
	}
	if root.Shared.NoDaemon {
		return "--no-daemon"
	}
	if root.Shared.OSS || strings.TrimSpace(root.Shared.OSSProvider) != "" {
		return "--oss"
	}
	if auth.IsWorkloadIdentitySelected() {
		return "workload identity"
	}
	if strings.TrimSpace(os.Getenv(appserver.CodexExecServerURLEnvVar)) != "" {
		return "executor selection (CODEX_EXEC_SERVER_URL)"
	}
	if agentsOverview {
		return ""
	}
	if strings.TrimSpace(root.Shared.Profile) != "" {
		return "--profile"
	}
	return daemonConfigExclusion(root)
}

// daemonConfigExclusion mirrors Rust daemon_startup::config_exclusion: the
// launch's configuration overrides must be replayable by a shared server.
// Go's interactive path has no configuration-loader overrides, so that Rust
// input has no Go counterpart.
func daemonConfigExclusion(root *cli.RootOptions) string {
	if !daemonOverridesAreReplayable(daemonLaunchOverrides(root)) {
		return "command-line configuration overrides (-c, --enable, --disable, or --search)"
	}
	if root.StrictConfig {
		return "--strict-config"
	}
	if root.Shared.DangerouslyBypassHookTrust {
		return "--dangerously-bypass-hook-trust"
	}
	return ""
}

// daemonLaunchOverrides renders the launch's configuration overrides the way
// Rust's CliConfigOverrides does: `-c` plus the `--enable`/`--disable` feature
// flags and `--search`'s web-search mode.
func daemonLaunchOverrides(root *cli.RootOptions) []config.Override {
	if root == nil {
		return nil
	}
	raw := append([]string(nil), root.ConfigOverrides...)
	for _, feature := range root.EnableFeatures {
		raw = append(raw, "features."+strings.TrimSpace(feature)+"=true")
	}
	for _, feature := range root.DisableFeatures {
		raw = append(raw, "features."+strings.TrimSpace(feature)+"=false")
	}
	if root.Shared.Search {
		raw = append(raw, `web_search="live"`)
	}
	overrides, err := config.ParseOverrides(raw)
	if err != nil {
		// An unparseable override cannot be replayed by the server.
		return []config.Override{{Path: "", Value: nil}}
	}
	return overrides
}

// daemonOverridesAreReplayable mirrors Rust's replayability predicate: only the
// unstable-features-warning switch and known feature keys may be set, every
// value must be a boolean (or a non-empty table of boolean features), and no
// shared-server feature may be disabled.
func daemonOverridesAreReplayable(overrides []config.Override) bool {
	for _, override := range overrides {
		switch override.Path {
		case "suppress_unstable_features_warning":
			if _, ok := override.Value.(bool); !ok {
				return false
			}
		case "features":
			table, ok := override.Value.(map[string]any)
			if !ok || len(table) == 0 {
				return false
			}
			for name, value := range table {
				if !daemonAllowedFeatureKeys[strings.TrimSpace(name)] {
					return false
				}
				if _, ok := value.(bool); !ok {
					return false
				}
			}
		default:
			name, ok := strings.CutPrefix(override.Path, "features.")
			if !ok || !daemonAllowedFeatureKeys[strings.TrimSpace(name)] {
				return false
			}
			if _, ok := override.Value.(bool); !ok {
				return false
			}
		}
	}
	return !daemonServerFeatureDisabled(overrides)
}

// daemonServerFeatureDisabled mirrors Rust's `server_features(...).values().any(|enabled| !enabled)`:
// a launch that disables a shared-server feature cannot reuse a daemon that may
// rely on it.
func daemonServerFeatureDisabled(overrides []config.Override) bool {
	for _, override := range overrides {
		if override.Path == "features" {
			if table, ok := override.Value.(map[string]any); ok {
				for name, value := range table {
					if !daemonServerFeatureKeys[strings.TrimSpace(name)] {
						continue
					}
					if enabled, ok := value.(bool); ok && !enabled {
						return true
					}
				}
			}
			continue
		}
		name, ok := strings.CutPrefix(override.Path, "features.")
		if !ok || !daemonServerFeatureKeys[strings.TrimSpace(name)] {
			continue
		}
		if enabled, ok := override.Value.(bool); ok && !enabled {
			return true
		}
	}
	return false
}

// localDaemonEndpointForLaunch returns the shared background server endpoint
// this launch should target, or nil to run embedded. socketPath is the
// daemon's control socket; a launch only targets it when the launch is eligible
// and a compatible daemon answers.
func localDaemonEndpointForLaunch(root *cli.RootOptions, agentsOverview bool, socketPath string) *appserverdaemon.RemoteAppServerEndpoint {
	return localDaemonEndpointForLaunchWithProbe(root, agentsOverview, socketPath, func(path string) error {
		_, err := appserverdaemon.ProbeAppServerVersionOnSocket(path, appserverdaemon.ControlSocketProbeTimeout)
		return err
	})
}

// localDaemonEndpointForLaunchWithProbe is the injectable form of
// localDaemonEndpointForLaunch: the probe reports whether a compatible daemon
// answers on the socket.
func localDaemonEndpointForLaunchWithProbe(root *cli.RootOptions, agentsOverview bool, socketPath string, probe func(string) error) *appserverdaemon.RemoteAppServerEndpoint {
	socketPath = strings.TrimSpace(socketPath)
	if socketPath == "" {
		return nil
	}
	if daemonStartupExclusion(root, agentsOverview) != "" {
		return nil
	}
	if probe == nil || probe(socketPath) != nil {
		return nil
	}
	return appserverdaemon.NewUnixSocketEndpoint(socketPath)
}

// defaultLocalDaemonSocketPath is the control socket of the shared local
// background server for this codex home.
func defaultLocalDaemonSocketPath() string {
	return appserver.AppServerControlSocketPath(auth.DefaultCodexHome())
}

// interactiveRootNoDaemon reports whether either the root flags or the
// subcommand flags requested --no-daemon (Rust merges the subcommand CLI into
// the interactive CLI).
func interactiveRootNoDaemon(root *cli.RootOptions, subcommand *cli.SharedOptions) bool {
	if root != nil && root.Shared.NoDaemon {
		return true
	}
	return subcommand != nil && subcommand.NoDaemon
}

// agentsSharedOptions exposes an agents command's shared flags for the
// daemon-eligibility check, including the hidden agents-only --no-daemon.
func agentsSharedOptions(opts *cli.AgentsOptions) *cli.SharedOptions {
	if opts == nil {
		return nil
	}
	shared := opts.Shared
	if opts.NoDaemon {
		shared.NoDaemon = true
	}
	return &shared
}
