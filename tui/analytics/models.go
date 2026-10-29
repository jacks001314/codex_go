package analytics

import (
	"time"

	"codex_go/auth"
)

// AccountKind matches the App's consumer, business, and workspace scopes
// (Rust AccountKind, #45742).
type AccountKind int

const (
	AccountKindConsumer AccountKind = iota
	AccountKindBusiness
	AccountKindEnterprise
	AccountKindUnknown
)

// AccountKindFor classifies a ChatGPT plan. Team-like plans are the business
// family; every other workspace account is enterprise-like
// (Rust AccountKind::from).
func AccountKindFor(plan auth.PlanType) AccountKind {
	switch {
	case plan == "" || plan == auth.PlanUnknown:
		return AccountKindUnknown
	case plan.IsTeamLike():
		return AccountKindBusiness
	case plan.IsWorkspaceAccount():
		return AccountKindEnterprise
	default:
		return AccountKindConsumer
	}
}

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

// CreditGroupings lists the credit breakdowns supported by the account's
// billing report (Rust AccountAnalyticsGrouping::credit_groupings, #45742).
func CreditGroupings(plan auth.PlanType) []AccountAnalyticsGrouping {
	switch AccountKindFor(plan) {
	case AccountKindBusiness:
		return []AccountAnalyticsGrouping{
			AccountAnalyticsGroupingSurface,
			AccountAnalyticsGroupingModel,
			AccountAnalyticsGroupingSpeed,
		}
	case AccountKindEnterprise:
		return []AccountAnalyticsGrouping{
			AccountAnalyticsGroupingSurface,
			AccountAnalyticsGroupingModel,
			AccountAnalyticsGroupingSpeed,
			AccountAnalyticsGroupingReasoning,
		}
	case AccountKindConsumer:
		return []AccountAnalyticsGrouping{AccountAnalyticsGroupingSurface}
	default:
		return nil
	}
}

// AccountAnalyticsUnit is the unit shown next to a normalized history.
type AccountAnalyticsUnit int

const (
	AccountAnalyticsUnitRelativeUsage AccountAnalyticsUnit = iota
	AccountAnalyticsUnitCredits
	AccountAnalyticsUnitCount
	AccountAnalyticsUnitTokens
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
