package rollout

import (
	"os"
	"testing"
)

// Captured from the real codex-cli 0.166 binary with
// `migrate-rollouts --apply --json`: turn_context permission_profile,
// active_permission_profile and file_system_sandbox_policy are re-serialized
// field by field (tagged enums, kebab-case kinds, legacy "none" access alias,
// current_working_directory special-path alias, dropped unknown values) instead
// of being passed through (see .tmp-usealign/out/r80-perms.txt).

func TestCanonicalizeRolloutReserializesPermissionPoliciesLikeRust(t *testing.T) {
	cases := []struct {
		name           string
		source         string
		want           string
		bytesProcessed uint64
	}{
		{
			name: "permission_profile_managed_restricted",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"turn_context","payload":{"cwd":"C:\\work","model":"gpt-5","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"permission_profile":{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"path","path":"C:\\work"},"access":"read"}],"glob_scan_max_depth":3},"network":"restricted","extra":1}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"turn_context","payload":{"cwd":"C:\\work","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"permission_profile":{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"path","path":"C:\\work"},"access":"read"}],"glob_scan_max_depth":3},"network":"restricted"},"model":"gpt-5"}}
`,
			bytesProcessed: 2379,
		},
		{
			name: "permission_profile_special_entries",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"turn_context","payload":{"cwd":"C:\\work","model":"gpt-5","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"permission_profile":{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"special","value":{"kind":"project_roots","subpath":"src"}},"access":"write","missing_path_behavior":"skip"},{"path":{"type":"glob_pattern","pattern":"**/*.go"},"access":"none"}]},"network":"enabled"}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"turn_context","payload":{"cwd":"C:\\work","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"permission_profile":{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"special","value":{"kind":"project_roots","subpath":"src"}},"access":"write","missing_path_behavior":"skip"},{"path":{"type":"glob_pattern","pattern":"**/*.go"},"access":"deny"}]},"network":"enabled"},"model":"gpt-5"}}
`,
			bytesProcessed: 2693,
		},
		{
			name: "permission_profile_unrestricted",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"turn_context","payload":{"cwd":"C:\\work","model":"gpt-5","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"permission_profile":{"type":"managed","file_system":{"type":"unrestricted"},"network":"enabled"}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"turn_context","payload":{"cwd":"C:\\work","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"permission_profile":{"type":"managed","file_system":{"type":"unrestricted"},"network":"enabled"},"model":"gpt-5"}}
`,
			bytesProcessed: 2081,
		},
		{
			name: "permission_profile_disabled",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"turn_context","payload":{"cwd":"C:\\work","model":"gpt-5","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"permission_profile":{"type":"disabled"}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"turn_context","payload":{"cwd":"C:\\work","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"permission_profile":{"type":"disabled"},"model":"gpt-5"}}
`,
			bytesProcessed: 1910,
		},
		{
			name: "permission_profile_external",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"turn_context","payload":{"cwd":"C:\\work","model":"gpt-5","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"permission_profile":{"type":"external","network":"enabled"}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"turn_context","payload":{"cwd":"C:\\work","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"permission_profile":{"type":"external","network":"enabled"},"model":"gpt-5"}}
`,
			bytesProcessed: 1970,
		},
		{
			name: "permission_profile_unknown_type_dropped",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"turn_context","payload":{"cwd":"C:\\work","model":"gpt-5","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"permission_profile":{"type":"wat"}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
`,
			bytesProcessed: 1445,
		},
		{
			name: "permission_profile_unknown_access_dropped",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"turn_context","payload":{"cwd":"C:\\work","model":"gpt-5","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"permission_profile":{"type":"managed","file_system":{"type":"restricted","entries":[{"path":{"type":"path","path":"C:\\work"},"access":"execute"}]},"network":"restricted"}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
`,
			bytesProcessed: 1582,
		},
		{
			name: "filesystem_policy_restricted",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"turn_context","payload":{"cwd":"C:\\work","model":"gpt-5","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"file_system_sandbox_policy":{"kind":"restricted","entries":[{"path":{"type":"path","path":"C:/work"},"access":"write"}],"glob_scan_max_depth":2,"extra":1}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"turn_context","payload":{"cwd":"C:\\work","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"file_system_sandbox_policy":{"kind":"restricted","glob_scan_max_depth":2,"entries":[{"path":{"type":"path","path":"C:/work"},"access":"write"}]},"model":"gpt-5"}}
`,
			bytesProcessed: 2235,
		},
		{
			name: "filesystem_policy_unrestricted",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"turn_context","payload":{"cwd":"C:\\work","model":"gpt-5","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"file_system_sandbox_policy":{"kind":"unrestricted"}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"turn_context","payload":{"cwd":"C:\\work","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"file_system_sandbox_policy":{"kind":"unrestricted"},"model":"gpt-5"}}
`,
			bytesProcessed: 1946,
		},
		{
			name: "filesystem_policy_special_unknown",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"turn_context","payload":{"cwd":"C:\\work","model":"gpt-5","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"file_system_sandbox_policy":{"kind":"restricted","entries":[{"path":{"type":"special","value":{"kind":"unknown","path":"future_token","subpath":"x"}},"access":"deny"}]}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"turn_context","payload":{"cwd":"C:\\work","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"file_system_sandbox_policy":{"kind":"restricted","entries":[{"path":{"type":"special","value":{"kind":"unknown","path":"future_token","subpath":"x"}},"access":"deny"}]},"model":"gpt-5"}}
`,
			bytesProcessed: 2297,
		},
		{
			name: "filesystem_policy_cwd_alias",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"turn_context","payload":{"cwd":"C:\\work","model":"gpt-5","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"file_system_sandbox_policy":{"kind":"restricted","entries":[{"path":{"type":"special","value":{"kind":"current_working_directory"}},"access":"read"}]}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"turn_context","payload":{"cwd":"C:\\work","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"file_system_sandbox_policy":{"kind":"restricted","entries":[{"path":{"type":"special","value":{"kind":"project_roots"}},"access":"read"}]},"model":"gpt-5"}}
`,
			bytesProcessed: 2219,
		},
		{
			name: "filesystem_policy_unknown_kind_dropped",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"turn_context","payload":{"cwd":"C:\\work","model":"gpt-5","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"file_system_sandbox_policy":{"kind":"wat"}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
`,
			bytesProcessed: 1453,
		},
		{
			name: "active_permission_profile",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"turn_context","payload":{"cwd":"C:\\work","model":"gpt-5","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"active_permission_profile":{"id":":workspace","extends":":base","extra":1}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"turn_context","payload":{"cwd":"C:\\work","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"active_permission_profile":{"id":":workspace","extends":":base"},"model":"gpt-5"}}
`,
			bytesProcessed: 1995,
		},
		{
			name: "active_permission_profile_without_extends",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"turn_context","payload":{"cwd":"C:\\work","model":"gpt-5","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"active_permission_profile":{"id":":read-only"}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"turn_context","payload":{"cwd":"C:\\work","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"active_permission_profile":{"id":":read-only"},"model":"gpt-5"}}
`,
			bytesProcessed: 1931,
		},
		{
			name: "active_permission_profile_missing_id_dropped",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"turn_context","payload":{"cwd":"C:\\work","model":"gpt-5","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"active_permission_profile":{"extends":"x"}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
`,
			bytesProcessed: 1453,
		},
		{
			name: "permission_and_filesystem_policies",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"turn_context","payload":{"cwd":"C:\\work","model":"gpt-5","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"permission_profile":{"type":"managed","file_system":{"type":"unrestricted"},"network":"enabled"},"active_permission_profile":{"id":":workspace"},"file_system_sandbox_policy":{"kind":"unrestricted"}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"turn_context","payload":{"cwd":"C:\\work","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"permission_profile":{"type":"managed","file_system":{"type":"unrestricted"},"network":"enabled"},"active_permission_profile":{"id":":workspace"},"file_system_sandbox_policy":{"kind":"unrestricted"},"model":"gpt-5"}}
`,
			bytesProcessed: 2384,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			home, path := writeCanonicalMigrationRawRollout(t, tc.source)
			bytesProcessed, err := CanonicalizeRolloutWithStats(home, path)
			if err != nil {
				t.Fatalf("CanonicalizeRolloutWithStats: %v", err)
			}
			got, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile: %v", err)
			}
			if string(got) != tc.want {
				t.Fatalf("migrated rollout mismatch\n got: %s\nwant: %s", got, tc.want)
			}
			if bytesProcessed != tc.bytesProcessed {
				t.Fatalf("bytes_processed = %d, want %d", bytesProcessed, tc.bytesProcessed)
			}
		})
	}
}
