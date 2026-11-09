package chatwidget

import (
	"sort"
	"strings"
)

const (
	MCPStartupSingleHeaderPrefix = "Booting MCP server:"
	MCPStartupMultiHeaderPrefix  = "Starting MCP servers"
)

type McpStartupStatusKind string

const (
	McpStartupStarting  McpStartupStatusKind = "starting"
	McpStartupReady     McpStartupStatusKind = "ready"
	McpStartupFailed    McpStartupStatusKind = "failed"
	McpStartupCancelled McpStartupStatusKind = "cancelled"
)

type McpStartupStatus struct {
	Kind McpStartupStatusKind
	// Error carries the startup error text surfaced to the user.
	Error string
	// FailureReason mirrors Rust's McpServerStartupFailureReason (for example
	// "reauthenticationRequired"), which marks the server as needing sign-in in
	// the startup warnings summary.
	FailureReason string
}

// MCPStartupInterruptedMessage is Rust's cancelled-server diagnostic.
func MCPStartupInterruptedMessage(cancelled []string) string {
	return "MCP startup interrupted. The following servers were not initialized: " + strings.Join(cancelled, ", ")
}

// MCPStartupIncompleteMessage is Rust's failed-server diagnostic.
func MCPStartupIncompleteMessage(failed []string) string {
	return "MCP startup incomplete (failed: " + strings.Join(failed, ", ") + ")"
}

type McpStartupRoundState struct {
	ExpectedServers    []string
	ExpectedServersSet bool
	// ReportedOnly marks a startup view whose round membership comes from the
	// startup notifications the client observed (Rust #60280). Shared app
	// servers only send updates after subscription and may run a different
	// configuration, and subagents can start deferred servers individually, so
	// waiting for locally configured servers would leave the TUI stuck busy.
	ReportedOnly bool
	// LastDiagnostics remembers the last terminal outcome reported per server so
	// an identical failure or cancellation is not announced twice. An observed
	// retry or a lagged notification stream clears the record.
	LastDiagnostics map[string]McpStartupStatus

	Status                      map[string]McpStartupStatus
	IgnoreUpdatesUntilNextStart bool
	AllowTerminalOnlyNextRound  bool
	PendingNextRoundSawStarting bool
	PendingNextRound            map[string]McpStartupStatus
}

type McpStartupUpdateResult struct {
	Header    string
	Warnings  []string
	Finished  bool
	Settled   bool
	Failed    []string
	Cancelled []string
	Active    bool
	// StandaloneDiagnostic reports a terminal outcome that arrived while no
	// round was active (Rust #60280). The caller renders the diagnostics but
	// must not open or close a startup round for it.
	StandaloneDiagnostic bool
}

func NewMcpStartupRoundState(expectedServers []string) McpStartupRoundState {
	return McpStartupRoundState{
		ExpectedServers:    normalizeStringSet(expectedServers),
		ExpectedServersSet: true,
		Status:             map[string]McpStartupStatus{},
		PendingNextRound:   map[string]McpStartupStatus{},
	}
}

// TrackReportedServers switches the round to wait only for startups the client
// observes, mirroring Rust's ChatWidget::track_reported_mcp_startup_servers.
// Repeating the call keeps the diagnostics recorded for the current view.
func (s *McpStartupRoundState) TrackReportedServers() {
	if s == nil || s.ReportedOnly {
		return
	}
	s.ReportedOnly = true
	s.ExpectedServers = nil
	s.ExpectedServersSet = true
	s.LastDiagnostics = map[string]McpStartupStatus{}
}

func (s *McpStartupRoundState) SetExpectedServers(serverNames []string) {
	if s == nil {
		return
	}
	s.ExpectedServers = normalizeStringSet(serverNames)
	s.ExpectedServersSet = true
	s.ReportedOnly = false
	s.LastDiagnostics = nil
}

func (s *McpStartupRoundState) Update(server string, status McpStartupStatus, completeWhenSettled bool) McpStartupUpdateResult {
	if s == nil {
		return McpStartupUpdateResult{}
	}
	if strings.TrimSpace(string(status.Kind)) == "" {
		status.Kind = McpStartupStarting
	}
	if s.Status == nil {
		s.Status = map[string]McpStartupStatus{}
	}
	if s.PendingNextRound == nil {
		s.PendingNextRound = map[string]McpStartupStatus{}
	}

	reportDiagnostic := true
	if s.ReportedOnly {
		if s.LastDiagnostics == nil {
			s.LastDiagnostics = map[string]McpStartupStatus{}
		}
		if mcpStartupStatusIsDiagnostic(status) {
			previous, seen := s.LastDiagnostics[server]
			s.LastDiagnostics[server] = status
			reportDiagnostic = !seen || !sameMcpStartupStatus(previous, status)
		} else {
			delete(s.LastDiagnostics, server)
			reportDiagnostic = false
		}
		// An idle client can receive a terminal update without ever seeing its
		// start; report it without reopening startup.
		if len(s.Status) == 0 && status.Kind != McpStartupStarting {
			if !reportDiagnostic {
				return McpStartupUpdateResult{}
			}
			return standaloneMcpStartupDiagnostic(server, status)
		}
	}

	var warnings []string
	activatedPendingRound := false
	if s.IgnoreUpdatesUntilNextStart {
		if status.Kind == McpStartupStarting && !s.PendingNextRoundSawStarting {
			s.PendingNextRound = map[string]McpStartupStatus{}
			s.AllowTerminalOnlyNextRound = false
		}
		if status.Kind == McpStartupStarting {
			s.PendingNextRoundSawStarting = true
		}
		s.PendingNextRound[server] = status
		if !s.pendingRoundLooksFresh() {
			return McpStartupUpdateResult{}
		}
		s.IgnoreUpdatesUntilNextStart = false
		s.AllowTerminalOnlyNextRound = false
		s.PendingNextRoundSawStarting = false
		s.Status = cloneMcpStartupMap(s.PendingNextRound)
		s.PendingNextRound = map[string]McpStartupStatus{}
		activatedPendingRound = true
	} else {
		if status.Kind == McpStartupFailed && reportDiagnostic {
			if previous, ok := s.Status[server]; !ok || !sameMcpStartupStatus(previous, status) {
				warnings = append(warnings, status.Error)
			}
		}
		s.Status[server] = status
	}
	if activatedPendingRound {
		for _, state := range s.Status {
			if state.Kind == McpStartupFailed {
				warnings = append(warnings, state.Error)
			}
		}
	}

	if completeWhenSettled && s.isSettled() {
		finish := s.Finish()
		finish.Warnings = append(warnings, finish.Warnings...)
		return finish
	}
	header := s.Header()
	settled := s.isSettled()
	return McpStartupUpdateResult{
		Header:   header,
		Warnings: warnings,
		Settled:  settled,
		Active:   len(s.Status) > 0,
	}
}

func (s *McpStartupRoundState) Finish() McpStartupUpdateResult {
	if s == nil {
		return McpStartupUpdateResult{}
	}
	failed := []string{}
	cancelled := []string{}
	allNames := normalizeStringSet(append(append([]string{}, s.ExpectedServers...), keysOfMcpStartupMap(s.Status)...))
	for _, name := range allNames {
		switch s.Status[name].Kind {
		case McpStartupReady:
		case McpStartupFailed:
			failed = append(failed, name)
		case McpStartupCancelled, McpStartupStarting, "":
			cancelled = append(cancelled, name)
		}
	}
	warnings := []string{}
	if len(cancelled) > 0 {
		warnings = append(warnings, MCPStartupInterruptedMessage(cancelled))
	}
	if len(failed) > 0 {
		warnings = append(warnings, MCPStartupIncompleteMessage(failed))
	}
	s.Status = map[string]McpStartupStatus{}
	s.IgnoreUpdatesUntilNextStart = true
	s.AllowTerminalOnlyNextRound = false
	s.PendingNextRoundSawStarting = false
	s.PendingNextRound = map[string]McpStartupStatus{}
	return McpStartupUpdateResult{
		Warnings:  warnings,
		Finished:  true,
		Failed:    failed,
		Cancelled: cancelled,
	}
}

func (s *McpStartupRoundState) FinishAfterLag() McpStartupUpdateResult {
	if s == nil {
		return McpStartupUpdateResult{}
	}
	result := McpStartupUpdateResult{}
	if len(s.Status) > 0 {
		result = s.Finish()
	}
	if s.ReportedOnly {
		// A missed Starting may have begun a new attempt with the same error.
		s.LastDiagnostics = map[string]McpStartupStatus{}
	}
	if s.IgnoreUpdatesUntilNextStart {
		if len(s.PendingNextRound) == 0 {
			s.PendingNextRoundSawStarting = false
		}
		s.AllowTerminalOnlyNextRound = true
	}
	return result
}

func (s McpStartupRoundState) Header() string {
	if len(s.Status) == 0 {
		return ""
	}
	starting := []string{}
	for name, status := range s.Status {
		if status.Kind == McpStartupStarting {
			starting = append(starting, name)
		}
	}
	sort.Strings(starting)
	if len(starting) == 0 {
		return ""
	}
	if len(s.Status) == 1 {
		return MCPStartupSingleHeaderPrefix + " " + starting[0]
	}
	completed := len(s.Status) - len(starting)
	shown := append([]string{}, starting...)
	if len(shown) > 3 {
		shown = append(shown[:3], "...")
	}
	return MCPStartupMultiHeaderPrefix + " (" + formatInt64(int64(completed)) + "/" + formatInt64(int64(len(s.Status))) + "): " + strings.Join(shown, ", ")
}

func (s McpStartupRoundState) StatusHeaderIsMcpStartupOwned(header string) bool {
	return strings.HasPrefix(header, MCPStartupSingleHeaderPrefix) || strings.HasPrefix(header, MCPStartupMultiHeaderPrefix)
}

func (s McpStartupRoundState) isSettled() bool {
	if len(s.Status) == 0 {
		return false
	}
	if !s.ReportedOnly {
		if !s.ExpectedServersSet {
			return false
		}
		for _, name := range s.ExpectedServers {
			if _, ok := s.Status[name]; !ok {
				return false
			}
		}
	}
	for _, status := range s.Status {
		if status.Kind == McpStartupStarting || status.Kind == "" {
			return false
		}
	}
	return true
}

func (s McpStartupRoundState) pendingRoundLooksFresh() bool {
	if !s.ExpectedServersSet {
		return false
	}
	for _, name := range s.ExpectedServers {
		if _, ok := s.PendingNextRound[name]; !ok {
			return false
		}
	}
	if s.PendingNextRoundSawStarting || s.AllowTerminalOnlyNextRound {
		return true
	}
	for _, status := range s.PendingNextRound {
		if status.Kind == McpStartupStarting {
			return true
		}
	}
	return false
}

func mcpStartupStatusIsDiagnostic(status McpStartupStatus) bool {
	return status.Kind == McpStartupFailed || status.Kind == McpStartupCancelled
}

func sameMcpStartupStatus(left, right McpStartupStatus) bool {
	return left.Kind == right.Kind && left.Error == right.Error && left.FailureReason == right.FailureReason
}

// standaloneMcpStartupDiagnostic mirrors Rust's idle ReportedOnly branch: the
// failure or cancellation is surfaced together with the round summary even
// though the client never saw the server start.
func standaloneMcpStartupDiagnostic(server string, status McpStartupStatus) McpStartupUpdateResult {
	result := McpStartupUpdateResult{StandaloneDiagnostic: true}
	switch status.Kind {
	case McpStartupFailed:
		result.Failed = []string{server}
		if status.Error != "" {
			result.Warnings = append(result.Warnings, status.Error)
		}
	case McpStartupCancelled:
		result.Cancelled = []string{server}
	}
	result.Warnings = append(result.Warnings, summarizeMcpStartupOutcomes(map[string]McpStartupStatus{server: status})...)
	return result
}

func summarizeMcpStartupOutcomes(statuses map[string]McpStartupStatus) []string {
	failed := []string{}
	cancelled := []string{}
	for name, status := range statuses {
		switch status.Kind {
		case McpStartupFailed:
			failed = append(failed, name)
		case McpStartupCancelled:
			cancelled = append(cancelled, name)
		}
	}
	sort.Strings(failed)
	sort.Strings(cancelled)
	warnings := []string{}
	if len(cancelled) > 0 {
		warnings = append(warnings, MCPStartupInterruptedMessage(cancelled))
	}
	if len(failed) > 0 {
		warnings = append(warnings, MCPStartupIncompleteMessage(failed))
	}
	return warnings
}

func cloneMcpStartupMap(in map[string]McpStartupStatus) map[string]McpStartupStatus {
	out := make(map[string]McpStartupStatus, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func keysOfMcpStartupMap(in map[string]McpStartupStatus) []string {
	out := make([]string, 0, len(in))
	for key := range in {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func normalizeStringSet(values []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}
