package voicehost

import (
	"errors"
	"testing"
	"time"
)

// fakeCodec maps one sample to one payload byte, which is enough to assert the
// media contract without a packaged Opus runtime.
type fakeCodec struct {
	encodeErr error
	decodeErr error
}

func (c *fakeCodec) Encode(samples []int16) ([]byte, error) {
	if c.encodeErr != nil {
		return nil, c.encodeErr
	}
	payload := make([]byte, len(samples))
	for index, sample := range samples {
		payload[index] = byte(sample)
	}
	return payload, nil
}

func (c *fakeCodec) Decode(payload []byte) ([]int16, error) {
	if c.decodeErr != nil {
		return nil, c.decodeErr
	}
	samples := make([]int16, len(payload))
	for index, value := range payload {
		samples[index] = int16(value)
	}
	return samples, nil
}

type sentFrame struct {
	payload []byte
	header  voiceRTPHeader
	at      time.Time
}

type fakeSender struct {
	frames []sentFrame
	err    error
}

func (s *fakeSender) SendFrame(payload []byte, header voiceRTPHeader, at time.Time) error {
	if s.err != nil {
		return s.err
	}
	s.frames = append(s.frames, sentFrame{payload: append([]byte(nil), payload...), header: header, at: at})
	return nil
}

func TestVoiceTrackUsesOpusPayloadTypeAndAdvancingClock(t *testing.T) {
	sender := &fakeSender{}
	track := newVoiceTrack(&fakeCodec{}, 7, sender)
	start := time.Unix(1000, 0)
	frame := make([]int16, voiceFrameSamples)
	if err := track.send(frame, start); err != nil {
		t.Fatal(err)
	}
	if err := track.send(frame, start.Add(voiceFrameDuration)); err != nil {
		t.Fatal(err)
	}
	if len(sender.frames) != 2 {
		t.Fatalf("frames = %d", len(sender.frames))
	}
	first, second := sender.frames[0].header, sender.frames[1].header
	if first.PayloadType != opusPayloadType || first.SSRC != 7 {
		t.Fatalf("first header = %#v", first)
	}
	if second.Timestamp != first.Timestamp+voiceFrameSamples {
		t.Fatalf("timestamp = %d, want %d", second.Timestamp, first.Timestamp+voiceFrameSamples)
	}
	if second.Sequence != first.Sequence+1 {
		t.Fatalf("sequence = %d, want %d", second.Sequence, first.Sequence+1)
	}
	if !first.Marker || !second.Marker {
		t.Fatal("audio frames must set the marker bit")
	}
}

func TestVoiceTrackFillsGapsWithHeaderOnlyPackets(t *testing.T) {
	sender := &fakeSender{}
	track := newVoiceTrack(&fakeCodec{}, 1, sender)
	start := time.Unix(1000, 0)
	frame := make([]int16, voiceFrameSamples)
	if err := track.send(frame, start); err != nil {
		t.Fatal(err)
	}
	// Two frame slots later, header-only packets advance the clock before the
	// next real frame.
	if err := track.send(frame, start.Add(3*voiceFrameDuration)); err != nil {
		t.Fatal(err)
	}
	if len(sender.frames) != 4 {
		t.Fatalf("frames = %d, want 4", len(sender.frames))
	}
	if len(sender.frames[1].payload) != 0 || sender.frames[1].header.Marker {
		t.Fatalf("gap frame = %#v", sender.frames[1])
	}
	if len(sender.frames[2].payload) != 0 {
		t.Fatalf("second gap frame = %#v", sender.frames[2])
	}
	if sender.frames[3].header.Timestamp != sender.frames[0].header.Timestamp+3*voiceFrameSamples {
		t.Fatalf("resumed timestamp = %d", sender.frames[3].header.Timestamp)
	}
}

func TestVoiceTrackReusesEncodedSilence(t *testing.T) {
	sender := &fakeSender{}
	track := newVoiceTrack(&fakeCodec{}, 1, sender)
	start := time.Unix(1000, 0)
	if err := track.sendSilence(start); err != nil {
		t.Fatal(err)
	}
	first := append([]byte(nil), track.silence...)
	if err := track.sendSilence(start.Add(voiceFrameDuration)); err != nil {
		t.Fatal(err)
	}
	if len(track.silence) != len(first) {
		t.Fatalf("silence was re-encoded: %d != %d", len(track.silence), len(first))
	}
	if len(sender.frames) != 2 || len(sender.frames[0].payload) == 0 {
		t.Fatalf("frames = %#v", sender.frames)
	}
	// The reused payload still advances the clock.
	if sender.frames[1].header.Timestamp <= sender.frames[0].header.Timestamp {
		t.Fatal("muted silence did not advance the RTP clock")
	}
}

func TestVoiceTrackRequiresCodecAndSender(t *testing.T) {
	if err := newVoiceTrack(nil, 1, &fakeSender{}).send(make([]int16, 1), time.Now()); !errors.Is(err, ErrVoiceCodecUnavailable) {
		t.Fatalf("error = %v", err)
	}
	if err := newVoiceTrack(&fakeCodec{}, 1, nil).send(make([]int16, 1), time.Now()); err == nil {
		t.Fatal("a track without a sender accepted a frame")
	}
}

func TestVoiceIncomingBoundsAndRejectsMixedStreams(t *testing.T) {
	incoming := newVoiceIncoming()
	incoming.setSuppressed(false)
	now := time.Now()
	if !incoming.handleRTP([]byte{1}, 5, now) {
		t.Fatal("first packet was rejected")
	}
	if incoming.handleRTP([]byte{1}, 6, now) {
		t.Fatal("a second SSRC was accepted")
	}
	if !incoming.failedClosed() {
		t.Fatal("the mixed stream did not fail closed")
	}

	oversized := newVoiceIncoming()
	if oversized.handleRTP(make([]byte, voiceIngressPacketBytes+1), 1, now) {
		t.Fatal("an oversized payload was accepted")
	}

	bounded := newVoiceIncoming()
	if err := bounded.setSuppressed(false); err != nil {
		t.Fatal(err)
	}
	for index := 0; index < voiceIngressPackets+8; index++ {
		bounded.handleRTP([]byte{1}, 1, now)
	}
	bounded.mu.Lock()
	queued := len(bounded.packets)
	bounded.mu.Unlock()
	if queued != voiceIngressPackets {
		t.Fatalf("queued packets = %d, want %d", queued, voiceIngressPackets)
	}
}

func TestVoiceIncomingSuppressionDropsQueuedAudio(t *testing.T) {
	incoming := newVoiceIncoming()
	now := time.Now()
	incoming.handleRTP([]byte{1, 2, 3}, 1, now)
	if err := incoming.setSuppressed(true); err != nil {
		t.Fatal(err)
	}
	if _, ok := incoming.take(now); ok {
		t.Fatal("suppressed audio was delivered")
	}
	// Unsuppressing must not resurrect the earlier epoch.
	if err := incoming.setSuppressed(false); err != nil {
		t.Fatal(err)
	}
	if _, ok := incoming.take(now); ok {
		t.Fatal("a superseded epoch was delivered")
	}
	incoming.handleRTP([]byte{4, 5, 6}, 1, now)
	if _, ok := incoming.take(now); !ok {
		t.Fatal("fresh audio was dropped")
	}
}

func TestVoiceIncomingDropsStaleAudio(t *testing.T) {
	incoming := newVoiceIncoming()
	now := time.Now()
	incoming.handleRTP([]byte{1}, 1, now)
	if _, ok := incoming.take(now.Add(voiceIngressMaxAge + time.Second)); ok {
		t.Fatal("stale audio was delivered")
	}
}

func TestVoicePlayoutHoldsPacketsForItsLatency(t *testing.T) {
	playout := newVoicePlayout()
	now := time.Now()
	playout.push(voicePacket{payload: []byte{1}, at: now})
	if _, ok := playout.due(now); ok {
		t.Fatal("a packet played before the jitter buffer latency elapsed")
	}
	if _, ok := playout.due(now.Add(voicePlayoutLatency)); !ok {
		t.Fatal("a due packet was not released")
	}
	if playout.length() != 0 {
		t.Fatalf("playout length = %d", playout.length())
	}
}

func TestVoicePlayoutKeepsNewestAudio(t *testing.T) {
	playout := newVoicePlayout()
	now := time.Now()
	for index := 0; index < voiceIngressPackets+4; index++ {
		playout.push(voicePacket{payload: []byte{byte(index)}, at: now})
	}
	if playout.length() != voiceIngressPackets {
		t.Fatalf("playout length = %d", playout.length())
	}
	playout.mu.Lock()
	last := playout.pending[len(playout.pending)-1].payload[0]
	playout.mu.Unlock()
	if last != byte(voiceIngressPackets+3) {
		t.Fatalf("newest packet = %d", last)
	}
}

func TestMediaSessionPumpsCaptureAndPlayout(t *testing.T) {
	pipeline := newPCMPipeline()
	pipeline.microphone.setEnabled(true)
	pipeline.speaker.setEnabled(true)
	pipeline.recordSessionStart()
	sender := &fakeSender{}
	session := newMediaSession(&fakeCodec{}, pipeline, sender)

	now := time.Now()
	block := make([]int16, audioBlockSamples)
	// The first accepted callback only establishes the unmute boundary, so two
	// admitted blocks are needed to complete one 20 ms frame.
	pipeline.pushCapture(block, now)
	pipeline.pushCapture(block, now.Add(10*time.Millisecond))
	pipeline.pushCapture(block, now.Add(20*time.Millisecond))
	sent, err := session.service(now.Add(30 * time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if sent != 1 {
		t.Fatalf("sent = %d, want 1", sent)
	}
	if len(sender.frames) != 1 {
		t.Fatalf("frames = %d, want 1", len(sender.frames))
	}
	// The track sends whole 20 ms frames, matching the Rust encoder cadence.
	if got := len(sender.frames[0].payload); got != voiceFrameSamples {
		t.Fatalf("frame samples = %d, want %d", got, voiceFrameSamples)
	}

	// An inbound packet becomes playback audio after the jitter latency.
	if err := session.handleRTP([]byte{9, 9, 9, 9}, 1, now); err != nil {
		t.Fatal(err)
	}
	if _, err := session.service(now.Add(voicePlayoutLatency + time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if queued := pipeline.playback.length(); queued != 1 {
		t.Fatalf("playback blocks = %d, want 1", queued)
	}
}

func TestMediaSessionSendsSilenceWhileMuted(t *testing.T) {
	pipeline := newPCMPipeline()
	pipeline.microphone.setEnabled(true)
	pipeline.recordSessionStart()
	sender := &fakeSender{}
	session := newMediaSession(&fakeCodec{}, pipeline, sender)
	if err := session.setControls(AudioControls{MicrophoneMuted: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.service(time.Now()); err != nil {
		t.Fatal(err)
	}
	if len(sender.frames) != 1 || len(sender.frames[0].payload) != voiceFrameSamples {
		t.Fatalf("frames = %#v", sender.frames)
	}
}

func TestMediaSessionSuppressesDecodedPlayback(t *testing.T) {
	pipeline := newPCMPipeline()
	pipeline.speaker.setEnabled(true)
	pipeline.recordSessionStart()
	sender := &fakeSender{}
	session := newMediaSession(&fakeCodec{}, pipeline, sender)
	now := time.Now()
	if err := session.handleRTP([]byte{1, 2}, 1, now); err != nil {
		t.Fatal(err)
	}
	if err := session.setControls(AudioControls{SpeakerSuppressed: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := session.service(now.Add(voicePlayoutLatency + time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if queued := pipeline.playback.length(); queued != 0 {
		t.Fatalf("suppressed playout queued %d blocks", queued)
	}
}

func TestMediaSessionWithoutCodecRunsControlPlaneOnly(t *testing.T) {
	pipeline := newPCMPipeline()
	sender := &fakeSender{}
	session := newMediaSession(nil, pipeline, sender)
	now := time.Now()
	block := make([]int16, audioBlockSamples)
	pipeline.pushCapture(block, now)
	sent, err := session.service(now)
	if err != nil {
		t.Fatalf("a missing codec must not fail the session: %v", err)
	}
	if sent != 0 || len(sender.frames) != 0 {
		t.Fatalf("sent=%d frames=%d", sent, len(sender.frames))
	}
	if err := session.setControls(AudioControls{MicrophoneMuted: true}); err != nil {
		t.Fatal(err)
	}
	if !pipeline.microphone.enabled() == false {
		t.Fatal("controls were not applied without a codec")
	}
}

func TestMediaSessionDropsStaleInboundWhenCodecMissing(t *testing.T) {
	pipeline := newPCMPipeline()
	session := newMediaSession(nil, pipeline, &fakeSender{})
	if err := session.handleRTP([]byte{1}, 1, time.Now()); err != nil {
		t.Fatal(err)
	}
	if session.playout.length() != 0 {
		t.Fatal("a packet was queued without a decoder")
	}
}
