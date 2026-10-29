package analytics

import (
	"time"

	"codex_go/auth"
)

// AnalyticsSection identifies one dashboard card. The values are stable so a
// selection survives the account-driven layout order changes
// (Rust analytics::sections::Section, #45764).
type AnalyticsSection int

const (
	AnalyticsSectionUsage AnalyticsSection = iota
	AnalyticsSectionPlugins
	AnalyticsSectionCredits
	AnalyticsSectionActivity
	AnalyticsSectionSkills
	// AnalyticsSectionChats appends the Top chats card without renumbering the
	// existing sections, so a retained selection keeps its identity
	// (Rust Section::Chats, #45765).
	AnalyticsSectionChats
	// AnalyticsSectionSummary appends the account Summary tab without
	// renumbering the existing sections (Rust Section::Summary, #45769).
	AnalyticsSectionSummary
)

// AnalyticsSectionCount is the number of dashboard sections.
const AnalyticsSectionCount = 7

// Report maps a section onto the report it renders. Every section has one.
func (s AnalyticsSection) Report() AccountAnalyticsReport {
	switch s {
	case AnalyticsSectionPlugins:
		return AccountAnalyticsReportPlugins
	case AnalyticsSectionCredits:
		return AccountAnalyticsReportCredits
	case AnalyticsSectionActivity:
		return AccountAnalyticsReportMessages
	case AnalyticsSectionSkills:
		return AccountAnalyticsReportSkills
	default:
		return AccountAnalyticsReportUsage
	}
}

// HasReport reports whether the section loads an account analytics report.
// Summary loads the account profile, and Top chats loads local roots and
// per-thread estimates instead (Rust Section::report returning None).
func (s AnalyticsSection) HasReport() bool {
	return s != AnalyticsSectionChats && s != AnalyticsSectionSummary
}

// AnalyticsSectionState is one section's report and its selection. Each section
// loads and selects independently of the others.
type AnalyticsSectionState struct {
	History AnalyticsLoad[AccountAnalyticsHistory]
	Cursor  int
	Detail  *int
	Group   int
}

// DefaultAnalyticsSectionState starts on the newest day of a 7-day range with
// no expanded detail and the first grouping (Rust SectionState::default).
func DefaultAnalyticsSectionState() AnalyticsSectionState {
	return AnalyticsSectionState{
		History: UnavailableAnalyticsLoad[AccountAnalyticsHistory](),
		Cursor:  6,
		Group:   0,
	}
}

// AnalyticsRangeGroup is the range shared by related sections. Usage, product
// activity, and tools each keep one range across the sections they cover.
type AnalyticsRangeGroup int

const (
	AnalyticsRangeGroupUsage AnalyticsRangeGroup = iota
	AnalyticsRangeGroupActivity
	AnalyticsRangeGroupTools
)

// RangeGroup reports the range group a section belongs to. Business usage is
// token activity, so it shares the activity range
// (Rust AnalyticsView::range_group).
func (s AnalyticsSection) RangeGroup(business bool) AnalyticsRangeGroup {
	switch s {
	case AnalyticsSectionUsage:
		if business {
			return AnalyticsRangeGroupActivity
		}
		return AnalyticsRangeGroupUsage
	case AnalyticsSectionCredits:
		return AnalyticsRangeGroupUsage
	case AnalyticsSectionChats:
		return AnalyticsRangeGroupUsage
	case AnalyticsSectionSummary:
		return AnalyticsRangeGroupUsage
	case AnalyticsSectionActivity:
		return AnalyticsRangeGroupActivity
	default:
		return AnalyticsRangeGroupTools
	}
}

// GroupLabel names one grouping for a section. Credit reports and
// non-attributed usage label the first grouping "Product"
// (Rust AnalyticsView::group_label).
func (s AnalyticsSection) GroupLabel(group int, consumerAttribution bool) string {
	if s == AnalyticsSectionSummary {
		return AnalyticsSummaryViewLabel(group)
	}
	if group == 0 && (s == AnalyticsSectionCredits || (s == AnalyticsSectionUsage && !consumerAttribution)) {
		return "Product"
	}
	if group < 0 || group >= len(accountAnalyticsGroupLabels) {
		return ""
	}
	return accountAnalyticsGroupLabels[group]
}

// AnalyticsVisibleSections is the account-specific card order. Unknown and
// unresolved accounts show nothing (Rust AnalyticsView::visible_sections).
// The consumer and thread-estimate plans both expose Top chats: consumers see
// current allowance and balance debits, businesses see lifetime estimates.
func AnalyticsVisibleSections(plan auth.PlanType, resolved bool) []AnalyticsSection {
	if !resolved {
		return nil
	}
	switch AccountKindFor(plan) {
	case AccountKindConsumer:
		return []AnalyticsSection{
			AnalyticsSectionSummary,
			AnalyticsSectionUsage,
			AnalyticsSectionActivity,
			AnalyticsSectionPlugins,
			AnalyticsSectionSkills,
			AnalyticsSectionChats,
		}
	case AccountKindBusiness, AccountKindEnterprise:
		if AnalyticsThreadUsageSupported(plan) {
			return []AnalyticsSection{
				AnalyticsSectionSummary,
				AnalyticsSectionCredits,
				AnalyticsSectionUsage,
				AnalyticsSectionPlugins,
				AnalyticsSectionSkills,
				AnalyticsSectionChats,
			}
		}
		return []AnalyticsSection{
			AnalyticsSectionSummary,
			AnalyticsSectionCredits,
			AnalyticsSectionUsage,
			AnalyticsSectionPlugins,
			AnalyticsSectionSkills,
		}
	default:
		// An unresolved plan is still an account: the profile summary needs no
		// plan-specific report.
		return []AnalyticsSection{AnalyticsSectionSummary}
	}
}

// AnalyticsSectionDays is the report range length in days.
func AnalyticsSectionDays(rangeIndex int) int {
	if rangeIndex == 0 {
		return 7
	}
	return 30
}

// AnalyticsSectionStartDate is the first day the section's range covers.
func AnalyticsSectionStartDate(end time.Time, rangeIndex int) time.Time {
	return end.AddDate(0, 0, -(AnalyticsSectionDays(rangeIndex) - 1))
}

// AnalyticsShiftSectionSelection moves a section's selection when its range
// switches between 7 and 30 days, keeping the same calendar day when it is
// still in range (Rust AnalyticsView::change_range).
func AnalyticsShiftSectionSelection(cursor int, detail *int, toThirty bool) (int, *int) {
	if toThirty {
		shifted := cursor + 23
		if detail != nil {
			value := *detail + 23
			return shifted, &value
		}
		return shifted, nil
	}
	shifted := cursor - 23
	if shifted < 0 {
		shifted = 0
	}
	if detail == nil {
		return shifted, nil
	}
	if *detail < 23 {
		return shifted, nil
	}
	value := *detail - 23
	return shifted, &value
}
