package tui

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestUITreeElementsRoundTripAndRequireExplicitRowSpacing mirrors Rust
// #59518: the tree round-trips with string leaves, explicit row gaps and no
// node keys; unsafe link destinations are rejected.
func TestUITreeElementsRoundTripAndRequireExplicitRowSpacing(t *testing.T) {
	raw := []byte(`{"type":"row","props":{"gap":1},"children":[{"type":"link","props":{"href":"https://example.com/pr/42","title":"PR details"},"children":["12%"]},null," "]}`)
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

	for _, href := range []string{"relative/path", "file:///tmp/example", "https://example.com/\u001b"} {
		if _, err := ParseUITree([]byte(`{"type":"link","props":{"href":"` + href + `"},"children":[]}`)); err == nil {
			t.Fatalf("unsafe href %q decoded", href)
		}
	}
	for _, tc := range []struct {
		gap  string
		want string
	}{{"0", "AB"}, {"1", "A B"}, {"2", "A  B"}, {"9007199254740991", "A  …"}} {
		row := MustUITree([]byte(`{"type":"row","props":{"gap":` + tc.gap + `},"children":["A","B"]}`))
		if line := row.RenderLine(4); line.Text != tc.want {
			t.Fatalf("gap %s rendered %q, want %q", tc.gap, line.Text, tc.want)
		}
	}
}

// TestUITreeRejectsInvalidGapsAndElements mirrors Rust's malformed-element
// matrix, including duplicate JSON keys that encoding/json maps would collapse.
func TestUITreeRejectsInvalidGapsAndElements(t *testing.T) {
	for _, gap := range []string{"-1", "1.5", "9007199254740992", `"1"`, "[1]"} {
		row := `{"children":[],"props":{"gap":` + gap + `},"type":"row"}`
		if _, err := ParseUITree([]byte(row)); err == nil {
			t.Fatalf("invalid gap %s decoded", gap)
		}
	}
	for _, input := range []string{
		"[]",
		"null true",
		`{"type":{"text":null},"children":[]}`,
		`{"type":"text","children":[["Ready"]]}`,
		`{"type":"text","props":null,"children":[]}`,
		`{"type":"row","props":{},"children":[]}`,
		`{"type":"row","props":{"gap":0,"extra":true},"children":[]}`,
		`{"props":{"gap":0,"gap":1},"children":[],"type":"row"}`,
		`{"type":"text","props":{"text":"old"},"children":[]}`,
		`{"type":"spacer"}`,
		`{"type":"divider"}`,
		`{"type":"row","key":7,"props":{"gap":0},"children":[]}`,
	} {
		if _, err := ParseUITree([]byte(input)); err == nil {
			t.Fatalf("invalid tree decoded: %s", input)
		}
	}
	if tree, err := ParseUITree([]byte("null")); err != nil || tree != nil {
		t.Fatalf("null tree = %#v, %v", tree, err)
	}
}

// TestUITreeDeserializationChecksLimitsAcrossWholeTree mirrors Rust #59518:
// every non-null node and string leaf counts, even when outside the viewport.
func TestUITreeDeserializationChecksLimitsAcrossWholeTree(t *testing.T) {
	parent := func(children []any) map[string]any {
		return map[string]any{"type": "text", "children": children}
	}
	nulls := make([]any, 257)
	mustTreeJSON(t, parent(nulls))

	nested := any("X")
	for range 16 {
		nested = parent([]any{nested})
	}
	mustTreeJSON(t, nested)
	if _, err := ParseUITree(mustJSON(t, parent([]any{nested}))); err == nil {
		t.Fatal("tree deeper than 16 decoded")
	}

	leaves := make([]any, 255)
	for index := range leaves {
		leaves[index] = ""
	}
	mustTreeJSON(t, parent(leaves))
	leaves = append(leaves, "")
	if _, err := ParseUITree(mustJSON(t, parent(leaves))); err == nil {
		t.Fatal("tree with 256 string leaves exceeded the node limit without an error")
	}

	text := strings.Repeat("界", 5461) // 16,383 UTF-8 bytes.
	mustTreeJSON(t, parent([]any{text, "x"}))
	if _, err := ParseUITree(mustJSON(t, parent([]any{text, "xx"}))); err == nil {
		t.Fatal("tree exceeding 16 KiB of text decoded")
	}

	atLimit := "null" + strings.Repeat(" ", UITreeMaxJSONBytes-4)
	if tree, err := ParseUITree([]byte(atLimit)); err != nil || tree != nil {
		t.Fatalf("JSON at the 64 KiB limit = %#v, %v", tree, err)
	}
	if _, err := ParseUITree([]byte(atLimit + " ")); err == nil {
		t.Fatal("JSON over 64 KiB decoded")
	}

	prefix := "https://example.com/"
	href := prefix + strings.Repeat("x", UITreeMaxLinkBytes-len(prefix))
	link := func(url string) []byte {
		raw, err := json.Marshal(map[string]any{
			"type":     "link",
			"props":    map[string]any{"href": url},
			"children": []any{"ok"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	mustTreeJSONBytes(t, link(href))
	if _, err := ParseUITree(link(href + "x")); err == nil {
		t.Fatal("link destination over 8 KiB decoded")
	}
}

// TestUITreeLinksOnlyAnnotateTheirOwnCellsLikeRust checks hyperlink clipping
// when a wide grapheme and a row gap precede the link.
func TestUITreeLinksOnlyAnnotateTheirOwnCellsLikeRust(t *testing.T) {
	tree := MustUITree([]byte(`{"type":"row","props":{"gap":1},"children":["界",{"type":"link","props":{"href":"https://example.com/pr/42"},"children":[{"type":"text","children":["PR #42"]}]}]}`))
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
	tree := MustUITree([]byte(`"ｶﾞ"`))
	if line := tree.RenderLine(2); line.Text != "ｶﾞ" {
		t.Fatalf("katakana line = %q", line.Text)
	}
}

// TestUITreeEllipsisCountsTowardWidthLikeRust covers the overflow boundary.
func TestUITreeEllipsisCountsTowardWidthLikeRust(t *testing.T) {
	tree := MustUITree([]byte(`{"type":"row","props":{"gap":1},"children":["abcde",null]}`))
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
	wide := MustUITree([]byte(`"a界界"`))
	if line := wide.RenderLine(4); line.Text != "a界…" {
		t.Fatalf("wide grapheme truncation = %q", line.Text)
	}
	long := strings.Repeat("x", 513)
	exact := MustUITree(mustJSON(t, long))
	if line := exact.RenderLine(513); line.Text != long {
		t.Fatalf("exact fit changed: len=%d", len(line.Text))
	}
}

// TestUITreeFirstLineSkipsHiddenTextLikeRust keeps only the first line of a
// string leaf and sanitizes its escapes before rendering siblings.
func TestUITreeFirstLineSkipsHiddenTextLikeRust(t *testing.T) {
	multiline := "\u001b[2Jok\n" + strings.Repeat("x", 1024)
	tree := MustUITree(mustJSON(t, map[string]any{
		"type": "row", "props": map[string]any{"gap": 0},
		"children": []any{multiline, "!"},
	}))
	if line := tree.RenderLine(4); line.Text != "ok!" {
		t.Fatalf("multiline line = %q", line.Text)
	}
}

// TestUITreeDirectlyConstructedTextKeepsBoundedUTF8PrefixLikeRust verifies
// built-in values bypassing the wire decoder still cap text at a UTF-8 boundary.
func TestUITreeDirectlyConstructedTextKeepsBoundedUTF8PrefixLikeRust(t *testing.T) {
	prefix := strings.Repeat("x", UITreeMaxTextBytes-1)
	node := &UINode{Kind: UINodeString, Text: prefix + "界tail"}
	if line := node.RenderLine(1 << 20); line.Text != prefix {
		t.Fatalf("direct text length = %d, want %d", len(line.Text), len(prefix))
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("marshal fixture: %v", err)
	}
	return raw
}

func mustTreeJSON(t *testing.T, value any) {
	t.Helper()
	mustTreeJSONBytes(t, mustJSON(t, value))
}

func mustTreeJSONBytes(t *testing.T, raw []byte) {
	t.Helper()
	if _, err := ParseUITree(raw); err != nil {
		t.Fatalf("ParseUITree(%s) error = %v", raw, err)
	}
}
