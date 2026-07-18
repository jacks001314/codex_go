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
	Kind  McpStartupStatusKind
	Error string
}

type McpStartupRoundState struct {
	ExpectedServers             []string
	ExpectedServersSet          bool
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
}

func NewMcpStartupRoundState(expectedServers []string) McpStartupRoundState {
	return McpStartupRoundState{
		ExpectedServers:    normalizeStringSet(expectedServers),
		ExpectedServersSet: true,
		Status:             map[string]McpStartupStatus{},
		PendingNextRound:   map[string]McpStartupStatus{},
	}
}

func (s *McpStartupRoundState) SetExpectedServers(serverNames []string) {
	if s == nil {
		return
	}
	s.ExpectedServers = normalizeStringSet(serverNames)
	s.ExpectedServersSet = true
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
		if status.Kind == McpStartupFailed {
			if previous, ok := s.Status[server]; !ok || previous.Kind != McpStartupFailed || previous.Error != status.Error {
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
		warnings = append(warnings, "MCP startup interrupted. The following servers were not initialized: "+strings.Join(cancelled, ", "))
	}
	if len(failed) > 0 {
		warnings = append(warnings, "MCP startup incomplete (failed: "+strings.Join(failed, ", ")+")")
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
	if s.IgnoreUpdatesUntilNextStart {
		if len(s.PendingNextRound) == 0 {
			s.PendingNextRoundSawStarting = false
		}
		s.AllowTerminalOnlyNextRound = true
	}
	if len(s.Status) == 0 {
		return McpStartupUpdateResult{}
	}
	return s.Finish()
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
	if !s.ExpectedServersSet {
		return false
	}
	for _, name := range s.ExpectedServers {
		if _, ok := s.Status[name]; !ok {
			return false
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
