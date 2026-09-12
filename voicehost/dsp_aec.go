package voicehost

import "math"

// Partitioned-block frequency-domain echo canceller. Each partition is one
// 10 ms (480 sample) 48 kHz frame; the render reference is the speaker signal
// the peer will hear, and the capture frame is the microphone signal containing
// that echo plus near-end speech. The filter adapts with a normalized LMS rule
// in the frequency domain, mirroring the echo cancellation stage of the Rust
// helper's WebRTC APM.

const (
	// aecFrameSamples is one partition/hop: the 10 ms 48 kHz APM frame.
	aecFrameSamples = 480
	// aecFFTSize keeps each partition a true linear convolution (N >= 2B-1).
	aecFFTSize = 1024
	// aecDefaultPartitions gives a ~130 ms echo tail.
	aecDefaultPartitions = 13
	// aecPowerSmoothing tracks the reference power spectrum over time.
	aecPowerSmoothing = 0.9
	// aecStepSize is the normalized adaptation rate. Values above ~0.22 make
	// the partitioned update diverge; 0.15 leaves margin while converging.
	aecStepSize = 0.15
	// aecRegularization floors the normalization power.
	aecRegularization = 1e-8
)

type echoCanceller struct {
	partitions int
	step       float64

	// reference is a ring of the most recent FFT-size reference samples.
	reference []float64
	refPos    int
	frames    int

	// xRe/xIm hold the stored reference spectra, one slot per partition.
	xRe [][]float64
	xIm [][]float64
	// hRe/hIm are the adaptive filter partitions.
	hRe [][]float64
	hIm [][]float64
	// power tracks the smoothed reference power spectrum per slot.
	power [][]float64

	workRe   []float64
	workIm   []float64
	freqRe   []float64
	freqIm   []float64
	powerSum []float64
}

func newEchoCanceller(partitions int) *echoCanceller {
	if partitions <= 0 {
		partitions = aecDefaultPartitions
	}
	e := &echoCanceller{
		partitions: partitions,
		step:       aecStepSize,
		reference:  make([]float64, aecFFTSize),
		workRe:     make([]float64, aecFFTSize),
		workIm:     make([]float64, aecFFTSize),
		freqRe:     make([]float64, aecFFTSize),
		freqIm:     make([]float64, aecFFTSize),
		powerSum:   make([]float64, aecFFTSize),
	}
	e.xRe = make([][]float64, partitions)
	e.xIm = make([][]float64, partitions)
	e.hRe = make([][]float64, partitions)
	e.hIm = make([][]float64, partitions)
	e.power = make([][]float64, partitions)
	for index := 0; index < partitions; index++ {
		e.xRe[index] = make([]float64, aecFFTSize)
		e.xIm[index] = make([]float64, aecFFTSize)
		e.hRe[index] = make([]float64, aecFFTSize)
		e.hIm[index] = make([]float64, aecFFTSize)
		e.power[index] = make([]float64, aecFFTSize)
	}
	return e
}

// reset clears the reference history and the adaptive filter.
func (e *echoCanceller) reset() {
	if e == nil {
		return
	}
	for index := range e.reference {
		e.reference[index] = 0
	}
	for slot := 0; slot < e.partitions; slot++ {
		clear(e.xRe[slot])
		clear(e.xIm[slot])
		clear(e.hRe[slot])
		clear(e.hIm[slot])
		clear(e.power[slot])
	}
	e.refPos = 0
	e.frames = 0
}

// processRender feeds one 10 ms echo reference frame. Frames with a different
// length are ignored so a malformed callback cannot desynchronize the filter.
func (e *echoCanceller) processRender(reference []float64) {
	if e == nil || len(reference) != aecFrameSamples {
		return
	}
	for _, sample := range reference {
		e.reference[e.refPos] = sample
		e.refPos = (e.refPos + 1) % aecFFTSize
	}
	slot := e.frames % e.partitions
	for index := 0; index < aecFFTSize; index++ {
		e.workRe[index] = e.reference[(e.refPos+index)%aecFFTSize]
		e.workIm[index] = 0
	}
	fftRadix2(e.workRe, e.workIm)
	copy(e.xRe[slot], e.workRe)
	copy(e.xIm[slot], e.workIm)
	for bin := 0; bin < aecFFTSize; bin++ {
		magnitude := e.workRe[bin]*e.workRe[bin] + e.workIm[bin]*e.workIm[bin]
		e.power[slot][bin] = aecPowerSmoothing*e.power[slot][bin] + (1-aecPowerSmoothing)*magnitude
	}
	e.frames++
}

// processCapture cancels the reference echo in one 10 ms capture frame and
// returns the near-end estimate. The input is never mutated.
func (e *echoCanceller) processCapture(capture []float64) []float64 {
	output := make([]float64, len(capture))
	if e == nil || len(capture) != aecFrameSamples || e.frames == 0 {
		copy(output, capture)
		return output
	}
	// Accumulate every partition's contribution to the current capture frame.
	clear(e.freqRe)
	clear(e.freqIm)
	slots := make([]int, 0, e.partitions)
	current := e.frames - 1
	for partition := 0; partition < e.partitions; partition++ {
		frame := current - partition
		if frame < 0 {
			break
		}
		slot := frame % e.partitions
		slots = append(slots, slot)
		xRe, xIm := e.xRe[slot], e.xIm[slot]
		hRe, hIm := e.hRe[slot], e.hIm[slot]
		for bin := 0; bin < aecFFTSize; bin++ {
			e.freqRe[bin] += hRe[bin]*xRe[bin] - hIm[bin]*xIm[bin]
			e.freqIm[bin] += hRe[bin]*xIm[bin] + hIm[bin]*xRe[bin]
		}
	}
	ifftRadix2(e.freqRe, e.freqIm)
	offset := aecFFTSize - aecFrameSamples
	for index := 0; index < aecFrameSamples; index++ {
		estimate := e.freqRe[offset+index]
		if math.IsNaN(estimate) || math.IsInf(estimate, 0) {
			estimate = 0
		}
		output[index] = capture[index] - estimate
	}

	// Adaptation error spectrum: zero-padded to the FFT size.
	for index := 0; index < aecFFTSize; index++ {
		if index < offset {
			e.workRe[index] = 0
			e.workIm[index] = 0
			continue
		}
		e.workRe[index] = output[index-offset]
		e.workIm[index] = 0
	}
	fftRadix2(e.workRe, e.workIm)
	errRe, errIm := e.workRe, e.workIm

	// Shared normalization power across the contributing partitions keeps a
	// quiet partition from taking an unbounded step.
	totalPower := e.powerSum
	clear(totalPower)
	for _, slot := range slots {
		for bin := 0; bin < aecFFTSize; bin++ {
			totalPower[bin] += e.power[slot][bin]
		}
	}
	for _, slot := range slots {
		xRe, xIm := e.xRe[slot], e.xIm[slot]
		hRe, hIm := e.hRe[slot], e.hIm[slot]
		for bin := 0; bin < aecFFTSize; bin++ {
			denominator := totalPower[bin] + aecRegularization
			// conj(X) * E
			gradRe := (xRe[bin]*errRe[bin] + xIm[bin]*errIm[bin]) / denominator
			gradIm := (xRe[bin]*errIm[bin] - xIm[bin]*errRe[bin]) / denominator
			hRe[bin] += e.step * gradRe
			hIm[bin] += e.step * gradIm
		}
	}
	return output
}
