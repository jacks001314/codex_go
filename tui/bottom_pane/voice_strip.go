package bottompane

// Compact voice controls and caller-owned microphone/speaker sample histories.
// Control positions stay stable and mute is offered only when the current phase
// permits it, mirroring the Rust voice strip.

import (
	"strings"
	"time"

	"github.com/mattn/go-runewidth"
)

// VoiceStripPhase is the strip's connection state.
type VoiceStripPhase string

const (
	VoiceStripConnecting VoiceStripPhase = "connecting"
	VoiceStripActive     VoiceStripPhase = "active"
)

// voiceLoadingFrames is the braille spinner used while connecting.
var voiceLoadingFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

const (
	// voiceIdleGlyph marks an inactive or reduced-motion session.
	voiceIdleGlyph = "◌"
	// voiceActiveGlyph marks live, unmuted capture.
	voiceActiveGlyph = "●"

	voiceStripLabel   = "voice "
	voiceMuteFallback = "/voice mute"
	voiceStopControl  = "/voice stop"
	voiceMicLabel     = "  mic "
	voiceCodexLabel   = "  codex "
)

// voiceMeterGlyphs are the eight block levels a meter sample can render.
var voiceMeterGlyphs = []string{"▁", "▂", "▃", "▄", "▅", "▆", "▇", "█"}

// VoiceStripState is the caller-owned strip state.
type VoiceStripState struct {
	Phase           VoiceStripPhase
	MicrophoneLive  bool
	MicrophoneMuted bool
	// MicrophoneHistory and SpeakerHistory hold recent intensity samples in
	// 0-255; the strip renders their most recent values.
	MicrophoneHistory []uint8
	SpeakerHistory    []uint8
	// Activity is the short label shown next to the marker.
	Activity string
	// Animations enables the spinner; otherwise a static glyph is shown.
	Animations bool
	// MuteHint is the rendered key binding for muting, when one is configured.
	MuteHint string
	// StartedAt anchors the spinner phase and resets when activity changes.
	StartedAt time.Time
	// Now overrides the current time, primarily for tests.
	Now time.Time
}

// VoiceStripHeight reports the strip's rendered line count. A zero-width strip
// renders nothing.
func VoiceStripHeight(width int) int {
	if width <= 0 {
		return 0
	}
	return 2
}

// VoiceLoadingGlyph returns the spinner frame for the elapsed time.
func VoiceLoadingGlyph(startedAt time.Time, animations bool, now time.Time) string {
	if !animations {
		return voiceIdleGlyph
	}
	if now.IsZero() {
		now = time.Now()
	}
	elapsed := now.Sub(startedAt)
	if elapsed < 0 {
		elapsed = 0
	}
	frame := int(elapsed.Milliseconds() / 100)
	return voiceLoadingFrames[frame%len(voiceLoadingFrames)]
}

// VoiceStripLines renders the strip for one width. Styling is applied by the
// caller; the layout, glyphs, and control collapsing match the Rust strip.
func VoiceStripLines(width int, state VoiceStripState) []string {
	if width <= 0 {
		return nil
	}
	// The strip is inset by one column on each side.
	available := width - 2
	if available <= 0 {
		return nil
	}
	connecting := state.Phase == VoiceStripConnecting
	now := state.Now
	if now.IsZero() {
		now = time.Now()
	}
	marker := voiceIdleGlyph
	switch {
	case connecting:
		marker = VoiceLoadingGlyph(state.StartedAt, state.Animations, now)
	case state.MicrophoneLive && !state.MicrophoneMuted:
		marker = voiceActiveGlyph
	}
	status := voiceStripLabel + marker + " " + state.Activity
	mute := voiceMuteFallback
	if hint := strings.TrimSpace(state.MuteHint); hint != "" {
		label := strings.ReplaceAll(hint, " + ", "+")
		if state.MicrophoneMuted {
			mute = label + " unmute"
		} else {
			mute = label + " mute  "
		}
	}
	fullControls := mute + "   " + voiceStopControl
	canMute := !connecting || state.MicrophoneLive || state.MicrophoneMuted
	if canMute &&
		available < displayWidth(status)+displayWidth(fullControls)+1 &&
		available >= displayWidth(voiceStripLabel)+displayWidth(fullControls)+2 {
		// Drop the activity label but keep the marker.
		status = voiceStripLabel + marker
	}
	controls := voiceStopControl
	if canMute && available > displayWidth(status)+displayWidth(fullControls) {
		controls = fullControls
	}
	if available < displayWidth(status)+displayWidth(controls)+1 {
		status = strings.TrimSpace(voiceStripLabel)
	}
	padding := available - displayWidth(status) - displayWidth(controls)
	if padding < 0 {
		padding = 0
	}
	statusLine := status + strings.Repeat(" ", padding) + controls

	meterWidth := (available - 14) / 2
	if meterWidth > 6 {
		meterWidth = 6
	}
	if meterWidth < 0 {
		meterWidth = 0
	}
	meterLine := voiceMicLabel +
		voiceHistoryMeter(state.MicrophoneHistory, meterWidth, state.MicrophoneMuted || !state.MicrophoneLive) +
		voiceCodexLabel +
		voiceHistoryMeter(state.SpeakerHistory, meterWidth, false)
	return []string{statusLine, meterLine}
}

// voiceHistoryMeter renders the newest samples as a fixed-width meter, padding
// an empty history on the left.
func voiceHistoryMeter(samples []uint8, meterWidth int, muted bool) string {
	if meterWidth <= 0 {
		return ""
	}
	tail := samples
	if len(tail) > meterWidth {
		tail = tail[len(tail)-meterWidth:]
	}
	var builder strings.Builder
	for index := 0; index < meterWidth-len(tail); index++ {
		builder.WriteString(voiceMeterGlyphs[0])
	}
	for _, intensity := range tail {
		height := 0
		if !muted {
			height = (int(intensity)*7 + 254) / 255
		}
		if height > len(voiceMeterGlyphs)-1 {
			height = len(voiceMeterGlyphs) - 1
		}
		builder.WriteString(voiceMeterGlyphs[height])
	}
	return builder.String()
}

// displayWidth reports the terminal cell width of the strip's text.
func displayWidth(text string) int {
	return runewidth.StringWidth(text)
}
