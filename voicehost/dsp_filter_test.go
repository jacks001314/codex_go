package voicehost

import (
	"math"
	"testing"
)

func TestHighPassFilterRemovesDCAndKeepsSpeechBand(t *testing.T) {
	filter := newHighPassFilter(48000, 80)
	const frames = 20
	frame := make([]float64, 480)
	var toneIn, toneOut float64
	for call := 0; call < frames; call++ {
		for index := range frame {
			frame[index] = 0.5 + 0.2*math.Sin(2*math.Pi*1000*float64(call*480+index)/48000)
		}
		output := filter.process(frame)
		if call < frames/2 {
			continue
		}
		for index := range output {
			toneIn += (0.2 * 0.2) / 2
			toneOut += output[index] * output[index]
		}
	}
	if ratio := toneOut / toneIn; ratio < 0.6 || ratio > 1.6 {
		t.Fatalf("1 kHz band ratio = %.3f, want ~1", ratio)
	}
	dcFilter := newHighPassFilter(48000, 80)
	dc := make([]float64, 480)
	for index := range dc {
		dc[index] = 1
	}
	var last float64
	for call := 0; call < 50; call++ {
		output := dcFilter.process(dc)
		last = output[len(output)-1]
	}
	if math.Abs(last) > 1e-3 {
		t.Fatalf("DC residual = %v, want ~0", last)
	}
}

func TestDelayLineDelaysTheReference(t *testing.T) {
	line := newDelayLine(3)
	output := line.process([]float64{1, 2, 3, 4, 5})
	want := []float64{0, 0, 0, 1, 2}
	for index := range want {
		if output[index] != want[index] {
			t.Fatalf("delayed output = %v, want %v", output, want)
		}
	}
	line.setDelay(0)
	pass := line.process([]float64{7, 8})
	if pass[0] != 7 || pass[1] != 8 {
		t.Fatalf("zero delay output = %v", pass)
	}
}

func TestAudioProcessorStreamDelayIsConfigurable(t *testing.T) {
	processor := newAudioProcessor()
	processor.setStreamDelayMS(40)
	if processor.streamDelayMS() != 40 {
		t.Fatalf("stream delay = %d, want 40", processor.streamDelayMS())
	}
	silence := make([]float64, aecFrameSamples)
	for call := 0; call < 20; call++ {
		processor.processRender(silence)
		for _, value := range processor.processCapture(silence) {
			if math.IsNaN(value) || math.IsInf(value, 0) {
				t.Fatalf("non-finite output with stream delay: %v", value)
			}
		}
	}
	processor.setStreamDelayMS(-5)
	if processor.streamDelayMS() != 0 {
		t.Fatalf("negative delay = %d, want 0", processor.streamDelayMS())
	}
}
