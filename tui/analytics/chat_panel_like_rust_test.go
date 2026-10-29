package analytics

import (
	"strings"
	"testing"

	"codex_go/chatgptapi"
)

// These tests mirror the Rust #45765 chat table rendering: the wide and
// stacked amount layouts, the hidden zero-credit groups, the unavailable
// breakdown, the overview card, and the viewport-sized row window.

func chatPanelTestUSD(micros int64) *int64 {
	return &micros
}

func chatPanelTestString(value string) *string {
	return &value
}

func chatPanelTestChats() *AnalyticsChats {
	return &AnalyticsChats{Rows: []AnalyticsChat{
		{
			Title: "Weekly review",
			Usage: &chatgptapi.ThreadUsage{
				EstimatedUsageCreditsMicros: 1_250_000,
				EstimatedUsageUSDMicros:     chatPanelTestUSD(1_500_000),
				Groups: []chatgptapi.ThreadUsageBreakdownGroup{
					{
						Model:                       chatPanelTestString("gpt-5-codex"),
						ReasoningEffort:             chatPanelTestString("high"),
						Speed:                       chatPanelTestString("standard"),
						EstimatedUsageCreditsMicros: 1_000_000,
					},
					{
						Model:                       chatPanelTestString("gpt-5-codex-mini"),
						EstimatedUsageCreditsMicros: 0,
					},
				},
			},
		},
		{Title: "Other account chat"},
	}}
}

func chatPanelTestContains(lines []string, needle string) bool {
	for _, line := range lines {
		if strings.Contains(line, needle) {
			return true
		}
	}
	return false
}

func TestAnalyticsChatPanelAmountLayoutsLikeRust(t *testing.T) {
	chats := chatPanelTestChats()
	wide := AnalyticsChatPanelLines(AnalyticsChatPanelOptions{
		Chats: chats, Ready: true, Width: 100, Zoomed: true, ViewportHeight: 40,
	})
	if !chatPanelTestContains(wide.Lines, "Active in past 30 days · sorted by lifetime credits") {
		t.Fatalf("wide table missing the lifetime credits header: %q", wide.Lines)
	}
	if !chatPanelTestContains(wide.Lines, "Est. $ spent  Lifetime credits") {
		t.Fatalf("wide table missing both amount columns: %q", wide.Lines)
	}
	if !chatPanelTestContains(wide.Lines, "$1.50") || !chatPanelTestContains(wide.Lines, "1.25") {
		t.Fatalf("wide table missing the USD and credit amounts: %q", wide.Lines)
	}
	if !chatPanelTestContains(wide.Lines, "1 chat estimate unavailable") {
		t.Fatalf("wide table missing the unavailable estimate note: %q", wide.Lines)
	}
	if !chatPanelTestContains(wide.Lines, "Chat usage unavailable") {
		t.Fatalf("wide table leaked a title without an estimate: %q", wide.Lines)
	}
	if !chatPanelTestContains(wide.Lines, "Local chats · excludes archived chats and subagent usage") {
		t.Fatalf("wide table missing its coverage note: %q", wide.Lines)
	}

	narrow := AnalyticsChatPanelLines(AnalyticsChatPanelOptions{
		Chats: chats, Ready: true, Width: 40, Zoomed: true, ViewportHeight: 40,
	})
	if !chatPanelTestContains(narrow.Lines, "Est. $ / credits") {
		t.Fatalf("narrow table missing the stacked amount header: %q", narrow.Lines)
	}
	stacked := false
	for index, line := range narrow.Lines {
		if strings.Contains(line, "Weekly review") && index+1 < len(narrow.Lines) &&
			strings.HasPrefix(strings.TrimSpace(narrow.Lines[index+1]), "$1.50") {
			stacked = true
		}
	}
	if !stacked {
		t.Fatalf("narrow table did not stack the amounts under the title: %q", narrow.Lines)
	}
}

func TestAnalyticsChatPanelZeroCreditGroupsLikeRust(t *testing.T) {
	chats := chatPanelTestChats()
	detail := 0
	hidden := AnalyticsChatPanelLines(AnalyticsChatPanelOptions{
		Chats: chats, Ready: true, Width: 100, Zoomed: true, ViewportHeight: 40, Detail: &detail,
	})
	if !chatPanelTestContains(hidden.Lines, "1 zero-credit group hidden · a show all") {
		t.Fatalf("hidden groups note = %q", hidden.Lines)
	}
	if chatPanelTestContains(hidden.Lines, "gpt-5-codex-mini") {
		t.Fatalf("zero-credit group rendered by default: %q", hidden.Lines)
	}
	if !chatPanelTestContains(hidden.Lines, "gpt-5-codex") {
		t.Fatalf("credited group missing: %q", hidden.Lines)
	}

	shown := AnalyticsChatPanelLines(AnalyticsChatPanelOptions{
		Chats: chats, Ready: true, Width: 100, Zoomed: true, ViewportHeight: 40,
		Detail: &detail, ShowZeroCreditGroups: true,
	})
	if !chatPanelTestContains(shown.Lines, "1 zero-credit group · a hide zeros") {
		t.Fatalf("shown groups note = %q", shown.Lines)
	}
	if !chatPanelTestContains(shown.Lines, "gpt-5-codex-mini") {
		t.Fatalf("zero-credit group stayed hidden: %q", shown.Lines)
	}
}

func TestAnalyticsChatPanelBreakdownUnavailableLikeRust(t *testing.T) {
	chats := &AnalyticsChats{Rows: []AnalyticsChat{{
		Title: "No groups",
		Usage: &chatgptapi.ThreadUsage{EstimatedUsageCreditsMicros: 1_000_000},
	}}}
	detail := 0
	panel := AnalyticsChatPanelLines(AnalyticsChatPanelOptions{
		Chats: chats, Ready: true, Width: 100, Zoomed: true, ViewportHeight: 40, Detail: &detail,
	})
	if !chatPanelTestContains(panel.Lines, "Breakdown unavailable") {
		t.Fatalf("missing breakdown note: %q", panel.Lines)
	}
}

func TestAnalyticsChatPanelWindowFollowsCursorLikeRust(t *testing.T) {
	rows := make([]AnalyticsChat, 0, 8)
	for index := 0; index < 8; index++ {
		rows = append(rows, AnalyticsChat{
			Title: "Chat " + string(rune('A'+index)),
			Usage: &chatgptapi.ThreadUsage{EstimatedUsageCreditsMicros: int64(8-index) * 100_000},
		})
	}
	chats := &AnalyticsChats{Rows: rows}
	panel := AnalyticsChatPanelLines(AnalyticsChatPanelOptions{
		Chats: chats, Ready: true, Width: 80, Zoomed: true, ViewportHeight: 14, Cursor: 5,
	})
	if !chatPanelTestContains(panel.Lines, "Showing ") {
		t.Fatalf("window missing its row counter: %q", panel.Lines)
	}
	if panel.SelectionStart >= len(panel.Lines) || panel.SelectionEnd <= panel.SelectionStart {
		t.Fatalf("selection = %d:%d of %d", panel.SelectionStart, panel.SelectionEnd, len(panel.Lines))
	}
	selected := strings.Join(panel.Lines[panel.SelectionStart:panel.SelectionEnd], "\n")
	if !strings.Contains(selected, "› Chat F") {
		t.Fatalf("selection does not cover the cursor row: %q", selected)
	}
	// A short viewport must still render every row of the selected chat.
	if panel.SelectionStart > panel.SelectionEnd {
		t.Fatalf("inverted selection %d:%d", panel.SelectionStart, panel.SelectionEnd)
	}
}

func TestAnalyticsChatOverviewLinesLikeRust(t *testing.T) {
	rows := make([]AnalyticsChat, 0, 6)
	for index := 0; index < 6; index++ {
		rows = append(rows, AnalyticsChat{
			Title: "Chat " + string(rune('A'+index)),
			Usage: &chatgptapi.ThreadUsage{EstimatedUsageCreditsMicros: int64(6-index) * 100_000},
		})
	}
	rows[5].Usage = nil
	chats := &AnalyticsChats{Rows: rows}
	lines := AnalyticsChatOverviewLines(chats, "", true, 80)
	if lines[0] != "30d active · lifetime credits" {
		t.Fatalf("overview header = %q", lines[0])
	}
	shown := 0
	for _, line := range lines {
		if strings.Contains(line, "Chat ") {
			shown++
		}
	}
	if shown != 5 {
		t.Fatalf("overview rendered %d rows, want 5: %q", shown, lines)
	}
	if chatPanelTestContains(lines, "Chat F") {
		t.Fatalf("overview rendered the sixth row: %q", lines)
	}
	if !chatPanelTestContains(lines, "Local chats · excludes subagents") {
		t.Fatalf("overview missing its coverage note: %q", lines)
	}
	if !chatPanelTestContains(lines, "Some estimates unavailable") {
		t.Fatalf("overview missing the unavailable note beyond the top rows: %q", lines)
	}

	loading := AnalyticsChatOverviewLines(nil, "Loading analytics…", false, 80)
	if !chatPanelTestContains(loading, "Loading analytics…") {
		t.Fatalf("overview missing the load message: %q", loading)
	}
}
