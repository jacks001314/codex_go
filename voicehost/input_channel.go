package voicehost

import (
	"errors"
	"math"
	"sort"
)

// errSelectedChannelsUnavailable mirrors the Rust helper's rejection of an
// empty or out-of-range microphone channel selection. An explicit selection
// never falls back to other inputs.
var errSelectedChannelsUnavailable = errors.New("selected microphone channels unavailable")

// errSelectedDeviceUnavailable mirrors the Rust helper's rejection of a
// selected device whose name no longer matches an eligible endpoint.
var errSelectedDeviceUnavailable = errors.New("selected audio device unavailable")

// inputChannel mixes validated microphone channels before metering or
// processing captured audio. Channel numbers are one-based on the wire, are
// sorted and deduplicated, and never fall back to an unselected input.
type inputChannel struct {
	indices []int
}

// newInputChannel resolves a selection against the device's channel count. A
// nil selection keeps every channel, matching the Rust helper's legacy mix; an
// empty or out-of-range selection is rejected.
func newInputChannel(selected []uint16, channels int) (inputChannel, error) {
	indices := make([]int, 0, len(selected))
	if selected == nil {
		for index := 0; index < channels; index++ {
			indices = append(indices, index)
		}
	} else {
		for _, channel := range selected {
			indices = append(indices, int(channel)-1)
		}
	}
	if len(indices) == 0 {
		return inputChannel{}, errSelectedChannelsUnavailable
	}
	for _, index := range indices {
		if index < 0 || index >= channels {
			return inputChannel{}, errSelectedChannelsUnavailable
		}
	}
	sort.Ints(indices)
	deduped := indices[:1]
	for _, index := range indices[1:] {
		if index != deduped[len(deduped)-1] {
			deduped = append(deduped, index)
		}
	}
	return inputChannel{indices: deduped}, nil
}

// sample averages the selected channels of one interleaved frame. The caller
// guarantees the frame holds every channel produced by newInputChannel.
func (c inputChannel) sample(frame []int16) int16 {
	sum := 0
	for _, index := range c.indices {
		sum += int(frame[index])
	}
	mean := math.Round(float64(sum) / float64(len(c.indices)))
	if mean > math.MaxInt16 {
		mean = math.MaxInt16
	} else if mean < math.MinInt16 {
		mean = math.MinInt16
	}
	return int16(mean)
}

// mix reduces interleaved device samples to the mono pipeline. A partial
// trailing frame is discarded, matching the Rust helper's exact chunking.
func (c inputChannel) mix(samples []int16, channels int) []int16 {
	if channels <= 1 {
		return samples
	}
	if len(samples) < channels {
		return nil
	}
	mixed := make([]int16, 0, len(samples)/channels)
	for offset := 0; offset+channels <= len(samples); offset += channels {
		mixed = append(mixed, c.sample(samples[offset:offset+channels]))
	}
	return mixed
}
