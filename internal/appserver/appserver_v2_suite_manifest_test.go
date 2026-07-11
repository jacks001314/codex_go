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
		{Module: "account", Owner: "internal/auth", Focus: "account read/login/logout/rate-limit RPCs"},
		{Module: "analytics", Owner: "internal/telemetry", Focus: "analytics enablement and payload capture"},
		{Module: "app_list", Owner: "internal/apps", Focus: "app list RPC and update notifications"},
		{Module: "attestation", Owner: "internal/appserver, internal/model", Focus: "attestation server request and Responses header"},
		{Module: "auto_env", Owner: "internal/appserver, internal/execserver", Focus: "auto environment selection"},
		{Module: "client_metadata", Owner: "internal/model", Focus: "Responses API client metadata propagation"},
		{Module: "collaboration_mode_list", Owner: "internal/appserver", Focus: "collaborationMode/list presets"},
		{Module: "command_exec", Owner: "internal/appserver", Focus: "command/exec RPC and output streaming"},
		{Module: "compaction", Owner: "internal/compact, internal/turn", Focus: "thread compaction flow"},
		{Module: "config_rpc", Owner: "internal/config, internal/appserver", Focus: "config read/write/requirements RPCs"},
		{Module: "connection_handling_websocket", Owner: "internal/appserver", Focus: "websocket connection lifecycle"},
		{Module: "connection_handling_websocket_unix", Owner: "internal/appserver", Focus: "unix websocket transport lifecycle"},
		{Module: "current_time", Owner: "internal/appserver", Focus: "currentTime/read server request"},
		{Module: "dynamic_tools", Owner: "internal/tool, internal/turn", Focus: "dynamic tool call server request"},
		{Module: "environment_add", Owner: "internal/appserver, internal/execserver", Focus: "environment/add RPC"},
		{Module: "environment_info", Owner: "internal/appserver, internal/execserver", Focus: "environment/info RPC"},
		{Module: "exec_server_test_support", Owner: "internal/execserver", Focus: "Rust exec-server suite helper parity"},
		{Module: "executor_mcp", Owner: "internal/mcp", Focus: "executor-scoped MCP registration and tools"},
		{Module: "executor_skills", Owner: "internal/prompt, internal/plugin", Focus: "executor-scoped skills"},
		{Module: "experimental_api", Owner: "internal/appserver", Focus: "experimental API gating"},
		{Module: "experimental_feature_list", Owner: "internal/features", Focus: "experimentalFeature/list RPC"},
		{Module: "external_agent_config", Owner: "internal/appserver", Focus: "external agent config detect/import"},
		{Module: "fs", Owner: "internal/appserver", Focus: "fs read/write/watch RPCs"},
		{Module: "hooks_list", Owner: "internal/appserver, internal/tool", Focus: "hooks/list RPC"},
		{Module: "imagegen_extension", Owner: "internal/tool, internal/model", Focus: "image generation extension"},
		{Module: "initialize", Owner: "internal/appserver", Focus: "initialize capabilities and protocol setup"},
		{Module: "marketplace_add", Owner: "internal/plugin", Focus: "marketplace/add RPC"},
		{Module: "marketplace_remove", Owner: "internal/plugin", Focus: "marketplace/remove RPC"},
		{Module: "marketplace_upgrade", Owner: "internal/plugin", Focus: "marketplace/upgrade RPC"},
		{Module: "mcp_resource", Owner: "internal/mcp", Focus: "mcpServer/resource/read RPC"},
		{Module: "mcp_server_elicitation", Owner: "internal/mcp, internal/appserver", Focus: "MCP elicitation server request"},
		{Module: "mcp_server_status", Owner: "internal/mcp", Focus: "MCP status list/update notifications"},
		{Module: "mcp_tool", Owner: "internal/mcp", Focus: "mcpServer/tool/call RPC"},
		{Module: "memory_reset", Owner: "internal/memories", Focus: "memory/reset RPC"},
		{Module: "model_list", Owner: "internal/model", Focus: "model/list RPC"},
		{Module: "model_provider_capabilities_read", Owner: "internal/model", Focus: "modelProvider/capabilities/read RPC"},
		{Module: "output_schema", Owner: "internal/model, internal/turn", Focus: "turn output schema propagation"},
		{Module: "permission_profile_list", Owner: "internal/sandbox, internal/appserver", Focus: "permissionProfile/list RPC"},
		{Module: "plan_item", Owner: "internal/turn, internal/appserver", Focus: "plan item notifications"},
		{Module: "plugin_install", Owner: "internal/plugin", Focus: "plugin/install RPC"},
		{Module: "plugin_list", Owner: "internal/plugin", Focus: "plugin/list and installed plugins"},
		{Module: "plugin_read", Owner: "internal/plugin", Focus: "plugin/read and plugin skill read"},
		{Module: "plugin_share", Owner: "internal/plugin", Focus: "plugin share save/list/update/delete"},
		{Module: "plugin_uninstall", Owner: "internal/plugin", Focus: "plugin/uninstall RPC"},
		{Module: "process_exec", Owner: "internal/appserver", Focus: "process/spawn stdio/pty lifecycle"},
		{Module: "rate_limit_reset_credits", Owner: "internal/auth, internal/appserver", Focus: "rate-limit reset credit RPCs"},
		{Module: "rate_limits", Owner: "internal/auth, internal/model", Focus: "account rate-limit notifications"},
		{Module: "realtime_conversation", Owner: "internal/realtime", Focus: "thread realtime RPCs and notifications"},
		{Module: "recommended_plugins", Owner: "internal/plugin", Focus: "recommended plugin metadata"},
		{Module: "remote_control", Owner: "internal/remotecontrol", Focus: "remote-control pairing and status RPCs"},
		{Module: "remote_thread_store", Owner: "internal/session, internal/appserver", Focus: "remote thread store debug fixture"},
		{Module: "request_permissions", Owner: "internal/appserver, internal/sandbox", Focus: "permissions approval server request"},
		{Module: "request_user_input", Owner: "internal/tool, internal/appserver", Focus: "request_user_input server request"},
		{Module: "request_validation", Owner: "internal/appserver", Focus: "JSON-RPC request validation"},
		{Module: "review", Owner: "internal/review, internal/appserver", Focus: "review/start RPC"},
		{Module: "safety_check_downgrade", Owner: "internal/safety, internal/appserver", Focus: "safety downgrade warnings"},
		{Module: "selected_capability_stack", Owner: "internal/appserver, internal/turn", Focus: "selected capability stack"},
		{Module: "skills_list", Owner: "internal/appserver, internal/prompt", Focus: "skills/list RPC"},
		{Module: "sleep", Owner: "internal/tool, internal/turn", Focus: "sleep tool item lifecycle"},
		{Module: "thread_archive", Owner: "internal/session, internal/appserver", Focus: "thread/archive RPC"},
		{Module: "thread_delete", Owner: "internal/session, internal/appserver", Focus: "thread/delete RPC"},
		{Module: "thread_fork", Owner: "internal/session, internal/appserver", Focus: "thread/fork RPC"},
		{Module: "thread_inject_items", Owner: "internal/session, internal/appserver", Focus: "thread/inject_items RPC"},
		{Module: "thread_list", Owner: "internal/session, internal/appserver", Focus: "thread/list RPC"},
		{Module: "thread_loaded_list", Owner: "internal/session, internal/appserver", Focus: "thread/loaded/list RPC"},
		{Module: "thread_memory_mode_set", Owner: "internal/memories, internal/appserver", Focus: "thread/memoryMode/set RPC"},
		{Module: "thread_metadata_update", Owner: "internal/session, internal/appserver", Focus: "thread/metadata/update RPC"},
		{Module: "thread_name_websocket", Owner: "internal/session, internal/appserver", Focus: "thread/name/set websocket updates"},
		{Module: "thread_read", Owner: "internal/session, internal/appserver", Focus: "thread/read RPC"},
		{Module: "thread_resume", Owner: "internal/session, internal/appserver", Focus: "thread/resume RPC"},
		{Module: "thread_rollback", Owner: "internal/session, internal/appserver", Focus: "thread/rollback RPC"},
		{Module: "thread_settings_update", Owner: "internal/session, internal/appserver", Focus: "thread/settings/update RPC"},
		{Module: "thread_shell_command", Owner: "internal/appserver", Focus: "thread/shellCommand RPC"},
		{Module: "thread_start", Owner: "internal/session, internal/appserver", Focus: "thread/start RPC"},
		{Module: "thread_status", Owner: "internal/session, internal/appserver", Focus: "thread status notifications"},
		{Module: "thread_unarchive", Owner: "internal/session, internal/appserver", Focus: "thread/unarchive RPC"},
		{Module: "thread_unsubscribe", Owner: "internal/session, internal/appserver", Focus: "thread/unsubscribe RPC"},
		{Module: "turn_interrupt", Owner: "internal/turn, internal/appserver", Focus: "turn/interrupt RPC"},
		{Module: "turn_start", Owner: "internal/turn, internal/appserver", Focus: "turn/start runtime and notifications"},
		{Module: "turn_start_zsh_fork", Owner: "internal/turn, internal/shell", Focus: "zsh fork shell tool behavior"},
		{Module: "turn_steer", Owner: "internal/turn, internal/appserver", Focus: "turn/steer RPC"},
		{Module: "web_search", Owner: "internal/tool, internal/codexapi", Focus: "web search tool round trip"},
		{Module: "windows_sandbox_setup", Owner: "internal/sandbox, internal/appserver", Focus: "windows sandbox setup RPC"},
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

func rustAppServerV2SuiteRoot(t *testing.T) string {
	t.Helper()
	candidates := []string{}
	if env := os.Getenv("CODEX_RUST_APP_SERVER_SUITE_V2"); env != "" {
		candidates = append(candidates, env)
	}
	candidates = append(candidates,
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
