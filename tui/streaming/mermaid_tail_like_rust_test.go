package streaming

import (
	"strings"
	"testing"
)

// Mirrors Rust #46054: a streamed mermaid diagram stays in the mutable tail
// until another top-level block starts, so the closing fence and a resize can
// update it; afterwards it enters scrollback.
func TestStreamedMermaidStaysMutableUntilNextTopLevelBlockLikeRust(t *testing.T) {
	controller := NewStreamController(80)
	controller.Push("intro\n\n")
	controller.Push("```mermaid\n")
	controller.Push("flowchart TD; A --> B\n")

	start, held := controller.core.activeTailSourceStart()
	want := len("intro\n\n")
	if !held || start != want {
		t.Fatalf("activeTailSourceStart() = (%d, %v), want (%d, true)", start, held, want)
	}
	if start != strings.Index(controller.core.rawSource, "```mermaid") {
		t.Fatalf("held tail starts at %d, want the fence opener %d", start, strings.Index(controller.core.rawSource, "```mermaid"))
	}

	// The closing fence keeps the block mutable; a resize re-renders it.
	controller.Push("```\n")
	if start, held := controller.core.activeTailSourceStart(); !held || start != want {
		t.Fatalf("after closing fence activeTailSourceStart() = (%d, %v), want (%d, true)", start, held, want)
	}
	controller.SetWidth(40)
	controller.SetWidth(80)

	// A following top-level block releases the diagram into scrollback.
	controller.Push("\nafter the diagram\n")
	if start, held := controller.core.activeTailSourceStart(); held && start <= want {
		t.Fatalf("diagram still held after the next block at %d", start)
	}
	if !strings.Contains(controller.core.rawSource, "```mermaid") {
		t.Fatal("raw source lost the original Mermaid stream")
	}
}

// The holdback is derived from the committed source, so the rendered lines for
// the diagram are recomputed at each width instead of being frozen.
func TestStreamedMermaidRecomputesOnResizeLikeRust(t *testing.T) {
	controller := NewStreamControllerWithTheme(80, mathStreamTheme)
	controller.Push("```mermaid\nflowchart LR; A --> B\n```\n")
	wide := strings.Join(trimmedTail(controller.CurrentTailLines()), "\n")
	controller.SetWidth(24)
	narrow := strings.Join(trimmedTail(controller.CurrentTailLines()), "\n")
	if wide == "" || narrow == "" {
		t.Fatalf("tail widths: wide=%q narrow=%q", wide, narrow)
	}
	// Both widths render the diagram (node names survive) rather than dropping
	// the block or leaking the fence markers.
	for _, rendered := range []string{wide, narrow} {
		if !strings.Contains(rendered, "A") || !strings.Contains(rendered, "B") {
			t.Fatalf("diagram lost node labels: %q", rendered)
		}
		if strings.Contains(rendered, "```") {
			t.Fatalf("diagram leaked fence markers: %q", rendered)
		}
	}
	if _, stillHeld := controller.core.activeTailSourceStart(); !stillHeld {
		t.Fatal("closed diagram must stay mutable while its block is last")
	}
}
