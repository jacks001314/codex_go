package analytics

import (
	"fmt"
	"sort"
	"strings"

	"codex_go/chatgptapi"
)

// Bounded chat tables with inline credit breakdowns and a viewport-sized row
// window (Rust analytics/chat_panel.rs, #45765). Zero-credit groups are
// optional; missing dimensions and signed adjustments stay distinct.

const (
	analyticsChatTableHeader      = "Active in past 30 days · sorted by lifetime credits"
	analyticsChatCoverage         = "Local chats · excludes archived chats and subagent usage"
	analyticsChatOverviewCoverage = "Local chats · excludes subagents"
)

// AnalyticsChatPanelOptions describes the maximized Top chats table.
type AnalyticsChatPanelOptions struct {
	Chats *AnalyticsChats
	// Message is the load placeholder or error while Ready is false.
	Message string
	Ready   bool
	Cursor  int
	Detail  *int
	// ShowZeroCreditGroups reveals breakdown groups that estimated zero credits.
	ShowZeroCreditGroups bool
	Width                int
	Zoomed               bool
	ViewportHeight       int
	// ModelName maps a model key onto its display name; nil keeps the key.
	ModelName func(string) string
}

// AnalyticsChatPanel is the table content plus the selection range of the
// cursor row.
type AnalyticsChatPanel struct {
	Lines          []string
	SelectionStart int
	SelectionEnd   int
}

// AnalyticsChatPanelLines renders the maximized Top chats table
// (Rust AnalyticsView::chat_lines).
func AnalyticsChatPanelLines(opts AnalyticsChatPanelOptions) AnalyticsChatPanel {
	width := opts.Width
	if width < 1 {
		width = 1
	}
	if width > 96 {
		width = 96
	}
	wrap := func(lines []string) []string { return analyticsWrapLines(lines, width) }
	lines := wrap([]string{analyticsChatTableHeader})
	if !opts.Ready {
		if opts.Message != "" {
			lines = append(lines, wrap([]string{opts.Message})...)
		}
		return AnalyticsChatPanel{Lines: lines, SelectionEnd: 1}
	}
	if opts.Chats == nil || len(opts.Chats.Rows) == 0 {
		lines = append(lines, wrap([]string{"No recent local chats."})...)
		return AnalyticsChatPanel{Lines: lines, SelectionEnd: 1}
	}
	chats := opts.Chats.Rows
	missing := 0
	for _, chat := range chats {
		if chat.Usage == nil {
			missing++
		}
	}
	if missing > 0 {
		plural := "s"
		if missing == 1 {
			plural = ""
		}
		lines = append(lines, wrap([]string{fmt.Sprintf("%d chat estimate%s unavailable", missing, plural)})...)
	}
	lines = append(lines, "")
	showUSD := false
	for _, chat := range chats {
		if chat.Usage != nil && chat.Usage.EstimatedUsageUSDMicros != nil {
			showUSD = true
			break
		}
	}
	stacked := showUSD && width < 70
	amountHeader := "Lifetime credits"
	switch {
	case stacked:
		amountHeader = "Est. $ / credits"
	case showUSD:
		amountHeader = "Est. $ spent  Lifetime credits"
	}
	lines = append(lines, analyticsColumns("  Chat", amountHeader, width))

	rows := make([][]string, 0, len(chats))
	for index, chat := range chats {
		selected := index == opts.Cursor
		amount := "—"
		if chat.Usage != nil {
			amount = analyticsCredits(chat.Usage.EstimatedUsageCreditsMicros)
		}
		if showUSD {
			usd := "—"
			if chat.Usage != nil && chat.Usage.EstimatedUsageUSDMicros != nil {
				usd = fmt.Sprintf("$%.2f", float64(*chat.Usage.EstimatedUsageUSDMicros)/1_000_000.0)
			}
			amount = fmt.Sprintf("%12s  %16s", usd, amount)
		}
		label := "  " + chat.DisplayTitle()
		if selected {
			label = "› " + chat.DisplayTitle()
		}
		labelWidth := width - (analyticsDisplayWidth(amount) + 1)
		if stacked {
			labelWidth = width
		}
		if labelWidth < 0 {
			labelWidth = 0
		}
		truncated := analyticsDisplayWidth(label) > labelWidth
		var row []string
		if stacked {
			row = []string{analyticsTruncate(label, labelWidth), "  " + strings.TrimSpace(amount)}
		} else {
			row = []string{analyticsColumns(analyticsTruncate(label, labelWidth), amount, width)}
		}
		if opts.Detail != nil && *opts.Detail == index && chat.Usage != nil {
			row = append(row, chatDetailLines(chat, opts, width, truncated)...)
		}
		rows = append(rows, wrap(row))
	}
	coverage := wrap([]string{analyticsChatCoverage})
	return analyticsChatRowWindow(lines, rows, coverage, width, opts)
}

func chatDetailLines(chat AnalyticsChat, opts AnalyticsChatPanelOptions, width int, truncated bool) []string {
	usage := chat.Usage
	divider := width - 2
	if divider < 0 {
		divider = 0
	}
	row := []string{"  " + strings.Repeat("─", divider)}
	if truncated {
		row = append(row, analyticsWrap("    "+chat.DisplayTitle(), width)...)
	}
	zeros := 0
	groups := make([]chatgptapi.ThreadUsageBreakdownGroup, 0, len(usage.Groups))
	for _, group := range usage.Groups {
		if group.EstimatedUsageCreditsMicros == 0 {
			zeros++
		}
		if opts.ShowZeroCreditGroups || group.EstimatedUsageCreditsMicros != 0 {
			groups = append(groups, group)
		}
	}
	sort.SliceStable(groups, func(left, right int) bool {
		return groups[left].EstimatedUsageCreditsMicros > groups[right].EstimatedUsageCreditsMicros
	})
	// On narrow terminals, retain every dimension on a second line.
	table := width >= 70
	modelWidth := width - 46
	if modelWidth < 1 {
		modelWidth = 1
	}
	if len(groups) > 0 {
		header := "  Model / effort / speed"
		if table {
			header = fmt.Sprintf("  %-*s  %-12s  %-12s", modelWidth, "Model", "Effort", "Speed")
		}
		row = append(row, analyticsColumns(header, "Credits", width))
	}
	for _, group := range groups {
		model := chatGroupText(group.Model, opts.ModelName, "Not reported")
		effort := chatGroupText(group.ReasoningEffort, nil, "Not reported")
		speed := chatGroupText(group.Speed, nil, "Not reported")
		amount := analyticsCredits(group.EstimatedUsageCreditsMicros)
		if table {
			// Wrap long model names instead of hiding distinctions between
			// groups.
			models := analyticsWrap(model, modelWidth)
			for _, wrapped := range models[:len(models)-1] {
				row = append(row, "  "+wrapped)
			}
			last := models[len(models)-1]
			label := fmt.Sprintf("  %-*s  %-12s  %-12s", modelWidth, last, effort, speed)
			row = append(row, analyticsColumns(label, amount, width))
		} else {
			label := "  " + model
			if analyticsDisplayWidth(label)+analyticsDisplayWidth(amount)+1 > width {
				row = append(row, label, "  "+amount)
			} else {
				row = append(row, analyticsColumns(label, amount, width))
			}
			row = append(row, "    "+effort+" · "+speed)
		}
	}
	if len(usage.Groups) == 0 {
		row = append(row, "  Breakdown unavailable")
	}
	if zeros > 0 {
		plural := "groups"
		if zeros == 1 {
			plural = "group"
		}
		hidden := " hidden"
		action := "show all"
		if opts.ShowZeroCreditGroups {
			hidden = ""
			action = "hide zeros"
		}
		row = append(row, fmt.Sprintf("  %d zero-credit %s%s · a %s", zeros, plural, hidden, action))
	}
	row = append(row, "")
	return row
}

// analyticsChatWindow chooses the visible rows around the cursor. The zoomed
// view fills the viewport; the overview keeps at most five rows
// (Rust AnalyticsView::chat_window).
func analyticsChatRowWindow(
	lines []string,
	rows [][]string,
	coverage []string,
	width int,
	opts AnalyticsChatPanelOptions,
) AnalyticsChatPanel {
	count := len(rows)
	rangeHeight := len(analyticsWrapLines([]string{fmt.Sprintf("Showing %d–%d of %d chats", count, count, count)}, width))
	budget := int(^uint(0) >> 1)
	if opts.Zoomed {
		budget = opts.ViewportHeight - (len(lines) + len(coverage) + rangeHeight + 4)
	}
	rowLimit := 5
	if opts.Zoomed {
		rowLimit = count
	}
	cursor := opts.Cursor
	if cursor > count-1 {
		cursor = count - 1
	}
	if cursor < 0 {
		cursor = 0
	}
	first := cursor
	end := cursor + 1
	used := len(rows[cursor])
	for first > 0 && end-first < rowLimit && used+len(rows[first-1]) <= budget {
		first--
		used += len(rows[first])
	}
	for end < count && end-first < rowLimit && used+len(rows[end]) <= budget {
		used += len(rows[end])
		end++
	}
	selectionStart, selectionEnd := 0, 1
	for index := first; index < end; index++ {
		if index == cursor {
			selectionStart = len(lines)
			selectionEnd = len(lines) + len(rows[index])
		}
		lines = append(lines, rows[index]...)
	}
	lines = append(lines, "")
	lines = append(lines, analyticsWrapLines([]string{fmt.Sprintf("Showing %d–%d of %d chats", first+1, end, count)}, width)...)
	lines = append(lines, coverage...)
	return AnalyticsChatPanel{Lines: lines, SelectionStart: selectionStart, SelectionEnd: selectionEnd}
}

// AnalyticsChatOverviewLines renders the Top chats overview card body
// (Rust dashboard.rs Section::Chats branch).
func AnalyticsChatOverviewLines(chats *AnalyticsChats, message string, ready bool, width int) []string {
	content := []string{"30d active · lifetime credits", ""}
	if !ready {
		if message != "" {
			content = append(content, message)
		}
		return content
	}
	if chats == nil || len(chats.Rows) == 0 {
		return append(content, "No recent local chats.")
	}
	unavailable := false
	for index, chat := range chats.Rows {
		if index >= 5 {
			break
		}
		amount := "—"
		if chat.Usage != nil {
			amount = analyticsCredits(chat.Usage.EstimatedUsageCreditsMicros)
		} else {
			unavailable = true
		}
		labelWidth := width - (analyticsDisplayWidth(amount) + 1)
		if labelWidth < 0 {
			labelWidth = 0
		}
		content = append(content, analyticsColumns(analyticsTruncate(chat.DisplayTitle(), labelWidth), amount, width))
	}
	content = append(content, "", analyticsChatOverviewCoverage)
	if unavailable || chatsAnyUnavailable(chats) {
		content = append(content, "Some estimates unavailable")
	}
	return content
}

func chatsAnyUnavailable(chats *AnalyticsChats) bool {
	if chats == nil {
		return false
	}
	for _, chat := range chats.Rows {
		if chat.Usage == nil {
			return true
		}
	}
	return false
}

func chatGroupText(value *string, mapName func(string) string, fallback string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return fallback
	}
	if mapName != nil {
		return mapName(*value)
	}
	return *value
}

func analyticsMinInt(left, right int) int {
	if left < right {
		return left
	}
	return right
}
