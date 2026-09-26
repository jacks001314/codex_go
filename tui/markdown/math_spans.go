package markdown

import (
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	gmtext "github.com/yuin/goldmark/text"

	codextui "codex_go/tui"
)

// Rust parity: codex-rs/tui/src/markdown_render/math.rs's source scan
// (`MathMarkdown`). Go renders Markdown with goldmark/glamour, so the scan works
// at the source level like the long-URL protection: an admitted math span is
// replaced with a unique placeholder before rendering and restored to its
// rendered Unicode afterwards, while rejected expressions stay verbatim. Code,
// links, HTML and block containers keep the ordinary Markdown renderer.

// mathPlaceholderPrefix/…Suffix build a placeholder that goldmark cannot
// reinterpret: underscores (as the long-URL placeholder uses) would let an
// emphasis run split the token, while a leading letter would make `<token>` a
// valid raw-HTML tag and swallow a masked span inside angle brackets. A
// leading digit keeps the token ordinary text everywhere.
const (
	mathPlaceholderPrefix = "8MATHPROT"
	mathPlaceholderSuffix = "END"
)

// mathSpanScan is the full result of Rust's `MathMarkdown::new`: the masked
// rendering copy, the placeholder restorations, and the streaming bookkeeping
// (`pending_start`/`display_ranges`) that Rust's tests pin even though the
// renderer consumes only the first two.
type mathSpanScan struct {
	placeholders map[string]string
	masked       string
	// pendingStart is the line start of an unterminated standalone display that
	// the streaming layer keeps mutable (Rust's `pending_start`).
	pendingStart *int
	// displayRanges are the source ranges of every matched or unterminated
	// standalone display (Rust's `display_ranges`).
	displayRanges [][2]int
}

// protectMathSpans masks admitted math spans in the source, returning the
// placeholder map and the masked text. It is a no-op when math rendering is
// disabled or the source cannot contain math.
func protectMathSpans(text string, width int) (map[string]string, string) {
	result := analyzeMathSpans(text, width)
	return result.placeholders, result.masked
}

// analyzeMathSpans runs the source scan and returns every field Rust's
// `MathMarkdown::new` records.
func analyzeMathSpans(text string, width int) mathSpanScan {
	result := mathSpanScan{masked: text}
	if !CurrentRendering().Math {
		return result
	}
	if !strings.Contains(text, "$") && !strings.Contains(text, `\(`) && !strings.Contains(text, `\[`) {
		return result
	}
	document := goldmark.DefaultParser().Parse(gmtext.NewReader([]byte(text)))
	math := &mathScanner{
		text:       text,
		width:      width,
		protected:  mathProtectedRanges(document, text),
		containers: mathContainerRanges(document),
	}
	math.scan()
	if len(math.placeholders) == 0 {
		return result
	}
	result.placeholders = math.placeholders
	result.masked = math.masked.String()
	result.pendingStart = math.pendingStart
	result.displayRanges = math.displayRanges
	return result
}

type mathScanner struct {
	text            string
	width           int
	protected       [][2]int
	containers      [][2]int
	masked          strings.Builder
	placeholders    map[string]string
	nextPlaceholder int
	pendingStart    *int
	displayRanges   [][2]int
}

func (s *mathScanner) placeholderFor(rendered string) string {
	if s.placeholders == nil {
		s.placeholders = map[string]string{}
		s.masked.Grow(len(s.text))
	}
	placeholder := mathPlaceholderPrefix + strconv.Itoa(s.nextPlaceholder) + mathPlaceholderSuffix
	s.nextPlaceholder++
	for strings.Contains(s.text, placeholder) {
		placeholder = mathPlaceholderPrefix + strconv.Itoa(s.nextPlaceholder) + mathPlaceholderSuffix
		s.nextPlaceholder++
	}
	s.placeholders[placeholder] = rendered
	return placeholder
}

// scan mirrors Rust's `MathMarkdown::new` loop: walk the source, skip protected
// and container ranges, classify an opener, find the matching closer and either
// mask an admitted span with a placeholder or leave the source verbatim.
func (s *mathScanner) scan() {
	text := s.text
	offset, scanned, lineStart := 0, 0, 0
	lineHasText := false
	protectedIdx, containerIdx := 0, 0
	cursor := 0
	emitTo := func(end int) {
		if end > cursor {
			s.masked.WriteString(text[cursor:end])
			cursor = end
		}
	}
	for offset < len(text) {
		if index := strings.LastIndexByte(text[scanned:offset], '\n'); index >= 0 {
			lineStart = scanned + index + 1
			lineHasText = false
		}
		segmentStart := scanned
		if lineStart > segmentStart {
			segmentStart = lineStart
		}
		if strings.TrimSpace(text[segmentStart:offset]) != "" {
			lineHasText = true
		}
		scanned = offset
		for protectedIdx < len(s.protected) && s.protected[protectedIdx][1] <= offset {
			protectedIdx++
		}
		for containerIdx < len(s.containers) && s.containers[containerIdx][1] <= offset {
			containerIdx++
		}
		if protectedIdx < len(s.protected) &&
			offset >= s.protected[protectedIdx][0] && offset < s.protected[protectedIdx][1] {
			offset = s.protected[protectedIdx][1]
			continue
		}
		rest := text[offset:]
		open, close, display, ok := mathOpenDelimiter(rest)
		if !ok {
			_, size := utf8.DecodeRuneInString(rest)
			offset += size
			continue
		}
		start := offset
		offset += len(open)
		if mathEscaped(text, start) {
			// glamour keeps a backslash-escaped dollar verbatim where Rust's
			// parser unescapes `\$` to `$`; mask the pair so the rendered copy
			// carries the literal dollar.
			if open == "$" {
				emitTo(start - 1)
				s.masked.WriteString(s.placeholderFor("$"))
				cursor = start + 1
				offset = start + 1
			}
			continue
		}
		body := text[offset:]
		if open == "$" && mathBodyRejected(body) {
			continue
		}
		limit := mathByteLimit(body)
		rejectedDisplay := display && lineHasText
		// Only standalone displays can retain an arbitrarily distant closer.
		search := body
		if !(display && !rejectedDisplay) {
			search = body[:limit]
		}
		multilineClose := false
		rejectedClose := false
		end := -1
		closingIdx := protectedIdx
		for _, index := range matchByteIndexes(search, close[0]) {
			position := offset + index
			if !strings.HasPrefix(search[index:], close) || mathEscaped(text, position) {
				continue
			}
			if display {
				for closingIdx < len(s.protected) && s.protected[closingIdx][1] <= position {
					closingIdx++
				}
				if closingIdx < len(s.protected) && s.protected[closingIdx][0] < position+len(close) {
					continue
				}
				if !rejectedDisplay && mathTextBeforeNewline(text[position+len(close):]) {
					if multilineClose || strings.Contains(text[offset:position], "\n") {
						multilineClose = true
						continue
					}
					rejectedClose = true
				}
			}
			end = position
			break
		}
		// Every matched or still-open standalone display records its source span,
		// so a streamed preview can keep the region mutable (Rust pushes before
		// the rejected-display handling).
		if display && (!rejectedDisplay || end >= 0 || len(body) < maxMathBytes) {
			endOffset := len(text)
			if end >= 0 {
				endOffset = end + len(close)
			}
			s.displayRanges = append(s.displayRanges, [2]int{start, endOffset})
		}
		// Retain rejected pairing only within the lookahead window, so shell PID
		// dollars cannot keep the whole streamed response mutable.
		if rejectedDisplay || rejectedClose {
			if end >= 0 && strings.TrimSpace(text[offset:end]) != "" {
				if rejectedDisplay && open == "$$" &&
					mathEndsOnBlankPrefix(text[offset:end]) &&
					mathTextBeforeNewline(text[end+len(close):]) {
					continue
				}
				offset = end + len(close)
			} else if end < 0 && open == `\[` && len(body) < maxMathBytes {
				break
			}
			continue
		}
		if end < 0 {
			if display {
				// Streamed display content with no closer yet stays inert: the
				// tail is masked so the Markdown parser sees text, but restored
				// verbatim afterwards (Rust records the tail as a replacement).
				if len(body) < maxMathBytes && s.pendingStart == nil {
					line := lineStart
					s.pendingStart = &line
				}
				emitTo(start)
				s.masked.WriteString(s.placeholderFor(text[start:]))
				cursor = len(text)
				break
			}
			continue
		}
		spanEnd := end + len(close)
		formula := text[offset:end]
		if display {
			offset = spanEnd
		}
		if protectedIdx < len(s.protected) && s.protected[protectedIdx][0] < spanEnd {
			continue
		}
		if !display && strings.Contains(formula, "\n") {
			continue
		}
		if open == "$" {
			// Rust rejects the two prose cases in order: a trailing-whitespace or
			// alphanumeric-adjacent formula leaves the cursor after the opener,
			// while the bare-number/initialism cases consume the whole span.
			next, hasNext := mathNextRune(text[spanEnd:])
			if mathFormulaTrailingWhitespace(formula) || (hasNext && isAlnumRune(next)) {
				continue
			}
			if mathFormulaDigitsOrInitialism(formula) {
				offset = spanEnd
				continue
			}
		}
		rendered, ok := mathRender(formula, display)
		if !ok || !mathRenderFits(rendered, s.width, s.containers, containerIdx, start) {
			continue
		}
		emitTo(start)
		s.masked.WriteString(s.placeholderFor(rendered))
		cursor = spanEnd
		offset = spanEnd
	}
	emitTo(len(text))
}

// mathFormulaDigitsOrInitialism reports the numeric/initialism rejections that,
// unlike the other prose cases, consume the whole span before continuing
// (Rust advances `offset` for them only).
func mathFormulaDigitsOrInitialism(formula string) bool {
	if formula != "0" && startsWithASCIIDigit(formula) && !strings.ContainsAny(formula, `\^_=+-*/<>`) {
		return true
	}
	return len(formula) > 1 && allASCIIUppercase(formula)
}

func mathBodyRejected(body string) bool {
	if body == "" {
		return false
	}
	first, _ := utf8.DecodeRuneInString(body)
	return unicode.IsSpace(first) || strings.HasPrefix(body, "(") || strings.HasPrefix(body, "{")
}

func mathNextRune(text string) (rune, bool) {
	if text == "" {
		return 0, false
	}
	character, _ := utf8.DecodeRuneInString(text)
	return character, true
}

// mathByteLimit mirrors Rust's first-char-boundary index at or beyond
// MAX_MATH_BYTES, defaulting to the whole body.
func mathByteLimit(body string) int {
	for index := range body {
		if index >= maxMathBytes {
			return index
		}
	}
	return len(body)
}

// mathTextBeforeNewline reports whether any non-whitespace character precedes
// the next newline (Rust's `take_while(|ch| *ch != '\n').any(|ch| !ch.is_whitespace())`).
func mathTextBeforeNewline(text string) bool {
	for _, character := range text {
		if character == '\n' {
			return false
		}
		if !unicode.IsSpace(character) {
			return true
		}
	}
	return false
}

// mathEndsOnBlankPrefix mirrors Rust's rejected-display guard: the text before
// the closer ends with a newline whose prefix is blank.
func mathEndsOnBlankPrefix(text string) bool {
	index := strings.LastIndexByte(text, '\n')
	if index < 0 {
		return false
	}
	return strings.TrimSpace(text[index+1:]) == ""
}

// mathRenderFits mirrors Rust's rendered filter: a spatial (multi-line) layout is
// kept only at the top level and only when every line fits the width minus four
// columns.
func mathRenderFits(rendered string, width int, containers [][2]int, containerIdx int, start int) bool {
	if !strings.Contains(rendered, "\n") {
		return true
	}
	if containerIdx < len(containers) && start >= containers[containerIdx][0] && start < containers[containerIdx][1] {
		return false
	}
	if width <= 0 {
		return true
	}
	for _, line := range strings.Split(rendered, "\n") {
		if codextui.DisplayWidth(line) > width-4 {
			return false
		}
	}
	return true
}

// matchByteIndexes lists the byte indexes of every occurrence of target in text.
func matchByteIndexes(text string, target byte) []int {
	indexes := []int{}
	for index := 0; index < len(text); index++ {
		if text[index] == target {
			indexes = append(indexes, index)
		}
	}
	return indexes
}

// restoreMathPlaceholders restores the rendered Unicode in place of the
// placeholders (Rust's `events` swap of the rendered text).
func restoreMathPlaceholders(rendered string, placeholders map[string]string) string {
	if len(placeholders) == 0 {
		return rendered
	}
	for placeholder, text := range placeholders {
		rendered = strings.ReplaceAll(rendered, placeholder, text)
	}
	return rendered
}

// mathProtectedRanges mirrors Rust's protected set: code spans/blocks, links and
// images, and HTML.
func mathProtectedRanges(document ast.Node, source string) [][2]int {
	ranges := collectCodeRanges(document)
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch block := node.(type) {
		case *ast.HTMLBlock:
			ranges = appendCodeBlockRanges(ranges, block.Lines())
		case *ast.RawHTML:
			if block.Segments != nil {
				for index := 0; index < block.Segments.Len(); index++ {
					segment := block.Segments.At(index)
					ranges = append(ranges, [2]int{segment.Start, segment.Stop})
				}
			}
		case *ast.Link:
			if span, ok := mathLinkSourceRange(block, source); ok {
				ranges = append(ranges, span)
			}
		case *ast.Image:
			if span, ok := mathLinkSourceRange(block, source); ok {
				ranges = append(ranges, span)
			}
		}
		return ast.WalkContinue, nil
	})
	sort.Slice(ranges, func(i int, j int) bool { return ranges[i][0] < ranges[j][0] })
	return ranges
}

// mathLinkSourceRange spans a link/image construct — the label brackets plus
// the inline destination parentheses — so dollars inside either stay under the
// ordinary Markdown renderer (Rust protects `Tag::Link`/`Tag::Image` ranges).
func mathLinkSourceRange(node ast.Node, source string) ([2]int, bool) {
	labelStart, labelEnd := -1, -1
	_ = ast.Walk(node, func(child ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		if text, ok := child.(*ast.Text); ok {
			if labelStart < 0 || text.Segment.Start < labelStart {
				labelStart = text.Segment.Start
			}
			if text.Segment.Stop > labelEnd {
				labelEnd = text.Segment.Stop
			}
		}
		return ast.WalkContinue, nil
	})
	if labelStart < 0 || labelEnd < 0 {
		return [2]int{}, false
	}
	open := labelStart - 1
	if node.Kind() == ast.KindImage && open > 0 && source[open-1] == '!' {
		open--
	}
	if open < 0 {
		open = labelStart
	}
	labelClose := labelEnd
	for labelClose < len(source) && source[labelClose] != ']' {
		labelClose++
	}
	if labelClose+1 >= len(source) || source[labelClose+1] != '(' {
		return [2]int{open, labelClose + 1}, true
	}
	index := labelClose + 2
	if index < len(source) && source[index] == '<' {
		if offset := strings.IndexByte(source[index:], '>'); offset >= 0 {
			return [2]int{open, index + offset + 1}, true
		}
		return [2]int{open, labelClose + 1}, true
	}
	depth := 1
	for index < len(source) && depth > 0 {
		switch source[index] {
		case '\\':
			index++
		case '(':
			depth++
		case ')':
			depth--
		}
		index++
	}
	return [2]int{open, index}, true
}

// mathContainerRanges mirrors Rust's container set: the span of every list and
// block quote, so a spatial layout nested in one keeps its source.
func mathContainerRanges(document ast.Node) [][2]int {
	containers := [][2]int{}
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node.(type) {
		case *ast.List, *ast.Blockquote:
		default:
			return ast.WalkContinue, nil
		}
		start, end := -1, -1
		_ = ast.Walk(node, func(child ast.Node, childEntering bool) (ast.WalkStatus, error) {
			if !childEntering {
				return ast.WalkContinue, nil
			}
			switch inner := child.(type) {
			case *ast.Text:
				if start < 0 || inner.Segment.Start < start {
					start = inner.Segment.Start
				}
				if inner.Segment.Stop > end {
					end = inner.Segment.Stop
				}
			case *ast.CodeBlock:
				start, end = extendSegmentRange(start, end, inner.Lines())
			case *ast.FencedCodeBlock:
				start, end = extendSegmentRange(start, end, inner.Lines())
			case *ast.HTMLBlock:
				start, end = extendSegmentRange(start, end, inner.Lines())
			}
			return ast.WalkContinue, nil
		})
		if start >= 0 && end > start {
			containers = append(containers, [2]int{start, end})
		}
		return ast.WalkSkipChildren, nil
	})
	sort.Slice(containers, func(i int, j int) bool { return containers[i][0] < containers[j][0] })
	return containers
}

func extendSegmentRange(start int, end int, lines *gmtext.Segments) (int, int) {
	if lines == nil {
		return start, end
	}
	for index := 0; index < lines.Len(); index++ {
		segment := lines.At(index)
		if start < 0 || segment.Start < start {
			start = segment.Start
		}
		if segment.Stop > end {
			end = segment.Stop
		}
	}
	return start, end
}
