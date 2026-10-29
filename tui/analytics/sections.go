package analytics

import "time"

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
)

// AnalyticsSectionCount is the number of dashboard sections.
const AnalyticsSectionCount = 5

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
func AnalyticsVisibleSections(kind AccountKind, resolved bool) []AnalyticsSection {
	if !resolved {
		return nil
	}
	switch kind {
	case AccountKindConsumer:
		return []AnalyticsSection{
			AnalyticsSectionUsage,
			AnalyticsSectionActivity,
			AnalyticsSectionPlugins,
			AnalyticsSectionSkills,
		}
	case AccountKindBusiness, AccountKindEnterprise:
		return []AnalyticsSection{
			AnalyticsSectionCredits,
			AnalyticsSectionUsage,
			AnalyticsSectionPlugins,
			AnalyticsSectionSkills,
		}
	default:
		return nil
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
