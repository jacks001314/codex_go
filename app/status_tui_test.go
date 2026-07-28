package app

import (
	"reflect"
	"testing"

	"codex_go/auth"
	codextui "codex_go/tui"
)

func TestInteractiveRateLimitStatuses(t *testing.T) {
	fiveHours := int64(300)
	weekly := int64(7 * 24 * 60)
	got := interactiveRateLimitStatuses(&auth.GetAccountRateLimitsResponse{RateLimits: auth.RateLimitSnapshot{
		Primary:   &auth.RateLimitWindow{UsedPercent: 37, WindowDurationMins: &fiveHours},
		Secondary: &auth.RateLimitWindow{UsedPercent: 81, WindowDurationMins: &weekly},
	}})
	want := []codextui.RateLimitStatus{{Label: "5h", UsedPercent: 37}, {Label: "weekly", UsedPercent: 81}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("interactiveRateLimitStatuses() = %#v, want %#v", got, want)
	}
}
