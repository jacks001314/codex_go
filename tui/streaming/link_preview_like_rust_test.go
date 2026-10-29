package streaming

import (
	"strings"
	"testing"
)

// Rust #47589: an unfinished link destination never expands the rich preview;
// only the label stays visible until the closing parenthesis arrives.
func TestStreamingPreviewHidesUnfinishedLinkDestinationsLikeRust(t *testing.T) {
	for _, destination := range []string{
		"https://example.com/a/very/long/path/to/the/guide",
		"https://example.com/a_(nested_(path))",
		`https://example.com/escaped\)parenthesis`,
		"<https://example.com/unbalanced)parenthesis>",
		" <https://example.com/unbalanced)parenthesis>",
		`https://example.com/guide "a title with ) parentheses"`,
		"https://example.com/guide (x < y)",
	} {
		controller := NewStreamController(24)
		controller.Push("See [documentation]")
		label := strings.Join(controller.CurrentTailLines(), "\n")
		if !strings.Contains(label, "documentation") {
			t.Fatalf("label preview = %q", label)
		}
		for _, r := range "(" + destination {
			controller.Push(string(r))
			if got := strings.Join(controller.CurrentTailLines(), "\n"); got != label {
				t.Fatalf("destination %q exposed %q in preview %q", destination, string(r), got)
			}
		}
		controller.Push(") and continue.")
		closed := NewStreamController(24)
		closed.Push("See [documentation](" + destination + ") and continue.")
		if got, want := strings.Join(controller.CurrentTailLines(), "\n"), strings.Join(closed.CurrentTailLines(), "\n"); got != want {
			t.Fatalf("closed link preview = %q, want %q", got, want)
		}
	}
	// The preview byte bound must not drop the opener and expose a long URL tail.
	controller := NewStreamController(24)
	controller.Push("See [documentation]")
	label := strings.Join(controller.CurrentTailLines(), "\n")
	controller.Push("(https://example.com/" + strings.Repeat("界", 3000))
	controller.Push("more-path")
	if got := strings.Join(controller.CurrentTailLines(), "\n"); got != label {
		t.Fatalf("bounded preview exposed the destination: %q", got)
	}
}

// Rust #47589: newline commitment and finalization still render the original
// source, and a resize keeps the label-only preview.
func TestStreamingUnfinishedLinkFlushesOnNewlineLikeRust(t *testing.T) {
	source := "See [documentation](https://example.com/a/very/long/path"
	controller := NewStreamController(24)
	controller.Push(source)
	if got := strings.Join(controller.CurrentTailLines(), "\n"); got != "See [documentation]" {
		t.Fatalf("preview = %q", got)
	}
	controller.SetWidth(32)
	if got := strings.Join(controller.CurrentTailLines(), "\n"); got != "See [documentation]" {
		t.Fatalf("resized preview = %q", got)
	}
	controller.Push("\n")
	if lines := controller.CurrentTailLines(); len(lines) != 0 {
		t.Fatalf("tail after newline = %#v", lines)
	}
	var committed []string
	for {
		cell, idle := controller.OnCommitTickBatch(1 << 20)
		if cell != nil {
			committed = append(committed, cell.RawLines()...)
		}
		if cell == nil || idle {
			break
		}
	}
	displayed := strings.Join(committed, "\n")
	if !strings.Contains(displayed, "See") || !strings.Contains(displayed, "documentation") {
		t.Fatalf("committed line = %q", displayed)
	}
}

// Rust #47589: escapes, inline code and complete links are not held back.
func TestStreamingPreviewWithoutLinkHoldbackLikeRust(t *testing.T) {
	for _, source := range []string{
		`See \[label](https://example.com/unfinished`,
		`See [label\](https://example.com/unfinished`,
		"See `[label](https://example.com/unfinished`",
		"See ``[la`bel](https://example.com/unfinished``",
	} {
		controller := NewStreamController(60)
		controller.Push(source)
		if got := strings.Join(controller.CurrentTailLines(), "\n"); !strings.Contains(got, "https://example.com") {
			t.Fatalf("%q preview = %q", source, got)
		}
	}
	controller := NewStreamController(60)
	controller.Push("See [label](https://example.com/complete) and prose")
	if got := strings.Join(controller.CurrentTailLines(), "\n"); !strings.Contains(got, "and prose") {
		t.Fatalf("complete link preview = %q", got)
	}
}
