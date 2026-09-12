package voicehost

import (
	"math"
	"math/rand"
	"testing"
)

func TestNoiseSuppressorReducesStationaryNoise(t *testing.T) {
	ns := newNoiseSuppressor()
	random := rand.New(rand.NewSource(11))
	frame := make([]float64, 480)
	var inputEnergy, outputEnergy float64
	for call := 0; call < 100; call++ {
		for index := range frame {
			frame[index] = (random.Float64()*2 - 1) * 0.02
		}
		output := ns.process(frame)
		if call >= 60 {
			for index := range output {
				inputEnergy += frame[index] * frame[index]
				outputEnergy += output[index] * output[index]
			}
		}
	}
	if inputEnergy == 0 {
		t.Fatal("no noise energy")
	}
	if outputEnergy > 0.5*inputEnergy {
		t.Fatalf("stationary noise not suppressed: input=%.6f output=%.6f", inputEnergy, outputEnergy)
	}
	t.Logf("noise energy %.6f -> %.6f (%.1f%% remains)", inputEnergy, outputEnergy, 100*outputEnergy/inputEnergy)
}

// TestNoiseSuppressorPreservesSpeechBursts uses an amplitude-gated tone so the
// signal is bursty like speech; the slow rising noise floor must not attenuate
// active speech. (A perfectly steady tone is indistinguishable from stationary
// noise and is expected to be suppressed.)
func TestNoiseSuppressorPreservesSpeechBursts(t *testing.T) {
	ns := newNoiseSuppressor()
	frame := make([]float64, 480)
	var inputEnergy, outputEnergy float64
	for call := 0; call < 200; call++ {
		burst := call%20 < 10
		for index := range frame {
			frame[index] = 0
			if burst {
				frame[index] = 0.2 * math.Sin(2*math.Pi*440*float64(call*480+index)/48000)
			}
		}
		output := ns.process(frame)
		if call >= 100 && burst {
			for index := range output {
				inputEnergy += frame[index] * frame[index]
				outputEnergy += output[index] * output[index]
			}
		}
	}
	if outputEnergy < 0.4*inputEnergy {
		t.Fatalf("speech bursts were attenuated: input=%.6f output=%.6f", inputEnergy, outputEnergy)
	}
}

func TestGainControllerLiftsQuietSpeech(t *testing.T) {
	agc := newGainController()
	frame := make([]float64, 480)
	var inputEnergy, outputEnergy float64
	for call := 0; call < 200; call++ {
		for index := range frame {
			frame[index] = 0.005 * math.Sin(2*math.Pi*440*float64(call*480+index)/48000)
		}
		output := agc.process(frame)
		if call >= 100 {
			for index := range output {
				inputEnergy += frame[index] * frame[index]
				outputEnergy += output[index] * output[index]
			}
		}
	}
	if outputEnergy <= 1.5*inputEnergy {
		t.Fatalf("quiet speech was not lifted: input=%.8f output=%.8f", inputEnergy, outputEnergy)
	}
}

func TestGainControllerLimitsLoudSpeech(t *testing.T) {
	agc := newGainController()
	frame := make([]float64, 480)
	for call := 0; call < 50; call++ {
		for index := range frame {
			frame[index] = 0.9 * math.Sin(2*math.Pi*440*float64(call*480+index)/48000)
		}
		for _, sample := range agc.process(frame) {
			if math.Abs(sample) > 1 {
				t.Fatalf("AGC output clipped: %v", sample)
			}
		}
	}
}
