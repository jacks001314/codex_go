package tea

import (
	"sort"
	"strings"

	bottompane "codex_go/tui/bottom_pane"
)

// composerRegisterSpan is one atomic payload inside the single-entry composer
// register: a local image (Path set) or a structured element placeholder.
// Start and End are byte offsets into the register text.
type composerRegisterSpan struct {
	Start       int
	End         int
	Path        string
	Placeholder string
}

// storeComposerRegister installs text as the composer's single-entry register.
// Rust keeps one register for editor kills, Vim yanks and Ctrl-Y
// (textarea.rs kill_buffer plus rich payloads, #57564), so the Vim fields and
// the Ctrl-Y kill buffer are two views of the same state.
func (m *Model) storeComposerRegister(text string, linewise bool, elements []ComposerTextElement, images []composerKillBufferImage) {
	if m == nil {
		return
	}
	m.vimYank = text
	m.vimYankLinewise = linewise
	m.vimRegisterElements = cloneComposerTextElements(elements)
	m.composerKillBuffer = text
	m.composerKillBufferImages = cloneKillBufferImages(images)
}

// composerRegisterSpans resolves the atomic payloads of a register text,
// dropping ranges that no longer match (Rust register::copy keeps only
// elements fully inside the copied range, register::paste re-validates them).
func composerRegisterSpans(text string, elements []ComposerTextElement, images []composerKillBufferImage) []composerRegisterSpan {
	if len(text) == 0 || (len(elements) == 0 && len(images) == 0) {
		return nil
	}
	candidates := make([]composerRegisterSpan, 0, len(elements)+len(images))
	for _, image := range images {
		if image.Start < 0 || image.End > len(text) || image.Start >= image.End || strings.TrimSpace(image.Path) == "" {
			continue
		}
		candidates = append(candidates, composerRegisterSpan{Start: image.Start, End: image.End, Path: image.Path})
	}
	for _, element := range elements {
		if element.Start < 0 || element.End > len(text) || element.Start >= element.End {
			continue
		}
		if text[element.Start:element.End] != element.Placeholder {
			continue
		}
		candidates = append(candidates, composerRegisterSpan{
			Start:       element.Start,
			End:         element.End,
			Placeholder: element.Placeholder,
		})
	}
	sort.SliceStable(candidates, func(i int, j int) bool { return candidates[i].Start < candidates[j].Start })
	spans := make([]composerRegisterSpan, 0, len(candidates))
	for _, candidate := range candidates {
		if len(spans) > 0 && candidate.Start < spans[len(spans)-1].End {
			continue
		}
		spans = append(spans, candidate)
	}
	return spans
}

// insertComposerRegisterText inserts register text at insertAt, re-registering
// its elements and re-attaching local images under fresh labels (Rust #57564
// register::paste). prefix/suffix carry the linewise newline without shifting
// the register's own element offsets. It returns the cursor offset just after
// the inserted text and the offset where the register text starts.
func (m *Model) insertComposerRegisterText(text string, spans []composerRegisterSpan, insertAt int, prefix string, suffix string) (int, int) {
	if m == nil {
		return 0, 0
	}
	before := m.composer.Value()
	if insertAt < 0 {
		insertAt = 0
	}
	if insertAt > len(before) {
		insertAt = len(before)
	}
	var builder strings.Builder
	builder.Grow(len(before) + len(prefix) + len(text) + len(suffix))
	builder.WriteString(before[:insertAt])
	builder.WriteString(prefix)
	contentStart := builder.Len()

	var restored []ComposerTextElement
	position := 0
	for _, span := range spans {
		if span.Start < position || span.End > len(text) {
			continue
		}
		builder.WriteString(text[position:span.Start])
		start := builder.Len()
		if span.Path != "" {
			placeholder := m.nextLocalImageLabel()
			builder.WriteString(placeholder)
			m.attachments = append(m.attachments, bottompane.ComposerAttachment{
				Kind: bottompane.AttachmentImage,
				Path: span.Path,
			})
			restored = append(restored, ComposerTextElement{
				Start:       start,
				End:         start + len(placeholder),
				Placeholder: placeholder,
			})
		} else {
			builder.WriteString(text[span.Start:span.End])
			restored = append(restored, ComposerTextElement{
				Start:       start,
				End:         start + len(span.Placeholder),
				Placeholder: span.Placeholder,
			})
		}
		position = span.End
	}
	builder.WriteString(text[position:])
	cursor := builder.Len() + len(suffix)
	builder.WriteString(suffix)
	builder.WriteString(before[insertAt:])
	after := builder.String()
	m.composer.SetValue(after)
	m.composerElements = transformComposerElements(m.composerElements, before, after)
	m.composerElements = append(m.composerElements, restored...)
	sort.SliceStable(m.composerElements, func(i int, j int) bool { return m.composerElements[i].Start < m.composerElements[j].Start })
	m.vimSetCursorAtByteOffset(cursor)
	return cursor, contentStart
}

func cloneKillBufferImages(images []composerKillBufferImage) []composerKillBufferImage {
	if len(images) == 0 {
		return nil
	}
	out := make([]composerKillBufferImage, len(images))
	copy(out, images)
	return out
}
