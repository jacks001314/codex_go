package analytics

import (
	"math"
	"strings"
	"testing"

	"codex_go/chatgptapi"
)

func planHistoryFixture() *chatgptapi.PlanLimitHistory {
	asOf := "2026-09-15T18:44:59Z"
	used := 4250.0
	return &chatgptapi.PlanLimitHistory{
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
			Breakdowns: []chatgptapi.PlanLimitBreakdown{{
				Dimension: chatgptapi.PlanLimitDimensionModel,
				Rows: []chatgptapi.PlanLimitValue{
					{Key: "z-model", BasisPoints: 1000},
					{Key: "a-model", BasisPoints: 3250},
				},
			}},
		}},
	}
}

func TestParsePlanHistoryValidatesWindowsAndUnknowns(t *testing.T) {
	report, err := ParsePlanHistory(planHistoryFixture())
	if err != nil || report == nil {
		t.Fatalf("ParsePlanHistory() = (%#v, %v)", report, err)
	}
	if len(report.Periods[PlanWindowFiveHour]) != 1 || len(report.Periods[PlanWindowWeekly]) != 0 {
		t.Fatalf("period buckets = %#v", report.Periods)
	}
	if !report.Approximate {
		t.Fatal("omitted approximate must default to true")
	}

	missing := planHistoryFixture()
	missing.DataAsOf = nil
	if report, err := ParsePlanHistory(missing); err != nil || report != nil {
		t.Fatalf("snapshot without data_as_of = (%#v, %v), want (nil, nil)", report, err)
	}

	badWindow := planHistoryFixture()
	badWindow.Periods[0].WindowMinutes = 60
	if _, err := ParsePlanHistory(badWindow); err == nil || err.Error() != "Unsupported plan history window." {
		t.Fatalf("bad window error = %v", err)
	}

	duplicate := planHistoryFixture()
	second := duplicate.Periods[0]
	second.WindowMinutes = 10080
	duplicate.Periods = append(duplicate.Periods, second)
	if _, err := ParsePlanHistory(duplicate); err == nil || err.Error() != "Invalid plan history period." {
		t.Fatalf("duplicate id error = %v", err)
	}

	nan := planHistoryFixture()
	bad := math.NaN()
	nan.Periods[0].UsedBasisPoints = &bad
	if _, err := ParsePlanHistory(nan); err == nil || err.Error() != "Invalid plan history period." {
		t.Fatalf("nan usage error = %v", err)
	}

	badStamp := planHistoryFixture()
	badStamp.Periods[0].StartsAt = "yesterday"
	if _, err := ParsePlanHistory(badStamp); err == nil || err.Error() != "Invalid plan history timestamp." {
		t.Fatalf("bad timestamp error = %v", err)
	}
}

func TestParsePlanHistoryRejectsTooManyPeriods(t *testing.T) {
	history := planHistoryFixture()
	for i := 0; i < maxPlanHistoryPeriods; i++ {
		period := history.Periods[0]
		period.ID = "extra-" + string(rune('a'+i%26))
		history.Periods = append(history.Periods, period)
	}
	if _, err := ParsePlanHistory(history); err == nil || err.Error() != "Plan history contains too many periods." {
		t.Fatalf("too many periods error = %v", err)
	}
}

func TestRenderPlanHistoryKeepsUnknownUsageAndSortedBreakdowns(t *testing.T) {
	report, err := ParsePlanHistory(planHistoryFixture())
	if err != nil {
		t.Fatalf("ParsePlanHistory() error = %v", err)
	}
	lines := RenderPlanHistory(report, 120)
	rendered := strings.Join(lines, "\n")
	for _, want := range []string{
		"Plan usage history",
		"snapshot as of 2026-09-15T18:44:59Z (approximate)",
		"coverage complete",
		"5-hour periods",
		"42.5%",
		"model: a-model 32.5%, z-model 10.0%",
		"weekly periods",
		"no periods reported",
	} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("render missing %q:\n%s", want, rendered)
		}
	}

	unknown := planHistoryFixture()
	unknown.Periods[0].UsedBasisPoints = nil
	unknown.Periods[0].AccountingComplete = false
	report, err = ParsePlanHistory(unknown)
	if err != nil {
		t.Fatalf("ParsePlanHistory() error = %v", err)
	}
	rendered = strings.Join(RenderPlanHistory(report, 120), "\n")
	if !strings.Contains(rendered, "unknown (incomplete accounting)") {
		t.Fatalf("unknown usage not preserved:\n%s", rendered)
	}
	if strings.Contains(rendered, "  0.0%") {
		t.Fatalf("unknown usage became zero:\n%s", rendered)
	}
}

func TestRenderPlanHistoryNarrowTruncatesAndUnavailable(t *testing.T) {
	report, err := ParsePlanHistory(planHistoryFixture())
	if err != nil {
		t.Fatalf("ParsePlanHistory() error = %v", err)
	}
	for _, line := range RenderPlanHistory(report, 24) {
		if len([]rune(line)) > 24 {
			t.Fatalf("line %q exceeds width 24", line)
		}
	}
	if got := RenderPlanHistory(nil, 80); len(got) != 1 || got[0] != "Plan history unavailable for this account." {
		t.Fatalf("nil report render = %#v", got)
	}
}
