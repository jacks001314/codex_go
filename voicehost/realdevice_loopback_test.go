package voicehost

import (
	"context"
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"testing"
	"time"
)

// TestRealDeviceLoopback exercises the real microphone and speaker through the
// same MiniAudioRuntime the packaged helper uses. It is skipped unless
// CODEX_VOICE_REAL_DEVICE=1 so ordinary CI stays hardware-free.
//
// It verifies two things the 48 kHz alignment depends on:
//   - capture timing: the packed blocks describe 48 kHz audio, so the wall
//     clock during a burst of reads must track samples/48000 (a 24 kHz device
//     feeding a 48 kHz pipeline would run ~2x fast);
//   - playback: a tone written to the real speaker is accepted and rendered.
//
// Acoustic coupling (microphone hearing the speaker) is reported but not
// asserted, because speaker volume, mute state, and room noise make it flaky.
func TestRealDeviceLoopback(t *testing.T) {
	if os.Getenv("CODEX_VOICE_REAL_DEVICE") != "1" {
		t.Skip("set CODEX_VOICE_REAL_DEVICE=1 to exercise the real microphone and speaker")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	runtime := NewMiniAudioRuntime()
	if err := runtime.Start(ctx, SessionConfig{Format: defaultSessionFormat}); err != nil {
		t.Fatalf("start miniaudio runtime: %v", err)
	}
	defer func() { _ = runtime.Stop() }()

	inputs, err := runtime.ListInputDevices(ctx)
	if err != nil {
		t.Fatalf("list input devices: %v", err)
	}
	outputs, err := runtime.ListOutputDevices(ctx)
	if err != nil {
		t.Fatalf("list output devices: %v", err)
	}
	if len(inputs) == 0 || len(outputs) == 0 {
		t.Fatalf("no real audio devices: inputs=%v outputs=%v", inputs, outputs)
	}
	t.Logf("input devices:  %v", loopbackDeviceNames(inputs))
	t.Logf("output devices: %v", loopbackDeviceNames(outputs))

	source, err := runtime.OpenInput(ctx, "")
	if err != nil {
		t.Fatalf("open input device: %v", err)
	}
	defer func() { _ = source.Close() }()
	sink, err := runtime.OpenOutput(ctx, "")
	if err != nil {
		t.Fatalf("open output device: %v", err)
	}
	defer func() { _ = sink.Close() }()

	if err := runtime.SetControls(AudioControls{MicrophoneMuted: false, SpeakerSuppressed: false}); err != nil {
		t.Fatalf("unmute devices: %v", err)
	}
	pipeline := runtime.Pipeline()
	if pipeline == nil {
		t.Fatal("miniaudio runtime has no media pipeline")
	}
	// The sink drops frames until a media session is being serviced.
	pipeline.recordSessionStart()

	if err := runtime.SetControls(AudioControls{MicrophoneMuted: false, SpeakerSuppressed: false}); err != nil {
		t.Fatalf("reapply unmute: %v", err)
	}

	// Baseline: ~200 ms of real capture before any playback.
	baselinePeak, baselineSamples, baselineElapsed := loopbackCapture(ctx, t, source, 20)

	// Play a 1 kHz tone to the real speaker in ~200 ms chunks while capturing.
	chunk := loopbackTone(defaultSessionFormat.SampleRate, 200*time.Millisecond)
	tonePeak := 0
	captureSamples := 0
	captureStarted := time.Now()
	for index := 0; index < 5; index++ {
		frame := Frame{
			Data:       chunk,
			Format:     defaultSessionFormat,
			Samples:    len(chunk) / 2,
			CapturedAt: time.Now(),
		}
		if err := sink.Write(ctx, frame); err != nil {
			t.Fatalf("write tone chunk %d: %v", index, err)
		}
		peak, samples, _ := loopbackCapture(ctx, t, source, 20)
		captureSamples += samples
		if peak > tonePeak {
			tonePeak = peak
		}
	}
	captureElapsed := time.Since(captureStarted)
	state := runtime.AudioState()

	rate := defaultSessionFormat.SampleRate
	baselineDuration := time.Duration(int64(baselineSamples) * int64(time.Second) / int64(rate))
	captureDuration := time.Duration(int64(captureSamples) * int64(time.Second) / int64(rate))
	baselineRatio := float64(baselineElapsed) / float64(baselineDuration)
	captureRatio := float64(captureElapsed) / float64(captureDuration)
	t.Logf("capture rate=%d Hz, chunk=%d ms, frame=%d samples", rate, voiceFrameDuration.Milliseconds(), audioBlockSamples)
	t.Logf("baseline: %d samples in %v (audio %v, ratio %.3f), peak %d",
		baselineSamples, baselineElapsed, baselineDuration, baselineRatio, baselinePeak)
	t.Logf("during tone: %d samples in %v (audio %v, ratio %.3f), mic peak %d, speaker peak %d",
		captureSamples, captureElapsed, captureDuration, captureRatio, tonePeak, state.SpeakerPeak)

	// The rate alignment is the point of this test: wall-clock capture must
	// track the 48 kHz sample clock. A 24 kHz device behind a 48 kHz pipeline
	// would deliver ~2x the audio duration.
	if captureRatio < 0.6 || captureRatio > 1.6 {
		t.Fatalf("capture timing off by %.2fx: %d samples took %v but should take ~%v",
			captureRatio, captureSamples, captureElapsed, captureDuration)
	}
	if state.SpeakerPeak == 0 {
		t.Fatal("speaker rendered no audio for the tone")
	}
	if tonePeak == 0 {
		t.Log("note: microphone captured only silence (no acoustic coupling observed)")
	}
}

// TestSelectedDeviceIDFailsClosedWithoutPanic guards the cgo pointer rule for a
// selected device id. The id bytes are Go memory referenced from the native
// device config, so they must stay pinned across ma_device_init; otherwise the
// cgo check panics with "Go pointer to unpinned Go pointer" and the helper
// crashes instead of failing closed. An unknown id must surface as an error.
func TestSelectedDeviceIDFailsClosedWithoutPanic(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	runtime := NewMiniAudioRuntime()
	if err := runtime.Start(ctx, SessionConfig{Format: defaultSessionFormat}); err != nil {
		t.Skipf("miniaudio runtime unavailable: %v", err)
	}
	defer func() { _ = runtime.Stop() }()
	const unknownID = "00112233445566778899aabbccddeeff"
	if _, err := runtime.OpenInput(ctx, unknownID); err == nil {
		t.Fatal("unknown selected input device id unexpectedly opened")
	}
	if _, err := runtime.OpenOutput(ctx, unknownID); err == nil {
		t.Fatal("unknown selected output device id unexpectedly opened")
	}
}

func loopbackCapture(ctx context.Context, t *testing.T, source AudioSource, blocks int) (int, int, time.Duration) {
	t.Helper()
	started := time.Now()
	peak := 0
	samples := 0
	for index := 0; index < blocks; index++ {
		frame, err := source.Read(ctx)
		if err != nil {
			t.Fatalf("read captured block %d: %v", index, err)
		}
		if frame.Format.SampleRate != defaultSessionFormat.SampleRate {
			t.Fatalf("capture format = %+v, want %d Hz", frame.Format, defaultSessionFormat.SampleRate)
		}
		samples += frame.Samples
		if value := loopbackPeak(frame.Data); value > peak {
			peak = value
		}
	}
	return peak, samples, time.Since(started)
}

// TestRealDeviceInputProbe opens every enumerated microphone, reports which one
// yields non-silent samples, and is skipped unless CODEX_VOICE_REAL_DEVICE=1.
// It diagnoses a capture path that opens correctly but stays silent.
func TestRealDeviceInputProbe(t *testing.T) {
	if os.Getenv("CODEX_VOICE_REAL_DEVICE") != "1" {
		t.Skip("set CODEX_VOICE_REAL_DEVICE=1 to probe the real microphones")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	runtime := NewMiniAudioRuntime()
	if err := runtime.Start(ctx, SessionConfig{Format: defaultSessionFormat}); err != nil {
		t.Fatalf("start miniaudio runtime: %v", err)
	}
	defer func() { _ = runtime.Stop() }()

	inputs, err := runtime.ListInputDevices(ctx)
	if err != nil {
		t.Fatalf("list input devices: %v", err)
	}
	for _, device := range inputs {
		source, err := runtime.OpenInput(ctx, device.ID)
		if err != nil {
			t.Logf("input %q: open failed: %v", device.Name, err)
			continue
		}
		if err := runtime.SetControls(AudioControls{MicrophoneMuted: false, SpeakerSuppressed: false}); err != nil {
			t.Logf("input %q: unmute failed: %v", device.Name, err)
		}
		peak := 0
		samples := 0
		var readErr error
		for index := 0; index < 50; index++ {
			frame, err := source.Read(ctx)
			if err != nil {
				readErr = err
				break
			}
			samples += frame.Samples
			if value := loopbackPeak(frame.Data); value > peak {
				peak = value
			}
		}
		_ = source.Close()
		if readErr != nil {
			t.Logf("input %q: read failed after %d samples: %v", device.Name, samples, readErr)
			continue
		}
		t.Logf("input %q: %d samples, peak %d", device.Name, samples, peak)
	}
}

func loopbackTone(rate int, duration time.Duration) []byte {
	count := int(int64(rate) * int64(duration) / int64(time.Second))
	data := make([]byte, count*2)
	for index := 0; index < count; index++ {
		value := int16(12000 * math.Sin(2*math.Pi*1000*float64(index)/float64(rate)))
		binary.LittleEndian.PutUint16(data[index*2:], uint16(value))
	}
	return data
}

func loopbackPeak(data []byte) int {
	peak := 0
	for index := 0; index+1 < len(data); index += 2 {
		value := int(int16(binary.LittleEndian.Uint16(data[index:])))
		if value < 0 {
			value = -value
		}
		if value > peak {
			peak = value
		}
	}
	return peak
}

func loopbackDeviceNames(devices []Device) []string {
	names := make([]string, 0, len(devices))
	for _, device := range devices {
		marker := ""
		if device.IsDefault {
			marker = " (default)"
		}
		names = append(names, fmt.Sprintf("%s%s", device.Name, marker))
	}
	return names
}
