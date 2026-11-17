package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

func sideFooterRowModel(t *testing.T, width int) *Model {
	t.Helper()
	model := agentsNavigationModel(t, Options{LocalDaemonSession: true})
	model.applyLayoutSize(width, 24)
	// Keep the side chat on the whole composer so the row under test is the
	// real passive footer rather than a split pane's.
	model.featureSettings = map[string]bool{sideWorkspaceFeatureKey: false}
	model.State.ThreadID = "parent-footer"
	model.activeSide = &activeSideConversation{
		ParentThreadID: "parent-footer",
		SideThreadID:   "side-footer",
		Storage:        SideChatStorageTemporary,
		ShowingSide:    true,
	}
	model.notice = model.sideContextLabel()
	return model
}

func sideFooterRowLabel(model *Model) string {
	return strings.TrimSpace(model.sideContextLabel())
}

func sideFooterRowWith(model *Model) string {
	for _, row := range strings.Split(plainView(model.viewString()), "\n") {
		if strings.Contains(row, sideFooterRowLabel(model)) {
			return strings.TrimSpace(row)
		}
	}
	return ""
}

// Rust bottom_pane/chat_composer/passive_hints.rs keeps navigation and action
// hints on the left of one passive row and adds the side conversation context
// label as the last left label, so the label shares the row with
// `← for agents` and `? for shortcuts` instead of claiming its own row.
func TestSideContextLabelSharesPassiveHintRowLikeRust(t *testing.T) {
	model := sideFooterRowModel(t, 240)
	label := sideFooterRowLabel(model)

	want := PlainKey(keymsg.KeyLeft).Label() + " for agents \u00b7 " + footerHelpText + " \u00b7 " + label
	if got := sideFooterRowWith(model); got != want {
		t.Fatalf("passive footer row = %q, want %q", got, want)
	}
	if count := strings.Count(plainView(model.viewString()), label); count != 1 {
		t.Fatalf("side label painted %d times, want once:\n%s", count, plainView(model.viewString()))
	}
}

// Rust fit() drops `? for shortcuts` before `← for agents`, then keeps the
// side label alone when the row cannot hold both hints.
func TestSideContextLabelCollapsesHintsLikeRust(t *testing.T) {
	label := sideFooterRowLabel(sideFooterRowModel(t, 240))
	agents := PlainKey(keymsg.KeyLeft).Label() + " for agents"

	withoutShortcuts := 2 + codextui.DisplayWidth(agents+" \u00b7 "+label)
	if got, want := sideFooterRowWith(sideFooterRowModel(t, withoutShortcuts)), agents+" \u00b7 "+label; got != want {
		t.Fatalf("row without shortcuts = %q, want %q", got, want)
	}

	labelOnly := 2 + codextui.DisplayWidth(label)
	if got, want := sideFooterRowWith(sideFooterRowModel(t, labelOnly)), label; got != want {
		t.Fatalf("row without hints = %q, want %q", got, want)
	}
}

// A row narrower than the label clips it instead of resurrecting the hints
// Rust already dropped.
func TestSideContextLabelClipsLastLikeRust(t *testing.T) {
	model := sideFooterRowModel(t, 24)
	rows := strings.Split(plainView(model.viewString()), "\n")
	row := strings.TrimSpace(rows[len(rows)-1])
	if row == "" {
		t.Fatalf("narrow side footer row missing the label:\n%s", plainView(model.viewString()))
	}
	if strings.Contains(row, "for agents") || strings.Contains(row, footerHelpText) {
		t.Fatalf("narrow side footer row kept dropped hints: %q", row)
	}
	if !strings.HasSuffix(row, "\u2026") {
		t.Fatalf("narrow side footer row = %q, want a clipped label", row)
	}
}
