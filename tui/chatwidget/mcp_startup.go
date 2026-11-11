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

// McpStartupRoundState tracks only the MCP starts and outcomes the TUI
// receives, including partial startup rounds after attaching to a running app
// server (Rust #60622). Locally configured servers that the app server never
// reports are not part of the round, so startup settles as soon as no observed
// start remains pending.
type McpStartupRoundState struct {
	// Status is the latest status per server in the active round. Its presence
	// keeps the composer busy, and its terminal outcomes determine the summary.
	Status map[string]McpStartupStatus
	// LastDiagnostics remembers the last terminal outcome reported per server so
	// a replayed failure or cancellation is announced only once. An observed
	// retry or a lagged notification stream clears the record.
	LastDiagnostics map[string]McpStartupStatus
}

type McpStartupUpdateResult struct {
	Header    string
	Warnings  []string
	Finished  bool
	Failed    []string
	Cancelled []string
	Active    bool
	// StandaloneDiagnostic reports a terminal outcome that arrived while no
	// round was active (Rust #60280/#60622). The caller renders the diagnostics
	// but must not open or close a startup round for it.
	StandaloneDiagnostic bool
}

func NewMcpStartupRoundState() McpStartupRoundState {
	return McpStartupRoundState{
		Status:          map[string]McpStartupStatus{},
		LastDiagnostics: map[string]McpStartupStatus{},
	}
}

// Update records one received status. The server announces eager starts before
// outcomes; on attach or after lag an outcome can arrive without its start.
func (s *McpStartupRoundState) Update(server string, status McpStartupStatus) McpStartupUpdateResult {
	if s == nil {
		return McpStartupUpdateResult{}
	}
	if strings.TrimSpace(string(status.Kind)) == "" {
		status.Kind = McpStartupStarting
	}
	if s.Status == nil {
		s.Status = map[string]McpStartupStatus{}
	}
	if s.LastDiagnostics == nil {
		s.LastDiagnostics = map[string]McpStartupStatus{}
	}

	reportDiagnostic := false
	if mcpStartupStatusIsDiagnostic(status) {
		previous, seen := s.LastDiagnostics[server]
		s.LastDiagnostics[server] = status
		reportDiagnostic = !seen || !sameMcpStartupStatus(previous, status)
	} else {
		delete(s.LastDiagnostics, server)
	}
	var warnings []string
	if reportDiagnostic && status.Kind == McpStartupFailed && status.Error != "" {
		warnings = append(warnings, status.Error)
	}

	// An idle client can receive a terminal update without ever seeing its
	// start; report it without reopening startup.
	if len(s.Status) == 0 && status.Kind != McpStartupStarting {
		if !reportDiagnostic {
			return McpStartupUpdateResult{}
		}
		return standaloneMcpStartupDiagnostic(server, status)
	}

	s.Status[server] = status
	header := s.Header()
	if header == "" {
		finish := s.Finish()
		finish.Warnings = append(warnings, finish.Warnings...)
		return finish
	}
	return McpStartupUpdateResult{
		Header:   header,
		Warnings: warnings,
		Active:   true,
	}
}

func (s *McpStartupRoundState) Finish() McpStartupUpdateResult {
	if s == nil {
		return McpStartupUpdateResult{}
	}
	failed := []string{}
	cancelled := []string{}
	for _, name := range keysOfMcpStartupMap(s.Status) {
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
	// A missed Starting may have begun a new attempt with the same error.
	s.LastDiagnostics = map[string]McpStartupStatus{}
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

func mcpStartupStatusIsDiagnostic(status McpStartupStatus) bool {
	return status.Kind == McpStartupFailed || status.Kind == McpStartupCancelled
}

func sameMcpStartupStatus(left, right McpStartupStatus) bool {
	return left.Kind == right.Kind && left.Error == right.Error && left.FailureReason == right.FailureReason
}

// standaloneMcpStartupDiagnostic mirrors Rust's idle branch: the failure or
// cancellation is surfaced together with the round summary even though the
// client never saw the server start.
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

func keysOfMcpStartupMap(in map[string]McpStartupStatus) []string {
	out := make([]string, 0, len(in))
	for key := range in {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}
