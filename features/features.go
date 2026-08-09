package features

import (
	"fmt"
	"runtime"
	"sort"
	"strings"
)

type Stage string

const (
	StageUnderDevelopment Stage = "under development"
	StageExperimental     Stage = "experimental"
	StageStable           Stage = "stable"
	StageDeprecated       Stage = "deprecated"
	StageRemoved          Stage = "removed"
)

type Spec struct {
	Key                         string
	Stage                       Stage
	DefaultEnabled              bool
	ExperimentalName            string
	ExperimentalMenuDescription string
	ExperimentalAnnouncement    string
}

var Registry = []Spec{
	{Key: "undo", Stage: StageRemoved},
	{Key: "shell_tool", Stage: StageStable, DefaultEnabled: true},
	{Key: "secret_auth_storage", Stage: StageStable, DefaultEnabled: runtime.GOOS == "windows"},
	{Key: "unified_exec", Stage: StageStable, DefaultEnabled: runtime.GOOS != "windows"},
	{Key: "shell_zsh_fork", Stage: StageUnderDevelopment},
	{Key: "unified_exec_zsh_fork", Stage: StageUnderDevelopment},
	{Key: "shell_snapshot", Stage: StageStable, DefaultEnabled: true},
	{Key: "deferred_executor", Stage: StageUnderDevelopment},
	{Key: "js_repl", Stage: StageRemoved},
	{Key: "executed_tool_call_metadata", Stage: StageUnderDevelopment},
	// Rust (codex-rs/features/src/lib.rs) defines code_mode with
	// default_enabled: false. An unset model tool_mode therefore resolves to
	// direct mode unless code_mode is explicitly enabled; models that require
	// code mode declare tool_mode in the catalog instead.
	{Key: "code_mode", Stage: StageUnderDevelopment, DefaultEnabled: false},
	{Key: "code_mode_buffered_exec", Stage: StageRemoved},
	{Key: "code_mode_host", Stage: StageStable, DefaultEnabled: true},
	{Key: "code_mode_only", Stage: StageUnderDevelopment},
	{Key: "js_repl_tools_only", Stage: StageRemoved},
	{Key: "terminal_resize_reflow", Stage: StageRemoved, DefaultEnabled: true},
	{Key: "web_search_request", Stage: StageDeprecated},
	{Key: "web_search_cached", Stage: StageDeprecated},
	{Key: "standalone_web_search", Stage: StageUnderDevelopment},
	{Key: "search_tool", Stage: StageRemoved},
	{Key: "codex_git_commit", Stage: StageRemoved},
	{Key: "runtime_metrics", Stage: StageUnderDevelopment},
	{Key: "sqlite", Stage: StageRemoved, DefaultEnabled: true},
	{
		Key:                         "memories",
		Stage:                       StageExperimental,
		ExperimentalName:            "Memories",
		ExperimentalMenuDescription: "Allow Codex to create new memories from conversations and bring relevant memories into new conversations.",
		ExperimentalAnnouncement:    "NEW: Codex can now generate and use memories. Try it now with `/memories`",
	},
	{Key: "external_agent_memory_import", Stage: StageUnderDevelopment},
	{Key: "local_thread_store_compression", Stage: StageUnderDevelopment},
	{Key: "background_paginated_rollout_migration", Stage: StageUnderDevelopment},
	{Key: "chronicle", Stage: StageUnderDevelopment},
	{Key: "apply_patch_freeform", Stage: StageRemoved},
	{Key: "apply_patch_streaming_events", Stage: StageUnderDevelopment},
	{Key: "exec_permission_approvals", Stage: StageUnderDevelopment},
	{Key: "hooks", Stage: StageStable, DefaultEnabled: true},
	{Key: "request_permissions_tool", Stage: StageUnderDevelopment},
	{Key: "use_linux_sandbox_bwrap", Stage: StageRemoved},
	{Key: "use_legacy_landlock", Stage: StageDeprecated},
	{Key: "request_rule", Stage: StageRemoved},
	{Key: "experimental_windows_sandbox", Stage: StageRemoved},
	{Key: "elevated_windows_sandbox", Stage: StageRemoved},
	{Key: "remote_models", Stage: StageRemoved},
	{Key: "enable_request_compression", Stage: StageStable, DefaultEnabled: true},
	{
		Key:                         "network_proxy",
		Stage:                       StageExperimental,
		ExperimentalName:            "Network proxy",
		ExperimentalMenuDescription: "Apply network proxy restrictions to sandboxed sessions that already have network access.",
		ExperimentalAnnouncement:    "NEW: Network proxy can now be enabled from /experimental. Restart Codex after enabling it.",
	},
	{Key: "respect_system_proxy", Stage: StageUnderDevelopment},
	{Key: "multi_agent", Stage: StageStable, DefaultEnabled: true},
	{Key: "multi_agent_v2", Stage: StageUnderDevelopment},
	{Key: "multi_agent_mode", Stage: StageRemoved},
	{Key: "enable_fanout", Stage: StageUnderDevelopment},
	{Key: "apps", Stage: StageStable, DefaultEnabled: true},
	{Key: "enable_mcp_apps", Stage: StageUnderDevelopment},
	{Key: "mcp_2026_07_28", Stage: StageUnderDevelopment},
	{Key: "apps_mcp_path_override", Stage: StageRemoved},
	{Key: "tool_search", Stage: StageRemoved},
	{Key: "tool_search_always_defer_mcp_tools", Stage: StageRemoved, DefaultEnabled: true},
	{Key: "deferred_tool_world_state", Stage: StageUnderDevelopment},
	{Key: "non_prefixed_mcp_tool_names", Stage: StageUnderDevelopment},
	{Key: "unavailable_dummy_tools", Stage: StageRemoved},
	{Key: "tool_suggest", Stage: StageStable, DefaultEnabled: true},
	{Key: "recommended_plugins", Stage: StageStable, DefaultEnabled: false},
	{Key: "plugins", Stage: StageStable, DefaultEnabled: true},
	{Key: "executor_capability_discovery", Stage: StageUnderDevelopment},
	{Key: "plugin_hooks", Stage: StageRemoved},
	{Key: "in_app_browser", Stage: StageStable, DefaultEnabled: true},
	{Key: "in_app_updates", Stage: StageStable, DefaultEnabled: true},
	{Key: "browser_use", Stage: StageStable, DefaultEnabled: true},
	{Key: "browser_use_full_cdp_access", Stage: StageStable, DefaultEnabled: true},
	{Key: "browser_use_external", Stage: StageStable, DefaultEnabled: true},
	{Key: "computer_use", Stage: StageStable, DefaultEnabled: true},
	{Key: "remote_plugin", Stage: StageStable, DefaultEnabled: true},
	{Key: "plugin_sharing", Stage: StageStable, DefaultEnabled: true},
	{Key: "external_migration", Stage: StageRemoved},
	{Key: "image_generation", Stage: StageStable, DefaultEnabled: true},
	{Key: "view_image", Stage: StageStable, DefaultEnabled: true},
	{Key: "image_resize_notice", Stage: StageUnderDevelopment, DefaultEnabled: false},
	{Key: "unified_image_budget", Stage: StageUnderDevelopment, DefaultEnabled: false},
	{Key: "resize_all_images", Stage: StageRemoved, DefaultEnabled: true},
	{Key: "item_ids", Stage: StageUnderDevelopment},
	{Key: "concurrent_reasoning_summaries", Stage: StageUnderDevelopment},
	{Key: "skill_mcp_dependency_install", Stage: StageStable, DefaultEnabled: true},
	{Key: "skill_search", Stage: StageStable, DefaultEnabled: true},
	{Key: "skill_env_var_dependency_prompt", Stage: StageRemoved},
	{Key: "mentions_v2", Stage: StageStable, DefaultEnabled: true},
	{Key: "steer", Stage: StageRemoved, DefaultEnabled: true},
	{Key: "default_mode_request_user_input", Stage: StageUnderDevelopment},
	{Key: "terminal_visualization_instructions", Stage: StageUnderDevelopment},
	{Key: "guardian_approval", Stage: StageStable, DefaultEnabled: true},
	{Key: "guardianv2", Stage: StageUnderDevelopment},
	{Key: "goals", Stage: StageStable, DefaultEnabled: true},
	{Key: "token_budget", Stage: StageUnderDevelopment},
	{Key: "rollout_budget", Stage: StageUnderDevelopment},
	{Key: "current_time_reminder", Stage: StageUnderDevelopment},
	{Key: "collaboration_modes", Stage: StageRemoved, DefaultEnabled: true},
	{Key: "tool_call_mcp_elicitation", Stage: StageStable, DefaultEnabled: true},
	{Key: "auth_elicitation", Stage: StageUnderDevelopment},
	{Key: "personality", Stage: StageStable, DefaultEnabled: true},
	{Key: "artifact", Stage: StageUnderDevelopment},
	{Key: "fast_mode", Stage: StageStable, DefaultEnabled: true},
	{Key: "realtime_conversation", Stage: StageUnderDevelopment},
	{Key: "remote_control", Stage: StageRemoved},
	{Key: "image_detail_original", Stage: StageRemoved},
	{Key: "tui_app_server", Stage: StageRemoved, DefaultEnabled: true},
	{
		Key:                         "prevent_idle_sleep",
		Stage:                       preventIdleSleepStage(),
		ExperimentalName:            "Prevent sleep while running",
		ExperimentalMenuDescription: "Keep your computer awake while Codex is running a thread.",
		ExperimentalAnnouncement:    "NEW: Prevent sleep while running is now available in /experimental.",
	},
	{Key: "workspace_owner_usage_nudge", Stage: StageRemoved},
	{Key: "responses_websockets", Stage: StageRemoved},
	{Key: "responses_websockets_v2", Stage: StageRemoved},
	{Key: "remote_compaction_v2", Stage: StageStable, DefaultEnabled: true},
	{Key: "use_agent_identity", Stage: StageUnderDevelopment},
	{Key: "workspace_dependencies", Stage: StageStable, DefaultEnabled: true},
}

func preventIdleSleepStage() Stage {
	switch runtime.GOOS {
	case "darwin", "linux", "windows":
		return StageExperimental
	default:
		return StageUnderDevelopment
	}
}

func Known(key string) bool {
	_, ok := CanonicalKey(key)
	return ok
}

func Validate(key string) error {
	if Known(key) {
		return nil
	}
	return fmt.Errorf("Unknown feature flag: %s", key)
}

func Defaults() map[string]bool {
	out := make(map[string]bool, len(Registry))
	for _, spec := range Registry {
		out[spec.Key] = spec.DefaultEnabled
	}
	return out
}

func Enabled(settings map[string]bool, key string) bool {
	defaults := Defaults()
	enabled := defaults[key]
	if value, ok := settings[key]; ok {
		enabled = value
	}
	return enabled
}

func ModelClientBetaFeaturesHeader(settings map[string]bool) string {
	effective := Defaults()
	for key, enabled := range settings {
		effective[key] = enabled
	}
	keys := []string{}
	for _, spec := range Registry {
		if !featureAdvertisedInModelClientHeader(&spec) || !effective[spec.Key] {
			continue
		}
		keys = append(keys, spec.Key)
	}
	return strings.Join(keys, ",")
}

func featureAdvertisedInModelClientHeader(spec *Spec) bool {
	if spec == nil {
		return false
	}
	return spec.Stage == StageExperimental || spec.Key == "remote_compaction_v2"
}

func Sorted() []Spec {
	out := append([]Spec(nil), Registry...)
	sort.Slice(out, func(i, j int) bool {
		return out[i].Key < out[j].Key
	})
	return out
}

func byKey() map[string]Spec {
	out := make(map[string]Spec, len(Registry))
	for _, spec := range Registry {
		out[spec.Key] = spec
	}
	return out
}
