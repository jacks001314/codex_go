package rollout

import (
	"os"
	"runtime"
	"testing"
)

// Captured from the real codex-cli 0.166 binary with
// `migrate-rollouts --apply --json`: legacy image_generation_end events become
// Extension(image_gen.generation) completed items, with the flattened
// GeneratedImageRef, always-present nullable trailers, Windows-cleaned
// AbsolutePathBuf values, and records the modern types cannot decode dropped
// (see .tmp-usealign/out/r79-imagegen.txt).

// imageGenerationSessionMetaMigrated is the first migrated line of every case:
// the legacy session_meta rewritten with the modern defaults.
const imageGenerationSessionMetaMigrated = `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}`

// imageGenerationOptionalFieldsMigrated is the Windows expectation captured from
// the Rust migration: the recorded C:/ paths are absolute there, so the item
// keeps its normalized savedPath and inputImages.
const imageGenerationOptionalFieldsMigrated = `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"rollout-1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"rollout-1","item":{"type":"Extension","kind":"image_gen.generation","id":"i5","status":"completed","revisedPrompt":null,"result":"r","transparentBackground":true,"failure":null,"savedPath":"C:\\tmp\\image.png","inputImages":[{"path":"C:\\tmp\\a.png","sha256":"abc"},{"path":"C:\\tmp\\b.png","sha256":"def"}],"generationId":"gen-1"},"completed_at_ms":1735787046000}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":3,"type":"event_msg","payload":{"type":"task_complete","turn_id":"rollout-1","last_agent_message":null,"completed_at":1735787046}}
`

// imageGenerationOptionalFieldsWant keeps that vector honest on POSIX hosts.
// The rollout records C:/ paths, which the host path cleaner (canonicalAbsolutePath,
// Rust AbsolutePathBuf decoding) rejects as relative, so the whole item is
// dropped and only the session_meta line is migrated.
func imageGenerationOptionalFieldsWant() string {
	if runtime.GOOS != "windows" {
		return imageGenerationSessionMetaMigrated + "\n"
	}
	return imageGenerationOptionalFieldsMigrated
}

// imageGenerationRelativePathWant keeps the dropped-path case honest per host:
// /tmp/root.png is relative on Windows, where the migration drops the record,
// and absolute on POSIX hosts, where it is kept.
func imageGenerationRelativePathWant() string {
	if runtime.GOOS == "windows" {
		return imageGenerationSessionMetaMigrated + "\n"
	}
	return imageGenerationSessionMetaMigrated + "\n" + `{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"rollout-1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"rollout-1","item":{"type":"Extension","kind":"image_gen.generation","id":"i11","status":"completed","revisedPrompt":null,"result":"r","transparentBackground":null,"failure":null,"savedPath":"/tmp/root.png","generationId":null},"completed_at_ms":1735787046000}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":3,"type":"event_msg","payload":{"type":"task_complete","turn_id":"rollout-1","last_agent_message":null,"completed_at":1735787046}}
`
}

// imageGenerationBytesProcessed reports the migration charge for a case. Only
// the two path cases differ across hosts: a dropped image record processes
// fewer bytes than one kept on a POSIX host, where the recorded path counts as
// absolute.
func imageGenerationBytesProcessed(name string, windowsValue uint64) uint64 {
	if runtime.GOOS == "windows" {
		return windowsValue
	}
	switch name {
	case "image_generation_optional_fields":
		return 1583
	case "image_generation_relative_path_dropped":
		return 3065
	}
	return windowsValue
}

func TestCanonicalizeRolloutConvertsImageGenerationLikeRust(t *testing.T) {
	cases := []struct {
		name           string
		source         string
		want           string
		bytesProcessed uint64
	}{
		{
			name: "image_generation_inline",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"image_generation_end","call_id":"i1","status":"completed","result":"cG5n","revised_prompt":"A blue square","turn_id":"t1"}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"rollout-1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"rollout-1","item":{"type":"Extension","kind":"image_gen.generation","id":"i1","status":"completed","revisedPrompt":"A blue square","result":"cG5n","transparentBackground":null,"failure":null,"generationId":null},"completed_at_ms":1735787046000}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":3,"type":"event_msg","payload":{"type":"task_complete","turn_id":"rollout-1","last_agent_message":null,"completed_at":1735787046}}
`,
			bytesProcessed: 3056,
		},
		{
			name: "image_generation_file_ref",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"image_generation_end","call_id":"i2","status":"completed","fileId":"f1"}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"rollout-1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"rollout-1","item":{"type":"Extension","kind":"image_gen.generation","id":"i2","status":"completed","revisedPrompt":null,"fileId":"f1","transparentBackground":null,"failure":null,"generationId":null},"completed_at_ms":1735787046000}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":3,"type":"event_msg","payload":{"type":"task_complete","turn_id":"rollout-1","last_agent_message":null,"completed_at":1735787046}}
`,
			bytesProcessed: 2980,
		},
		{
			name: "image_generation_failure",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"image_generation_end","call_id":"i3","status":"failed","result":"","failure":{"type":"usageLimitExceeded","limitId":"lim","resetsAt":1735787046}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"rollout-1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"rollout-1","item":{"type":"Extension","kind":"image_gen.generation","id":"i3","status":"failed","revisedPrompt":null,"result":"","transparentBackground":null,"failure":{"type":"usageLimitExceeded","limitId":"lim","resetsAt":1735787046},"generationId":null},"completed_at_ms":1735787046000}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":3,"type":"event_msg","payload":{"type":"task_complete","turn_id":"rollout-1","last_agent_message":null,"completed_at":1735787046}}
`,
			bytesProcessed: 3169,
		},
		{
			name: "image_generation_failure_without_resets",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"image_generation_end","call_id":"i4","status":"failed","result":"","failure":{"type":"usageLimitExceeded","limitId":"lim"}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"rollout-1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"rollout-1","item":{"type":"Extension","kind":"image_gen.generation","id":"i4","status":"failed","revisedPrompt":null,"result":"","transparentBackground":null,"failure":{"type":"usageLimitExceeded","limitId":"lim","resetsAt":null},"generationId":null},"completed_at_ms":1735787046000}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":3,"type":"event_msg","payload":{"type":"task_complete","turn_id":"rollout-1","last_agent_message":null,"completed_at":1735787046}}
`,
			bytesProcessed: 3135,
		},
		{
			name: "image_generation_optional_fields",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"image_generation_end","call_id":"i5","status":"completed","result":"r","saved_path":"C:/tmp/image.png","input_images":[{"path":"C:/tmp/./a.png","sha256":"abc"},{"path":"C:/tmp/b/../b.png","sha256":"def"}],"transparent_background":true,"generation_id":"gen-1","unknown":5}}
`,
			want:           imageGenerationOptionalFieldsWant(),
			bytesProcessed: imageGenerationBytesProcessed("image_generation_optional_fields", 3445),
		},
		{
			name: "image_generation_nulls",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"image_generation_end","call_id":"i6","status":"completed","result":"r","revised_prompt":null,"failure":null,"transparent_background":null,"generation_id":null,"input_images":[]}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"rollout-1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"rollout-1","item":{"type":"Extension","kind":"image_gen.generation","id":"i6","status":"completed","revisedPrompt":null,"result":"r","transparentBackground":null,"failure":null,"generationId":null},"completed_at_ms":1735787046000}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":3,"type":"event_msg","payload":{"type":"task_complete","turn_id":"rollout-1","last_agent_message":null,"completed_at":1735787046}}
`,
			bytesProcessed: 3083,
		},
		{
			name: "image_generation_empty_input_images",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"image_generation_end","call_id":"i7","status":"completed","result":"r","input_images":[]}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":1,"type":"event_msg","payload":{"type":"task_started","turn_id":"rollout-1","started_at":1735787046,"model_context_window":null,"collaboration_mode_kind":"default"}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":2,"type":"event_msg","payload":{"type":"item_completed","thread_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","turn_id":"rollout-1","item":{"type":"Extension","kind":"image_gen.generation","id":"i7","status":"completed","revisedPrompt":null,"result":"r","transparentBackground":null,"failure":null,"generationId":null},"completed_at_ms":1735787046000}}
{"timestamp":"2025-01-02T03:04:06.000Z","ordinal":3,"type":"event_msg","payload":{"type":"task_complete","turn_id":"rollout-1","last_agent_message":null,"completed_at":1735787046}}
`,
			bytesProcessed: 2995,
		},
		{
			name: "image_generation_missing_status_dropped",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"image_generation_end","call_id":"i8","result":"r"}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
`,
			bytesProcessed: 1362,
		},
		{
			name: "image_generation_missing_image_dropped",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"image_generation_end","call_id":"i9","status":"completed"}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
`,
			bytesProcessed: 1370,
		},
		{
			name: "image_generation_unknown_failure_dropped",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"image_generation_end","call_id":"i10","status":"failed","result":"","failure":{"type":"wat"}}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
`,
			bytesProcessed: 1405,
		},
		{
			name: "image_generation_relative_path_dropped",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"image_generation_end","call_id":"i11","status":"completed","result":"r","saved_path":"/tmp/root.png"}}
`,
			want:           imageGenerationRelativePathWant(),
			bytesProcessed: imageGenerationBytesProcessed("image_generation_relative_path_dropped", 1413),
		},
		{
			name: "image_generation_input_image_missing_sha_dropped",
			source: `{"timestamp":"2025-01-02T03:04:05.000Z","type":"session_meta","payload":{"id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli"}}
{"timestamp":"2025-01-02T03:04:06.000Z","type":"event_msg","payload":{"type":"image_generation_end","call_id":"i12","status":"completed","result":"r","input_images":[{"path":"C:/tmp/a.png"}]}}
`,
			want: `{"timestamp":"2025-01-02T03:04:05.000Z","ordinal":0,"type":"session_meta","payload":{"session_id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","id":"0199a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b","timestamp":"2025-01-02T03:04:05.000Z","cwd":"C:/work","originator":"codex_cli_rs","cli_version":"0.9.0","source":"cli","model_provider":null,"base_instructions":null,"history_mode":"paginated"}}
`,
			bytesProcessed: 1425,
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
