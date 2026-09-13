package app

import (
	codextea "codex_go/tui/tea"
	"codex_go/turn"
)

// Rust #45255 removed the command center's inline composer (and with it the
// dashboard task-input/image path), so this file now only carries the composer
// text-element mapping still shared with the main prompt path.

// turnTextElementsFromComposer maps the composer's byte-range elements onto the
// turn input's text_elements (Rust UserTextElement: range plus placeholder).
func turnTextElementsFromComposer(elements []codextea.ComposerTextElement) []turn.TextElement {
	if len(elements) == 0 {
		return nil
	}
	out := make([]turn.TextElement, 0, len(elements))
	for _, element := range elements {
		if element.Start < 0 || element.End <= element.Start {
			continue
		}
		placeholder := element.Placeholder
		out = append(out, turn.TextElement{
			ByteRange:   turn.ByteRange{Start: uint(element.Start), End: uint(element.End)},
			Placeholder: &placeholder,
		})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
