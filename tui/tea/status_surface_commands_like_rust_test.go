package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
)

// Rust #54387 (upstream 141bf7680d, "Add session-only arguments to `/title` and
// `/statusline`") lets both status-surface commands take inline items plus a
// `--clear` switch, and gives `/statusline` the `colors`/`--no-colors` boolean
// setting, all applied to the live widget without touching saved defaults
// (codex-rs/tui/src/chatwidget/status_surface_commands.rs, and
// slash_command.rs:181-182 listing Title/Statusline in supports_inline_args).

func statusSurfaceTestModel(t *testing.T, useColors *bool) *Model {
	t.Helper()
	state := codextui.NewState(&codextui.Options{Model: "gpt-5", ReasoningEffort: "high"})
	model := NewModel(state, Options{
		Width:               90,
		Height:              24,
		StatusLineUseColors: useColors,
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			t.Fatalf("inline status-surface command was submitted as a prompt: %#v", request)
			return nil
		},
	})
	return model
}

// Typed `/statusline <items>` must dispatch locally instead of being sent to the
// model, and it must keep the colour preference the user configured (Rust only
// changes colours when `--colors`/`--no-colors` is given).
func TestStatusLineInlineItemsDispatchAndKeepColorPreferenceLikeRust(t *testing.T) {
	disabled := false
	model := statusSurfaceTestModel(t, &disabled)
	typeText(t, model, "/statusline model-with-reasoning current-dir")
	model.Update(key(bubbletea.KeyEnter))

	if !model.statusLineConfiguredByUser {
		t.Fatal("typed inline status-line items did not configure the live status line")
	}
	if view := model.View(); !strings.Contains(view, "gpt-5") || !strings.Contains(view, "high") {
		t.Fatalf("status line did not render the inline items:\n%s", view)
	}
	if model.statusControls.StatusLineUseThemeColors {
		t.Fatal("inline items must keep the configured colour preference (Rust only flips it for --colors)")
	}
}

// Typed `/title <items>` must dispatch locally and write the title OSC.
func TestTerminalTitleInlineItemsDispatchThroughTheComposerLikeRust(t *testing.T) {
	state := codextui.NewState(&codextui.Options{Model: "gpt-5"})
	var writes []string
	model := NewModel(state, Options{
		Width:  90,
		Height: 24,
		OnWriteTerminalTitle: func(sequence string) bubbletea.Cmd {
			writes = append(writes, sequence)
			return nil
		},
		OnSubmitRequest: func(request SubmitRequest) bubbletea.Cmd {
			t.Fatalf("inline /title command was submitted as a prompt: %#v", request)
			return nil
		},
	})
	typeText(t, model, "/title app-name model")
	model.Update(key(bubbletea.KeyEnter))

	if len(writes) != 1 || !strings.Contains(writes[0], "\x1b]0;codex | gpt-5\x07") {
		t.Fatalf("terminal title writes = %#v", writes)
	}
}

// Rust's `--clear` switch empties the selection and cannot be combined with
// items.
func TestStatusSurfaceClearSwitchLikeRust(t *testing.T) {
	model := statusSurfaceTestModel(t, nil)
	model.applyStatusLineCommand("model permissions")
	if len(model.statusControls.StatusLineIDs) == 0 {
		t.Fatal("inline status-line items did not take effect")
	}
	model.applyStatusLineCommand("--clear")
	if len(model.statusControls.StatusLineIDs) != 0 {
		t.Fatalf("--clear kept items: %#v", model.statusControls.StatusLineIDs)
	}
	model.applyStatusLineCommand("--clear model")
	if want := "Cannot combine --clear with status-line items."; model.notice != want {
		t.Fatalf("notice = %q, want %q", model.notice, want)
	}

	var writes []string
	model.terminalTitleWriter = func(sequence string) bubbletea.Cmd {
		writes = append(writes, sequence)
		return nil
	}
	model.applyTerminalTitleCommand("app-name model")
	model.applyTerminalTitleCommand("--clear")
	// Rust sets terminal_title = Some(vec![]) for `--clear`, so the title stays
	// explicitly configured with an empty selection (disabled), like "off".
	if ids := model.statusControls.TerminalTitleIDs; len(ids) != 0 {
		t.Fatalf("--clear kept terminal-title items: %#v", ids)
	}
	if want := "Terminal title: disabled"; model.notice != want {
		t.Fatalf("notice = %q, want %q", model.notice, want)
	}
	model.applyTerminalTitleCommand("--clear app-name")
	if want := "Cannot combine --clear with terminal-title items."; model.notice != want {
		t.Fatalf("notice = %q, want %q", model.notice, want)
	}
}

// `/statusline --colors` and `--no-colors` update only the colour preference and
// preserve the current items; conflicts and unknown settings are rejected.
func TestStatusLineColorsBooleanSettingLikeRust(t *testing.T) {
	model := statusSurfaceTestModel(t, nil)
	model.applyStatusLineCommand("model current-dir")
	before := append([]string(nil), model.statusControls.StatusLineIDs...)
	if len(before) == 0 {
		t.Fatal("inline status-line items did not take effect")
	}

	model.applyStatusLineCommand("--no-colors")
	if model.statusLineUseColors || model.statusControls.StatusLineUseThemeColors {
		t.Fatal("--no-colors did not disable the status line colours")
	}
	if got := model.statusControls.StatusLineIDs; strings.Join(got, ",") != strings.Join(before, ",") {
		t.Fatalf("colour-only update changed the items: %#v, want %#v", got, before)
	}

	model.applyStatusLineCommand("--colors")
	if !model.statusLineUseColors || !model.statusControls.StatusLineUseThemeColors {
		t.Fatal("--colors did not re-enable the status line colours")
	}

	model.applyStatusLineCommand("--colors --no-colors")
	if want := "Specify only one of --colors or --no-colors."; model.notice != want {
		t.Fatalf("notice = %q, want %q", model.notice, want)
	}

	model.applyTerminalTitleCommand("--colors")
	if want := "Unknown command setting `--colors`."; model.notice != want {
		t.Fatalf("/title notice = %q, want %q", model.notice, want)
	}

	model.applyStatusLineCommand("--bogus")
	if want := "Unknown command setting `--bogus`."; model.notice != want {
		t.Fatalf("notice = %q, want %q", model.notice, want)
	}
}

// Rust reports unknown items with the surface id and the owning command.
func TestStatusSurfaceUnknownItemNoticeLikeRust(t *testing.T) {
	model := statusSurfaceTestModel(t, nil)
	model.applyStatusLineCommand("nope")
	if want := "Unknown status-line item 'nope'. Use /statusline to see available items."; model.notice != want {
		t.Fatalf("status line notice = %q, want %q", model.notice, want)
	}
	model.applyTerminalTitleCommand("nope")
	if want := "Unknown terminal-title item 'nope'. Use /title to see available items."; model.notice != want {
		t.Fatalf("terminal title notice = %q, want %q", model.notice, want)
	}
}
