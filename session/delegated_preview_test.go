package session

import (
	"fmt"
	"testing"
	"time"
)

// Rust #50462 (codex-rs/state/src/delegated_preview_tests.rs:
// delegated_previews_unwrap_only_recognized_inputs).
func TestDelegatedPreviewsUnwrapOnlyRecognizedInputsLikeRust(t *testing.T) {
	const wrapped = "<codex_delegation>\n  <source_thread_id>source</source_thread_id>\n  <input>Check &lt;main&gt; &amp; &amp;lt;literal&amp;gt;</input>\n</codex_delegation>"
	cases := []struct {
		name      string
		namespace string
		tool      string
		text      string
		expected  string
		ok        bool
	}{
		{"app create_thread wrapped", "codex_app", "create_thread", wrapped, "Check <main> & &lt;literal&gt;", true},
		{"tui send_message_to_thread wrapped", "codex_tui", "send_message_to_thread", wrapped, "Check <main> & &lt;literal&gt;", true},
		{"plain text is returned unchanged", "codex_app", "create_thread", "plain &lt;text&gt;", "plain &lt;text&gt;", true},
		{"blank text is ignored", "codex_tui", "create_thread", "  ", "", false},
		{"other namespace is ignored", "other", "create_thread", wrapped, "", false},
		{"missing namespace is ignored", "", "create_thread", wrapped, "", false},
		{"other tool is ignored", "codex_app", "shell", wrapped, "", false},
		{"unrecognized wrapper falls back to raw text", "codex_app", "create_thread", "<codex_delegation>incomplete", "<codex_delegation>incomplete", true},
	}
	for _, tc := range cases {
		preview, ok := DelegatedOutputPreview(tc.namespace, tc.tool, tc.text)
		if preview != tc.expected || ok != tc.ok {
			t.Fatalf("%s: DelegatedOutputPreview(%q, %q, %q) = (%q, %v), want (%q, %v)",
				tc.name, tc.namespace, tc.tool, tc.text, preview, ok, tc.expected, tc.ok)
		}
	}
}

// Rust #50462 (codex-rs/thread-store/src/thread_metadata_sync_preview_tests.rs:
// delegated_output_emits_only_first_preview_in_live_patch). The first delegated
// output fills the preview and later ones never move it.
func TestDelegatedOutputEmitsOnlyFirstPreviewInLivePatchLikeRust(t *testing.T) {
	store := NewStore(t.TempDir())
	now := fixedTime()
	if err := store.Save(&Record{ID: "thread-1", CreatedAt: now, UpdatedAt: now, RecencyAt: now}); err != nil {
		t.Fatalf("Save() error = %v", err)
	}
	for index, text := range []string{"first task", "later task"} {
		item := Item{
			ID:        fmt.Sprintf("output-%d", index),
			Type:      "function_call_output",
			Name:      "create_thread",
			Namespace: "codex_tui",
			Text: "<codex_delegation>\n  <source_thread_id>source</source_thread_id>\n  <input>" + text +
				"</input>\n</codex_delegation>",
			CreatedAt: now.Add(time.Duration(index) * time.Second),
		}
		record, err := store.AppendItems("thread-1", []Item{item})
		if err != nil {
			t.Fatalf("AppendItems() error = %v", err)
		}
		if index == 0 {
			if record.Preview != text {
				t.Fatalf("first delegated preview = %q, want %q", record.Preview, text)
			}
			continue
		}
		if record.Preview != "first task" {
			t.Fatalf("second delegated preview = %q, want %q", record.Preview, "first task")
		}
	}
}
