package tea

import (
	"strings"
	"testing"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
)

// Rust #45116: an unbracketed multiline paste arrives as individual key events.
// Newlines inside the burst belong to the paste and must not submit.
func TestComposerPasteBurstKeepsMultilinePaste(t *testing.T) {
	state := codextui.NewState(nil)
	var submitted []string
	model := NewModel(state, Options{
		Width:  100,
		Height: 30,
		OnSubmit: func(prompt string) bubbletea.Cmd {
			submitted = append(submitted, prompt)
			return nil
		},
	})
	clock := time.Date(2026, 10, 29, 12, 0, 0, 0, time.UTC)
	model.now = func() time.Time { return clock }

	type event struct {
		rune rune
		key  bubbletea.KeyType
	}
	paste := "draft-row-0\ndraft-row-1\ndraft-row-2"
	events := make([]event, 0, len(paste))
	for _, r := range paste {
		if r == '\n' {
			events = append(events, event{key: bubbletea.KeyEnter})
			continue
		}
		events = append(events, event{rune: r})
	}
	for _, ev := range events {
		clock = clock.Add(time.Millisecond)
		if ev.key != 0 {
			model.Update(bubbletea.KeyMsg{Type: ev.key})
			continue
		}
		model.Update(keyRunes(ev.rune))
	}
	if len(submitted) != 0 {
		t.Fatalf("paste submitted %d message(s): %#v", len(submitted), submitted)
	}
	if got := model.composer.Value(); got != paste {
		t.Fatalf("composer = %q, want %q", got, paste)
	}

	// After the burst window expires a real Enter still submits.
	clock = clock.Add(bottompane.PasteEnterSuppressWindow + time.Millisecond)
	model.Update(key(bubbletea.KeyEnter))
	if len(submitted) != 1 {
		t.Fatalf("submitted = %#v, want one message after the burst", submitted)
	}
	if submitted[0] != paste {
		t.Fatalf("submitted %q, want %q", submitted[0], paste)
	}
}

// Rust paints the `›` live prefix once at the textarea's top row; every
// continuation row keeps a two-column gutter of plain spaces (chat_composer.rs
// draws LIVE_PREFIX_COLS only at textarea_rect.y).
func TestComposerMultilineDraftPaintsSinglePrompt(t *testing.T) {
	model := NewModel(codextui.NewState(nil), Options{Width: 100, Height: 30})
	model.composer.SetValue("draft-row-0\ndraft-row-1\ndraft-row-2")
	view := model.View()

	if got := strings.Count(view, "›"); got != 1 {
		t.Fatalf("view painted %d prompt glyphs, want 1:\n%s", got, view)
	}
	lines := strings.Split(view, "\n")
	start := -1
	for i, line := range lines {
		if strings.Contains(line, "draft-row-0") {
			start = i
			break
		}
	}
	if start < 0 {
		t.Fatalf("draft row missing from view:\n%s", view)
	}
	if !strings.HasPrefix(lines[start], "› draft-row-0") {
		t.Fatalf("first row = %q, want the prompt", lines[start])
	}
	for offset, want := range []string{"  draft-row-1", "  draft-row-2"} {
		row := lines[start+1+offset]
		if !strings.HasPrefix(row, want) {
			t.Fatalf("row %d = %q, want prefix %q", start+1+offset, row, want)
		}
	}
}
