package tea

import (
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"

	"codex_go/tui/keymsg"
)

// setSlashArgumentDraft replaces the draft, parks the cursor at a byte offset
// in canonical text and resynchronizes the popup.
func setSlashArgumentDraft(t *testing.T, model *Model, text string, cursor int) {
	t.Helper()
	model.setComposerPrompt(text)
	body := model.composerBody()
	offset := cursor - len(model.composerPrefix.text())
	if offset < 0 {
		offset = 0
	}
	if offset > len(body) {
		offset = len(body)
	}
	for offset > 0 && offset < len(body) && !utf8.RuneStart(body[offset]) {
		offset--
	}
	model.setComposerCursorOffset(utf8.RuneCountInString(body[:offset]))
	model.refreshSlashPopup()
}

// Rust #63036: named settings complete flag names and values in both the
// `--name=value` and `--name value` forms, including multiline drafts.
func TestSlashNamedArgumentCompletesNamesAndValuesLikeRust(t *testing.T) {
	model := newSlashArgumentModel(t)
	for _, test := range []struct {
		draft string
		want  string
	}{
		{"/permissions p --reviewer=a", "/permissions p --reviewer=auto_review"},
		{"/permissions p --reviewer a", "/permissions p --reviewer auto_review"},
		{"/statusline model --no-c", "/statusline model --no-colors"},
		{"/model\u00a0--sp", "/model\u00a0--speed"},
		{"/model\n--speed=f", "/model\n--speed=flex"},
		{"/model --speed\n  f", "/model --speed\n  flex"},
		{"/model\n\u00a0--sp", "/model\n\u00a0--speed"},
		{"/mcp login 'a b' --e", "/mcp login 'a b' --environment"},
	} {
		setSlashArgumentDraft(t, model, test.draft, len(test.draft))
		if !model.slashPopup.Active || !model.slashPopup.Argument || !model.slashPopup.ArgumentNamed {
			t.Fatalf("%q popup = %#v", test.draft, model.slashPopup)
		}
		model.composerLastCharAt = nil
		model.Update(keymsg.KeyMsg{Type: keymsg.KeyTab})
		if got := model.composerText(); got != test.want {
			t.Fatalf("%q completed to %q, want %q", test.draft, got, test.want)
		}
	}
}

// Rust #63036: completion keeps the text after the cursor, leaves quoted and
// escaped positions alone, filters flags already present, and never completes
// inside a comment or after `--`.
func TestSlashNamedArgumentPreservesTailAndRejectsShellContextLikeRust(t *testing.T) {
	model := newSlashArgumentModel(t)
	draft := "/mcp login --enX=\"foo bar\" srv"
	setSlashArgumentDraft(t, model, draft, len("/mcp login --en"))
	if !model.slashPopup.Argument || !model.slashPopup.ArgumentNamed {
		t.Fatalf("popup = %#v", model.slashPopup)
	}
	model.composerLastCharAt = nil
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyTab})
	if got, want := model.composerText(), "/mcp login --environment=\"foo bar\" srv"; got != want {
		t.Fatalf("completed tail = %q, want %q", got, want)
	}
	if got, want := model.composerCursor(), len("/mcp login --environment"); got != want {
		t.Fatalf("cursor after tab = %d, want %d", got, want)
	}

	draft = "/model --speed=f\u00a0x"
	setSlashArgumentDraft(t, model, draft, len("/model --speed=f"))
	model.composerLastCharAt = nil
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyTab})
	if got := model.composerText(); got != "/model --speed=flex" {
		t.Fatalf("non-breaking-space tail completed to %q", got)
	}

	cursor := len("/model --spe")
	for _, test := range []struct {
		draft  string
		cursor int
	}{
		{"/model --spe --speed=flex", cursor},
		{"/model --speed  flex", len("/model --speed ")},
		{"/model --speed= flex", len("/model --speed=")},
		{"/model --speed # comment\nflex", len("/model --speed ")},
		{"/title --cl -- model", len("/title --cl")},
		{"/model --spe\n--speed=flex", cursor},
		{"/statusline --no-c=true", len("/statusline --no-c")},
	} {
		setSlashArgumentDraft(t, model, test.draft, test.cursor)
		if model.slashPopup.Argument {
			t.Fatalf("%q opened an argument popup: %#v", test.draft, model.slashPopup)
		}
	}

	for _, draft := range []string{
		"/raw\no",
		"/model --speed=flex\n--spe",
		"/model -- --spe",
		"/model 'quoted --spe",
		"/model escaped\\ --spe",
		"/model --speed=flex --speed=s",
		"/model --speed=flex --speed s",
		"/statusline --colors --no-c",
		"/mcp verbose --env",
		"/mcp 'login' server --env",
		"/model __codex_completion_probe__ # --spe",
	} {
		setSlashArgumentDraft(t, model, draft, len(draft))
		if model.slashPopup.Argument {
			t.Fatalf("%q opened an argument popup: %#v", draft, model.slashPopup)
		}
	}

	draft = "/model --spe --rea"
	setSlashArgumentDraft(t, model, draft, len("/model --spe"))
	if !model.slashPopup.Argument {
		t.Fatalf("popup = %#v", model.slashPopup)
	}
	model.Update(keymsg.KeyMsg{Type: keymsg.KeyEsc})
	if model.slashPopup.Active {
		t.Fatal("esc left the popup open")
	}
	model.refreshSlashPopup()
	if model.slashPopup.Argument {
		t.Fatal("escaped popup reopened for the same draft")
	}
	setSlashArgumentDraft(t, model, draft, len(draft))
	if !model.slashPopup.Argument {
		t.Fatalf("popup after moving the cursor = %#v", model.slashPopup)
	}
}

// Rust #63036: the popup lists the command's flags and filters flags that are
// already present.
func TestSlashNamedArgumentPopupRendersFlagsLikeRust(t *testing.T) {
	model := newSlashArgumentModel(t)
	setSlashArgumentDraft(t, model, "/model --", len("/model --"))
	if names := slashArgumentNames(model); strings.Join(names, ",") != "--reasoning,--speed" {
		t.Fatalf("flag suggestions = %#v", names)
	}
	rendered := ansi.Strip(model.renderSlashPopup())
	if !strings.Contains(rendered, "--reasoning") || !strings.Contains(rendered, "--speed") {
		t.Fatalf("popup render =\n%s", rendered)
	}

	setSlashArgumentDraft(t, model, "/model --speed=flex --", len("/model --speed=flex --"))
	if names := slashArgumentNames(model); strings.Join(names, ",") != "--reasoning" {
		t.Fatalf("deduplicated flag suggestions = %#v", names)
	}

	setSlashArgumentDraft(t, model, "/model --speed=s", len("/model --speed=s"))
	if names := slashArgumentNames(model); strings.Join(names, ",") != "standard" {
		t.Fatalf("value suggestions = %#v", names)
	}
}
