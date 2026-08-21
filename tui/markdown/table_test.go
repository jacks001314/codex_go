package markdown

import (
	"strings"
	"testing"

	"codex_go/utils"
)

func TestRenderCompactTableMatchesRustFormat(t *testing.T) {
	tables, _ := detectSourceTables("| A | B |\n|---|---|\n| 1 | 2 |\n")
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}
	lines := renderCompactTable(tables[0])
	got := strings.Join(lines, "\n")
	want := " A      B\n" +
		"\u2501\u2501\u2501\u2501\u2501  \u2501\u2501\u2501\u2501\u2501\n" +
		" 1      2"
	if got != want {
		t.Fatalf("compact table mismatch\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}

func TestRenderCompactTableBodySeparator(t *testing.T) {
	tables, _ := detectSourceTables("| Name | Value |\n|---|---|\n| alpha | 1 |\n| beta | 22 |")
	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}
	lines := renderCompactTable(tables[0])
	joined := strings.Join(lines, "\n")
	// Header separator uses heavy horizontal; the inter-row separator uses light.
	if strings.Contains(joined, "\u2501") && !strings.Contains(joined, "\u2500") {
		// both present
	}
	if !strings.Contains(joined, "\u2500") {
		t.Fatalf("expected light row separator, got:\n%s", joined)
	}
	// No pipe separators remain.
	if strings.Contains(joined, "|") {
		t.Fatalf("pipe separator leaked into table:\n%s", joined)
	}
}

func TestRenderWithThemeSplicesCompactTable(t *testing.T) {
	source := "Before\n\n| Name | Value |\n|---|---|\n| alpha | 1 |\n\nAfter"
	rendered, err := RenderWithTheme(source, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	clean := utils.StripANSI(rendered)
	for _, want := range []string{"Before", "After", "Name", "alpha", "\u2501"} {
		if !strings.Contains(clean, want) {
			t.Fatalf("spliced table missing %q:\n%s", want, clean)
		}
	}
	if strings.Contains(clean, "CODEX_INTERNAL_TABLE_") {
		t.Fatalf("table marker leaked into output:\n%s", clean)
	}
}

func TestDetectSourceTablesDetectsBlockquoteSkipsIndentedCode(t *testing.T) {
	source := "> | A | B |\n> |---|---|\n> | 1 | 2 |\n\n    | C | D |\n    |---|---|\n    | 3 | 4 |"
	tables, _ := detectSourceTables(source)
	if len(tables) != 1 {
		t.Fatalf("expected 1 blockquote table (indented code skipped), got %d: %+v", len(tables), tables)
	}
	if !tables[0].blockquote {
		t.Fatalf("expected first table to be blockquoted, got %+v", tables[0])
	}
}

func TestDetectSourceTablesSkipsFencedCode(t *testing.T) {
	source := "```markdown\n| A | B |\n|---|---|\n| 1 | 2 |\n```"
	tables, _ := detectSourceTables(source)
	if len(tables) != 0 {
		t.Fatalf("expected no table inside fenced code, got %d: %+v", len(tables), tables)
	}
}

func TestDetectSourceTablesMultiple(t *testing.T) {
	source := "| A | B |\n|---|---|\n| 1 | 2 |\n\nText\n\n| C | D |\n|---|---|\n| 3 | 4 |"
	tables, _ := detectSourceTables(source)
	if len(tables) != 2 {
		t.Fatalf("expected 2 tables, got %d", len(tables))
	}
}

func TestRenderWithThemeBlockquoteTable(t *testing.T) {
	source := "> | A | B |\n> |---|---|\n> | 1 | 2 |"
	rendered, err := RenderWithTheme(source, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	clean := utils.StripANSI(rendered)
	if !strings.Contains(clean, "\u2501\u2501\u2501\u2501\u2501  \u2501\u2501\u2501\u2501\u2501") {
		t.Fatalf("expected box-drawing table separator in blockquote:\n%s", clean)
	}
	if !strings.Contains(clean, " A      B") {
		t.Fatalf("expected blockquote table header row:\n%s", clean)
	}
}

func TestStripInlineMarkdownPreservesSnakeCase(t *testing.T) {
	cases := map[string]string{
		"wide_cell":         "wide_cell",
		"snake_case_var":    "snake_case_var",
		"**bold**":          "bold",
		"*italic*":          "italic",
		"`code`":            "code",
		"~~strike~~":        "strike",
		"[label](http://x)": "label",
		"normal text":       "normal text",
	}
	for in, want := range cases {
		if got := stripInlineMarkdown(in); got != want {
			t.Fatalf("stripInlineMarkdown(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRenderWithThemePreservesSnakeCaseInTableCell(t *testing.T) {
	source := "| Name | Value |\n|---|---|\n| wide_cell | 42 |"
	rendered, err := RenderWithTheme(source, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	clean := utils.StripANSI(rendered)
	if !strings.Contains(clean, "wide_cell") {
		t.Fatalf("snake_case cell was mangled by table renderer:\n%s", clean)
	}
	if strings.Contains(clean, "wide_") && !strings.Contains(clean, "wide_cell") {
		t.Fatalf("underscore stripped from snake_case cell:\n%s", clean)
	}
}

func TestRenderWithThemeBlockquoteTablePreservesLeadingText(t *testing.T) {
	source := "> A blockquote with a table:\n> | Name | Value |\n> |---|---|\n> | alpha | 1 |"
	rendered, err := RenderWithTheme(source, 60, "")
	if err != nil {
		t.Fatal(err)
	}
	clean := utils.StripANSI(rendered)
	if !strings.Contains(clean, "A blockquote with a table:") {
		t.Fatalf("leading blockquote text was swallowed:\n%s", clean)
	}
	if strings.Contains(clean, "CODEX_INTERNAL_TABLE_") {
		t.Fatalf("table marker leaked into output:\n%s", clean)
	}
}

func TestRenderWideTableFitsWidth(t *testing.T) {
	source := "| Name | Value |\n|---|---|\n| " + strings.Repeat("x", 120) + " | 42 |\n| short | 7 |"
	rendered, err := RenderWithTheme(source, 40, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(utils.StripANSI(rendered), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if w := len([]rune(line)); w > 40 {
			t.Fatalf("wide table line exceeds width 40 (%d): %q", w, line)
		}
	}
	if !strings.Contains(utils.StripANSI(rendered), "Value") {
		t.Fatalf("compact column was squeezed so much its header wrapped away:\n%s", utils.StripANSI(rendered))
	}
	if !strings.Contains(utils.StripANSI(rendered), "42") {
		t.Fatalf("table body lost a value after wrapping:\n%s", utils.StripANSI(rendered))
	}
}

func TestRenderWideTableFallsBackToKeyValueWhenTooNarrow(t *testing.T) {
	// At a trivial width even minimum-width columns cannot fit; the renderer
	// falls back to key/value records instead of overflowing the terminal.
	source := "| Name | Value |\n|---|---|\n| alpha | 1 |"
	rendered, err := RenderWithTheme(source, 8, "")
	if err != nil {
		t.Fatal(err)
	}
	clean := utils.StripANSI(rendered)
	if !strings.Contains(clean, "Name") || !strings.Contains(clean, "Value") {
		t.Fatalf("key/value fallback missing header labels:\n%s", clean)
	}
	if !strings.Contains(clean, "alph") {
		t.Fatalf("key/value fallback missing body value:\n%s", clean)
	}
	// The marker must never leak into the transcript.
	if strings.Contains(clean, "CODEX_INTERNAL_TABLE_") {
		t.Fatalf("table marker leaked into output:\n%s", clean)
	}
}
