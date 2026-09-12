package voicehost

import "math"

// Adaptive digital automatic gain control with a hard ceiling. It mirrors the
// adaptive-digital gain stage of the Rust helper's WebRTC APM: quiet near-end
// speech is lifted toward a target level, loud speech is attenuated, and the
// output never clips.

const (
	// agcTargetRMS is the target frame level (~ -26 dBFS).
	agcTargetRMS = 0.05
	// agcMaxGain bounds both boost and cut.
	agcMaxGain = 8.0
	// agcAttack smooths gain reductions quickly; agcRelease raises it slowly.
	agcAttack  = 0.5
	agcRelease = 0.05
	// agcCeiling is the limiter threshold.
	agcCeiling = 0.98
	// agcNoiseFloor ignores frames too quiet to yield a stable estimate.
	agcNoiseFloor = 1e-5
)

type gainController struct {
	gain float64
}

func newGainController() *gainController {
	return &gainController{gain: 1}
}

func (g *gainController) reset() {
	if g == nil {
		return
	}
	g.gain = 1
}

// process applies the smoothed gain and limits the result in place-safe form:
// the input frame is never mutated.
func (g *gainController) process(frame []float64) []float64 {
	output := make([]float64, len(frame))
	if g == nil || len(frame) == 0 {
		copy(output, frame)
		return output
	}
	if g.gain <= 0 {
		g.gain = 1
	}
	var sum float64
	for _, sample := range frame {
		sum += sample * sample
	}
	rms := math.Sqrt(sum / float64(len(frame)))
	desired := 1.0
	if rms > agcNoiseFloor {
		desired = agcTargetRMS / rms
		if desired > agcMaxGain {
			desired = agcMaxGain
		}
		if desired < 1/agcMaxGain {
			desired = 1 / agcMaxGain
		}
	}
	coefficient := agcRelease
	if desired < g.gain {
		coefficient = agcAttack
	}
	g.gain += coefficient * (desired - g.gain)
	if math.IsNaN(g.gain) || g.gain <= 0 {
		g.gain = 1
	}
	for index, sample := range frame {
		value := sample * g.gain
		if value > agcCeiling {
			value = agcCeiling
		} else if value < -agcCeiling {
			value = -agcCeiling
		}
		output[index] = value
	}
	return output
}
