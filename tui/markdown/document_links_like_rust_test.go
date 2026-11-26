package markdown

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// Rust #61880 markdown_render_tests: `sidebar_file_link` keeps only local
// `.md`/`.markdown` targets with no location suffix, fragment, query, or host.
func TestDocumentLinkTargetMatchesRustSidebarFileLink(t *testing.T) {
	cases := []struct {
		destination string
		want        string
		ok          bool
	}{
		{destination: "./guide.md", want: "./guide.md", ok: true},
		{destination: "docs/guide.markdown", want: "docs/guide.markdown", ok: true},
		{destination: "/tmp/Guide.MD", want: "/tmp/Guide.MD", ok: true},
		{destination: "file:///tmp/guide.md", want: "/tmp/guide.md", ok: true},
		{destination: "file:///tmp/Guide%5Cnotes.md", want: `/tmp/Guide\notes.md`, ok: true},
		{destination: "file://server/share/guide.md"},
		{destination: "guide.md#L3"},
		{destination: "guide.md?plain=1"},
		{destination: "~/guide.md"},
		{destination: "//server/share/guide.md"},
		{destination: "guide.txt"},
		{destination: "guide.md:12"},
		{destination: "https://example.com/guide.md"},
	}
	for _, tc := range cases {
		got, ok := DocumentLinkTarget(tc.destination)
		if ok != tc.ok || got != tc.want {
			t.Fatalf("DocumentLinkTarget(%q) = (%q, %t), want (%q, %t)", tc.destination, got, ok, tc.want, tc.ok)
		}
	}
}

// Rust #61880: a POSIX file URI keeps the literal backslash in the preview
// target while the rendered label keeps forward-slash separators.
func TestPosixFileURIDocumentTargetKeepsLiteralBackslashLikeRust(t *testing.T) {
	target, ok := DocumentLinkTarget("file:///tmp/Guide%5Cnotes.md")
	if !ok || target != `/tmp/Guide\notes.md` {
		t.Fatalf("document target = (%q, %t)", target, ok)
	}
	display, ok := renderLocalLinkTarget("file:///tmp/Guide%5Cnotes.md", "")
	if !ok || display != "/tmp/Guide/notes.md" {
		t.Fatalf("display target = (%q, %t)", display, ok)
	}
	rendered, err := Render("[Guide](file:///tmp/Guide%5Cnotes.md)", 80)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(codextui.StripOSC8(rendered), "/tmp/Guide/notes.md") {
		t.Fatalf("rendered label lost the document path: %q", rendered)
	}
}

// Rust #61880: a document target is not a terminal hyperlink in the reader, but
// Go keeps painting the file escape so the link still works from the terminal.
// This pins the escape the transcript hit test reads back.
func TestDocumentLinkStaysReadableFromPaintedRow(t *testing.T) {
	rendered, err := RenderWithThemeCwd("[Guide](./guide.md)", 80, "", "/workspace/docs")
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	links := codextui.OSC8LinksInLine(rendered)
	if len(links) != 1 {
		t.Fatalf("painted links = %#v", links)
	}
	target, ok := DocumentLinkTarget(links[0].Destination)
	if !ok || !strings.HasSuffix(target, "guide.md") || !strings.HasPrefix(target, "/") {
		t.Fatalf("painted destination %q did not classify as a document", links[0].Destination)
	}
}
