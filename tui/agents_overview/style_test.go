package agentsoverview

import (
	"strings"
	"testing"
)

func stripANSIForTest(value string) string {
	var builder strings.Builder
	inEscape := false
	for _, r := range value {
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		if r == '\x1b' {
			inEscape = true
			continue
		}
		builder.WriteRune(r)
	}
	return builder.String()
}

func TestRenderStyledMatchesRenderWhenStripped(t *testing.T) {
	view := New(sampleRows(), "", false)
	for _, dims := range [][2]int{{120, 24}, {80, 20}, {100, 12}, {60, 10}} {
		plain := view.Render(dims[0], dims[1])
		styled := view.RenderStyled(dims[0], dims[1])
		if len(plain) != len(styled) {
			t.Fatalf("Render(%dx%d) lines = %d, styled = %d", dims[0], dims[1], len(plain), len(styled))
		}
		for i := range plain {
			if got := stripANSIForTest(styled[i]); got != plain[i] {
				t.Errorf("Render(%dx%d)[%d] stripped = %q, want %q", dims[0], dims[1], i, got, plain[i])
			}
		}
	}
}

func TestRenderStyledAppliesRustStyles(t *testing.T) {
	view := New(sampleRows(), "", false)
	styled := strings.Join(view.RenderStyled(120, 24), "\n")
	for _, want := range []string{
		"\x1b[1mAgent command center\x1b[0m", // bold header
		"\x1b[36;1m›\x1b[0m",                 // cyan bold selection marker
		"\x1b[32m●\x1b[0m",                   // green working dot
		"\x1b[31m●\x1b[0m",                   // red needs-you dot
		"\x1b[36m○\x1b[0m",                   // cyan ready dot
		"\x1b[2m✓\x1b[0m",                    // dim finished dot
		"\x1b[1mTask details\x1b[0m",         // bold details title
	} {
		if !strings.Contains(styled, want) {
			t.Errorf("styled output missing %q:\n%s", want, styled)
		}
	}
}

func TestRenderStyledHelpStopHint(t *testing.T) {
	view := New(sampleRows(), "", false)
	styled := strings.Join(view.RenderStyled(120, 24), "\n")
	if strings.Contains(styled, " stop") {
		t.Fatalf("stop hint must move into help:\n%s", styled)
	}
	view.ToggleHelp()
	if help := strings.Join(view.RenderStyled(120, 30), "\n"); !strings.Contains(help, "x") || !strings.Contains(help, "Stop") {
		t.Errorf("help missing stop hint:\n%s", help)
	}
}

// Rust #47319: the selected row's status symbol uses the row selection style,
// while unselected status symbols keep their group color.
func TestSelectedAgentStatusSymbolUsesSelectionStyleLikeRust(t *testing.T) {
	view := New([]Row{
		{ThreadID: "selected", Name: "Selected", Group: GroupReady},
		{ThreadID: "other", Name: "Other", Group: GroupReady},
	}, "selected", false)
	view.Selected = 0
	styled := strings.Join(view.RenderStyled(100, 12), "\n")
	if !strings.Contains(styled, "\x1b[36;1m○\x1b[0m") {
		t.Fatalf("selected status symbol did not use the row selection style:\n%s", styled)
	}
	if !strings.Contains(styled, "\x1b[36m○\x1b[0m") {
		t.Fatalf("unselected status symbol lost its group color:\n%s", styled)
	}
}

// Rust #47382: the task owning the running voice conversation carries a
// visible voice badge, styled with the row selection when selected.
func TestVoiceBadgeRendersLikeRust(t *testing.T) {
	view := New([]Row{{ThreadID: "voice", Name: "Voice owner", Group: GroupReady, HasVoice: true}}, "", false)
	plain := strings.Join(view.Render(100, 12), "\n")
	if !strings.Contains(plain, "voice") {
		t.Fatalf("voice badge missing:\n%s", plain)
	}
	view.Selected = 0
	styled := strings.Join(view.RenderStyled(100, 12), "\n")
	if !strings.Contains(styled, "\x1b[36;1m  voice\x1b[0m") {
		t.Fatalf("selected voice badge did not use the selection style:\n%s", styled)
	}
}

func TestRenderStyledFooterUsesResolvedShortcutHints(t *testing.T) {
	view := New(sampleRows(), "", false)
	view.SetShortcutHint(ShortcutHintSearch, "ctrl-l")
	view.SetShortcutHint(ShortcutHintStop, "")
	view.ToggleHelp()
	styled := strings.Join(view.RenderStyled(120, 30), "\n")
	if !strings.Contains(styled, "ctrl-l") || !strings.Contains(styled, " Search") {
		t.Errorf("custom search hint missing:\n%s", styled)
	}
	if strings.Contains(styled, "ctrl+f") {
		t.Errorf("default search hint should be replaced:\n%s", styled)
	}
	if strings.Contains(styled, " Stop") {
		t.Errorf("unbound stop hint should be hidden:\n%s", styled)
	}
	if !strings.Contains(styled, "Group") || !strings.Contains(styled, "Rename") {
		t.Errorf("default group/rename hints missing:\n%s", styled)
	}
}

func TestRenderStyledLineWidths(t *testing.T) {
	view := New(sampleRows(), "", false)
	for _, dims := range [][2]int{{120, 24}, {80, 20}, {46, 12}} {
		for _, line := range view.RenderStyled(dims[0], dims[1]) {
			if width := displayWidth(stripANSIForTest(line)); width > dims[0] {
				t.Errorf("RenderStyled(%dx%d) line width %d > %d: %q", dims[0], dims[1], width, dims[0], line)
			}
		}
	}
}

// TestFooterWrapsWholeHintsOnNarrowTerminals pins Rust's footer_hint behavior:
// hints move to additional rows instead of being clipped mid-label.
func TestFooterWrapsWholeHintsOnNarrowTerminals(t *testing.T) {
	view := New(sampleRows(), "", false)
	view.Selected = 0
	styled := strings.Join(view.RenderStyled(60, 24), "\n")
	plain := stripANSIForTest(styled)
	for _, label := range []string{"navigate", "open", "new", "help", "quit"} {
		if !strings.Contains(plain, label) {
			t.Fatalf("footer hint %q missing (truncated?):\n%s", label, plain)
		}
	}
	if strings.Contains(plain, "ctrl-\n") || strings.HasSuffix(plain, "ctrl-") {
		t.Fatalf("footer hint was clipped mid-label:\n%s", plain)
	}
	if strings.Contains(plain, "esc cancel") {
		t.Fatalf("the cancel hint only appears while editing metadata:\n%s", plain)
	}
}
