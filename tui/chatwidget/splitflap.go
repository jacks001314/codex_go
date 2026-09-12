package chatwidget

// Bounded, presentation-only split-flap board for live voice transcripts. This
// mirrors the animation core of the Rust realtime_split_flap.rs so the spoken
// transcript tiles flip in the same order and with the same timing.

import (
	"strings"
	"time"
	"unicode"

	"github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
)

const (
	// SplitFlapFrameInterval is the animation frame cadence.
	SplitFlapFrameInterval = 45 * time.Millisecond
	// SplitFlapAnimationDuration bounds the whole flip.
	SplitFlapAnimationDuration = 675 * time.Millisecond
	// SplitFlapTileSettleDuration is when a tile shows its real glyph.
	SplitFlapTileSettleDuration = 180 * time.Millisecond
	// SplitFlapTileAfterglowDuration is how long a settled tile glows.
	SplitFlapTileAfterglowDuration = 135 * time.Millisecond
	// splitFlapSampleWindow bounds the tile-sample window.
	splitFlapSampleWindow = 24
)

// VoiceAmplitudeHistory keeps four bounded, calibrated amplitude samples.
type VoiceAmplitudeHistory [4]int

// Push rotates in one sample, clamped to the display range.
func (h *VoiceAmplitudeHistory) Push(level int) {
	if h == nil {
		return
	}
	if level < 0 {
		level = 0
	}
	if level > 5 {
		level = 5
	}
	h[0], h[1], h[2], h[3] = h[1], h[2], h[3], level
}

// SplitFlapBoard is one animated transcript row.
type SplitFlapBoard struct {
	Role           string
	Target         string
	VisibleTarget  string
	FlapSample     []byte
	TileArrivals   []time.Time
	StartedAt      time.Time
	PhaseStartedAt time.Time
	Animated       bool
}

// NewSplitFlapBoard builds the board for one transcript target. When previous
// still carries the retained prefix of the same speaker's text, its settled
// tiles are reused so only new arrivals flip.
func NewSplitFlapBoard(role, target string, previous *SplitFlapBoard, discardedPrefixBytes int, animated bool, now time.Time) *SplitFlapBoard {
	board := &SplitFlapBoard{
		Role:           role,
		Target:         target,
		VisibleTarget:  splitFlapFlippableGlyphs(target),
		StartedAt:      now,
		PhaseStartedAt: now,
		Animated:       animated,
	}
	board.FlapSample = splitFlapSample(board.VisibleTarget)

	retained := previous
	discardedTiles := 0
	if previous != nil {
		if discardedPrefixBytes < 0 || discardedPrefixBytes > len(previous.Target) {
			retained = nil
		} else {
			prefix := previous.Target[discardedPrefixBytes:]
			switch {
			case previous.Role != role, prefix == "", !strings.HasPrefix(target, prefix):
				retained = nil
			default:
				retainedGlyphs := previous.VisibleTarget
				if discardedPrefixBytes != 0 {
					retainedGlyphs = splitFlapFlippableGlyphs(prefix)
				}
				if len(previous.VisibleTarget) < len(retainedGlyphs) {
					retained = nil
					break
				}
				discardedTiles = len(previous.VisibleTarget) - len(retainedGlyphs)
				if previous.VisibleTarget[discardedTiles:] != retainedGlyphs ||
					!strings.HasPrefix(board.VisibleTarget, retainedGlyphs) {
					retained = nil
				}
			}
		}
	}
	if retained != nil {
		board.PhaseStartedAt = retained.PhaseStartedAt
		if discardedTiles <= len(retained.TileArrivals) {
			board.TileArrivals = append(board.TileArrivals, retained.TileArrivals[discardedTiles:]...)
		}
	}
	existing := len(board.TileArrivals)
	for index := existing; index < len(board.VisibleTarget); index++ {
		offset := time.Duration(min((index-existing)*8, 56)) * time.Millisecond
		start := now
		if retained != nil {
			start = now.Add(-SplitFlapFrameInterval)
		}
		board.TileArrivals = append(board.TileArrivals, start.Add(offset))
	}
	return board
}

// IsAnimating reports whether the board still needs frames at elapsed.
func (b *SplitFlapBoard) IsAnimating(elapsed time.Duration) bool {
	return b != nil && b.Animated && elapsed < SplitFlapAnimationDuration && len(b.TileArrivals) > 0
}

// AnimationTick returns the frame index while animating.
func (b *SplitFlapBoard) AnimationTick(elapsed time.Duration) (int, bool) {
	if !b.IsAnimating(elapsed) {
		return 0, false
	}
	return int(elapsed / SplitFlapFrameInterval), true
}

// AnimateLine renders one display line. Reduced motion returns it untouched.
func (b *SplitFlapBoard) AnimateLine(displayText string, width int, elapsed time.Duration) string {
	return b.AnimateLines([]string{displayText}, width, elapsed)[0]
}

// AnimateLines renders every non-empty line with one shared glyph index, the
// runway animation on the final non-empty line, and padding to width. Reduced
// motion returns the original lines untouched.
func (b *SplitFlapBoard) AnimateLines(lines []string, width int, elapsed time.Duration) []string {
	if b == nil || !b.Animated {
		return lines
	}
	now := b.StartedAt.Add(elapsed)
	phaseElapsed := now.Sub(b.PhaseStartedAt)
	glyphIndex := 0
	wordUnsettled := false
	finalLine := -1
	for index, line := range lines {
		if strings.TrimSpace(line) != "" {
			finalLine = index
		}
	}
	out := make([]string, len(lines))
	for lineIndex, line := range lines {
		if strings.TrimSpace(line) == "" {
			out[lineIndex] = line
			continue
		}
		var builder strings.Builder
		graphemes := uniseg.NewGraphemes(line)
		for graphemes.Next() {
			grapheme := graphemes.Str()
			if splitFlapIsFlippable(grapheme) {
				position := glyphIndex
				glyphIndex++
				arrival := b.StartedAt
				if position < len(b.TileArrivals) {
					arrival = b.TileArrivals[position]
				}
				text, flipping := splitFlapGlyph(grapheme, position, now.Sub(arrival), phaseElapsed, b.FlapSample)
				if flipping {
					wordUnsettled = true
				}
				builder.WriteString(text)
				continue
			}
			if strings.TrimSpace(grapheme) == "" {
				wordUnsettled = false
			}
			if wordUnsettled && splitFlapIsPunctuation(grapheme) {
				builder.WriteString(" ")
			} else {
				builder.WriteString(grapheme)
			}
		}
		text := builder.String()
		remaining := width - runewidth.StringWidth(text)
		if lineIndex == finalLine && b.IsAnimating(elapsed) && remaining > 1 && len(b.FlapSample) > 0 &&
			len(b.TileArrivals) > 0 && now.Sub(b.TileArrivals[0]) >= SplitFlapTileSettleDuration {
			phase := int(phaseElapsed / SplitFlapFrameInterval)
			count := min(remaining-1, 3)
			runway := make([]byte, 0, count)
			for index := 0; index < count; index++ {
				runway = append(runway, b.FlapSample[(phase+index)%len(b.FlapSample)])
			}
			text += " " + string(runway)
		}
		if padding := width - runewidth.StringWidth(text); padding > 0 {
			text += strings.Repeat(" ", padding)
		}
		out[lineIndex] = text
	}
	return out
}

func splitFlapFlippableGlyphs(target string) string {
	var out strings.Builder
	graphemes := uniseg.NewGraphemes(target)
	for graphemes.Next() {
		grapheme := graphemes.Str()
		if splitFlapIsFlippable(grapheme) {
			out.WriteString(grapheme)
		}
	}
	return out.String()
}

func splitFlapIsFlippable(grapheme string) bool {
	if len(grapheme) != 1 {
		return false
	}
	byteValue := grapheme[0]
	return (byteValue >= '0' && byteValue <= '9') ||
		(byteValue >= 'a' && byteValue <= 'z') ||
		(byteValue >= 'A' && byteValue <= 'Z')
}

func splitFlapIsPunctuation(grapheme string) bool {
	if len(grapheme) != 1 {
		return false
	}
	r := rune(grapheme[0])
	return r < 128 && unicode.IsPunct(r)
}

func splitFlapSample(visibleTarget string) []byte {
	sample := make([]byte, 0, splitFlapSampleWindow)
	for index := len(visibleTarget) - 1; index >= 0 && len(sample) < splitFlapSampleWindow; index-- {
		value := visibleTarget[index]
		if (value >= 'a' && value <= 'z') || (value >= 'A' && value <= 'Z') {
			sample = append(sample, value)
		}
	}
	if len(sample) == 0 {
		for index := len(visibleTarget) - 1; index >= 0 && len(sample) < splitFlapSampleWindow; index-- {
			sample = append(sample, visibleTarget[index])
		}
	}
	for left, right := 0, len(sample)-1; left < right; left, right = left+1, right-1 {
		sample[left], sample[right] = sample[right], sample[left]
	}
	return sample
}

func splitFlapGlyph(grapheme string, index int, elapsed, phaseElapsed time.Duration, sample []byte) (string, bool) {
	if len(sample) == 0 {
		return grapheme, false
	}
	if elapsed < 40*time.Millisecond {
		return " ", true
	}
	if elapsed >= SplitFlapTileSettleDuration {
		return grapheme, false
	}
	frame := int(phaseElapsed / SplitFlapFrameInterval)
	offset := int(grapheme[0]) + index*7 + frame*11
	return string(sample[offset%len(sample)]), true
}
