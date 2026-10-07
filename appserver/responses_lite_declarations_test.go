package appserver

import (
	"encoding/json"
	"testing"
	"time"

	"codex_go/model"
	"codex_go/session"
)

// TestResponsesLiteWindowDeclarationFreezeLikeRust covers the appserver half of
// Rust #51480: a responses-lite context window records its tool declarations
// once, at window start, and replays them unchanged until a window replacement
// (a compaction or a context reset advances the window number) re-derives them
// from the current settings. Mirrors the request-history scenarios of Rust
// `core/tests/suite/scenarios_incremental_tools_resume.rs` (legacy resume,
// migration after remote compaction/window reset, disabling incremental tools,
// tool updates after restart).
func TestResponsesLiteWindowDeclarationFreezeLikeRust(t *testing.T) {
	router, _ := newResponsesLiteDeclarationRouter(t, "thread-51480")

	tools := []any{map[string]any{"type": "function", "name": "exec_command"}}
	instructions := "Use the available tools to help the user."
	history := []any{map[string]any{"type": "message", "role": "user", "content": "Begin."}}

	// Window start: a brand new thread records the current declarations.
	frozen := router.responsesLiteWindowDeclarationItemsForTurn("thread-51480", nil, true, true, tools, instructions)
	if len(frozen) != 2 {
		t.Fatalf("frozen declarations = %#v", frozen)
	}
	declaration, _ := frozen[0].(map[string]any)
	if declaration["type"] != "additional_tools" {
		t.Fatalf("frozen declaration = %#v", frozen[0])
	}
	stored, err := router.threadRecord(session.ThreadID("thread-51480"), true, true)
	if err != nil || stored == nil {
		t.Fatalf("threadRecord error = %v", err)
	}
	if stored.Metadata.Extra[model.ToolDeclarationWindowNumberKey] == nil {
		t.Fatalf("window decision not persisted: %#v", stored.Metadata.Extra)
	}

	// Later requests of the same window reuse the recorded declarations even
	// when the tool settings changed, so the window never moves them.
	changedTools := []any{map[string]any{"type": "function", "name": "update_plan"}}
	reused := router.responsesLiteWindowDeclarationItemsForTurn("thread-51480", history, true, true, changedTools, "different instructions")
	if !sameJSON(t, frozen, reused) {
		t.Fatalf("window rebuilt declarations on resume:\n%v\n%v", frozen, reused)
	}

	// Rust #51480 `disable_incremental_tools_at_next_window`: disabling the
	// incremental tools setting preserves the existing window's declarations.
	disabled := router.responsesLiteWindowDeclarationItemsForTurn("thread-51480", history, true, false, changedTools, "different instructions")
	if !sameJSON(t, frozen, disabled) {
		t.Fatalf("disabling incremental tools moved the live window's declarations:\n%v\n%v", frozen, disabled)
	}

	// A window replacement - remote compaction or a context reset advances the
	// window number - re-derives the declarations from the current settings.
	router.advanceWindowNumber("thread-51480")
	migrated := router.responsesLiteWindowDeclarationItemsForTurn("thread-51480", history, true, true, changedTools, instructions)
	want := model.ResponsesLiteDeclarationItems(changedTools, instructions, "thread-51480")
	if !sameJSON(t, want, migrated) {
		t.Fatalf("new window did not adopt the current tools:\n%v\n%v", want, migrated)
	}

	// Tool updates after restart: the restored window number still matches, so
	// the window keeps its migrated declarations.
	restarted := router.responsesLiteWindowDeclarationItemsForTurn("thread-51480", history, true, true, changedTools, instructions)
	if !sameJSON(t, want, restarted) {
		t.Fatalf("restart rebuilt the live window's declarations:\n%v\n%v", want, restarted)
	}

	// A window replacement while incremental tools are disabled records the new
	// window as legacy: the request rebuilds its prefix, the responses-lite shape
	// that predates #51480.
	router.advanceWindowNumber("thread-51480")
	if got := router.responsesLiteWindowDeclarationItemsForTurn("thread-51480", history, true, false, tools, instructions); got != nil {
		t.Fatalf("legacy replacement window declarations = %#v", got)
	}

	// A non-lite model never records declarations.
	if got := router.responsesLiteWindowDeclarationItemsForTurn("thread-51480", history, false, true, tools, instructions); got != nil {
		t.Fatalf("non-lite declarations = %#v", got)
	}
}

// TestResponsesLiteLegacyResumeStaysLegacyLikeRust covers the legacy-resume case:
// an existing window recorded before the decision existed keeps rebuilding its
// declaration prefix instead of adopting incremental tools mid-window.
func TestResponsesLiteLegacyResumeStaysLegacyLikeRust(t *testing.T) {
	router, _ := newResponsesLiteDeclarationRouter(t, "thread-legacy")
	history := []any{map[string]any{"type": "message", "role": "user", "content": "Begin with the legacy tool prefix."}}

	if got := router.responsesLiteWindowDeclarationItemsForTurn("thread-legacy", history, true, true, []any{map[string]any{"type": "function", "name": "exec_command"}}, "Use the tools."); got != nil {
		t.Fatalf("legacy resume declarations = %#v", got)
	}
	// The window is now frozen as legacy, so later requests stay legacy too.
	if got := router.responsesLiteWindowDeclarationItemsForTurn("thread-legacy", history, true, true, []any{map[string]any{"type": "function", "name": "update_plan"}}, "Use the tools."); got != nil {
		t.Fatalf("frozen legacy declarations = %#v", got)
	}
	stored, err := router.threadRecord(session.ThreadID("thread-legacy"), true, true)
	if err != nil || stored == nil {
		t.Fatalf("threadRecord error = %v", err)
	}
	if stored.Metadata.Extra[model.ToolDeclarationModeKey] != string(model.ToolDeclarationLegacy) {
		t.Fatalf("legacy decision = %#v", stored.Metadata.Extra)
	}
}

func newResponsesLiteDeclarationRouter(t *testing.T, threadID string) (*RuntimeRouter, *session.Record) {
	t.Helper()
	store := session.NewStore(t.TempDir())
	now := time.Now().UTC()
	record := &session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata:  session.Metadata{CWD: t.TempDir(), Model: "gpt-5.4"},
	}
	if err := store.Create(record); err != nil {
		t.Fatal(err)
	}
	return NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(store)}), record
}

func sameJSON(t *testing.T, left any, right any) bool {
	t.Helper()
	leftJSON, err := json.Marshal(left)
	if err != nil {
		t.Fatalf("Marshal left error = %v", err)
	}
	rightJSON, err := json.Marshal(right)
	if err != nil {
		t.Fatalf("Marshal right error = %v", err)
	}
	return string(leftJSON) == string(rightJSON)
}
