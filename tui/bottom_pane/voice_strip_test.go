package bottompane

import (
	"strings"
	"testing"
	"time"
)

func TestVoiceStripHeight(t *testing.T) {
	if got := VoiceStripHeight(0); got != 0 {
		t.Fatalf("height = %d", got)
	}
	if got := VoiceStripHeight(40); got != 2 {
		t.Fatalf("height = %d", got)
	}
	if lines := VoiceStripLines(0, VoiceStripState{}); lines != nil {
		t.Fatalf("zero width rendered %#v", lines)
	}
}

func TestVoiceLoadingGlyphHonoursReducedMotion(t *testing.T) {
	started := time.Unix(0, 0)
	if got := VoiceLoadingGlyph(started, false, started.Add(time.Second)); got != voiceIdleGlyph {
		t.Fatalf("reduced-motion glyph = %q", got)
	}
	// The spinner advances every 100 ms and wraps.
	first := VoiceLoadingGlyph(started, true, started)
	if first != voiceLoadingFrames[0] {
		t.Fatalf("first frame = %q", first)
	}
	second := VoiceLoadingGlyph(started, true, started.Add(150*time.Millisecond))
	if second != voiceLoadingFrames[1] {
		t.Fatalf("second frame = %q", second)
	}
	wrapped := VoiceLoadingGlyph(started, true, started.Add(time.Duration(len(voiceLoadingFrames))*100*time.Millisecond))
	if wrapped != voiceLoadingFrames[0] {
		t.Fatalf("wrapped frame = %q", wrapped)
	}
	// A clock before the start does not panic.
	if got := VoiceLoadingGlyph(started, true, started.Add(-time.Second)); got == "" {
		t.Fatal("negative elapsed produced no glyph")
	}
}

func TestVoiceStripRendersActiveControls(t *testing.T) {
	state := VoiceStripState{
		Phase:          VoiceStripActive,
		MicrophoneLive: true,
		Activity:       "listening",
	}
	lines := VoiceStripLines(80, state)
	if len(lines) != 2 {
		t.Fatalf("lines = %#v", lines)
	}
	if !strings.HasPrefix(lines[0], "voice "+voiceActiveGlyph) {
		t.Fatalf("status line = %q", lines[0])
	}
	if !strings.Contains(lines[0], "listening") {
		t.Fatalf("activity missing from %q", lines[0])
	}
	if !strings.HasPrefix(lines[0], "voice ") || !strings.HasSuffix(lines[0], voiceStopControl) {
		t.Fatalf("status line = %q", lines[0])
	}
	if !strings.Contains(lines[0], voiceMuteFallback) {
		t.Fatalf("mute control missing from %q", lines[0])
	}
	if !strings.HasPrefix(lines[1], voiceMicLabel) || !strings.Contains(lines[1], voiceCodexLabel) {
		t.Fatalf("meter line = %q", lines[1])
	}
}

func TestVoiceStripHidesMuteWhileConnecting(t *testing.T) {
	state := VoiceStripState{
		Phase:      VoiceStripConnecting,
		Animations: false,
		StartedAt:  time.Unix(0, 0),
		Now:        time.Unix(0, 0),
		Activity:   "connecting",
	}
	lines := VoiceStripLines(80, state)
	if !strings.Contains(lines[0], voiceIdleGlyph) {
		t.Fatalf("status line = %q", lines[0])
	}
	// Without live capture the mute control is withheld.
	if strings.Contains(lines[0], voiceMuteFallback) {
		t.Fatalf("mute control shown while connecting: %q", lines[0])
	}
	if !strings.HasSuffix(lines[0], voiceStopControl) {
		t.Fatalf("stop control missing: %q", lines[0])
	}

	// Once capture is live the mute control returns.
	state.MicrophoneLive = true
	lines = VoiceStripLines(80, state)
	if !strings.Contains(lines[0], voiceMuteFallback) {
		t.Fatalf("mute control missing with live capture: %q", lines[0])
	}
}

func TestVoiceStripCollapsesControlsOnNarrowWidths(t *testing.T) {
	state := VoiceStripState{
		Phase:          VoiceStripActive,
		MicrophoneLive: true,
		Activity:       "listening",
	}
	// Wide: activity and full controls.
	wide := VoiceStripLines(80, state)
	if !strings.Contains(wide[0], "listening") || !strings.Contains(wide[0], voiceMuteFallback) {
		t.Fatalf("wide status line = %q", wide[0])
	}
	// Narrow: the activity label is dropped before the mute control.
	narrow := VoiceStripLines(40, state)
	if strings.Contains(narrow[0], "listening") {
		t.Fatalf("activity survived a narrow width: %q", narrow[0])
	}
	if !strings.Contains(narrow[0], voiceMuteFallback) {
		t.Fatalf("mute control dropped too early: %q", narrow[0])
	}
	// Very narrow: the status collapses to the bare label and the renderer
	// clips the overflow, matching the Rust strip.
	tiny := VoiceStripLines(12, state)
	if !strings.HasPrefix(tiny[0], strings.TrimSpace(voiceStripLabel)) {
		t.Fatalf("tiny status line = %q", tiny[0])
	}
	if strings.Contains(tiny[0], "listening") || strings.Contains(tiny[0], voiceActiveGlyph) {
		t.Fatalf("tiny status line kept detail: %q", tiny[0])
	}
}

func TestVoiceStripMuteHintAndState(t *testing.T) {
	state := VoiceStripState{
		Phase:          VoiceStripActive,
		MicrophoneLive: true,
		MuteHint:       "ctrl + x",
	}
	lines := VoiceStripLines(80, state)
	if !strings.Contains(lines[0], "ctrl+x mute") {
		t.Fatalf("status line = %q", lines[0])
	}
	state.MicrophoneMuted = true
	lines = VoiceStripLines(80, state)
	if !strings.Contains(lines[0], "ctrl+x unmute") {
		t.Fatalf("muted status line = %q", lines[0])
	}
	// A muted session renders the idle marker even while capture is live.
	if !strings.Contains(lines[0], voiceIdleGlyph) {
		t.Fatalf("muted marker missing: %q", lines[0])
	}
}

func TestVoiceStripMeterScaling(t *testing.T) {
	// A byte history maps onto the eight block levels.
	if got := voiceHistoryMeter([]uint8{0}, 1, false); got != voiceMeterGlyphs[0] {
		t.Fatalf("zero intensity = %q", got)
	}
	if got := voiceHistoryMeter([]uint8{255}, 1, false); got != voiceMeterGlyphs[len(voiceMeterGlyphs)-1] {
		t.Fatalf("full intensity = %q", got)
	}
	if got := voiceHistoryMeter([]uint8{128}, 1, false); got != voiceMeterGlyphs[4] {
		t.Fatalf("mid intensity = %q", got)
	}
	// A muted history renders the floor regardless of samples.
	if got := voiceHistoryMeter([]uint8{255, 255}, 2, true); got != voiceMeterGlyphs[0]+voiceMeterGlyphs[0] {
		t.Fatalf("muted meter = %q", got)
	}
	// An empty history is left-padded and the newest samples win.
	if got := voiceHistoryMeter(nil, 3, false); got != voiceMeterGlyphs[0]+voiceMeterGlyphs[0]+voiceMeterGlyphs[0] {
		t.Fatalf("padded meter = %q", got)
	}
	got := voiceHistoryMeter([]uint8{0, 255}, 2, false)
	if got != voiceMeterGlyphs[0]+voiceMeterGlyphs[len(voiceMeterGlyphs)-1] {
		t.Fatalf("tail meter = %q", got)
	}
	// Samples beyond the meter width keep only the newest.
	got = voiceHistoryMeter([]uint8{255, 0, 0}, 1, false)
	if got != voiceMeterGlyphs[0] {
		t.Fatalf("truncated meter = %q", got)
	}
}

func TestVoiceStripMeterWidthIsBounded(t *testing.T) {
	state := VoiceStripState{
		Phase:             VoiceStripActive,
		MicrophoneLive:    true,
		MicrophoneHistory: []uint8{255, 255, 255, 255, 255, 255, 255, 255},
		SpeakerHistory:    []uint8{255, 255, 255, 255, 255, 255, 255, 255},
	}
	lines := VoiceStripLines(120, state)
	meter := strings.TrimPrefix(lines[1], voiceMicLabel)
	if index := strings.Index(meter, voiceCodexLabel); index >= 0 {
		meter = meter[:index]
	}
	if width := displayWidth(meter); width != 6 {
		t.Fatalf("meter width = %d (%q)", width, meter)
	}
}
