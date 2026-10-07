package markdown

import (
	"strings"
	"testing"

	"codex_go/utils"
)

func localLinkLabelRenderedText(t *testing.T, source string, cwd string) string {
	t.Helper()
	rendered, err := RenderWithThemeCwd(source, 120, "", cwd)
	if err != nil {
		t.Fatalf("RenderWithThemeCwd(%q, %q) error = %v", source, cwd, err)
	}
	return utils.StripANSI(rendered)
}

// TestLocalFileLinkPreservesLabelsLikeRust covers Rust #50695 ("Preserve local
// Markdown link labels in the TUI",
// codex-rs/tui/src/markdown_render.rs / markdown_render/local_links.rs): a local
// link renders its non-blank label next to the formatted destination as
// "label (target)", including in tables, while an empty or whitespace-only label
// renders the destination alone.
//
// Rust counterparts: `file_link_preserves_label_and_decodes_fallback_destination`,
// `file_link_preserves_path_labels_and_formats_destinations` and
// `file_link_preserves_directory_labels_and_destinations`
// (codex-rs/tui/src/markdown_render_tests.rs).
func TestLocalFileLinkPreservesLabelsLikeRust(t *testing.T) {
	cases := []struct {
		name string
		cwd  string
		link string
		want string
	}{
		{
			name: "path-like label is preserved verbatim beside the formatted destination",
			cwd:  "/Users/example/code/codex",
			link: "[/Users/example/code/codex/codex-rs/tui/src/My%20File.rs](/Users/example/code/codex/codex-rs/tui/src/My%20File.rs)",
			want: "/Users/example/code/codex/codex-rs/tui/src/My%20File.rs (codex-rs/tui/src/My File.rs)",
		},
		{
			name: "empty label shows only the destination",
			cwd:  "/repo",
			link: "[](/repo/src/lib.rs)",
			want: "src/lib.rs",
		},
		{
			name: "whitespace-only label shows only the destination",
			cwd:  "/repo",
			link: "[   ](/repo/src/lib.rs)",
			want: "src/lib.rs",
		},
		{
			name: "line-number label keeps the destination suffix",
			cwd:  "/Users/example/code/codex",
			link: "[markdown_render.rs](/Users/example/code/codex/codex-rs/tui/src/markdown_render.rs:74)",
			want: "markdown_render.rs (codex-rs/tui/src/markdown_render.rs:74)",
		},
		{
			name: "directory labels keep their trailing separator",
			cwd:  "/repo",
			link: "[dir/](./dir)",
			want: "dir/ (./dir)",
		},
		{
			name: "relative destination stays relative",
			cwd:  "/repo",
			link: "[src/lib.rs](./src/lib.rs)",
			want: "src/lib.rs (./src/lib.rs)",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := strings.TrimSpace(localLinkLabelRenderedText(t, tc.link, tc.cwd))
			// The destination is rendered as a code span, so strip the backticks
			// the plain-text extraction keeps.
			got = strings.ReplaceAll(got, "`", "")
			if got != tc.want {
				t.Fatalf("rendered local link = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestLocalFileLinkLabelsInTableLikeRust pins the table-cell form of Rust #50695:
// the label and its formatted destination are appended into the active cell, so
// a table row keeps "label (target)" instead of collapsing to the destination.
func TestLocalFileLinkLabelsInTableLikeRust(t *testing.T) {
	source := "| File |\n|---|\n| [a](./src/a.rs) |\n| [b](/repo/src/b.go) |"
	got := utils.StripANSI(mustRenderCwd(t, source, "/repo"))
	for _, want := range []string{"a (./src/a.rs)", "b (src/b.go)"} {
		if !strings.Contains(got, want) {
			t.Fatalf("table cell missing %q:\n%s", want, got)
		}
	}
}

// TestLocalFileLinkMalformedFileURLFallsBackToDestinationLikeRust pins Rust
// #50695's fallback: when the local file URL cannot be parsed the renderer shows
// the original destination instead of dropping the link. Rust counterpart: the
// `file://[` case of `file_citation_paths_preserve_markdown_significant_characters`
// (codex-rs/tui/src/markdown_render/file_citations_tests.rs).
func TestLocalFileLinkMalformedFileURLFallsBackToDestinationLikeRust(t *testing.T) {
	got := strings.TrimSpace(localLinkLabelRenderedText(t, "[label](file://[)", "/repo"))
	got = strings.ReplaceAll(got, "`", "")
	if got != "label (file://[)" {
		t.Fatalf("malformed local file URL rendered as %q, want %q", got, "label (file://[)")
	}
	citation := strings.TrimSpace(localLinkLabelRenderedText(t, `:codex-file-citation{path="file://["}`, "/repo"))
	if citation != "file://[" {
		t.Fatalf("malformed file citation rendered as %q, want %q", citation, "file://[")
	}
}

func mustRenderCwd(t *testing.T, source string, cwd string) string {
	t.Helper()
	rendered, err := RenderWithThemeCwd(source, 120, "", cwd)
	if err != nil {
		t.Fatalf("RenderWithThemeCwd error = %v", err)
	}
	return rendered
}
