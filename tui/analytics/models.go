package analytics

import "time"

// AccountAnalyticsReport identifies one account analytics report. The display
// models stay independent of the app-server wire protocol (Rust #45740).
type AccountAnalyticsReport int

const (
	AccountAnalyticsReportUsage AccountAnalyticsReport = iota
	AccountAnalyticsReportCredits
	AccountAnalyticsReportMessages
	AccountAnalyticsReportPlugins
	AccountAnalyticsReportSkills
)

// AccountAnalyticsGrouping selects how one report's daily values are keyed.
type AccountAnalyticsGrouping int

const (
	AccountAnalyticsGroupingFeature AccountAnalyticsGrouping = iota
	AccountAnalyticsGroupingModel
	AccountAnalyticsGroupingSurface
	AccountAnalyticsGroupingTaskStart
	AccountAnalyticsGroupingSpeed
	AccountAnalyticsGroupingReasoning
	AccountAnalyticsGroupingTokenType
)

// AccountAnalyticsUnit is the unit shown next to a normalized history.
type AccountAnalyticsUnit int

const (
	AccountAnalyticsUnitRelativeUsage AccountAnalyticsUnit = iota
	AccountAnalyticsUnitCredits
	AccountAnalyticsUnitCount
)

// AccountAnalyticsHistory is one normalized daily series.
type AccountAnalyticsHistory struct {
	Unit AccountAnalyticsUnit
	// UpdatedAt is the source freshness in Unix seconds, when provided. It is
	// not a completeness watermark.
	UpdatedAt *int64
	Data      []AccountAnalyticsDay
}

// AccountAnalyticsDay is one reporting date with its full daily denominator.
type AccountAnalyticsDay struct {
	// Date is the source reporting date at UTC midnight; display formatting
	// belongs to the UI.
	Date time.Time
	// Total is the full daily denominator, including non-task usage when the
	// series is grouped by task start.
	Total  float64
	Values []AccountAnalyticsValue
}

// AccountAnalyticsValue is one labeled bucket inside a day.
type AccountAnalyticsValue struct {
	Key   string
	Label string
	Value float64
}
