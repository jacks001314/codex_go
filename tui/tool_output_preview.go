package tui

import (
	"strconv"
	"unicode/utf8"
)

// Rust parity: codex-rs/tui/src/tool_output.rs (#46492).
//
// A compact tool output preview keeps three screen rows and reports the hidden
// logical lines below them; a partially displayed logical line counts as hidden.
// The full output belongs in the transcript.

const (
	// PreviewLines is the shared rendered-row budget for a compact preview.
	PreviewLines = 3
	// MaxPreviewLineBytes bounds how much of one logical line is considered
	// before wrapping, so a megabyte-long result cannot stall the renderer.
	MaxPreviewLineBytes = 16 * 1024
	// TranscriptHint tells the user where the hidden output lives
	// (Rust TRANSCRIPT_HINT).
	TranscriptHint = "ctrl+t to view transcript"
)

// ToolOutputPreview collects the leading preview rows of a tool result.
type ToolOutputPreview struct {
	lines   []string
	width   int
	omitted int
	full    bool
}

// NewToolOutputPreview creates a preview for the given content width; omitted
// counts logical lines the caller already dropped.
func NewToolOutputPreview(width int, omitted int) *ToolOutputPreview {
	if width < 1 {
		width = 1
	}
	return &ToolOutputPreview{width: width, omitted: omitted}
}

// PushLine adds one logical line, hard-wrapping it so the preview cannot exceed
// its row budget. A line that does not fit completely is reported as hidden - and
// so is every later line, because Rust's preview never skips ahead.
func (p *ToolOutputPreview) PushLine(line string) {
	if p == nil {
		return
	}
	remaining := PreviewLines - len(p.lines)
	if p.full || remaining == 0 {
		p.omitted++
		return
	}
	// Bound the input before wrapping; keep one extra row so cutting a word cannot
	// change the last visible row's wrapping.
	bounded, truncated := previewLinePrefix(line, p.width*(remaining+1), MaxPreviewLineBytes)
	wrapped := AdaptiveWrapLine(bounded, WrapOptions{Width: p.width, BreakWords: true})
	if truncated || len(wrapped) > remaining {
		p.omitted++
		p.full = true
	}
	if len(wrapped) > remaining {
		wrapped = wrapped[:remaining]
	}
	p.lines = append(p.lines, wrapped...)
}

// Finish returns the preview rows plus the hidden-line count and transcript hint.
func (p *ToolOutputPreview) Finish() []string {
	if p == nil {
		return nil
	}
	out := append([]string(nil), p.lines...)
	if p.omitted > 0 {
		unit := "lines"
		if p.omitted == 1 {
			unit = "line"
		}
		marker := "+" + strconv.Itoa(p.omitted) + " " + unit + " (" + TranscriptHint + ")"
		out = append(out, TruncateWithEllipsis(marker, p.width))
	}
	return out
}

// ToolOutputPreviewLines is the one-shot form of Rust's `tool_output_preview`:
// the leading rows of `lines`, with `totalLines` reporting everything the caller
// retains.
func ToolOutputPreviewLines(lines []string, width int, totalLines int) []string {
	preview := NewToolOutputPreview(width, 0)
	consumed := 0
	for index, line := range lines {
		if index >= PreviewLines {
			break
		}
		preview.PushLine(line)
		consumed++
	}
	if totalLines > consumed {
		preview.omitted += totalLines - consumed
	}
	return preview.Finish()
}

// previewLinePrefix bounds one logical line by display columns and bytes,
// reporting whether anything was cut. The byte bound is applied on a rune
// boundary so a combining character cannot be split.
func previewLinePrefix(text string, columns int, maxBytes int) (string, bool) {
	truncated := false
	if maxBytes > 0 && len(text) > maxBytes {
		text = text[:floorRuneBoundary(text, maxBytes)]
		truncated = true
	}
	prefix, rest, _ := TakePrefixByWidth(text, columns)
	if rest != "" {
		truncated = true
	}
	return prefix, truncated
}

func floorRuneBoundary(text string, maxBytes int) int {
	if maxBytes >= len(text) {
		return len(text)
	}
	end := maxBytes
	for end > 0 && !utf8.RuneStart(text[end]) {
		end--
	}
	return end
}
