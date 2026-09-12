package voicehost

import "math"

// Second-order high-pass filter (RBJ biquad, Q = 1/sqrt(2)). WebRTC's APM
// applies a capture high-pass stage before echo cancellation to remove DC and
// rumble; this is the pure-Go equivalent.

const highPassQ = math.Sqrt2 / 2

type highPassFilter struct {
	b0, b1, b2 float64
	a1, a2     float64
	x1, x2     float64
	y1, y2     float64
}

func newHighPassFilter(sampleRate, cutoff float64) *highPassFilter {
	if sampleRate <= 0 || cutoff <= 0 || cutoff >= sampleRate/2 {
		return &highPassFilter{b0: 1}
	}
	w0 := 2 * math.Pi * cutoff / sampleRate
	cosine := math.Cos(w0)
	sine := math.Sin(w0)
	alpha := sine / (2 * highPassQ)
	b0 := (1 + cosine) / 2
	b1 := -(1 + cosine)
	b2 := (1 + cosine) / 2
	a0 := 1 + alpha
	a1 := -2 * cosine
	a2 := 1 - alpha
	return &highPassFilter{
		b0: b0 / a0,
		b1: b1 / a0,
		b2: b2 / a0,
		a1: a1 / a0,
		a2: a2 / a0,
	}
}

// process returns a filtered copy of the frame.
func (f *highPassFilter) process(frame []float64) []float64 {
	output := make([]float64, len(frame))
	if f == nil {
		copy(output, frame)
		return output
	}
	for index, sample := range frame {
		value := f.b0*sample + f.b1*f.x1 + f.b2*f.x2 - f.a1*f.y1 - f.a2*f.y2
		f.x2, f.x1 = f.x1, sample
		f.y2, f.y1 = f.y1, value
		output[index] = value
	}
	return output
}

func (f *highPassFilter) reset() {
	if f == nil {
		return
	}
	f.x1, f.x2, f.y1, f.y2 = 0, 0, 0, 0
}

// delayLine delays the echo reference by a whole number of samples, mirroring
// WebRTC's set_stream_delay_ms: aligning the reference to the capture lets the
// adaptive filter spend its taps on the room impulse response instead of the
// known render/capture offset.
type delayLine struct {
	buffer []float64
	pos    int
}

func newDelayLine(samples int) *delayLine {
	if samples <= 0 {
		return &delayLine{}
	}
	return &delayLine{buffer: make([]float64, samples)}
}

// setDelay resizes the delay, preserving nothing (callers reset on change).
func (d *delayLine) setDelay(samples int) {
	if d == nil {
		return
	}
	if samples < 0 {
		samples = 0
	}
	if len(d.buffer) == samples {
		return
	}
	d.buffer = make([]float64, samples)
	d.pos = 0
}

// process returns the input delayed by the configured number of samples.
func (d *delayLine) process(frame []float64) []float64 {
	output := make([]float64, len(frame))
	if d == nil || len(d.buffer) == 0 {
		copy(output, frame)
		return output
	}
	for index, sample := range frame {
		output[index] = d.buffer[d.pos]
		d.buffer[d.pos] = sample
		d.pos = (d.pos + 1) % len(d.buffer)
	}
	return output
}

func (d *delayLine) reset() {
	if d == nil {
		return
	}
	for index := range d.buffer {
		d.buffer[index] = 0
	}
	d.pos = 0
}
