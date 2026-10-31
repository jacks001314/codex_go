package rollout

import (
	"os"
	"testing"
)

// Captured from the real codex-cli 0.166 binary with
// `migrate-rollouts --apply --json`: turn_context records are re-serialized in
// TurnContextItem field order with legacy sandbox/approval spellings
// normalized, compacted records always carry the two nullable trailer fields,
// and records the modern types cannot decode are dropped (see
// .tmp-usealign/out/r77-turn-context-compacted.txt).

func TestCanonicalizeRolloutReserializesTurnContextAndCompactedLikeRust(t *testing.T) {
	cases := []struct {
		name           string
		source         string
		want           string
		bytesProcessed uint64
	}{
		{
			name: "turn_context_legacy_workspace_write",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"turn_context","payload":{"cwd":"C:\\work","model":"gpt-5","approval_policy":"on-request","sandbox_policy":{"mode":"workspace-write"}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"turn_context","payload":{"cwd":"C:\\work","approval_policy":"on-request","sandbox_policy":{"type":"workspace-write","network_access":false,"exclude_tmpdir_env_var":false,"exclude_slash_tmp":false},"model":"gpt-5"}}
`,
			bytesProcessed: 1969,
		},
		{
			name: "turn_context_read_only_network",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"turn_context","payload":{"cwd":"C:\\work","model":"gpt-5","approval_policy":"on-request","sandbox_policy":{"type":"read-only","network_access":true}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"turn_context","payload":{"cwd":"C:\\work","approval_policy":"on-request","sandbox_policy":{"type":"read-only","network_access":true},"model":"gpt-5"}}
`,
			bytesProcessed: 1857,
		},
		{
			name: "turn_context_workspace_write_roots",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"turn_context","payload":{"cwd":"C:\\work","model":"gpt-5","approval_policy":"on-request","sandbox_policy":{"mode":"workspace-write","writable_roots":["C:\\other"],"network_access":true,"exclude_slash_tmp":true,"extra":1}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"turn_context","payload":{"cwd":"C:\\work","approval_policy":"on-request","sandbox_policy":{"type":"workspace-write","writable_roots":["C:\\other"],"network_access":true,"exclude_tmpdir_env_var":false,"exclude_slash_tmp":true},"model":"gpt-5"}}
`,
			bytesProcessed: 2115,
		},
		{
			name: "turn_context_optional_fields",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"turn_context","payload":{"turn_id":"t1","root_turn_id":"root","cwd":"C:\\work","model":"gpt-5","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"comp_hash":"abc","effort":"high","summary":"auto","unknown_field":5}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"turn_context","payload":{"turn_id":"t1","root_turn_id":"root","cwd":"C:\\work","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"model":"gpt-5","comp_hash":"abc","effort":"high","summary":"auto"}}
`,
			bytesProcessed: 2073,
		},
		{
			name: "turn_context_collaboration_mode",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"turn_context","payload":{"cwd":"C:\\work","model":"gpt-5","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"collaboration_mode":{"mode":"plan","settings":{"model":"gpt-5","reasoning_effort":"high"}}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"turn_context","payload":{"cwd":"C:\\work","approval_policy":"on-request","sandbox_policy":{"type":"read-only"},"model":"gpt-5","collaboration_mode":{"mode":"plan","settings":{"model":"gpt-5","reasoning_effort":"high","developer_instructions":null}}}}
`,
			bytesProcessed: 2127,
		},
		{
			name: "turn_context_dropped_unknown_sandbox",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"turn_context","payload":{"cwd":"C:\\work","model":"gpt-5","approval_policy":"on-request","sandbox_policy":{"type":"wat"}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
`,
			bytesProcessed: 1407,
		},
		{
			name: "turn_context_dropped_unknown_approval",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"turn_context","payload":{"cwd":"C:\\work","model":"gpt-5","approval_policy":"always","sandbox_policy":{"type":"read-only"}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
`,
			bytesProcessed: 1409,
		},
		{
			name: "compacted_with_identifiers",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"compacted","payload":{"message":"s2","compaction_response_id":"resp-1","window_number":3,"first_window_id":"w1","previous_window_id":"w2","window_id":"w3"}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"compacted","payload":{"message":"s2","window_number":3,"first_window_id":"w1","previous_window_id":"w2","window_id":"w3","compaction_response_id":"resp-1","latest_token_usage_record":null}}
`,
			bytesProcessed: 1941,
		},
		{
			name: "compacted_minimal",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"compacted","payload":{"message":"summary","replacement_history":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}]}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"compacted","payload":{"message":"summary","replacement_history":[{"type":"message","role":"user","content":[{"type":"input_text","text":"hi"}]}],"compaction_response_id":null,"latest_token_usage_record":null}}
`,
			bytesProcessed: 1971,
		},
		{
			name: "compacted_dropped_missing_message",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"compacted","payload":{"replacement_history":[]}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
`,
			bytesProcessed: 1333,
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
