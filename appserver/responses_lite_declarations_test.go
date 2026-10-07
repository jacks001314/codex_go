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

// TestResponsesLiteContextResetRedrivesDeclarationsLikeRust covers the
// context-reset branch of Rust #51480: Rust `Session::start_new_context_window`
// advances the auto-compact window (`auto_compact_window.advance()`), so a
// token-budget roll-over starts a new context window and the replacement window
// re-derives its tool declarations from the current settings instead of
// replaying the previous window's catalog. Mirrors the `legacy_resume_window_reset`
// and `disable_incremental_tools` scenarios of Rust
// `core/tests/suite/scenarios_incremental_tools_resume.rs`.
func TestResponsesLiteContextResetRedrivesDeclarationsLikeRust(t *testing.T) {
	router, _ := newResponsesLiteDeclarationRouter(t, "thread-reset")
	tools := []any{map[string]any{"type": "function", "name": "exec_command"}}
	instructions := "Use the available tools to help the user."
	history := []any{map[string]any{"type": "message", "role": "user", "content": "Continue."}}
	changedTools := []any{map[string]any{"type": "function", "name": "update_plan"}}

	// Window start records the current declarations and every later request of
	// the live window replays them unchanged.
	frozen := router.responsesLiteWindowDeclarationItemsForTurn("thread-reset", nil, true, true, tools, instructions)
	if len(frozen) != 2 {
		t.Fatalf("frozen declarations = %#v", frozen)
	}
	if got := router.responsesLiteWindowDeclarationItemsForTurn("thread-reset", history, true, true, changedTools, instructions); !sameJSON(t, frozen, got) {
		t.Fatalf("live window moved declarations before the reset:\n%v\n%v", frozen, got)
	}

	windowBefore := router.windowNumberForThread("thread-reset")
	if err := router.resetContextWindow("thread-reset", "turn-reset"); err != nil {
		t.Fatalf("resetContextWindow error = %v", err)
	}
	windowAfter := router.windowNumberForThread("thread-reset")
	if windowAfter != windowBefore+1 {
		t.Fatalf("reset window number = %d, want %d", windowAfter, windowBefore+1)
	}
	record, err := router.threadRecord(session.ThreadID("thread-reset"), true, true)
	if err != nil || record == nil {
		t.Fatalf("threadRecord error = %v", err)
	}
	if stored := record.Metadata.Extra["auto_compact_window_number"]; !sameJSON(t, stored, windowAfter) {
		t.Fatalf("reset did not persist the new window: %#v", record.Metadata.Extra["auto_compact_window_number"])
	}

	// The replacement window adopts the current settings (Rust #51480 applies the
	// incremental tools setting when building a new window after a context reset).
	migrated := router.responsesLiteWindowDeclarationItemsForTurn("thread-reset", history, true, true, changedTools, instructions)
	want := model.ResponsesLiteDeclarationItems(changedTools, instructions, "thread-reset")
	if !sameJSON(t, want, migrated) {
		t.Fatalf("reset window did not adopt the current tools:\n%v\n%v", want, migrated)
	}

	// A later replacement that lands on a legacy window rebuilds its prefix per
	// request (the pre-#51480 responses-lite shape).
	router.advanceWindowNumber("thread-reset")
	if got := router.responsesLiteWindowDeclarationItemsForTurn("thread-reset", history, true, false, tools, instructions); got != nil {
		t.Fatalf("legacy reset window declarations = %#v", got)
	}
}

// TestResponsesLiteForkKeepsWindowDeclarationsLikeRust covers the fork branch of
// Rust #51480: `keep_forked_rollout_item` keeps `AdditionalTools` for a
// full-history fork, so the child replays the parent window's recorded catalog
// instead of rebuilding it (`ContextManager::has_tool_declarations`). Mirrors
// Rust `core/src/agent/control/spawn.rs` fork preservation.
func TestResponsesLiteForkKeepsWindowDeclarationsLikeRust(t *testing.T) {
	router, _ := newResponsesLiteDeclarationRouter(t, "thread-fork-parent")
	tools := []any{map[string]any{"type": "function", "name": "exec_command"}}
	instructions := "Use the available tools to help the user."
	frozen := router.responsesLiteWindowDeclarationItemsForTurn("thread-fork-parent", nil, true, true, tools, instructions)
	if len(frozen) != 2 {
		t.Fatalf("frozen declarations = %#v", frozen)
	}
	parent, err := router.threadRecord(session.ThreadID("thread-fork-parent"), true, true)
	if err != nil || parent == nil {
		t.Fatalf("threadRecord error = %v", err)
	}

	child, err := router.services.ThreadRouter.store.ForkRecord(parent, session.ForkOptions{
		NewID: session.ThreadID("thread-fork-child"),
		Mode:  session.ForkAll,
		Now:   time.Now().UTC(),
	})
	if err != nil || child == nil {
		t.Fatalf("ForkRecord error = %v", err)
	}
	// Tool declarations and their window baseline survive the fork together.
	for _, key := range []string{model.ToolDeclarationWindowNumberKey, model.ToolDeclarationModeKey, model.ToolDeclarationItemsKey} {
		if child.Metadata.Extra[key] == nil {
			t.Fatalf("fork dropped %q: %#v", key, child.Metadata.Extra)
		}
	}

	// A resumed fork replays the parent's recorded catalog instead of rebuilding
	// it, even when the tool settings changed after the fork.
	childRouter := NewRuntimeRouter(RuntimeServices{ThreadRouter: NewRouter(router.services.ThreadRouter.store)})
	history := []any{map[string]any{"type": "message", "role": "user", "content": "Forked turn."}}
	changedTools := []any{map[string]any{"type": "function", "name": "update_plan"}}
	reused := childRouter.responsesLiteWindowDeclarationItemsForTurn("thread-fork-child", history, true, true, changedTools, instructions)
	if !sameJSON(t, frozen, reused) {
		t.Fatalf("fork rebuilt the window's declarations:\n%v\n%v", frozen, reused)
	}
}
