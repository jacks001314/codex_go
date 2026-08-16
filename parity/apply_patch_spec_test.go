package parity

import (
	"path/filepath"
	"strings"
	"testing"

	"codex_go/applypatch"
)

// TestRustApplyPatchFreeformSpecMatchesGo is the djalign dynamic-layer
// method-1 shared-fixture differential for the apply_patch freeform tool
// specification: Rust embeds core/src/tools/handlers/apply_patch.lark via
// include_str! and builds the FREEFORM tool description in apply_patch_spec.rs
// (create_apply_patch_freeform_tool). Go mirrors both in applypatch.go
// (LarkGrammar constant + CreateFreeformTool). The model-visible tool surface
// (description, grammar, syntax, name, and the optional Environment ID
// variant) must match byte-for-byte.
//
// The Rust side is pinned by blob content: the lark grammar and the
// description line are read from the frozen git checkout (candidateRustTo) so
// upstream edits break the contract instead of silently drifting.
func TestRustApplyPatchFreeformSpecMatchesGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	rustRepo := filepath.Dir(root)

	wantGrammar := gitOutput(t, rustRepo, "show", candidateRustTo+":codex-rs/core/src/tools/handlers/apply_patch.lark")
	if string(wantGrammar) != applypatch.LarkGrammar {
		t.Fatalf("Go LarkGrammar differs from Rust apply_patch.lark blob:\n--- go ---\n%s\n--- rust ---\n%s", applypatch.LarkGrammar, wantGrammar)
	}

	specSource := gitOutput(t, rustRepo, "show", candidateRustTo+":codex-rs/core/src/tools/handlers/apply_patch_spec.rs")
	spec := string(specSource)
	if !strings.Contains(spec, `description: "The `+"`apply_patch`"+` tool can be used to edit files. This is a FREEFORM tool, so do not wrap the patch in JSON.".to_string()`) {
		t.Fatal("Rust apply_patch_spec.rs no longer carries the expected freeform description; re-sync the shared fixture")
	}

	tool := applypatch.CreateFreeformTool(false)
	if tool.Name != "apply_patch" {
		t.Fatalf("Go tool name = %q, want apply_patch", tool.Name)
	}
	if tool.Format.Type != "grammar" || tool.Format.Syntax != "lark" {
		t.Fatalf("Go tool format = %#v, want grammar/lark", tool.Format)
	}
	if tool.Description != "The `apply_patch` tool can be used to edit files. This is a FREEFORM tool, so do not wrap the patch in JSON." {
		t.Fatalf("Go tool description = %q", tool.Description)
	}

	// The environment-id variant must extend the grammar the same way Rust
	// does (create_apply_patch_freeform_tool with include_environment_id).
	withEnv := applypatch.CreateFreeformTool(true)
	wantEnvDefinition := strings.Replace(
		applypatch.LarkGrammar,
		"start: begin_patch hunk+ end_patch",
		"start: begin_patch environment_id? hunk+ end_patch\nenvironment_id: \"*** Environment ID: \" filename LF",
		1,
	)
	if withEnv.Format.Definition != wantEnvDefinition {
		t.Fatalf("Go environment-id grammar differs from Rust substitution:\n--- go ---\n%s\n--- want ---\n%s", withEnv.Format.Definition, wantEnvDefinition)
	}
}
