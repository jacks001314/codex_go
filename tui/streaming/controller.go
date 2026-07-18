package streaming

import (
	"strings"
	"time"

	"codex_go/tui"
	historycell "codex_go/tui/history_cell"
)

// Rust parity: codex-rs/tui/src/streaming/controller.rs.

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
	now               func() time.Time
}

func newStreamCore(width int) streamCore {
	return streamCore{
		width:           width,
		holdbackScanner: NewTableHoldbackScanner(),
		now:             time.Now,
	}
}

func (c *streamCore) pushDelta(delta string) bool {
	if delta == "" {
		return false
	}
	c.pendingSource += delta
	committed := c.commitCompleteSource()
	if committed == "" {
		return false
	}
	c.rawSource += committed
	c.holdbackScanner.PushSourceChunk(committed)
	c.renderedLines = renderSourceLines(c.rawSource, c.width)
	return c.syncStableQueue()
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
	rendered := renderSourceLines(c.rawSource, c.width)
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
	return append([]string(nil), c.renderedLines[start:]...)
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
	c.width = width
	if c.rawSource == "" {
		return
	}
	c.renderedLines = renderSourceLines(c.rawSource, c.width)
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
	*c = newStreamCore(width)
	c.now = now
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
	state := c.holdbackScanner.State()
	if state.Kind == TableHoldbackNone {
		return max(len(c.renderedLines), c.emittedStableLen)
	}
	prefixLen := renderedLineCountBeforeSource(c.rawSource, state.SourceStart, c.width)
	return max(prefixLen, c.emittedStableLen)
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
	cell := historycell.NewAgentMessageCell(lines, !c.headerEmitted)
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

func renderSourceLines(source string, width int) []string {
	source = strings.TrimRight(source, "\n")
	if source == "" {
		return nil
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

func renderedLineCountBeforeSource(source string, sourceStart int, width int) int {
	if sourceStart <= 0 {
		return 0
	}
	if sourceStart > len(source) {
		sourceStart = len(source)
	}
	return len(renderSourceLines(source[:sourceStart], width))
}
