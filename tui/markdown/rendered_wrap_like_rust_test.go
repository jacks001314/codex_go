package markdown

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

// Rust lays prose out with `RtOptions::break_words` enabled (the wrapping.rs
// default), so a token that cannot fit on a row is broken across rows. glamour
// word-wraps prose but leaves an over-long token intact, and the transcript
// viewport clipped such rows at the pane edge: a JSON blob or hash lost its
// tail instead of wrapping.
const renderedWrapLongToken = `payload=0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef`

func TestRenderedProseBreaksLongTokenLikeRust(t *testing.T) {
	rendered, err := RenderWithTheme("snapshot "+renderedWrapLongToken, 40, "")
	if err != nil {
		t.Fatal(err)
	}
	var joined strings.Builder
	for index, row := range strings.Split(rendered, "\n") {
		plain := ansi.Strip(row)
		joined.WriteString(strings.TrimSpace(plain))
		if width := ansi.StringWidth(plain); width > 40 {
			t.Fatalf("row %d width %d > 40: %q", index, width, plain)
		}
	}
	if !strings.Contains(joined.String(), renderedWrapLongToken) {
		t.Fatalf("wrapped rows lost part of the token:\n%s", joined.String())
	}
}

// Rust's url_preserving_wrap_options turns break_words off for URL tokens so
// terminal link detection keeps seeing one destination (#51449). An over-long
// URL therefore keeps its own over-wide row instead of being split mid-name.
func TestRenderedURLTokenStaysWholeLikeRust(t *testing.T) {
	const url = "https://example.com/a-very-long-path-with-many-segments-and-a-query?token=abcdef"
	rendered, err := RenderWithTheme("- "+url+"\n- Short", 24, "")
	if err != nil {
		t.Fatal(err)
	}
	lines := plainRenderedLines(rendered)
	if len(lines) == 0 || !strings.Contains(lines[0], url) {
		t.Fatalf("URL token was split across rows: %#v", lines)
	}
}

// Rust's continuation rows repeat the block indentation (subsequent_indent) and
// re-apply the active style and link, so a broken token keeps all three.
func TestRenderedBrokenTokenKeepsIndentLikeRust(t *testing.T) {
	rows := wrapRenderedRow("      "+strings.Repeat("y", 40), 20)
	if len(rows) < 2 {
		t.Fatalf("token was not broken across rows: %#v", rows)
	}
	for _, row := range rows {
		plain := ansi.Strip(row)
		if !strings.HasPrefix(plain, "      ") {
			t.Fatalf("row lost the block indent: %q", plain)
		}
		if width := ansi.StringWidth(plain); width > 20 {
			t.Fatalf("row width %d > 20: %q", width, plain)
		}
	}
}

func TestRenderedBrokenTokenKeepsStyleAndLinkLikeRust(t *testing.T) {
	styled := wrapRenderedRow("\x1b[31m"+strings.Repeat("z", 40)+"\x1b[0m", 16)
	if len(styled) < 2 {
		t.Fatalf("styled token was not broken: %#v", styled)
	}
	for _, row := range styled[1:] {
		if !strings.Contains(row, "\x1b[31m") {
			t.Fatalf("continuation row lost the active style: %q", row)
		}
	}
	linked := wrapRenderedRow("\x1b]8;;file:///repo/a.md\x07"+strings.Repeat("w", 40)+"\x1b]8;;\x07", 16)
	if len(linked) < 2 {
		t.Fatalf("linked token was not broken: %#v", linked)
	}
	for _, row := range linked[1:] {
		if !strings.Contains(row, "\x1b]8;;file:///repo/a.md\x07") {
			t.Fatalf("continuation row lost the hyperlink: %q", row)
		}
	}
}

// Code blocks keep their source lines verbatim: Rust skips wrapping inside a
// code block "to preserve whitespace for copy/paste".
func TestRenderedWrapLeavesCodeBlocksAloneLikeRust(t *testing.T) {
	source := "```text\n" + renderedWrapLongToken + "\n```"
	rendered, err := RenderWithTheme(source, 30, "")
	if err != nil {
		t.Fatal(err)
	}
	if !containsExactLine(ansi.Strip(rendered), renderedWrapLongToken) {
		t.Fatalf("code block source line was wrapped:\n%s", ansi.Strip(rendered))
	}
}
