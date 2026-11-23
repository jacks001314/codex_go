package tea

import (
	"strings"
	"testing"
)

func hoverOSC8Link(url, text string) string {
	return "]8;;" + url + "" + text + "]8;;"
}


// Rust #62390: the link under the pointer is located from the painted row, so
// the highlight follows the visible text instead of a guessed cell range.
func TestPaintedLinkSpanAtFindsVisibleLinkLikeRust(t *testing.T) {
	row := "see " + hoverOSC8Link("https://example.com", "docs") + " now"
	span, ok := paintedLinkSpanAt(row, 4)
	if !ok {
		t.Fatalf("column 4 is inside the link: %q", row)
	}
	if got := row[span.bodyStart:span.bodyEnd]; got != "docs" {
		t.Fatalf("span body = %q, want docs", got)
	}
	for _, column := range []int{-1, 0, 3, 8, 9} {
		if _, ok := paintedLinkSpanAt(row, column); ok {
			t.Fatalf("column %d is outside the link", column)
		}
	}
}

// A wide glyph occupies two columns, so the link is found on both of them and
// a following link starts after the wide text.
func TestPaintedLinkSpanAtHandlesWideCharactersLikeRust(t *testing.T) {
	row := "x " + hoverOSC8Link("https://example.com/a", "文档") + " " + hoverOSC8Link("https://example.com/b", "b")
	for _, column := range []int{2, 3, 4, 5} {
		span, ok := paintedLinkSpanAt(row, column)
		if !ok {
			t.Fatalf("column %d is inside the wide link: %q", column, row)
		}
		if got := row[span.bodyStart:span.bodyEnd]; got != "文档" {
			t.Fatalf("column %d body = %q", column, got)
		}
	}
	span, ok := paintedLinkSpanAt(row, 7)
	if !ok || row[span.bodyStart:span.bodyEnd] != "b" {
		t.Fatalf("second link = %#v ok=%v", span, ok)
	}
}

func TestRestylePaintedLinkKeepsWrapperLikeRust(t *testing.T) {
	row := "\x1b[38;5;10msee " + hoverOSC8Link("https://example.com", "docs") + " now"
	span, ok := paintedLinkSpanAt(row, 4)
	if !ok {
		t.Fatal("link not found")
	}
	hover := "\x1b[1;36m"
	out := restylePaintedLink(row, span, hover)
	if !strings.Contains(out, "\x1b]8;;https://example.com\x07"+hover+"docs") {
		t.Fatalf("hover style is not inside the wrapper: %q", out)
	}
	if !strings.Contains(out, "docs\x1b[38;5;10m") {
		t.Fatalf("surrounding foreground was not restored: %q", out)
	}
}

// The model applies the highlight only while it owns the screen and nothing
// else owns the pointer, and only on the row and span under the pointer.
func TestModelLinkHoverRecoloursPointerTargetLikeRust(t *testing.T) {
	model := NewModel(nil, Options{Width: 80, Height: 24})
	model.ownedScreen = true
	model.mouseCaptureActive = false
	link := "docs " + hoverOSC8Link("https://example.com", "page")
	frame := "header\n" + link + "\ntail"
	model.hoverPointerX, model.hoverPointerY, model.hoverPointerSet = 5, 1, true
	if got := model.applyLinkHover(frame); got != frame {
		t.Fatalf("hover applied without mouse capture: %q", got)
	}

	model.mouseCaptureActive = true
	got := model.applyLinkHover(frame)
	if got == frame {
		t.Fatal("hover did not recolour the link under the pointer")
	}
	if !strings.Contains(got, model.linkHoverSGR()+"page") {
		t.Fatalf("hovered span = %q", got)
	}
	if !strings.Contains(got, "header") || !strings.Contains(got, "tail") {
		t.Fatalf("hover changed other rows: %q", got)
	}

	model.hoverPointerY = 0
	if got := model.applyLinkHover(frame); got != frame {
		t.Fatalf("hover applied to a row without a link: %q", got)
	}
}
