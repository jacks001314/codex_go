package tea

import (
	"errors"
	"strings"
	"testing"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/chatgptapi"
	codextui "codex_go/tui"
	"codex_go/tui/analytics"
)

func planHistoryTestReport(t *testing.T) *analytics.Report {
	t.Helper()
	asOf := "2026-09-15T18:44:59Z"
	used := 4250.0
	report, err := analytics.ParsePlanHistory(&chatgptapi.PlanLimitHistory{
		DataAsOf:         &asOf,
		CoverageComplete: true,
		Periods: []chatgptapi.PlanLimitPeriod{{
			ID:                 "p1",
			WindowMinutes:      300,
			PlanType:           "plus",
			StartsAt:           "2026-09-15T12:00:00Z",
			EndsAt:             "2026-09-15T17:00:00Z",
			AccountingComplete: true,
			UsedBasisPoints:    &used,
		}},
	})
	if err != nil || report == nil {
		t.Fatalf("fixture report = (%#v, %v)", report, err)
	}
	return report
}

func TestUsagePlanRequiresFeatureGateLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{
		Width: 80, Height: 24, HasChatGPTAccount: true,
		OnReadPlanHistory: func() (*analytics.Report, error) {
			t.Fatal("reader called while analytics_plan_history is disabled")
			return nil, nil
		},
	})
	typeText(t, model, "/usage plan")
	if _, cmd := model.Update(key(bubbletea.KeyEnter)); cmd != nil {
		t.Fatal("disabled plan history must not start a request")
	}
	if model.notice != "Plan usage history is not enabled." {
		t.Fatalf("notice = %q", model.notice)
	}
}

func TestUsagePlanRendersReportLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{
		Width: 100, Height: 30, HasChatGPTAccount: true,
		PlanHistoryEnabled: true,
		OnReadPlanHistory:  func() (*analytics.Report, error) { return planHistoryTestReport(t), nil },
	})
	typeText(t, model, "/usage plan")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	if cmd == nil {
		t.Fatal("enabled plan history did not start a request")
	}
	model.Update(cmd())

	raw := strings.Join(historyRawTexts(state), "\n")
	for _, want := range []string{"Plan usage history", "5-hour periods", "42.5%"} {
		if !strings.Contains(raw, want) {
			t.Fatalf("transcript missing %q:\n%s", want, raw)
		}
	}
}

func TestUsagePlanReaderFailureRendersUnavailableLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{
		Width: 100, Height: 30, HasChatGPTAccount: true,
		PlanHistoryEnabled: true,
		OnReadPlanHistory:  func() (*analytics.Report, error) { return nil, errors.New("boom") },
	})
	typeText(t, model, "/usage plan")
	_, cmd := model.Update(key(bubbletea.KeyEnter))
	model.Update(cmd())
	raw := strings.Join(historyRawTexts(state), "\n")
	if !strings.Contains(raw, "Plan history unavailable for this account.") {
		t.Fatalf("unavailable line missing:\n%s", raw)
	}
}

func TestUsagePlanIgnoresStaleResultsLikeRust(t *testing.T) {
	state := codextui.NewState(nil)
	model := NewModel(state, Options{
		Width: 80, Height: 24, HasChatGPTAccount: true, PlanHistoryEnabled: true,
	})
	model.pendingPlanHistoryRequestID = 7
	before := len(state.Messages)
	model.applyPlanHistoryResult(PlanHistoryResultMsg{RequestID: 6, Report: planHistoryTestReport(t)})
	if len(state.Messages) != before {
		t.Fatalf("stale plan history result was applied")
	}
}

func historyRawTexts(state *codextui.State) []string {
	var out []string
	for _, message := range state.Messages {
		out = append(out, message.RawText)
	}
	return out
}
