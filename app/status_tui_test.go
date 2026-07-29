package app

import (
	"strings"
	"testing"
	"time"

	"codex_go/auth"
)

func TestInteractiveRateLimitStatuses(t *testing.T) {
	fiveHours := int64(300)
	weekly := int64(7 * 24 * 60)
	got := interactiveRateLimitStatuses(&auth.GetAccountRateLimitsResponse{RateLimits: auth.RateLimitSnapshot{
		Primary:   &auth.RateLimitWindow{UsedPercent: 37, WindowDurationMins: &fiveHours},
		Secondary: &auth.RateLimitWindow{UsedPercent: 81, WindowDurationMins: &weekly},
	}})
	if len(got) != 2 || got[0].Label != "5h limit" || got[0].UsedPercent != 37 || got[1].Label != "Weekly limit" || got[1].UsedPercent != 81 {
		t.Fatalf("interactiveRateLimitStatuses() = %#v", got)
	}
	if got[0].CapturedAt.IsZero() || got[1].CapturedAt.IsZero() {
		t.Fatalf("interactiveRateLimitStatuses() omitted capture time: %#v", got)
	}
}

func TestInteractiveRateLimitStatusesPreservesBucketsResetsAndCredits(t *testing.T) {
	fiveHours := int64(300)
	reset := time.Date(2026, 7, 29, 4, 0, 0, 0, time.Local).Unix()
	name := "other"
	balance := "37.5"
	response := &auth.GetAccountRateLimitsResponse{RateLimitsByLimitID: map[string]auth.RateLimitSnapshot{
		"other": {
			LimitName:       &name,
			Primary:         &auth.RateLimitWindow{UsedPercent: 20, WindowDurationMins: &fiveHours, ResetsAt: &reset},
			Credits:         &auth.CreditsSnapshot{HasCredits: true, Balance: &balance},
			IndividualLimit: &auth.SpendControlLimitSnapshot{Limit: "100", Used: "25", RemainingPercent: 75, ResetsAt: reset},
		},
	}}
	got := interactiveRateLimitStatuses(response)
	if len(got) != 3 || got[0].Label != "other 5h limit" || got[0].ResetsAt == nil || got[1].Text != "38 credits" || got[2].Label != "Monthly credit limit" {
		t.Fatalf("detailed rate limits = %#v", got)
	}
	if !strings.Contains(got[2].Details, "25 of 100 credits used") || got[2].UsedPercent != 25 {
		t.Fatalf("monthly credit row = %#v", got[2])
	}
}
