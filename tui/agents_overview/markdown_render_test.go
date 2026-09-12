package agentsoverview

import (
	"strings"
	"testing"
)

// TestTaskDetailsUsesInjectedMarkdownRenderer covers Rust #44752's styled
// prompt preview: the injected renderer supplies styled, width-wrapped lines
// that RenderStyled emits verbatim and Render strips back to plain text.
func TestTaskDetailsUsesInjectedMarkdownRenderer(t *testing.T) {
	view := &View{
		Rows:     []Row{{ThreadID: "thread-1", Preview: "**bold** text", Group: GroupWorking}},
		Selected: 0,
	}
	var sawInput string
	view.RenderMarkdown = func(text string, width int) []string {
		sawInput = text
		return []string{"\x1b[1mbold\x1b[0m text"}
	}

	styled := strings.Join(view.RenderStyled(120, 40), "\n")
	if sawInput != "**bold** text" {
		t.Fatalf("markdown input = %q", sawInput)
	}
	if !strings.Contains(styled, "\x1b[1mbold\x1b[0m text") {
		t.Fatalf("styled render missing markdown styling:\n%s", styled)
	}

	plain := strings.Join(view.Render(120, 40), "\n")
	if strings.Contains(plain, "\x1b[1m") {
		t.Fatalf("plain render leaked markdown styling:\n%s", plain)
	}
	if !strings.Contains(plain, "bold text") {
		t.Fatalf("plain render missing markdown text:\n%s", plain)
	}
}

// TestTaskDetailsMarkdownFallsBackToPlainPreview ensures a renderer that
// declines (or is absent) keeps the existing plain two-line preview.
func TestTaskDetailsMarkdownFallsBackToPlainPreview(t *testing.T) {
	view := &View{
		Rows:     []Row{{ThreadID: "thread-1", Preview: "alpha beta", Group: GroupWorking}},
		Selected: 0,
	}
	view.RenderMarkdown = func(string, int) []string { return nil }
	lines := view.RenderStyled(120, 40)
	promptIndex := -1
	for i, line := range lines {
		if strings.Contains(line, "Prompt") {
			promptIndex = i
			break
		}
	}
	if promptIndex < 0 || promptIndex+1 >= len(lines) {
		t.Fatalf("prompt section missing:\n%s", strings.Join(lines, "\n"))
	}
	previewLine := lines[promptIndex+1]
	if strings.Contains(previewLine, "\x1b[") {
		t.Fatalf("fallback preview is styled: %q", previewLine)
	}
	if !strings.Contains(previewLine, "alpha beta") {
		t.Fatalf("fallback preview = %q, want alpha beta", previewLine)
	}
}
