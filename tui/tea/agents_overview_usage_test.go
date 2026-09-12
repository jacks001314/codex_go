package tea

import (
	"errors"
	"strings"
	"testing"
	"time"

	agentsoverview "codex_go/tui/agents_overview"
	chatwidget "codex_go/tui/chatwidget"
	"codex_go/utils"
)

func usageInt64Ptr(value int64) *int64 { return &value }

// newUsageDashboardModel opens the dashboard without a usage reader so the
// test can install one and drive the fetch explicitly.
func newUsageDashboardModel(t *testing.T, plan string) *Model {
	t.Helper()
	model := NewModel(nil, Options{
		Width:             120,
		Height:            24,
		HasChatGPTAccount: true,
		ChatGPTPlanType:   plan,
		OnAgentsOverviewRefresh: func(string) ([]agentsoverview.Row, error) {
			return agentsOverviewTestRows(), nil
		},
	})
	openAgentsDashboard(t, model)
	return model
}

func TestAgentsOverviewUsageFetchRendersGroupTotals(t *testing.T) {
	model := newUsageDashboardModel(t, "business")
	reads := 0
	model.onAgentsOverviewUsage = func(threadID string) (AgentsOverviewUsageResult, error) {
		reads++
		return AgentsOverviewUsageResult{
			Outcome: AgentsOverviewUsageAvailable,
			Usage: AgentsOverviewThreadUsage{
				ThreadID:               threadID,
				HasGroups:              true,
				GroupInputTokens:       usageInt64Ptr(12_000),
				GroupOutputTokens:      usageInt64Ptr(3_000),
				EstimatedCreditsMicros: 3_400_000,
				EstimatedUSDMicros:     usageInt64Ptr(140_000),
			},
		}, nil
	}
	command := model.refreshAgentsOverviewUsageCmd()
	if command == nil {
		t.Fatal("no usage fetch command")
	}
	model.Update(command())
	if reads != 1 {
		t.Fatalf("usage reads = %d, want 1", reads)
	}
	view := utils.StripANSI(model.View())
	for _, want := range []string{"Tokens: 12K in \u00b7 3K out", "Est. usage: 3.4 credits \u00b7 ~$0.14"} {
		if !strings.Contains(view, want) {
			t.Errorf("dashboard missing %q:\n%s", want, view)
		}
	}
	// A second refresh inside the cadence does not fetch again.
	command = model.refreshAgentsOverviewUsageCmd()
	if command == nil {
		t.Fatal("a fresh estimate must schedule the next check")
	}
	if reads != 1 {
		t.Fatalf("usage reads = %d, want 1 after a cadence check", reads)
	}
}

func TestAgentsOverviewUsagePreservesPriorNonzeroEstimate(t *testing.T) {
	model := newUsageDashboardModel(t, "business")
	model.agentsOverviewUsage["root-1"] = &agentsOverviewUsageEntry{
		estimate: &AgentsOverviewThreadUsage{
			ThreadID:               "root-1",
			HasGroups:              true,
			GroupInputTokens:       usageInt64Ptr(12_000),
			GroupOutputTokens:      usageInt64Ptr(3_000),
			EstimatedCreditsMicros: 3_400_000,
			EstimatedUSDMicros:     usageInt64Ptr(200_000),
		},
		fetchedAt: time.Now().Add(-2 * time.Minute),
	}
	model.agentsOverviewUsagePendingThread = "root-1"
	model.agentsOverviewUsagePendingRequest = 7
	model.applyAgentsOverviewUsageLoaded(agentsOverviewUsageLoadedMsg{
		threadID:  "root-1",
		requestID: 7,
		result: AgentsOverviewUsageResult{
			Outcome: AgentsOverviewUsageAvailable,
			Usage: AgentsOverviewThreadUsage{
				ThreadID:               "root-1",
				EstimatedCreditsMicros: 0,
				EstimatedUSDMicros:     usageInt64Ptr(200_000),
			},
		},
	})
	view := utils.StripANSI(model.View())
	for _, want := range []string{"Est. usage: 3.4 credits", "Tokens: 12K in \u00b7 3K out"} {
		if !strings.Contains(view, want) {
			t.Errorf("dashboard missing %q after a zero settlement:\n%s", want, view)
		}
	}
}

func TestAgentsOverviewUsageDisabledClearsEstimatesKeepsTokens(t *testing.T) {
	model := newUsageDashboardModel(t, "business")
	model.agentsOverviewUsage["root-1"] = &agentsOverviewUsageEntry{
		liveInput:  usageInt64Ptr(13_000),
		liveOutput: usageInt64Ptr(4_000),
		estimate: &AgentsOverviewThreadUsage{
			ThreadID:               "root-1",
			EstimatedCreditsMicros: 3_400_000,
		},
	}
	model.agentsOverviewUsagePendingThread = "root-1"
	model.agentsOverviewUsagePendingRequest = 9
	model.applyAgentsOverviewUsageLoaded(agentsOverviewUsageLoadedMsg{
		threadID:  "root-1",
		requestID: 9,
		result:    AgentsOverviewUsageResult{Outcome: AgentsOverviewUsageDisabled},
	})
	if !model.agentsOverviewUsageDisabled {
		t.Fatal("disabled outcome must latch the capability off")
	}
	entry := model.agentsOverviewUsage["root-1"]
	if entry.estimate != nil {
		t.Fatalf("estimate = %#v, want cleared on the disabled capability", entry.estimate)
	}
	view := utils.StripANSI(model.View())
	if !strings.Contains(view, "Tokens: 13K in \u00b7 4K out") {
		t.Errorf("live tokens must survive the disabled capability:\n%s", view)
	}
	if strings.Contains(view, "Est. usage:") {
		t.Errorf("estimate must not render after the disabled capability:\n%s", view)
	}
	// A different selection must not retry an unavailable account capability.
	if command := model.refreshAgentsOverviewUsageCmd(); command != nil {
		t.Fatal("the disabled capability must stop further fetches")
	}
}

func TestAgentsOverviewUsageGates(t *testing.T) {
	for _, plan := range []string{"", "free", "pro", "enterprise"} {
		model := newUsageDashboardModel(t, plan)
		model.onAgentsOverviewUsage = func(string) (AgentsOverviewUsageResult, error) {
			return AgentsOverviewUsageResult{}, errors.New("must not fetch")
		}
		if command := model.refreshAgentsOverviewUsageCmd(); command != nil {
			t.Errorf("plan %q must not fetch dashboard usage", plan)
		}
	}
	for _, plan := range []string{"business", "enterprise_cbp_usage_based", "enterprise_cbp_automation"} {
		model := newUsageDashboardModel(t, plan)
		model.onAgentsOverviewUsage = func(string) (AgentsOverviewUsageResult, error) {
			return AgentsOverviewUsageResult{}, nil
		}
		if command := model.refreshAgentsOverviewUsageCmd(); command == nil {
			t.Errorf("plan %q must fetch dashboard usage", plan)
		}
	}
}

func TestAgentsOverviewUsagePlanChangeClearsCache(t *testing.T) {
	model := newUsageDashboardModel(t, "business")
	model.agentsOverviewUsage["root-1"] = &agentsOverviewUsageEntry{estimate: &AgentsOverviewThreadUsage{ThreadID: "root-1"}}
	model.agentsOverviewUsageDisabled = true
	model.applyRateLimitSnapshot(chatwidget.RateLimitSnapshot{LimitID: "codex", PlanType: "free"})
	if len(model.agentsOverviewUsage) != 0 || model.agentsOverviewUsageDisabled {
		t.Fatalf("plan change must clear usage: %#v disabled=%v", model.agentsOverviewUsage, model.agentsOverviewUsageDisabled)
	}
}

// TestAgentsOverviewUsageLinesIncompleteGroups covers Rust's incomplete
// breakdown rule: only the complete side is summed.
func TestAgentsOverviewUsageLinesIncompleteGroups(t *testing.T) {
	entry := &agentsOverviewUsageEntry{estimate: &AgentsOverviewThreadUsage{
		GroupOutputTokens:      usageInt64Ptr(30),
		EstimatedCreditsMicros: 0,
	}}
	lines := agentsOverviewUsageLines(entry)
	if len(lines) != 2 || lines[0] != "Tokens: 30 out" || lines[1] != "Est. usage: 0 credits" {
		t.Fatalf("usage lines = %#v", lines)
	}
}
