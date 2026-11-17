package markdown

import (
	"strings"
	"testing"
)

// Rust parity: codex-rs/tui/src/markdown_render/local_links.rs
// `sidebar_file_link`, markdown_render::file_links (#61920).

func TestSidebarFileLinksCollectLinksAndImagesLikeRust(t *testing.T) {
	source := strings.Join([]string{
		"Read [the notes](docs/notes.md) and [entry](/workspace/repo/main.go:12:3).",
		"\n\n![screenshot](./shots/after.png)\n\n",
		"Web links stay out: [site](https://example.com/page).",
	}, "")
	links := SidebarFileLinks(source)
	if len(links) != 3 {
		t.Fatalf("links = %#v", links)
	}
	if links[0].Path != "docs/notes.md" || links[0].Location != nil {
		t.Fatalf("first = %#v", links[0])
	}
	if links[1].Path != "/workspace/repo/main.go" || links[1].Location == nil ||
		links[1].Location.Line != 12 || links[1].Location.Column != 3 {
		t.Fatalf("second = %#v", links[1])
	}
	if links[2].Path != "./shots/after.png" {
		t.Fatalf("image = %#v", links[2])
	}
}

func TestSidebarFileLinkDestinationRulesLikeRust(t *testing.T) {
	cases := []struct {
		destination string
		want        bool
	}{
		{"docs/a.md#L10C2", true},
		{"docs/a.md#notes", false},
		{"file:///workspace/repo/a.md", true},
		{"file://other-host/workspace/a.md", false},
		{"docs/a.md?plain=1", false},
		{"docs/", false},
		{"~/secrets.txt", false},
		{"//other-host/share/a.md", false},
		{"/workspace/" + repeatRune('a', 4100), false},
		{"\\\\server\\share\\a.md", false},
		{"notes.md", true},
	}
	for _, testCase := range cases {
		_, ok := SidebarFileLinkForDestination(testCase.destination)
		if ok != testCase.want {
			t.Fatalf("%q accepted = %v, want %v", testCase.destination, ok, testCase.want)
		}
	}
	if link, ok := SidebarFileLinkForDestination("docs/a.md#L10C2"); !ok ||
		link.Path != "docs/a.md" || link.Location == nil || link.Location.Line != 10 || link.Location.Column != 2 {
		t.Fatalf("hash location = %#v, %v", link, ok)
	}
	if link, ok := SidebarFileLinkForDestination("docs/a.md:0"); !ok || link.Location == nil || link.Location.Line != 1 {
		t.Fatalf("zero line clamps to 1: %#v, %v", link, ok)
	}
}

func repeatRune(value rune, count int) string {
	out := make([]rune, count)
	for index := range out {
		out[index] = value
	}
	return string(out)
}
