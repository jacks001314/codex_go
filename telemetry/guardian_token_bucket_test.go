package telemetry

import "testing"

func TestGuardianRequestTokenBucketLikeRust(t *testing.T) {
	cases := []struct {
		tokens int
		want   string
	}{
		{0, "01_0-20k"},
		{20_000, "01_0-20k"},
		{20_001, "02_20k-40k"},
		{240_000, "12_220k-240k"},
		{240_001, "13_240k-256k"},
		{256_000, "13_240k-256k"},
		{256_001, "14_256k-max"},
	}
	for _, testCase := range cases {
		if got := GuardianRequestTokenBucket(testCase.tokens); got != testCase.want {
			t.Fatalf("GuardianRequestTokenBucket(%d) = %q, want %q", testCase.tokens, got, testCase.want)
		}
	}
}
