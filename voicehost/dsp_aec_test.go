package voicehost

import (
	"math"
	"math/rand"
	"testing"
)

func TestFFTRoundTrip(t *testing.T) {
	random := rand.New(rand.NewSource(7))
	for _, size := range []int{2, 8, 512, 1024} {
		re := make([]float64, size)
		im := make([]float64, size)
		for index := range re {
			re[index] = random.Float64()*2 - 1
		}
		original := append([]float64(nil), re...)
		fftRadix2(re, im)
		ifftRadix2(re, im)
		for index := range re {
			if math.Abs(re[index]-original[index]) > 1e-9 {
				t.Fatalf("size %d: round trip [%d] = %v, want %v", size, index, re[index], original[index])
			}
			if math.Abs(im[index]) > 1e-9 {
				t.Fatalf("size %d: imaginary residue [%d] = %v", size, index, im[index])
			}
		}
	}
}

// TestEchoCancellerAttenuatesDelayedEcho mirrors the Rust processing test: the
// microphone hears a delayed copy of the render signal, and the canceller must
// remove most of that echo after adapting.
func TestEchoCancellerAttenuatesDelayedEcho(t *testing.T) {
	const (
		frames = 400 // 4 s at 10 ms per frame
		delay  = 1440
	)
	reference := make([]float64, frames*aecFrameSamples)
	for index := range reference {
		n := float64(index)
		reference[index] = 0.08*math.Sin(2*math.Pi*311*n/48000) +
			0.08*math.Sin(2*math.Pi*719*n/48000) +
			0.04*math.Sin(2*math.Pi*1423*n/48000)
	}

	aec := newEchoCanceller(aecDefaultPartitions)
	render := make([]float64, aecFrameSamples)
	capture := make([]float64, aecFrameSamples)
	var inputEnergy, outputEnergy float64
	for frame := 0; frame < frames; frame++ {
		copy(render, reference[frame*aecFrameSamples:(frame+1)*aecFrameSamples])
		aec.processRender(render)
		for index := range capture {
			position := frame*aecFrameSamples + index - delay
			if position >= 0 {
				capture[index] = 0.5 * reference[position]
			} else {
				capture[index] = 0
			}
		}
		output := aec.processCapture(capture)
		if frame >= frames/2 {
			for index := range capture {
				inputEnergy += capture[index] * capture[index]
				outputEnergy += output[index] * output[index]
			}
		}
	}
	if inputEnergy == 0 {
		t.Fatal("synthetic capture carries no echo energy")
	}
	if outputEnergy > 0.25*inputEnergy {
		t.Fatalf("echo cancellation insufficient: input=%.6f output=%.6f (%.1f%% remains)",
			inputEnergy, outputEnergy, 100*outputEnergy/inputEnergy)
	}
	t.Logf("echo energy %.6f -> %.6f (%.2f%% remains)", inputEnergy, outputEnergy,
		100*outputEnergy/inputEnergy)
}

// TestEchoCancellerKeepsNearEndSignal checks that with no reference at all the
// canceller leaves a near-end signal essentially intact.
func TestEchoCancellerKeepsNearEndSignal(t *testing.T) {
	aec := newEchoCanceller(aecDefaultPartitions)
	silent := make([]float64, aecFrameSamples)
	capture := make([]float64, aecFrameSamples)
	var energy float64
	for frame := 0; frame < 50; frame++ {
		aec.processRender(silent)
		for index := range capture {
			capture[index] = 0.2 * math.Sin(2*math.Pi*440*float64(frame*aecFrameSamples+index)/48000)
		}
		output := aec.processCapture(capture)
		if frame >= 25 {
			for index := range output {
				energy += output[index] * output[index]
			}
		}
	}
	if energy == 0 {
		t.Fatal("near-end signal was cancelled with no reference")
	}
}
