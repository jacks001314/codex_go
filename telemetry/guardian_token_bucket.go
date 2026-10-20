package telemetry

// GuardianRequestTokenBucket returns the bounded token_bucket label for an
// admitted Guardian request (Rust #55226). Upper bounds are inclusive, with
// 20k-token steps through 240k, a 256k boundary, and an overflow bucket.
func GuardianRequestTokenBucket(tokens int) string {
	boundaries := [...]int{20_000, 40_000, 60_000, 80_000, 100_000, 120_000, 140_000, 160_000, 180_000, 200_000, 220_000, 240_000, 256_000}
	labels := [...]string{
		"01_0-20k", "02_20k-40k", "03_40k-60k", "04_60k-80k", "05_80k-100k",
		"06_100k-120k", "07_120k-140k", "08_140k-160k", "09_160k-180k",
		"10_180k-200k", "11_200k-220k", "12_220k-240k", "13_240k-256k",
		"14_256k-max",
	}
	if tokens < 0 {
		tokens = 0
	}
	index := 0
	for index < len(boundaries) && tokens > boundaries[index] {
		index++
	}
	return labels[index]
}
