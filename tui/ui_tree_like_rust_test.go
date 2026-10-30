package tui

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestUITreeNormalizedRoundTripAndLeafShapeLikeRust mirrors Rust #57970: the
// tree round-trips in the `codex-tui/1` shape, rejects children on text leaves,
// decodes null to no tree, and rejects unsafe link destinations.
func TestUITreeNormalizedRoundTripAndLeafShapeLikeRust(t *testing.T) {
	raw := []byte(`{"type":"row","key":7,"props":{"gap":2.0},"children":[{"type":"link","props":{"href":"https://example.com/pr/42"},"children":[{"type":"text","key":"usage","props":{"text":"12%"}}]},null,{"type":"spacer"}]}`)
	tree := MustUITree(raw)
	encoded, err := json.Marshal(tree)
	if err != nil {
		t.Fatalf("marshal tree: %v", err)
	}
	var got, want any
	if err := json.Unmarshal(encoded, &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &want); err != nil {
		t.Fatal(err)
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("round trip = %s, want %s", gotJSON, wantJSON)
	}

	if _, err := ParseUITree([]byte(`{"type":"text","props":{"text":"hello"},"children":[]}`)); err == nil {
		t.Fatal("text node with children decoded")
	}
	if tree, err := ParseUITree([]byte("null")); err != nil || tree != nil {
		t.Fatalf("null tree = %#v, %v", tree, err)
	}
	for _, href := range []string{"relative/path", "file:///tmp/example", "https://example.com/\u001b"} {
		if _, err := ParseUITree([]byte(`{"type":"link","props":{"href":"` + href + `"},"children":[]}`)); err == nil {
			t.Fatalf("unsafe href %q decoded", href)
		}
	}
}

// TestUITreeLinksOnlyAnnotateTheirOwnCellsLikeRust checks hyperlink clipping
// when a wide grapheme and a gap precede the link, and that the ellipsis stays
// outside the link.
func TestUITreeLinksOnlyAnnotateTheirOwnCellsLikeRust(t *testing.T) {
	tree := MustUITree([]byte(`{"type":"row","props":{"gap":2},"children":[{"type":"text","key":"prefix","props":{"text":"界"}},{"type":"link","props":{"href":"https://example.com/pr/42"},"children":[{"type":"row","props":{},"children":[{"type":"text","key":"pr","props":{"text":"PR #42"}}]}]}]}`))
	wide := tree.RenderLine(16)
	if wide.Text != "界 PR #42" {
		t.Fatalf("wide text = %q", wide.Text)
	}
	if len(wide.Hyperlinks) != 1 || wide.Hyperlinks[0].Start != 3 || wide.Hyperlinks[0].End != 9 {
		t.Fatalf("wide hyperlinks = %#v", wide.Hyperlinks)
	}
	rendered := wide.Render()
	if !strings.Contains(rendered, "https://example.com/pr/42") {
		t.Fatalf("rendered link missing: %q", rendered)
	}
	if strings.Contains(StripOSC8(rendered), "\u001b") {
		t.Fatalf("visible text leaked escapes: %q", StripOSC8(rendered))
	}

	if line := tree.RenderLine(1); line.Text != "…" || len(line.Hyperlinks) != 0 {
		t.Fatalf("width-1 line = %#v", line)
	}
	narrow := tree.RenderLine(6)
	if narrow.Text != "界 PR…" {
		t.Fatalf("narrow text = %q", narrow.Text)
	}
	if len(narrow.Hyperlinks) != 1 || narrow.Hyperlinks[0].Start != 3 || narrow.Hyperlinks[0].End != 5 {
		t.Fatalf("narrow hyperlinks = %#v", narrow.Hyperlinks)
	}
	if links := OSC8LinksInLine(narrow.Render()); len(links) != 1 || links[0].Start != 3 || links[0].End != 5 {
		t.Fatalf("rendered narrow links = %#v", links)
	}
}

// TestUITreeHalfwidthKatakanaFitsLikeRust keeps an exactly fitting halfwidth
// voiced katakana pair without adding an ellipsis.
func TestUITreeHalfwidthKatakanaFitsLikeRust(t *testing.T) {
	tree := MustUITree([]byte(`{"type":"text","key":"name","props":{"text":"ｶﾞ"}}`))
	if line := tree.RenderLine(2); line.Text != "ｶﾞ" {
		t.Fatalf("katakana line = %q", line.Text)
	}
}

// TestUITreeEllipsisCountsTowardWidthLikeRust covers the overflow boundary.
func TestUITreeEllipsisCountsTowardWidthLikeRust(t *testing.T) {
	tree := MustUITree([]byte(`{"type":"row","props":{"gap":2},"children":[{"type":"text","key":"label","props":{"text":"abcde"}},null]}`))
	for _, tc := range []struct {
		width int
		want  string
	}{{0, ""}, {1, "…"}, {4, "abc…"}, {5, "abcde"}} {
		line := tree.RenderLine(tc.width)
		if line.Text != tc.want {
			t.Fatalf("width %d text = %q, want %q", tc.width, line.Text, tc.want)
		}
		if line.Width() > tc.width {
			t.Fatalf("width %d line is %d wide", tc.width, line.Width())
		}
	}
	wide := MustUITree([]byte(`{"type":"text","key":"label","props":{"text":"a界界"}}`))
	if line := wide.RenderLine(4); line.Text != "a界…" {
		t.Fatalf("wide grapheme truncation = %q", line.Text)
	}
	long := strings.Repeat("x", 513)
	exact := MustUITree([]byte(`{"type":"text","key":"label","props":{"text":"` + long + `"}}`))
	if line := exact.RenderLine(513); line.Text != long {
		t.Fatalf("exact fit changed: len=%d", len(line.Text))
	}
}

// TestUITreeFirstLineSkipsHiddenTextLikeRust keeps only the first line of a
// text leaf and sanitizes its escapes before rendering siblings.
func TestUITreeFirstLineSkipsHiddenTextLikeRust(t *testing.T) {
	multiline := "\u001b[2Jok\n" + strings.Repeat("x", 16*1024)
	tree := MustUITree([]byte(`{"type":"row","props":{},"children":[{"type":"text","key":"first","props":{"text":"` + escapeJSON(multiline) + `"}},{"type":"text","key":"sibling","props":{"text":"!"}}]}`))
	if line := tree.RenderLine(4); line.Text != "ok!" {
		t.Fatalf("multiline line = %q", line.Text)
	}
}

func escapeJSON(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded[1 : len(encoded)-1])
}
