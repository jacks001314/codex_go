package voicehost

// Opus RTP media path for one host. The contract mirrors the Rust helper's
// audio track and inbound interceptor: a single 48 kHz monaural Opus stream on
// payload type 111, 20 ms frames, generated silence while muted, bounded
// inbound admission, and an epoch that makes suppression irrevocable for audio
// already in flight.
//
// The codec itself is a platform dependency. Rust ships a private libopus build
// inside its voice runtime; the Go helper accepts a VoiceCodec so a packaged
// codec can be supplied without changing this contract.

import (
	"errors"
	"sync"
	"time"
)

const (
	// opusPayloadType is the dynamic payload type both implementations use.
	opusPayloadType = 111
	// opusClockRate is the RTP clock for Opus.
	opusClockRate = 48000
	// voiceFrameSamples is one 20 ms monaural frame.
	voiceFrameSamples = 960
	// voiceFrameDuration is the packetization interval.
	voiceFrameDuration = 20 * time.Millisecond
	// voiceSilenceBuffer is the encoder output bound for one frame.
	voiceSilenceBuffer = 1275
	// voiceSendTimeout bounds one outbound packet write.
	voiceSendTimeout = 100 * time.Millisecond

	// voiceIngressPackets bounds unread inbound packets.
	voiceIngressPackets = 64
	// voiceIngressBytes bounds unread inbound audio.
	voiceIngressBytes = 2 * 1024 * 1024
	// voiceIngressPacketBytes bounds one inbound payload.
	voiceIngressPacketBytes = 64 * 1024
	// voiceIngressMaxAge drops inbound audio older than this.
	voiceIngressMaxAge = time.Second
	// voicePlayoutLatency is the jitter buffer's playout delay.
	voicePlayoutLatency = 60 * time.Millisecond
)

// ErrVoiceCodecUnavailable indicates that no audio codec is packaged for this
// host, so the media path cannot encode or decode. The control plane still
// works.
var ErrVoiceCodecUnavailable = errors.New("voice audio codec is unavailable")

// defaultVoiceCodec is the host's packaged Opus codec. It stays nil until a
// packaged runtime supplies one, which leaves the control plane and device
// buffers running without media.
var defaultVoiceCodec VoiceCodec

// SetDefaultVoiceCodec installs the host's audio codec for subsequent helper
// sessions.
func SetDefaultVoiceCodec(codec VoiceCodec) {
	defaultVoiceCodec = codec
}

// codecReady reports whether an audio codec is available for this session.
func (m *mediaSession) codecReady() bool {
	return m != nil && m.track != nil && m.track.codec != nil
}

// VoiceCodec encodes and decodes 20 ms monaural 48 kHz frames.
type VoiceCodec interface {
	// Encode converts one frame of samples into an Opus payload.
	Encode(samples []int16) ([]byte, error)
	// Decode converts one Opus payload into samples.
	Decode(payload []byte) ([]int16, error)
}

// voiceTrack is one outbound Opus RTP stream. Muted capture sends generated
// silence and still advances the RTP clock, so the peer keeps its timing
// without hearing the room.
type voiceTrack struct {
	codec     VoiceCodec
	ssrc      uint32
	sequence  uint16
	timestamp uint32
	end       time.Time
	silence   []byte
	sender    voiceSender
}

// voiceRTPHeader is the subset of the RTP header the helper owns.
type voiceRTPHeader struct {
	PayloadType uint8
	Sequence    uint16
	Timestamp   uint32
	SSRC        uint32
	Marker      bool
}

// voiceSender is the outbound media sink. A sender may build and own the wire
// packetization; the header carries the contract the helper is responsible for.
// A nil payload is a header-only packet that advances the peer's clock.
type voiceSender interface {
	SendFrame(payload []byte, header voiceRTPHeader, at time.Time) error
}

func newVoiceTrack(codec VoiceCodec, ssrc uint32, sender voiceSender) *voiceTrack {
	return &voiceTrack{codec: codec, ssrc: ssrc, sender: sender}
}

// sendFrame writes one packet through the configured sender.
func (t *voiceTrack) sendFrame(payload []byte, header voiceRTPHeader, at time.Time) error {
	if t == nil || t.sender == nil {
		return errors.New("voice media sender is unavailable")
	}
	return t.sender.SendFrame(payload, header, at)
}

// send encodes and sends one captured frame.
func (t *voiceTrack) send(samples []int16, at time.Time) error {
	if t == nil || t.codec == nil {
		return ErrVoiceCodecUnavailable
	}
	if len(samples) == 0 {
		return nil
	}
	payload, err := t.codec.Encode(samples)
	if err != nil {
		return err
	}
	return t.sendPayload(payload, len(samples), at)
}

// sendSilence sends one pre-encoded silence frame, encoding it once. Time still
// advances the RTP clock, so muting never invents packet loss.
func (t *voiceTrack) sendSilence(at time.Time) error {
	if t == nil || t.codec == nil {
		return ErrVoiceCodecUnavailable
	}
	if t.silence == nil {
		payload, err := t.codec.Encode(make([]int16, voiceFrameSamples))
		if err != nil {
			return err
		}
		t.silence = payload
	}
	return t.sendPayload(t.silence, voiceFrameSamples, at)
}

// sendPayload advances the clock across any gap and writes one packet.
func (t *voiceTrack) sendPayload(payload []byte, samples int, at time.Time) error {
	if t == nil || t.sender == nil {
		return ErrVoiceCodecUnavailable
	}
	if samples <= 0 {
		return errors.New("voice frame carries no samples")
	}
	if !t.end.IsZero() && at.Before(t.end.Add(-voiceFrameDuration)) {
		// A frame older than the last one is dropped rather than reordered.
		return nil
	}
	if !t.end.IsZero() && at.After(t.end) {
		gap := at.Sub(t.end)
		steps := int(gap / voiceFrameDuration)
		if steps > 0 {
			// Emit header-only packets so the receiver's clock stays aligned
			// instead of seeing an invented burst of new audio.
			for index := 0; index < steps; index++ {
				if err := t.sendFrame(nil, t.nextHeader(false), t.end); err != nil {
					return err
				}
				t.advance(voiceFrameSamples)
			}
		}
	}
	if err := t.sendFrame(payload, t.nextHeader(true), at); err != nil {
		return err
	}
	t.advance(samples)
	if t.end.IsZero() || at.After(t.end) {
		t.end = at.Add(voiceFrameDuration)
	}
	return nil
}

func (t *voiceTrack) nextHeader(marker bool) voiceRTPHeader {
	return voiceRTPHeader{
		PayloadType: opusPayloadType,
		Sequence:    t.sequence,
		Timestamp:   t.timestamp,
		SSRC:        t.ssrc,
		Marker:      marker,
	}
}

func (t *voiceTrack) advance(samples int) {
	t.sequence++
	t.timestamp += uint32(samples)
}

// voiceIncoming admits inbound RTP with bounded packet and byte budgets, drops
// stale media, and applies the speaker epoch before playback sees anything.
type voiceIncoming struct {
	mu      sync.Mutex
	packets []voicePacket
	bytes   int
	stream  uint32
	started bool
	failed  bool
	epoch   uint64
}

type voicePacket struct {
	payload []byte
	at      time.Time
	epoch   uint64
}

func newVoiceIncoming() *voiceIncoming {
	return &voiceIncoming{epoch: 1}
}

// setSuppressed advances the epoch when the suppression parity changes.
func (v *voiceIncoming) setSuppressed(suppressed bool) error {
	if v == nil {
		return nil
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if (v.epoch%2 == 1) != suppressed {
		v.epoch++
		if suppressed {
			v.packets = nil
			v.bytes = 0
		}
	}
	return nil
}

// handleRTP is the ingress interceptor. It reports false when the stream failed
// closed, which stops the media session.
func (v *voiceIncoming) handleRTP(payload []byte, ssrc uint32, at time.Time) bool {
	if v == nil {
		return true
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.failed {
		return false
	}
	if len(payload) > voiceIngressPacketBytes {
		v.failed = true
		return false
	}
	if v.started && v.stream != ssrc {
		v.failed = true
		return false
	}
	v.stream = ssrc
	v.started = true
	if v.epoch%2 == 1 {
		return true
	}
	if len(v.packets) >= voiceIngressPackets || v.bytes+len(payload) > voiceIngressBytes {
		// Saturated media is discarded so a slow worker resumes with fresh
		// audio instead of stale backlog.
		return true
	}
	v.packets = append(v.packets, voicePacket{payload: payload, at: at, epoch: v.epoch})
	v.bytes += len(payload)
	return true
}

// take returns the next admissible packet, dropping stale or superseded media.
func (v *voiceIncoming) take(now time.Time) (voicePacket, bool) {
	if v == nil {
		return voicePacket{}, false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.failed {
		return voicePacket{}, false
	}
	for index := 0; index < voiceIngressPackets; index++ {
		if len(v.packets) == 0 {
			return voicePacket{}, false
		}
		packet := v.packets[0]
		v.packets = v.packets[1:]
		v.bytes -= len(packet.payload)
		if v.epoch%2 == 1 || packet.epoch != v.epoch {
			continue
		}
		if now.Sub(packet.at) > voiceIngressMaxAge {
			continue
		}
		return packet, true
	}
	return voicePacket{}, false
}

func (v *voiceIncoming) failedClosed() bool {
	if v == nil {
		return false
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	return v.failed
}

// voicePlayout holds decoded-order packets for the playout latency before
// releasing them, dropping anything that arrives too late to be useful.
type voicePlayout struct {
	mu       sync.Mutex
	pending  []voicePacket
	capacity int
}

func newVoicePlayout() *voicePlayout {
	return &voicePlayout{capacity: voiceIngressPackets}
}

func (p *voicePlayout) push(packet voicePacket) {
	if p == nil || len(packet.payload) == 0 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.pending) >= p.capacity {
		// Keep the newest audio when the buffer overflows.
		p.pending = p.pending[1:]
	}
	p.pending = append(p.pending, packet)
}

// due returns the next packet whose playout latency has elapsed.
func (p *voicePlayout) due(now time.Time) (voicePacket, bool) {
	if p == nil {
		return voicePacket{}, false
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for len(p.pending) > 0 {
		packet := p.pending[0]
		if now.Sub(packet.at) < voicePlayoutLatency {
			return voicePacket{}, false
		}
		p.pending = p.pending[1:]
		if now.Sub(packet.at) > voiceIngressMaxAge {
			// Too late to play; dropping keeps latency bounded.
			continue
		}
		return packet, true
	}
	return voicePacket{}, false
}

func (p *voicePlayout) length() int {
	if p == nil {
		return 0
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.pending)
}

// mediaSession ties the outbound track, inbound admission, and playout to the
// device pipeline for one host.
type mediaSession struct {
	track    *voiceTrack
	incoming *voiceIncoming
	playout  *voicePlayout
	pipeline *pcmPipeline
	clock    func() time.Time

	// pending accumulates capture blocks into whole 20 ms Opus frames, matching
	// the Rust helper's encoder cadence and keeping the RTP clock exact.
	pending   []int16
	pendingAt time.Time
}

func newMediaSession(codec VoiceCodec, pipeline *pcmPipeline, sender voiceSender) *mediaSession {
	session := &mediaSession{
		track:    newVoiceTrack(codec, 1, sender),
		incoming: newVoiceIncoming(),
		playout:  newVoicePlayout(),
		pipeline: pipeline,
		clock:    time.Now,
	}
	// Devices open after negotiation and the startup controls are applied
	// before media flows, so a session that never suppresses the speaker is
	// audible from the start.
	_ = session.incoming.setSuppressed(false)
	return session
}

// handleRTP admits one inbound packet and queues it for playout.
func (m *mediaSession) handleRTP(payload []byte, ssrc uint32, at time.Time) error {
	if m == nil {
		return nil
	}
	if !m.incoming.handleRTP(payload, ssrc, at) {
		return errors.New("voice incoming audio failed")
	}
	if !m.codecReady() {
		// Without a decoder nothing can be played, so inbound audio is consumed
		// and discarded instead of accumulating in the bounded queue.
		for {
			if _, ok := m.incoming.take(m.clock()); !ok {
				break
			}
		}
		return nil
	}
	if packet, ok := m.incoming.take(m.clock()); ok {
		m.playout.push(packet)
	}
	return nil
}

// service pumps capture upstream and playout downstream. It returns the number
// of frames sent and reports the first fixed media failure.
func (m *mediaSession) service(now time.Time) (int, error) {
	if m == nil {
		return 0, nil
	}
	if !m.codecReady() {
		// Without a packaged codec no media can flow, but the control plane and
		// the device epochs stay active.
		return 0, nil
	}
	if m.incoming.failedClosed() {
		return 0, errors.New("voice incoming audio failed")
	}
	if err := m.drainPlayout(now); err != nil {
		return 0, err
	}
	sent, err := m.pumpCapture(now)
	if err != nil {
		return sent, err
	}
	if m.pipeline != nil && m.pipeline.microphone.current()%2 == 1 {
		// Muted sessions send generated silence so the peer stays alive.
		if err := m.track.sendSilence(now); err != nil {
			return sent, err
		}
	}
	return sent, nil
}

// drainPlayout decodes every due packet into the playback queue.
func (m *mediaSession) drainPlayout(now time.Time) error {
	if m.pipeline == nil || m.track.codec == nil {
		return nil
	}
	for index := 0; index < voiceIngressPackets; index++ {
		packet, ok := m.playout.due(now)
		if !ok {
			return nil
		}
		samples, err := m.track.codec.Decode(packet.payload)
		if err != nil {
			return err
		}
		for offset := 0; offset < len(samples); offset += audioBlockSamples {
			end := min(offset+audioBlockSamples, len(samples))
			var block audioBlock
			block.length = copy(block.samples[:], samples[offset:end])
			block.at = packet.at
			m.pipeline.pushPlayback(block)
		}
	}
	return nil
}

// pumpCapture encodes and sends every queued capture block.
func (m *mediaSession) pumpCapture(now time.Time) (int, error) {
	if m.pipeline == nil {
		return 0, nil
	}
	sent := 0
	for index := 0; index < audioQueueCapacity; index++ {
		block, ok := m.pipeline.readCapture(now)
		if !ok {
			break
		}
		if m.pendingAt.IsZero() {
			m.pendingAt = block.at
		}
		m.pending = append(m.pending, block.samples[:block.length]...)
		for len(m.pending) >= voiceFrameSamples {
			frame := m.pending[:voiceFrameSamples]
			if err := m.track.send(frame, m.pendingAt); err != nil {
				return sent, err
			}
			sent++
			m.pending = append(m.pending[:0], m.pending[voiceFrameSamples:]...)
			m.pendingAt = m.pendingAt.Add(voiceFrameDuration)
		}
	}
	return sent, nil
}

// dropPendingCapture discards a partial frame, which mute transitions require
// so pre-transition samples cannot reach the peer.
func (m *mediaSession) dropPendingCapture() {
	if m == nil {
		return
	}
	m.pending = m.pending[:0]
	m.pendingAt = time.Time{}
}

// setControls applies the ordered privacy snapshot to both directions.
func (m *mediaSession) setControls(controls AudioControls) error {
	if m == nil {
		return nil
	}
	if err := m.incoming.setSuppressed(controls.SpeakerSuppressed); err != nil {
		return err
	}
	if controls.MicrophoneMuted {
		m.dropPendingCapture()
	}
	if m.pipeline != nil {
		m.pipeline.setControls(controls)
	}
	return nil
}

// takeAudioState returns the pipeline's accumulated levels.
func (m *mediaSession) takeAudioState() AudioState {
	if m == nil || m.pipeline == nil {
		return AudioState{}
	}
	return m.pipeline.takeState()
}
