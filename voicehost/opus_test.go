package voicehost

import (
	"math"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// preparedOpusLibrary locates the locally prepared codec. The prepare script
// writes third_party/voice/build/<goos>-<goarch>/lib, and CI may point at a
// different prefix through CODEX_VOICE_OPUS_LIB.
func preparedOpusLibrary(t *testing.T) string {
	t.Helper()
	if configured := os.Getenv("CODEX_VOICE_OPUS_LIB"); configured != "" {
		return configured
	}
	root := moduleRoot(t)
	return filepath.Join(root, "third_party", "voice", "build", runtime.GOOS+"-"+runtime.GOARCH, "lib", OpusLibraryName())
}

// openPreparedCodec opens the prepared codec or skips when no native Opus build
// is available on this machine.
func openPreparedCodec(t *testing.T) VoiceCodec {
	t.Helper()
	libraryPath := preparedOpusLibrary(t)
	if _, err := os.Stat(libraryPath); err != nil {
		t.Skipf("prepared Opus codec is unavailable at %s; run third_party/voice/prepare_opus.py", libraryPath)
	}
	codec, err := OpenOpusCodec(libraryPath)
	if err != nil {
		t.Fatalf("open prepared codec: %v", err)
	}
	t.Cleanup(func() {
		if closer, ok := codec.(interface{ Close() error }); ok {
			_ = closer.Close()
		}
	})
	return codec
}

// toneFrame builds one 20 ms frame of a mono 48 kHz tone.
func toneFrame(amplitude float64, frequency float64) []int16 {
	frame := make([]int16, voiceFrameSamples)
	for index := range frame {
		sample := amplitude * math.Sin(2*math.Pi*frequency*float64(index)/opusClockRate)
		frame[index] = int16(sample)
	}
	return frame
}

// TestPackagedOpusCodecRoundTrip proves the packaged codec encodes real Opus
// payloads and decodes them back with the signal preserved.
func TestPackagedOpusCodecRoundTrip(t *testing.T) {
	codec := openPreparedCodec(t)
	source := toneFrame(12000, 440)

	payload, err := codec.Encode(source)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if len(payload) == 0 || len(payload) > voiceSilenceBuffer {
		t.Fatalf("payload length = %d", len(payload))
	}

	decoded, err := codec.Decode(payload)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(decoded) == 0 || len(decoded) > voiceFrameSamples {
		t.Fatalf("decoded %d samples", len(decoded))
	}
	// Opus is lossy but must preserve a strong tone; a broken binding would
	// produce silence or noise far below the input energy.
	var sourceEnergy, decodedEnergy float64
	for index := 0; index < len(decoded); index++ {
		sourceSample := float64(source[index])
		sourceEnergy += sourceSample * sourceSample
		decodedSample := float64(decoded[index])
		decodedEnergy += decodedSample * decodedSample
	}
	if sourceEnergy == 0 {
		t.Fatal("the source frame is silent")
	}
	if ratio := decodedEnergy / sourceEnergy; ratio < 0.5 || ratio > 2.0 {
		t.Fatalf("decoded energy ratio = %.3f, want a preserved tone", ratio)
	}
}

// TestPackagedOpusCodecEncodesSilence proves the muted-capture path produces a
// compact payload that still decodes.
func TestPackagedOpusCodecEncodesSilence(t *testing.T) {
	codec := openPreparedCodec(t)
	payload, err := codec.Encode(make([]int16, voiceFrameSamples))
	if err != nil {
		t.Fatalf("encode silence: %v", err)
	}
	decoded, err := codec.Decode(payload)
	if err != nil {
		t.Fatalf("decode silence: %v", err)
	}
	for index, sample := range decoded {
		if sample > 64 || sample < -64 {
			t.Fatalf("decoded silence sample %d = %d", index, sample)
		}
	}
}

// TestPackagedOpusCodecDecodesAnotherEncodersPayload proves the decoder accepts
// payloads it did not produce, which is what peer audio requires.
func TestPackagedOpusCodecDecodesAnotherEncodersPayload(t *testing.T) {
	encoder := openPreparedCodec(t)
	decoder := openPreparedCodec(t)
	payload, err := encoder.Encode(toneFrame(9000, 1000))
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	decoded, err := decoder.Decode(payload)
	if err != nil {
		t.Fatalf("decode across instances: %v", err)
	}
	if len(decoded) == 0 {
		t.Fatal("no samples decoded")
	}
}

func TestOpusCodecRejectsInvalidInput(t *testing.T) {
	codec := openPreparedCodec(t)
	if _, err := codec.Encode(make([]int16, 7)); err == nil {
		t.Fatal("an invalid frame size was accepted")
	}
	if _, err := codec.Decode(nil); err == nil {
		t.Fatal("an empty payload was accepted")
	}
}

func TestOpusLibraryPathIsPackageRelative(t *testing.T) {
	path := OpusLibraryPath("/pkg")
	want := filepath.Join("/pkg", filepath.FromSlash(VoiceRuntimeDirectory), "lib", OpusLibraryName())
	if path != want {
		t.Fatalf("path = %q, want %q", path, want)
	}
	if library, err := LoadPackagedVoiceCodec(""); err == nil {
		t.Fatalf("an empty package produced a codec: %#v", library)
	}
}

// TestMediaSessionEndToEndWithRealCodec drives the whole media path with a real
// codec: capture -> Opus encode -> RTP -> inbound admission -> jitter buffer ->
// Opus decode -> playback queue.
func TestMediaSessionEndToEndWithRealCodec(t *testing.T) {
	codec := openPreparedCodec(t)
	pipeline := newPCMPipeline()
	pipeline.microphone.setEnabled(true)
	pipeline.speaker.setEnabled(true)
	pipeline.recordSessionStart()
	sender := &fakeSender{}
	session := newMediaSession(codec, pipeline, sender)

	// Two admitted 10 ms blocks complete one 20 ms frame. The first accepted
	// callback only establishes the unmute boundary.
	now := time.Now()
	tone := toneFrame(12000, 440)
	pipeline.pushCapture(tone[:audioBlockSamples], now)
	pipeline.pushCapture(tone[:audioBlockSamples], now.Add(10*time.Millisecond))
	pipeline.pushCapture(tone[audioBlockSamples:], now.Add(20*time.Millisecond))
	sent, err := session.service(now.Add(30 * time.Millisecond))
	if err != nil {
		t.Fatalf("service: %v", err)
	}
	if sent != 1 || len(sender.frames) != 1 {
		t.Fatalf("sent = %d, frames = %d", sent, len(sender.frames))
	}
	frame := sender.frames[0]
	if frame.header.PayloadType != opusPayloadType {
		t.Fatalf("payload type = %d", frame.header.PayloadType)
	}
	if len(frame.payload) == 0 || len(frame.payload) > voiceSilenceBuffer {
		t.Fatalf("payload length = %d", len(frame.payload))
	}

	// The peer's payload re-enters through the inbound interceptor and is
	// decoded into the playback queue after the jitter latency.
	if err := session.handleRTP(frame.payload, 7, now); err != nil {
		t.Fatalf("handle inbound: %v", err)
	}
	if _, err := session.service(now.Add(voicePlayoutLatency + time.Millisecond)); err != nil {
		t.Fatalf("playout service: %v", err)
	}
	// One 20 ms decoded frame becomes whole playback blocks.
	wantBlocks := voiceFrameSamples / audioBlockSamples
	if queued := pipeline.playback.length(); queued != wantBlocks {
		t.Fatalf("playback blocks = %d, want %d", queued, wantBlocks)
	}
	var energy float64
	for index := 0; index < wantBlocks; index++ {
		block, ok := pipeline.playback.pop()
		if !ok || block.length == 0 {
			t.Fatalf("playback block %d = %#v", index, block)
		}
		for sampleIndex := 0; sampleIndex < block.length; sampleIndex++ {
			sample := float64(block.samples[sampleIndex])
			energy += sample * sample
		}
	}
	if energy == 0 {
		t.Fatal("the decoded frame is silent")
	}
}
