package status

import "testing"

// TestFormatCreditMicrosMatchesRust covers the Rust
// thread_usage_credit_formatting_rounds_to_one_decimal_place vectors (#44970).
func TestFormatCreditMicrosMatchesRust(t *testing.T) {
	cases := []struct {
		micros int64
		want   string
	}{
		{0, "0"},
		{400_000, "0.4"},
		{5_200_000, "5.2"},
		{5_240_000, "5.2"},
		{5_250_000, "5.3"},
		{46_000_000, "46"},
		{1_240_000_000, "1.2K"},
		{12_400_000_000, "12.4K"},
	}
	for _, tc := range cases {
		if got := FormatCreditMicros(tc.micros); got != tc.want {
			t.Errorf("FormatCreditMicros(%d) = %q, want %q", tc.micros, got, tc.want)
		}
	}
	if got := FormatCreditMicros(-1); got != "0" {
		t.Errorf("FormatCreditMicros(-1) = %q, want 0", got)
	}
}

// TestFormatEstimatedUSDMicrosMatchesRust covers the Rust
// format_estimated_usd_micros branches (#44970).
func TestFormatEstimatedUSDMicrosMatchesRust(t *testing.T) {
	cases := []struct {
		micros int64
		want   string
		ok     bool
	}{
		{-1, "", false},
		{0, "~$0.00", true},
		{5, "~$0.000005", true},
		{100, "~$0.0001", true},
		{9_999, "~$0.0100", true},
		{11_000, "~$0.01", true},
		{140_000, "~$0.14", true},
		{200_000, "~$0.20", true},
		{1_000_000, "~$1.00", true},
	}
	for _, tc := range cases {
		got, ok := FormatEstimatedUSDMicros(tc.micros)
		if got != tc.want || ok != tc.ok {
			t.Errorf("FormatEstimatedUSDMicros(%d) = (%q, %v), want (%q, %v)", tc.micros, got, ok, tc.want, tc.ok)
		}
	}
}
