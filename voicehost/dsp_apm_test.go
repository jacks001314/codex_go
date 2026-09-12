package voicehost

import (
	"math"
	"testing"
)

// TestAudioProcessorUsesRenderReferenceForEcho mirrors the Rust differential
// test: two identical processors receive the same echo capture, but only one is
// fed the render reference. The echoed energy must be lower with the reference.
func TestAudioProcessorUsesRenderReferenceForEcho(t *testing.T) {
	const (
		frames = 400
		delay  = 1440
	)
	reference := make([]float64, frames*aecFrameSamples)
	for index := range reference {
		n := float64(index)
		reference[index] = 0.08*math.Sin(2*math.Pi*311*n/48000) +
			0.08*math.Sin(2*math.Pi*719*n/48000) +
			0.04*math.Sin(2*math.Pi*1423*n/48000)
	}
	withReference := newAudioProcessor()
	withoutReference := newAudioProcessor()
	silence := make([]float64, aecFrameSamples)
	render := make([]float64, aecFrameSamples)
	capture := make([]float64, aecFrameSamples)
	var referencedEnergy, controlEnergy float64
	for frame := 0; frame < frames; frame++ {
		copy(render, reference[frame*aecFrameSamples:(frame+1)*aecFrameSamples])
		withReference.processRender(render)
		withoutReference.processRender(silence)
		for index := range capture {
			position := frame*aecFrameSamples + index - delay
			capture[index] = 0
			if position >= 0 {
				capture[index] = 0.5 * reference[position]
			}
		}
		referenced := withReference.processCapture(capture)
		control := withoutReference.processCapture(capture)
		if frame >= frames/2 {
			for index := range capture {
				referencedEnergy += referenced[index] * referenced[index]
				controlEnergy += control[index] * control[index]
			}
		}
	}
	if controlEnergy == 0 {
		t.Fatal("control capture carries no echo energy")
	}
	if referencedEnergy > 0.6*controlEnergy {
		t.Fatalf("render reference did not reduce echo: with=%.6f without=%.6f",
			referencedEnergy, controlEnergy)
	}
	t.Logf("echo energy with reference %.6f vs control %.6f (%.1f%%)",
		referencedEnergy, controlEnergy, 100*referencedEnergy/controlEnergy)
}

// TestAudioProcessorChainStaysFiniteAndLiftsSpeech checks the full AEC+NS+AGC
// chain: bursty near-end speech survives, and no stage emits a non-finite value.
func TestAudioProcessorChainStaysFiniteAndLiftsSpeech(t *testing.T) {
	apm := newAudioProcessor()
	silence := make([]float64, aecFrameSamples)
	frame := make([]float64, aecFrameSamples)
	var inputEnergy, outputEnergy float64
	for call := 0; call < 200; call++ {
		burst := call%20 < 10
		for index := range frame {
			frame[index] = 0
			if burst {
				frame[index] = 0.005 * math.Sin(2*math.Pi*500*float64(call*480+index)/48000)
			}
		}
		apm.processRender(silence)
		output := apm.processCapture(frame)
		for _, value := range output {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				t.Fatalf("non-finite DSP output at call %d: %v", call, value)
			}
		}
		if call >= 100 && burst {
			for index := range output {
				inputEnergy += frame[index] * frame[index]
				outputEnergy += output[index] * output[index]
			}
		}
	}
	if inputEnergy == 0 {
		t.Fatal("no near-end speech energy")
	}
	if outputEnergy < 0.5*inputEnergy {
		t.Fatalf("quiet speech was attenuated by the chain: input=%.8f output=%.8f", inputEnergy, outputEnergy)
	}
}
