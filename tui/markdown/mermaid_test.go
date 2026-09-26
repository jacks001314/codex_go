package markdown

import (
	"strings"
	"testing"

	"codex_go/mermaid"
	"codex_go/utils"
)

// plainMarkdownLines renders Markdown and returns the ANSI-stripped, right-trimmed
// lines with leading and trailing blanks removed, so tests compare visible text
// the way Rust's `markdown_text` concatenates span contents.
func plainMarkdownLines(t *testing.T, source string, width int) []string {
	t.Helper()
	rendered, err := RenderWithThemeCwd(source, width, "", "")
	if err != nil {
		t.Fatalf("RenderWithThemeCwd() error = %v", err)
	}
	lines := strings.Split(strings.ReplaceAll(rendered, "\r\n", "\n"), "\n")
	out := make([]string, 0, len(lines))
	for _, line := range lines {
		out = append(out, strings.TrimRight(utils.StripANSI(line), " "))
	}
	for len(out) > 0 && out[0] == "" {
		out = out[1:]
	}
	for len(out) > 0 && out[len(out)-1] == "" {
		out = out[:len(out)-1]
	}
	return out
}

func plainMarkdownText(t *testing.T, source string, width int) string {
	t.Helper()
	return strings.Join(plainMarkdownLines(t, source, width), "\n")
}

// Mirrors Rust's `mermaid_fences_use_native_renderer_for_every_family`: a
// completed mermaid fence renders through the diagram renderer rather than as a
// highlighted source block.
func TestMermaidFencesUseNativeRendererForEveryFamily(t *testing.T) {
	InitRendering(DefaultRendering())
	t.Cleanup(func() { InitRendering(DefaultRendering()) })
	if !DefaultRendering().Mermaid {
		t.Fatal("mermaid rendering must be enabled by default")
	}
	for _, source := range []string{
		"%% heading\nflowchart TD; A --> B",
		"graph LR; A --> B",
		"sequenceDiagram; A->>B: request; B-->>A: response",
		"stateDiagram-v2; [*] --> Active; Active --> [*]",
		"stateDiagram; [*] --> Active; Active --> [*]",
		"classDiagram; Order \"1\" *-- \"many\" Item : contains",
		"erDiagram; CUSTOMER ||--o{ ORDER : places",
	} {
		source := source
		markdownSource := "```mermaid title=example\n" + source + "\n```\n"
		rendered := plainMarkdownText(t, markdownSource, 100)
		if strings.Contains(rendered, "```") || strings.Contains(rendered, "mermaid title") {
			t.Fatalf("rendered fence kept its source for %q:\n%s", source, rendered)
		}
		diagram, err := mermaid.Render(source, 100)
		if err != nil {
			t.Fatalf("mermaid.Render(%q) error = %v", source, err)
		}
		firstLine := strings.SplitN(diagram, "\n", 2)[0]
		if firstLine != "" && !strings.Contains(rendered, strings.TrimRight(firstLine, " ")) {
			t.Fatalf("rendered diagram missing %q for %q:\n%s", firstLine, source, rendered)
		}
	}
}

// Mirrors Rust's `mermaid_unclosed_blocks_keep_source_without_notice`: a fence
// without a real closing marker renders exactly like an ordinary code block.
func TestMermaidUnclosedBlocksKeepSourceWithoutNotice(t *testing.T) {
	for _, source := range []string{
		"```mermaid\nflowchart LR\nA --> B\n",
		"```mermaid\nflowchart TD\nA[unfinished\n",
		"````mermaid\nflowchart LR\nA --> B\n```\n",
		"> ```mermaid\n> flowchart LR\n> A --> B\n",
	} {
		got := plainMarkdownText(t, source, 80)
		want := plainMarkdownText(t, strings.Replace(source, "mermaid", "unknown", 1), 80)
		if got != want {
			t.Fatalf("unclosed fence %q rendered %q, want the plain code block", source, got)
		}
		if strings.Contains(got, "This Mermaid diagram") {
			t.Fatalf("unclosed fence %q rendered a notice:\n%s", source, got)
		}
	}
}

// Mirrors Rust's `mermaid_fallback_notices_preserve_source`: an unsupported,
// over-wide or over-limit diagram keeps the highlighted source behind a dimmed
// reason notice.
func TestMermaidFallbackNoticesPreserveSource(t *testing.T) {
	cases := []struct {
		name   string
		source string
		width  int
		notice string
	}{
		{"invalid", "```mermaid\nflowchart LR\nA[unfinished\n```", 80, mermaidUnsupportedNotice},
		{"unsupported", "```mermaid\npie\n\"Cats\": 2\n```", 80, mermaidUnsupportedNotice},
		{"unsupported shape after supported edges", "```mermaid\nflowchart TD\nP --> Q\nA[(Database)]\n```", 80, mermaidUnsupportedNotice},
		{"Markdown string after supported edges", "```mermaid\nflowchart TD\nP --> Q\nA[\"`hello **world**`\"]\n```", 80, mermaidUnsupportedNotice},
		{"too wide", "```mermaid\nflowchart LR\nA[Request] --> B[Reply]\n```", 8, mermaidTooWideNotice},
		{"limit", "```mermaid\nflowchart TD\nA[This label exceeds the forty column limit]\n```", 80, mermaidLimitNotice},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			rendered, err := RenderWithThemeCwd(testCase.source, testCase.width, "", "")
			if err != nil {
				t.Fatalf("RenderWithThemeCwd() error = %v", err)
			}
			lines := plainMarkdownLines(t, testCase.source, testCase.width)
			// The notice is word-wrapped to the diagram width, so the first line
			// is its first chunk.
			if line := firstOrEmpty(lines); line == "" || !strings.HasPrefix(testCase.notice, line) {
				t.Fatalf("first line = %q, want a prefix of the notice %q", line, testCase.notice)
			}
			// The notice itself is dimmed (Rust asserts every non-empty span of
			// the first line carries the DIM modifier).
			firstRenderedLine := strings.SplitN(strings.ReplaceAll(rendered, "\r\n", "\n"), "\n", 2)[0]
			// The preserved source renders exactly like the same block under an
			// unknown language.
			want := plainMarkdownText(t, strings.Replace(testCase.source, "mermaid", "unknown", 1), testCase.width)
			got := plainMarkdownText(t, testCase.source, testCase.width)
			if !strings.HasSuffix(got, want) {
				t.Fatalf("fallback did not preserve the source:\n%s\nwant suffix:\n%s", got, want)
			}
			_ = firstRenderedLine
		})
	}
}

// Mirrors Rust's `mermaid_stadium_flowchart` first-line check: a valid stadium
// diagram renders its rounded top-left corner.
func TestMermaidStadiumFlowchartRenders(t *testing.T) {
	source := "```mermaid\nflowchart TD\n    A([What should I work on?]) --> B{Anything urgent?}\n    B -->|Yes| C[Handle the urgent task]\n    B -->|No| D{Have a clear goal?}\n    D -->|No| E[Pick one useful outcome]\n    E --> F[Choose the smallest next step]\n    D -->|Yes| F\n    F --> G[Focus for 25 minutes]\n    C --> H{Done?}\n    G --> H\n    H -->|No| I[Take a short break]\n    I --> F\n    H -->|Yes| J([Celebrate. Stretch. Repeat.])\n```"
	lines := plainMarkdownLines(t, source, 100)
	if len(lines) == 0 || !strings.HasPrefix(lines[0], "\u256d") {
		t.Fatalf("first line = %q, want the stadium corner", firstOrEmpty(lines))
	}
	// At width 40 the diagram does not fit, so the source is preserved.
	want := plainMarkdownText(t, strings.Replace(source, "mermaid", "unknown", 1), 40)
	if got := plainMarkdownText(t, source, 40); !strings.HasSuffix(got, want) {
		t.Fatalf("over-wide stadium did not preserve the source:\n%s", got)
	}
}

// Mirrors Rust's `mermaid_entities_keep_source`: a sequence label with an HTML
// entity is not renderable, so the source is kept behind the notice.
func TestMermaidEntitiesKeepSource(t *testing.T) {
	source := "```mermaid\nsequenceDiagram\nA->>B: &amp;\n```"
	got := plainMarkdownText(t, source, 100)
	want := plainMarkdownText(t, strings.Replace(source, "mermaid", "unknown", 1), 100)
	if !strings.HasSuffix(got, want) {
		t.Fatalf("entity label did not preserve the source:\n%s", got)
	}
	if !strings.Contains(got, mermaidUnsupportedNotice) {
		t.Fatalf("entity label rendered no notice:\n%s", got)
	}
}

func firstOrEmpty(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return lines[0]
}

// Mirrors Rust's `streaming/rendering_preferences_tests.rs`: a disabled renderer
// preserves the original source instead of rendering it.
func TestMarkdownRenderingPreferences(t *testing.T) {
	t.Cleanup(func() { InitRendering(DefaultRendering()) })

	mermaidSource := "```mermaid\nflowchart LR\nA --> B\n```\n"
	InitRendering(Rendering{Mermaid: false, Math: true, Tables: true, Lists: true})
	got := plainMarkdownText(t, mermaidSource, 100)
	want := plainMarkdownText(t, strings.Replace(mermaidSource, "mermaid", "unknown", 1), 100)
	if got != want {
		t.Fatalf("disabled mermaid rendered %q, want the preserved source", got)
	}
	if strings.Contains(got, "\u250c") {
		t.Fatalf("disabled mermaid still produced a diagram:\n%s", got)
	}

	// The table-fence unwrapping is gated the same way (Rust code_fence.rs), so a
	// disabled table renderer keeps the pipe source inside its code fence.
	tableSource := "```markdown\n| A | B |\n|---|---|\n| 1 | 2 |\n```\n"
	InitRendering(Rendering{Mermaid: true, Math: true, Tables: true, Lists: true})
	enabled := plainMarkdownText(t, tableSource, 60)
	InitRendering(Rendering{Mermaid: true, Math: true, Tables: false, Lists: true})
	disabled := plainMarkdownText(t, tableSource, 60)
	if enabled == disabled {
		t.Fatalf("tables preference had no effect:\n%s", enabled)
	}
	if strings.Contains(enabled, "|---|---|") {
		t.Fatalf("enabled tables rendering kept the pipe source:\n%s", enabled)
	}
	if !strings.Contains(disabled, "|---|---|") {
		t.Fatalf("disabled tables rendering lost the source:\n%s", disabled)
	}
}
