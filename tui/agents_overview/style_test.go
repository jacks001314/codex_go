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
		"\x1b[36;1mNew task › \x1b[0m",       // cyan bold prompt label
		"\x1b[2mDescribe a task and press enter to dispatch it\x1b[0m", // dim placeholder
		"\x1b[1mTask details\x1b[0m",                                   // bold details title
	} {
		if !strings.Contains(styled, want) {
			t.Errorf("styled output missing %q:\n%s", want, styled)
		}
	}
}

func TestRenderStyledFooterStopHintDependsOnSelection(t *testing.T) {
	// Selected row root-1 is active -> ctrl+x is bold.
	view := New(sampleRows(), "", false)
	view.Selected = 0
	styled := strings.Join(view.RenderStyled(120, 24), "\n")
	if !strings.Contains(styled, "\x1b[1mctrl+x\x1b[0m") {
		t.Errorf("active selection should bold ctrl+x:\n%s", styled)
	}
	// Selected row root-2 is ready -> ctrl+x is dim.
	view.Selected = 1
	styled = strings.Join(view.RenderStyled(120, 24), "\n")
	if !strings.Contains(styled, "\x1b[2mctrl+x\x1b[0m") {
		t.Errorf("ready selection should dim ctrl+x:\n%s", styled)
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
