package app

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"codex_go/auth"
	"codex_go/tui/chatwidget"
	codextea "codex_go/tui/tea"
)

// TestInteractiveBackendBannerViewLikeRust pins the usage-read conversion:
// validated copy with {time} substitution, CTA resolution, and dismissal.
func TestInteractiveBackendBannerViewLikeRust(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		"banner_type": "usage_limit",
		"title":       "Usage limit reached",
		"description": "Resets at {time}.",
		"ctas": []any{
			map[string]any{"action": "view_usage", "label": "View usage"},
			map[string]any{"action": "notify_owner", "label": "Notify owner"},
		},
		"presentation": "dismissible",
		"reset_at":     int64(1_700_000_000),
	})
	if err != nil {
		t.Fatal(err)
	}
	accountID := "acct-1"
	response := &auth.GetAccountRateLimitsResponse{RateLimitUpsell: payload, AccountID: &accountID}
	read := interactiveBackendBannerRead(response, auth.PlanPlus, time.Unix(1_700_000_000, 0))
	view := read.Banner
	if view == nil {
		t.Fatal("banner view was not produced")
	}
	if view.Title != "Usage limit reached" || !view.Dismissible {
		t.Fatalf("banner view = %#v", view)
	}
	if strings.Contains(view.Description, "{time}") || !strings.HasPrefix(view.Description, "Resets at ") {
		t.Fatalf("reset time was not substituted: %q", view.Description)
	}
	if len(view.Actions) != 2 {
		t.Fatalf("actions = %#v", view.Actions)
	}
	if view.Actions[0].Label != "View usage" || view.Actions[0].Kind != codextea.BannerActionOpenURL {
		t.Fatalf("first CTA = %#v", view.Actions[0])
	}
	if view.Actions[0].URL == "" {
		t.Fatalf("view usage CTA has no destination: %#v", view.Actions[0])
	}
	if view.Actions[1].Kind != codextea.BannerActionNotifyOwner {
		t.Fatalf("second CTA = %#v", view.Actions[1])
	}
	if view.AccountID != "acct-1" || view.ResetAt == nil || *view.ResetAt != 1_700_000_000 {
		t.Fatalf("banner identity = %#v", view)
	}
	if read.Recovery.AccountID != "acct-1" || !read.Recovery.HasRateLimitUpsell {
		t.Fatalf("recovery inputs = %#v", read.Recovery)
	}
}

// TestInteractiveBackendBannerReadRejectsAbsentPayload pins that a missing or
// null upsell leaves the surface untouched (Rust keeps the existing UI).
func TestInteractiveBackendBannerReadRejectsAbsentPayload(t *testing.T) {
	if read := interactiveBackendBannerRead(nil, auth.PlanPlus, time.Now()); read.Banner != nil {
		t.Fatalf("nil response produced a banner: %#v", read.Banner)
	}
	if read := interactiveBackendBannerRead(&auth.GetAccountRateLimitsResponse{RateLimitUpsell: json.RawMessage("null")}, auth.PlanPlus, time.Now()); read.Banner != nil {
		t.Fatalf("null upsell produced a banner: %#v", read.Banner)
	}
	bad := json.RawMessage(`{"banner_type":"usage_limit","title":"","description":"x","ctas":[]}`)
	if read := interactiveBackendBannerRead(&auth.GetAccountRateLimitsResponse{RateLimitUpsell: bad}, auth.PlanPlus, time.Now()); read.Banner != nil {
		t.Fatalf("invalid payload produced a banner: %#v", read.Banner)
	}
}

// TestInteractiveBackendBannerActionHandlerDispatches pins CTA dispatch.
func TestInteractiveBackendBannerActionHandlerDispatches(t *testing.T) {
	notified := 0
	handler := interactiveBackendBannerActionHandler(func(creditType chatwidget.AddCreditsNudgeCreditType) error {
		notified++
		return nil
	})
	if cmd := handler(codextea.BackendBannerAction{Kind: codextea.BannerActionOpenURL}); cmd != nil {
		t.Fatal("an empty destination should not open a browser")
	}
	if cmd := handler(codextea.BackendBannerAction{Kind: codextea.BannerActionResetUsage}); cmd != nil {
		t.Fatal("the reset-usage CTA is handled inside the model")
	}
	cmd := handler(codextea.BackendBannerAction{Kind: codextea.BannerActionNotifyOwner})
	if cmd == nil {
		t.Fatal("notify-owner CTA returned no command")
	}
	_ = cmd()
	if notified != 1 {
		t.Fatalf("nudge notifications = %d, want 1", notified)
	}
}
