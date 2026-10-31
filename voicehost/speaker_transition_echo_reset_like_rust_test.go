package voicehost

import (
	"testing"
	"time"
)

// TestSpeakerTransitionResetsEchoReferenceLikeRust mirrors Rust #43248: a
// speaker epoch change discards the echo references queued for the retired
// epoch while the capture-side processing history is preserved.
func TestSpeakerTransitionResetsEchoReferenceLikeRust(t *testing.T) {
	pipeline := newPCMPipeline()
	pipeline.microphone.setEnabled(true)
	pipeline.speaker.setEnabled(true)
	pipeline.recordSessionStart()
	session := newMediaSession(&fakeCodec{}, pipeline, &fakeSender{})
	now := time.Now()

	// Inbound audio becomes an echo reference once its playout latency elapses.
	if err := session.handleRTP(make([]byte, audioBlockSamples), 1, now); err != nil {
		t.Fatal(err)
	}
	if _, err := session.service(now.Add(voicePlayoutLatency + time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if session.apm.echo.frames == 0 {
		t.Fatal("no echo reference was recorded")
	}

	// Quiet capture lifts the AGC, so capture-side history is observable.
	quiet := make([]int16, audioBlockSamples)
	for index := range quiet {
		quiet[index] = 200
	}
	pipeline.pushCapture(quiet, now)
	pipeline.pushCapture(quiet, now.Add(10*time.Millisecond))
	pipeline.pushCapture(quiet, now.Add(20*time.Millisecond))
if _, err := session.service(now.Add(30 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	gainBefore := session.apm.gain.gain
	if gainBefore <= 1 {
		t.Fatalf("capture gain = %v, want the AGC to lift quiet audio", gainBefore)
	}

	if err := session.setControls(AudioControls{SpeakerSuppressed: true}); err != nil {
		t.Fatal(err)
	}
	if got := session.apm.echo.frames; got != 0 {
		t.Fatalf("echo reference frames = %d, want the retired epoch discarded", got)
	}
	if got := session.apm.gain.gain; got != gainBefore {
		t.Fatalf("capture gain = %v, want %v preserved", got, gainBefore)
	}
}
