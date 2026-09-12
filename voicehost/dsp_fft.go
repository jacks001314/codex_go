package voicehost

// Iterative radix-2 complex FFT used by the voice DSP stages. The input length
// must be a power of two; both slices are transformed in place.

import "math"

// fftRadix2 performs an in-place forward FFT.
func fftRadix2(re, im []float64) {
	n := len(re)
	if n <= 1 {
		return
	}
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			re[i], re[j] = re[j], re[i]
			im[i], im[j] = im[j], im[i]
		}
	}
	for length := 2; length <= n; length <<= 1 {
		half := length >> 1
		angle := -2 * math.Pi / float64(length)
		wr := math.Cos(angle)
		wi := math.Sin(angle)
		for start := 0; start < n; start += length {
			cr, ci := 1.0, 0.0
			for j := 0; j < half; j++ {
				ur, ui := re[start+j], im[start+j]
				xr, xi := re[start+j+half], im[start+j+half]
				vr := xr*cr - xi*ci
				vi := xr*ci + xi*cr
				re[start+j] = ur + vr
				im[start+j] = ui + vi
				re[start+j+half] = ur - vr
				im[start+j+half] = ui - vi
				cr, ci = cr*wr-ci*wi, cr*wi+ci*wr
			}
		}
	}
}

// ifftRadix2 performs an in-place inverse FFT with 1/N scaling.
func ifftRadix2(re, im []float64) {
	n := len(re)
	if n <= 1 {
		return
	}
	for i := range im {
		im[i] = -im[i]
	}
	fftRadix2(re, im)
	scale := 1 / float64(n)
	for i := range re {
		re[i] *= scale
		im[i] = -im[i] * scale
	}
}
