package voicehost

import "math"

// Spectral-subtraction noise suppressor. A Hann-windowed STFT (512 sample
// frames, 50% overlap) tracks a per-bin noise floor and attenuates bins that
// sit at that floor, mirroring the noise-suppression stage of the Rust
// helper's WebRTC APM. The process call is rate-preserving: it always returns
// as many samples as it was given, with a small startup latency.

const (
	nsFrame = 512
	nsHop   = 256
	// nsOversubtraction controls how aggressively floor bins are removed.
	nsOversubtraction = 2.0
	// nsFloor keeps a residual of suppressed bins so speech is not chopped.
	nsFloor = 0.1
	// nsDownCoefficient tracks a falling noise floor quickly; nsUpCoefficient
	// follows a rising one very slowly so a steady tone is not treated as noise.
	nsDownCoefficient = 0.5
	nsUpCoefficient   = 0.01
	// nsSpeechRatio: when a frame's energy rises above the long-term average by
	// this factor the frame is treated as speech and the noise-floor update is
	// frozen, so active speech is not learned as noise while stationary noise
	// still converges.
	nsSpeechRatio = 1.5
	// nsSpeechDecay slowly releases the frozen floor so a changed noise floor is
	// still tracked during long speech.
	nsSpeechDecay = 0.999
	// nsLongTermCoefficient smooths the frame-energy average used by the gate.
	nsLongTermCoefficient = 0.05
)

type noiseSuppressor struct {
	window    []float64
	buffer    []float64
	overlap   []float64
	pending   []float64
	noise     []float64
	magnitude []float64
	longTerm  float64
	re        []float64
	im        []float64
	primed    bool
}

func newNoiseSuppressor() *noiseSuppressor {
	ns := &noiseSuppressor{
		window:    make([]float64, nsFrame),
		overlap:   make([]float64, nsFrame),
		noise:     make([]float64, nsFrame/2+1),
		magnitude: make([]float64, nsFrame/2+1),
		re:        make([]float64, nsFrame),
		im:        make([]float64, nsFrame),
	}
	for index := range ns.window {
		// sqrt-Hann for both analysis and synthesis so their product is Hann,
		// which is constant-overlap-add at 50% overlap (sums to 1).
		ns.window[index] = math.Sqrt(0.5 - 0.5*math.Cos(2*math.Pi*float64(index)/float64(nsFrame-1)))
	}
	return ns
}

func (n *noiseSuppressor) reset() {
	if n == nil {
		return
	}
	n.buffer = n.buffer[:0]
	n.pending = n.pending[:0]
	clear(n.overlap)
	clear(n.noise)
	n.primed = false
	n.longTerm = 0
}

// process returns len(frame) samples with stationary noise attenuated.
func (n *noiseSuppressor) process(frame []float64) []float64 {
	output := make([]float64, len(frame))
	if n == nil || len(frame) == 0 {
		copy(output, frame)
		return output
	}
	n.buffer = append(n.buffer, frame...)
	for len(n.buffer) >= nsFrame {
		n.analyze()
		n.buffer = append(n.buffer[:0], n.buffer[nsHop:]...)
	}
	for index := range output {
		if len(n.pending) == 0 {
			break
		}
		output[index] = n.pending[0]
		n.pending = n.pending[1:]
	}
	return output
}

func (n *noiseSuppressor) analyze() {
	for index := 0; index < nsFrame; index++ {
		n.re[index] = n.buffer[index] * n.window[index]
		n.im[index] = 0
	}
	fftRadix2(n.re, n.im)
	half := nsFrame / 2
	var energy, totalFloor float64
	for bin := 0; bin <= half; bin++ {
		n.magnitude[bin] = math.Hypot(n.re[bin], n.im[bin])
		energy += n.magnitude[bin] * n.magnitude[bin]
		totalFloor += n.noise[bin]
	}
	if !n.primed {
		n.longTerm = energy
	} else {
		n.longTerm = (1-nsLongTermCoefficient)*n.longTerm + nsLongTermCoefficient*energy
	}
	speech := n.primed && n.longTerm > 0 && energy > nsSpeechRatio*n.longTerm
	for bin := 0; bin <= half; bin++ {
		magnitude := n.magnitude[bin]
		floor := n.noise[bin]
		switch {
		case speech:
			floor *= nsSpeechDecay
		case magnitude < floor:
			floor = nsDownCoefficient*floor + (1-nsDownCoefficient)*magnitude
		default:
			floor = (1-nsUpCoefficient)*floor + nsUpCoefficient*magnitude
		}
		n.noise[bin] = floor
		gain := 1.0
		if magnitude > 1e-12 {
			gain = 1 - nsOversubtraction*floor/magnitude
			if gain < nsFloor {
				gain = nsFloor
			}
			if gain > 1 {
				gain = 1
			}
		}
		n.re[bin] *= gain
		n.im[bin] *= gain
		if bin > 0 && bin < half {
			mirror := nsFrame - bin
			n.re[mirror] = n.re[bin]
			n.im[mirror] = -n.im[bin]
		}
	}
	n.primed = true
	ifftRadix2(n.re, n.im)
	for index := 0; index < nsFrame; index++ {
		n.overlap[index] += n.re[index] * n.window[index]
	}
	for index := 0; index < nsHop; index++ {
		n.pending = append(n.pending, n.overlap[index])
	}
	copy(n.overlap, n.overlap[nsHop:])
	for index := nsFrame - nsHop; index < nsFrame; index++ {
		n.overlap[index] = 0
	}
}
