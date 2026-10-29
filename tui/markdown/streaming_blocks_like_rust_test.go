package markdown

import (
	"strings"
	"testing"
)

func TestAnalyzeStreamingMarkdownLastTopLevelBlockLikeRust(t *testing.T) {
	cases := []struct {
		name          string
		source        string
		expectStartOf string
	}{
		{
			name:          "single block",
			source:        "Only paragraph.\n",
			expectStartOf: "",
		},
		{
			name:          "trailing blank line keeps the table last",
			source:        "| A | B |\n| --- | --- |\n| 1 | 2 |\n\n",
			expectStartOf: "",
		},
		{
			name:          "paragraph after table",
			source:        "| A | B |\n| --- | --- |\n| 1 | 2 |\n\nAfter table.\n",
			expectStartOf: "After table.",
		},
		{
			name:          "later table",
			source:        "Before.\n\n| Next | Table |\n| --- | --- |\n",
			expectStartOf: "| Next | Table |",
		},
		{
			name:          "list after paragraph",
			source:        "Intro.\n\n- one\n- two\n",
			expectStartOf: "- one",
		},
		{
			name:          "fenced code opener",
			source:        "Intro.\n\n```go\nfmt.Println()\n```\n",
			expectStartOf: "```go",
		},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			metadata := AnalyzeStreamingMarkdown(testCase.source)
			if testCase.expectStartOf == "" {
				if metadata.HasLastTopLevelBlock {
					t.Fatalf("unexpected boundary at %d", metadata.LastTopLevelBlockStart)
				}
				return
			}
			want := strings.Index(testCase.source, testCase.expectStartOf)
			if !metadata.HasLastTopLevelBlock || metadata.LastTopLevelBlockStart != want {
				t.Fatalf("boundary = %d ok=%v, want %d", metadata.LastTopLevelBlockStart, metadata.HasLastTopLevelBlock, want)
			}
		})
	}
}

func TestHasReferenceLinkDefinitionLikeRust(t *testing.T) {
	for _, source := range []string{
		"[ref]: https://example.com\n",
		"[A][ref]\n\n[ref]: https://example.com",
		"| [A][ref] | row |\n\n[ref]: https://example.com\n",
	} {
		if !HasReferenceLinkDefinition(source) {
			t.Fatalf("expected a reference definition in %q", source)
		}
	}
	for _, source := range []string{
		"[A][ref]\n",
		"```md\n[ref]: https://example.com\n```\n",
	} {
		if HasReferenceLinkDefinition(source) {
			t.Fatalf("unexpected reference definition in %q", source)
		}
	}
}
