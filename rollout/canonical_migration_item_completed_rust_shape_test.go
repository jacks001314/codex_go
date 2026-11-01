package rollout

import (
	"os"
	"testing"
)

// Captured from the real codex-cli 0.166 binary with
// `migrate-rollouts --apply --json`: item_completed records are decoded into the
// modern ItemCompletedEvent/TurnItem types and written back in declaration
// order. Undecodable items (an old AgentMessageContent tag, a shell cwd that is
// not a file: URI) drop the whole line, mismatched thread ids are rewritten to
// the rollout thread, and a missing completed_at_ms defaults to 0 (see
// .tmp-usealign/out/r85-itemcompleted.txt).

func TestCanonicalizeRolloutReplaysItemCompletedLikeRust(t *testing.T) {
	cases := []struct {
		name           string
		source         string
		want           string
		bytesProcessed uint64
	}{
		{
			name: "user_text_elements",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"UserMessage","id":"u1","content":[{"type":"text","text":"ab","text_elements":[{"byte_range":{"start":0,"end":1}}]}]},"completed_at_ms":1735787046000}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"UserMessage","id":"u1","content":[{"type":"text","text":"ab","text_elements":[{"byte_range":{"start":0,"end":1},"placeholder":null}]}]},"completed_at_ms":1735787046000}}
`,
			bytesProcessed: 2275,
		},
		{
			name: "user_client_snake",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"UserMessage","id":"u1","client_id":"c1","content":[{"type":"text","text":"hey"}]},"completed_at_ms":1735787046000}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"UserMessage","id":"u1","client_id":"c1","content":[{"type":"text","text":"hey","text_elements":[]}]},"completed_at_ms":1735787046000}}
`,
			bytesProcessed: 2170,
		},
		{
			name: "user_client_camel",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"UserMessage","id":"u1","clientId":"c1","content":[{"type":"text","text":"hey"}]},"completed_at_ms":1735787046000}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"UserMessage","id":"u1","content":[{"type":"text","text":"hey","text_elements":[]}]},"completed_at_ms":1735787046000}}
`,
			bytesProcessed: 2135,
		},
		{
			name: "agent_allopts",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"AgentMessage","id":"a1","content":[{"type":"Text","text":"hi"}],"phase":"final_answer","delivery":"async","questions":[{"title":"Pick","options":null}],"memory_citation":null},"completed_at_ms":1735787046000}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"AgentMessage","id":"a1","content":[{"type":"Text","text":"hi"}],"phase":"final_answer","delivery":"async","questions":[{"title":"Pick","options":null}]},"completed_at_ms":1735787046000}}
`,
			bytesProcessed: 2368,
		},
		{
			name: "agent_text_lower",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"AgentMessage","id":"a1","content":[{"type":"text","text":"hi"}]},"completed_at_ms":1735787046000}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
`,
			bytesProcessed: 1507,
		},
		{
			name: "cmd_exec_full",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"CommandExecution","id":"c1","command":["echo","x"],"cwd":"file:///C:/work","parsed_cmd":[{"type":"unknown","cmd":"echo x"}],"source":"agent","status":"completed","plugin_id":"p@cur","script_path":"s.py","process_id":"pid","interaction_input":"in","aggregated_output":"x\n","exit_code":0,"duration":{"secs":2,"nanos":0},"model_context":{"model_slug":"m","reasoning_effort":"high"},"sandbox_type":"seatbelt","unknown":1},"completed_at_ms":1735787046000}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"CommandExecution","id":"c1","plugin_id":"p@cur","script_path":"s.py","process_id":"pid","command":["echo","x"],"cwd":"file:///C:/work","parsed_cmd":[{"type":"unknown","cmd":"echo x"}],"source":"agent","interaction_input":"in","status":"completed","aggregated_output":"x\n","exit_code":0,"duration":{"secs":2,"nanos":0}},"completed_at_ms":1735787046000}}
`,
			bytesProcessed: 2945,
		},
		{
			name: "cmd_exec_bad_cwd",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"CommandExecution","id":"c1","command":["echo","x"],"cwd":"C:/work","parsed_cmd":[{"type":"unknown","cmd":"echo x"}],"source":"agent","status":"completed"},"completed_at_ms":1735787046000}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
`,
			bytesProcessed: 1597,
		},
		{
			name: "websearch_other_action",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"WebSearch","id":"w1","query":"q","action":{"type":"navigate","url":"u"}},"completed_at_ms":1735787046000}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"WebSearch","id":"w1","query":"q","action":{"type":"other"}},"completed_at_ms":1735787046000}}
`,
			bytesProcessed: 2079,
		},
		{
			name: "ext_websearch",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"Extension","kind":"web.search","id":"e2","query":"q","action":{"type":"openPage","url":"u"},"results":null},"completed_at_ms":1735787046000}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"Extension","kind":"web.search","id":"e2","query":"q","action":{"type":"openPage","url":"u"},"results":null},"completed_at_ms":1735787046000}}
`,
			bytesProcessed: 2210,
		},
		{
			name: "mcp_full",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"McpToolCall","id":"m1","server":"srv","tool":"tl","arguments":{"a":1},"connectorId":"conn","mcpAppUi":{"resourceUri":"ui://y","preferredModelDisplayMode":"inline"},"readOnlyHint":true,"status":"completed","result":{"content":[],"isError":false},"duration":{"secs":1,"nanos":500}},"completed_at_ms":1735787046000}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"McpToolCall","id":"m1","server":"srv","tool":"tl","arguments":{"a":1},"connectorId":"conn","mcpAppUi":{"resourceUri":"ui://y","preferredModelDisplayMode":"inline"},"readOnlyHint":true,"status":"completed","result":{"content":[],"isError":false},"duration":{"secs":1,"nanos":500}},"completed_at_ms":1735787046000}}
`,
			bytesProcessed: 2726,
		},
		{
			name: "collab_completed_state",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"CollabAgentToolCall","id":"ca1","tool":"close_agent","status":"failed","sender_thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","receiver_thread_ids":["0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4d7e"],"agents_states":{"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4d7e":{"completed":null}}},"completed_at_ms":1735787046000}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"CollabAgentToolCall","id":"ca1","tool":"close_agent","status":"failed","sender_thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","receiver_thread_ids":["0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4d7e"],"receiver_agents":[],"agents_states":{"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4d7e":{"completed":null}}},"completed_at_ms":1735787046000}}
`,
			bytesProcessed: 2735,
		},
		{
			name: "file_change",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"FileChange","id":"fc1","changes":{"new.txt":{"type":"add","content":"hello"}},"status":"completed"},"completed_at_ms":1735787046000}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"FileChange","id":"fc1","changes":{"new.txt":{"type":"add","content":"hello"}},"status":"completed"},"completed_at_ms":1735787046000}}
`,
			bytesProcessed: 2186,
		},
		{
			name: "thread_id_mismatch",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4b6c","turn_id":"t1","item":{"type":"AgentMessage","id":"a1","content":[{"type":"Text","text":"hi"}]},"completed_at_ms":1735787046000}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"AgentMessage","id":"a1","content":[{"type":"Text","text":"hi"}]},"completed_at_ms":1735787046000}}
`,
			bytesProcessed: 2081,
		},
		{
			name: "no_completed_at",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"AgentMessage","id":"a1","content":[{"type":"Text","text":"hi"}]}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"AgentMessage","id":"a1","content":[{"type":"Text","text":"hi"}]},"completed_at_ms":0}}
`,
			bytesProcessed: 2025,
		},
		{
			name: "with_started_at",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"AgentMessage","id":"a1","content":[{"type":"Text","text":"hi"}]},"completed_at_ms":1735787046000,"started_at_ms":1735787045000}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"t1","item":{"type":"AgentMessage","id":"a1","content":[{"type":"Text","text":"hi"}]},"started_at_ms":1735787045000,"completed_at_ms":1735787046000}}
`,
			bytesProcessed: 2171,
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
