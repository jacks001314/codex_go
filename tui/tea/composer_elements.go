package tea

import (
	"strings"
	"unicode/utf8"
)

// Rust parity: codex-rs/tui/src/bottom_pane/textarea.rs text elements. A
// composer element is a byte range inside the draft text whose placeholder the
// model receives in the turn input's text_elements (mentions and attachment
// placeholders). Go's composer is the bubbles textarea, so ranges are tracked
// beside it and re-validated against the current text after every edit: an
// element that no longer matches its placeholder is dropped rather than
// reporting a stale range.
type ComposerTextElement struct {
	Start       int
	End         int
	Placeholder string
}

// composerTextElements returns the valid elements for a draft value, dropping
// any whose bytes no longer match the recorded placeholder.
func composerTextElements(value string, elements []ComposerTextElement) []ComposerTextElement {
	if len(elements) == 0 {
		return nil
	}
	out := make([]ComposerTextElement, 0, len(elements))
	for _, element := range elements {
		if element.Start < 0 || element.End > len(value) || element.Start >= element.End {
			continue
		}
		if value[element.Start:element.End] != element.Placeholder {
			continue
		}
		out = append(out, element)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// transformComposerElements applies the edit between oldValue and newValue to
// the tracked element ranges, mirroring how Rust's textarea keeps element ranges
// valid across edits. Elements touched by a deletion are dropped (Go's composer
// cannot delete them atomically), and every surviving element is re-validated
// against the new text.
func transformComposerElements(elements []ComposerTextElement, oldValue string, newValue string) []ComposerTextElement {
	if len(elements) == 0 || oldValue == newValue {
		return elements
	}
	prefix := commonPrefixLen(oldValue, newValue)
	removedEnd := len(oldValue) - commonSuffixLen(oldValue[prefix:], newValue[prefix:])
	insertedEnd := len(newValue) - commonSuffixLen(oldValue[prefix:], newValue[prefix:])
	removedStart := prefix
	delta := (insertedEnd - prefix) - (removedEnd - removedStart)

	out := make([]ComposerTextElement, 0, len(elements))
	for _, element := range elements {
		if removedEnd > removedStart && element.Start < removedEnd && element.End > removedStart {
			// The edit touched the element; Go cannot expand the edit to the whole
			// element, so drop it instead of reporting a stale range.
			continue
		}
		start, end := element.Start, element.End
		if start >= removedEnd {
			start += delta
			end += delta
		}
		if start < 0 || end > len(newValue) || start >= end || newValue[start:end] != element.Placeholder {
			continue
		}
		out = append(out, ComposerTextElement{Start: start, End: end, Placeholder: element.Placeholder})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// registerComposerElement records a structured element for the current draft.
func (m *Model) registerComposerElement(value string, start int, end int) {
	if m == nil || start < 0 || end > len(value) || start >= end {
		return
	}
	placeholder := value[start:end]
	if strings.TrimSpace(placeholder) == "" {
		return
	}
	for _, existing := range m.composerElements {
		if existing.Start == start && existing.End == end && existing.Placeholder == placeholder {
			return
		}
	}
	m.composerElements = append(m.composerElements, ComposerTextElement{Start: start, End: end, Placeholder: placeholder})
}

// syncComposerElements transforms the tracked element ranges after the composer
// value changed.
func (m *Model) syncComposerElements(before string) {
	if m == nil {
		return
	}
	after := m.composer.Value()
	if after == before {
		return
	}
	m.composerElements = transformComposerElements(m.composerElements, before, after)
}

// currentComposerTextElements returns the draft's valid elements for the submit
// request in canonical-prompt coordinates (Rust textarea::text_elements).
func (m *Model) currentComposerTextElements() []ComposerTextElement {
	if m == nil {
		return nil
	}
	body := m.composer.Value()
	elements := composerTextElements(body, m.composerElements)
	m.composerElements = elements
	return m.composerElementsForPrompt(body, elements, m.composerText())
}

// composerTextElementsForPromptValue maps the tracked elements onto the prompt
// text that is actually submitted (Go trims the composer value before
// submitting), using the raw composer value captured before any reset.
func (m *Model) composerTextElementsForPromptValue(body string, prompt string) []ComposerTextElement {
	if m == nil || len(m.composerElements) == 0 {
		return nil
	}
	elements := composerTextElements(body, m.composerElements)
	m.composerElements = elements
	return m.composerElementsForPrompt(body, elements, prompt)
}

func (m *Model) composerElementsForPrompt(body string, elements []ComposerTextElement, prompt string) []ComposerTextElement {
	if m == nil || len(elements) == 0 {
		return nil
	}
	canonical := m.composerPrefix.text() + body
	leading := len(canonical) - len(strings.TrimLeft(canonical, " \t\r\n"))
	prefixLen := m.composerPrefix.len()
	out := make([]ComposerTextElement, 0, len(elements))
	for _, element := range elements {
		start := element.Start + prefixLen - leading
		end := element.End + prefixLen - leading
		if start < 0 || end > len(prompt) || start >= end {
			continue
		}
		if prompt[start:end] != element.Placeholder {
			continue
		}
		out = append(out, ComposerTextElement{Start: start, End: end, Placeholder: element.Placeholder})
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// restoreComposerTextElements maps canonical prompt ranges back onto the
// editable body after setComposerPrompt has absorbed its leading marker.
func (m *Model) restoreComposerTextElements(prompt string, elements []ComposerTextElement) {
	if m == nil {
		return
	}
	m.composerElements = nil
	body := m.composer.Value()
	prefixLen := m.composerPrefix.len()
	for _, element := range elements {
		start := element.Start - prefixLen
		end := element.End - prefixLen
		if element.Start < 0 || element.End > len(prompt) || start < 0 || end > len(body) || start >= end {
			continue
		}
		if element.Placeholder != "" && prompt[element.Start:element.End] != element.Placeholder {
			continue
		}
		placeholder := body[start:end]
		if strings.TrimSpace(placeholder) == "" {
			continue
		}
		m.composerElements = append(m.composerElements, ComposerTextElement{Start: start, End: end, Placeholder: placeholder})
	}
}

func cloneComposerTextElements(elements []ComposerTextElement) []ComposerTextElement {
	if len(elements) == 0 {
		return nil
	}
	return append([]ComposerTextElement(nil), elements...)
}

func commonPrefixLen(left string, right string) int {
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	index := 0
	for index < limit && left[index] == right[index] {
		index++
	}
	// Keep the split on a UTF-8 boundary so byte ranges stay rune-aligned.
	for index > 0 && index < len(right) && !utf8.RuneStart(right[index]) {
		index--
	}
	return index
}

func commonSuffixLen(left string, right string) int {
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	index := 0
	for index < limit && left[len(left)-1-index] == right[len(right)-1-index] {
		index++
	}
	for index > 0 && index < len(right) && !utf8.RuneStart(right[len(right)-index]) {
		index--
	}
	return index
}
