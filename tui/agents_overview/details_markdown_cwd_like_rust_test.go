package agentsoverview

import (
	"strings"
	"testing"
)

// detailsMarkdownCall records one task-details markdown render request.
type detailsMarkdownCall struct {
	text  string
	width int
	cwd   string
}

// TestDetailsMarkdownUsesTaskCWDLikeRust covers Rust #44752's render inputs:
// both the last message and the prompt render at the details pane width with
// the task's own working directory, so relative links resolve like they do in
// the thread.
func TestDetailsMarkdownUsesTaskCWDLikeRust(t *testing.T) {
	view := New([]Row{{
		ThreadID:    "t-1",
		CWD:         "/work/alpha",
		Preview:     "see [notes](notes.md)",
		LastMessage: "see [answer](answer.md)",
		Group:       GroupWorking,
	}}, "", false)
	var calls []detailsMarkdownCall
	view.RenderMarkdownCWD = func(text string, width int, cwd string) []string {
		calls = append(calls, detailsMarkdownCall{text: text, width: width, cwd: cwd})
		return []string{text}
	}
	_ = view.RenderStyled(140, 40)
	if len(calls) != 2 {
		t.Fatalf("markdown render calls = %#v, want the last message and the prompt", calls)
	}
	// The details pane renders the last message before the prompt, like Rust.
	if calls[0].text != "see [answer](answer.md)" || calls[1].text != "see [notes](notes.md)" {
		t.Fatalf("render order = %#v", calls)
	}
	for _, call := range calls {
		if call.cwd != "/work/alpha" {
			t.Fatalf("render cwd = %q for %q, want the task's working directory", call.cwd, call.text)
		}
		if call.width != 38 {
			t.Fatalf("render width = %d for %q, want the details pane width", call.width, call.text)
		}
	}
}

// TestDetailsMarkdownFallsBackToModelRendererLikeRust covers the fallback: a
// cwd-aware renderer that declines keeps the existing model-level renderer.
func TestDetailsMarkdownFallsBackToModelRendererLikeRust(t *testing.T) {
	view := New([]Row{{
		ThreadID: "t-1",
		CWD:      "/work/alpha",
		Preview:  "plain prompt",
		Group:    GroupWorking,
	}}, "", false)
	view.RenderMarkdownCWD = func(string, int, string) []string { return nil }
	view.RenderMarkdown = func(text string, width int) []string {
		return []string{"fallback:" + text}
	}
	joined := strings.Join(view.Render(140, 40), "\n")
	if !strings.Contains(joined, "fallback:plain prompt") {
		t.Fatalf("details pane did not fall back to the model renderer:\n%s", joined)
	}
}

// TestDetailsPromptKeepsTwoRenderedLinesLikeRust covers the prompt line budget:
// markdown rendering happens first, then the preview keeps two lines and marks
// the truncation with an ellipsis.
func TestDetailsPromptKeepsTwoRenderedLinesLikeRust(t *testing.T) {
	view := New([]Row{{
		ThreadID: "t-1",
		CWD:      "/work/alpha",
		Preview:  "# Heading\n\nfirst paragraph\n\nsecond paragraph",
		Group:    GroupWorking,
	}}, "", false)
	view.RenderMarkdown = func(string, int) []string {
		return []string{"Heading", "first paragraph", "second paragraph"}
	}
	section := detailsSection(t, view.Render(140, 40), "Prompt")
	if len(section) != 2 || section[0] != "Heading" || section[1] != "\u2026" {
		t.Fatalf("prompt section = %#v, want [Heading, …]", section)
	}
}

// TestDetailsLastMessageIsNotLineLimitedLikeRust covers the asymmetric budget:
// only the prompt is cut to two lines; the last message keeps every rendered
// line.
func TestDetailsLastMessageIsNotLineLimitedLikeRust(t *testing.T) {
	view := New([]Row{{
		ThreadID:    "t-1",
		CWD:         "/work/alpha",
		LastMessage: "line one\n\nline two\n\nline three",
		Group:       GroupWorking,
	}}, "", false)
	view.RenderMarkdown = func(string, int) []string {
		return []string{"line one", "", "line two", "", "line three"}
	}
	joined := strings.Join(view.Render(140, 40), "\n")
	for _, want := range []string{"line one", "line two", "line three"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("details pane dropped %q:\n%s", want, joined)
		}
	}
	if strings.Contains(joined, "\u2026\n") {
		t.Fatalf("last message must not be ellipsis-truncated:\n%s", joined)
	}
}

// detailsSection returns the non-blank lines following a labelled section.
func detailsSection(t *testing.T, lines []string, label string) []string {
	t.Helper()
	index := -1
	for i, line := range lines {
		if strings.TrimSpace(line) == label {
			index = i
			break
		}
	}
	if index < 0 {
		t.Fatalf("section %q missing:\n%s", label, strings.Join(lines, "\n"))
	}
	var section []string
	for _, line := range lines[index+1:] {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			break
		}
		section = append(section, trimmed)
	}
	return section
}
