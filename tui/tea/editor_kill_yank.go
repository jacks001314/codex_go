package tea

import (
	"sort"
	"strconv"
	"strings"

	bottompane "codex_go/tui/bottom_pane"
	bubbletea "github.com/charmbracelet/bubbletea"
)

// composerKillBufferImage records a local image attachment whose placeholder
// was cut into the composer kill buffer. Start and End are byte offsets inside
// the kill buffer text (Rust #57502 chat_composer::KillBufferSnapshot.images).
type composerKillBufferImage struct {
	Start int
	End   int
	Path  string
}

// applyEditorKillYankKey dispatches the composer editor kill/yank actions that
// the bubbletea textarea does not handle natively, mirroring Rust
// bottom_pane/textarea.rs: kill_line_start (ctrl-u) and kill_line_end (ctrl-k)
// cut the text between the cursor and the line boundary into a single-entry
// kill buffer (at the boundary the adjacent newline is killed instead, like
// Rust), kill_whole_line cuts the current line, and yank (ctrl-y) pastes the
// kill buffer at the cursor. The buffer survives composer-level clears
// (submit / slash dispatch), matching Rust's single-entry kill buffer.
func (m *Model) applyEditorKillYankKey(msg bubbletea.KeyMsg, keySpec string) bool {
	if m == nil || m.modal != nil || m.overlay != nil || m.slashPopup.Active || m.skillPopup.Active {
		return false
	}
	switch {
	case m.keyMatches("editor", "kill_line_start", keySpec):
		m.killComposerToLineStart()
	case m.keyMatches("editor", "kill_line_end", keySpec):
		m.killComposerToLineEnd()
	case m.keyMatches("editor", "kill_whole_line", keySpec):
		m.killComposerWholeLine()
	case m.keyMatches("editor", "yank", keySpec):
		m.yankComposerKillBuffer()
	default:
		return false
	}
	return true
}

// killComposerToLineEnd cuts from the cursor to the end of the current line
// into the kill buffer; at the line end the trailing newline is cut instead.
func (m *Model) killComposerToLineEnd() {
	line := m.vimCurrentLine()
	col := m.vimCursorColumn()
	lineStart := m.vimLineStartByteOffset()
	eol := lineStart + len(line)
	if col >= len(line) {
		// Cursor at the line end: kill the newline when present.
		if eol < len(m.composer.Value()) {
			m.editorKillRange(eol, eol+1)
		}
		return
	}
	m.editorKillRange(lineStart+col, eol)
}

// killComposerToLineStart cuts from the start of the current line to the
// cursor into the kill buffer; at the line start the preceding newline is cut
// instead.
func (m *Model) killComposerToLineStart() {
	col := m.vimCursorColumn()
	lineStart := m.vimLineStartByteOffset()
	if col == 0 {
		if lineStart > 0 {
			m.editorKillRange(lineStart-1, lineStart)
		}
		return
	}
	m.editorKillRange(lineStart, lineStart+col)
}

// killComposerWholeLine cuts the whole current line (including its newline)
// into the kill buffer.
func (m *Model) killComposerWholeLine() {
	line := m.vimCurrentLine()
	lineStart := m.vimLineStartByteOffset()
	end := lineStart + len(line)
	if end < len(m.composer.Value()) {
		end++
	}
	m.editorKillRangeWithKind(lineStart, end, true)
}

// yankComposerKillBuffer pastes the kill buffer at the cursor.
func (m *Model) yankComposerKillBuffer() {
	if m == nil || m.composerKillBuffer == "" {
		return
	}
	elements := m.vimRegisterElements
	if m.composerKillBuffer != m.vimYank {
		// A caller replaced the kill buffer directly; only its image payloads
		// are known to belong to the text.
		elements = nil
	}
	spans := composerRegisterSpans(m.composerKillBuffer, elements, m.composerKillBufferImages)
	if m.isVimReplaceMode() && len(spans) > 0 {
		// Rust's rich yank goes through replace_range, which resets Replace's
		// per-character recovery; the pasted elements stay atomic.
		m.clearVimReplaceRecovery()
	}
	m.insertComposerRegisterText(m.composerKillBuffer, spans, m.vimValueByteOffset(), "", "")
}

// editorKillRange removes [start, end) from the composer value, storing it in
// the single-entry kill buffer (byte offsets; cursor columns are byte-based
// like SetCursor).
func (m *Model) editorKillRange(start, end int) {
	m.editorKillRangeWithKind(start, end, false)
}

// editorKillRangeWithKind removes [start, end) and stores it as the single-entry
// register. linewise mirrors Rust's KillBufferKind for whole-line kills, which
// makes a later Vim p paste as a line.
func (m *Model) editorKillRangeWithKind(start, end int, linewise bool) {
	value := m.composer.Value()
	if start < 0 {
		start = 0
	}
	if end > len(value) {
		end = len(value)
	}
	if start >= end {
		return
	}
	m.storeComposerRegister(value[start:end], linewise, m.vimElementsWithin(start, end), m.killBufferImagesWithin(start, end))
	after := value[:start] + value[end:]
	m.composer.SetValue(after)
	m.composerElements = transformComposerElements(m.composerElements, value, after)
	m.vimSetCursorAtByteOffset(start)
}

// killBufferImagesWithin collects the local images whose placeholder elements
// fall inside the killed range, with ranges relative to the new kill buffer.
func (m *Model) killBufferImagesWithin(start int, end int) []composerKillBufferImage {
	if m == nil || len(m.composerElements) == 0 || len(m.attachments) == 0 {
		return nil
	}
	paths := m.localImagePlaceholderPaths()
	if len(paths) == 0 {
		return nil
	}
	var images []composerKillBufferImage
	for _, element := range m.composerElements {
		if element.Start < start || element.End > end || element.Start >= element.End {
			continue
		}
		path, ok := paths[element.Placeholder]
		if !ok {
			continue
		}
		images = append(images, composerKillBufferImage{
			Start: element.Start - start,
			End:   element.End - start,
			Path:  path,
		})
	}
	sort.Slice(images, func(i int, j int) bool { return images[i].Start < images[j].Start })
	return images
}

// localImagePlaceholderPaths maps each local image's [Image #N] placeholder to
// its path, numbering them like Rust's local_image_label_text across the
// composer's local and remote images.
func (m *Model) localImagePlaceholderPaths() map[string]string {
	if m == nil || len(m.attachments) == 0 {
		return nil
	}
	remote := 0
	for _, attachment := range m.attachments {
		if attachment.Kind == bottompane.AttachmentRemoteImage {
			remote++
		}
	}
	paths := make(map[string]string)
	local := 0
	for _, attachment := range m.attachments {
		if attachment.Kind != bottompane.AttachmentImage || strings.TrimSpace(attachment.Path) == "" {
			continue
		}
		local++
		paths[localImageLabelText(remote+local)] = attachment.Path
	}
	return paths
}

// nextLocalImageLabel returns the placeholder the next local image receives,
// mirroring Rust local_image_label_text(remote + local + 1).
func (m *Model) nextLocalImageLabel() string {
	remote := 0
	local := 0
	if m != nil {
		for _, attachment := range m.attachments {
			switch attachment.Kind {
			case bottompane.AttachmentRemoteImage:
				remote++
			case bottompane.AttachmentImage:
				local++
			}
		}
	}
	return localImageLabelText(remote + local + 1)
}

// localImageLabelText renders a local image placeholder like Rust's
// local_image_label_text.
func localImageLabelText(index int) string {
	return "[Image #" + strconv.Itoa(index) + "]"
}
