package voicehost

import (
	"testing"
	"time"
)

// alternatingBlock is a Nyquist-rate tone, which survives the APM high-pass
// filter so frame payloads can be attributed to their source block.
func alternatingBlock(amplitude int16) []int16 {
	samples := make([]int16, audioBlockSamples)
	for index := range samples {
		if index%2 == 0 {
			samples[index] = amplitude
		} else {
			samples[index] = -amplitude
		}
	}
	return samples
}

// TestMediaSessionDropsStaleCaptureLikeRust mirrors Rust #44671: a capture
// block older than the 500 ms processing deadline is discarded together with
// the pending frame and processor history, and fresh audio resumes cleanly.
func TestMediaSessionDropsStaleCaptureLikeRust(t *testing.T) {
	pipeline := newPCMPipeline()
	pipeline.microphone.setEnabled(true)
	pipeline.recordSessionStart()
	sender := &fakeSender{}
	session := newMediaSession(&fakeCodec{}, pipeline, sender)
	session.apm = nil
	now := time.Unix(2000, 0)

	stale := make([]int16, audioBlockSamples)
	for index := range stale {
		stale[index] = 111
	}
	fresh := alternatingBlock(8000)

	// The first callback after an unmute only establishes the cutoff.
	pipeline.pushCapture(stale, now.Add(-600*time.Millisecond))
	pipeline.pushCapture(stale, now.Add(-590*time.Millisecond))
	sent, err := session.pumpCapture(now)
	if err != nil {
		t.Fatal(err)
	}
	if sent != 0 || len(sender.frames) != 0 {
		t.Fatalf("sent=%d frames=%d, want stale capture dropped", sent, len(sender.frames))
	}
	if len(session.pending) != 0 || !session.pendingAt.IsZero() {
		t.Fatalf("pending=%d at=%v, want no retained stale samples", len(session.pending), session.pendingAt)
	}

	pipeline.pushCapture(fresh, now)
	pipeline.pushCapture(fresh, now.Add(10*time.Millisecond))
	sent, err = session.pumpCapture(now.Add(15 * time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if sent != 1 || len(sender.frames) != 1 {
		t.Fatalf("sent=%d frames=%d, want one fresh frame", sent, len(sender.frames))
	}
	if !sender.frames[0].at.Equal(now) {
		t.Fatalf("frame at=%v, want %v", sender.frames[0].at, now)
	}
	posInt, negInt := int16(8000), int16(-8000)
	pos, neg := byte(posInt), byte(negInt)
	for index, value := range sender.frames[0].payload {
		if value != pos && value != neg {
			t.Fatalf("frame byte %d = %d, want fresh audio only", index, value)
		}
	}
}

// TestMediaSessionDropsLatePendingFrameLikeRust mirrors Rust #44671: a pending
// frame that aged past the processing deadline is dropped before send instead
// of shipping audio the peer no longer needs.
func TestMediaSessionDropsLatePendingFrameLikeRust(t *testing.T) {
	pipeline := newPCMPipeline()
	pipeline.microphone.setEnabled(true)
	pipeline.recordSessionStart()
	sender := &fakeSender{}
	session := newMediaSession(&fakeCodec{}, pipeline, sender)
	session.apm = nil
	now := time.Unix(3000, 0)

	old := make([]int16, audioBlockSamples)
	for index := range old {
		old[index] = 111
	}
	fresh := alternatingBlock(8000)

	pipeline.pushCapture(old, now.Add(-20*time.Millisecond))
	pipeline.pushCapture(old, now)
	sent, err := session.pumpCapture(now.Add(15 * time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if sent != 0 || len(session.pending) != audioBlockSamples {
		t.Fatalf("sent=%d pending=%d, want one buffered block", sent, len(session.pending))
	}

	pipeline.pushCapture(fresh, now.Add(600*time.Millisecond))
	sent, err = session.pumpCapture(now.Add(605 * time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if sent != 0 || len(sender.frames) != 0 {
		t.Fatalf("sent=%d frames=%d, want late pending frame dropped", sent, len(sender.frames))
	}
	if len(session.pending) != 0 || !session.pendingAt.IsZero() {
		t.Fatalf("pending=%d at=%v, want partial frame discarded", len(session.pending), session.pendingAt)
	}

	pipeline.pushCapture(fresh, now.Add(610*time.Millisecond))
	pipeline.pushCapture(fresh, now.Add(620*time.Millisecond))
	sent, err = session.pumpCapture(now.Add(625 * time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if sent != 1 || len(sender.frames) != 1 {
		t.Fatalf("sent=%d frames=%d, want one fresh frame", sent, len(sender.frames))
	}
	if !sender.frames[0].at.Equal(now.Add(610 * time.Millisecond)) {
		t.Fatalf("frame at=%v, want %v", sender.frames[0].at, now.Add(610*time.Millisecond))
	}
	posInt, negInt := int16(8000), int16(-8000)
	pos, neg := byte(posInt), byte(negInt)
	for index, value := range sender.frames[0].payload {
		if value != pos && value != neg {
			t.Fatalf("frame byte %d = %d, want fresh audio only", index, value)
		}
	}
}

// TestMediaSessionDrainsCaptureBacklogAfterOverflowLikeRust mirrors Rust
// #44671: when a device callback is rejected the whole capture backlog is
// discarded so the next frame starts from fresh audio.
func TestMediaSessionDrainsCaptureBacklogAfterOverflowLikeRust(t *testing.T) {
	pipeline := newPCMPipeline()
	pipeline.microphone.setEnabled(true)
	pipeline.recordSessionStart()
	sender := &fakeSender{}
	session := newMediaSession(&fakeCodec{}, pipeline, sender)
	session.apm = nil
	now := time.Unix(4000, 0)

	backlog := make([]int16, audioBlockSamples)
	for index := range backlog {
		backlog[index] = 111
	}
	fresh := alternatingBlock(8000)

	pipeline.pushCapture(backlog, now.Add(-10*time.Millisecond))
	rejected := false
	for index := 0; index < audioQueueCapacity*2; index++ {
		at := now.Add(time.Duration(index) * 10 * time.Millisecond)
		if !pipeline.pushCapture(backlog, at) {
			rejected = true
		}
	}
	if !rejected {
		t.Fatal("capture queue never rejected a block")
	}
	sent, err := session.service(now.Add(20 * time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if sent != 0 || len(sender.frames) != 0 {
		t.Fatalf("sent=%d frames=%d, want overflow backlog discarded", sent, len(sender.frames))
	}

	pipeline.pushCapture(fresh, now.Add(time.Second))
	pipeline.pushCapture(fresh, now.Add(time.Second+10*time.Millisecond))
	sent, err = session.service(now.Add(time.Second + 15*time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if sent != 1 || len(sender.frames) != 1 {
		t.Fatalf("sent=%d frames=%d, want one fresh frame after overflow", sent, len(sender.frames))
	}
	posInt, negInt := int16(8000), int16(-8000)
	pos, neg := byte(posInt), byte(negInt)
	for index, value := range sender.frames[0].payload {
		if value != pos && value != neg {
			t.Fatalf("frame byte %d = %d, want fresh audio only", index, value)
		}
	}
}

// TestMediaSessionLateCaptureResetsProcessorLikeRust mirrors Rust #44671: a
// stale capture drop also clears the APM history so residual echo references
// from before the gap cannot cancel fresh audio.
func TestMediaSessionLateCaptureResetsProcessorLikeRust(t *testing.T) {
	pipeline := newPCMPipeline()
	pipeline.microphone.setEnabled(true)
	pipeline.recordSessionStart()
	session := newMediaSession(&fakeCodec{}, pipeline, &fakeSender{})
	now := time.Unix(5000, 0)

	session.apm.processRender(make([]float64, audioBlockSamples))
	if session.apm.echo.frames == 0 {
		t.Fatal("render reference was not recorded")
	}

	stale := alternatingBlock(8000)
	pipeline.pushCapture(stale, now.Add(-600*time.Millisecond))
	pipeline.pushCapture(stale, now.Add(-590*time.Millisecond))
	if _, err := session.pumpCapture(now); err != nil {
		t.Fatal(err)
	}
	if session.apm.echo.frames != 0 {
		t.Fatalf("echo frames=%d, want processing history reset", session.apm.echo.frames)
	}
	if len(session.pending) != 0 || !session.pendingAt.IsZero() {
		t.Fatalf("pending=%d at=%v, want no retained stale samples", len(session.pending), session.pendingAt)
	}
}
