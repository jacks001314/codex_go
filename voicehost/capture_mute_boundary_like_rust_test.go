package voicehost

import (
	"testing"
	"time"
)

// TestMediaSessionMuteBoundaryDropsPartialCaptureLikeRust mirrors Rust #43090:
// a mute transition clears the pending frame and the processing history, so
// samples captured before the transition never reach the peer and the first
// post-unmute frame starts from fresh audio.
func TestMediaSessionMuteBoundaryDropsPartialCaptureLikeRust(t *testing.T) {
	pipeline := newPCMPipeline()
	pipeline.microphone.setEnabled(true)
	pipeline.recordSessionStart()
	sender := &fakeSender{}
	session := newMediaSession(&fakeCodec{}, pipeline, sender)
	session.apm = nil
	now := time.Now()

	preMute := make([]int16, audioBlockSamples)
	for index := range preMute {
		preMute[index] = 111
	}
	postMute := make([]int16, audioBlockSamples)
	for index := range postMute {
		postMute[index] = 222
	}

	pipeline.pushCapture(preMute, now)
	pipeline.pushCapture(preMute, now.Add(10*time.Millisecond))
	if sent, err := session.service(now.Add(15 * time.Millisecond)); err != nil || sent != 0 {
		t.Fatalf("sent=%d err=%v, want no frame before a whole 20 ms block", sent, err)
	}
	if err := session.setControls(AudioControls{MicrophoneMuted: true}); err != nil {
		t.Fatal(err)
	}
	if err := session.setControls(AudioControls{MicrophoneMuted: false}); err != nil {
		t.Fatal(err)
	}
	pipeline.pushCapture(postMute, now.Add(40*time.Millisecond))
	pipeline.pushCapture(postMute, now.Add(50*time.Millisecond))
	pipeline.pushCapture(postMute, now.Add(60*time.Millisecond))
	sent, err := session.service(now.Add(70 * time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if sent != 1 || len(sender.frames) != 1 {
		t.Fatalf("sent=%d frames=%d, want one fresh frame", sent, len(sender.frames))
	}
	for index, value := range sender.frames[0].payload {
		if value != 222 {
			t.Fatalf("frame byte %d = %d, want post-mute audio only", index, value)
		}
	}
}
