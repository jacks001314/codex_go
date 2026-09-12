package voicehost

// audioProcessor is the Go equivalent of the Rust helper's WebRTC APM. It runs
// echo cancellation, noise suppression, and adaptive digital gain over 10 ms
// (480 sample) 48 kHz frames, using the render (speaker) signal as the echo
// reference. The Rust helper builds sonora::AudioProcessing; this is a pure-Go
// approximation of the same signal chain rather than a bit-identical port.

type audioProcessor struct {
	echo    *echoCanceller
	noise   *noiseSuppressor
	gain    *gainController
	filter  *highPassFilter
	delay   *delayLine
	delayMS int
}

func newAudioProcessor() *audioProcessor {
	return &audioProcessor{
		echo:  newEchoCanceller(0),
		noise: newNoiseSuppressor(),
		gain:  newGainController(),
		// WebRTC's APM applies an 80 Hz capture high-pass before echo
		// cancellation; the delay line realises set_stream_delay_ms.
		filter: newHighPassFilter(resampleOutputRate, 80),
		delay:  newDelayLine(0),
	}
}

// reset drops all filter, noise-floor, and gain history. Mute transitions and
// capture gaps call it so audio from before the boundary cannot leak.
func (p *audioProcessor) reset() {
	if p == nil {
		return
	}
	p.echo.reset()
	p.noise.reset()
	p.gain.reset()
	p.filter.reset()
	p.delay.reset()
}

// processRender feeds one 10 ms echo reference frame (the decoded audio that is
// about to be played), delayed by the configured stream delay so it aligns with
// the capture frame that may still carry its echo.
func (p *audioProcessor) processRender(frame []float64) {
	if p == nil {
		return
	}
	p.echo.processRender(p.delay.process(frame))
}

// processCapture runs the capture chain and returns a new frame. The echo
// canceller absorbs the render/capture delay inside its partition tail, so the
// stream delay reported by the caller is recorded for diagnostics only.
func (p *audioProcessor) processCapture(frame []float64) []float64 {
	if p == nil || len(frame) == 0 {
		return append([]float64(nil), frame...)
	}
	output := p.filter.process(frame)
	output = p.echo.processCapture(output)
	output = p.noise.process(output)
	output = p.gain.process(output)
	return output
}

// setStreamDelayMS delays the echo reference by the measured capture/render
// delay, mirroring WebRTC's APM stream delay.
func (p *audioProcessor) setStreamDelayMS(delay int) {
	if p == nil {
		return
	}
	if delay < 0 {
		delay = 0
	}
	if delay == p.delayMS {
		return
	}
	p.delayMS = delay
	p.delay.setDelay(delay * resampleOutputRate / 1000)
}

// streamDelayMS reports the configured delay.
func (p *audioProcessor) streamDelayMS() int {
	if p == nil {
		return 0
	}
	return p.delayMS
}
