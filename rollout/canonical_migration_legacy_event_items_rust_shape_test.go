package rollout

import (
	"os"
	"testing"
)

// Captured from the real codex-cli 0.166 binary with
// `migrate-rollouts --apply --json`: legacy MCP, web search, review mode,
// sub-agent and dynamic tool completion events become completed turn items,
// with serde's camelCase item fields, strict duration/enum decoding, and
// records the modern types cannot decode dropped (see
// .tmp-usealign/out/r78-convert.txt).

func TestCanonicalizeRolloutConvertsLegacyEventItemsLikeRust(t *testing.T) {
	cases := []struct {
		name           string
		source         string
		want           string
		bytesProcessed uint64
	}{
		{
			name: "mcp_tool_call_ok",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"mcp_tool_call_end","call_id":"m1","turn_id":"t1","invocation":{"server":"srv","tool":"t","arguments":{"z":1,"a":[1,2]}},"connector_id":"conn","read_only_hint":false,"duration":{"secs":1,"nanos":0},"result":{"Ok":{"content":[{"type":"text","text":"hi"}],"isError":false}}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"t1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"McpToolCall","id":"m1","server":"srv","tool":"t","arguments":{"z":1,"a":[1,2]},"connectorId":"conn","readOnlyHint":false,"status":"completed","result":{"content":[{"type":"text","text":"hi"}],"isError":false},"duration":{"secs":1,"nanos":0}},"completed_at_ms":1735787046000}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":3,"type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","last_agent_message":null,"completed_at":1735787046}}
`,
			bytesProcessed: 3281,
		},
		{
			name: "mcp_tool_call_error_result",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"mcp_tool_call_end","call_id":"m2","turn_id":"t1","invocation":{"server":"srv","tool":"t"},"duration":{"secs":1,"nanos":0},"result":{"Err":"boom"}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"t1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"McpToolCall","id":"m2","server":"srv","tool":"t","arguments":null,"status":"failed","error":{"message":"boom"},"duration":{"secs":1,"nanos":0}},"completed_at_ms":1735787046000}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":3,"type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","last_agent_message":null,"completed_at":1735787046}}
`,
			bytesProcessed: 2960,
		},
		{
			name: "mcp_tool_call_is_error",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"mcp_tool_call_end","call_id":"m3","turn_id":"t1","invocation":{"server":"srv","tool":"t"},"duration":{"secs":1,"nanos":0},"result":{"Ok":{"content":[],"isError":true}}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"t1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"McpToolCall","id":"m3","server":"srv","tool":"t","arguments":null,"status":"failed","result":{"content":[],"isError":true},"duration":{"secs":1,"nanos":0}},"completed_at_ms":1735787046000}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":3,"type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","last_agent_message":null,"completed_at":1735787046}}
`,
			bytesProcessed: 3006,
		},
		{
			name: "mcp_tool_call_implicit_turn",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"mcp_tool_call_end","call_id":"m4","invocation":{"server":"srv","tool":"t"},"duration":{"secs":1,"nanos":0},"result":{"Ok":{"content":[],"isError":false}}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"rollout-1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"rollout-1","item":{"type":"McpToolCall","id":"m4","server":"srv","tool":"t","arguments":null,"status":"completed","result":{"content":[],"isError":false},"duration":{"secs":1,"nanos":0}},"completed_at_ms":1735787046000}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":3,"type":"event_msg","payload":{"type":"task_complete","turn_id":"rollout-1","last_agent_message":null,"completed_at":1735787046}}
`,
			bytesProcessed: 3042,
		},
		{
			name: "mcp_tool_call_app_ui",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"mcp_tool_call_end","call_id":"m5","turn_id":"t1","invocation":{"server":"srv","tool":"t"},"mcp_app_ui":{"resourceUri":"ui://x","preferredModelDisplayMode":"inline"},"duration":{"secs":1,"nanos":0},"result":{"Ok":{"content":[],"isError":false}}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"t1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"McpToolCall","id":"m5","server":"srv","tool":"t","arguments":null,"mcpAppUi":{"resourceUri":"ui://x","preferredModelDisplayMode":"inline"},"status":"completed","result":{"content":[],"isError":false},"duration":{"secs":1,"nanos":0}},"completed_at_ms":1735787046000}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":3,"type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","last_agent_message":null,"completed_at":1735787046}}
`,
			bytesProcessed: 3236,
		},
		{
			name: "mcp_tool_call_result_extras",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"mcp_tool_call_end","call_id":"m6","turn_id":"t1","invocation":{"server":"srv","tool":"t"},"duration":{"secs":1,"nanos":0},"result":{"Ok":{"content":[],"structuredContent":{"z":1},"_meta":{"q":1},"unknown":5}}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"t1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"McpToolCall","id":"m6","server":"srv","tool":"t","arguments":null,"status":"completed","result":{"content":[],"structuredContent":{"z":1},"_meta":{"q":1}},"duration":{"secs":1,"nanos":0}},"completed_at_ms":1735787046000}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":3,"type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","last_agent_message":null,"completed_at":1735787046}}
`,
			bytesProcessed: 3111,
		},
		{
			name: "mcp_tool_call_duration_extra_dropped",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"mcp_tool_call_end","call_id":"m7","turn_id":"t1","invocation":{"server":"srv","tool":"t"},"duration":{"secs":1,"nanos":0,"extra":9},"result":{"Ok":{"content":[],"isError":false}}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
`,
			bytesProcessed: 1495,
		},
		{
			name: "mcp_tool_call_bad_result_dropped",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"mcp_tool_call_end","call_id":"m8","turn_id":"t1","invocation":{"server":"srv","tool":"t"},"duration":{"secs":1,"nanos":0},"result":{"Other":1}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
`,
			bytesProcessed: 1459,
		},
		{
			name: "web_search_end",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"web_search_end","call_id":"w1","query":"q","action":{"type":"search","query":"q","extra":1},"results":[{"z":1,"a":2}]}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"rollout-1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"rollout-1","item":{"type":"WebSearch","id":"w1","query":"q","action":{"type":"search","query":"q"},"results":[{"z":1,"a":2}]},"completed_at_ms":1735787046000}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":3,"type":"event_msg","payload":{"type":"task_complete","turn_id":"rollout-1","last_agent_message":null,"completed_at":1735787046}}
`,
			bytesProcessed: 2884,
		},
		{
			name: "web_search_end_unknown_action",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"web_search_end","call_id":"w2","query":"q2","action":{"type":"wat","x":1}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"rollout-1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"rollout-1","item":{"type":"WebSearch","id":"w2","query":"q2","action":{"type":"other"}},"completed_at_ms":1735787046000}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":3,"type":"event_msg","payload":{"type":"task_complete","turn_id":"rollout-1","last_agent_message":null,"completed_at":1735787046}}
`,
			bytesProcessed: 2764,
		},
		{
			name: "web_search_end_missing_action_dropped",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"web_search_end","call_id":"w3","query":"q3"}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
`,
			bytesProcessed: 1360,
		},
		{
			name: "context_compacted",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"context_compacted"}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"rollout-1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"rollout-1","item":{"type":"ContextCompaction","id":"item-1"},"completed_at_ms":1735787046000}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":3,"type":"event_msg","payload":{"type":"task_complete","turn_id":"rollout-1","last_agent_message":null,"completed_at":1735787046}}
`,
			bytesProcessed: 2655,
		},
		{
			name: "entered_review_mode_target",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"entered_review_mode","target":{"type":"custom","instructions":"look","extra":1},"user_facing_hint":"Hi","turn_id":"t1","item_id":"rv1"}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"t1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"EnteredReviewMode","id":"rv1","target":{"type":"custom","instructions":"look"},"user_facing_hint":"Hi"},"completed_at_ms":1735787046000}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":3,"type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","last_agent_message":null,"completed_at":1735787046}}
`,
			bytesProcessed: 2869,
		},
		{
			name: "entered_review_mode_legacy_prompt",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"entered_review_mode","prompt":"look","turn_id":"t1"}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"t1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"EnteredReviewMode","id":"item-1","target":{"type":"custom","instructions":"look"},"user_facing_hint":"Review requested."},"completed_at_ms":1735787046000}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":3,"type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","last_agent_message":null,"completed_at":1735787046}}
`,
			bytesProcessed: 2822,
		},
		{
			name: "entered_review_mode_bad_target_dropped",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"entered_review_mode","target":{"type":"wat"},"turn_id":"t1"}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
`,
			bytesProcessed: 1376,
		},
		{
			name: "exited_review_mode_output",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"exited_review_mode","turn_id":"t1","item_id":"rv2","review_output":{"findings":[{"title":"t","body":"b","confidence_score":0.5,"priority":2,"code_location":{"absolute_file_path":"C:\\work\\a.go","line_range":{"start":1,"end":2}},"extra":7}],"overall_correctness":"patch is correct","overall_explanation":"fine","overall_confidence_score":0.1234567891234,"extra_top":{"k":1}}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"t1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"ExitedReviewMode","id":"rv2","review_output":{"findings":[{"title":"t","body":"b","confidence_score":0.5,"priority":2,"code_location":{"absolute_file_path":"C:\\work\\a.go","line_range":{"start":1,"end":2}}}],"overall_correctness":"patch is correct","overall_explanation":"fine","overall_confidence_score":0.12345679}},"completed_at_ms":1735787046000}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":3,"type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","last_agent_message":null,"completed_at":1735787046}}
`,
			bytesProcessed: 3539,
		},
		{
			name: "exited_review_mode_null_output",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"exited_review_mode","turn_id":"t1"}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"t1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"ExitedReviewMode","id":"item-1","review_output":null},"completed_at_ms":1735787046000}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":3,"type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","last_agent_message":null,"completed_at":1735787046}}
`,
			bytesProcessed: 2669,
		},
		{
			name: "exited_review_mode_incomplete_dropped",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"exited_review_mode","turn_id":"t1","review_output":{"findings":[],"summary":"ok"}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
`,
			bytesProcessed: 1398,
		},
		{
			name: "sub_agent_activity",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"sub_agent_activity","event_id":"e1","kind":"started","agent_thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4b6c","agent_path":"/root/a","model":"gpt-5","reasoning_effort":"high"}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"rollout-1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"rollout-1","item":{"type":"SubAgentActivity","model":"gpt-5","reasoning_effort":"high","id":"e1","kind":"started","agent_thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4b6c","agent_path":"/root/a"},"completed_at_ms":1735787046000}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":3,"type":"event_msg","payload":{"type":"task_complete","turn_id":"rollout-1","last_agent_message":null,"completed_at":1735787046}}
`,
			bytesProcessed: 3079,
		},
		{
			name: "sub_agent_activity_nulls",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"sub_agent_activity","event_id":"e2","kind":"completed","agent_thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4b6c","agent_path":"/root","model":null,"reasoning_effort":"custom-effort"}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"rollout-1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"rollout-1","item":{"type":"SubAgentActivity","model":null,"reasoning_effort":"custom-effort","id":"e2","kind":"completed","agent_thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4b6c","agent_path":"/root"},"completed_at_ms":1735787046000}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":3,"type":"event_msg","payload":{"type":"task_complete","turn_id":"rollout-1","last_agent_message":null,"completed_at":1735787046}}
`,
			bytesProcessed: 3097,
		},
		{
			name: "sub_agent_activity_bad_kind_dropped",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"sub_agent_activity","event_id":"e3","kind":"finished","agent_thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4b6c","agent_path":"/root/a","model":null,"reasoning_effort":null}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
`,
			bytesProcessed: 1487,
		},
		{
			name: "sub_agent_activity_bad_path_dropped",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"sub_agent_activity","event_id":"e4","kind":"started","agent_thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4b6c","agent_path":"C:\\work\\a","model":null,"reasoning_effort":null}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
`,
			bytesProcessed: 1490,
		},
		{
			name: "dynamic_tool_call",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"dynamic_tool_call_response","call_id":"d1","turn_id":"t1","namespace":"ns","tool":"t","arguments":{"z":1,"a":2},"success":false,"content_items":[{"type":"inputText","text":"hi","extra":1},{"type":"inputImage","imageUrl":"u"}],"error":"bad","duration":{"secs":0,"nanos":5}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"t1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"DynamicToolCall","id":"d1","namespace":"ns","tool":"t","arguments":{"z":1,"a":2},"status":"failed","content_items":[{"type":"inputText","text":"hi"},{"type":"inputImage","imageUrl":"u"}],"success":false,"error":"bad","duration":{"secs":0,"nanos":5}},"completed_at_ms":1735787046000}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":3,"type":"event_msg","payload":{"type":"task_complete","turn_id":"t1","last_agent_message":null,"completed_at":1735787046}}
`,
			bytesProcessed: 3298,
		},
		{
			name: "dynamic_tool_call_bad_item_dropped",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"dynamic_tool_call_response","call_id":"d2","turn_id":"t1","tool":"t","arguments":{},"success":true,"content_items":[{"type":"input_text","text":"hi"}],"duration":{"secs":0,"nanos":5}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:\\work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
`,
			bytesProcessed: 1499,
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
