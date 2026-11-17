package tea

import (
	"testing"

	bubbletea "charm.land/bubbletea/v2"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

// Rust parity: codex-rs/tui/src/chatwidget/rate_limits.rs observe_quota_plan,
// chatwidget/settings.rs on_account_email_loaded / update_account_state, and
// chatwidget/thread_usage.rs refresh_billing_plan (#61977, #61978).

func billingPlanTestModel(t *testing.T, plan string) *Model {
	t.Helper()
	state := codextui.NewState(&codextui.Options{Model: "gpt-5.4"})
	state.SetThreadID("thread-billing")
	return NewModel(state, Options{
		Width:             100,
		Height:            30,
		HasChatGPTAccount: true,
		ChatGPTPlanType:   plan,
		OnReadThreadUsage: func(string) (ThreadUsageReadResult, error) { return ThreadUsageReadResult{}, nil },
	})
}

func applyAccountReadPlan(t *testing.T, model *Model, plan string) {
	t.Helper()
	model.accountEmailRequestID = "account-read"
	applied, _ := model.applyAccountEmailLoaded(AccountEmailLoadedMsg{
		RequestID: "account-read",
		Email:     "user@example.com",
		PlanType:  plan,
	})
	if !applied {
		t.Fatal("account read was not applied")
	}
}

func applyQuotaRead(model *Model, limitID, plan string) bubbletea.Cmd {
	return model.applyRateLimitSnapshotFromSource(
		chatwidget.RateLimitSnapshot{LimitID: limitID, PlanType: plan},
		rateLimitSnapshotAccountUsage,
		false,
	)
}

// TestBillingPlanPrefersKnownQuotaPlanInEitherReplyOrderLikeRust covers Rust's
// billing_prefers_a_known_quota_plan_in_either_reply_order: account/read can use
// a cached token, so a known quota plan wins no matter which reply lands last.
func TestBillingPlanPrefersKnownQuotaPlanInEitherReplyOrderLikeRust(t *testing.T) {
	cases := []struct {
		name    string
		account string
		quota   string
		want    string
	}{
		{name: "quota overrides account", account: "plus", quota: "business", want: "business"},
		{name: "account unknown", account: "", quota: "business", want: "business"},
		{name: "quota unknown is ignored", account: "business", quota: "unknown", want: "business"},
		{name: "quota missing is ignored", account: "business", quota: "", want: "business"},
	}
	for _, testCase := range cases {
		for _, quotaFirst := range []bool{true, false} {
			model := billingPlanTestModel(t, "")
			if quotaFirst {
				applyQuotaRead(model, "codex", testCase.quota)
				applyAccountReadPlan(t, model, testCase.account)
			} else {
				applyAccountReadPlan(t, model, testCase.account)
				applyQuotaRead(model, "codex", testCase.quota)
			}
			if model.chatGPTPlanType != testCase.want {
				t.Fatalf("%s (quotaFirst=%v): billing plan = %q, want %q", testCase.name, quotaFirst, model.chatGPTPlanType, testCase.want)
			}
		}
	}
}

// TestBillingPlanIgnoresNonCodexQuotaLimitsLikeRust covers Rust's limit_id gate:
// only the Codex limit may speak for the billing plan.
func TestBillingPlanIgnoresNonCodexQuotaLimitsLikeRust(t *testing.T) {
	model := billingPlanTestModel(t, "plus")
	applyQuotaRead(model, "other-limit", "business")
	if model.quotaPlanType != "" || model.chatGPTPlanType != "plus" {
		t.Fatalf("non-codex limit changed the billing plan: quota=%q plan=%q", model.quotaPlanType, model.chatGPTPlanType)
	}
}

// TestRollingQuotaCannotRestoreObsoleteBillingPlanLikeRust covers Rust's removal
// of plan_type assignment from rolling snapshots (#61978).
func TestRollingQuotaCannotRestoreObsoleteBillingPlanLikeRust(t *testing.T) {
	model := billingPlanTestModel(t, "")
	applyAccountReadPlan(t, model, "business")
	applyAccountReadPlan(t, model, "plus")
	if model.chatGPTPlanType != "plus" {
		t.Fatalf("account read plan = %q, want plus", model.chatGPTPlanType)
	}
	model.applyRateLimitSnapshot(chatwidget.RateLimitSnapshot{LimitID: "codex", PlanType: "business"})
	if model.chatGPTPlanType != "plus" {
		t.Fatalf("rolling snapshot restored an obsolete plan: %q", model.chatGPTPlanType)
	}
}

// TestAccountUpdateDropsQuotaPlanLikeRust covers Rust's update_account_state:
// the account plan is rebound and a quota plan from the previous account dies.
func TestAccountUpdateDropsQuotaPlanLikeRust(t *testing.T) {
	model := billingPlanTestModel(t, "plus")
	applyQuotaRead(model, "codex", "ent26")
	if model.chatGPTPlanType != "ent26" {
		t.Fatalf("quota plan = %q, want ent26", model.chatGPTPlanType)
	}
	model.Update(AccountStatusUpdatedMsg{
		Display:           "other@example.com (Plus)",
		HasChatGPTAccount: true,
		PlanType:          "plus",
	})
	if model.quotaPlanType != "" || model.chatGPTPlanType != "plus" {
		t.Fatalf("account update kept the old quota plan: quota=%q plan=%q", model.quotaPlanType, model.chatGPTPlanType)
	}
	model.applyRateLimitSnapshot(chatwidget.RateLimitSnapshot{LimitID: "codex", PlanType: "ent26"})
	if model.chatGPTPlanType != "plus" {
		t.Fatalf("rolling snapshot restored the dropped quota plan: %q", model.chatGPTPlanType)
	}
}

// TestBillingPlanClearsThreadUsageOnlyWhenUnavailableLikeRust covers Rust's
// refresh_billing_plan: a billable-to-billable change keeps the estimate.
func TestBillingPlanClearsThreadUsageOnlyWhenUnavailableLikeRust(t *testing.T) {
	model := billingPlanTestModel(t, "business")
	model.threadUsage = &ThreadUsageEstimate{ThreadID: "thread-billing", EstimatedUsageCreditsMicros: 5_000_000}
	applyQuotaRead(model, "codex", "ent26")
	if model.chatGPTPlanType != "ent26" {
		t.Fatalf("billing plan = %q, want ent26", model.chatGPTPlanType)
	}
	if model.threadUsage == nil {
		t.Fatal("billable-to-billable plan change dropped the estimate")
	}
	applyQuotaRead(model, "codex", "plus")
	if model.chatGPTPlanType != "plus" {
		t.Fatalf("billing plan = %q, want plus", model.chatGPTPlanType)
	}
	if model.threadUsage != nil {
		t.Fatalf("non-billable plan kept the estimate: %#v", model.threadUsage)
	}
}

// TestQuotaReadReArmsPendingStatusThreadUsageLikeRust covers Rust's
// request_thread_usage_for_status: a committed /status card that rendered while
// the account was not billable learns the corrected plan from its quota read.
func TestQuotaReadReArmsPendingStatusThreadUsageLikeRust(t *testing.T) {
	model := billingPlanTestModel(t, "plus")
	model.pendingStatusRateLimitRequests[1] = pendingStatusRateLimitRequest{messageIndex: 0, width: 80}
	command := applyQuotaRead(model, "codex", "business")
	if command == nil {
		t.Fatal("quota read did not request thread usage for the pending status card")
	}
	if model.threadUsagePendingRequestID == 0 {
		t.Fatal("quota read left the pending status card without a usage request")
	}
	if len(model.threadUsageStatusTargets) != 1 {
		t.Fatalf("status targets = %#v, want one", model.threadUsageStatusTargets)
	}
}
