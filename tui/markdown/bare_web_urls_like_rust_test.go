package markdown

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
	"codex_go/utils"
)

func markdownLinkSGR() string {
	return linkStyleSGR(codexMarkdownStyle().Link, codextui.DetectStdoutColorLevel())
}

// Mirrors Rust #46884's `bare_urls_are_styled_without_coloring_surrounding_prose`:
// every bare URL borrows the Markdown link style while the punctuation, Unicode
// prose, and emphasis around it keep their own styling.
func TestBareURLsAreStyledWithoutColoringSurroundingProseLikeRust(t *testing.T) {
	InitRendering(DefaultRendering())
	t.Cleanup(func() { InitRendering(DefaultRendering()) })

	source := "界 (https://example.com/a); https://example.com/b! **https://example.com/c**"
	rendered, err := Render(source, 100)
	if err != nil {
		t.Fatal(err)
	}
	plain := utils.StripANSI(rendered)
	wantPlain := "界 (https://example.com/a); https://example.com/b! https://example.com/c"
	if strings.TrimSpace(plain) != wantPlain {
		t.Fatalf("plain text = %q, want %q", strings.TrimSpace(plain), wantPlain)
	}
	linkSGR := markdownLinkSGR()
	if linkSGR == "" {
		t.Fatal("Markdown link style produced no SGR")
	}
	for _, url := range []string{
		"https://example.com/a",
		"https://example.com/b",
		"https://example.com/c",
	} {
		index := strings.Index(rendered, url)
		if index < 0 {
			t.Fatalf("rendered output lost %q: %q", url, rendered)
		}
		prefix := rendered[:index]
		if strings.LastIndex(prefix, linkSGR) <= strings.LastIndex(prefix, "\x1b[0m") {
			t.Fatalf("%q is not inside link styling: %q", url, rendered)
		}
	}
	firstURL := strings.Index(rendered, "https://example.com/a")
	if firstURL < 0 || !strings.HasSuffix(utils.StripANSI(rendered[:firstURL]), "界 (") {
		t.Fatalf("prose before the URL borrowed the link style: %q", rendered)
	}
	if !strings.Contains(rendered, "\x1b[24m);") {
		t.Fatalf("sentence punctuation stayed underlined: %q", rendered)
	}
	if !strings.Contains(rendered, "\x1b[0m!") {
		t.Fatalf("exclamation mark stayed inside the link style: %q", rendered)
	}
}

// Mirrors Rust's wrapped-table snapshot: a bare URL in a table cell carries the
// link style instead of plain cell text.
func TestBareURLsKeepLinkStyleInTablesLikeRust(t *testing.T) {
	InitRendering(DefaultRendering())
	t.Cleanup(func() { InitRendering(DefaultRendering()) })

	rendered, err := Render("| Resource |\n| --- |\n| https://example.com/docs |", 12)
	if err != nil {
		t.Fatal(err)
	}
	linkSGR := markdownLinkSGR()
	if !strings.Contains(rendered, linkSGR+"https://example.com/docs") {
		t.Fatalf("table URL lost its link style: %q", rendered)
	}
	if plain := utils.StripANSI(rendered); strings.Contains(plain, "|") || !strings.Contains(plain, "https://example.com/docs") {
		t.Fatalf("table plain text = %q", plain)
	}
}

// A long URL protected from renderer wrapping must still be styled and must not
// leak its placeholder, including at narrow widths.
func TestLongBareURLAtNarrowWidthStaysStyledLikeRust(t *testing.T) {
	InitRendering(DefaultRendering())
	t.Cleanup(func() { InitRendering(DefaultRendering()) })

	url := "https://example.com/" + strings.Repeat("a", 80)
	rendered, err := Render("See "+url+" end", 12)
	if err != nil {
		t.Fatal(err)
	}
	if strings.ContainsRune(rendered, '\uE000') {
		t.Fatalf("long-URL placeholder leaked: %q", rendered)
	}
	opener := "\x1b]8;;" + url + "\x07"
	openIndex := strings.Index(rendered, opener)
	if openIndex < 0 {
		t.Fatalf("long URL is not a full hyperlink: %q", rendered)
	}
	closeIndex := strings.Index(rendered[openIndex+len(opener):], "\x1b]8;;\x07")
	if closeIndex < 0 || !strings.Contains(rendered[openIndex:openIndex+len(opener)+closeIndex], markdownLinkSGR()) {
		t.Fatalf("long URL hyperlink lost its link style: %q", rendered)
	}
}
