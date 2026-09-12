package bottompane

import (
	"strings"
	"testing"
)

func TestStatusLineRenderStyledUsesAccentAndFallbackColors(t *testing.T) {
	line, ok := StatusLineFromSegments([]StatusLineSegment{
		{Item: StatusLineModelName, Text: "gpt-5"},
		{Item: StatusLineCurrentDir, Text: "/repo"},
		{Item: StatusLineGitBranch, Text: "main"},
	}, true)
	if !ok {
		t.Fatal("status line was not rendered")
	}
	styled := line.RenderStyled("", nil, false)
	if line.PlainText() != "gpt-5 \u00b7 /repo \u00b7 main" {
		t.Fatalf("plain text = %q", line.PlainText())
	}
	// Themed spans use the fallback palette: model cyan, path green, branch magenta.
	for _, want := range []string{"\x1b[36mgpt-5\x1b[0m", "\x1b[32m/repo\x1b[0m", "\x1b[35mmain\x1b[0m"} {
		if !strings.Contains(styled, want) {
			t.Fatalf("styled = %q, missing %q", styled, want)
		}
	}
	// Separators stay dim and unstyled by accents.
	if !strings.Contains(styled, "\x1b[2m \u00b7 \x1b[0m") {
		t.Fatalf("styled = %q, missing dim separator", styled)
	}
}

func TestStatusLineRenderStyledPrefersThemeAndThreadColors(t *testing.T) {
	line, ok := StatusLineFromSegments([]StatusLineSegment{
		{Item: StatusLineThreadTitle, Text: "Task"},
		{Item: StatusLineModelName, Text: "gpt-5"},
		{Item: StatusLinePullRequestNumber, Text: "PR #1"},
	}, true)
	if !ok {
		t.Fatal("status line was not rendered")
	}
	styled := line.RenderStyled("\x1b[38;2;1;2;3m", func(accent StatusLineAccent) string {
		if accent == StatusLineAccentModel {
			return "\x1b[38;2;9;9;9m"
		}
		return ""
	}, false)
	if !strings.Contains(styled, "\x1b[38;2;1;2;3mTask\x1b[0m") {
		t.Fatalf("styled = %q, missing thread identity color", styled)
	}
	if !strings.Contains(styled, "\x1b[38;2;9;9;9mgpt-5\x1b[0m") {
		t.Fatalf("styled = %q, missing resolved theme color", styled)
	}
	// The PR number keeps the link underline alongside the branch accent.
	if !strings.Contains(styled, "\x1b[35m\x1b[4mPR #1\x1b[0m") {
		t.Fatalf("styled = %q, missing underlined PR number", styled)
	}
}

func TestStatusLineRenderStyledDimsWhenThemeColorsDisabled(t *testing.T) {
	line, ok := StatusLineFromSegments([]StatusLineSegment{
		{Item: StatusLineModelName, Text: "gpt-5"},
	}, false)
	if !ok {
		t.Fatal("status line was not rendered")
	}
	styled := line.RenderStyled("\x1b[38;2;1;2;3m", func(StatusLineAccent) string {
		return "\x1b[38;2;9;9;9m"
	}, false)
	if styled != "\x1b[2mgpt-5\x1b[0m" {
		t.Fatalf("styled = %q, want dim plain text", styled)
	}
}
