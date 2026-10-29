package streaming

import (
	"strings"
	"testing"
	"time"
)

// TestStreamControllerReleasesCompletedTableIntoScrollbackLikeRust mirrors
// Rust #50207: a confirmed table enters the stable queue once its containing
// top-level block is stable, while a later table keeps the tail mutable.
func TestStreamControllerReleasesCompletedTableIntoScrollbackLikeRust(t *testing.T) {
	controller := NewStreamController(80)
	controller.core.now = func() time.Time { return time.Unix(0, 0) }

	controller.Push("Before table.\n")
	if cell, _ := controller.OnCommitTick(); cell == nil {
		t.Fatal("pre-table line should commit independently")
	}

	controller.Push("| first | row |\n\nAfter table.\n\n| Next | Table |\n| --- | --- |\n| second | row |\n")
	cell, idle := controller.OnCommitTickBatch(1000)
	if cell == nil {
		t.Fatal("completed table and prose should enter scrollback")
	}
	if !idle {
		t.Fatal("released lines should drain in one batch")
	}
	committed := strings.Join(cell.RawLines(), "\n")
	if !strings.Contains(committed, "| first | row |") || !strings.Contains(committed, "After table.") {
		t.Fatalf("released cell = %q", committed)
	}
	if !controller.HasLiveTail() {
		t.Fatal("the later table must stay mutable")
	}
	tail := strings.Join(controller.CurrentTailLines(), "\n")
	if !strings.Contains(tail, "| Next | Table |") {
		t.Fatalf("live tail lost the later table: %q", tail)
	}
	if strings.Contains(tail, "first") {
		t.Fatalf("live tail kept the released table: %q", tail)
	}
}

// TestStreamControllerNeedsScrollbackReflowForLateReferenceDefinitionLikeRust
// mirrors Rust #50207: a definition arriving after the table was released still
// forces a completion reflow, with and without a trailing newline.
func TestStreamControllerNeedsScrollbackReflowForLateReferenceDefinitionLikeRust(t *testing.T) {
	for _, definition := range []string{
		"[ref]: https://example.com\n",
		"[ref]: https://example.com",
	} {
		controller := NewStreamController(80)
		controller.core.now = func() time.Time { return time.Unix(0, 0) }
		committed := []string{}
		for _, delta := range []string{
			"| Key | Value |\n| --- | --- |\n| [A][ref] | row |\n",
			"\nAfter table.\n\n",
			definition,
		} {
			controller.Push(delta)
			if cell, _ := controller.OnCommitTickBatch(1000); cell != nil {
				committed = append(committed, cell.RawLines()...)
			}
		}
		joined := strings.Join(committed, "\n")
		if !strings.Contains(joined, "[A][ref]") {
			t.Fatalf("definition %q: committed = %q", definition, joined)
		}
		if controller.HasLiveTail() {
			t.Fatalf("definition %q: the completed table should be released", definition)
		}
		if !controller.NeedsScrollbackReflow() {
			t.Fatalf("definition %q: late definitions must repair scrollback", definition)
		}
		_, source := controller.Finalize()
		if !strings.Contains(source, definition) {
			t.Fatalf("definition %q missing from finalized source %q", definition, source)
		}
	}
}

// TestStreamControllerNeedsScrollbackReflowFalseForPlainProse pins the negative
// case: unterminated prose without a live tail or definition needs no reflow.
func TestStreamControllerNeedsScrollbackReflowFalseForPlainProse(t *testing.T) {
	controller := NewStreamController(80)
	controller.Push("Plain prose without newline")
	if controller.NeedsScrollbackReflow() {
		t.Fatal("plain prose should not require a scrollback reflow")
	}
}
