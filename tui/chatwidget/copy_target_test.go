package chatwidget

import (
	"strings"
	"testing"
)

func TestCopyTargetsFromMarkdownExtractsWholeCodeAndQuotesLikeRust(t *testing.T) {
	markdown := "Intro\n\n```go\nfunc main() {}\n```\n\n> a quote\n> continued\n\n```text\nplain\n```"
	targets := CopyTargetsFromMarkdown(markdown)
	if len(targets) < 4 {
		t.Fatalf("expected whole + 2 code + 1 quote = 4 targets, got %d: %+v", len(targets), targets)
	}
	if targets[0].ID != CopyTargetWholeID || targets[0].Text != markdown {
		t.Fatalf("first target should be the whole response: %+v", targets[0])
	}
	if targets[1].Label != "Code block (go)" {
		t.Fatalf("expected go code block label, got %q", targets[1].Label)
	}
	if strings.Contains(targets[1].Text, "```") || targets[1].Text != "func main() {}\n" {
		t.Fatalf("go code target should be plain source: %q", targets[1].Text)
	}
	foundQuote := false
	for _, target := range targets {
		if target.Label == "Blockquote" {
			foundQuote = true
			if !strings.Contains(target.Text, "a quote") {
				t.Fatalf("blockquote text = %q", target.Text)
			}
			if strings.Contains(target.Text, "> a quote") || strings.Contains(target.Text, "> continued") {
				t.Fatalf("blockquote markers leaked into copy: %q", target.Text)
			}
		}
	}
	if !foundQuote {
		t.Fatalf("expected a blockquote target, got %+v", targets)
	}
	selected, ok := CopyTargetForID(targets, targets[1].ID)
	if !ok || selected.Text == "" {
		t.Fatalf("CopyTargetForID should find a copyable target: %+v", selected)
	}
}

func TestCopyTargetsFromMarkdownEmpty(t *testing.T) {
	if targets := CopyTargetsFromMarkdown(""); len(targets) != 0 {
		t.Fatalf("empty markdown should have no targets, got %d", len(targets))
	}
}

func TestCopyTargetPickerViewUsesTargets(t *testing.T) {
	view := NewCopyTargetPickerView("hello\n\n```go\nx\n```")
	if view.ViewID != CopyTargetPickerViewID {
		t.Fatalf("view id = %q", view.ViewID)
	}
	if len(view.Items) < 2 {
		t.Fatalf("expected at least whole + code block items, got %d", len(view.Items))
	}
	if view.Items[0].Name != "Whole response" {
		t.Fatalf("first item = %q", view.Items[0].Name)
	}
}
