package mermaid

import (
	"fmt"
	"strings"
	"testing"
)

// Mirrors Rust #62550 `multiline_edge_labels_in_every_direction`: `<br>`,
// `<br/>` and `<br />` split flowchart edge labels into rendered lines, the
// fixtures below are the Rust snapshot for
// `codex-rs/mermaid/src/snapshots/codex_mermaid__tests__multiline_edge_labels_in_every_direction.snap`.
func TestMultilineEdgeLabelsInEveryDirectionLikeRust(t *testing.T) {
	const template = "flowchart %s; A[Source] -->|\"one<br/>two\"| B[Target]; A -. retry<br />later .-> C[Other]; C -->|x<br>y| A"
	var cases []string
	for _, direction := range []string{"TD", "BT", "LR", "RL"} {
		source := fmt.Sprintf(template, direction)
		rendered, err := Render(source, 100)
		if err != nil {
			t.Fatalf("%s: Render error = %v", direction, err)
		}
		width := 0
		for _, line := range strings.Split(rendered, "\n") {
			if w := textWidth(line); w > width {
				width = w
			}
		}
		if got, err := Render(source, width); err != nil || got != rendered {
			t.Fatalf("%s: Render(width) = (%q, %v), want the same output", direction, got, err)
		}
		if _, err := Render(source, width-1); err != ErrTooWide {
			t.Fatalf("%s: Render(width-1) error = %v, want ErrTooWide", direction, err)
		}
		cases = append(cases, direction+"\n"+rendered)
	}
	const expected = `TD
┌────────┐
│ Source │
│        ├──one─────┐
│        │  two     │
│        ├─┄retry┄┄┄╪┄┐
│        │  later   │ ┆
│        ├◄─────────╪─╪─┐
└────────┘          │ ┆ │
                    │ ┆ │
                    │ ┆ │
┌────────┐          │ ┆ │
│ Target │          │ ┆ │
│        ├◄─────────┘ ┆ │
└────────┘            ┆ │
                      ┆ │
                      ┆ │
┌────────┐            ┆ │
│ Other  │            ┆ │
│        ├◄┄┄┄┄┄┄┄┄┄┄┄┘ │
│        ├──x───────────┘
│        │  y
└────────┘

BT
┌────────┐
│ Other  │
│        ├◄┄┄┄┄┄┄┄┄┄┄┄┐
│        ├──x─────────╪─┐
│        │  y         ┆ │
└────────┘            ┆ │
                      ┆ │
                      ┆ │
┌────────┐            ┆ │
│ Target │            ┆ │
│        ├◄─────────┐ ┆ │
└────────┘          │ ┆ │
                    │ ┆ │
                    │ ┆ │
┌────────┐          │ ┆ │
│ Source │          │ ┆ │
│        ├──one─────┘ ┆ │
│        │  two       ┆ │
│        ├─┄retry┄┄┄┄┄┘ │
│        │  later       │
│        ├◄─────────────┘
└────────┘

LR
┌─────────────────────┐  ┌────────┐  ┌──────────────┐
│ Source              │  │ Target │  │ Other        │
└┬──────┬──────┬──────┘  └┬───────┘  └┬──────┬──────┘
 │      │      ▲          ▲           ▲      │
 │one   ┆retry │          │           ┆      │x
 │two   ┆later │          │           ┆      │y
 │      ┆      │          │           ┆      │
 │      ┆      │          │           ┆      │
 └──────╪──────╪──────────┘           ┆      │
        ┆      │                      ┆      │
        └┄┄┄┄┄┄╪┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┘      │
               │                             │
               └─────────────────────────────┘

RL
┌──────────────┐  ┌────────┐  ┌─────────────────────┐
│ Other        │  │ Target │  │ Source              │
└┬──────┬──────┘  └┬───────┘  └┬──────┬──────┬──────┘
 ▲      │          ▲           │      │      ▲
 ┆      │x         │           │one   ┆retry │
 ┆      │y         │           │two   ┆later │
 ┆      │          │           │      ┆      │
 ┆      │          │           │      ┆      │
 ┆      │          └───────────┘      ┆      │
 ┆      │                             ┆      │
 └┄┄┄┄┄┄╪┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┄┘      │
        │                                    │
        └────────────────────────────────────┘`
	if got := strings.Join(cases, "\n\n"); got != expected {
		t.Fatalf("rendered directions do not match the Rust snapshot:\n%s", got)
	}
}

// Mirrors the Rust #62550 blank-line vectors: a whitespace-only line reserves
// space but renders exactly like an empty line, and the pipe and spaced infix
// spellings of an edge label agree.
func TestMultilineEdgeLabelsBlankLinesLikeRust(t *testing.T) {
	for _, testCase := range [][2]string{
		{"one<br> <br>two", "one<br><br>two"},
		{"   <br>go", "<br>go"},
	} {
		withSpaces, err := Render(fmt.Sprintf("flowchart TD; A -->|%s| B", testCase[0]), 100)
		if err != nil {
			t.Fatalf("Render(%q) error = %v", testCase[0], err)
		}
		empty, err := Render(fmt.Sprintf("flowchart TD; A -->|%s| B", testCase[1]), 100)
		if err != nil {
			t.Fatalf("Render(%q) error = %v", testCase[1], err)
		}
		if withSpaces != empty {
			t.Fatalf("blank line with spaces = %q, want %q", withSpaces, empty)
		}
	}
	pipe, err := Render("flowchart TD; A -->|one<br/>two| B", 100)
	if err != nil {
		t.Fatalf("Render(pipe) error = %v", err)
	}
	infix, err := Render("flowchart TD; A -- one<br/>two --> B", 100)
	if err != nil {
		t.Fatalf("Render(infix) error = %v", err)
	}
	if pipe != infix {
		t.Fatalf("pipe form = %q, want the infix form %q", pipe, infix)
	}
}

// Mirrors Rust #62550 limit vectors: the 40-display-cell bound applies to each
// rendered edge-label line, and a multiline edge label still reports TooWide.
func TestMultilineEdgeLabelsLimitsLikeRust(t *testing.T) {
	for _, source := range []string{
		fmt.Sprintf("graph TD; A -->|\"ok<br />%s\"| B", strings.Repeat("\u754c", 21)),
		fmt.Sprintf("graph TD; A -->|\"one<br>%s<br>two\"| B", strings.Repeat(" ", 41)),
	} {
		if _, err := Render(source, 200); err != ErrLimit {
			t.Fatalf("Render(over-long edge label) error = %v, want ErrLimit", err)
		}
	}
	source := fmt.Sprintf("graph LR; A -->|\"%s<br/>%s\"| B", strings.Repeat("\u754c", 20), strings.Repeat("x", 40))
	output, err := Render(source, 100)
	if err != nil {
		t.Fatalf("Render(multiline edge label) error = %v", err)
	}
	width := 0
	for _, line := range strings.Split(output, "\n") {
		if w := textWidth(line); w > width {
			width = w
		}
	}
	if got, err := Render(source, width); err != nil || got != output {
		t.Fatalf("Render(width) = (%q, %v), want the same output", got, err)
	}
	if _, err := Render(source, width-1); err != ErrTooWide {
		t.Fatalf("Render(width-1) error = %v, want ErrTooWide", err)
	}
}
