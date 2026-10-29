package analytics

import (
	"strings"
	"testing"
	"time"

	"codex_go/chatgptapi"
)

// These tests mirror Rust #45768's consumer panel coverage: metric selection,
// ranking, partial and unavailable rows, the top-five card, and the expanded
// breakdown.

func taskPanelPercent(value float64) *float64 {
	return &value
}

func taskPanelCredits(value string) *chatgptapi.TaskUsageCredits {
	credits, err := chatgptapi.ParseTaskUsageCredits(value)
	if err != nil {
		panic(err)
	}
	return &credits
}

func taskPanelChat(title string, amounts chatgptapi.TaskUsageAmounts, status chatgptapi.TaskUsageStatus, groups ...chatgptapi.TaskUsageGroup) AnalyticsTaskChat {
	return AnalyticsTaskChat{
		Title: title,
		Task: &chatgptapi.TaskUsage{
			ThreadID:   title,
			DataStatus: status,
			Amounts:    amounts,
			Groups:     groups,
		},
	}
}

func taskPanelString(value string) *string {
	return &value
}

func taskPanelContains(lines []string, needle string) bool {
	for _, line := range lines {
		if strings.Contains(line, needle) {
			return true
		}
	}
	return false
}

func TestAnalyticsTaskMetricsFollowAvailableAmountsLikeRust(t *testing.T) {
	chats := &AnalyticsTaskChats{Rows: []AnalyticsTaskChat{
		taskPanelChat("five-hour only", chatgptapi.TaskUsageAmounts{
			FiveHourLimitPercent: taskPanelPercent(42.5),
			BalanceUsageCredits:  taskPanelCredits("1.5"),
		}, chatgptapi.TaskUsageStatusAvailable),
		{Title: "missing"},
	}}
	metrics := AnalyticsTaskMetrics(chats)
	if len(metrics) != 2 || metrics[0] != 1 || metrics[1] != 2 {
		t.Fatalf("metrics = %v, want [1 2]", metrics)
	}
	if got := AnalyticsTaskMetric(0, chats); got != 1 {
		t.Fatalf("metric fallback = %d, want 1", got)
	}
	if got := AnalyticsTaskMetric(2, chats); got != 2 {
		t.Fatalf("metric 2 = %d, want 2", got)
	}
	weekly := AnalyticsTaskMetrics(&AnalyticsTaskChats{Rows: []AnalyticsTaskChat{
		taskPanelChat("both", chatgptapi.TaskUsageAmounts{
			WeeklyLimitPercent:   taskPanelPercent(12.25),
			FiveHourLimitPercent: taskPanelPercent(50),
		}, chatgptapi.TaskUsageStatusAvailable),
	}})
	if len(weekly) != 3 || weekly[0] != 0 || weekly[1] != 1 || weekly[2] != 2 {
		t.Fatalf("metrics = %v, want [0 1 2]", weekly)
	}
}

func TestAnalyticsTaskRowsRankAndKeepMissingLastLikeRust(t *testing.T) {
	chats := &AnalyticsTaskChats{Rows: []AnalyticsTaskChat{
		taskPanelChat("middle", chatgptapi.TaskUsageAmounts{BalanceUsageCredits: taskPanelCredits("1.5")}, chatgptapi.TaskUsageStatusAvailable),
		{Title: "unavailable"},
		taskPanelChat("top", chatgptapi.TaskUsageAmounts{BalanceUsageCredits: taskPanelCredits("1e-20")}, chatgptapi.TaskUsageStatusAvailable),
		taskPanelChat("zero", chatgptapi.TaskUsageAmounts{BalanceUsageCredits: taskPanelCredits("0")}, chatgptapi.TaskUsageStatusAvailable),
	}}
	rows := AnalyticsTaskRows(chats, 2)
	got := make([]string, 0, len(rows))
	for _, row := range rows {
		got = append(got, row.DisplayTitle())
	}
	want := []string{"middle", "top", "zero", analyticsChatUnavailableTitle}
	for index := range want {
		if got[index] != want[index] {
			t.Fatalf("ranked rows = %v, want %v", got, want)
		}
	}
	byPercent := &AnalyticsTaskChats{Rows: []AnalyticsTaskChat{
		taskPanelChat("low", chatgptapi.TaskUsageAmounts{WeeklyLimitPercent: taskPanelPercent(1)}, chatgptapi.TaskUsageStatusAvailable),
		taskPanelChat("high", chatgptapi.TaskUsageAmounts{WeeklyLimitPercent: taskPanelPercent(90)}, chatgptapi.TaskUsageStatusAvailable),
		{Title: "none"},
	}}
	rows = AnalyticsTaskRows(byPercent, 0)
	if rows[0].Title != "high" || rows[1].Title != "low" || rows[2].Title != "none" {
		t.Fatalf("percent ranking = %+v", rows)
	}
}

func TestAnalyticsTaskPanelRendersPartialAndUnavailableRowsLikeRust(t *testing.T) {
	updated := time.Date(2026, 10, 1, 12, 30, 0, 0, time.UTC)
	chats := &AnalyticsTaskChats{
		Truncated: true,
		UpdatedAt: &updated,
		Rows: []AnalyticsTaskChat{
			taskPanelChat("Weekly review", chatgptapi.TaskUsageAmounts{
				WeeklyLimitPercent:   taskPanelPercent(12.25),
				FiveHourLimitPercent: taskPanelPercent(150.5),
				BalanceUsageCredits:  taskPanelCredits("12.500000000000000001"),
			}, chatgptapi.TaskUsageStatusPartial, chatgptapi.TaskUsageGroup{
				Model:             taskPanelString("gpt-5.5"),
				ReasoningEffort:   taskPanelString("high"),
				Speed:             taskPanelString("standard"),
				ProductExperience: taskPanelString("codex"),
				Amounts: chatgptapi.TaskUsageAmounts{
					WeeklyLimitPercent:   taskPanelPercent(10),
					FiveHourLimitPercent: taskPanelPercent(120),
					BalanceUsageCredits:  taskPanelCredits("9.25"),
				},
			}),
			{Title: "Other account"},
		},
	}
	detail := 0
	panel := AnalyticsTaskPanelLines(AnalyticsTaskPanelOptions{
		Chats: chats, Ready: true, Width: 110, Zoomed: true, ViewportHeight: 60,
		Detail: &detail, Metric: 2,
	})
	if !taskPanelContains(panel.Lines, "Usage estimates · active in past 30 days · sorted by Balance credits") {
		t.Fatalf("header missing: %q", panel.Lines)
	}
	if !taskPanelContains(panel.Lines, "Credits debited from balance · includes adjustments") {
		t.Fatalf("balance subtitle missing: %q", panel.Lines)
	}
	if !taskPanelContains(panel.Lines, "Partial ranking · 1 partial · 1 unavailable") {
		t.Fatalf("partial summary missing: %q", panel.Lines)
	}
	if !taskPanelContains(panel.Lines, "Weekly review · partial") {
		t.Fatalf("partial marker missing: %q", panel.Lines)
	}
	if !taskPanelContains(panel.Lines, "12.500000000000000001") || !taskPanelContains(panel.Lines, "—") {
		t.Fatalf("amounts missing: %q", panel.Lines)
	}
	if !taskPanelContains(panel.Lines, "Model / effort / speed") ||
		!taskPanelContains(panel.Lines, "high · standard · codex") {
		t.Fatalf("group breakdown missing: %q", panel.Lines)
	}
	if !taskPanelContains(panel.Lines, "Local chats · includes discovered descendants · excludes archived roots") {
		t.Fatalf("coverage missing: %q", panel.Lines)
	}
	if !taskPanelContains(panel.Lines, "Partial ranking · 100 most recently active chats") {
		t.Fatalf("truncation note missing: %q", panel.Lines)
	}
	if !taskPanelContains(panel.Lines, "Updated Oct 1 12:30 UTC · recent activity may be delayed") {
		t.Fatalf("freshness note missing: %q", panel.Lines)
	}
	selected := strings.Join(panel.Lines[panel.SelectionStart:panel.SelectionEnd], "\n")
	if !strings.Contains(selected, "Weekly review") {
		t.Fatalf("selection does not cover the cursor row: %q", selected)
	}

	missingDetail := 1
	missing := AnalyticsTaskPanelLines(AnalyticsTaskPanelOptions{
		Chats: chats, Ready: true, Width: 110, Zoomed: true, ViewportHeight: 60,
		Detail: &missingDetail, Metric: 2,
	})
	if !taskPanelContains(missing.Lines, "Task usage unavailable.") {
		t.Fatalf("unavailable detail missing: %q", missing.Lines)
	}
}

func TestAnalyticsTaskPanelCardShowsTopFiveLikeRust(t *testing.T) {
	rows := make([]AnalyticsTaskChat, 0, 6)
	for index := 0; index < 6; index++ {
		rows = append(rows, taskPanelChat(
			"Chat "+string(rune('A'+index)),
			chatgptapi.TaskUsageAmounts{WeeklyLimitPercent: taskPanelPercent(float64(6 - index))},
			chatgptapi.TaskUsageStatusAvailable,
		))
	}
	card := AnalyticsTaskPanelLines(AnalyticsTaskPanelOptions{
		Chats: &AnalyticsTaskChats{Rows: rows}, Ready: true, Width: 80, Zoomed: false, Metric: 0,
	})
	for index := 0; index < 5; index++ {
		if !taskPanelContains(card.Lines, "Chat "+string(rune('A'+index))) {
			t.Fatalf("card missing row %d: %q", index, card.Lines)
		}
	}
	if taskPanelContains(card.Lines, "Chat F") {
		t.Fatalf("card rendered the sixth row: %q", card.Lines)
	}
	if !taskPanelContains(card.Lines, "Showing top 5 of 6 chats") {
		t.Fatalf("card counter missing: %q", card.Lines)
	}
	if card.SelectionStart != 0 || card.SelectionEnd != 1 {
		t.Fatalf("card selection = %d:%d, want 0:1", card.SelectionStart, card.SelectionEnd)
	}

	loading := AnalyticsTaskPanelLines(AnalyticsTaskPanelOptions{Width: 80, Zoomed: false})
	if len(loading.Lines) != 1 || loading.Lines[0] != "No task usage reported." {
		t.Fatalf("default message = %q", loading.Lines)
	}
	custom := AnalyticsTaskPanelLines(AnalyticsTaskPanelOptions{Width: 80, Message: "Loading analytics…"})
	if len(custom.Lines) != 1 || custom.Lines[0] != "Loading analytics…" {
		t.Fatalf("custom message = %q", custom.Lines)
	}
}
