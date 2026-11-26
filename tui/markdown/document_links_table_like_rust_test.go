package markdown

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// Rust #61880 narrow_header_only_table_retains_local_file_target: a table link
// keeps its target when the table is laid out narrow, including when the cell
// wraps the path across rows.
func TestNarrowTableKeepsDocumentTargetLikeRust(t *testing.T) {
	source := "| [Guide](./guide.md) | A | B | C | D | E |\n| --- | --- | --- | --- | --- | --- |"
	for _, width := range []int{40, 50, 60, 80} {
		rendered, err := RenderWithThemeCwd(source, width, "", "/workspace/docs")
		if err != nil {
			t.Fatalf("render width %d: %v", width, err)
		}
		var joined strings.Builder
		annotated := 0
		for _, line := range strings.Split(rendered, "\n") {
			links := codextui.OSC8LinksInLine(line)
			for _, link := range links {
				path, ok := DocumentLinkTarget(link.Destination)
				if !ok || path != "/workspace/docs/guide.md" {
					t.Fatalf("width %d row %q link = (%q, %t)", width, line, path, ok)
				}
			}
			annotated += len(links)
			joined.WriteString(codextui.StripOSC8(line))
			joined.WriteString("\n")
		}
		if annotated == 0 {
			t.Fatalf("width %d dropped the document target: %q", width, rendered)
		}
		if text := joined.String(); !strings.Contains(text, "gui") || !strings.Contains(text, "md") {
			t.Fatalf("width %d lost the visible path: %q", width, text)
		}
	}
}

// A wrapped path is linked on every row that paints part of it, so no row shows
// the target as unlinked text (Rust remap_wrapped_line keeps per-row ranges).
func TestWrappedDocumentTargetLinksEveryRow(t *testing.T) {
	source := "| [Guide](./guide.md) | A | B | C | D | E |\n| --- | --- | --- | --- | --- | --- |"
	rendered, err := RenderWithThemeCwd(source, 40, "", "/workspace/docs")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	rows := strings.Split(rendered, "\n")
	fragments := []string{"gui", "md"}
	for _, fragment := range fragments {
		matched := false
		for _, row := range rows {
			if !strings.Contains(codextui.StripOSC8(row), fragment) {
				continue
			}
			matched = true
			if len(codextui.OSC8LinksInLine(row)) == 0 {
				t.Fatalf("row %q shows the wrapped path without a link", codextui.StripOSC8(row))
			}
		}
		if !matched {
			t.Fatalf("wrapped fragment %q missing from %q", fragment, rendered)
		}
	}
}
