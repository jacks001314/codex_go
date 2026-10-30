package codemode

import (
	"strings"
	"unicode/utf8"

	"codex_go/utils"
)

// Bounded head/tail retention for raw Code Mode output. Text has a rolling
// byte allowance. Whole images and audio clips are admitted in source order
// against independent byte allowances, and item pressure can free space for
// later media (Rust code-mode-runtime `output/output_collection.rs`, #57848).

const (
	// OutputMaxItems limits logical items independently of payload bytes. An
	// observation that fits this limit keeps its item boundaries even if the
	// storage layout changes.
	OutputMaxItems = 32 * 1024
	textBlockBytes = 4096
)

// OutputOmission counts output the collector dropped.
type OutputOmission struct {
	TextBytes  uint64
	TextItems  uint64
	ImageItems uint64
	AudioItems uint64
}

func (o *OutputOmission) combine(other OutputOmission) {
	o.TextBytes += other.TextBytes
	o.TextItems += other.TextItems
	o.ImageItems += other.ImageItems
	o.AudioItems += other.AudioItems
}

// OutputGap records missing output at a boundary in the retained logical items.
// A nonzero text offset marks loss inside a single text item.
type OutputGap struct {
	Boundary utils.OutputBoundary
	Omitted  OutputOmission
}

// CollectedOutput is retained content plus where the collector dropped data.
// Loss stays private to the runtime until the renderer turns it into text.
type CollectedOutput struct {
	Items []ContentItem
	Gaps  []OutputGap
}

func (c *CollectedOutput) pushGap(boundary utils.OutputBoundary, omitted OutputOmission) {
	if n := len(c.Gaps); n > 0 && c.Gaps[n-1].Boundary == boundary {
		c.Gaps[n-1].Omitted.combine(omitted)
		return
	}
	c.Gaps = append(c.Gaps, OutputGap{Boundary: boundary, Omitted: omitted})
}

type retainedKind int

const (
	retainedContent retainedKind = iota
	retainedPartial
	retainedGap
)

// retainedItem is one slot of the collector: intact content, a partially
// evicted text suffix, or an omission marker.
type retainedItem struct {
	kind    retainedKind
	content ContentItem
	partial *partialText
	gap     OutputOmission
}

// partialText stores a partially evicted suffix in blocks so later writes can
// release its oldest bytes without copying the rest. Only the first block has
// an offset.
type partialText struct {
	blocks          []string
	frontOffset     int
	bytes           int
	omitted         uint64
	continuesPrefix bool
}

func contentRetained(item ContentItem) retainedItem {
	return retainedItem{kind: retainedContent, content: item}
}

func gapRetained(loss OutputOmission) retainedItem {
	return retainedItem{kind: retainedGap, gap: loss}
}

// textSuffixItem evicts bytes after the retained prefix and keeps any
// surviving suffix.
func textSuffixItem(text string, prefixLen int, bytesToEvict int) retainedItem {
	tail := text[prefixLen:]
	omitted := ceilCharBoundary(tail, minInt(bytesToEvict, len(tail)))
	suffix := tail[omitted:]
	if suffix == "" {
		return gapRetained(OutputOmission{TextBytes: uint64(omitted), TextItems: 1})
	}
	bytes := len(suffix)
	blocks := make([]string, 0, (bytes/textBlockBytes)+1)
	for suffix != "" {
		end := floorCharBoundary(suffix, minInt(len(suffix), textBlockBytes))
		blocks = append(blocks, suffix[:end])
		suffix = suffix[end:]
	}
	return retainedItem{kind: retainedPartial, partial: &partialText{
		blocks:          blocks,
		bytes:           bytes,
		omitted:         uint64(omitted),
		continuesPrefix: prefixLen > 0,
	}}
}

func (r *retainedItem) contentBytes() int {
	switch r.kind {
	case retainedContent:
		if r.content.Type == "input_text" {
			return len(r.content.Text)
		}
		return 0
	case retainedPartial:
		return r.partial.bytes
	}
	return 0
}

func (r *retainedItem) omission() OutputOmission {
	switch r.kind {
	case retainedContent:
		switch r.content.Type {
		case "input_text":
			return OutputOmission{TextBytes: uint64(len(r.content.Text)), TextItems: 1}
		case "input_image":
			return OutputOmission{ImageItems: 1}
		case "input_audio":
			return OutputOmission{AudioItems: 1}
		}
		return OutputOmission{}
	case retainedPartial:
		return OutputOmission{TextBytes: r.partial.omitted + uint64(r.partial.bytes), TextItems: 1}
	}
	return r.gap
}

func (r *retainedItem) moveInto(output *CollectedOutput) {
	switch r.kind {
	case retainedContent:
		output.Items = append(output.Items, r.content)
	case retainedGap:
		output.pushGap(utils.OutputBoundary{ItemIndex: len(output.Items)}, r.gap)
	case retainedPartial:
		text := r.partial
		// A split suffix continues the preceding prefix. A suffix whose prefix
		// was discarded starts its own logical item.
		if !text.continuesPrefix {
			output.Items = append(output.Items, InputText(""))
		}
		index := len(output.Items) - 1
		textOffset := len(output.Items[index].Text)
		var builder strings.Builder
		builder.Grow(textOffset + text.bytes)
		builder.WriteString(output.Items[index].Text)
		for i, block := range text.blocks {
			start := 0
			if i == 0 {
				start = text.frontOffset
			}
			builder.WriteString(block[start:])
		}
		output.Items[index].Text = builder.String()
		if text.omitted > 0 {
			output.pushGap(
				utils.OutputBoundary{ItemIndex: index, TextOffset: textOffset},
				OutputOmission{TextBytes: text.omitted},
			)
		}
	}
}

type openOutput struct {
	items        []retainedItem
	contentBytes int
}

// mediaBytes tracks independent image and audio URL allowances.
type mediaBytes struct {
	images int
	audio  int
}

// admit reports whether the item fits its allowance and, when it does not,
// the omission it contributes.
func (m *mediaBytes) admit(item *ContentItem) (OutputOmission, bool) {
	var length, used, limit int
	var omission OutputOmission
	switch item.Type {
	case "input_text":
		return OutputOmission{}, true
	case "input_image":
		length, used, limit = len(item.ImageURL), m.images, BoundedOutputMaxImageBytes
		omission = OutputOmission{ImageItems: 1}
	case "input_audio":
		length, used, limit = len(item.AudioURL), m.audio, BoundedOutputMaxAudioBytes
		omission = OutputOmission{AudioItems: 1}
	default:
		return OutputOmission{}, true
	}
	if length > limit-used {
		return omission, false
	}
	if item.Type == "input_image" {
		m.images += length
	} else {
		m.audio += length
	}
	return OutputOmission{}, true
}

// boundedOutput keeps either every individually fitting item in source order
// or a fixed prefix plus a rolling tail with omissions in source order.
type boundedOutput struct {
	open  *openOutput
	split *headTailOutput
	media mediaBytes
}

func newBoundedOutput() *boundedOutput {
	return &boundedOutput{open: &openOutput{}}
}

func (b *boundedOutput) push(item ContentItem) {
	loss, admitted := b.media.admit(&item)
	if !admitted {
		b.pushRetained(gapRetained(loss))
		// Adding the loss marker can evict earlier media from a full
		// observation. Replace only this item's marker if that freed space.
		if b.split != nil && len(b.split.tail) > 0 {
			slot := &b.split.tail[len(b.split.tail)-1]
			if slot.kind == retainedGap && slot.gap == loss {
				if _, ok := b.media.admit(&item); ok {
					*slot = contentRetained(item)
				}
			}
		}
		return
	}
	b.pushRetained(contentRetained(item))
}

func (b *boundedOutput) pushRetained(item retainedItem) {
	bytes := item.contentBytes()
	var tail *[]retainedItem
	var retained *int
	if b.open != nil {
		tail, retained = &b.open.items, &b.open.contentBytes
	} else {
		tail, retained = &b.split.tail, &b.split.contentBytes
	}
	if item.kind == retainedGap {
		if n := len(*tail); n > 0 && (*tail)[n-1].kind == retainedGap {
			(*tail)[n-1].gap.combine(item.gap)
			return
		}
	}
	*tail = append(*tail, item)
	*retained += bytes
	if b.open != nil {
		if b.open.contentBytes <= BoundedOutputMaxBytes && len(b.open.items) <= OutputMaxItems {
			return
		}
		open := b.open
		b.open = nil
		b.split = headTailFromOpen(open)
	}
	b.split.trim(&b.media)
}

func collectRetained(items []retainedItem) CollectedOutput {
	output := CollectedOutput{}
	for i := range items {
		items[i].moveInto(&output)
	}
	return output
}

func (b *boundedOutput) intoCollectedOutput() CollectedOutput {
	if b.open != nil {
		return collectRetained(b.open.items)
	}
	items := make([]retainedItem, 0, len(b.split.head)+len(b.split.tail)+1)
	items = append(items, b.split.head...)
	if b.split.middle != (OutputOmission{}) {
		items = append(items, gapRetained(b.split.middle))
	}
	items = append(items, b.split.tail...)
	return collectRetained(items)
}

type headTailOutput struct {
	head      []retainedItem
	headBytes int
	tail      []retainedItem
	// cursor is the first tail item that might still have text bytes.
	cursor int
	// adjacentGaps counts tail gaps that share one logical item.
	adjacentGaps int
	// contentBytes counts text bytes retained in the whole observation.
	contentBytes int
	// middle holds tail-front gaps that no longer need their own slots.
	middle OutputOmission
}

func headTailFromOpen(open *openOutput) *headTailOutput {
	output := &headTailOutput{
		tail:         open.items,
		contentBytes: open.contentBytes,
	}
	for len(output.head) < OutputMaxItems/2 {
		if len(output.tail) == 0 {
			break
		}
		item := output.tail[0]
		output.tail = output.tail[1:]
		bytes := item.contentBytes()
		remaining := BoundedOutputMaxBytes/2 - output.headBytes
		if bytes <= remaining {
			output.headBytes += bytes
			output.head = append(output.head, item)
		} else if output.contentBytes <= BoundedOutputMaxBytes {
			// Under item pressure a whole item may borrow unused tail bytes.
			output.headBytes += bytes
			output.head = append(output.head, item)
			break
		} else {
			output.tail = append([]retainedItem{item}, output.tail...)
			output.splitFront(remaining)
			break
		}
	}
	return output
}

// splitFront preserves a text prefix and copies only suffix bytes that could
// still fit.
func (h *headTailOutput) splitFront(remaining int) {
	if len(h.tail) == 0 || h.tail[0].kind != retainedContent || h.tail[0].content.Type != "input_text" {
		return
	}
	text := h.tail[0].content.Text
	split := floorCharBoundary(text, remaining)
	if split == 0 {
		return
	}
	h.head = append(h.head, contentRetained(InputText(text[:split])))
	h.headBytes += split
	excess := h.contentBytes - BoundedOutputMaxBytes
	suffix := textSuffixItem(text, split, excess)
	h.contentBytes -= len(text) - split - suffix.contentBytes()
	if suffix.kind == retainedGap && len(h.tail) > 1 && h.tail[1].kind == retainedGap {
		h.adjacentGaps++
	}
	h.tail[0] = suffix
}

func (h *headTailOutput) advanceFront() retainedItem {
	if h.cursor > 0 {
		h.cursor--
	}
	item := h.tail[0]
	h.tail = h.tail[1:]
	if item.kind == retainedGap && len(h.tail) > 0 && h.tail[0].kind == retainedGap {
		h.adjacentGaps--
	}
	return item
}

func (h *headTailOutput) trim(media *mediaBytes) {
	if h.contentBytes > BoundedOutputMaxBytes && h.headBytes > BoundedOutputMaxBytes/2 {
		// Only the last prefix item can have borrowed space. Reconsider it
		// before the middle gap, whose source position follows that item.
		item := h.head[len(h.head)-1]
		h.head = h.head[:len(h.head)-1]
		h.headBytes -= item.contentBytes()
		if h.middle != (OutputOmission{}) {
			h.tail = append([]retainedItem{gapRetained(h.middle)}, h.tail...)
			h.middle = OutputOmission{}
		}
		h.tail = append([]retainedItem{item}, h.tail...)
		h.cursor = 0
		h.splitFront(BoundedOutputMaxBytes/2 - h.headBytes)
	}
	// Skip retained media and preserve each gap between its source neighbors.
	used := h.contentBytes
	for used > BoundedOutputMaxBytes {
		slot := &h.tail[h.cursor]
		oldBytes := slot.contentBytes()
		if oldBytes > 0 {
			excess := used - BoundedOutputMaxBytes
			remaining := oldBytes - excess
			if remaining < 0 {
				remaining = 0
			}
			switch {
			case slot.kind == retainedContent && slot.content.Type == "input_text" && remaining > 0:
				*slot = textSuffixItem(slot.content.Text, 0, excess)
			case slot.kind == retainedPartial && remaining > 0:
				// Later writes may evict more of this same suffix.
				text := slot.partial
				for text.bytes > remaining {
					front := text.blocks[0][text.frontOffset:]
					removed := ceilCharBoundary(front, minInt(text.bytes-remaining, len(front)))
					text.bytes -= removed
					text.omitted += uint64(removed)
					if removed == len(front) {
						text.blocks = text.blocks[1:]
						text.frontOffset = 0
					} else {
						text.frontOffset += removed
					}
				}
				if text.bytes == 0 {
					*slot = gapRetained(slot.omission())
				}
			default:
				*slot = gapRetained(slot.omission())
			}
			newBytes := slot.contentBytes()
			used -= oldBytes - newBytes
			h.contentBytes = used
			if newBytes > 0 {
				break
			}
			if h.cursor > 0 && h.tail[h.cursor-1].kind == retainedGap {
				h.adjacentGaps++
			}
			if h.cursor+1 < len(h.tail) && h.tail[h.cursor+1].kind == retainedGap {
				h.adjacentGaps++
			}
		}
		h.cursor++
	}
	// Each sweep removes at least this many slots. Tiny writes cannot
	// repeatedly scan the tail, even when a live image brackets many gaps.
	if h.adjacentGaps >= ceilDiv(OutputMaxItems, 8) {
		for i := 1; i < len(h.tail); i++ {
			left, right := &h.tail[i-1], &h.tail[i]
			if left.kind == retainedGap && right.kind == retainedGap {
				right.gap.combine(left.gap)
				*left = gapRetained(OutputOmission{})
			}
		}
		compacted := h.tail[:0]
		for _, item := range h.tail {
			if item.kind == retainedGap && item.gap == (OutputOmission{}) {
				continue
			}
			compacted = append(compacted, item)
		}
		h.tail = compacted
		h.cursor = 0
		h.adjacentGaps = 0
	}
	var splitSuffix *retainedItem
	if len(h.tail) > 0 && h.tail[0].kind == retainedPartial && h.tail[0].partial.continuesPrefix {
		// The suffix shares its head item's slot, and no middle gap exists yet.
		if len(h.head)+len(h.tail)-h.adjacentGaps <= OutputMaxItems+1 {
			return
		}
		item := h.advanceFront()
		splitSuffix = &item
	}
	// A middle gap costs a slot only once it exists. The first eviction
	// creates that gap, so recompute the logical item count on each pass.
	for len(h.head)+len(h.tail)-h.adjacentGaps+boolToInt(h.middle != (OutputOmission{})) > OutputMaxItems {
		item := h.advanceFront()
		h.contentBytes -= item.contentBytes()
		if item.kind == retainedContent {
			switch item.content.Type {
			case "input_image":
				media.images -= len(item.content.ImageURL)
			case "input_audio":
				media.audio -= len(item.content.AudioURL)
			}
		}
		h.middle.combine(item.omission())
	}
	for len(h.tail) > 0 && h.tail[0].kind == retainedGap {
		item := h.advanceFront()
		h.middle.combine(item.omission())
	}
	if splitSuffix != nil {
		middle := h.middle
		h.middle = OutputOmission{}
		h.tail = append([]retainedItem{gapRetained(middle)}, h.tail...)
		h.tail = append([]retainedItem{*splitSuffix}, h.tail...)
		h.cursor = 0
	}
}

// outputCollector applies the session's truncation mode while cells run.
type outputCollector struct {
	client []ContentItem
	server *boundedOutput
}

func newOutputCollector(mode OutputTruncation) *outputCollector {
	if mode == OutputTruncationServer {
		return &outputCollector{server: newBoundedOutput()}
	}
	return &outputCollector{}
}

func (c *outputCollector) push(item ContentItem) {
	if c.server != nil {
		c.server.push(item)
		return
	}
	c.client = append(c.client, item)
}

// collectError records a server-mode diagnostic inside the observation budget
// and returns the independently bounded copy the response carries. Client mode
// keeps the original diagnostic for the caller to append and truncate.
func (c *outputCollector) collectError(errorText string) string {
	if c.server == nil {
		return errorText
	}
	c.server.push(InputText(errorText))
	if len(errorText) <= maxServerDiagnosticBytes {
		return errorText
	}
	return utils.TruncateMiddleWithMarker(errorText, maxServerDiagnosticBytes, "…")
}

// take hands the retained state to an observer. A failed send can restore it,
// including any omissions, so retries do not lose accounting.
func (c *outputCollector) take() *outputCollector {
	taken := &outputCollector{client: c.client, server: c.server}
	if c.server != nil {
		c.server = newBoundedOutput()
	} else {
		c.client = nil
	}
	return taken
}

// tryDeliver sends the taken state; a false return restores it unchanged.
func (c *outputCollector) tryDeliver(send func(taken *outputCollector) bool) bool {
	taken := c.take()
	if send(taken) {
		return true
	}
	*c = *taken
	return false
}

func (c *outputCollector) intoCollectedOutput() CollectedOutput {
	if c.server != nil {
		return c.server.intoCollectedOutput()
	}
	return CollectedOutput{Items: c.client}
}

// maxServerDiagnosticBytes bounds the buffered server diagnostic independently
// of model-visible output (Rust #57870).
const maxServerDiagnosticBytes = 64 * 1024

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func boolToInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func ceilDiv(a, b int) int {
	return (a + b - 1) / b
}

func floorCharBoundary(s string, index int) int {
	if index <= 0 {
		return 0
	}
	if index >= len(s) {
		return len(s)
	}
	for index > 0 && !utf8.RuneStart(s[index]) {
		index--
	}
	return index
}

func ceilCharBoundary(s string, index int) int {
	if index <= 0 {
		return 0
	}
	if index >= len(s) {
		return len(s)
	}
	for index < len(s) && !utf8.RuneStart(s[index]) {
		index++
	}
	return index
}
