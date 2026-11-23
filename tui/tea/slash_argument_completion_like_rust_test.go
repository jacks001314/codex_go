package tea

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	codextui "codex_go/tui"
	"codex_go/tui/keymsg"
)

func newSlashArgumentModel(t *testing.T) *Model {
	t.Helper()
	state := codextui.NewState(nil)
	state.SetThreadID("thread-a")
	state.AddMessage(codextui.RoleUser, "hello")
	model := NewModel(state, Options{Width: 100, Height: 30})
	model.refreshTranscript()
	return model
}

func slashArgumentNames(model *Model) []string {
	names := make([]string, 0, len(model.slashPopup.Items))
	for _, item := range model.slashPopup.Items {
		names = append(names, item.Name)
	}
	return names
}

// Rust #62343: the popup suggests the first argument of a supported command,
// Tab rewrites that token, and Enter still sends the draft as typed.
func TestSlashArgumentPopupCompletesFirstArgumentLikeRust(t *testing.T) {
	model := newSlashArgumentModel(t)
	typeText(t, model, "/mcp lo")
	if !model.slashPopup.Active || !model.slashPopup.Argument {
		t.Fatalf("popup = %#v", model.slashPopup)
	}
	if names := slashArgumentNames(model); len(names) != 1 || names[0] != "login" {
		t.Fatalf("argument suggestions = %#v", names)
	}
	if rendered := ansi.Strip(model.renderSlashPopup()); !strings.Contains(rendered, "login") {
		t.Fatalf("popup render =\n%s", rendered)
	}

	model.composerLastCharAt = nil
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyTab})
	if got := model.composerText(); got != "/mcp login" {
		t.Fatalf("composer after tab = %q", got)
	}
	if model.slashPopup.Active {
		t.Fatal("tab left the argument popup open")
	}
}

// The empty prefix lists the whole table and a typed prefix filters it.
func TestSlashArgumentPopupFiltersByPrefixLikeRust(t *testing.T) {
	model := newSlashArgumentModel(t)
	typeText(t, model, "/raw ")
	if names := slashArgumentNames(model); strings.Join(names, ",") != "on,off" {
		t.Fatalf("empty prefix suggestions = %#v", names)
	}
	typeText(t, model, "of")
	if names := slashArgumentNames(model); strings.Join(names, ",") != "off" {
		t.Fatalf("filtered suggestions = %#v", names)
	}
	model.composerLastCharAt = nil
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyTab})
	if got := model.composerText(); got != "/raw off" {
		t.Fatalf("composer after tab = %q", got)
	}
}

// Enter submits an argument draft as typed instead of accepting the row.
func TestSlashArgumentPopupEnterKeepsTypedDraftLikeRust(t *testing.T) {
	model := newSlashArgumentModel(t)
	typeText(t, model, "/raw o")
	if !model.slashPopup.Argument {
		t.Fatalf("popup = %#v", model.slashPopup)
	}
	// Enter is not handled by the argument popup: the draft follows ordinary
	// submission routing exactly as typed.
	if cmd, handled := model.updateSlashPopupKey(keymsg.KeyMsg{Type: keymsg.KeyEnter}); handled || cmd != nil {
		t.Fatalf("enter handled by the argument popup: handled=%t cmd=%v", handled, cmd)
	}
	if model.slashPopup.Active {
		t.Fatal("enter left the argument popup open")
	}
	if got := model.composerText(); strings.Contains(got, "raw on") || strings.Contains(got, "raw off") {
		t.Fatalf("enter rewrote the draft to %q", got)
	}
}

// Commands without an argument table, a second argument, and a draft edited
// after the popup was built never complete.
func TestSlashArgumentPopupIgnoresUnsupportedDraftsLikeRust(t *testing.T) {
	model := newSlashArgumentModel(t)
	for _, draft := range []string{"/model f", "/mcp login now", "/raw on off"} {
		typeText(t, model, draft)
		if model.slashPopup.Active && model.slashPopup.Argument {
			t.Fatalf("%q opened an argument popup: %#v", draft, model.slashPopup)
		}
		model.setComposerPrompt("")
	}

	typeText(t, model, "/mcp lo")
	model.slashPopup.ArgumentEnd++ // the live token no longer matches the popup
	before := model.composerText()
	if cmd := model.completeSelectedSlashCommand(); cmd != nil {
		t.Fatal("stale completion returned a command")
	}
	if got := model.composerText(); got != before {
		t.Fatalf("stale completion rewrote %q to %q", before, got)
	}
}
