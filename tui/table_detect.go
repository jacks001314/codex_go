package tui

import "strings"

// Rust parity: codex-rs/tui/src/table_detect.rs.

func ParseTableSegments(line string) ([]string, bool) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return nil, false
	}
	hasOuterPipe := strings.HasPrefix(trimmed, "|") || strings.HasSuffix(trimmed, "|")
	content := strings.TrimPrefix(trimmed, "|")
	content = strings.TrimSuffix(content, "|")
	rawSegments := splitUnescapedPipe(content)
	if !hasOuterPipe && len(rawSegments) <= 1 {
		return nil, false
	}
	segments := make([]string, 0, len(rawSegments))
	for _, segment := range rawSegments {
		segments = append(segments, strings.TrimSpace(segment))
	}
	return segments, len(segments) > 0
}

func IsTableHeaderLine(line string) bool {
	segments, ok := ParseTableSegments(line)
	if !ok {
		return false
	}
	for _, segment := range segments {
		if segment != "" {
			return true
		}
	}
	return false
}

func IsTableDelimiterLine(line string) bool {
	segments, ok := ParseTableSegments(line)
	if !ok {
		return false
	}
	for _, segment := range segments {
		if !isTableDelimiterSegment(segment) {
			return false
		}
	}
	return true
}

func splitUnescapedPipe(content string) []string {
	segments := []string{}
	start := 0
	for i := 0; i < len(content); {
		switch content[i] {
		case '\\':
			i += 2
		case '|':
			segments = append(segments, content[start:i])
			start = i + 1
			i++
		default:
			i++
		}
	}
	return append(segments, content[start:])
}

func isTableDelimiterSegment(segment string) bool {
	trimmed := strings.TrimSpace(segment)
	if trimmed == "" {
		return false
	}
	trimmed = strings.TrimPrefix(trimmed, ":")
	trimmed = strings.TrimSuffix(trimmed, ":")
	return len(trimmed) >= 3 && strings.Trim(trimmed, "-") == ""
}

type FenceKind int

const (
	FenceOutside FenceKind = iota
	FenceMarkdown
	FenceOther
)

type FenceTracker struct {
	state *fenceState
}

type fenceState struct {
	marker rune
	length int
	kind   FenceKind
}

func NewFenceTracker() *FenceTracker {
	return &FenceTracker{}
}

func (t *FenceTracker) Advance(rawLine string) {
	leadingSpaces := 0
	for leadingSpaces < len(rawLine) && rawLine[leadingSpaces] == ' ' {
		leadingSpaces++
	}
	if leadingSpaces > 3 {
		return
	}
	fenceScanText := StripBlockquotePrefix(rawLine[leadingSpaces:])
	marker, length, ok := ParseFenceMarker(fenceScanText)
	if !ok {
		return
	}
	if t.state != nil {
		if marker == t.state.marker && length >= t.state.length && strings.TrimSpace(fenceScanText[length:]) == "" {
			t.state = nil
		}
		return
	}
	kind := FenceOther
	if IsMarkdownFenceInfo(fenceScanText, length) {
		kind = FenceMarkdown
	}
	t.state = &fenceState{marker: marker, length: length, kind: kind}
}

func (t *FenceTracker) Kind() FenceKind {
	if t == nil || t.state == nil {
		return FenceOutside
	}
	return t.state.kind
}

func ParseFenceMarker(line string) (rune, int, bool) {
	if line == "" {
		return 0, 0, false
	}
	first := rune(line[0])
	if first != '`' && first != '~' {
		return 0, 0, false
	}
	length := 0
	for length < len(line) && rune(line[length]) == first {
		length++
	}
	if length < 3 {
		return 0, 0, false
	}
	return first, length, true
}

func IsMarkdownFenceInfo(trimmedLine string, markerLen int) bool {
	if markerLen > len(trimmedLine) {
		return false
	}
	info := strings.Fields(trimmedLine[markerLen:])
	if len(info) == 0 {
		return false
	}
	return strings.EqualFold(info[0], "md") || strings.EqualFold(info[0], "markdown")
}

func StripBlockquotePrefix(line string) string {
	rest := strings.TrimLeft(line, " \t")
	for {
		stripped, ok := strings.CutPrefix(rest, ">")
		if !ok {
			return rest
		}
		stripped = strings.TrimPrefix(stripped, " ")
		rest = strings.TrimLeft(stripped, " \t")
	}
}
