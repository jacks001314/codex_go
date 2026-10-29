package analytics

import (
	"context"
	"errors"
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

// accountAnalyticsGroupLabels names each grouping position in the dashboard
// picker (Rust analytics/data.rs GROUP_LABELS, #45762).
var accountAnalyticsGroupLabels = []string{
	"Surface",
	"Feature",
	"Model",
	"Turn start",
	"Speed",
	"Reasoning",
	"Token type",
}

// AnalyticsGroupLabel returns the picker label for one grouping position.
func AnalyticsGroupLabel(index int) string {
	if index < 0 || index >= len(accountAnalyticsGroupLabels) {
		return ""
	}
	return accountAnalyticsGroupLabels[index]
}

// AnalyticsLoadState is one report-loading state. Replacing a pending load
// aborts it; its response cannot populate a newer range or account (Rust
// analytics/data.rs Load, #45762).
type AnalyticsLoadState int

const (
	AnalyticsLoadStateLoading AnalyticsLoadState = iota
	AnalyticsLoadStateReady
	AnalyticsLoadStateUnavailable
	AnalyticsLoadStateError
)

const (
	analyticsLoadTimeout        = 30 * time.Second
	analyticsLoadingMessage     = "Loading…"
	analyticsUnavailableMessage = "No history has been reported."
	analyticsInterruptedMessage = "Request interrupted. Press R to retry."
	analyticsTimedOutMessage    = "Request timed out. Press R to retry."
)

type analyticsLoadResult[T any] struct {
	value *T
	err   error
}

// PendingAnalyticsLoad is an in-flight report request.
type PendingAnalyticsLoad[T any] struct {
	result <-chan analyticsLoadResult[T]
	cancel context.CancelFunc
}

// AnalyticsLoad holds a report's loading state. Cancel mirrors Rust's Drop: a
// replaced load aborts its request.
type AnalyticsLoad[T any] struct {
	state   AnalyticsLoadState
	pending *PendingAnalyticsLoad[T]
	value   T
	message string
}

// StartAnalyticsLoad runs load on its own goroutine with a 30 second timeout and
// schedules a frame once the result is available (Rust Load::start).
func StartAnalyticsLoad[T any](ctx context.Context, load func(context.Context) (*T, error), scheduleFrame func()) *AnalyticsLoad[T] {
	if ctx == nil {
		ctx = context.Background()
	}
	taskCtx, cancel := context.WithCancel(ctx)
	results := make(chan analyticsLoadResult[T], 1)
	handle := &AnalyticsLoad[T]{
		state:   AnalyticsLoadStateLoading,
		pending: &PendingAnalyticsLoad[T]{result: results, cancel: cancel},
	}
	go func() {
		result := runAnalyticsLoad(taskCtx, load)
		cancel()
		results <- result
		if scheduleFrame != nil {
			scheduleFrame()
		}
	}()
	return handle
}

func runAnalyticsLoad[T any](ctx context.Context, load func(context.Context) (*T, error)) (result analyticsLoadResult[T]) {
	timeoutCtx, cancel := context.WithTimeout(ctx, analyticsLoadTimeout)
	defer cancel()
	defer func() {
		if recover() != nil {
			result = analyticsLoadResult[T]{err: errors.New(analyticsInterruptedMessage)}
		}
	}()
	value, err := load(timeoutCtx)
	switch {
	case err != nil && errors.Is(ctx.Err(), context.Canceled):
		return analyticsLoadResult[T]{err: errors.New(analyticsInterruptedMessage)}
	case err != nil && errors.Is(timeoutCtx.Err(), context.DeadlineExceeded):
		return analyticsLoadResult[T]{err: errors.New(analyticsTimedOutMessage)}
	default:
		return analyticsLoadResult[T]{value: value, err: err}
	}
}

// Poll advances a pending load without blocking (Rust Load::poll).
func (l *AnalyticsLoad[T]) Poll() {
	if l == nil || l.state != AnalyticsLoadStateLoading || l.pending == nil {
		return
	}
	select {
	case result := <-l.pending.result:
		l.pending.cancel()
		l.pending = nil
		switch {
		case result.err != nil:
			l.state = AnalyticsLoadStateError
			l.message = result.err.Error()
		case result.value == nil:
			l.state = AnalyticsLoadStateUnavailable
			l.message = analyticsUnavailableMessage
		default:
			l.state = AnalyticsLoadStateReady
			l.value = *result.value
		}
	default:
	}
}

// Cancel aborts a pending load. Poll returns nothing after cancellation.
func (l *AnalyticsLoad[T]) Cancel() {
	if l == nil || l.pending == nil {
		return
	}
	l.pending.cancel()
	l.pending = nil
}

// State reports the current loading state.
func (l *AnalyticsLoad[T]) State() AnalyticsLoadState {
	if l == nil {
		return AnalyticsLoadStateLoading
	}
	return l.state
}

// Ready returns the loaded value when the load is ready.
func (l *AnalyticsLoad[T]) Ready() (T, bool) {
	if l == nil || l.state != AnalyticsLoadStateReady {
		var zero T
		return zero, false
	}
	return l.value, true
}

// UnavailableAnalyticsLoad returns an untouched load (Rust Load::Unavailable).
func UnavailableAnalyticsLoad[T any]() AnalyticsLoad[T] {
	return AnalyticsLoad[T]{state: AnalyticsLoadStateUnavailable}
}

// Invalidate aborts any pending request and returns the load to Unavailable,
// which is what closing or refreshing the dashboard does (Rust
// AnalyticsView::cancel_loads).
func (l *AnalyticsLoad[T]) Invalidate() {
	if l == nil {
		return
	}
	if l.pending != nil {
		l.pending.cancel()
		l.pending = nil
	}
	var zero T
	l.state = AnalyticsLoadStateUnavailable
	l.value = zero
	l.message = ""
}

// Message returns the placeholder or error text, if any (Rust Load::message).
func (l *AnalyticsLoad[T]) Message() (string, bool) {
	switch {
	case l == nil:
		return "", false
	case l.state == AnalyticsLoadStateReady:
		return "", false
	case l.state == AnalyticsLoadStateError:
		return l.message, true
	case l.state == AnalyticsLoadStateUnavailable:
		return analyticsUnavailableMessage, true
	default:
		return analyticsLoadingMessage, true
	}
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
