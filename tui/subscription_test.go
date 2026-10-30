package tui

import (
	"testing"

	"codex_go/auth"
)

// TestSubscriptionLabelsMatchRust pins Rust #49079
// (codex-rs/tui/src/subscription.rs `SubscriptionDisplay::label`): status and
// analytics share one mapping, with the Pro tiers renamed to Pro 100/200/500
// and the status-only Enterprise/Edu variants preserved.
func TestSubscriptionLabelsMatchRust(t *testing.T) {
	cases := []struct {
		plan      auth.PlanType
		status    string
		analytics string
	}{
		{auth.PlanFree, "Free", "Free"},
		{auth.PlanGo, "Go", "Go"},
		{auth.PlanPlus, "Plus", "Plus"},
		{auth.PlanPro, "Pro 200", "Pro 200"},
		{auth.PlanProlite, "Pro 100", "Pro 100"},
		{PlanProMax, "Pro 500", "Pro 500"},
		{auth.PlanTeam, "Business", "Business"},
		{auth.PlanSelfServeBusinessUsageBased, "Business", "Business"},
		{auth.PlanBusiness, "Enterprise", "Business"},
		{auth.PlanSelfServeBusinessProlite, "Business Premium", "Business"},
		{auth.PlanEnterpriseCBPAutomation, "Enterprise (Automation)", "Enterprise"},
		{auth.PlanEnterprise, "Enterprise", "Enterprise"},
		{auth.PlanEnt26, "Enterprise", "Enterprise"},
		{auth.PlanEnterpriseCBPUsageBased, "Enterprise", "Enterprise"},
		{auth.PlanEdu, "Edu", "Education"},
		{auth.PlanEduPlus, "Edu Plus", "Education"},
		{auth.PlanEduPro, "Edu Pro", "Education"},
		{auth.PlanUnknown, "Unknown", "Account"},
	}
	for _, tc := range cases {
		if got := SubscriptionLabel(tc.plan, SubscriptionStatus); got != tc.status {
			t.Fatalf("SubscriptionLabel(%q, status) = %q, want %q", tc.plan, got, tc.status)
		}
		if got := SubscriptionLabel(tc.plan, SubscriptionAnalytics); got != tc.analytics {
			t.Fatalf("SubscriptionLabel(%q, analytics) = %q, want %q", tc.plan, got, tc.analytics)
		}
	}
}
