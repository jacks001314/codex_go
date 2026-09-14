package voicehost

import "encoding/binary"

// samplesS16LE encodes samples as signed 16-bit little-endian bytes.
func samplesS16LE(samples []int16) []byte {
	data := make([]byte, len(samples)*2)
	for index, sample := range samples {
		binary.LittleEndian.PutUint16(data[index*2:], uint16(sample))
	}
	return data
}

// pcmPeak returns the normalized peak of signed 16-bit little-endian samples as
// an unsigned 16-bit level. The Rust helper derives the same scale from f32
// samples, so the reported magnitude stays comparable.
func pcmPeak(data []byte) uint16 {
	return uint16(pcmS16Peak(s16leSamples(data)))
}

// s16leSamples decodes signed 16-bit little-endian bytes, ignoring an odd
// trailing byte.
func s16leSamples(data []byte) []int16 {
	count := len(data) / 2
	if count == 0 {
		return nil
	}
	samples := make([]int16, count)
	for index := 0; index < count; index++ {
		samples[index] = int16(binary.LittleEndian.Uint16(data[index*2:]))
	}
	return samples
}
