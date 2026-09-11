package agentsoverview

import (
	"strings"
	"testing"
)

func TestRenderStyledUsesThreadColorsForTitles(t *testing.T) {
	view := New(sampleRows(), "", false)
	view.UseThemeColors = true
	view.ThreadColor = func(threadID string) string {
		if strings.TrimSpace(threadID) == "" {
			return ""
		}
		return "#ff8000"
	}

	styled := strings.Join(view.RenderStyled(120, 24), "\n")
	if !strings.Contains(styled, "\x1b[38;2;255;128;0m") {
		t.Fatalf("styled output missing thread title color:\n%s", styled)
	}

	// Rendering stays plain and width-stable: stripping the styles must match
	// the unstyled render exactly.
	plain := view.Render(120, 24)
	styledLines := view.RenderStyled(120, 24)
	if len(plain) != len(styledLines) {
		t.Fatalf("plain lines = %d, styled = %d", len(plain), len(styledLines))
	}
	for i := range plain {
		if got := stripANSIForTest(styledLines[i]); got != plain[i] {
			t.Fatalf("styled[%d] stripped = %q, want %q", i, got, plain[i])
		}
	}
}

func TestRenderStyledWithoutThemeColorsKeepsBoldTitles(t *testing.T) {
	view := New(sampleRows(), "", false)
	view.ThreadColor = func(string) string { return "#ff8000" }
	styled := strings.Join(view.RenderStyled(120, 24), "\n")
	if strings.Contains(styled, "\x1b[38;2;") {
		t.Fatalf("theme colors disabled but identity color emitted:\n%s", styled)
	}
}
