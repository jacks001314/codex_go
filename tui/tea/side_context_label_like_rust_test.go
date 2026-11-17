package tea

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
)

// Rust app/side.rs `side_context_label` composes the composer context label
// from " · "-joined parts: the parent identity, the parent's actionable
// status, the toggle binding, and the close action. A saved side chat in a
// workspace session archives on ctrl-c, so the hint has to say "archive".
// Mirrors codex-rs/tui/src/chatwidget/tests/side.rs:302/323/344 and
// app/tests.rs:8274-8276.
func sideContextLabelModel(t *testing.T) *Model {
	t.Helper()
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 24})
	model.State.ThreadID = "parent-24"
	model.activeSide = &activeSideConversation{
		ParentThreadID: "parent-24",
		SideThreadID:   "child-24",
		Storage:        SideChatStorageTemporary,
		ShowingSide:    true,
	}
	return model
}

func TestSideContextLabelJoinsRustPartsLikeRust(t *testing.T) {
	closeHint := codextui.ModifierLabelPrefix(codextui.ControlKeyLabel()) + "c to close"
	model := sideContextLabelModel(t)
	want := "Side from main thread \u00b7 ctrl+/ to switch \u00b7 " + closeHint
	if got := model.sideContextLabel(); got != want {
		t.Fatalf("side context label = %q, want %q", got, want)
	}

	model.activeSide.ParentSideStatus = SideParentStatusNeedsInput
	want = "Side from main thread \u00b7 main needs input \u00b7 ctrl+/ to switch \u00b7 " + closeHint
	if got := model.sideContextLabel(); got != want {
		t.Fatalf("side context label with parent status = %q, want %q", got, want)
	}

	model.activeSide.ParentSideStatus = SideParentStatusNeedsApproval
	want = "Side from main thread \u00b7 main needs approval \u00b7 ctrl+/ to switch \u00b7 " + closeHint
	if got := model.sideContextLabel(); got != want {
		t.Fatalf("side context label with approval status = %q, want %q", got, want)
	}
}

func TestSideContextLabelArchivesSavedWorkspaceSideLikeRust(t *testing.T) {
	closeHint := codextui.ModifierLabelPrefix(codextui.ControlKeyLabel()) + "c to archive"
	model := sideContextLabelModel(t)
	model.featureSettings = map[string]bool{sideWorkspaceFeatureKey: true}
	model.activeSide.Storage = SideChatStorageAttached
	want := "Side from main thread \u00b7 ctrl+/ to switch \u00b7 " + closeHint
	if got := model.sideContextLabel(); got != want {
		t.Fatalf("saved workspace side label = %q, want %q", got, want)
	}

	// The legacy temporary fork is deleted, and Rust keeps "close" when the
	// workspace feature is off even for a saved side chat.
	model.featureSettings = map[string]bool{sideWorkspaceFeatureKey: false}
	model.activeSide.Storage = SideChatStorageAttached
	want = "Side from main thread \u00b7 ctrl+/ to switch \u00b7 " +
		codextui.ModifierLabelPrefix(codextui.ControlKeyLabel()) + "c to close"
	if got := model.sideContextLabel(); got != want {
		t.Fatalf("saved side label without workspace = %q, want %q", got, want)
	}
}

func TestSideContextLabelNamesNonPrimaryParentLikeRust(t *testing.T) {
	model := sideContextLabelModel(t)
	model.agentItems = []codextui.AgentThreadEntry{{
		ThreadID:  "parent-24",
		AgentPath: "agents/reviewer",
	}}
	want := "Side from parent thread (agents/reviewer) \u00b7 ctrl+/ to switch \u00b7 " +
		codextui.ModifierLabelPrefix(codextui.ControlKeyLabel()) + "c to close"
	if got := model.sideContextLabel(); got != want {
		t.Fatalf("side context label for a subagent parent = %q, want %q", got, want)
	}
}

func TestSideContextLabelHidesToggleWithoutBindingLikeRust(t *testing.T) {
	model := sideContextLabelModel(t)
	model.keymapConfig = codextui.NewKeymapConfig()
	if err := model.keymapConfig.Set("global", "toggle_side_conversation", nil); err != nil {
		t.Fatalf("clear toggle binding: %v", err)
	}
	if got := model.sideContextLabel(); got != "Side from main thread \u00b7 ctrl+c to close" {
		t.Fatalf("side context label without a toggle binding = %q", got)
	}

	model.activeSide.ShowingSide = false
	if got := model.sideContextLabel(); got != "" {
		t.Fatalf("hidden side label without a toggle binding = %q, want empty", got)
	}
}

// Rust bottom_pane/footer.rs `side_conversation_context_line` splits the label
// on " · ", paints "Side" bold magenta, keeps the remaining prose magenta, and
// renders each "… to switch/close/archive" / "… for side" segment as a bold key
// binding followed by secondary action text.
func TestSideContextLinePaintsRustFooterSpansLikeRust(t *testing.T) {
	restore := codextui.SetStdoutColorLevelForTest(codextui.ColorANSI256)
	defer restore()

	model := sideContextLabelModel(t)
	label := model.sideContextLabel()
	line := model.renderSideContextLine(label, 120)
	if got := ansi.Strip(line); got != label {
		t.Fatalf("styled side context line text = %q, want %q", got, label)
	}
	if !strings.HasPrefix(line, "\x1b[1;38;5;5mSide\x1b[0m ") {
		t.Fatalf("side context line does not start with bold magenta Side: %q", line)
	}
	if !strings.Contains(line, "\x1b[1mctrl+/\x1b[0m to switch") {
		t.Fatalf("toggle binding is not painted as a bold key hint: %q", line)
	}
	// The action verb keeps the secondary style, never the key-hint bold.
	if strings.Contains(line, "\x1b[1m to switch") {
		t.Fatalf("action verb inherited the key-hint style: %q", line)
	}
	// The long-form parent prose stays magenta rather than dim footer grey.
	if !strings.Contains(line, "38;5;5mfrom main thread") {
		t.Fatalf("parent prose is not magenta: %q", line)
	}

	narrow := model.renderSideContextLine(label, 20)
	if width := ansi.StringWidth(ansi.Strip(narrow)); width > 20 {
		t.Fatalf("narrow side context line is %d columns wide: %q", width, ansi.Strip(narrow))
	}
}
