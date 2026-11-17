package tea

import (
	"strings"

	bubbletea "charm.land/bubbletea/v2"

	"codex_go/tui/chatwidget"
)

// Rust parity: codex-rs/tui/src/chatwidget/rate_limits.rs observe_quota_plan and
// refresh_billing_plan (#61977, #61978).
//
// Two plan sources can disagree. The account read may hand back a cached
// subscription plan, and rolling quota updates can still carry the plan the
// thread started under. Thread usage and `/status` must bill with the plan that
// actually serves requests, so a full Codex quota read wins over the account
// plan, and rolling updates never change it.

// observeQuotaPlan records the plan confirmed by a full quota read. Only the
// Codex limit may speak for the billing plan, and an unknown plan is ignored so
// it cannot erase a known one.
func (m *Model) observeQuotaPlan(snapshot chatwidget.RateLimitSnapshot) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if limitID := strings.TrimSpace(snapshot.LimitID); limitID != "" && !strings.EqualFold(limitID, "codex") {
		return nil
	}
	plan := strings.TrimSpace(snapshot.PlanType)
	if plan == "" || strings.EqualFold(plan, "unknown") {
		return nil
	}
	if plan == m.quotaPlanType {
		return nil
	}
	m.quotaPlanType = plan
	return m.refreshBillingPlan()
}

// refreshBillingPlan recomputes the effective plan and drops the usage caches
// that were priced with the previous one.
func (m *Model) refreshBillingPlan() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	effective := m.quotaPlanType
	if effective == "" {
		effective = m.accountPlanType
	}
	if effective == m.chatGPTPlanType {
		return nil
	}
	previouslyAvailable := m.threadUsageAvailable()
	m.chatGPTPlanType = effective
	var command bubbletea.Cmd
	if !m.threadUsageAvailable() {
		m.clearThreadUsage()
	} else if !previouslyAvailable {
		// A pending /status can learn that the account is billable from its own
		// quota read (Rust refresh_billing_plan -> request_thread_usage_for_status).
		command = m.requestThreadUsageForPendingStatusCards()
	}
	// The agents overview caches rendered usage lines, so reprice them with the
	// plan that now serves requests (Rust re-renders with the current plan).
	m.syncAgentsOverviewUsageLines()
	return command
}

// setAccountPlan replaces the account-reported plan and drops any quota plan,
// which belonged to the previous account (Rust update_account_state).
func (m *Model) setAccountPlan(planType string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	m.accountPlanType = strings.TrimSpace(planType)
	m.quotaPlanType = ""
	return m.refreshBillingPlan()
}

// setAccountReadPlan records the plan from a fresh account/read. Unlike an
// account update this keeps the quota plan, so whatever read lands last a known
// quota plan still bills (Rust on_account_email_loaded, #61978).
func (m *Model) setAccountReadPlan(planType string) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	m.accountPlanType = strings.TrimSpace(planType)
	return m.refreshBillingPlan()
}

// resetBillingPlans drops both plan sources when the account identity changes.
func (m *Model) resetBillingPlans() bubbletea.Cmd {
	if m == nil {
		return nil
	}
	m.accountPlanType = ""
	m.quotaPlanType = ""
	return m.refreshBillingPlan()
}

// requestThreadUsageForPendingStatusCards re-arms the committed /status cards
// that rendered while the account was not billable.
func (m *Model) requestThreadUsageForPendingStatusCards() bubbletea.Cmd {
	if m == nil || len(m.pendingStatusRateLimitRequests) == 0 {
		return nil
	}
	var commands []bubbletea.Cmd
	for requestID, pending := range m.pendingStatusRateLimitRequests {
		pending.snapshot.StatusLabelReserves = m.threadUsageLabelReserves()
		m.pendingStatusRateLimitRequests[requestID] = pending
		if command, started := m.startThreadUsageRefresh(pending.messageIndex, pending.width); started && command != nil {
			commands = append(commands, command)
		}
	}
	return batchSideCommands(commands...)
}
