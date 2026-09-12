package voicehost

import (
	"math"
	"testing"
	"time"
)

func TestSincResamplerIdentityAtOutputRate(t *testing.T) {
	resampler, err := newSincResampler(resampleOutputRate)
	if err != nil {
		t.Fatal(err)
	}
	input := make([]float64, 480)
	for index := range input {
		input[index] = math.Sin(2 * math.Pi * 440 * float64(index) / float64(resampleOutputRate))
	}
	resampler.push(input)
	output := make([]float64, 0, len(input))
	for resampler.available() {
		value, ok := resampler.next()
		if !ok {
			break
		}
		output = append(output, value)
	}
	if len(output) != len(input) {
		t.Fatalf("identity output length = %d, want %d", len(output), len(input))
	}
	for index := range input {
		if output[index] != input[index] {
			t.Fatalf("identity output[%d] = %v, want %v", index, output[index], input[index])
		}
	}
}

func TestResampleConverterUpsamples24kTo48k(t *testing.T) {
	const rate = 24000
	converter, err := newResampleConverter(rate)
	if err != nil {
		t.Fatal(err)
	}
	const frames = 100 // 1 s at 24 kHz
	const frameSamples = 240
	start := time.Now()
	emitted := 0
	blocks := 0
	var lastAt time.Time
	for frame := 0; frame < frames; frame++ {
		values := make([]float64, frameSamples)
		for index := range values {
			values[index] = math.Sin(2 * math.Pi * 1000 * float64(frame*frameSamples+index) / float64(rate))
		}
		at := start.Add(time.Duration(frame*frameSamples) * time.Second / rate)
		if err := converter.push(values, at); err != nil {
			t.Fatalf("push frame %d: %v", frame, err)
		}
		for {
			blockAt, block, ok := converter.next()
			if !ok {
				break
			}
			if len(block) != resampleBlockSamples {
				t.Fatalf("block length = %d", len(block))
			}
			if !lastAt.IsZero() && blockAt.Before(lastAt) {
				t.Fatalf("block timestamps went backwards: %v then %v", lastAt, blockAt)
			}
			lastAt = blockAt
			emitted += len(block)
			blocks++
		}
	}
	want := frames * frameSamples * 2
	if blocks == 0 {
		t.Fatal("no output blocks produced")
	}
	if math.Abs(float64(emitted)-float64(want))/float64(want) > 0.02 {
		t.Fatalf("upsampled %d samples, want about %d", emitted, want)
	}
	t.Logf("24k -> 48k: emitted %d samples in %d blocks (want ~%d)", emitted, blocks, want)
}

func TestResampleConverterPreservesToneAt44100(t *testing.T) {
	const rate = 44100
	converter, err := newResampleConverter(rate)
	if err != nil {
		t.Fatal(err)
	}
	const frames = 200 // ~1.09 s
	const frameSamples = 441
	start := time.Now()
	output := make([]float64, 0, frames*frameSamples*2)
	for frame := 0; frame < frames; frame++ {
		values := make([]float64, frameSamples)
		for index := range values {
			values[index] = 0.1 * math.Sin(2*math.Pi*1000*float64(frame*frameSamples+index)/float64(rate))
		}
		at := start.Add(time.Duration(frame*frameSamples) * time.Second / rate)
		if err := converter.push(values, at); err != nil {
			t.Fatalf("push frame %d: %v", frame, err)
		}
		for {
			_, block, ok := converter.next()
			if !ok {
				break
			}
			output = append(output, block...)
		}
	}
	if len(output) < resampleOutputRate {
		t.Fatalf("too little output: %d samples", len(output))
	}
	// Skip the filter warm-up, then estimate the frequency from zero crossings.
	trimmed := output[resampleOutputRate/10:]
	frequency := estimatedFrequency(trimmed, resampleOutputRate)
	if math.Abs(frequency-1000) > 100 {
		t.Fatalf("resampled tone frequency = %.1f Hz, want ~1000 Hz", frequency)
	}
	t.Logf("44.1k -> 48k: 1000 Hz tone measured at %.1f Hz", frequency)
}

func TestResampleConverterRejectsBacklog(t *testing.T) {
	converter, err := newResampleConverter(24000)
	if err != nil {
		t.Fatal(err)
	}
	oversized := make([]float64, 24001)
	if err := converter.push(oversized, time.Now()); err == nil {
		t.Fatal("oversized resampler backlog was accepted")
	}
}

func estimatedFrequency(samples []float64, rate int) float64 {
	crossings := 0
	for index := 1; index < len(samples); index++ {
		if (samples[index-1] < 0) != (samples[index] < 0) {
			crossings++
		}
	}
	duration := float64(len(samples)) / float64(rate)
	if duration == 0 {
		return 0
	}
	return float64(crossings) / 2 / duration
}
