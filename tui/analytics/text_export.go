package analytics

// Exported text layout helpers. The dashboard view lives in the tea layer and
// must wrap and truncate the same way the package's own panels do
// (Rust analytics/render.rs RtOptions, #45764).

// AnalyticsDisplayWidth reports the terminal columns text occupies.
func AnalyticsDisplayWidth(text string) int {
	return analyticsDisplayWidth(text)
}

// AnalyticsTruncate shortens text with an ellipsis when it overflows width.
func AnalyticsTruncate(text string, width int) string {
	return analyticsTruncate(text, width)
}

// AnalyticsWrap wraps one row at word boundaries, hard-splitting long words.
func AnalyticsWrap(text string, width int) []string {
	return analyticsWrap(text, width)
}

// AnalyticsWrapLines wraps every row while keeping their order.
func AnalyticsWrapLines(lines []string, width int) []string {
	return analyticsWrapLines(lines, width)
}
