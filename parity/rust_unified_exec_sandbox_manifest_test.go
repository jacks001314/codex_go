package parity

import (
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"sort"
	"strings"
	"testing"
)

type rustUnifiedExecSandboxSuiteCase struct {
	Path      string
	Owner     string
	Focus     string
	Platform  string
	Tests     []string
	TestCases int
}

func TestRustUnifiedExecSandboxSuiteManifest(t *testing.T) {
	root := rustSnapshotRoot(t)
	for _, entry := range rustUnifiedExecSandboxManifest() {
		if entry.Path == "" || entry.Owner == "" || entry.Focus == "" || entry.Platform == "" || len(entry.Tests) == 0 {
			t.Fatalf("incomplete unified_exec/sandbox manifest entry: %#v", entry)
		}

		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(entry.Path)))
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", entry.Path, err)
		}

		got := rustAnnotatedTestFunctions(string(data))
		want := append([]string(nil), entry.Tests...)
		sort.Strings(want)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("Rust unified_exec/sandbox test drift for %s; missing=%v unexpected=%v gotCount=%d wantCount=%d", entry.Path, missingStrings(want, got), missingStrings(got, want), len(got), len(want))
		}

		if gotCases := strings.Count(string(data), "#[test_case("); gotCases != entry.TestCases {
			t.Fatalf("Rust unified_exec/sandbox test-case drift for %s: got %d want %d", entry.Path, gotCases, entry.TestCases)
		}
	}
}

func rustUnifiedExecSandboxManifest() []rustUnifiedExecSandboxSuiteCase {
	return []rustUnifiedExecSandboxSuiteCase{
		{
			Path:     "core/tests/suite/unified_exec.rs",
			Owner:    "turn, tool, appserver, execserver",
			Focus:    "unified exec lifecycle, output events, stdin, PTY, timeouts, truncation, sessions, and sandboxed execution",
			Platform: "all; individual Rust tests skip or gate host/target Windows, Unix, macOS, network, sandbox, and ARM cases",
			Tests: []string{
				"exec_command_clamps_model_requested_max_output_tokens_to_policy",
				"exec_command_reports_chunk_and_exit_metadata",
				"unified_exec_can_enable_tty",
				"unified_exec_defaults_to_pipe",
				"unified_exec_emits_end_event_when_session_dies_via_stdin",
				"unified_exec_emits_exec_command_begin_event",
				"unified_exec_emits_exec_command_end_event",
				"unified_exec_emits_one_begin_and_one_end_event",
				"unified_exec_emits_output_delta_for_exec_command",
				"unified_exec_emits_terminal_interaction_for_write_stdin",
				"unified_exec_enforces_glob_deny_read_policy",
				"unified_exec_formats_large_output_summary",
				"unified_exec_full_lifecycle_with_background_end_event",
				"unified_exec_intercepts_apply_patch_exec_command",
				"unified_exec_interrupt_preserves_long_running_session",
				"unified_exec_keeps_long_running_session_after_turn_end",
				"unified_exec_network_denial_emits_failed_background_end_event",
				"unified_exec_prunes_exited_sessions_first",
				"unified_exec_python_prompt_under_seatbelt",
				"unified_exec_resolves_relative_workdir",
				"unified_exec_respects_early_exit_notifications",
				"unified_exec_respects_workdir_override",
				"unified_exec_reuses_session_via_stdin",
				"unified_exec_runs_on_all_platforms",
				"unified_exec_runs_under_sandbox",
				"unified_exec_short_lived_network_denial_emits_failed_end_event",
				"unified_exec_streams_after_lagged_output",
				"unified_exec_terminal_interaction_captures_delayed_output",
				"unified_exec_timeout_and_followup_poll",
				"write_stdin_clamps_model_requested_max_output_tokens_to_policy",
				"write_stdin_calls_run_in_parallel_across_sessions",
				"write_stdin_ctrl_c_default_interrupt_reports_130_for_non_tty_session",
				"write_stdin_ctrl_c_interrupts_non_tty_session",
				"write_stdin_ctrl_c_reports_unsupported_interrupt_to_model_on_windows",
				"write_stdin_returns_exit_metadata_and_clears_session",
			},
		},
		{
			Path:      "core/tests/suite/unified_exec_process_events.rs",
			Owner:     "execserver, turn, tool",
			Focus:     "remote exec-server pushed process events, replay gaps, direct denials, and legacy exit metadata",
			Platform:  "all; async websocket exec-server fixture",
			TestCases: 4,
			Tests: []string{
				"exec_command_consumes_pushed_remote_process_events",
			},
		},
		{
			Path:     "core/tests/suite/unified_exec_zsh_fork_approvals.rs",
			Owner:    "shell, execpolicy, tool",
			Focus:    "zsh fork parent approval flow, denied reads, intercepted exec escalation, and explicit prompt rules",
			Platform: "Unix-style zsh fork flow; Rust helper skips unsupported targets",
			Tests: []string{
				"unified_exec_zsh_fork_parent_approval_escalates_intercepted_exec",
				"unified_exec_zsh_fork_parent_approval_keeps_explicit_prompt_rule",
				"unified_exec_zsh_fork_parent_approval_preserves_denied_reads",
			},
		},
		{
			Path:     "exec/tests/suite/sandbox.rs",
			Owner:    "exec, sandbox",
			Focus:    "platform sandbox spawning, policy cwd separation, home creation denial, Python runtime compatibility, and Unix sockets",
			Platform: "Unix only; Linux and macOS branches inside helper functions",
			Tests: []string{
				"allow_unix_socketpair_recvfrom",
				"python_getpwuid_works_under_sandbox",
				"python_multiprocessing_lock_works_under_sandbox",
				"sandbox_blocks_first_time_dot_codex_creation",
				"sandbox_distinguishes_command_and_policy_cwds",
			},
		},
		{
			Path:     "core/tests/suite/windows_sandbox.rs",
			Owner:    "sandbox, execpolicy",
			Focus:    "Windows restricted-token and elevated sandbox deny-read enforcement",
			Platform: "Windows only",
			Tests: []string{
				"windows_elevated_enforces_deny_read_and_protects_setup_marker",
				"windows_restricted_token_rejects_exact_and_glob_deny_read_policy",
			},
		},
		{
			Path:     "core/src/tools/sandboxing_tests.rs",
			Owner:    "sandbox, execpolicy, tool",
			Focus:    "sandbox approval payloads, granular approvals, guardian bypass, deny-read blocking, and exec-server env context",
			Platform: "unit tests; platform-specific behavior encoded in policy values",
			Tests: []string{
				"additional_permissions_allow_bypass_sandbox_first_attempt_when_execpolicy_skips",
				"bash_permission_request_payload_includes_description_when_present",
				"bash_permission_request_payload_omits_missing_description",
				"default_exec_approval_requirement_keeps_prompt_when_granular_allows_sandbox_approval",
				"default_exec_approval_requirement_rejects_sandbox_prompt_when_granular_disables_it",
				"deny_read_blocks_explicit_escalation_and_policy_bypass",
				"exec_server_env_keeps_command_native_and_carries_sandbox_context",
				"external_sandbox_skips_exec_approval_on_request",
				"guardian_bypasses_sandbox_for_explicit_escalation_on_first_attempt",
				"restricted_sandbox_requires_exec_approval_on_request",
			},
		},
		{
			Path:     "core/src/tools/handlers/unified_exec_tests.rs",
			Owner:    "tool, turn, shell",
			Focus:    "unified exec command selection, shell restrictions, remote direct mode, and pre/post tool-use payloads",
			Platform: "unit tests; includes bash, PowerShell, cmd, zsh fork, and remote environment modes",
			Tests: []string{
				"exec_command_post_tool_use_payload_skips_running_sessions",
				"exec_command_post_tool_use_payload_uses_output_for_interactive_completion",
				"exec_command_post_tool_use_payload_uses_output_for_noninteractive_one_shot_commands",
				"exec_command_pre_tool_use_payload_skips_write_stdin",
				"exec_command_pre_tool_use_payload_uses_raw_command",
				"shell_mode_for_environment_uses_direct_mode_for_remote_environments",
				"test_get_command_rejects_explicit_login_when_disallowed",
				"test_get_command_rejects_explicit_shell_in_zsh_fork_mode",
				"test_get_command_respects_explicit_bash_shell",
				"test_get_command_respects_explicit_cmd_shell",
				"test_get_command_respects_explicit_powershell_shell",
				"test_get_command_uses_default_shell_when_unspecified",
				"write_stdin_post_tool_use_payload_keeps_parallel_session_metadata_separate",
				"write_stdin_post_tool_use_payload_uses_original_exec_call_id_and_command_on_completion",
			},
		},
	}
}

func rustAnnotatedTestFunctions(source string) []string {
	fnLine := regexp.MustCompile(`^\s*(?:pub(?:\([^)]*\))?\s+)?(?:async\s+)?fn\s+([A-Za-z_][A-Za-z0-9_]*)\b`)
	pendingTest := false
	names := []string{}
	for _, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#[test]") || strings.HasPrefix(trimmed, "#[tokio::test") {
			pendingTest = true
			continue
		}
		if !pendingTest {
			continue
		}
		if match := fnLine.FindStringSubmatch(line); len(match) == 2 {
			names = append(names, match[1])
			pendingTest = false
			continue
		}
		if trimmed == "" || strings.HasPrefix(trimmed, "#[") || strings.HasPrefix(trimmed, "//") {
			continue
		}
		pendingTest = false
	}
	sort.Strings(names)
	return names
}
