package app

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"codex_go/auth"
	chatwidget "codex_go/tui/chatwidget"
	"codex_go/tui/status"
)

func TestParseBackendBannerRejectsTooManyActions(t *testing.T) {
	var ctas []any
	var labels []string
	for index := 1; index <= backendBannerMaxCTAs; index++ {
		label := fmt.Sprintf("Action %d", index)
		labels = append(labels, label)
		ctas = append(ctas, map[string]any{"action": "view_usage", "label": label})
	}
	raw := map[string]any{
		"banner_type": "usage_limit",
		"title":       "Usage limit reached",
		"description": "Choose how to continue.",
		"ctas":        ctas,
	}
	banner, ok := ParseBackendBanner(raw)
	if !ok {
		t.Fatal("eight actions should fit")
	}
	actions := banner.ActionableActions()
	if len(actions) != len(labels) {
		t.Fatalf("actions = %#v", actions)
	}
	for index, action := range actions {
		if action.Label != labels[index] {
			t.Fatalf("action %d = %q, want %q", index, action.Label, labels[index])
		}
	}

	raw["ctas"] = append(ctas, map[string]any{"action": "view_usage", "label": "Hidden ninth action"})
	if _, ok := ParseBackendBanner(raw); ok {
		t.Fatal("a ninth action must be rejected")
	}
}

func TestParseBackendBannerRejectsUnsupportedOrUnrenderableContent(t *testing.T) {
	valid := func() map[string]any {
		return map[string]any{
			"banner_type": "usage_limit",
			"title":       "Usage limit reached",
			"description": "Choose how to continue.",
			"ctas":        []any{},
		}
	}
	invalidFields := []map[string]any{
		{"presentation": "future_mode"},
		{"presentation": nil},
		{"title": " "},
		{"title": strings.Repeat("x", backendBannerMaxTitleBytes+1)},
		{"title": strings.Repeat("line\n", backendBannerMaxTitleLines+1)},
		{"description": strings.Repeat("x", backendBannerMaxDescriptionByte+1)},
		{"description": strings.Repeat("line\n", backendBannerMaxDescriptionLine+1)},
		{"blocked_model_slug": ""},
		{"blocked_model_slug": "bad\nslug"},
		{"fallback_model_slugs": []any{strings.Repeat("x", backendBannerMaxSlugBytes+1)}},
		{"fallback_model_slugs": repeatedStrings("model", backendBannerMaxFallbackSlugs+1)},
	}
	for _, fields := range invalidFields {
		raw := valid()
		for key, value := range fields {
			raw[key] = value
		}
		if _, ok := ParseBackendBanner(raw); ok {
			t.Fatalf("invalid field %#v was accepted", fields)
		}
	}

	// Missing required fields are rejected too (Rust's non-default fields).
	for _, missing := range []string{"banner_type", "title", "description", "ctas"} {
		raw := valid()
		delete(raw, missing)
		if _, ok := ParseBackendBanner(raw); ok {
			t.Fatalf("missing %q was accepted", missing)
		}
	}

	// A supported dismissible banner parses.
	raw := valid()
	raw["presentation"] = "dismissible"
	banner, ok := ParseBackendBanner(raw)
	if !ok || !banner.Dismissible() {
		t.Fatalf("dismissible banner = %#v ok=%v", banner, ok)
	}
}

func repeatedStrings(value string, count int) []any {
	out := make([]any, 0, count)
	for index := 0; index < count; index++ {
		out = append(out, value)
	}
	return out
}

func TestResolveBackendBannerActions(t *testing.T) {
	requestURL := "https://example.test/request"
	personal := BackendBanner{PlanType: auth.PlanPlus}
	workspaceWithAccount := BackendBanner{PlanType: auth.PlanBusiness, AccountID: "account-1"}

	cases := []struct {
		name   string
		banner BackendBanner
		action string
		kind   BannerActionKind
		url    string
		credit chatwidget.AddCreditsNudgeCreditType
		ok     bool
	}{
		{"notify owner", personal, "notify_owner", BannerActionNotifyOwner, "", chatwidget.AddCreditsNudgeCredits, true},
		{"contact owner", personal, "contact_owner", BannerActionNotifyOwner, "", chatwidget.AddCreditsNudgeCredits, true},
		{"reset usage", personal, "reset_usage", BannerActionResetUsage, "", "", true},
		{"request increase without url", personal, "request_increase", BannerActionNotifyOwner, "", chatwidget.AddCreditsNudgeUsageLimit, true},
		{"request increase with url", BackendBanner{RequestURL: &requestURL}, "request_increase", BannerActionOpenURL, requestURL, "", true},
		{"view usage", personal, "view_usage", BannerActionOpenURL, backendBannerUsageURL, "", true},
		{"workspace usage", workspaceWithAccount, "view_workspace_usage", BannerActionOpenURL, backendBannerWorkspaceUsageURL + "?account_id=account-1", "", true},
		{"buy reset", personal, "buy_reset", BannerActionOpenURL, "https://chatgpt.com/codex/purchase/reset", "", true},
		{"plus pricing", personal, "open_plus_pricing_web", BannerActionOpenURL, "https://chatgpt.com/explore/plus", "", true},
		{"unknown action", personal, "open_referral", "", "", "", false},
	}
	for _, test := range cases {
		action, ok := test.banner.ResolveBannerAction(test.action)
		if ok != test.ok {
			t.Fatalf("%s: ok = %v, want %v", test.name, ok, test.ok)
		}
		if !ok {
			continue
		}
		if action.Kind != test.kind {
			t.Fatalf("%s: kind = %q, want %q", test.name, action.Kind, test.kind)
		}
		if test.url != "" && action.URL != test.url {
			t.Fatalf("%s: url = %q, want %q", test.name, action.URL, test.url)
		}
		if test.credit != "" && action.CreditType != test.credit {
			t.Fatalf("%s: credit = %q, want %q", test.name, action.CreditType, test.credit)
		}
	}

	// add_credits routes workspace accounts to admin billing.
	action, ok := workspaceWithAccount.ResolveBannerAction("add_credits")
	if !ok || !strings.HasPrefix(action.URL, "https://chatgpt.com/admin/billing?") ||
		!strings.Contains(action.URL, "codex_credit_action=add_credits") || !strings.Contains(action.URL, "account_id=account-1") {
		t.Fatalf("workspace add_credits = %#v ok=%v", action, ok)
	}
	action, ok = personal.ResolveBannerAction("add_credits")
	if !ok || action.URL != backendBannerUsageURL+"?credits_modal=true" {
		t.Fatalf("personal add_credits = %#v ok=%v", action, ok)
	}

	// open_pricing_dialog targets pro for Plus/ProLite and plus otherwise.
	action, ok = personal.ResolveBannerAction("open_pricing_dialog")
	if !ok || !strings.Contains(action.URL, "highlight_plan=pro") || !strings.HasSuffix(action.URL, "#pricing") {
		t.Fatalf("plus pricing dialog = %#v ok=%v", action, ok)
	}
	action, ok = BackendBanner{PlanType: auth.PlanProlite}.ResolveBannerAction("open_pricing_dialog")
	if !ok || !strings.Contains(action.URL, "pro_variant=2x") {
		t.Fatalf("prolite pricing dialog = %#v ok=%v", action, ok)
	}
	action, ok = BackendBanner{PlanType: auth.PlanFree}.ResolveBannerAction("open_pricing_dialog")
	if !ok || !strings.Contains(action.URL, "highlight_plan=plus") {
		t.Fatalf("free pricing dialog = %#v ok=%v", action, ok)
	}
}

func TestResolveBackendBannerRejectsUnsafeDestinations(t *testing.T) {
	for _, raw := range []string{
		"javascript:alert(1)",
		"https://user:pass@example.test/x",
		"https:///no-host",
		"not a url",
	} {
		banner := BackendBanner{RequestURL: &raw}
		if action, ok := banner.ResolveBannerAction("request_increase"); ok {
			t.Fatalf("unsafe destination %q resolved to %#v", raw, action)
		}
	}

	// Admin destinations require an account id (Rust resolve_action).
	adminBanner := BackendBanner{PlanType: auth.PlanBusiness}
	if action, ok := adminBanner.ResolveBannerAction("view_workspace_usage"); ok {
		t.Fatalf("workspace usage without account id = %#v", action)
	}
	adminBanner.AccountID = "account-1"
	action, ok := adminBanner.ResolveBannerAction("increase_spend_cap")
	if !ok || !strings.Contains(action.URL, "account_id=account-1") {
		t.Fatalf("increase spend cap = %#v ok=%v", action, ok)
	}
}

func TestBackendBannerCopyStripsControlsAndSubstitutesResetTime(t *testing.T) {
	resetAt := time.Date(2026, 7, 7, 15, 14, 0, 0, time.UTC).Unix()
	banner := BackendBanner{ResetAt: &resetAt}
	now := time.Date(2026, 7, 7, 15, 0, 0, 0, time.UTC)
	wantTime := status.FormatResetTimestamp(time.Unix(resetAt, 0), now)
	copied := banner.BannerCopy("Resets at {time}\x07\nsecond line", now)
	if copied != "Resets at "+wantTime+"\nsecond line" {
		t.Fatalf("copy = %q", copied)
	}
	if got := (BackendBanner{}).BannerCopy("no placeholder", now); got != "no placeholder" {
		t.Fatalf("copy without reset = %q", got)
	}
}
