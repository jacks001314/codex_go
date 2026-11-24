package tea

import (
	"strings"
	"testing"

	codextui "codex_go/tui"
)

// Rust #62390: the disclosure control the pointer sits on is recoloured like a
// link, while a pointer outside the visible label leaves the row alone.
func TestPaintedDisclosureControlSpanLikeRust(t *testing.T) {
	row := "\x1b[2m    + 2 lines\x1b[0m"
	span, ok := paintedDisclosureControlSpan(row, 5)
	if !ok {
		t.Fatalf("column 5 is inside the control: %q", row)
	}
	if got := row[span.bodyStart:span.bodyEnd]; got != "+ 2 lines" {
		t.Fatalf("span body = %q, want the label", got)
	}
	for _, column := range []int{-1, 0, 3, 14} {
		if _, ok := paintedDisclosureControlSpan(row, column); ok {
			t.Fatalf("column %d is outside the control", column)
		}
	}
	out := restylePaintedLink(row, span, "\x1b[1;36m")
	if !strings.Contains(out, "\x1b[1;36m+ 2 lines\x1b[2m\x1b[0m") {
		t.Fatalf("hover style = %q", out)
	}
	if !strings.HasPrefix(out, "\x1b[2m    ") {
		t.Fatalf("indent changed: %q", out)
	}
	if _, ok := paintedDisclosureControlSpan("plain text", 2); ok {
		t.Fatal("a non-control row has no disclosure hover area")
	}
}

// The right-aligned passive warning notice is hoverable from its glyph to the
// end of the row.
func TestPaintedWarningNoticeSpanLikeRust(t *testing.T) {
	row := "esc interrupt   \u26a0 2 warnings \u00b7 f2 to view"
	glyphColumn := codextui.DisplayWidth("esc interrupt   ")
	span, ok := paintedWarningNoticeSpan(row, glyphColumn)
	if !ok {
		t.Fatalf("column %d is inside the notice: %q", glyphColumn, row)
	}
	if got := row[span.bodyStart:span.bodyEnd]; got != "\u26a0 2 warnings \u00b7 f2 to view" {
		t.Fatalf("span body = %q", got)
	}
	if _, ok := paintedWarningNoticeSpan(row, glyphColumn-1); ok {
		t.Fatal("text before the notice is not hoverable")
	}
	if _, ok := paintedWarningNoticeSpan("esc interrupt", 2); ok {
		t.Fatal("a row without the notice has no notice area")
	}
	styled := "\x1b[38;5;8m" + row + "\x1b[0m"
	styledSpan, ok := paintedWarningNoticeSpan(styled, glyphColumn)
	if !ok {
		t.Fatalf("notice not found in the styled row: %q", styled)
	}
	out := restylePaintedLink(styled, styledSpan, "\x1b[1;36m")
	if !strings.Contains(out, "\x1b[1;36m\u26a0 2 warnings") {
		t.Fatalf("hover style = %q", out)
	}
}

// The model applies the highlight through applyLinkHover, gated on the position
// the same way as the painted links.
func TestModelHoverRecoloursDisclosureControlLikeRust(t *testing.T) {
	model := NewModel(nil, Options{Width: 80, Height: 24})
	model.ownedScreen = true
	model.mouseCaptureActive = true
	control := "\x1b[2m    + 2 lines\x1b[0m"
	frame := "header\n" + control + "\ntail"
	model.hoverPointerX, model.hoverPointerY, model.hoverPointerSet = 5, 1, true
	got := model.applyLinkHover(frame)
	if !strings.Contains(got, model.linkHoverSGR()+"+ 2 lines") {
		t.Fatalf("hovered control = %q", got)
	}
	if !strings.Contains(got, "header") || !strings.Contains(got, "tail") {
		t.Fatalf("hover changed other rows: %q", got)
	}

	model.rawOutput = true
	if got := model.applyLinkHover(frame); got != frame {
		t.Fatalf("raw output has no clickable controls: %q", got)
	}
}

func TestModelHoverRecoloursWarningNoticeLikeRust(t *testing.T) {
	model := NewModel(nil, Options{Width: 80, Height: 24})
	model.ownedScreen = true
	model.mouseCaptureActive = true
	notice := "hints   \u26a0 1 warning \u00b7 f2 to view"
	frame := "header\n" + notice
	model.hoverPointerX, model.hoverPointerY, model.hoverPointerSet = 9, 1, true
	got := model.applyLinkHover(frame)
	if !strings.Contains(got, model.linkHoverSGR()+"\u26a0 1 warning") {
		t.Fatalf("hovered notice = %q", got)
	}
}
