package voicehost

// Bounded device buffering for one host. Device callbacks allocate nothing and
// take no locks beyond a short queue push; the processing side packs small
// callbacks into fixed blocks, drops stale capture, and keeps mute transitions
// ordered with monotonic generations.

import (
	"sync"
	"sync/atomic"
	"time"
)

const (
	// audioBlockSamples is one packed processing block: 10 ms of 48 kHz mono.
	audioBlockSamples = 480
	// audioQueueCapacity bounds each direction's queued blocks.
	audioQueueCapacity = 32
	// audioCaptureGapTolerance absorbs ordinary device timestamp jitter,
	// consistently at both capture buffering boundaries.
	audioCaptureGapTolerance = 20 * time.Millisecond
	// audioMaxCaptureAge drops capture backlog that is older than this.
	audioMaxCaptureAge = time.Second
	// audioProcessingDeadline bounds end-to-end processing latency.
	audioProcessingDeadline = 500 * time.Millisecond
)

// audioBlock is one packed monaural block of signed 16-bit samples.
type audioBlock struct {
	samples    [audioBlockSamples]int16
	length     int
	offset     int
	at         time.Time
	generation uint64
}

// blockQueue is a bounded FIFO. A full queue rejects the incoming block so a
// slow consumer can resume with fresh audio instead of stale backlog.
type blockQueue struct {
	mu     sync.Mutex
	blocks []audioBlock
	limit  int
}

func newBlockQueue(limit int) *blockQueue {
	if limit <= 0 {
		limit = audioQueueCapacity
	}
	return &blockQueue{blocks: make([]audioBlock, 0, limit), limit: limit}
}

func (q *blockQueue) push(block audioBlock) bool {
	if q == nil {
		return false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.blocks) >= q.limit {
		return false
	}
	q.blocks = append(q.blocks, block)
	return true
}

func (q *blockQueue) pop() (audioBlock, bool) {
	if q == nil {
		return audioBlock{}, false
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	if len(q.blocks) == 0 {
		return audioBlock{}, false
	}
	block := q.blocks[0]
	q.blocks = q.blocks[1:]
	return block, true
}

func (q *blockQueue) drain() {
	if q == nil {
		return
	}
	q.mu.Lock()
	q.blocks = q.blocks[:0]
	q.mu.Unlock()
}

func (q *blockQueue) length() int {
	if q == nil {
		return 0
	}
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.blocks)
}

// audioGeneration tracks one direction's enabled/disabled epoch. Even values
// are enabled and odd values are disabled; every transition advances the value
// so a disable/re-enable cannot replay earlier audio.
type audioGeneration struct {
	value atomic.Uint64
}

func newAudioGeneration() *audioGeneration {
	generation := &audioGeneration{}
	generation.value.Store(1)
	return generation
}

func (g *audioGeneration) current() uint64 {
	if g == nil {
		return 1
	}
	return g.value.Load()
}

func (g *audioGeneration) enabled() bool {
	return g.current()%2 == 0
}

// setEnabled advances the epoch only when the requested state differs from the
// current parity, which makes the transition idempotent.
func (g *audioGeneration) setEnabled(enabled bool) {
	if g == nil {
		return
	}
	for {
		current := g.value.Load()
		if (current%2 == 0) == enabled {
			return
		}
		if g.value.CompareAndSwap(current, current+1) {
			return
		}
	}
}

// audioPacker packs variable-size callbacks into fixed blocks without
// allocating. Only complete blocks enter the queue, and the oldest sample keeps
// its timestamp.
type audioPacker struct {
	pending    []int16
	at         time.Time
	generation uint64
}

func (p *audioPacker) reset() {
	if p == nil {
		return
	}
	p.pending = p.pending[:0]
	p.at = time.Time{}
	p.generation = 0
}

// discardCaptureGap resets the partial block when capture resumes after a gap,
// so packing cannot hide the discontinuity inside an old block.
func (p *audioPacker) discardCaptureGap(start time.Time, rate int) {
	if p == nil || len(p.pending) == 0 || rate <= 0 {
		return
	}
	end := p.at.Add(time.Duration(int64(len(p.pending)) * int64(time.Second) / int64(rate)))
	if start.Sub(end) > audioCaptureGapTolerance {
		p.reset()
	}
}

// push appends samples and emits every complete block. A generation change
// discards the partial block, and a rejected block reports false so the caller
// can reset.
func (p *audioPacker) push(samples []int16, at time.Time, generation uint64, queue *blockQueue) bool {
	if p == nil || len(samples) == 0 {
		return true
	}
	if p.generation != generation && p.generation != 0 {
		p.reset()
	}
	if len(p.pending) == 0 {
		p.at = at
		p.generation = generation
	}
	if len(p.pending)+len(samples) > audioBlockSamples*audioQueueCapacity {
		p.reset()
		return false
	}
	p.pending = append(p.pending, samples...)
	for len(p.pending) >= audioBlockSamples {
		var block audioBlock
		copy(block.samples[:], p.pending[:audioBlockSamples])
		block.length = audioBlockSamples
		block.offset = 0
		block.at = p.at
		block.generation = generation
		p.pending = append(p.pending[:0], p.pending[audioBlockSamples:]...)
		p.at = block.at.Add(time.Duration(int64(audioBlockSamples) * int64(time.Second) / 48000))
		if !queue.push(block) {
			return false
		}
	}
	return true
}

// captureBoundary rejects capture buffers sampled before the current unmute
// boundary. This is conservative software admission, not a guarantee of
// hardware clock accuracy.
type captureBoundary struct {
	generation uint64
	cutoff     time.Time
	previous   time.Time
	waiting    bool
}

func (b *captureBoundary) reset() {
	if b == nil {
		return
	}
	*b = captureBoundary{}
}

func (b *captureBoundary) accepts(generation uint64, at time.Time) bool {
	if b == nil {
		return true
	}
	if generation%2 == 1 || b.generation != generation {
		b.generation = generation
		b.cutoff = time.Time{}
		b.previous = time.Time{}
		// The current callback may have been sampled before the transition, so
		// wait for the next callback before establishing a cutoff.
		b.waiting = true
		return false
	}
	if b.waiting {
		b.cutoff = at
		b.waiting = false
	}
	if at.Before(b.cutoff) || (!b.previous.IsZero() && at.Before(b.previous)) {
		return false
	}
	b.previous = at
	return true
}

// pcmPipeline owns the bounded device buffers and the mute generations for one
// host.
type pcmPipeline struct {
	capture  *blockQueue
	rendered *blockQueue
	playback *blockQueue

	microphone *audioGeneration
	speaker    *audioGeneration

	capturePacker  audioPacker
	playbackPacker audioPacker
	boundary       captureBoundary

	microphonePeak atomic.Uint32
	speakerPeak    atomic.Uint32
	droppedCapture atomic.Bool
	droppedRender  atomic.Bool
	serviced       atomic.Bool
}

func newPCMPipeline() *pcmPipeline {
	return &pcmPipeline{
		capture:    newBlockQueue(audioQueueCapacity),
		rendered:   newBlockQueue(audioQueueCapacity),
		playback:   newBlockQueue(audioQueueCapacity),
		microphone: newAudioGeneration(),
		speaker:    newAudioGeneration(),
	}
}

// pushCapture admits one capture callback. It reports false when the callback
// was rejected, which tells the device callback to reset its partial block.
func (p *pcmPipeline) pushCapture(samples []int16, at time.Time) bool {
	if p == nil {
		return true
	}
	generation := p.microphone.current()
	if generation%2 == 1 {
		p.capturePacker.reset()
		return true
	}
	if !p.boundary.accepts(generation, at) {
		p.capturePacker.reset()
		return true
	}
	p.capturePacker.discardCaptureGap(at, 48000)
	if !p.capturePacker.push(samples, at, generation, p.capture) {
		p.capturePacker.reset()
		p.droppedCapture.Store(true)
		return false
	}
	recordUint32Peak(&p.microphonePeak, pcmS16Peak(samples))
	return true
}

// readCapture pops the next fresh block for the upstream encoder.
func (p *pcmPipeline) readCapture(now time.Time) (audioBlock, bool) {
	if p == nil {
		return audioBlock{}, false
	}
	generation := p.microphone.current()
	for index := 0; index < audioQueueCapacity; index++ {
		block, ok := p.capture.pop()
		if !ok {
			return audioBlock{}, false
		}
		if generation%2 == 1 || block.generation != generation {
			continue
		}
		if now.Sub(block.at) > audioMaxCaptureAge {
			continue
		}
		return block, true
	}
	return audioBlock{}, false
}

// pushPlayback enqueues one decoded playback block. Suppressed audio is
// discarded so a later unmute cannot replay it.
func (p *pcmPipeline) pushPlayback(block audioBlock) bool {
	if p == nil {
		return false
	}
	generation := p.speaker.current()
	if generation%2 == 1 {
		return false
	}
	block.generation = generation
	return p.playback.push(block)
}

// nextPlaybackSample returns one sample for the device output callback. A
// generation mismatch or an empty queue reads as silence.
func (p *pcmPipeline) nextPlaybackSample(state *audioBlock) (int16, bool) {
	if p == nil || state == nil {
		return 0, false
	}
	generation := p.speaker.current()
	if generation%2 == 1 {
		*state = audioBlock{}
		return 0, false
	}
	if state.length == 0 || state.generation != generation {
		*state = audioBlock{}
		for index := 0; index < audioQueueCapacity; index++ {
			block, ok := p.playback.pop()
			if !ok {
				break
			}
			if block.generation == generation && block.length > 0 {
				*state = block
				break
			}
		}
	}
	if state.length == 0 {
		return 0, false
	}
	sample := state.samples[state.offset]
	state.offset++
	state.length--
	recordUint32Peak(&p.speakerPeak, s16Level(sample))
	return sample, true
}

// takeState returns the accumulated peaks and clears them.
func (p *pcmPipeline) takeState() AudioState {
	if p == nil {
		return AudioState{}
	}
	return AudioState{
		MicrophonePeak: uint16(p.microphonePeak.Swap(0)),
		SpeakerPeak:    uint16(p.speakerPeak.Swap(0)),
	}
}

// setControls applies an ordered privacy snapshot by advancing the epochs.
func (p *pcmPipeline) setControls(controls AudioControls) {
	if p == nil {
		return
	}
	previous := p.speaker.current()
	p.microphone.setEnabled(!controls.MicrophoneMuted)
	p.speaker.setEnabled(!controls.SpeakerSuppressed)
	if previous != p.speaker.current() {
		// Every queued block belongs to the previous epoch.
		p.playback.drain()
		p.rendered.drain()
	}
	if p.microphone.current()%2 == 1 {
		p.capture.drain()
		p.capturePacker.reset()
	}
}

// recordSessionStart marks the pipeline as serviced, which is what makes the
// output callback emit audio instead of silence.
func (p *pcmPipeline) recordSessionStart() {
	if p == nil {
		return
	}
	p.serviced.Store(true)
}

func (p *pcmPipeline) sessionServiced() bool {
	return p != nil && p.serviced.Load()
}

func recordUint32Peak(target *atomic.Uint32, value uint32) {
	if target == nil || value == 0 {
		return
	}
	for {
		current := target.Load()
		if value <= current {
			return
		}
		if target.CompareAndSwap(current, value) {
			return
		}
	}
}

func absInt32(value int32) int32 {
	if value < 0 {
		return -value
	}
	return value
}

// pcmS16Peak returns the normalized peak of signed 16-bit samples.
func pcmS16Peak(samples []int16) uint32 {
	var peak uint32
	for _, sample := range samples {
		value := s16Level(sample)
		if value > peak {
			peak = value
		}
	}
	return peak
}

// s16Level maps one sample to the reported unsigned 16-bit level, matching the
// Rust helper's f32 scale.
func s16Level(sample int16) uint32 {
	level := absInt32(int32(sample)) * 2
	if level > 65535 {
		return 65535
	}
	return uint32(level)
}
