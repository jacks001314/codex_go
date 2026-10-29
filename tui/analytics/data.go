package analytics

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"codex_go/auth"
)

// accountAnalyticsGroupings is the dashboard's grouping order (Rust
// analytics/data.rs GROUPINGS, #45742).
var accountAnalyticsGroupings = []AccountAnalyticsGrouping{
	AccountAnalyticsGroupingSurface,
	AccountAnalyticsGroupingFeature,
	AccountAnalyticsGroupingModel,
	AccountAnalyticsGroupingTaskStart,
	AccountAnalyticsGroupingSpeed,
	AccountAnalyticsGroupingReasoning,
	AccountAnalyticsGroupingTokenType,
}

// CreditGroupIndices maps the plan's credit groupings onto positions in
// accountAnalyticsGroupings, skipping any grouping the dashboard does not
// render (Rust analytics/client.rs, #45742).
func CreditGroupIndices(plan auth.PlanType) []int {
	groupings := CreditGroupings(plan)
	indices := make([]int, 0, len(groupings))
	for _, grouping := range groupings {
		for index, candidate := range accountAnalyticsGroupings {
			if candidate == grouping {
				indices = append(indices, index)
				break
			}
		}
	}
	return indices
}

// analyticsAmount keeps tiny refunds visible while avoiding noise on ordinary
// credit amounts (Rust analytics/data.rs amount).
func analyticsAmount(value float64) string {
	if value == 0 {
		return "0"
	}
	if math.Abs(value) < 0.000001 {
		return rustExponent(fmt.Sprintf("%.2e", value))
	}
	var rounded string
	if math.Abs(value) < 0.01 {
		rounded = fmt.Sprintf("%.6f", value)
	} else {
		rounded = fmt.Sprintf("%.2f", value)
	}
	whole, fraction, _ := strings.Cut(rounded, ".")
	grouped := groupThousands(whole)
	fraction = strings.TrimRight(fraction, "0")
	if fraction != "" {
		grouped += "." + fraction
	}
	return grouped
}

// analyticsCreditAmount aligns ordinary credit amounts to two decimals without
// hiding tiny adjustments (Rust analytics/data.rs credit_amount).
func analyticsCreditAmount(value float64) string {
	formatted := analyticsAmount(value)
	if value != 0 && math.Abs(value) < 0.01 {
		return formatted
	}
	_, fraction, ok := strings.Cut(formatted, ".")
	if !ok {
		return formatted + ".00"
	}
	if len(fraction) == 1 {
		return formatted + "0"
	}
	return formatted
}

// analyticsCredits formats integer millionths without floating-point rounding or
// hiding tiny adjustments (Rust analytics/data.rs credits).
func analyticsCredits(micros int64) string {
	magnitude := uint64(micros)
	if micros < 0 {
		magnitude = uint64(-(micros + 1)) + 1
	}
	if magnitude > 0 && magnitude < 10000 {
		fractional := fmt.Sprintf("%06d", magnitude)
		sign := ""
		if micros < 0 {
			sign = "-"
		}
		return sign + "0." + strings.TrimRight(fractional, "0")
	}
	cents := (magnitude + 5000) / 10000
	whole := groupThousands(strconv.FormatUint(cents/100, 10))
	sign := ""
	if micros < 0 {
		sign = "-"
	}
	return fmt.Sprintf("%s%s.%02d", sign, whole, cents%100)
}

// analyticsShortDate renders an analytics date as "Jan 9", leaving unparseable
// input untouched (Rust analytics/data.rs date).
func analyticsShortDate(date string) string {
	parsed, err := time.Parse("2006-01-02", date)
	if err != nil {
		return date
	}
	return parsed.Format("Jan 2")
}

// groupThousands inserts commas every three digits of the integer part, leaving
// the sign untouched (Rust's amount/credits grouping loops).
func groupThousands(whole string) string {
	if len(whole) <= 3 {
		return whole
	}
	var builder strings.Builder
	for index, char := range whole {
		if index > 0 && char >= '0' && char <= '9' && (len(whole)-index)%3 == 0 && whole[index-1] != '-' {
			builder.WriteByte(',')
		}
		builder.WriteRune(char)
	}
	return builder.String()
}

// rustExponent rewrites Go's zero-padded scientific exponent ("-4.00e-06") into
// Rust's form ("-4.00e-6").
func rustExponent(value string) string {
	exponentAt := strings.IndexAny(value, "eE")
	if exponentAt < 0 {
		return value
	}
	mantissa := value[:exponentAt]
	exponent := value[exponentAt+1:]
	sign := ""
	if strings.HasPrefix(exponent, "+") || strings.HasPrefix(exponent, "-") {
		sign = exponent[:1]
		exponent = exponent[1:]
	}
	exponent = strings.TrimLeft(exponent, "0")
	if exponent == "" {
		exponent = "0"
	}
	return mantissa + "e" + sign + exponent
}
