package tea

import (
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"
)

// Batch T2-8b: `/title` and `/statusline` must tokenize their arguments with the
// shared slash-command grammar Rust uses everywhere
// (codex-rs/tui/src/chatwidget/slash_command_args.rs:54-102, called from
// status_surface_commands.rs:77 and :94). The scanner these tests replace split
// on whitespace, so it skipped `--` and treated settings after it as switches,
// accepted repeated settings, checked the `--clear` combination before the items
// and had no quoting rules at all.

func TestStatusSurfaceBareDoubleDashLeavesSettingsPositionalLikeRust(t *testing.T) {
	model := statusSurfaceTestModel(t, nil)
	model.applyStatusLineCommand("model current-dir")
	before := strings.Join(model.statusControls.StatusLineIDs, ",")
	if before == "" {
		t.Fatal("inline status-line items did not take effect")
	}

	// Rust stops parsing settings at a bare `--` (slash_command_args.rs:66-68), so
	// `--clear` after it is an item and gets reported as unknown.
	model.applyStatusLineCommand("-- --clear")
	if want := "Unknown status-line item '--clear'. Use /statusline to see available items."; model.notice != want {
		t.Fatalf("notice = %q, want %q", model.notice, want)
	}
	if after := strings.Join(model.statusControls.StatusLineIDs, ","); after != before {
		t.Fatalf("`-- --clear` cleared the status line: %q -> %q", before, after)
	}

	model.applyTerminalTitleCommand("-- --clear")
	if want := "Unknown terminal-title item '--clear'. Use /title to see available items."; model.notice != want {
		t.Fatalf("notice = %q, want %q", model.notice, want)
	}

	// `--` is a separator rather than a "rest is junk" marker, so a real item
	// still applies after it.
	model.applyStatusLineCommand("-- model")
	ids := model.statusControls.StatusLineIDs
	if len(ids) != 1 || !strings.Contains(ids[0], "model") {
		t.Fatalf("status-line items after `--` = %#v, want a single model item", ids)
	}

	// Same through the composer: the typed form must dispatch locally and keep
	// the selection intact (statusSurfaceTestModel fails the test on a submit).
	typed := statusSurfaceTestModel(t, nil)
	typeText(t, typed, "/statusline model")
	typed.Update(key(bubbletea.KeyEnter))
	typeText(t, typed, "/statusline -- --clear")
	typed.Update(key(bubbletea.KeyEnter))
	if want := "Unknown status-line item '--clear'. Use /statusline to see available items."; typed.notice != want {
		t.Fatalf("typed notice = %q, want %q", typed.notice, want)
	}
	if ids := typed.statusControls.StatusLineIDs; len(ids) != 1 {
		t.Fatalf("typed `-- --clear` changed the status line: %#v", ids)
	}
}

func TestStatusSurfaceRepeatedSettingsRejectedLikeRust(t *testing.T) {
	model := statusSurfaceTestModel(t, nil)
	model.applyStatusLineCommand("model current-dir")
	before := strings.Join(model.statusControls.StatusLineIDs, ",")

	cases := []struct {
		apply func(string) bubbletea.Cmd
		args  string
		want  string
	}{
		// Rust reports the repeat before it applies anything (slash_command_args.rs:85-96).
		{model.applyStatusLineCommand, "--clear --clear", "Command setting `--clear` was provided more than once."},
		{model.applyStatusLineCommand, "--colors --colors", "Command setting `--colors` was provided more than once."},
		// Boolean settings are stored under the positive name, so the repeat is
		// reported as `--colors` even for `--no-colors`.
		{model.applyStatusLineCommand, "--no-colors --no-colors", "Command setting `--colors` was provided more than once."},
		{model.applyStatusLineCommand, "--colors --no-colors", "Specify only one of --colors or --no-colors."},
		{model.applyTerminalTitleCommand, "--clear --clear", "Command setting `--clear` was provided more than once."},
	}
	for _, testCase := range cases {
		testCase.apply(testCase.args)
		if model.notice != testCase.want {
			t.Fatalf("%q notice = %q, want %q", testCase.args, model.notice, testCase.want)
		}
	}
	if after := strings.Join(model.statusControls.StatusLineIDs, ","); after != before {
		t.Fatalf("rejected settings changed the status line: %q -> %q", before, after)
	}
}

func TestStatusSurfaceUnknownItemOutranksClearConflictLikeRust(t *testing.T) {
	model := statusSurfaceTestModel(t, nil)
	model.applyStatusLineCommand("model current-dir")
	before := strings.Join(model.statusControls.StatusLineIDs, ",")

	// Rust resolves every positional item before it looks at `--clear`
	// (status_surface_commands.rs:94-104 with parse_item at :122-160).
	model.applyStatusLineCommand("--clear nope")
	if want := "Unknown status-line item 'nope'. Use /statusline to see available items."; model.notice != want {
		t.Fatalf("notice = %q, want %q", model.notice, want)
	}
	model.applyTerminalTitleCommand("--clear nope")
	if want := "Unknown terminal-title item 'nope'. Use /title to see available items."; model.notice != want {
		t.Fatalf("notice = %q, want %q", model.notice, want)
	}
	// A valid item still trips the combination error.
	model.applyStatusLineCommand("--clear model")
	if want := "Cannot combine --clear with status-line items."; model.notice != want {
		t.Fatalf("notice = %q, want %q", model.notice, want)
	}
	model.applyTerminalTitleCommand("--clear app-name")
	if want := "Cannot combine --clear with terminal-title items."; model.notice != want {
		t.Fatalf("notice = %q, want %q", model.notice, want)
	}
	if after := strings.Join(model.statusControls.StatusLineIDs, ","); after != before {
		t.Fatalf("rejected items changed the status line: %q -> %q", before, after)
	}
}

func TestStatusSurfaceQuotedArgumentsFollowShlexLikeRust(t *testing.T) {
	model := statusSurfaceTestModel(t, nil)

	// Rust splits with shlex (slash_command_args.rs:60-61), so the quotes are
	// removed and the quoted word is one positional token.
	model.applyStatusLineCommand("'current-dir'")
	ids := model.statusControls.StatusLineIDs
	if len(ids) != 1 || !strings.Contains(ids[0], "current-dir") {
		t.Fatalf("single-quoted item ids = %#v, want one current-dir item", ids)
	}
	model.applyStatusLineCommand("\"current-dir\"")
	if ids := model.statusControls.StatusLineIDs; len(ids) != 1 {
		t.Fatalf("double-quoted item ids = %#v, want one current-dir item", ids)
	}

	// A quoted phrase stays one token, so it fails as one unknown item.
	model.applyStatusLineCommand("'my host'")
	if want := "Unknown status-line item 'my host'. Use /statusline to see available items."; model.notice != want {
		t.Fatalf("notice = %q, want %q", model.notice, want)
	}
	if ids := model.statusControls.StatusLineIDs; len(ids) != 1 {
		t.Fatalf("quoted phrase changed the status line: %#v", ids)
	}

	// Unbalanced quoting is rejected before any token is applied.
	model.applyStatusLineCommand("'unfinished")
	if want := "Invalid command arguments. Check the quoting."; model.notice != want {
		t.Fatalf("notice = %q, want %q", model.notice, want)
	}
	model.applyTerminalTitleCommand("'unfinished")
	if want := "Invalid command arguments. Check the quoting."; model.notice != want {
		t.Fatalf("notice = %q, want %q", model.notice, want)
	}

	model.applyTerminalTitleCommand("'app-name'")
	if ids := model.statusControls.TerminalTitleIDs; len(ids) != 1 {
		t.Fatalf("quoted terminal-title ids = %#v, want one item", ids)
	}
}
