package voicehost

import (
	"context"
	"encoding/hex"
	"io"
	"testing"
	"time"
)

func TestPCMBufferReadDrainAndClose(t *testing.T) {
	buffer := newPCMBuffer(16)
	buffer.Append([]byte{1, 2, 3, 4})
	readContext, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	output := make([]byte, 3)
	count, err := buffer.Read(readContext, output)
	if err != nil || count != 3 || !equalBytes(output, []byte{1, 2, 3}) {
		t.Fatalf("read = %d, %v, %v", count, err, output)
	}
	buffer.Append([]byte{5, 6, 7, 8, 9})
	// After the read only [4] remains, so appending [5..9] keeps all six bytes.
	drained := make([]byte, 8)
	count = buffer.Drain(drained)
	if count != 6 || !equalBytes(drained, []byte{4, 5, 6, 7, 8, 9, 0, 0}) {
		t.Fatalf("drain = %d, %v", count, drained)
	}
	buffer.Close()
	if _, err := buffer.Read(readContext, output); err != io.EOF {
		t.Fatalf("closed read = %v", err)
	}
}

func TestPCMBufferReadBlocksUntilDataOrContext(t *testing.T) {
	buffer := newPCMBuffer(16)
	canceledContext, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := buffer.Read(canceledContext, make([]byte, 1)); err != context.Canceled {
		t.Fatalf("cancelled read = %v", err)
	}
}

func TestDecodeDeviceIDRoundTrip(t *testing.T) {
	deviceID, err := decodeDeviceID("010203ff")
	if err != nil {
		t.Fatal(err)
	}
	if deviceID.String() != "010203ff" {
		t.Fatalf("device id string = %q", deviceID.String())
	}
	decoded, err := hex.DecodeString(deviceID.String())
	if err != nil {
		t.Fatal(err)
	}
	if len(decoded) != 4 || !equalBytes(decoded, []byte{1, 2, 3, 0xff}) {
		t.Fatalf("decoded = %v", decoded)
	}
	if _, err := decodeDeviceID("not-hex"); err == nil {
		t.Fatal("invalid device id was accepted")
	}
}

func TestPCMBufferCapacityForFormat(t *testing.T) {
	format := AudioFormat{SampleRate: 24000, Channels: 1, Encoding: AudioEncodingS16LE}
	if got := pcmBufferBytes(format, 2*time.Second); got != 96000 {
		t.Fatalf("capacity = %d", got)
	}
	if got := pcmBufferBytes(AudioFormat{SampleRate: 1000, Channels: 1}, time.Second); got != 64*1024 {
		t.Fatalf("minimum capacity = %d", got)
	}
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func TestPCMReportingComparesToNativeScale(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want uint16
	}{
		{name: "silence", data: []byte{0, 0, 0, 0}, want: 0},
		{name: "empty", data: nil, want: 0},
		{name: "positive peak", data: []byte{0x10, 0x27}, want: 20000},
		{name: "negative peak", data: []byte{0xf0, 0xd8}, want: 20000},
		{name: "saturates", data: []byte{0x00, 0x80}, want: 65535},
		{name: "odd tail ignored", data: []byte{0x00, 0x80, 0x7f}, want: 65535},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := pcmPeak(test.data); got != test.want {
				t.Fatalf("peak = %d, want %d", got, test.want)
			}
		})
	}
}

func TestRuntimeAudioStateClearsOnRead(t *testing.T) {
	runtime := NewMiniAudioRuntime()
	recordUint32Peak(&runtime.pipeline.microphonePeak, 10)
	recordUint32Peak(&runtime.pipeline.speakerPeak, 20)
	recordUint32Peak(&runtime.pipeline.microphonePeak, 5)
	state := runtime.AudioState()
	if state.MicrophonePeak != 10 || state.SpeakerPeak != 20 {
		t.Fatalf("state = %+v", state)
	}
	if cleared := runtime.AudioState(); cleared.MicrophonePeak != 0 || cleared.SpeakerPeak != 0 {
		t.Fatalf("second read = %+v, want cleared", cleared)
	}
}

func TestMutedCaptureProducesNoAudibleBlock(t *testing.T) {
	runtime := NewMiniAudioRuntime()
	if err := runtime.SetControls(AudioControls{MicrophoneMuted: true}); err != nil {
		t.Fatal(err)
	}
	block := make([]int16, audioBlockSamples)
	for index := range block {
		block[index] = 20000
	}
	if !runtime.pipeline.pushCapture(block, time.Now()) {
		t.Fatal("a muted capture callback must be accepted")
	}
	if _, ok := runtime.pipeline.readCapture(time.Now()); ok {
		t.Fatal("muted capture produced an audible block")
	}
	if state := runtime.AudioState(); state.MicrophonePeak != 0 {
		t.Fatalf("muted capture recorded a peak: %+v", state)
	}
}

func TestUnmutedCaptureRecordsPeak(t *testing.T) {
	runtime := NewMiniAudioRuntime()
	if err := runtime.SetControls(AudioControls{}); err != nil {
		t.Fatal(err)
	}
	block := make([]int16, audioBlockSamples)
	block[0] = 10000
	// The first accepted callback only establishes the unmute boundary.
	runtime.pipeline.pushCapture(block, time.Now())
	runtime.pipeline.pushCapture(block, time.Now().Add(10*time.Millisecond))
	if _, ok := runtime.pipeline.readCapture(time.Now().Add(10 * time.Millisecond)); !ok {
		t.Fatal("unmuted capture produced no block")
	}
	if state := runtime.AudioState(); state.MicrophonePeak != 20000 {
		t.Fatalf("state = %+v", state)
	}
}

func TestSuppressedPlaybackDiscardsAudio(t *testing.T) {
	runtime := NewMiniAudioRuntime()
	runtime.pipeline.recordSessionStart()
	if err := runtime.SetControls(AudioControls{SpeakerSuppressed: true}); err != nil {
		t.Fatal(err)
	}
	sink := &miniaudioSink{format: defaultSessionFormat, runtime: runtime, pipeline: runtime.pipeline}
	if err := sink.Write(context.Background(), Frame{Data: []byte{0x10, 0x27}, Format: defaultSessionFormat}); err != nil {
		t.Fatal(err)
	}
	if queued := runtime.pipeline.playback.length(); queued != 0 {
		t.Fatalf("suppressed playback queued %d blocks", queued)
	}
	if err := runtime.SetControls(AudioControls{}); err != nil {
		t.Fatal(err)
	}
	if err := sink.Write(context.Background(), Frame{Data: []byte{0x10, 0x27}, Format: defaultSessionFormat}); err != nil {
		t.Fatal(err)
	}
	if queued := runtime.pipeline.playback.length(); queued != 1 {
		t.Fatalf("unsuppressed playback queued %d blocks, want 1", queued)
	}
}
