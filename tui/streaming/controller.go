package streaming

import (
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/rivo/uniseg"

	"codex_go/eventmap"
	"codex_go/tui"
	historycell "codex_go/tui/history_cell"
	"codex_go/tui/markdown"
)

// Rust parity: codex-rs/tui/src/streaming/controller.rs.

// maxProsePreviewBytes bounds the disposable prose preview (Rust
// streaming/prose_preview.rs MAX_PREVIEW_BYTES).
const maxProsePreviewBytes = 8192

// prosePreview mirrors Rust's ProsePreview: a bounded, disposable render of the
// unterminated trailing source. Its lines never enter the stable queue or
// scrollback; newline commitment and finalization render the original source
// independently.
type prosePreview struct {
	lines   []string
	scanned int
	safeLen int
	hasPipe bool
}

func (p *prosePreview) reset() {
	*p = prosePreview{}
}

// currentStreamTheme holds the active TUI theme used for streaming markdown
// rendering. It is set by the model each frame; an empty value keeps the
// historical plain-text fallback so streaming behavior is unchanged when no
// theme is configured.
var currentStreamTheme string

func SetStreamTheme(theme string) { currentStreamTheme = theme }
func CurrentStreamTheme() string  { return currentStreamTheme }

type queuedLine struct {
	text       string
	enqueuedAt time.Time
}

type streamCore struct {
	width             int
	rawSource         string
	pendingSource     string
	renderedLines     []string
	queue             []queuedLine
	enqueuedStableLen int
	emittedStableLen  int
	holdbackScanner   *TableHoldbackScanner
	preview           prosePreview
	now               func() time.Time
	hasVisualization  bool
	theme             string
	mathPendingReady  bool
	mathPendingSet    bool
	mathPendingLen    int
	mathPendingWidth  int
}

func newStreamCore(width int) streamCore {
	return streamCore{
		width:           width,
		holdbackScanner: NewTableHoldbackScanner(),
		now:             time.Now,
	}
}

func newStreamCoreWithTheme(width int, theme string) streamCore {
	core := newStreamCore(width)
	core.theme = theme
	return core
}

func (c *streamCore) pushDelta(delta string) bool {
	if delta == "" {
		return false
	}
	// A committed newline ends the current unterminated prose (Rust
	// StreamCore::push_delta resets the preview when the delta contains '\n').
	if strings.Contains(delta, "\n") {
		c.preview.reset()
	}
	c.pendingSource += delta
	committed := c.commitCompleteSource()
	enqueued := false
	if committed != "" {
		c.rawSource += committed
		c.hasVisualization = c.hasVisualization || strings.Contains(committed, tui.InlineVisualizationDirectivePrefix)
		c.holdbackScanner.PushSourceChunk(committed)
		c.renderedLines = c.renderSourceLines(c.rawSource)
		enqueued = c.syncStableQueue()
	}
	return enqueued || c.refreshProsePreview()
}

// previewMode selects how the disposable tail preview renders (Rust
// streaming/prose_preview.rs `PreviewMode`). A math preview shows the
// unterminated display source itself, wrapped to width, without interpreting
// TeX punctuation as Markdown.
type previewMode int

const (
	previewModeProse previewMode = iota
	previewModeMath
)

// refreshProsePreview re-renders the disposable preview of the unterminated
// trailing source and reports whether it changed. Rust StreamCore::refresh_preview.
func (c *streamCore) refreshProsePreview() bool {
	pending := c.pendingSource
	prosePreview := c.holdbackScanner.AllowsProsePreview()
	mode := previewModeProse
	if c.hasPendingDisplayMath() ||
		(prosePreview && (strings.HasPrefix(pending, "$$") || strings.HasPrefix(pending, `\[`))) {
		mode = previewModeMath
	} else if !prosePreview {
		if len(c.preview.lines) == 0 {
			return false
		}
		c.preview.reset()
		return true
	}
	if pending == "" {
		if len(c.preview.lines) == 0 {
			return false
		}
		c.preview.reset()
		return true
	}
	if c.preview.scanned > len(pending) {
		c.preview.reset()
	}
	if c.preview.scanned < len(pending) {
		if strings.Contains(pending[c.preview.scanned:], "|") {
			c.preview.hasPipe = true
		}
		c.preview.scanned = len(pending)
	}
	// Indented and quoted lines may belong to nested code blocks, so their
	// newline holdback is preserved; a pipe (table) or fence start also keeps
	// the previous safe preview instead of guessing at the missing structure.
	// A math preview advances regardless because it shows the source verbatim.
	if mode == previewModeMath || !(c.preview.hasPipe ||
		strings.HasPrefix(pending, " ") ||
		strings.HasPrefix(pending, "\t") ||
		strings.HasPrefix(pending, ">") ||
		strings.HasPrefix(pending, "```") ||
		strings.HasPrefix(pending, "~~~") ||
		pending == "`" || pending == "``" || pending == "~" || pending == "~~") {
		c.preview.safeLen = len(pending)
	}
	safe := pending
	if c.preview.safeLen < len(safe) {
		safe = safe[:c.preview.safeLen]
	}
	start := 0
	if len(safe) > maxProsePreviewBytes {
		start = len(safe) - maxProsePreviewBytes
		for start < len(safe) && !utf8.RuneStart(safe[start]) {
			start++
		}
	}
	var lines []string
	if start < len(safe) {
		if mode == previewModeMath {
			lines = wrapMathPreviewLines(safe[start:], c.width)
		} else {
			lines = c.renderPreviewSourceLines(safe[start:])
		}
	}
	if start > 0 {
		lines = append([]string{"…"}, lines...)
	}
	if slices.Equal(c.preview.lines, lines) {
		return false
	}
	c.preview.lines = lines
	return true
}

// hasPendingDisplayMath reports whether the committed source still ends inside
// an unterminated standalone display equation (Rust
// `StreamingRender::pending_math_start`). The scan is cached per committed
// length and width so per-token pushes do not re-parse the whole source.
func (c *streamCore) hasPendingDisplayMath() bool {
	if c.mathPendingReady && c.mathPendingLen == len(c.rawSource) && c.mathPendingWidth == c.width {
		return c.mathPendingSet
	}
	_, pending := markdown.MathPendingStart(c.rawSource, c.width)
	c.mathPendingReady = true
	c.mathPendingSet = pending
	c.mathPendingLen = len(c.rawSource)
	c.mathPendingWidth = c.width
	return pending
}

func (c *streamCore) commitCompleteSource() string {
	index := strings.LastIndex(c.pendingSource, "\n")
	if index < 0 {
		return ""
	}
	committed := c.pendingSource[:index+1]
	c.pendingSource = c.pendingSource[index+1:]
	return committed
}

func (c *streamCore) finalizeRemaining() []string {
	if c.pendingSource != "" {
		remainder := c.pendingSource
		c.pendingSource = ""
		c.rawSource += remainder
		c.holdbackScanner.PushSourceChunk(remainder)
	}
	rendered := c.renderSourceLines(c.rawSource)
	if c.emittedStableLen >= len(rendered) {
		return nil
	}
	return append([]string(nil), rendered[c.emittedStableLen:]...)
}

func (c *streamCore) tick(limit int) []string {
	if limit <= 0 || len(c.queue) == 0 {
		return nil
	}
	if limit > len(c.queue) {
		limit = len(c.queue)
	}
	lines := make([]string, 0, limit)
	for i := 0; i < limit; i++ {
		lines = append(lines, c.queue[i].text)
	}
	c.queue = append([]queuedLine(nil), c.queue[limit:]...)
	c.emittedStableLen += len(lines)
	return lines
}

func (c *streamCore) isIdle() bool {
	return len(c.queue) == 0
}

func (c *streamCore) queuedLines() int {
	return len(c.queue)
}

func (c *streamCore) oldestQueuedAge(now time.Time) *time.Duration {
	if len(c.queue) == 0 {
		return nil
	}
	age := now.Sub(c.queue[0].enqueuedAt)
	return &age
}

func (c *streamCore) currentTailLines() []string {
	start := min(c.enqueuedStableLen, len(c.renderedLines))
	lines := append([]string(nil), c.renderedLines[start:]...)
	// The committed-source tail may be followed by a bounded prose preview of
	// the unterminated trailing line (Rust StreamCore::current_tail_lines).
	return append(lines, c.preview.lines...)
}

func (c *streamCore) hasTail() bool {
	return c.enqueuedStableLen < len(c.renderedLines)
}

func (c *streamCore) setWidth(width int) {
	if c.width == width {
		return
	}
	hadPendingQueue := len(c.queue) > 0
	hadLiveTail := c.hasTail()
	previousWidth := c.width
	previousTailStart, hadTailStart := c.activeTailSourceStart()
	c.width = width
	if c.rawSource == "" {
		c.refreshProsePreview()
		return
	}
	c.renderedLines = c.renderSourceLines(c.rawSource)
	// Rust StreamCore::set_width re-renders before refresh_preview so the math
	// preview decides from the fresh render and the new width.
	c.refreshProsePreview()
	// Rust StreamCore::recompute_render: the emitted count is a rendered-line
	// count, so when a tail is held back the emitted prefix is carried across the
	// reflow in source terms. Without this, a resize that changes the height of
	// the earlier lines swallows the held tail (Rust #48623
	// resizing_does_not_drop_held_list_marker).
	if hadTailStart {
		previousPrefixLen := renderedLineCountBeforeSource(c.rawSource, previousTailStart, previousWidth, c.theme)
		prefixLen := renderedLineCountBeforeSource(c.rawSource, previousTailStart, c.width, c.theme)
		if c.emittedStableLen >= previousPrefixLen {
			c.emittedStableLen = prefixLen + (c.emittedStableLen - previousPrefixLen)
		} else {
			c.emittedStableLen = min(c.emittedStableLen, prefixLen)
		}
	}
	c.emittedStableLen = min(c.emittedStableLen, len(c.renderedLines))
	if hadPendingQueue && c.emittedStableLen == len(c.renderedLines) && c.emittedStableLen > 0 {
		c.emittedStableLen--
	}
	c.queue = nil
	if c.emittedStableLen > 0 && !hadPendingQueue && !hadLiveTail {
		c.enqueuedStableLen = len(c.renderedLines)
		return
	}
	c.rebuildStableQueueFromRender()
}

func (c *streamCore) reset() {
	width := c.width
	now := c.now
	theme := c.theme
	*c = newStreamCore(width)
	c.now = now
	c.theme = theme
}

func (c *streamCore) syncStableQueue() bool {
	target := c.computeTargetStableLen()
	if target < c.enqueuedStableLen {
		c.queue = nil
		if c.emittedStableLen < target {
			c.enqueue(c.renderedLines[c.emittedStableLen:target])
		}
		c.enqueuedStableLen = target
		return len(c.queue) > 0
	}
	if target == c.enqueuedStableLen {
		return false
	}
	c.enqueue(c.renderedLines[c.enqueuedStableLen:target])
	c.enqueuedStableLen = target
	return true
}

func (c *streamCore) computeTargetStableLen() int {
	if c.hasVisualization {
		return max(len(c.renderedLines), c.emittedStableLen)
	}
	start, held := c.activeTailSourceStart()
	if !held {
		return max(len(c.renderedLines), c.emittedStableLen)
	}
	prefixLen := renderedLineCountBeforeSource(c.rawSource, start, c.width, c.theme)
	return max(prefixLen, c.emittedStableLen)
}

// activeTailSourceStart returns the committed-source offset the mutable tail
// starts at, or false when nothing is held back. Candidates are combined by
// keeping the earliest holdback, matching Rust's `active_tail_source_start`
// (which takes the minimum of the table/fence, mutable-fence, pending-math and
// bare-list-marker starts).
func (c *streamCore) activeTailSourceStart() (int, bool) {
	start, held := 0, false
	if state := c.holdbackScanner.State(); state.Kind != TableHoldbackNone {
		start, held = state.SourceStart, true
	}
	// Rust #48623 (streaming/controller.rs): a trailing bare list marker stays in
	// the mutable tail, so a continuation that arrives in a later chunk, and a
	// terminal resize, both re-render the item from the whole source instead of
	// emitting a marker that the next chunk would change.
	if markerStart, ok := bareListMarkerSourceStart(c.rawSource); ok && (!held || markerStart < start) {
		start, held = markerStart, true
	}
	// Rust #45501 (streaming.rs): a cached prefix must never end inside a
	// display equation, or the streamed copy would diverge from a whole-source
	// render once the closer arrives. Without a line cache the equivalent is to
	// keep the whole span in the mutable tail.
	boundary := len(c.rawSource)
	if held {
		boundary = start
	}
	if mathStart, ok := markdown.MathDisplayTailStart(c.rawSource, boundary, c.width); ok {
		start, held = mathStart, true
	}
	return start, held
}

// bareListMarkerSourceStart reports the source offset of a trailing bare list
// marker in the committed source: a line that contains only "-", "+", "*", or an
// ordinal such as "8." / "8)" (optionally behind blockquote markers). It mirrors
// Rust's `active_tail_source_start` marker probe (#48623).
func bareListMarkerSourceStart(source string) (int, bool) {
	marker := strings.TrimRight(source, "\n")
	start := strings.LastIndexByte(marker, '\n') + 1
	line := strings.TrimSpace(marker[start:])
	line = strings.TrimLeft(line, "> \t")
	switch line {
	case "-", "+", "*":
		return start, true
	}
	if len(line) > 1 && (line[len(line)-1] == '.' || line[len(line)-1] == ')') {
		digits := line[:len(line)-1]
		allDigits := true
		for index := 0; index < len(digits); index++ {
			if digits[index] < '0' || digits[index] > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			return start, true
		}
	}
	return 0, false
}

func (c *streamCore) renderSourceLines(source string) []string {
	if c.hasVisualization || strings.Contains(source, tui.InlineVisualizationDirectivePrefix) {
		source, _ = tui.RewriteInlineVisualizations(source, nil)
	}
	return renderSourceLines(source, c.width, c.theme)
}

func (c *streamCore) renderPreviewSourceLines(source string) []string {
	source = tui.HideUnfinishedFollowupDirective(source)
	if c.hasVisualization || strings.Contains(source, tui.InlineVisualizationDirectivePrefix) {
		source, _ = tui.RewriteInlineVisualizations(source, nil)
	}
	return renderSourceLines(source, c.width, c.theme)
}

func (c *streamCore) rebuildStableQueueFromRender() {
	target := c.computeTargetStableLen()
	c.queue = nil
	if c.emittedStableLen < target {
		c.enqueue(c.renderedLines[c.emittedStableLen:target])
	}
	c.enqueuedStableLen = target
}

func (c *streamCore) enqueue(lines []string) {
	now := c.now()
	for _, line := range lines {
		c.queue = append(c.queue, queuedLine{text: line, enqueuedAt: now})
	}
}

type StreamController struct {
	core          streamCore
	headerEmitted bool
}

func NewStreamController(width int) *StreamController {
	return &StreamController{core: newStreamCore(width)}
}

func NewStreamControllerWithTheme(width int, theme string) *StreamController {
	return &StreamController{core: newStreamCoreWithTheme(width, theme)}
}

// SetTheme updates the markdown rendering theme used for streaming lines so
// the transcript re-renders with the active TUI theme.
func (c *StreamController) SetTheme(theme string) {
	c.core.theme = theme
}

func (c *StreamController) Push(delta string) bool {
	return c.core.pushDelta(delta)
}

func (c *StreamController) OnCommitTick() (historycell.HistoryCell, bool) {
	return c.OnCommitTickBatch(1)
}

func (c *StreamController) OnCommitTickBatch(maxLines int) (historycell.HistoryCell, bool) {
	lines := c.core.tick(maxLines)
	if len(lines) == 0 {
		return nil, c.core.isIdle()
	}
	cell := historycell.NewPrewrappedAgentMessageCell(lines, !c.headerEmitted)
	c.headerEmitted = true
	return cell, c.core.isIdle()
}

func (c *StreamController) Finalize() (historycell.HistoryCell, string) {
	source := c.core.rawSource + c.core.pendingSource
	remaining := c.core.finalizeRemaining()
	if len(remaining) == 0 {
		c.core.reset()
		return nil, source
	}
	cell := historycell.NewAgentMessageCell(remaining, !c.headerEmitted)
	c.core.reset()
	c.headerEmitted = false
	return cell, source
}

func (c *StreamController) SetWidth(width int) {
	c.core.setWidth(width)
}

func (c *StreamController) QueuedLines() int {
	return c.core.queuedLines()
}

func (c *StreamController) OldestQueuedAge(now time.Time) *time.Duration {
	return c.core.oldestQueuedAge(now)
}

func (c *StreamController) CurrentTailLines() []string {
	return c.core.currentTailLines()
}

func (c *StreamController) HasLiveTail() bool {
	return c != nil && c.core.hasTail()
}

func (c *StreamController) TailStartsStream() bool {
	return c != nil && !c.headerEmitted && c.core.emittedStableLen == 0
}

type PlanStreamController struct {
	core streamCore
}

func NewPlanStreamController(width int) *PlanStreamController {
	return &PlanStreamController{core: newStreamCore(width)}
}

func (c *PlanStreamController) Push(delta string) bool {
	return c.core.pushDelta(delta)
}

func (c *PlanStreamController) OnCommitTick() (historycell.HistoryCell, bool) {
	return c.OnCommitTickBatch(1)
}

func (c *PlanStreamController) OnCommitTickBatch(maxLines int) (historycell.HistoryCell, bool) {
	lines := c.core.tick(maxLines)
	if len(lines) == 0 {
		return nil, c.core.isIdle()
	}
	return historycell.NewPlainHistoryCell(lines), c.core.isIdle()
}

func (c *PlanStreamController) Finalize() (historycell.HistoryCell, string) {
	source := c.core.rawSource + c.core.pendingSource
	remaining := c.core.finalizeRemaining()
	c.core.reset()
	if len(remaining) == 0 {
		return nil, source
	}
	return historycell.NewProposedPlan(strings.Join(remaining, "\n")), source
}

func (c *PlanStreamController) SetWidth(width int) {
	c.core.setWidth(width)
}

func (c *PlanStreamController) QueuedLines() int {
	return c.core.queuedLines()
}

func (c *PlanStreamController) OldestQueuedAge(now time.Time) *time.Duration {
	return c.core.oldestQueuedAge(now)
}

func (c *PlanStreamController) CurrentTailDisplayLines() []string {
	return c.core.currentTailLines()
}

func (c *PlanStreamController) HasLiveTail() bool {
	return c != nil && c.core.hasTail()
}

// wrapMathPreviewLines renders the unfinished display-math tail the way Rust's
// `PreviewMode::Math` does: the original source wrapped to width without
// interpreting TeX or Markdown punctuation (streaming/prose_preview.rs uses
// `textwrap::wrap`). Wrapping follows Unicode line-break opportunities so a
// formula breaks where Rust breaks it, keeps the first line's leading
// whitespace, and only splits inside a segment that cannot fit on its own.
func wrapMathPreviewLines(source string, width int) []string {
	if width <= 0 {
		return []string{source}
	}
	lines := []string{}
	for _, line := range strings.Split(strings.ReplaceAll(source, "\r\n", "\n"), "\n") {
		lines = append(lines, wrapMathPreviewLine(line, width)...)
	}
	return lines
}

func wrapMathPreviewLine(line string, width int) []string {
	segments := lineBreakSegments(line)
	out := []string{}
	var current strings.Builder
	currentWidth := 0
	flush := func() {
		if current.Len() == 0 {
			return
		}
		out = append(out, strings.TrimRight(current.String(), " \t"))
		current.Reset()
		currentWidth = 0
	}
	for _, segment := range segments {
		blank := strings.TrimSpace(segment) == ""
		if blank && currentWidth == 0 && len(out) > 0 {
			// textwrap drops whitespace at the start of a continuation line.
			continue
		}
		segmentWidth := tui.DisplayWidth(segment)
		if currentWidth > 0 && currentWidth+segmentWidth > width {
			flush()
			if blank {
				continue
			}
		}
		if !blank && segmentWidth > width {
			for _, piece := range splitPreviewSegment(segment, width) {
				flush()
				current.WriteString(piece)
				currentWidth = tui.DisplayWidth(piece)
				flush()
			}
			continue
		}
		current.WriteString(segment)
		currentWidth += segmentWidth
	}
	flush()
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

// lineBreakSegments splits text at every Unicode line-break opportunity
// (UAX #14), the separator Rust's textwrap uses for math previews.
func lineBreakSegments(text string) []string {
	segments := []string{}
	remaining, state := text, -1
	for remaining != "" {
		segment, rest, _, next := uniseg.FirstLineSegmentInString(remaining, state)
		if segment == "" && rest == remaining {
			break
		}
		segments = append(segments, segment)
		remaining, state = rest, next
	}
	return segments
}

// splitPreviewSegment splits an overlong run into display-width pieces, the
// equivalent of textwrap's `break_words`.
func splitPreviewSegment(segment string, width int) []string {
	if width <= 0 {
		return []string{segment}
	}
	out := []string{}
	var current strings.Builder
	used := 0
	graphemes := uniseg.NewGraphemes(segment)
	for graphemes.Next() {
		grapheme := graphemes.Str()
		graphemeWidth := tui.DisplayWidth(grapheme)
		if used > 0 && used+graphemeWidth > width {
			out = append(out, current.String())
			current.Reset()
			used = 0
		}
		current.WriteString(grapheme)
		used += graphemeWidth
	}
	if current.Len() > 0 {
		out = append(out, current.String())
	}
	return out
}

func renderSourceLines(source string, width int, theme string) []string {
	// Keep provider citation controls out of the human transcript. The raw
	// source remains available from Finalize for protocol/history consumers.
	source = eventmap.StripHiddenAssistantMarkup(source, false)
	source = strings.TrimRight(source, "\n")
	if source == "" {
		return nil
	}
	// When a theme is active, render the in-progress markdown so the streaming
	// transcript shows styling/code highlighting (Rust StreamingMarkdownRender).
	// The unclosed-block holdback keeps the earlier stable blocks in place.
	if theme != "" && width > 0 {
		if rendered, err := markdown.RenderWithThemeCwd(source, width, theme, ""); err == nil && rendered != "" {
			lines := strings.Split(strings.ReplaceAll(rendered, "\r\n", "\n"), "\n")
			for len(lines) > 0 && lines[len(lines)-1] == "" {
				lines = lines[:len(lines)-1]
			}
			return lines
		}
	}
	raw := strings.Split(source, "\n")
	if width <= 0 {
		return raw
	}
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		out = append(out, tui.AdaptiveWrapLine(line, tui.WrapOptions{
			Width:      width,
			BreakWords: true,
		})...)
	}
	return out
}

func renderedLineCountBeforeSource(source string, sourceStart int, width int, theme string) int {
	if sourceStart <= 0 {
		return 0
	}
	if sourceStart > len(source) {
		sourceStart = len(source)
	}
	return len(renderSourceLines(source[:sourceStart], width, theme))
}
