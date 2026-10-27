package tea

import (
	"math/big"
	"sort"
	"strings"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"

	codextui "codex_go/tui"
	bottompane "codex_go/tui/bottom_pane"
	chatwidget "codex_go/tui/chatwidget"
	tuistatus "codex_go/tui/status"
)

// Rust parity: codex-rs/tui/src/status/thread_usage.rs (#44970, #57251).

// ThreadUsageOutcome is the result of reading a thread's billing estimate.
type ThreadUsageOutcome int

const (
	ThreadUsageAvailable ThreadUsageOutcome = iota
	ThreadUsageDisabled
)

// ThreadUsageGroup is one model/speed/reasoning bucket in a thread estimate.
type ThreadUsageGroup struct {
	Model                       *string
	ReasoningEffort             *string
	Speed                       *string
	EstimatedUsageCreditsMicros int64
	CachedInputTokens           *int64
	InputTokens                 *int64
	OutputTokens                *int64
}

// ThreadUsageEstimate is the thread-scoped billing data rendered in /status.
type ThreadUsageEstimate struct {
	ThreadID                    string
	EstimatedUsageCreditsMicros int64
	EstimatedUsageUSDMicros     *int64
	NativeUsageUSDMicros        *int64
	Groups                      []ThreadUsageGroup
}

// ThreadUsageReadResult is the fetch outcome for the active thread.
type ThreadUsageReadResult struct {
	Outcome ThreadUsageOutcome
	Usage   ThreadUsageEstimate
}

// ThreadUsageReaderFunc reads the active thread's usage through the host.
type ThreadUsageReaderFunc func(threadID string) (ThreadUsageReadResult, error)

type threadUsageResultMsg struct {
	requestID uint64
	result    ThreadUsageReadResult
	err       error
}

type threadUsageRetryMsg struct {
	threadID string
}

type threadUsageSettlementMsg struct {
	threadID string
	attempt  int
}

type threadUsageStatusTarget struct {
	messageIndex int
	width        int
}

var threadUsageRetryDelays = []time.Duration{5 * time.Second, 15 * time.Second, 60 * time.Second}

// threadUsageSettlementDelays mirrors Rust THREAD_USAGE_SETTLEMENT_DELAYS:
// post-turn billing settles asynchronously, so the estimate is re-read after
// 15s, 60s, and 120s until it grows past the pre-turn baseline.
var threadUsageSettlementDelays = []time.Duration{15 * time.Second, 60 * time.Second, 120 * time.Second}

// threadUsagePlanAllowed mirrors Rust's plan gate for billing estimates.
func (m *Model) threadUsagePlanAllowed() bool {
	if m == nil {
		return false
	}
	switch strings.TrimSpace(m.chatGPTPlanType) {
	case "business", "ent26", "enterprise_cbp_usage_based", "enterprise_cbp_automation":
		return true
	default:
		return false
	}
}

// threadUsageAvailable mirrors ChatWidget::thread_usage_is_available.
func (m *Model) threadUsageAvailable() bool {
	if m == nil || m.State == nil || m.onReadThreadUsage == nil {
		return false
	}
	if !m.hasChatGPTAccount || !m.threadUsagePlanAllowed() || m.threadUsageDisabled {
		return false
	}
	return strings.TrimSpace(m.State.ThreadID) != ""
}

// threadUsageLabelReserves keeps the status label column stable while the
// asynchronous estimate is in flight (Rust StatusThreadUsage::push_labels).
func (m *Model) threadUsageLabelReserves() []string {
	if !m.threadUsageAvailable() {
		return nil
	}
	return []string{"  Billed tokens"}
}

// startThreadUsageRefresh starts one asynchronous read for the active thread.
// Existing estimates stay visible while the request is in flight.
func (m *Model) startThreadUsageRefresh(messageIndex, width int) (bubbletea.Cmd, bool) {
	if m == nil || m.State == nil || m.onReadThreadUsage == nil {
		return nil, false
	}
	if !m.threadUsageAvailable() {
		return nil, false
	}
	m.threadUsageStatusTargets = append(m.threadUsageStatusTargets, threadUsageStatusTarget{messageIndex: messageIndex, width: width})
	m.threadUsageRetryAttempts = 0
	if m.threadUsagePendingRequestID != 0 {
		return nil, true
	}
	return m.requestThreadUsage()
}

// ensureThreadUsageForSurfaces mirrors Rust
// StatusSurfaceSelections::uses_thread_usage -> ensure_thread_usage_requested:
// a configured thread-credits or estimated-thread-cost item on the status line
// or terminal title asks for the estimate, and clearing both stops polling.
func (m *Model) ensureThreadUsageForSurfaces(selections chatwidget.StatusSurfaceSelections) bubbletea.Cmd {
	if m == nil {
		return nil
	}
	if !selections.UsesThreadUsage() {
		m.cancelThreadUsagePolling()
		return nil
	}
	m.threadUsageSurfaceSelected = true
	return m.ensureThreadUsageRequested()
}

// currentThreadUsageEstimate returns the cached estimate only when it belongs
// to the visible thread, so a late response for a previous thread never leaks
// into the status line or terminal title.
func (m *Model) currentThreadUsageEstimate() *ThreadUsageEstimate {
	if m == nil || m.State == nil || m.threadUsage == nil {
		return nil
	}
	usageThreadID := strings.TrimSpace(m.threadUsage.ThreadID)
	currentThreadID := strings.TrimSpace(m.State.ThreadID)
	if usageThreadID != "" && currentThreadID != "" && !strings.EqualFold(usageThreadID, currentThreadID) {
		return nil
	}
	return m.threadUsage
}

func threadUsageCreditsMicros(usage *ThreadUsageEstimate) *int64 {
	if usage == nil {
		return nil
	}
	value := usage.EstimatedUsageCreditsMicros
	return &value
}

func threadUsageEstimatedUSDMicros(usage *ThreadUsageEstimate) *int64 {
	if usage == nil || usage.EstimatedUsageUSDMicros == nil {
		return nil
	}
	value := *usage.EstimatedUsageUSDMicros
	return &value
}

func threadUsageNativeUSDMicros(usage *ThreadUsageEstimate) *int64 {
	if usage == nil || usage.NativeUsageUSDMicros == nil {
		return nil
	}
	value := *usage.NativeUsageUSDMicros
	return &value
}

// ensureThreadUsageRequested starts at most one request per thread, matching
// Rust ChatWidget::ensure_thread_usage_requested.
func (m *Model) ensureThreadUsageRequested() bubbletea.Cmd {
	if m == nil || m.State == nil || m.onReadThreadUsage == nil {
		return nil
	}
	threadID := strings.TrimSpace(m.State.ThreadID)
	if threadID == "" || !m.threadUsageAvailable() {
		return nil
	}
	if m.threadUsage != nil && !strings.EqualFold(strings.TrimSpace(m.threadUsage.ThreadID), threadID) {
		m.clearThreadUsage()
	}
	if m.threadUsageRequested {
		return nil
	}
	cmd, ok := m.requestThreadUsage()
	if !ok {
		return nil
	}
	m.threadUsageRequested = true
	return cmd
}

// cancelThreadUsagePolling mirrors Rust cancel_thread_usage_polling: nothing
// polls once neither surface wants the estimate, but an outstanding /status
// card keeps its request alive.
func (m *Model) cancelThreadUsagePolling() {
	if m == nil {
		return
	}
	if len(m.threadUsageStatusTargets) > 0 {
		return
	}
	m.threadUsageRequested = false
	m.threadUsageSurfaceSelected = false
	m.threadUsageRetryAttempts = 0
	m.threadUsagePendingRequestID = 0
	m.clearThreadUsageSettlement()
}

// refreshThreadUsageAfterTurn mirrors Rust
// ChatWidget::refresh_thread_usage_after_turn: a completed turn changes the
// thread's cost, so the selected surfaces re-read the estimate and keep polling
// until the backend's billing has caught up.
func (m *Model) refreshThreadUsageAfterTurn() bubbletea.Cmd {
	if m == nil || m.State == nil || !m.threadUsageSurfaceSelected || !m.threadUsageAvailable() {
		return nil
	}
	m.threadUsageSettlementActive = true
	m.threadUsageSettlementAttempts = 0
	m.threadUsageSettlementBaselineCreditsMicros = 0
	m.threadUsageSettlementBaselineUSDMicros = nil
	if usage := m.currentThreadUsageEstimate(); usage != nil {
		m.threadUsageSettlementBaselineCreditsMicros = usage.EstimatedUsageCreditsMicros
		m.threadUsageSettlementBaselineUSDMicros = tuistatus.ThreadCostMicros(usage.NativeUsageUSDMicros, usage.EstimatedUsageUSDMicros, m.chatGPTPlanType)
	}
	return bubbletea.Batch(m.requestThreadUsageForSettlement(), m.scheduleThreadUsageSettlement())
}

func (m *Model) requestThreadUsageForSettlement() bubbletea.Cmd {
	if m == nil || m.threadUsagePendingRequestID != 0 || !m.threadUsageAvailable() {
		return nil
	}
	cmd, ok := m.requestThreadUsage()
	if !ok {
		return nil
	}
	m.threadUsageRequested = true
	return cmd
}

// applyThreadUsageSettlement re-reads the estimate when a settlement delay
// elapses. A request that is already in flight finishes the step instead.
func (m *Model) applyThreadUsageSettlement(message threadUsageSettlementMsg) bubbletea.Cmd {
	if m == nil || m.State == nil || !m.threadUsageSettlementActive {
		return nil
	}
	if strings.TrimSpace(message.threadID) != strings.TrimSpace(m.State.ThreadID) {
		return nil
	}
	if !m.threadUsageSurfaceSelected || !m.threadUsageAvailable() {
		m.clearThreadUsageSettlement()
		return nil
	}
	if m.threadUsagePendingRequestID != 0 {
		return nil
	}
	return m.requestThreadUsageForSettlement()
}

// finishThreadUsageSettlement clears the baseline once both selected amounts
// grew past it, or schedules the next settlement delay otherwise.
func (m *Model) finishThreadUsageSettlement() bubbletea.Cmd {
	if m == nil || !m.threadUsageSettlementActive {
		return nil
	}
	usage := m.currentThreadUsageEstimate()
	if usage == nil {
		m.clearThreadUsageSettlement()
		return nil
	}
	creditsSelected, costSelected := m.threadUsageSelection()
	creditsSettled := !creditsSelected || usage.EstimatedUsageCreditsMicros > m.threadUsageSettlementBaselineCreditsMicros
	costSettled := true
	if costSelected {
		current := tuistatus.ThreadCostMicros(usage.NativeUsageUSDMicros, usage.EstimatedUsageUSDMicros, m.chatGPTPlanType)
		if current == nil {
			costSettled = false
		} else if m.threadUsageSettlementBaselineUSDMicros == nil {
			costSettled = *current > 0
		} else {
			costSettled = *current > *m.threadUsageSettlementBaselineUSDMicros
		}
	}
	if creditsSettled && costSettled {
		m.clearThreadUsageSettlement()
		return nil
	}
	return m.scheduleThreadUsageSettlement()
}

func (m *Model) scheduleThreadUsageSettlement() bubbletea.Cmd {
	if m == nil || m.State == nil || !m.threadUsageSettlementActive {
		return nil
	}
	if m.threadUsageSettlementAttempts >= len(threadUsageSettlementDelays) {
		m.clearThreadUsageSettlement()
		return nil
	}
	threadID := strings.TrimSpace(m.State.ThreadID)
	if threadID == "" {
		return nil
	}
	delay := threadUsageSettlementDelays[m.threadUsageSettlementAttempts]
	m.threadUsageSettlementAttempts++
	attempt := m.threadUsageSettlementAttempts
	return bubbletea.Tick(delay, func(time.Time) bubbletea.Msg {
		return threadUsageSettlementMsg{threadID: threadID, attempt: attempt}
	})
}

func (m *Model) clearThreadUsageSettlement() {
	if m == nil {
		return
	}
	m.threadUsageSettlementActive = false
	m.threadUsageSettlementAttempts = 0
	m.threadUsageSettlementBaselineCreditsMicros = 0
	m.threadUsageSettlementBaselineUSDMicros = nil
}

// threadUsageSelection reports which billing items the configured surfaces
// currently show. ENT26 never shows credits (Rust selected_thread_usage).
func (m *Model) threadUsageSelection() (credits bool, cost bool) {
	if m == nil || m.statusControls == nil {
		return false, false
	}
	selections := chatwidget.NewStatusSurfaceSelections(m.statusControls.ConfiguredStatusLineItems(), m.statusControls.ConfiguredTerminalTitleItems())
	for _, item := range selections.StatusLineItems {
		switch item {
		case bottompane.StatusLineThreadCredits:
			credits = true
		case bottompane.StatusLineEstimatedThreadCost:
			cost = true
		}
	}
	for _, item := range selections.TerminalTitleItems {
		switch item {
		case chatwidget.TerminalTitleThreadCredits:
			credits = true
		case chatwidget.TerminalTitleEstimatedThreadCost:
			cost = true
		}
	}
	if strings.EqualFold(strings.TrimSpace(m.chatGPTPlanType), tuistatus.PlanTypeEnt26) {
		credits = false
	}
	return credits, cost
}

func (m *Model) requestThreadUsage() (bubbletea.Cmd, bool) {
	if m == nil || m.State == nil || m.threadUsagePendingRequestID != 0 || !m.threadUsageAvailable() {
		return nil, false
	}
	threadID := strings.TrimSpace(m.State.ThreadID)
	if threadID == "" {
		return nil, false
	}
	m.threadUsageNextRequestID++
	if m.threadUsageNextRequestID == 0 {
		m.threadUsageNextRequestID = 1
	}
	requestID := m.threadUsageNextRequestID
	m.threadUsagePendingRequestID = requestID
	reader := m.onReadThreadUsage
	return func() bubbletea.Msg {
		result, err := reader(threadID)
		return threadUsageResultMsg{requestID: requestID, result: result, err: err}
	}, true
}

func (m *Model) applyThreadUsageRetry(message threadUsageRetryMsg) bubbletea.Cmd {
	if m == nil || m.State == nil || strings.TrimSpace(message.threadID) != strings.TrimSpace(m.State.ThreadID) {
		return nil
	}
	if m.threadUsagePendingRequestID != 0 || !m.threadUsageAvailable() {
		return nil
	}
	if len(m.threadUsageStatusTargets) == 0 && m.threadUsage == nil && !m.threadUsageSurfaceSelected {
		return nil
	}
	cmd, _ := m.requestThreadUsage()
	return cmd
}

// applyThreadUsageResult stores the estimate and refreshes the committed card.
func (m *Model) applyThreadUsageResult(message threadUsageResultMsg) bubbletea.Cmd {
	if m == nil || m.State == nil || message.requestID == 0 || message.requestID != m.threadUsagePendingRequestID {
		return nil
	}
	m.threadUsagePendingRequestID = 0
	targets := m.threadUsageStatusTargets
	m.threadUsageStatusTargets = nil
	if message.err != nil {
		m.threadUsageStatusTargets = targets
		return m.scheduleThreadUsageRetry()
	}
	switch message.result.Outcome {
	case ThreadUsageDisabled:
		m.threadUsage = nil
		m.threadUsageDisabled = true
		m.threadUsageRetryAttempts = 0
		m.threadUsageRequested = false
	case ThreadUsageAvailable:
		usage := message.result.Usage
		usage.ThreadID = strings.TrimSpace(usage.ThreadID)
		if usage.ThreadID == "" {
			usage.ThreadID = strings.TrimSpace(m.State.ThreadID)
		}
		if current := strings.TrimSpace(m.State.ThreadID); current != "" && !strings.EqualFold(usage.ThreadID, current) {
			m.threadUsageStatusTargets = targets
			return m.scheduleThreadUsageRetry()
		}
		stabilizeThreadUsageEstimate(&usage, m.threadUsage)
		m.threadUsage = &usage
		m.threadUsageRetryAttempts = 0
	}
	fields := m.threadUsageStatusFields()
	m.State.StatusExtraFields = fields
	m.State.StatusLabelReserves = m.threadUsageLabelReserves()
	for requestID, pending := range m.pendingStatusRateLimitRequests {
		pending.snapshot.StatusExtraFields = fields
		pending.snapshot.StatusLabelReserves = m.State.StatusLabelReserves
		m.pendingStatusRateLimitRequests[requestID] = pending
	}
	for _, target := range targets {
		if target.messageIndex < 0 || target.messageIndex >= len(m.State.Messages) {
			continue
		}
		snapshot := *m.State
		snapshot.Messages = nil
		snapshot.RateLimits = append([]codextui.RateLimitStatus(nil), m.State.RateLimits...)
		snapshot.RateLimitsRefreshing = len(m.pendingStatusRateLimitRequests) > 0
		card := snapshot.RenderStatusCardWidth(target.width)
		history := statusHistoryText(card)
		m.State.Messages[target.messageIndex] = codextui.Message{Role: codextui.RoleHistory, Text: history, RawText: history}
		m.State.BumpMessagesRevision()
		if m.statusCopyTargets != nil {
			m.statusCopyTargets.withSnapshot(snapshot)
		}
	}
	if len(targets) > 0 {
		m.refreshTranscript()
	}
	// The configured status line and terminal title read the estimate from the
	// refreshed runtime snapshot; the title also needs an OSC update right away.
	m.ensureStatusControls()
	return bubbletea.Batch(m.applyTerminalTitleResult(m.refreshStatusControls()), m.finishThreadUsageSettlement())
}

func (m *Model) scheduleThreadUsageRetry() bubbletea.Cmd {
	if m == nil || m.State == nil {
		return nil
	}
	if len(m.threadUsageStatusTargets) == 0 && !m.threadUsageSurfaceSelected {
		return nil
	}
	if m.threadUsageRetryAttempts >= len(threadUsageRetryDelays) {
		return nil
	}
	threadID := strings.TrimSpace(m.State.ThreadID)
	if threadID == "" {
		return nil
	}
	delay := threadUsageRetryDelays[m.threadUsageRetryAttempts]
	m.threadUsageRetryAttempts++
	return bubbletea.Tick(delay, func(time.Time) bubbletea.Msg {
		return threadUsageRetryMsg{threadID: threadID}
	})
}

// clearThreadUsage drops the cached estimate after an account/plan change.
func (m *Model) clearThreadUsage() {
	if m == nil {
		return
	}
	m.threadUsage = nil
	m.threadUsageDisabled = false
	m.threadUsagePendingRequestID = 0
	m.threadUsageStatusTargets = nil
	m.threadUsageRetryAttempts = 0
	m.threadUsageRequested = false
	m.threadUsageSurfaceSelected = false
	m.clearThreadUsageSettlement()
	if m.State != nil {
		m.State.StatusExtraFields = nil
		m.State.StatusLabelReserves = nil
	}
}

// stabilizeThreadUsageEstimate keeps a previously positive estimate visible
// when the backend briefly reports zero before billing settles (Rust
// finish_thread_usage_refresh).
func stabilizeThreadUsageEstimate(usage *ThreadUsageEstimate, previous *ThreadUsageEstimate) {
	if usage == nil || previous == nil {
		return
	}
	transientZeroCredits := usage.EstimatedUsageCreditsMicros == 0 && previous.EstimatedUsageCreditsMicros > 0
	if transientZeroCredits {
		usage.EstimatedUsageCreditsMicros = previous.EstimatedUsageCreditsMicros
	}
	transientZeroCost := usage.EstimatedUsageUSDMicros != nil && *usage.EstimatedUsageUSDMicros == 0 &&
		previous.EstimatedUsageUSDMicros != nil && *previous.EstimatedUsageUSDMicros > 0
	if transientZeroCost {
		value := *previous.EstimatedUsageUSDMicros
		usage.EstimatedUsageUSDMicros = &value
	}
	if (transientZeroCredits || transientZeroCost) && len(usage.Groups) == 0 && len(previous.Groups) > 0 {
		usage.Groups = append([]ThreadUsageGroup(nil), previous.Groups...)
	}
}

func (m *Model) threadUsageStatusFields() []codextui.StatusExtraField {
	if m == nil || m.threadUsage == nil {
		return nil
	}
	usage := m.threadUsage
	if usage.ThreadID != "" && m.State != nil && m.State.ThreadID != "" && !strings.EqualFold(usage.ThreadID, m.State.ThreadID) {
		return nil
	}
	planType := ""
	if m != nil {
		planType = m.chatGPTPlanType
	}
	if strings.EqualFold(strings.TrimSpace(planType), tuistatus.PlanTypeEnt26) {
		cost := tuistatus.ThreadCostMicros(usage.NativeUsageUSDMicros, usage.EstimatedUsageUSDMicros, planType)
		if cost == nil {
			return nil
		}
		text, ok := tuistatus.FormatEstimatedUSDMicros(*cost)
		if !ok {
			return nil
		}
		return []codextui.StatusExtraField{{Label: "Thread usage", Value: text, SeparatorBefore: true}}
	}

	values := []string{tuistatus.FormatCreditMicros(usage.EstimatedUsageCreditsMicros) + " credits"}
	if usage.EstimatedUsageUSDMicros != nil {
		if cost, ok := tuistatus.FormatEstimatedUSDMicros(*usage.EstimatedUsageUSDMicros); ok {
			values = append(values, cost)
		}
	}
	fields := []codextui.StatusExtraField{{Label: "Thread usage", Value: strings.Join(values, " \u00b7 "), SeparatorBefore: true}}
	if len(usage.Groups) == 0 {
		return fields
	}
	if value := groupedThreadUsage(usage.Groups, threadUsageDimensionModel); value != "" {
		fields = append(fields, codextui.StatusExtraField{Label: "  Models", Value: value})
	}
	if value := groupedThreadUsage(usage.Groups, threadUsageDimensionReasoning); value != "" {
		fields = append(fields, codextui.StatusExtraField{Label: "  Reasoning", Value: value})
	}
	if value := groupedThreadUsage(usage.Groups, threadUsageDimensionSpeed); value != "" {
		fields = append(fields, codextui.StatusExtraField{Label: "  Speed", Value: value})
	}
	if value, ok := billedTokensText(usage.Groups); ok {
		fields = append(fields, codextui.StatusExtraField{Label: "  Billed tokens", Value: value})
	}
	return fields
}

type threadUsageDimension int

const (
	threadUsageDimensionModel threadUsageDimension = iota
	threadUsageDimensionReasoning
	threadUsageDimensionSpeed
)

var threadUsageReasoningOrder = []string{"None", "Minimal", "Light", "Medium", "High", "Extra High", "Max", "Ultra"}
var threadUsageSpeedOrder = []string{"Fast mode", "Ultrafast", "Standard"}

func groupedThreadUsage(groups []ThreadUsageGroup, dimension threadUsageDimension) string {
	totals := map[string]int64{}
	for _, group := range groups {
		if group.EstimatedUsageCreditsMicros < 0 {
			continue
		}
		var value string
		switch dimension {
		case threadUsageDimensionModel:
			if group.Model != nil {
				value = formatThreadUsageModelName(*group.Model)
			} else {
				value = "Other"
			}
		case threadUsageDimensionReasoning:
			value = reasoningDisplayName(group.ReasoningEffort)
		case threadUsageDimensionSpeed:
			value = speedDisplayName(group.Speed)
		}
		totals[value] = saturatingAddThreadUsage(totals[value], group.EstimatedUsageCreditsMicros)
	}
	if len(totals) == 0 {
		return ""
	}
	totalCredits := int64(0)
	for _, credits := range totals {
		totalCredits = saturatingAddThreadUsage(totalCredits, credits)
	}
	type entry struct {
		name    string
		credits int64
	}
	entries := make([]entry, 0, len(totals))
	for name, credits := range totals {
		entries = append(entries, entry{name: name, credits: credits})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		left, right := entries[i], entries[j]
		switch dimension {
		case threadUsageDimensionModel:
			if left.credits != right.credits {
				return left.credits > right.credits
			}
		case threadUsageDimensionReasoning:
			if rank := reasoningRank(left.name) - reasoningRank(right.name); rank != 0 {
				return rank < 0
			}
		case threadUsageDimensionSpeed:
			if rank := speedRank(left.name) - speedRank(right.name); rank != 0 {
				return rank < 0
			}
		}
		return left.name < right.name
	})
	parts := make([]string, 0, len(entries))
	for _, item := range entries {
		percentage := threadUsagePercentage(item.credits, totalCredits)
		parts = append(parts, item.name+" "+percentage+"%")
	}
	return strings.Join(parts, ", ")
}

func reasoningDisplayName(value *string) string {
	if value == nil {
		return "Other"
	}
	switch *value {
	case "none":
		return "None"
	case "minimal":
		return "Minimal"
	case "low":
		return "Light"
	case "medium":
		return "Medium"
	case "high":
		return "High"
	case "xhigh":
		return "Extra High"
	case "max":
		return "Max"
	case "ultra":
		return "Ultra"
	default:
		return *value
	}
}

func speedDisplayName(value *string) string {
	if value == nil {
		return "Other"
	}
	switch *value {
	case "fast":
		return "Fast mode"
	case "ultrafast":
		return "Ultrafast"
	case "standard":
		return "Standard"
	default:
		return *value
	}
}

func reasoningRank(name string) int {
	for i, candidate := range threadUsageReasoningOrder {
		if candidate == name {
			return i
		}
	}
	return len(threadUsageReasoningOrder)
}

func speedRank(name string) int {
	for i, candidate := range threadUsageSpeedOrder {
		if candidate == name {
			return i
		}
	}
	return len(threadUsageSpeedOrder)
}

// threadUsagePercentage rounds half up, matching Rust's integer arithmetic.
func threadUsagePercentage(credits, total int64) string {
	if total <= 0 {
		return "0"
	}
	value := new(big.Int).Mul(big.NewInt(credits), big.NewInt(100))
	value.Add(value, big.NewInt(total/2))
	value.Quo(value, big.NewInt(total))
	return value.String()
}

func formatThreadUsageModelName(model string) string {
	model = strings.TrimSpace(model)
	if strings.EqualFold(model, "gpt-reserve") {
		return "Luna Reserve"
	}
	prefix, remainder, found := strings.Cut(model, "-")
	if !found || !strings.EqualFold(prefix, "gpt") || remainder == "" {
		return model
	}
	segments := strings.Split(remainder, "-")
	first := segments[0]
	display := "GPT-" + first
	numeric := first != "" && first[0] >= '0' && first[0] <= '9'
	for _, segment := range segments[1:] {
		if numeric {
			display += " "
		} else {
			display += "-"
		}
		if strings.EqualFold(segment, "oai") {
			display += "OAI"
			continue
		}
		if segment == "" {
			continue
		}
		display += strings.ToUpper(segment[:1]) + segment[1:]
	}
	return display
}

func billedTokensText(groups []ThreadUsageGroup) (string, bool) {
	input, inputOK := int64(0), true
	cached, cachedOK := int64(0), true
	output, outputOK := int64(0), true
	for _, group := range groups {
		if group.InputTokens == nil {
			inputOK = false
		} else {
			input = saturatingAddThreadUsage(input, *group.InputTokens)
		}
		if group.CachedInputTokens == nil {
			cachedOK = false
		} else {
			cached = saturatingAddThreadUsage(cached, *group.CachedInputTokens)
		}
		if group.OutputTokens == nil {
			outputOK = false
		} else {
			output = saturatingAddThreadUsage(output, *group.OutputTokens)
		}
	}
	if len(groups) == 0 || (!inputOK && !outputOK) {
		return "", false
	}
	parts := []string{}
	if inputOK {
		part := tuistatus.FormatTokensCompact(input) + " input"
		if cachedOK {
			part += " (" + tuistatus.FormatTokensCompact(cached) + " cached)"
		}
		parts = append(parts, part)
	}
	if outputOK {
		part := tuistatus.FormatTokensCompact(output) + " output"
		if len(parts) > 0 {
			part = "+ " + part
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, " "), true
}

func saturatingAddThreadUsage(left, right int64) int64 {
	sum := left + right
	if right > 0 && sum < left {
		return int64(^uint64(0) >> 1)
	}
	if right < 0 && sum > left {
		return -int64(^uint64(0)>>1) - 1
	}
	return sum
}
