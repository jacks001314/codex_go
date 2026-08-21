package appserver

import (
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
)

type rustAppServerV2SuiteCase struct {
	Module string
	Owner  string
	Focus  string
}

func rustAppServerV2SuiteManifest() []rustAppServerV2SuiteCase {
	return []rustAppServerV2SuiteCase{
		{Module: "account", Owner: "auth", Focus: "account read/login/logout/rate-limit RPCs"},
		{Module: "account_thread_usage", Owner: "auth, appserver", Focus: "account usage/read thread-level estimation"},
		{Module: "analytics", Owner: "telemetry", Focus: "analytics enablement and payload capture"},
		{Module: "app_installed", Owner: "apps", Focus: "app/installed RPC"},
		{Module: "app_list", Owner: "apps", Focus: "app list RPC and update notifications"},
		{Module: "app_read", Owner: "apps", Focus: "app/read RPC"},
		{Module: "attestation", Owner: "appserver, model", Focus: "attestation server request and Responses header"},
		{Module: "auto_env", Owner: "appserver, execserver", Focus: "auto environment selection"},
		{Module: "client_metadata", Owner: "model", Focus: "Responses API client metadata propagation"},
		{Module: "code_mode_host", Owner: "codemode", Focus: "code mode host lifecycle and tool dispatch"},
		{Module: "collaboration_mode_list", Owner: "appserver", Focus: "collaborationMode/list presets"},
		{Module: "command_exec", Owner: "appserver", Focus: "command/exec RPC, output streaming, and launch-context env scrubbing"},
		{Module: "compaction", Owner: "compact, turn", Focus: "thread compaction flow"},
		{Module: "config_requirements_in_app_browser", Owner: "config, appserver", Focus: "config requirements and in-app browser import gating"},
		{Module: "config_rpc", Owner: "config, appserver", Focus: "config read/write/requirements RPCs incl. auto-review requirements"},
		{Module: "connection_handling_websocket", Owner: "appserver", Focus: "websocket connection lifecycle"},
		{Module: "connection_handling_websocket_unix", Owner: "appserver", Focus: "unix websocket transport lifecycle"},
		{Module: "current_time", Owner: "appserver", Focus: "currentTime/read server request"},
		{Module: "curated_mcp_sync", Owner: "mcp", Focus: "curated MCP synchronization"},
		{Module: "dynamic_tools", Owner: "tool, turn", Focus: "dynamic tool call server request"},
		{Module: "environment_add", Owner: "appserver, execserver", Focus: "environment/add RPC"},
		{Module: "environment_info", Owner: "appserver, execserver", Focus: "environment/info RPC"},
		{Module: "environment_status", Owner: "appserver, execserver", Focus: "environment/status RPC"},
		{Module: "exec_server_test_support", Owner: "execserver", Focus: "Rust exec-server suite helper parity"},
		{Module: "executor_mcp", Owner: "mcp", Focus: "executor-scoped MCP registration and tools"},
		{Module: "executor_skills", Owner: "prompt, plugin", Focus: "executor-scoped skills and manifest focus"},
		{Module: "experimental_api", Owner: "appserver", Focus: "experimental API gating"},
		{Module: "experimental_feature_list", Owner: "features", Focus: "experimentalFeature/list RPC"},
		{Module: "external_agent_config", Owner: "appserver", Focus: "external agent config detect/import incl. bounded Cursor path resolution and I/O failure subtypes"},
		{Module: "external_agent_import_sync", Owner: "appserver, config, rollout", Focus: "incremental imported-session suffix synchronization"},
		{Module: "feedback", Owner: "appserver", Focus: "feedback submission"},
		{Module: "fs", Owner: "appserver", Focus: "fs read/write/watch RPCs"},
		{Module: "git_attribution", Owner: "appserver, session", Focus: "git attribution propagation"},
		{Module: "guardian_v2", Owner: "appserver, guardian", Focus: "Guardian V2 approval routing, risk scores and managed-reviewer gating"},
		{Module: "history_notes_extension", Owner: "token-budget, history, notes", Focus: "history and notes tools for token-budget sessions"},
		{Module: "hooks_list", Owner: "appserver, tool", Focus: "hooks/list RPC incl. execution mode"},
		{Module: "host_skills", Owner: "prompt, execserver", Focus: "host-provided skill discovery"},
		{Module: "imagegen_extension", Owner: "tool, model", Focus: "image generation extension"},
		{Module: "initialize", Owner: "appserver", Focus: "initialize capabilities and protocol setup"},
		{Module: "marketplace_add", Owner: "plugin", Focus: "marketplace/add RPC"},
		{Module: "marketplace_remove", Owner: "plugin", Focus: "marketplace/remove RPC"},
		{Module: "marketplace_upgrade", Owner: "plugin", Focus: "marketplace/upgrade RPC"},
		{Module: "mcp_resource", Owner: "mcp", Focus: "mcpServer/resource/read RPC"},
		{Module: "mcp_resource_origin", Owner: "mcp", Focus: "MCP resource reads scoped by origin and connector"},
		{Module: "mcp_event_stream", Owner: "mcp, appserver", Focus: "MCP server event streaming notifications"},
		{Module: "mcp_server_elicitation", Owner: "mcp, appserver", Focus: "MCP elicitation server request"},
		{Module: "mcp_server_status", Owner: "mcp", Focus: "MCP status list/update notifications"},
		{Module: "mcp_tool", Owner: "mcp", Focus: "mcpServer/tool/call RPC"},
		{Module: "memory_reset", Owner: "memories", Focus: "memory/reset RPC"},
		{Module: "misalignment_policy", Owner: "appserver, protocol", Focus: "misalignment policy violations surfaced as typed errors (Rust #38682)"},
		{Module: "model_auto_review", Owner: "appserver, config", Focus: "auto_review required-on-models enforcement and requirements exposure"},
		{Module: "model_list", Owner: "model", Focus: "model/list RPC"},
		{Module: "model_provider_capabilities_read", Owner: "model", Focus: "modelProvider/capabilities/read RPC"},
		{Module: "multi_agent_v2_developer_instructions", Owner: "agent, prompt", Focus: "multi-agent developer instruction propagation"},
		{Module: "otel", Owner: "telemetry, appserver", Focus: "OpenTelemetry provider reload after account and config changes"},
		{Module: "output_schema", Owner: "model, turn", Focus: "turn output schema propagation"},
		{Module: "permission_profile_list", Owner: "sandbox, appserver", Focus: "permissionProfile/list RPC"},
		{Module: "plan_item", Owner: "turn, appserver", Focus: "plan item notifications"},
		{Module: "plugin_install", Owner: "plugin", Focus: "plugin/install RPC and analytics subtypes"},
		{Module: "plugin_list", Owner: "plugin", Focus: "plugin/list and installed plugins"},
		{Module: "plugin_read", Owner: "plugin", Focus: "plugin/read and plugin skill read"},
		{Module: "plugin_search", Owner: "plugin", Focus: "experimental plugin/search RPC and remote plugin search"},
		{Module: "plugin_share", Owner: "plugin", Focus: "plugin share save/list/update/delete"},
		{Module: "plugin_uninstall", Owner: "plugin", Focus: "plugin/uninstall RPC"},
		{Module: "process_exec", Owner: "appserver", Focus: "process/spawn stdio/pty lifecycle and launch-context env scrubbing"},
		{Module: "projects", Owner: "state, appserver", Focus: "experimental project/list/read/create/import/update/move/delete RPCs, thread assignment and notifications"},
		{Module: "rate_limit_reset_credits", Owner: "auth, appserver", Focus: "rate-limit reset credit RPCs"},
		{Module: "rate_limits", Owner: "auth, model", Focus: "account rate-limit notifications"},
		{Module: "realtime_conversation", Owner: "realtime", Focus: "thread realtime RPCs and notifications"},
		{Module: "recommended_plugins", Owner: "plugin", Focus: "recommended plugin metadata"},
		{Module: "remote_control", Owner: "remotecontrol", Focus: "remote-control pairing and status RPCs"},
		{Module: "remote_thread_store", Owner: "session, appserver", Focus: "remote thread store debug fixture"},
		{Module: "request_permissions", Owner: "appserver, sandbox", Focus: "permissions approval server request"},
		{Module: "request_user_input", Owner: "tool, appserver", Focus: "request_user_input server request"},
		{Module: "request_validation", Owner: "appserver", Focus: "JSON-RPC request validation"},
		{Module: "residency", Owner: "config, model", Focus: "managed residency enforcement for model providers"},
		{Module: "review", Owner: "review, appserver", Focus: "review/start RPC"},
		{Module: "rollout_migration", Owner: "rollout, state, appserver", Focus: "legacy rollout to paginated history migration (dry-run/apply, canonicalization, journaling, subagent replay bounds)"},
		{Module: "safety_check_downgrade", Owner: "safety, appserver", Focus: "safety downgrade warnings"},
		{Module: "selected_capability_stack", Owner: "appserver, turn", Focus: "selected capability stack"},
		{Module: "selected_environment", Owner: "appserver, execserver", Focus: "selected environment propagation"},
		{Module: "server_diagnostics", Owner: "appserver, telemetry", Focus: "server/diagnostics experimental API and gauges"},
		{Module: "session_end", Owner: "hooks, appserver", Focus: "session end lifecycle and hooks"},
		{Module: "skills_list", Owner: "appserver, prompt", Focus: "skills/list RPC"},
		{Module: "sleep", Owner: "tool, turn", Focus: "sleep tool item lifecycle"},
		{Module: "thread_archive", Owner: "session, appserver", Focus: "thread/archive RPC"},
		{Module: "thread_delete", Owner: "session, appserver", Focus: "thread/delete RPC"},
		{Module: "thread_fork", Owner: "session, appserver", Focus: "thread/fork RPC"},
		{Module: "thread_inject_items", Owner: "session, appserver", Focus: "thread/inject_items RPC"},
		{Module: "thread_list", Owner: "session, appserver", Focus: "thread/list RPC"},
		{Module: "thread_loaded_list", Owner: "session, appserver", Focus: "thread/loaded/list RPC"},
		{Module: "thread_memory_mode_set", Owner: "memories, appserver", Focus: "thread/memoryMode/set RPC"},
		{Module: "thread_metadata_update", Owner: "session, appserver", Focus: "thread/metadata/update RPC"},
		{Module: "thread_name_websocket", Owner: "session, appserver", Focus: "thread/name/set websocket updates"},
		{Module: "thread_queue", Owner: "session, appserver", Focus: "experimental thread queue APIs and notifications"},
		{Module: "thread_read", Owner: "session, appserver", Focus: "thread/read RPC"},
		{Module: "thread_resume", Owner: "session, appserver", Focus: "thread/resume RPC"},
		{Module: "thread_revert", Owner: "session, appserver", Focus: "thread/revert RPC and reload notifications"},
		{Module: "thread_rollback", Owner: "session, appserver", Focus: "thread/rollback RPC"},
		{Module: "thread_settings_update", Owner: "session, appserver", Focus: "thread/settings/update RPC"},
		{Module: "thread_sections", Owner: "appserver, session", Focus: "thread section list/create/update/delete/move RPCs"},
		{Module: "thread_shell_command", Owner: "appserver", Focus: "thread/shellCommand RPC"},
		{Module: "thread_start", Owner: "session, appserver", Focus: "thread/start RPC"},
		{Module: "thread_status", Owner: "session, appserver", Focus: "thread status notifications"},
		{Module: "thread_unarchive", Owner: "session, appserver", Focus: "thread/unarchive RPC"},
		{Module: "thread_unsubscribe", Owner: "session, appserver", Focus: "thread/unsubscribe RPC"},
		{Module: "turn_interrupt", Owner: "turn, appserver", Focus: "turn/interrupt RPC"},
		{Module: "turn_start", Owner: "turn, appserver", Focus: "turn/start runtime and notifications"},
		{Module: "turn_start_zsh_fork", Owner: "turn, shell", Focus: "zsh fork shell tool behavior"},
		{Module: "turn_steer", Owner: "turn, appserver", Focus: "turn/steer RPC"},
		{Module: "view_image", Owner: "tool, appserver", Focus: "view_image feature gate and model image capability"},
		{Module: "web_search", Owner: "tool, codexapi", Focus: "web search tool round trip"},
		{Module: "windows_sandbox_setup", Owner: "sandbox, appserver", Focus: "windows sandbox setup RPC"},
	}
}

func TestRustAppServerV2SuiteManifestCoversRustModules(t *testing.T) {
	root := rustAppServerV2SuiteRoot(t)
	modules := rustAppServerV2ModulesFromMod(t, filepath.Join(root, "mod.rs"))
	actual := make(map[string]bool, len(modules))
	for _, module := range modules {
		actual[module] = true
	}

	expected := map[string]rustAppServerV2SuiteCase{}
	for _, entry := range rustAppServerV2SuiteManifest() {
		if entry.Module == "" || entry.Owner == "" || entry.Focus == "" {
			t.Fatalf("incomplete app-server v2 suite manifest entry: %#v", entry)
		}
		if previous, ok := expected[entry.Module]; ok {
			t.Fatalf("duplicate app-server v2 suite manifest entry for %q: %#v and %#v", entry.Module, previous, entry)
		}
		expected[entry.Module] = entry
		if _, err := os.Stat(filepath.Join(root, entry.Module+".rs")); err != nil {
			t.Fatalf("manifest module %q does not match a Rust suite file: %v", entry.Module, err)
		}
	}

	var missing []string
	for module := range actual {
		if _, ok := expected[module]; !ok {
			missing = append(missing, module)
		}
	}
	sort.Strings(missing)

	var stale []string
	for module := range expected {
		if !actual[module] {
			stale = append(stale, module)
		}
	}
	sort.Strings(stale)

	if len(missing) > 0 || len(stale) > 0 {
		t.Fatalf("Rust app-server v2 suite manifest drift; missing=%v stale=%v", missing, stale)
	}
}

func TestRustAppServerV2ManifestOwnerCoverage(t *testing.T) {
	manifest := rustAppServerV2SuiteManifest()
	if len(manifest) == 0 {
		t.Fatal("Rust app-server v2 suite manifest is empty")
	}
	owners := map[string]int{}
	for _, entry := range manifest {
		owners[entry.Owner]++
	}
	for _, owner := range []string{
		"appserver",
		"turn, appserver",
		"session, appserver",
		"plugin",
		"mcp",
		"model",
	} {
		if owners[owner] == 0 {
			t.Fatalf("Rust app-server v2 suite manifest lacks owner grouping %q", owner)
		}
	}
}

func rustAppServerV2SuiteRoot(t *testing.T) string {
	t.Helper()
	candidates := []string{}
	if env := os.Getenv("CODEX_RUST_APP_SERVER_SUITE_V2"); env != "" {
		candidates = append(candidates, env)
	}
	candidates = append(candidates,
		filepath.Join("..", "..", "git", "codex", "codex-rs", "app-server", "tests", "suite", "v2"),
		filepath.Join("..", "..", "..", "codex-main", "codex-rs", "app-server", "tests", "suite", "v2"),
		filepath.Join("..", "codex-main", "codex-rs", "app-server", "tests", "suite", "v2"),
	)
	for _, candidate := range candidates {
		abs, err := filepath.Abs(candidate)
		if err != nil {
			continue
		}
		if _, err := os.Stat(filepath.Join(abs, "mod.rs")); err == nil {
			return abs
		}
	}
	t.Skip("Rust app-server v2 suite not found; set CODEX_RUST_APP_SERVER_SUITE_V2")
	return ""
}

func rustAppServerV2ModulesFromMod(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s) error = %v", path, err)
	}
	re := regexp.MustCompile(`(?m)^\s*mod\s+([a-z0-9_]+);`)
	matches := re.FindAllStringSubmatch(string(data), -1)
	modules := make([]string, 0, len(matches))
	for _, match := range matches {
		if len(match) == 2 && strings.TrimSpace(match[1]) != "" {
			modules = append(modules, match[1])
		}
	}
	sort.Strings(modules)
	return modules
}
