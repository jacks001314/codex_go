package mermaid

import (
	"strings"
	"testing"
)

// Rust #61878: flat subgraphs reserve a frame around their members, keep the
// members contiguous in declaration order, and route cross-group edges outside
// every frame.
func TestFlatSubgraphsAndCrossGroupEdgesLikeRust(t *testing.T) {
	body := `
    Z --> A
    subgraph Old["Old approach"]
      A[Input] --> B[Output]
    end
    subgraph New["New approach"]
      C[Input] --> D[Output]
    end
    B -->|compare| C
  `
	want := strings.Join([]string{
		"  ┌────────┐",
		"  │ Z      │",
		"  │        ├───────────────────┐",
		"  └────────┘                   │",
		"                               │",
		"                               │",
		"┌─Old approach──────────────┐  │",
		"│                           │  │",
		"│ ┌────────┐                │  │",
		"│ │ Input  │                │  │",
		"│ │        ├◄───────────────┼──┘",
		"│ │        ├────────────┐   │",
		"│ └────────┘            │   │",
		"│                       │   │",
		"│                       │   │",
		"│ ┌────────┐            │   │",
		"│ │ Output │            │   │",
		"│ │        ├◄───────────┘   │",
		"│ │        ├──compare───────┼────┐",
		"│ └────────┘                │    │",
		"│                           │    │",
		"└───────────────────────────┘    │",
		"                                 │",
		"                                 │",
		"┌─New approach──────────────┐    │",
		"│                           │    │",
		"│ ┌────────┐                │    │",
		"│ │ Input  │                │    │",
		"│ │        ├──────────────┐ │    │",
		"│ │        ├◄─────────────╪─┼────┘",
		"│ └────────┘              │ │",
		"│                         │ │",
		"│                         │ │",
		"│ ┌────────┐              │ │",
		"│ │ Output │              │ │",
		"│ │        ├◄─────────────┘ │",
		"│ └────────┘                │",
		"│                           │",
		"└───────────────────────────┘",
	}, "\n")
	for _, direction := range []string{"TB", "BT", "LR", "RL"} {
		source := "flowchart " + direction + "\n" + body
		output, err := Render(source, 200)
		if err != nil {
			t.Fatalf("Render(%s) error = %v", direction, err)
		}
		width := 0
		for _, line := range strings.Split(output, "\n") {
			if lineWidth := textWidth(line); lineWidth > width {
				width = lineWidth
			}
		}
		if _, err := Render(source, width-1); err != ErrTooWide {
			t.Fatalf("Render(%s, %d) error = %v, want ErrTooWide", direction, width-1, err)
		}
		if direction == "TB" && output != want {
			t.Fatalf("Render(TB) mismatch\n got:\n%s\nwant:\n%s", output, want)
		}
	}
}

// A bare title names the group when it is one word; a title containing
// whitespace gets a synthesized `subGraph<n>` id.
func TestSubgraphTitlesAndMembershipLikeRust(t *testing.T) {
	tabbed, err := Render("graph; subgraph\tSimple title; A; end", 100)
	if err != nil {
		t.Fatalf("tab title error = %v", err)
	}
	quoted, err := Render(`graph; subgraph "Simple title"; A; end`, 100)
	if err != nil {
		t.Fatalf("quoted title error = %v", err)
	}
	if tabbed != quoted {
		t.Fatalf("tab title = %q, quoted title = %q", tabbed, quoted)
	}
	for _, quoted := range []struct{ source, title string }{
		{`graph; subgraph "Old [deprecated]"; A; end`, "Old [deprecated]"},
		{`graph; subgraph "G --> H"; A; end`, "G --> H"},
	} {
		output, err := Render(quoted.source, 100)
		if err != nil {
			t.Fatalf("Render(%q) error = %v", quoted.source, err)
		}
		if !strings.Contains(output, quoted.title) {
			t.Fatalf("Render(%q) = %q, missing title", quoted.source, output)
		}
	}
	graph, err := parseFlowchart("graph", []string{
		"subgraph Old",
		"A --> C",
		"end",
		"subgraph New",
		"C[Input] --> D",
		"end",
	})
	if err != nil {
		t.Fatalf("parseFlowchart error = %v", err)
	}
	got := make([][]int, len(graph.subgraphs))
	for i, group := range graph.subgraphs {
		got[i] = group.nodes
	}
	want := [][]int{{0, 1}, {2}}
	if len(got) != len(want) {
		t.Fatalf("groups = %v, want %v", got, want)
	}
	for i := range want {
		if len(got[i]) != len(want[i]) {
			t.Fatalf("groups = %v, want %v", got, want)
		}
		for j := range want[i] {
			if got[i][j] != want[i][j] {
				t.Fatalf("groups = %v, want %v", got, want)
			}
		}
	}
}

func TestInvalidOrUnsupportedSubgraphsKeepSourceLikeRust(t *testing.T) {
	for _, source := range []string{
		"graph; end",
		"graph; subgraph X; A",
		"graph; subgraph X; end",
		"graph; subgraph X; subgraph Y; A; end; end",
		"graph; subgraph X; A; end; subgraph X; B; end",
		"graph; subgraph X; A[First]; end; subgraph Y; A[Second]; end",
		"graph; subgraph X; A; end; X --> A",
		"graph; subgraph X; direction LR; A; end",
		"graph; subgraph G --> H; A; end",
		"graph; subgraph G -.-> H; A; end",
		"graph; subgraph G ==> H; A; end",
		"graph; subgraph G ~~~ H; A; end",
		"graph; subgraph \"`**Old**`\"; A; end",
		"graph; subgraph Old approach; A; end; subGraph0 --> B",
	} {
		if _, err := Render(source, 100); err != ErrUnsupported {
			t.Fatalf("Render(%q) error = %v, want ErrUnsupported", source, err)
		}
	}
}
