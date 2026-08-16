package parity

import (
	"bytes"
	"path/filepath"
	"strings"
	"testing"

	"codex_go/appserver"
	chatwidget "codex_go/tui/chatwidget"
)

// TestRustCollaborationModeTemplatesMatchGo is the djalign dynamic-layer
// method-1 shared-fixture differential for the collaboration-mode developer
// templates: Rust embeds collaboration-mode-templates/templates/default.md and
// plan.md via include_str! and renders the default template with
// KNOWN_MODE_NAMES = "Default and Plan" (TUI_VISIBLE_COLLABORATION_MODES
// order, models-manager collaboration_mode_presets.rs format_mode_names). Go
// mirrors the default template as a string constant (chatwidget +
// appserver turn runtime) and the plan template as an embedded file.
//
// Both sides must produce byte-identical developer instructions. The Rust
// blobs are read from the frozen git checkout (candidateRustTo) so Windows
// autocrlf CRLF normalization cannot mask or fabricate a match (the templates
// are include_str!-embedded in Rust and therefore always LF).
func TestRustCollaborationModeTemplatesMatchGo(t *testing.T) {
	root := rustSnapshotRoot(t)
	rustRepo := filepath.Dir(root)

	// Default template: Rust default.md with KNOWN_MODE_NAMES substituted.
	rustDefault := gitOutput(t, rustRepo, "show", candidateRustTo+":codex-rs/collaboration-mode-templates/templates/default.md")
	wantDefault := strings.ReplaceAll(string(rustDefault), "{{KNOWN_MODE_NAMES}}", "Default and Plan")
	if strings.Contains(wantDefault, "{{KNOWN_MODE_NAMES}}") {
		t.Fatal("Rust default template still contains unresolved KNOWN_MODE_NAMES placeholder")
	}

	// Plan template: Rust plan.md.
	wantPlan := gitOutput(t, rustRepo, "show", candidateRustTo+":codex-rs/collaboration-mode-templates/templates/plan.md")

	// Verify the Rust rendering order source is pinned (Default, Plan).
	presets := gitOutput(t, rustRepo, "show", candidateRustTo+":codex-rs/models-manager/src/collaboration_mode_presets.rs")
	if !strings.Contains(string(presets), "TUI_VISIBLE_COLLABORATION_MODES") {
		t.Fatal("Rust collaboration_mode_presets.rs no longer references TUI_VISIBLE_COLLABORATION_MODES; re-sync the shared fixture")
	}
	configTypes := gitOutput(t, rustRepo, "show", candidateRustTo+":codex-rs/protocol/src/config_types.rs")
	if !strings.Contains(string(configTypes), "pub const TUI_VISIBLE_COLLABORATION_MODES: [ModeKind; 2] = [ModeKind::Default, ModeKind::Plan]") {
		t.Fatal("Rust TUI_VISIBLE_COLLABORATION_MODES order changed from [Default, Plan]; re-sync the shared fixture")
	}

	// Go: chatwidget default instructions (the production TUI constant) must
	// equal the rendered Rust template, plus trailing newline exactly once.
	goDefault := chatwidget.DefaultInstructions()
	if goDefault != wantDefault {
		t.Fatalf("Go chatwidget default instructions differ from rendered Rust template:\n--- go ---\n%q\n--- rust ---\n%q", goDefault, wantDefault)
	}

	// Go: appserver turn runtime default instructions must match too (both
	// copies must not drift from Rust).
	goDefaultAppserver := appserver.CollaborationModeDefaultInstructions()
	if goDefaultAppserver != wantDefault {
		t.Fatalf("Go appserver default instructions differ from rendered Rust template:\n--- go ---\n%q\n--- rust ---\n%q", goDefaultAppserver, wantDefault)
	}

	// Go: plan template embedded file must equal the Rust plan.md blob.
	goPlan := chatwidget.PlanInstructions()
	if !bytes.Equal([]byte(goPlan), wantPlan) {
		t.Fatalf("Go plan template differs from Rust plan.md blob:\n--- go (%d bytes) ---\n%s\n--- rust (%d bytes) ---\n%s", len(goPlan), goPlan, len(wantPlan), wantPlan)
	}
}
