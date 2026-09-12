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
	delayMS int
}

func newAudioProcessor() *audioProcessor {
	return &audioProcessor{
		echo:  newEchoCanceller(0),
		noise: newNoiseSuppressor(),
		gain:  newGainController(),
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
}

// processRender feeds one 10 ms echo reference frame (the decoded audio that is
// about to be played).
func (p *audioProcessor) processRender(frame []float64) {
	if p == nil {
		return
	}
	p.echo.processRender(frame)
}

// processCapture runs the capture chain and returns a new frame. The echo
// canceller absorbs the render/capture delay inside its partition tail, so the
// stream delay reported by the caller is recorded for diagnostics only.
func (p *audioProcessor) processCapture(frame []float64) []float64 {
	if p == nil || len(frame) == 0 {
		return append([]float64(nil), frame...)
	}
	output := p.echo.processCapture(frame)
	output = p.noise.process(output)
	output = p.gain.process(output)
	return output
}

// setStreamDelayMS records the measured capture/render delay.
func (p *audioProcessor) setStreamDelayMS(delay int) {
	if p == nil {
		return
	}
	p.delayMS = delay
}
