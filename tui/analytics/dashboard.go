package analytics

import (
	"strconv"
	"strings"
)

// AnalyticsGroupOptions lists the groupings offered for a section. Usage
// offers attributed consumer groupings only when attribution is complete;
// credits follow the plan's supported breakdowns
// (Rust AnalyticsView::group_options, #45764).
func AnalyticsGroupOptions(section AnalyticsSection, business, consumerAttribution bool, creditGroups []int) []int {
	switch {
	case section == AnalyticsSectionUsage && business:
		return []int{6, 2}
	case section == AnalyticsSectionUsage && consumerAttribution:
		return []int{1, 2, 0, 3}
	case section == AnalyticsSectionUsage:
		return []int{0, 2}
	case section == AnalyticsSectionCredits:
		if len(creditGroups) == 0 {
			return []int{0}
		}
		return creditGroups
	case section == AnalyticsSectionActivity:
		return []int{2, 0}
	default:
		return nil
	}
}

// AnalyticsConsumerAttribution reports whether a non-business account has
// complete usage attribution (Rust AnalyticsView::consumer_attribution).
func AnalyticsConsumerAttribution(business bool, attributedUsage *bool) bool {
	if business {
		return false
	}
	return attributedUsage == nil || *attributedUsage
}

// AnalyticsSectionTitle is the card and panel title for a section.
func AnalyticsSectionTitle(section AnalyticsSection, business, consumerAttribution bool) string {
	switch section {
	case AnalyticsSectionUsage:
		switch {
		case business:
			return "Token usage history"
		case consumerAttribution:
			return "Total usage history"
		default:
			return "Usage history"
		}
	case AnalyticsSectionPlugins:
		return "Plugins called"
	case AnalyticsSectionCredits:
		return "Credits usage history"
	case AnalyticsSectionActivity:
		return "Messages"
	default:
		return "Skills used"
	}
}

// AnalyticsDashboardOptions describes the overview grid. History renders one
// card's body at the given inner size.
type AnalyticsDashboardOptions struct {
	Width               int
	Height              int
	Visible             []AnalyticsSection
	Section             AnalyticsSection
	Ranges              [3]int
	Groups              [AnalyticsSectionCount]int
	GroupOptions        []int
	GroupPicker         *int
	Business            bool
	ConsumerAttribution bool
	TokenModel          *string
	History             func(section AnalyticsSection, width, height int) []string
}

// AnalyticsDashboardLines renders the bordered overview cards and returns the
// row range of the focused card. Card heights depend on the viewport only.
func AnalyticsDashboardLines(opts AnalyticsDashboardOptions) ([]string, int, int) {
	if opts.Width <= 0 {
		return nil, 0, 0
	}
	count := 1
	if opts.Width >= 104 {
		count = 2
	}
	cardWidth := opts.Width
	if count == 2 {
		cardWidth = (opts.Width - 4) / 2
	}
	visible := opts.Visible
	rows := (len(visible) + count - 1) / count
	if rows < 1 {
		rows = 1
	}
	cardHeight := (opts.Height - 2) / rows
	if cardHeight < 16 {
		cardHeight = 16
	}
	if cardHeight > 24 {
		cardHeight = 24
	}
	innerWidth := cardWidth - 4
	if innerWidth < 1 {
		innerWidth = 1
	}
	innerHeight := cardHeight - 4
	if innerHeight < 1 {
		innerHeight = 1
	}
	var lines []string
	selectionStart, selectionEnd := 0, 0
	for row := 0; row*count < len(visible); row++ {
		end := (row + 1) * count
		if end > len(visible) {
			end = len(visible)
		}
		chunk := visible[row*count : end]
		cards := make([][]string, 0, len(chunk))
		for column, section := range chunk {
			card := analyticsDashboardCard(opts, section, row*count+column+1, cardWidth, innerWidth, innerHeight)
			cards = append(cards, card)
		}
		for _, section := range chunk {
			if section == opts.Section {
				selectionStart = len(lines)
				selectionEnd = len(lines) + cardHeight
			}
		}
		if len(cards) == 2 {
			lines = append(lines, analyticsJoinColumns(cards[0], cards[1], cardWidth)...)
		} else if len(cards) == 1 {
			lines = append(lines, cards[0]...)
		}
		if (row+1)*count < len(visible) {
			lines = append(lines, "")
		}
	}
	return lines, selectionStart, selectionEnd
}

func analyticsDashboardCard(
	opts AnalyticsDashboardOptions,
	section AnalyticsSection,
	position int,
	cardWidth int,
	innerWidth int,
	innerHeight int,
) []string {
	focused := section == opts.Section
	content := []string{""}
	rangeDays := 7
	if opts.Ranges[section.RangeGroup(opts.Business)] != 0 {
		rangeDays = 30
	}
	header := strconv.Itoa(rangeDays) + "d"
	if section == AnalyticsSectionUsage && opts.Business {
		model := "All models"
		if opts.TokenModel != nil && *opts.TokenModel != "" {
			model = *opts.TokenModel
		}
		header += " · " + model
	}
	content[0] = header
	if section == AnalyticsSectionUsage || section == AnalyticsSectionCredits || section == AnalyticsSectionActivity {
		content = append(content, "By "+strings.ToLower(section.GroupLabel(opts.Groups[section], opts.ConsumerAttribution)))
	}
	if focused && opts.GroupPicker != nil {
		for index, group := range opts.GroupOptions {
			marker := " "
			if index == *opts.GroupPicker {
				marker = "›"
			}
			content = append(content, marker+" "+section.GroupLabel(group, opts.ConsumerAttribution))
		}
	} else if opts.History != nil {
		historyHeight := innerHeight - 13
		if historyHeight < 1 {
			historyHeight = 1
		}
		content = append(content, opts.History(section, innerWidth, historyHeight)...)
	}
	content = analyticsWrapLines(content, innerWidth)
	if len(content) > innerHeight {
		content = content[:innerHeight]
		content[innerHeight-1] = "… z to maximize"
	}
	for len(content) < innerHeight {
		content = append(content, "")
	}
	label := " " + strconv.Itoa(position) + " " + AnalyticsSectionTitle(section, opts.Business, opts.ConsumerAttribution) + " "
	title := "  "
	if focused {
		title = " ▸"
	}
	title += label
	title = analyticsTruncate(title, cardWidth-3)
	top := "╭─" + title
	remaining := cardWidth - analyticsDisplayWidth(title) - 3
	if remaining < 0 {
		remaining = 0
	}
	top += strings.Repeat("─", remaining) + "╮"
	lines := []string{top, analyticsColumns("│", "│", cardWidth)}
	for _, line := range content {
		line = analyticsTruncate(line, innerWidth)
		padding := cardWidth - analyticsDisplayWidth(line) - 3
		if padding < 0 {
			padding = 0
		}
		lines = append(lines, "│ "+line+strings.Repeat(" ", padding)+"│")
	}
	lines = append(lines, analyticsColumns("│", "│", cardWidth))
	lines = append(lines, "╰"+strings.Repeat("─", cardWidth-2)+"╯")
	return lines
}
