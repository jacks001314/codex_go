package voicehost

import (
	"errors"
	"math"
	"time"
)

// Streaming windowed-sinc resampler and 48 kHz converter. This is the Go
// equivalent of the Rust helper's rubato-based Converter: it converts a device
// rate to 48 kHz, keeps bounded input/output FIFOs, and stamps each emitted
// 10 ms block with the timestamp derived from the frame end and the current
// resampler delay.

const (
	// resampleOutputRate is the voice pipeline rate.
	resampleOutputRate = 48000
	// resampleBlockSamples is one emitted processing block (10 ms at 48 kHz).
	resampleBlockSamples = 480
	// sincPhases is the number of fractional phases in the interpolation table.
	sincPhases = 256
	// sincHalfTaps is half the interpolation kernel width, in input samples.
	sincHalfTaps = 16
	// sincCutoff is the kernel cutoff as a fraction of Nyquist.
	sincCutoff = 0.95
)

var (
	// errResampleRate rejects a non-positive device rate.
	errResampleRate = errors.New("invalid voice resample rate")
	// errResamplerBacklog reports an unbounded resampler queue.
	errResamplerBacklog = errors.New("voice resampler backlog exceeded")
)

// sincResampler converts a fixed input rate to 48 kHz.
type sincResampler struct {
	rate     int
	stepNum  int64
	stepDen  int64
	identity bool

	buffer []float64
	// position of the next output sample, in input samples scaled by stepDen.
	position int64
	table    []float64
}

func newSincResampler(rate int) (*sincResampler, error) {
	if rate <= 0 {
		return nil, errResampleRate
	}
	resampler := &sincResampler{
		rate:    rate,
		stepNum: int64(rate),
		stepDen: resampleOutputRate,
	}
	if rate == resampleOutputRate {
		resampler.identity = true
		return resampler, nil
	}
	ratio := float64(resampleOutputRate) / float64(rate)
	cutoff := sincCutoff
	if ratio < 1 {
		// Downsampling must band-limit below the output Nyquist.
		cutoff *= ratio
	}
	resampler.buildTable(cutoff)
	// The kernel needs sincHalfTaps-1 samples of history before its center, so
	// the first output lands at that position instead of the (unreachable)
	// start of the buffer.
	resampler.position = int64(sincHalfTaps-1) * resampler.stepDen
	return resampler, nil
}

func (r *sincResampler) buildTable(cutoff float64) {
	taps := sincHalfTaps * 2
	table := make([]float64, sincPhases*taps)
	for phase := 0; phase < sincPhases; phase++ {
		fraction := float64(phase) / float64(sincPhases)
		var sum float64
		for tap := 0; tap < taps; tap++ {
			x := float64(tap-sincHalfTaps+1) - fraction
			weight := sincPi(x*cutoff) * blackmanWindow(x/float64(sincHalfTaps))
			table[phase*taps+tap] = weight
			sum += weight
		}
		if sum != 0 {
			for tap := 0; tap < taps; tap++ {
				table[phase*taps+tap] /= sum
			}
		}
	}
	r.table = table
}

// push appends input samples to the resampler's FIFO.
func (r *sincResampler) push(samples []float64) {
	r.buffer = append(r.buffer, samples...)
}

// available reports whether one more output sample can be produced.
func (r *sincResampler) available() bool {
	if r.identity {
		return r.position < int64(len(r.buffer))
	}
	center := r.position / r.stepDen
	low := center - sincHalfTaps + 1
	high := center + sincHalfTaps
	return low >= 0 && high < int64(len(r.buffer))
}

// next produces one output sample.
func (r *sincResampler) next() (float64, bool) {
	if !r.available() {
		return 0, false
	}
	if r.identity {
		value := r.buffer[r.position]
		r.position++
		return value, true
	}
	taps := sincHalfTaps * 2
	fraction := float64(r.position%r.stepDen) / float64(r.stepDen)
	phase := int(fraction * sincPhases)
	if phase >= sincPhases {
		phase = sincPhases - 1
	}
	center := r.position / r.stepDen
	base := center - sincHalfTaps + 1
	var sum float64
	for tap := 0; tap < taps; tap++ {
		sum += r.buffer[base+int64(tap)] * r.table[phase*taps+tap]
	}
	r.position += r.stepNum
	return sum, true
}

// pendingInput is the number of buffered input samples not yet consumed.
func (r *sincResampler) pendingInput() int {
	consumed := int(r.position / r.stepDen)
	pending := len(r.buffer) - consumed
	if pending < 0 {
		return 0
	}
	return pending
}

// outputDelay is the resampler's group delay in output samples.
func (r *sincResampler) outputDelay() int {
	if r.identity {
		return 0
	}
	return sincHalfTaps
}

// compact drops input samples that can no longer contribute.
func (r *sincResampler) compact() {
	if r.identity {
		consumed := int(r.position)
		if consumed <= 0 {
			return
		}
		r.buffer = append(r.buffer[:0], r.buffer[consumed:]...)
		r.position -= int64(consumed)
		return
	}
	keep := int(r.position/r.stepDen) - sincHalfTaps + 1
	if keep <= 0 {
		return
	}
	if keep > len(r.buffer) {
		keep = len(r.buffer)
	}
	r.buffer = append(r.buffer[:0], r.buffer[keep:]...)
	r.position -= int64(keep) * r.stepDen
}

// reset clears all buffered history.
func (r *sincResampler) reset() {
	r.buffer = r.buffer[:0]
	r.position = 0
}

// resampleConverter mirrors the Rust helper's Converter: it feeds device-rate
// frames and emits 48 kHz blocks with timestamps.
type resampleConverter struct {
	resampler *sincResampler
	output    []float64
	rate      int
	end       time.Time
}

func newResampleConverter(rate int) (*resampleConverter, error) {
	resampler, err := newSincResampler(rate)
	if err != nil {
		return nil, err
	}
	return &resampleConverter{resampler: resampler, rate: rate}, nil
}

// push feeds one device-rate frame captured at `at`.
func (c *resampleConverter) push(frame []float64, at time.Time) error {
	if len(frame) == 0 {
		return nil
	}
	if c.resampler.pendingInput()+len(frame) > c.rate || len(c.output) > resampleOutputRate {
		return errResamplerBacklog
	}
	c.end = at.Add(time.Duration(int64(len(frame)) * int64(time.Second) / int64(c.rate)))
	c.resampler.push(frame)
	for c.resampler.available() {
		value, ok := c.resampler.next()
		if !ok {
			break
		}
		c.output = append(c.output, value)
	}
	c.resampler.compact()
	return nil
}

// next returns the next 48 kHz block and its timestamp.
func (c *resampleConverter) next() (time.Time, []float64, bool) {
	if len(c.output) < resampleBlockSamples {
		return time.Time{}, nil, false
	}
	delay := float64(c.resampler.pendingInput())/float64(c.rate) +
		float64(len(c.output)+c.resampler.outputDelay())/float64(resampleOutputRate)
	at := c.end.Add(-time.Duration(delay * float64(time.Second)))
	block := make([]float64, resampleBlockSamples)
	copy(block, c.output[:resampleBlockSamples])
	c.output = append(c.output[:0], c.output[resampleBlockSamples:]...)
	return at, block, true
}

func (c *resampleConverter) reset() {
	if c == nil {
		return
	}
	c.resampler.reset()
	c.output = c.output[:0]
	c.end = time.Time{}
}

func sincPi(x float64) float64 {
	if x == 0 {
		return 1
	}
	px := math.Pi * x
	return math.Sin(px) / px
}

// blackmanWindow evaluates a Blackman window over t in [-1, 1].
func blackmanWindow(t float64) float64 {
	if t <= -1 || t >= 1 {
		return 0
	}
	return 0.42 + 0.5*math.Cos(math.Pi*t) + 0.08*math.Cos(2*math.Pi*t)
}
