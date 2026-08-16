package parity

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"codex_go/chatgptapi"
)

// TestBackendTaskDetailsFixturesMatchRust is the shared-fixture double-run for
// backend-client task-details parsing: Go's chatgptapi.CodeTaskDetailsResponse
// must extract the same diff / messages / prompt / error from the committed
// Rust fixtures as codex-rs/backend-client/src/types.rs tests assert.
func TestBackendTaskDetailsFixturesMatchRust(t *testing.T) {
	root := rustSnapshotRoot(t)
	diffFixture := loadTaskDetailsFixture(t, root, "task_details_with_diff.json")
	errorFixture := loadTaskDetailsFixture(t, root, "task_details_with_error.json")

	// unified_diff prefers current_diff_task_turn output_diff.
	diff, ok := diffFixture.UnifiedDiff()
	if !ok || !strings.Contains(diff, "diff --git") {
		t.Fatalf("diff fixture UnifiedDiff() = %q, %v; want diff --git from current_diff_task_turn", diff, ok)
	}

	// unified_diff falls back to pr output diff when there is no diff turn.
	fallback, ok := errorFixture.UnifiedDiff()
	if !ok || !strings.Contains(fallback, "lib.rs") {
		t.Fatalf("error fixture UnifiedDiff() = %q, %v; want fallback to pr output_diff", fallback, ok)
	}

	// assistant_text_messages extracts text content.
	messages := diffFixture.AssistantTextMessages()
	if !reflect.DeepEqual(messages, []string{"Assistant response"}) {
		t.Fatalf("diff fixture AssistantTextMessages() = %#v, want [Assistant response]", messages)
	}

	// user_text_prompt joins parts with spacing.
	prompt, ok := diffFixture.UserTextPrompt()
	if !ok || prompt != "First line\n\nSecond line" {
		t.Fatalf("diff fixture UserTextPrompt() = %q, %v; want %q", prompt, ok, "First line\n\nSecond line")
	}

	// assistant_error_message combines code and message.
	msg, ok := errorFixture.AssistantErrorMessage()
	if !ok || msg != "APPLY_FAILED: Patch could not be applied" {
		t.Fatalf("error fixture AssistantErrorMessage() = %q, %v", msg, ok)
	}
}

func loadTaskDetailsFixture(t *testing.T, root, name string) chatgptapi.CodeTaskDetailsResponse {
	t.Helper()
	path := filepath.Join(root, "backend-client", "tests", "fixtures", name)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile(%s): %v", path, err)
	}
	var details chatgptapi.CodeTaskDetailsResponse
	if err := json.Unmarshal(data, &details); err != nil {
		t.Fatalf("Unmarshal(%s): %v", path, err)
	}
	return details
}
