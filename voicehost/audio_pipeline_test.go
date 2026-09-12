package voicehost

import (
	"testing"
	"time"
)

func TestAudioGenerationIsIdempotentAndOrdered(t *testing.T) {
	generation := newAudioGeneration()
	if generation.enabled() {
		t.Fatal("a fresh generation starts disabled")
	}
	generation.setEnabled(true)
	first := generation.current()
	if !generation.enabled() || first%2 != 0 {
		t.Fatalf("enabled generation = %d", first)
	}
	generation.setEnabled(true)
	if generation.current() != first {
		t.Fatalf("re-enabling advanced the epoch: %d -> %d", first, generation.current())
	}
	generation.setEnabled(false)
	if generation.enabled() || generation.current() != first+1 {
		t.Fatalf("disable epoch = %d", generation.current())
	}
	generation.setEnabled(true)
	if !generation.enabled() || generation.current() != first+2 {
		t.Fatalf("re-enable epoch = %d", generation.current())
	}
}

func TestAudioPackerEmitsOnlyCompleteBlocks(t *testing.T) {
	queue := newBlockQueue(4)
	var packer audioPacker
	start := time.Unix(0, 0)
	half := make([]int16, audioBlockSamples/2)
	if !packer.push(half, start, 2, queue) {
		t.Fatal("partial block rejected")
	}
	if queue.length() != 0 {
		t.Fatalf("a partial block entered the queue: %d", queue.length())
	}
	if !packer.push(half, start.Add(5*time.Millisecond), 2, queue) {
		t.Fatal("completing block rejected")
	}
	if queue.length() != 1 {
		t.Fatalf("queue length = %d, want 1", queue.length())
	}
	block, ok := queue.pop()
	if !ok || block.length != audioBlockSamples {
		t.Fatalf("block = %#v, %v", block, ok)
	}
	if block.generation != 2 || !block.at.Equal(start) {
		t.Fatalf("block metadata = %#v", block)
	}
}

func TestAudioPackerResetsOnGenerationChange(t *testing.T) {
	queue := newBlockQueue(4)
	var packer audioPacker
	half := make([]int16, audioBlockSamples/2)
	packer.push(half, time.Unix(0, 0), 2, queue)
	if !packer.push(half, time.Unix(1, 0), 4, queue) {
		t.Fatal("generation change rejected")
	}
	if queue.length() != 0 {
		t.Fatal("a block survived a generation change")
	}
}

func TestAudioPackerRejectsUnboundedBacklog(t *testing.T) {
	queue := newBlockQueue(1)
	var packer audioPacker
	oversized := make([]int16, audioBlockSamples*audioQueueCapacity+1)
	if packer.push(oversized, time.Unix(0, 0), 2, queue) {
		t.Fatal("an unbounded backlog was accepted")
	}
	if len(packer.pending) != 0 {
		t.Fatalf("packer retained %d samples", len(packer.pending))
	}
}

func TestCaptureBoundaryRejectsPreUnmuteBuffer(t *testing.T) {
	var boundary captureBoundary
	start := time.Unix(100, 0)
	// A new generation always waits for the next callback.
	if boundary.accepts(2, start) {
		t.Fatal("the first callback of a generation must not be admitted")
	}
	if !boundary.accepts(2, start.Add(5*time.Millisecond)) {
		t.Fatal("the next callback was rejected")
	}
	if !boundary.accepts(2, start.Add(6*time.Millisecond)) {
		t.Fatal("a forward callback was rejected")
	}
	if boundary.accepts(2, start.Add(5500*time.Microsecond)) {
		t.Fatal("an out-of-order callback was admitted")
	}
	if boundary.accepts(2, start.Add(1*time.Millisecond)) {
		t.Fatal("a callback before the cutoff was admitted")
	}
	// A generation change resets the boundary.
	if boundary.accepts(4, start) {
		t.Fatal("a new generation was admitted immediately")
	}
}

func TestCaptureGapDiscardsPartialBlock(t *testing.T) {
	queue := newBlockQueue(4)
	var packer audioPacker
	start := time.Unix(0, 0)
	packer.push(make([]int16, audioBlockSamples/2), start, 2, queue)
	// A gap wider than the tolerance resets the partial block.
	packer.discardCaptureGap(start.Add(500*time.Millisecond), 48000)
	if len(packer.pending) != 0 {
		t.Fatalf("gap did not reset the partial block: %d", len(packer.pending))
	}
	// Jitter inside the tolerance keeps it.
	packer.push(make([]int16, audioBlockSamples/2), start, 2, queue)
	packer.discardCaptureGap(start.Add(audioCaptureGapTolerance/2), 48000)
	if len(packer.pending) != audioBlockSamples/2 {
		t.Fatalf("jitter reset the partial block: %d", len(packer.pending))
	}
}

func TestCaptureRejectsStaleBacklog(t *testing.T) {
	pipeline := newPCMPipeline()
	pipeline.microphone.setEnabled(true)
	pipeline.recordSessionStart()
	block := make([]int16, audioBlockSamples)
	now := time.Now()
	pipeline.pushCapture(block, now)
	pipeline.pushCapture(block, now.Add(10*time.Millisecond))
	if _, ok := pipeline.readCapture(now.Add(10 * time.Millisecond)); !ok {
		t.Fatal("fresh capture was dropped")
	}
	pipeline.pushCapture(block, now.Add(20*time.Millisecond))
	pipeline.pushCapture(block, now.Add(30*time.Millisecond))
	if _, ok := pipeline.readCapture(now.Add(audioMaxCaptureAge + time.Second)); ok {
		t.Fatal("stale capture was delivered")
	}
}

func TestPipelineMuteDropsQueuedCapture(t *testing.T) {
	pipeline := newPCMPipeline()
	pipeline.microphone.setEnabled(true)
	block := make([]int16, audioBlockSamples)
	now := time.Now()
	pipeline.pushCapture(block, now)
	pipeline.pushCapture(block, now.Add(10*time.Millisecond))
	if pipeline.capture.length() == 0 {
		t.Fatal("capture was not queued")
	}
	pipeline.setControls(AudioControls{MicrophoneMuted: true})
	if pipeline.capture.length() != 0 {
		t.Fatal("muting did not drop queued capture")
	}
	if _, ok := pipeline.readCapture(now.Add(20 * time.Millisecond)); ok {
		t.Fatal("muted pipeline delivered audio")
	}
}

func TestPipelineSuppressionNeverReplaysAudio(t *testing.T) {
	pipeline := newPCMPipeline()
	pipeline.speaker.setEnabled(true)
	var block audioBlock
	block.length = 2
	block.samples[0] = 1000
	if !pipeline.pushPlayback(block) {
		t.Fatal("unsuppressed playback was rejected")
	}
	pipeline.setControls(AudioControls{SpeakerSuppressed: true})
	if pipeline.playback.length() != 0 {
		t.Fatal("suppressing did not drop queued playback")
	}
	if !pipeline.pushPlayback(block) {
		// Suppressed writes are discarded rather than queued.
		t.Log("suppressed playback discarded")
	}
	if pipeline.playback.length() != 0 {
		t.Fatal("suppressed playback was queued")
	}
	pipeline.setControls(AudioControls{})
	var state audioBlock
	if _, ok := pipeline.nextPlaybackSample(&state); ok {
		t.Fatal("re-enabling replayed earlier audio")
	}
}

func TestPipelineQueueIsBounded(t *testing.T) {
	pipeline := newPCMPipeline()
	pipeline.speaker.setEnabled(true)
	var block audioBlock
	block.length = 1
	for index := 0; index < audioQueueCapacity+8; index++ {
		pipeline.pushPlayback(block)
	}
	if pipeline.playback.length() != audioQueueCapacity {
		t.Fatalf("queue length = %d, want %d", pipeline.playback.length(), audioQueueCapacity)
	}
}

func TestPipelineSpeakerPeakFollowsRenderedSamples(t *testing.T) {
	pipeline := newPCMPipeline()
	pipeline.speaker.setEnabled(true)
	var block audioBlock
	block.length = 2
	block.samples[0] = 100
	block.samples[1] = -200
	if !pipeline.pushPlayback(block) {
		t.Fatal("playback was rejected")
	}
	var state audioBlock
	if _, ok := pipeline.nextPlaybackSample(&state); !ok {
		t.Fatal("no rendered sample")
	}
	if _, ok := pipeline.nextPlaybackSample(&state); !ok {
		t.Fatal("no second rendered sample")
	}
	if _, ok := pipeline.nextPlaybackSample(&state); ok {
		t.Fatal("unexpected third sample")
	}
	if state := pipeline.takeState(); state.SpeakerPeak != 400 {
		t.Fatalf("speaker peak = %d, want 400", state.SpeakerPeak)
	}
}

func TestS16ConversionRoundTrip(t *testing.T) {
	samples := []int16{0, 1, -1, 32767, -32768}
	encoded := samplesS16LE(samples)
	if len(encoded) != len(samples)*2 {
		t.Fatalf("encoded length = %d", len(encoded))
	}
	decoded := s16leSamples(encoded)
	if len(decoded) != len(samples) {
		t.Fatalf("decoded length = %d", len(decoded))
	}
	for index := range samples {
		if decoded[index] != samples[index] {
			t.Fatalf("sample %d = %d, want %d", index, decoded[index], samples[index])
		}
	}
	if s16leSamples([]byte{1}) != nil {
		t.Fatal("an odd trailing byte was decoded")
	}
}
