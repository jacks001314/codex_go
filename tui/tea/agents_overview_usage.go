package tea

import (
	"strings"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/protocol"
	agentsoverview "codex_go/tui/agents_overview"
	tuistatus "codex_go/tui/status"
)

// Rust parity: codex-rs/tui/src/app/agents_overview_usage.rs (#44970).
// Only the selected task is fetched, on a one-minute cadence. Account changes,
// reconnects, and missed server events clear the cache and the disabled
// capability state.
const agentsOverviewUsageRefreshInterval = 60 * time.Second

// AgentsOverviewUsageOutcome mirrors Rust's ThreadUsageOutcome.
type AgentsOverviewUsageOutcome int

const (
	// AgentsOverviewUsageAvailable carries an estimate for the task.
	AgentsOverviewUsageAvailable AgentsOverviewUsageOutcome = iota
	// AgentsOverviewUsageDisabled means the account cannot use usage estimates.
	AgentsOverviewUsageDisabled
)

// AgentsOverviewThreadUsage is the usage estimate and complete breakdown totals
// for one task (Rust agents_overview_usage::usage_lines inputs).
type AgentsOverviewThreadUsage struct {
	ThreadID string
	// HasGroups reports whether the estimate carried a breakdown. Rust's
	// nonzero-preservation rule only copies the previous groups when the new
	// response has none.
	HasGroups bool
	// GroupInputTokens / GroupOutputTokens are the complete totals summed over
	// the breakdown groups; nil when the breakdown is empty or incomplete.
	GroupInputTokens       *int64
	GroupOutputTokens      *int64
	EstimatedCreditsMicros int64
	EstimatedUSDMicros     *int64
}

// AgentsOverviewUsageResult is the fetch outcome for one task.
type AgentsOverviewUsageResult struct {
	Outcome AgentsOverviewUsageOutcome
	Usage   AgentsOverviewThreadUsage
}

// AgentsOverviewUsageReaderFunc reads one task's usage estimate through the
// host (the remote TUI asks the app server's account/usage/read).
type AgentsOverviewUsageReaderFunc func(threadID string) (AgentsOverviewUsageResult, error)

type agentsOverviewUsageEntry struct {
	liveInput  *int64
	liveOutput *int64
	estimate   *AgentsOverviewThreadUsage
	fetchedAt  time.Time
}

type agentsOverviewUsageLoadedMsg struct {
	threadID  string
	requestID uint64
	result    AgentsOverviewUsageResult
	err       error
}

// agentsOverviewUsageRefreshMsg re-runs the lazy usage check on the cadence.
type agentsOverviewUsageRefreshMsg struct{}

// agentsOverviewUsagePlanAllowed mirrors Rust's plan gate: only Business and
// the Enterprise usage-based/automation plans fetch estimates.
func (m *Model) agentsOverviewUsagePlanAllowed() bool {
	if m == nil {
		return false
	}
	switch strings.TrimSpace(m.chatGPTPlanType) {
	case "business", "enterprise_cbp_usage_based", "enterprise_cbp_automation":
		return true
	default:
		return false
	}
}

// refreshAgentsOverviewUsageCmd starts a usage fetch for the selected task when
// its cached value is missing or stale, scheduling the next check otherwise
// (Rust refresh_agents_overview_usage).
func (m *Model) refreshAgentsOverviewUsageCmd() bubbletea.Cmd {
	if m == nil || m.agentsOverview == nil || m.agentsOverviewEmbedded {
		return nil
	}
	if m.onAgentsOverviewUsage == nil || !m.hasChatGPTAccount || m.agentsOverviewUsageDisabled {
		return nil
	}
	if !m.agentsOverviewUsagePlanAllowed() || m.agentsOverviewUsagePendingThread != "" {
		return nil
	}
	threadID := m.agentsOverview.SelectedThreadID()
	if threadID == "" {
		return nil
	}
	if entry := m.agentsOverviewUsage[threadID]; entry != nil && !entry.fetchedAt.IsZero() {
		if age := m.currentTime().Sub(entry.fetchedAt); age < agentsOverviewUsageRefreshInterval {
			return m.scheduleAgentsOverviewUsageRefresh(agentsOverviewUsageRefreshInterval - age)
		}
	}
	m.agentsOverviewUsageNextRequest++
	if m.agentsOverviewUsageNextRequest == 0 {
		m.agentsOverviewUsageNextRequest = 1
	}
	requestID := m.agentsOverviewUsageNextRequest
	m.agentsOverviewUsagePendingThread = threadID
	m.agentsOverviewUsagePendingRequest = requestID
	reader := m.onAgentsOverviewUsage
	return func() bubbletea.Msg {
		result, err := reader(threadID)
		return agentsOverviewUsageLoadedMsg{threadID: threadID, requestID: requestID, result: result, err: err}
	}
}

func (m *Model) scheduleAgentsOverviewUsageRefresh(delay time.Duration) bubbletea.Cmd {
	if m == nil || delay <= 0 {
		return nil
	}
	return bubbletea.Tick(delay, func(time.Time) bubbletea.Msg {
		return agentsOverviewUsageRefreshMsg{}
	})
}

// applyAgentsOverviewUsageLoaded stores a fetched estimate, preserving a prior
// nonzero credits/cost value when a settlement reports zero (Rust
// finish_agents_overview_usage).
func (m *Model) applyAgentsOverviewUsageLoaded(msg agentsOverviewUsageLoadedMsg) bubbletea.Cmd {
	if m == nil || m.agentsOverview == nil {
		return nil
	}
	if m.agentsOverviewUsagePendingThread != msg.threadID || m.agentsOverviewUsagePendingRequest != msg.requestID {
		return nil
	}
	m.agentsOverviewUsagePendingThread = ""
	m.agentsOverviewUsagePendingRequest = 0
	if !agentsOverviewThreadListed(m.agentsOverview.Rows, msg.threadID) {
		return nil
	}
	entry := m.agentsOverviewUsageEntry(msg.threadID)
	entry.fetchedAt = m.currentTime()
	if msg.err == nil {
		switch msg.result.Outcome {
		case AgentsOverviewUsageAvailable:
			estimate := msg.result.Usage
			if strings.TrimSpace(estimate.ThreadID) == msg.threadID {
				if previous := entry.estimate; previous != nil {
					zeroCredits := estimate.EstimatedCreditsMicros == 0 && previous.EstimatedCreditsMicros > 0
					zeroCost := estimate.EstimatedUSDMicros != nil && *estimate.EstimatedUSDMicros == 0 &&
						previous.EstimatedUSDMicros != nil && *previous.EstimatedUSDMicros > 0
					if zeroCredits {
						estimate.EstimatedCreditsMicros = previous.EstimatedCreditsMicros
					}
					if zeroCost {
						estimate.EstimatedUSDMicros = cloneInt64PtrTea(previous.EstimatedUSDMicros)
					}
					if (zeroCredits || zeroCost) && !estimate.HasGroups {
						estimate.HasGroups = previous.HasGroups
						estimate.GroupInputTokens = cloneInt64PtrTea(previous.GroupInputTokens)
						estimate.GroupOutputTokens = cloneInt64PtrTea(previous.GroupOutputTokens)
					}
				}
				copied := estimate
				entry.estimate = &copied
			}
		case AgentsOverviewUsageDisabled:
			m.agentsOverviewUsageDisabled = true
			for _, cached := range m.agentsOverviewUsage {
				cached.estimate = nil
			}
		}
	}
	m.syncAgentsOverviewUsageLines()
	m.refreshTranscript()
	if !m.agentsOverviewUsageDisabled {
		return m.scheduleAgentsOverviewUsageRefresh(agentsOverviewUsageRefreshInterval)
	}
	return nil
}

// applyAgentsOverviewUsageTokens records a live token total for a task
// (Rust ThreadTokenUsageUpdated tracking).
func (m *Model) applyAgentsOverviewUsageTokens(threadID string, input *int64, output *int64) {
	if m == nil || m.agentsOverview == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	entry := m.agentsOverviewUsageEntry(threadID)
	entry.liveInput = cloneInt64PtrTea(input)
	entry.liveOutput = cloneInt64PtrTea(output)
	m.syncAgentsOverviewUsageLines()
}

// clearAgentsOverviewUsageTokens drops a task's live token total without
// discarding its estimate (Rust ThreadReverted/ThreadClosed).
func (m *Model) clearAgentsOverviewUsageTokens(threadID string) {
	if m == nil || m.agentsOverviewUsage == nil {
		return
	}
	if entry := m.agentsOverviewUsage[strings.TrimSpace(threadID)]; entry != nil {
		entry.liveInput = nil
		entry.liveOutput = nil
		m.syncAgentsOverviewUsageLines()
	}
}

func (m *Model) dropAgentsOverviewUsage(threadID string) {
	if m == nil || m.agentsOverviewUsage == nil {
		return
	}
	delete(m.agentsOverviewUsage, strings.TrimSpace(threadID))
}

// clearAgentsOverviewUsage drops every cached estimate and the disabled flag
// (Rust account change / reconnect / lagged event).
func (m *Model) clearAgentsOverviewUsage() {
	if m == nil {
		return
	}
	m.agentsOverviewUsage = map[string]*agentsOverviewUsageEntry{}
	m.agentsOverviewUsageDisabled = false
	m.agentsOverviewUsagePendingThread = ""
	m.agentsOverviewUsagePendingRequest = 0
	m.syncAgentsOverviewUsageLines()
}

func (m *Model) agentsOverviewUsageEntry(threadID string) *agentsOverviewUsageEntry {
	if m.agentsOverviewUsage == nil {
		m.agentsOverviewUsage = map[string]*agentsOverviewUsageEntry{}
	}
	threadID = strings.TrimSpace(threadID)
	entry := m.agentsOverviewUsage[threadID]
	if entry == nil {
		entry = &agentsOverviewUsageEntry{}
		m.agentsOverviewUsage[threadID] = entry
	}
	return entry
}

// syncAgentsOverviewUsageLines recomputes the rendered usage lines for every
// listed task.
func (m *Model) syncAgentsOverviewUsageLines() {
	if m == nil || m.agentsOverview == nil {
		return
	}
	for _, row := range m.agentsOverview.Rows {
		threadID := strings.TrimSpace(row.ThreadID)
		if threadID == "" {
			continue
		}
		m.agentsOverview.SetUsageLines(threadID, agentsOverviewUsageLines(m.agentsOverviewUsage[threadID]))
	}
}

// agentsOverviewUsageLines renders the "Tokens:" and "Est. usage:" lines,
// preferring the live totals and falling back to the complete breakdown
// (Rust agents_overview_usage::usage_lines).
func agentsOverviewUsageLines(entry *agentsOverviewUsageEntry) []string {
	if entry == nil {
		return nil
	}
	input := entry.liveInput
	if input == nil {
		input = entry.estimateGroupInput()
	}
	output := entry.liveOutput
	if output == nil {
		output = entry.estimateGroupOutput()
	}
	var lines []string
	var tokens []string
	if input != nil {
		tokens = append(tokens, tuistatus.FormatTokensCompact(*input)+" in")
	}
	if output != nil {
		tokens = append(tokens, tuistatus.FormatTokensCompact(*output)+" out")
	}
	if len(tokens) > 0 {
		lines = append(lines, "Tokens: "+strings.Join(tokens, " \u00b7 "))
	}
	if estimate := entry.estimate; estimate != nil {
		var values []string
		if estimate.EstimatedCreditsMicros >= 0 {
			values = append(values, tuistatus.FormatCreditMicros(estimate.EstimatedCreditsMicros)+" credits")
		}
		if estimate.EstimatedUSDMicros != nil {
			if cost, ok := tuistatus.FormatEstimatedUSDMicros(*estimate.EstimatedUSDMicros); ok {
				values = append(values, cost)
			}
		}
		if len(values) > 0 {
			lines = append(lines, "Est. usage: "+strings.Join(values, " \u00b7 "))
		}
	}
	return lines
}

func (e *agentsOverviewUsageEntry) estimateGroupInput() *int64 {
	if e == nil || e.estimate == nil {
		return nil
	}
	return e.estimate.GroupInputTokens
}

func (e *agentsOverviewUsageEntry) estimateGroupOutput() *int64 {
	if e == nil || e.estimate == nil {
		return nil
	}
	return e.estimate.GroupOutputTokens
}

func agentsOverviewThreadListed(rows []agentsoverview.Row, threadID string) bool {
	threadID = strings.TrimSpace(threadID)
	for _, row := range rows {
		if strings.TrimSpace(row.ThreadID) == threadID {
			return true
		}
	}
	return false
}

// agentsOverviewUsageTokenTotals extracts the live total token counts from a
// thread token-usage event (Rust uses the notification's total breakdown).
func agentsOverviewUsageTokenTotals(usage *protocol.ThreadTokenUsage) (*int64, *int64) {
	if usage == nil {
		return nil, nil
	}
	input := usage.Total.InputTokens
	output := usage.Total.OutputTokens
	return &input, &output
}
