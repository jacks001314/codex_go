package tea

import (
	"strings"
	"testing"
	"unicode/utf8"

	codextui "codex_go/tui"
	"codex_go/utils"
)

func newComposerHyperlinkModel(t *testing.T) *Model {
	t.Helper()
	return NewModel(codextui.NewState(nil), Options{
		Width:               40,
		Height:              20,
		AlternateScreenMode: "always",
	})
}

func osc8Open(destination string) string {
	return "\x1b]8;;" + destination + "\x07"
}

func linkedComposerText(view string, destination string) string {
	open := osc8Open(destination)
	close := "\x1b]8;;\x07"
	var out strings.Builder
	for {
		start := strings.Index(view, open)
		if start < 0 {
			break
		}
		view = view[start+len(open):]
		end := strings.Index(view, close)
		if end < 0 {
			break
		}
		out.WriteString(utils.StripANSI(view[:end]))
		view = view[end+len(close):]
	}
	return out.String()
}

// TestComposerWrappedURLKeepsCompleteDestinationLikeRust mirrors #40720: each
// independently wrapped URL fragment carries the full OSC-8 destination.
func TestComposerWrappedURLKeepsCompleteDestinationLikeRust(t *testing.T) {
	const url = "https://github.com/openai/codex/pull/20252"
	model := newComposerHyperlinkModel(t)
	model.composer.SetValue("Fix CI on " + url + " now")
	view := model.View()
	if count := strings.Count(view, osc8Open(url)); count < 2 {
		t.Fatalf("wrapped URL emitted %d full destinations, want at least 2:\n%q", count, view)
	}
	if got := linkedComposerText(view, url); got != url {
		t.Fatalf("linked wrapped fragments = %q, want %q", got, url)
	}
}

// TestInlineComposerWrappedURLKeepsDestinationLikeRust covers the fixed-height
// inline renderer as well as the owned-screen window.
func TestInlineComposerWrappedURLKeepsDestinationLikeRust(t *testing.T) {
	const url = "https://github.com/openai/codex/pull/20252"
	model := NewModel(codextui.NewState(nil), Options{Width: 40, Height: 20})
	model.composer.SetValue("Fix CI on " + url + " now")
	view := model.View()
	if got := linkedComposerText(view, url); got != url {
		t.Fatalf("inline linked fragments = %q, want %q", got, url)
	}
}

// TestComposerUnicodeWhitespaceURLKeepsDestinationLikeRust covers the shared
// Unicode-whitespace boundary used by the Rust link scanner.
func TestComposerUnicodeWhitespaceURLKeepsDestinationLikeRust(t *testing.T) {
	const url = "https://example.test/path?q=1"
	model := newComposerHyperlinkModel(t)
	model.composer.SetValue("visit\u00a0" + url + "\u00a0now")
	if view := model.View(); !strings.Contains(view, osc8Open(url)) {
		t.Fatalf("Unicode-whitespace URL was not linked:\n%q", view)
	}
}

// TestComposerJoinedEmojiBeforeURLKeepsDestinationLikeRust keeps grapheme
// clusters atomic while mapping source columns to visible cells.
func TestComposerJoinedEmojiBeforeURLKeepsDestinationLikeRust(t *testing.T) {
	const url = "https://example.test/grapheme"
	model := newComposerHyperlinkModel(t)
	model.composer.SetValue("\U0001F469\u200D\U0001F4BB " + url)
	view := model.View()
	if got := linkedComposerText(view, url); got != url {
		t.Fatalf("emoji-prefixed linked text = %q, want %q", got, url)
	}
}

// TestComposerOversizedURLIsNotAnnotatedLikeRust leaves URLs over 8 KiB as
// plain text instead of emitting an unusable terminal destination.
func TestComposerOversizedURLIsNotAnnotatedLikeRust(t *testing.T) {
	url := "https://example.test/" + strings.Repeat("a", 8200)
	model := newComposerHyperlinkModel(t)
	model.composer.SetValue(url)
	view := model.View()
	if strings.Contains(view, "\x1b]8;;"+url+"\x07") {
		t.Fatal("oversized destination was annotated")
	}
}

// TestComposerHyperlinkCacheInvalidatesWithDraftLikeRust reuses link metadata
// while the draft is unchanged and recomputes after an edit.
func TestComposerHyperlinkCacheInvalidatesWithDraftLikeRust(t *testing.T) {
	model := newComposerHyperlinkModel(t)
	model.composer.SetValue("https://example.test/one")
	first := model.composerHyperlinks()
	second := model.composerHyperlinks()
	if len(first) != 1 || len(second) != 1 || &first[0] != &second[0] {
		t.Fatalf("hyperlink cache was not reused: %#v %#v", first, second)
	}
	model.composer.SetValue("https://example.test/two")
	updated := model.composerHyperlinks()
	if len(updated) != 1 || updated[0].destination != "https://example.test/two" {
		t.Fatalf("cache did not invalidate: %#v", updated)
	}
}

// TestComposerScrolledURLKeepsOffscreenDestinationLikeRust keeps the complete
// target when only the tail of a long draft is visible.
func TestComposerScrolledURLKeepsOffscreenDestinationLikeRust(t *testing.T) {
	const url = "https://example.test/alpha/beta/gamma/delta/epsilon"
	model := newComposerHyperlinkModel(t)
	model.composer.SetValue(strings.Repeat("intro ", 120) + url + " end")
	model.View()
	if model.composerScroll <= 0 {
		t.Fatalf("composer scroll = %d, want a scrolled window", model.composerScroll)
	}
	if view := model.View(); !strings.Contains(view, osc8Open(url)) {
		t.Fatalf("scrolled URL lost its complete destination:\n%q", view)
	}
}

// TestComposerURLHyperlinkPreservesSelectionHighlightLikeRust keeps reverse
// video intact around a selected URL fragment.
func TestComposerURLHyperlinkPreservesSelectionHighlightLikeRust(t *testing.T) {
	const url = "https://example.test/alpha/beta"
	model := newComposerHyperlinkModel(t)
	value := "visit " + url
	model.composer.SetValue(value)
	model.View()
	startByte := strings.Index(value, url)
	startRune := utf8.RuneCountInString(value[:startByte])
	model.composerSelection = composerTextSelection{
		active:     true,
		anchorRune: startRune,
		focusRune:  startRune + utf8.RuneCountInString(url),
	}
	view := model.View()
	if !strings.Contains(view, osc8Open(url)) || !strings.Contains(view, "\x1b[7m") {
		t.Fatalf("selection/hyperlink combination was lost:\n%q", view)
	}
}
