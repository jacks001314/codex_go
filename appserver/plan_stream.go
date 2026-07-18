package appserver

import "strings"

const (
	proposedPlanOpenTag  = "<proposed_plan>"
	proposedPlanCloseTag = "</proposed_plan>"
)

type proposedPlanSegmentKind int

const (
	proposedPlanSegmentNormal proposedPlanSegmentKind = iota
	proposedPlanSegmentStart
	proposedPlanSegmentDelta
	proposedPlanSegmentEnd
)

type proposedPlanSegment struct {
	Kind proposedPlanSegmentKind
	Text string
}

type proposedPlanStreamParser struct {
	buffer string
	inPlan bool
}

func newProposedPlanStreamParser() *proposedPlanStreamParser {
	return &proposedPlanStreamParser{}
}

func (p *proposedPlanStreamParser) push(delta string) []proposedPlanSegment {
	if p == nil || delta == "" {
		return nil
	}
	p.buffer += delta
	return p.drain(false)
}

func (p *proposedPlanStreamParser) finish() []proposedPlanSegment {
	if p == nil {
		return nil
	}
	segments := p.drain(true)
	if p.inPlan {
		if p.buffer != "" {
			segments = append(segments, proposedPlanSegment{Kind: proposedPlanSegmentDelta, Text: p.buffer})
			p.buffer = ""
		}
		p.inPlan = false
		segments = append(segments, proposedPlanSegment{Kind: proposedPlanSegmentEnd})
	}
	if p.buffer != "" {
		segments = append(segments, proposedPlanSegment{Kind: proposedPlanSegmentNormal, Text: p.buffer})
		p.buffer = ""
	}
	return segments
}

func (p *proposedPlanStreamParser) drain(allowEOF bool) []proposedPlanSegment {
	segments := []proposedPlanSegment{}
	for {
		if p.inPlan {
			start, end, ok := findProposedPlanTagLine(p.buffer, proposedPlanCloseTag, allowEOF)
			if !ok {
				flush := len(p.buffer) - longestTagPrefixSuffix(p.buffer, proposedPlanCloseTag)
				if flush <= 0 {
					return segments
				}
				segments = append(segments, proposedPlanSegment{
					Kind: proposedPlanSegmentDelta,
					Text: p.buffer[:flush],
				})
				p.buffer = p.buffer[flush:]
				return segments
			}
			if start > 0 {
				segments = append(segments, proposedPlanSegment{
					Kind: proposedPlanSegmentDelta,
					Text: p.buffer[:start],
				})
			}
			p.buffer = p.buffer[end:]
			p.inPlan = false
			segments = append(segments, proposedPlanSegment{Kind: proposedPlanSegmentEnd})
			continue
		}

		start, end, ok := findProposedPlanTagLine(p.buffer, proposedPlanOpenTag, allowEOF)
		if !ok {
			flush := len(p.buffer) - longestTagPrefixSuffix(p.buffer, proposedPlanOpenTag)
			if flush <= 0 {
				return segments
			}
			segments = append(segments, proposedPlanSegment{
				Kind: proposedPlanSegmentNormal,
				Text: p.buffer[:flush],
			})
			p.buffer = p.buffer[flush:]
			return segments
		}
		if start > 0 {
			segments = append(segments, proposedPlanSegment{
				Kind: proposedPlanSegmentNormal,
				Text: p.buffer[:start],
			})
		}
		p.buffer = p.buffer[end:]
		p.inPlan = true
		segments = append(segments, proposedPlanSegment{Kind: proposedPlanSegmentStart})
	}
}

func findProposedPlanTagLine(buffer string, tag string, allowEOF bool) (int, int, bool) {
	searchFrom := 0
	for {
		index := strings.Index(buffer[searchFrom:], tag)
		if index < 0 {
			return 0, 0, false
		}
		index += searchFrom
		if !isLineStart(buffer, index) {
			searchFrom = index + 1
			continue
		}
		after := index + len(tag)
		if after == len(buffer) {
			if allowEOF {
				return index, after, true
			}
			return 0, 0, false
		}
		switch buffer[after] {
		case '\n':
			return index, after + 1, true
		case '\r':
			if after+1 < len(buffer) && buffer[after+1] == '\n' {
				return index, after + 2, true
			}
			return index, after + 1, true
		default:
			searchFrom = index + 1
		}
	}
}

func isLineStart(buffer string, index int) bool {
	return index == 0 || buffer[index-1] == '\n'
}

func longestTagPrefixSuffix(buffer string, tag string) int {
	max := len(tag)
	if len(buffer) < max {
		max = len(buffer)
	}
	for size := max; size > 0; size-- {
		if strings.HasSuffix(buffer, tag[:size]) {
			return size
		}
	}
	return 0
}

func splitProposedPlanText(text string) (string, string, bool) {
	parser := newProposedPlanStreamParser()
	segments := append(parser.push(text), parser.finish()...)
	var visible strings.Builder
	var plan strings.Builder
	sawPlan := false
	for _, segment := range segments {
		switch segment.Kind {
		case proposedPlanSegmentNormal:
			visible.WriteString(segment.Text)
		case proposedPlanSegmentStart:
			sawPlan = true
			plan.Reset()
		case proposedPlanSegmentDelta:
			if sawPlan {
				plan.WriteString(segment.Text)
			}
		}
	}
	return visible.String(), plan.String(), sawPlan
}
