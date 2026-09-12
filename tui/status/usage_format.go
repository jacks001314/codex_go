package status

import (
	"math/big"
	"strconv"
	"strings"
)

// Rust parity: codex-rs/tui/src/status/thread_usage.rs (#44970).

// FormatCreditMicros renders a credit amount in micros with a compact suffix
// and at most one decimal place, rounding half up (Rust format_credit_micros).
func FormatCreditMicros(micros int64) string {
	if micros < 0 {
		micros = 0
	}
	value := big.NewInt(micros)
	unit := big.NewInt(1_000_000)
	suffix := ""
	for _, candidate := range []struct {
		unit   int64
		suffix string
	}{
		{1_000_000_000_000_000_000, "T"},
		{1_000_000_000_000_000, "B"},
		{1_000_000_000_000, "M"},
		{1_000_000_000, "K"},
	} {
		candidateUnit := big.NewInt(candidate.unit)
		if value.Cmp(candidateUnit) >= 0 {
			unit = candidateUnit
			suffix = candidate.suffix
			break
		}
	}
	// tenths = (micros * 10 + unit / 2) / unit
	tenths := new(big.Int).Mul(value, big.NewInt(10))
	tenths.Add(tenths, new(big.Int).Div(unit, big.NewInt(2)))
	tenths.Div(tenths, unit)
	whole := new(big.Int).Div(tenths, big.NewInt(10))
	fraction := new(big.Int).Mod(tenths, big.NewInt(10))
	if fraction.Sign() == 0 {
		return whole.String() + suffix
	}
	return whole.String() + "." + fraction.String() + suffix
}

// FormatEstimatedUSDMicros renders an estimated cost in micros, or false when
// the amount is negative (Rust format_estimated_usd_micros -> Option).
func FormatEstimatedUSDMicros(micros int64) (string, bool) {
	if micros < 0 {
		return "", false
	}
	if micros > 0 && micros < 100 {
		return "~$0." + zeroPad(strconv.FormatInt(micros, 10), 6), true
	}
	if micros > 0 && micros < 10_000 {
		tenThousandths := (micros + 50) / 100
		return "~$0." + zeroPad(strconv.FormatInt(tenThousandths, 10), 4), true
	}
	cents := (micros + 5_000) / 10_000
	return "~$" + strconv.FormatInt(cents/100, 10) + "." + zeroPad(strconv.FormatInt(cents%100, 10), 2), true
}

func zeroPad(value string, width int) string {
	if len(value) >= width {
		return value
	}
	return strings.Repeat("0", width-len(value)) + value
}
