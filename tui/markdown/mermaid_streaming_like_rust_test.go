package markdown

import (
	"strings"
	"testing"
)

// Mirrors Rust #46054's streaming tracker: a mermaid fence keeps its whole
// top-level block mutable while that block is the last one, including when the
// fence sits inside a list item or blockquote.
func TestMermaidFenceTailStartLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		source string
		want   int
		ok     bool
	}{
		{
			name:   "final mermaid block",
			source: "intro\n\n```mermaid\nflowchart TD; A --> B\n```\n",
			want:   len("intro\n\n"),
			ok:     true,
		},
		{
			name:   "unclosed final mermaid block",
			source: "intro\n\n```mermaid\nflowchart TD; A --> B\n",
			want:   len("intro\n\n"),
			ok:     true,
		},
		{
			name:   "mermaid inside a list keeps the list start",
			source: "1. step\n\n   ```mermaid\n   flowchart TD; A --> B\n   ```\n",
			want:   0,
			ok:     true,
		},
		{
			name:   "mermaid inside a blockquote keeps the quote start",
			source: "> ```mermaid\n> flowchart TD; A --> B\n> ```\n",
			want:   0,
			ok:     true,
		},
		{
			name:   "info string after the language still matches",
			source: "```mermaid title=example\nflowchart TD; A --> B\n```\n",
			want:   0,
			ok:     true,
		},
		{
			name:   "a later top-level block releases the diagram",
			source: "```mermaid\nflowchart TD; A --> B\n```\n\nafter\n",
		},
		{
			name:   "non-mermaid fence",
			source: "```go\nfunc main() {}\n```\n",
		},
		{
			name:   "no fences",
			source: "plain prose mentioning mermaid\n",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			start, ok := MermaidFenceTailStart(testCase.source)
			if ok != testCase.ok {
				t.Fatalf("MermaidFenceTailStart() ok = %v, want %v (start %d)", ok, testCase.ok, start)
			}
			if ok && start != testCase.want {
				t.Fatalf("start = %d, want %d for %q", start, testCase.want, testCase.source)
			}
		})
	}
	// The reported offset always begins a line, so a stable boundary can never
	// split the fence's opening marker.
	source := "intro\n\n```mermaid\nflowchart TD; A --> B\n```\n"
	start, ok := MermaidFenceTailStart(source)
	if !ok || start == 0 || start > len(source) || source[start-1] != '\n' {
		t.Fatalf("start %d does not begin a line of %q", start, source)
	}
	if !strings.HasPrefix(source[start:], "```mermaid") {
		t.Fatalf("start %d does not point at the fence: %q", start, source[start:])
	}
}
