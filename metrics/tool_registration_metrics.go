package metrics

// ToolRegistrationsMetric counts finalized tool registrations for one sampling
// request. It is a histogram so zero-registration exposure categories are
// exported alongside non-zero categories (Rust #51868
// `TOOL_REGISTRATIONS_METRIC`).
const ToolRegistrationsMetric = "codex.tools.registered"

// ToolRegistrationsMetricBoundaries are the explicit registration-count
// boundaries from Rust's `TOOL_REGISTRATIONS_METRIC_BUCKETS`.
var ToolRegistrationsMetricBoundaries = []float64{
	0, 1, 2, 5, 10, 20, 30, 50, 75, 100,
	150, 200, 300, 500, 750, 1_000, 2_000, 5_000, 10_000, 20_000,
}
