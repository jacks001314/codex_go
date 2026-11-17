package tea

// Rust parity: codex-rs/tui/src/chatwidget/statusline_state.rs (#61979).
//
// The saved MCP provider receives one JSON snapshot of the displayed
// conversation in the `com.openai/codex-statusline-state` resource metadata.
// Field names and optionality follow the Rust serde projection, so a provider
// written against the Rust client sees the same document here.

import (
	"strings"

	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
)

// statuslineState projects the displayed conversation for a provider read. It
// returns nil for read-only history and before the thread exists, matching
// Rust ChatWidget::statusline_state.
func (m *Model) statuslineState() map[string]any {
	if m == nil || m.State == nil || m.readOnlyThread {
		return nil
	}
	threadID := strings.TrimSpace(m.State.ThreadID)
	if threadID == "" {
		return nil
	}
	m.ensureStatusControls()
	m.syncStatusControlsRuntime()
	runtime := m.statusControls.Runtime
	selections := chatwidget.NewStatusSurfaceSelections(
		m.statusControls.ConfiguredStatusLineItems(),
		m.statusControls.ConfiguredTerminalTitleItems(),
	)
	statuslineItems := make([]string, 0, len(selections.StatusLineItems))
	for _, item := range selections.StatusLineItems {
		statuslineItems = append(statuslineItems, chatwidget.StatusLineItemID(item))
	}

	configState := map[string]any{"statusline_items": statuslineItems}
	if profile := strings.TrimSpace(runtime.Profile); profile != "" {
		configState["profile"] = profile
	}

	displayMode := "standard"
	if m.rawOutput {
		displayMode = "raw"
	}

	thread := map[string]any{
		"id":        threadID,
		"name":      nil,
		"agent":     map[string]any{"state": m.statuslineRunState()},
		"model":     m.statuslineModelState(runtime),
		"workspace": map[string]any{"cwd": m.statuslineWorkspaceCWD()},
		"usage":     m.statuslineUsageState(runtime),
	}
	if name := strings.TrimSpace(runtime.ThreadName); name != "" {
		thread["name"] = name
	}
	if m.threadSettingsConfirmed {
		if approval := m.statuslineApprovalState(); approval != nil {
			thread["approval"] = approval
		}
	}

	return map[string]any{
		"account": m.statuslineAccountState(runtime),
		"app": map[string]any{
			"cli_version":  strings.TrimSpace(m.State.CLIVersion),
			"display_mode": displayMode,
		},
		"config": configState,
		"thread": thread,
	}
}

// statuslineWorkspaceCWD mirrors Rust status_line_workspace_cwd for a local
// connection: the thread's current directory.
func (m *Model) statuslineWorkspaceCWD() string {
	return m.statusLineCWD()
}

// statuslineAccountState projects the built-in status line's account values:
// the codex rate-limit sample and the workspace headline.
func (m *Model) statuslineAccountState(runtime chatwidget.StatusControlsRuntime) map[string]any {
	rateLimits := []any{}
	if sample, ok := runtime.RateLimitSnapshots["codex"]; ok && (sample.Primary != nil || sample.Secondary != nil) {
		entry := map[string]any{
			"limit_id":        "codex",
			"primary_limit":   statuslineRateLimitWindow(sample.Primary),
			"secondary_limit": statuslineRateLimitWindow(sample.Secondary),
		}
		if name := strings.TrimSpace(sample.LimitName); name != "" {
			entry["limit_name"] = name
		}
		rateLimits = append(rateLimits, entry)
	}
	account := map[string]any{"rate_limits": rateLimits}
	if headline := strings.TrimSpace(runtime.WorkspaceHeadline); headline != "" {
		account["workspace"] = map[string]any{"headline": headline}
	}
	return account
}

func statuslineRateLimitWindow(window *chatwidget.RateLimitWindow) any {
	if window == nil {
		return nil
	}
	entry := map[string]any{
		"used_percent":   window.UsedPercent,
		"window_minutes": nil,
		"resets_at":      nil,
	}
	if window.WindowDurationMins != nil {
		entry["window_minutes"] = *window.WindowDurationMins
	}
	if window.ResetsAt != nil {
		entry["resets_at"] = *window.ResetsAt
	}
	return entry
}

// statuslineRunState mirrors Rust status_line_run_state.
func (m *Model) statuslineRunState() string {
	if m == nil || m.State == nil {
		return "thinking"
	}
	if m.mcpStartupActive {
		return "starting"
	}
	if !m.isTaskRunning() {
		return "ready"
	}
	if m.statusControls != nil {
		switch m.statusControls.StatusState.TerminalTitleStatusKind {
		case chatwidget.TerminalTitleStatusWorking:
			return "working"
		case chatwidget.TerminalTitleStatusWaitingForBackgroundTerminal:
			return "waiting"
		}
	}
	return "thinking"
}

// statuslineModelState mirrors Rust StatuslineStateModel. Thread-confirmed
// fields stay absent until the server reports the thread's settings.
func (m *Model) statuslineModelState(runtime chatwidget.StatusControlsRuntime) map[string]any {
	model := map[string]any{
		"display_name": strings.TrimSpace(runtime.ModelName),
		"provider":     strings.TrimSpace(runtime.ModelProviderID),
	}
	if id := strings.TrimSpace(m.State.Model); id != "" {
		model["id"] = id
	}
	if effort := strings.TrimSpace(m.State.EffectiveReasoningEffort()); effort != "" {
		model["reasoning_level"] = effort
	}
	if m.threadSettingsConfirmed {
		model["service_tier"] = statuslineServiceTier(m.State.ServiceTier)
		if display := strings.TrimSpace(runtime.ProcessingTier); display != "" {
			model["service_tier_display_name"] = display
		}
	}
	if program, err := m.cyberAccessProgramForTurn(); err == nil && strings.TrimSpace(program) != "" {
		model["access_program"] = strings.TrimSpace(program)
	}
	return model
}

// statuslineServiceTier maps the request value onto Rust's display tokens.
func statuslineServiceTier(tier string) string {
	switch value := strings.TrimSpace(tier); {
	case value == "" || value == chatwidget.ServiceTierDefaultRequestValue:
		return "normal"
	case value == "priority":
		return "fast"
	default:
		return value
	}
}

// statuslineApprovalState mirrors Rust StatuslineStateApproval.
func (m *Model) statuslineApprovalState() map[string]any {
	if m == nil || m.State == nil {
		return nil
	}
	policy := strings.TrimSpace(m.State.ApprovalPolicy)
	switch policy {
	case "untrusted", "on-request", "granular", "never":
	default:
		return nil
	}
	reviewer := "user"
	if m.approvalsReviewer == chatwidget.ApprovalsReviewerAutoReview {
		reviewer = "auto-review"
	}
	return map[string]any{"policy": policy, "reviewer": reviewer}
}

// statuslineUsageState mirrors Rust StatuslineStateUsage.
func (m *Model) statuslineUsageState(runtime chatwidget.StatusControlsRuntime) map[string]any {
	usage := map[string]any{}
	context := map[string]any{}
	if runtime.ContextWindowSize != nil {
		context["context_window_size"] = *runtime.ContextWindowSize
		if percent, ok := m.statusControls.StatusLineContextUsedPercent(); ok {
			context["used_percentage"] = percent
		}
	}
	if !m.State.LastTokenUsage.IsZero() {
		context["used_tokens"] = m.State.LastTokenUsage.TokensInContextWindow()
	}
	usage["context"] = context
	if total, ok := statuslineReportedCounts(m.State.TotalTokenUsage); ok {
		usage["total"] = total
	}
	cost := map[string]any{}
	if micros := runtime.ThreadUsageEstimatedUSDMicros; micros != nil && *micros >= 0 {
		cost["total_cost_usd"] = float64(*micros) / 1_000_000
	}
	if micros := runtime.ThreadUsageCreditsMicros; micros != nil && *micros >= 0 {
		cost["total_credits"] = float64(*micros) / 1_000_000
	}
	if len(cost) > 0 {
		usage["cost"] = cost
	}
	return usage
}

// statuslineReportedCounts mirrors Rust Counts::reported: a synthetic context
// fill and inconsistent counters report no breakdown.
func statuslineReportedCounts(usage codextui.TokenUsage) (map[string]any, bool) {
	other := usage.InputTokens - usage.CachedInputTokens
	if other < 0 || usage.CachedInputTokens < 0 || usage.OutputTokens < 0 {
		return nil, false
	}
	if usage.TotalTokens != 0 && usage.InputTokens == 0 && usage.OutputTokens == 0 {
		return nil, false
	}
	return map[string]any{
		"total_input_tokens":       usage.InputTokens,
		"cache_read_input_tokens":  usage.CachedInputTokens,
		"cache_write_input_tokens": int64(0),
		"other_input_tokens":       other,
		"output_tokens":            usage.OutputTokens,
	}, true
}
