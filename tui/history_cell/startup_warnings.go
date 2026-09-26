package historycell

import (
	"strconv"
	"strings"

	"codex_go/tui"
)

// Rust parity: codex-rs/tui/src/history_cell/startup_warnings.rs. Compact
// startup diagnostics with the full details retained in the transcript:
// affected sources are unique, and the sign-in servers are a subset of the MCP
// servers.

// MCPStartupFailureReauthenticationRequired is the wire failure reason that
// marks an MCP server as needing sign-in
// (McpServerStartupFailureReason::ReauthenticationRequired).
const MCPStartupFailureReauthenticationRequired = "reauthenticationRequired"

type StartupWarningsCell struct {
	Messages     []string
	OtherSources map[string]bool
	MCPServers   map[string]bool
	// MCPDetails maps each failed MCP server to the diagnostics reported for it
	// (Rust's `mcp_details`), so the warnings viewer can show per-server detail.
	MCPDetails     map[string][]string
	SignInServers  map[string]bool
	PendingHeader  bool
	TranscriptHint string
}

func cloneWarningDetails(values map[string][]string) map[string][]string {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string][]string, len(values))
	for key, messages := range values {
		out[key] = append([]string(nil), messages...)
	}
	return out
}

// WarningKeys mirrors `HistoryCell::warning_keys`: one key per other source and
// one per MCP server, in the sorted order Rust's `BTreeSet` iteration produces.
func (c StartupWarningsCell) WarningKeys() []WarningKey {
	keys := make([]WarningKey, 0, len(c.OtherSources)+len(c.MCPServers))
	for _, message := range sortedWarningSetKeys(c.OtherSources) {
		keys = append(keys, WarningKey{Kind: WarningIdMessage, Value: message})
	}
	for _, server := range sortedWarningSetKeys(c.MCPServers) {
		keys = append(keys, WarningKey{Kind: WarningIdMCPServer, Value: server})
	}
	return keys
}

// WarningEntries mirrors `HistoryCell::warning_entries` for the startup cell:
// other sources keep their own diagnostic text, and an MCP server's details are
// its reported diagnostics (or Rust's fallback when none were recorded) plus the
// sign-in note when reauthentication is required.
func (c StartupWarningsCell) WarningEntries() []WarningEntry {
	entries := make([]WarningEntry, 0, len(c.OtherSources)+len(c.MCPServers))
	for _, message := range sortedWarningSetKeys(c.OtherSources) {
		entries = append(entries, WarningEntry{
			ID:      WarningId{Kind: WarningIdMessage, Value: message},
			Source:  "Startup",
			Details: message,
		})
	}
	for _, server := range sortedWarningSetKeys(c.MCPServers) {
		details, recorded := c.MCPDetails[server]
		rendered := "MCP startup incomplete: " + server
		if recorded {
			rendered = strings.Join(details, "\n\n")
		}
		if c.SignInServers[server] {
			rendered += "\n\nSign-in required."
		}
		entries = append(entries, WarningEntry{
			ID:      WarningId{Kind: WarningIdMCPServer, Value: server},
			Source:  "MCP \u00b7 " + server,
			Details: rendered,
		})
	}
	return entries
}

// NewStartupWarnings mirrors StartupWarningsCell::new.
func NewStartupWarnings(messages []string) StartupWarningsCell {
	cell := StartupWarningsCell{
		Messages:     append([]string(nil), messages...),
		OtherSources: map[string]bool{},
	}
	for _, message := range messages {
		cell.OtherSources[message] = true
	}
	return cell
}

// NewMCPStartupWarnings mirrors StartupWarningsCell::mcp: the servers that
// failed, plus the sign-in subset when the failure reason requires
// reauthentication.
func NewMCPStartupWarnings(messages []string, servers []string, failureReason string) StartupWarningsCell {
	cell := StartupWarningsCell{
		Messages:   append([]string(nil), messages...),
		MCPServers: map[string]bool{},
		MCPDetails: map[string][]string{},
	}
	for _, server := range servers {
		cell.MCPServers[server] = true
		cell.MCPDetails[server] = append([]string(nil), messages...)
	}
	if strings.TrimSpace(failureReason) == MCPStartupFailureReauthenticationRequired {
		cell.SignInServers = map[string]bool{}
		for server := range cell.MCPServers {
			cell.SignInServers[server] = true
		}
	}
	return cell
}

// Merge mirrors App::merge_startup_warnings: messages are appended once (the
// summary must not double-count recurring diagnostics) and the source sets are
// unioned.
func (c StartupWarningsCell) Merge(incoming StartupWarningsCell) StartupWarningsCell {
	merged := StartupWarningsCell{
		Messages:       append([]string(nil), c.Messages...),
		OtherSources:   cloneWarningSet(c.OtherSources),
		MCPServers:     cloneWarningSet(c.MCPServers),
		MCPDetails:     cloneWarningDetails(c.MCPDetails),
		SignInServers:  cloneWarningSet(c.SignInServers),
		PendingHeader:  c.PendingHeader,
		TranscriptHint: c.TranscriptHint,
	}
	for _, message := range incoming.Messages {
		if !containsWarningMessage(merged.Messages, message) {
			merged.Messages = append(merged.Messages, message)
		}
	}
	for source := range incoming.OtherSources {
		if merged.OtherSources == nil {
			merged.OtherSources = map[string]bool{}
		}
		merged.OtherSources[source] = true
	}
	for server := range incoming.MCPServers {
		if merged.MCPServers == nil {
			merged.MCPServers = map[string]bool{}
		}
		merged.MCPServers[server] = true
	}
	for server, messages := range incoming.MCPDetails {
		if merged.MCPDetails == nil {
			merged.MCPDetails = map[string][]string{}
		}
		details := merged.MCPDetails[server]
		for _, message := range messages {
			if !containsWarningMessage(details, message) {
				details = append(details, message)
			}
		}
		merged.MCPDetails[server] = details
	}
	for server := range incoming.SignInServers {
		if merged.SignInServers == nil {
			merged.SignInServers = map[string]bool{}
		}
		merged.SignInServers[server] = true
	}
	return merged
}

func (c StartupWarningsCell) DisplayLines(width int) []string {
	if c.PendingHeader || len(c.Messages) == 0 || width <= 0 {
		return nil
	}
	return []string{tui.TruncateWithEllipsis(c.SummaryLine(), width)}
}

// SummaryLine renders Rust's summary: "⚠ N startup issue(s)" with the MCP
// prefix when every source is MCP, the "(N MCP; N need sign-in)" breakdown, and
// the transcript hint.
func (c StartupWarningsCell) SummaryLine() string {
	mcpCount := len(c.MCPServers)
	count := mcpCount + len(c.OtherSources)
	signInCount := len(c.SignInServers)
	plural := "s"
	if count == 1 {
		plural = ""
	}
	source := ""
	if mcpCount == count {
		source = "MCP "
	}
	var builder strings.Builder
	builder.WriteString("\u26a0 ")
	builder.WriteString(strconv.Itoa(count))
	builder.WriteString(" ")
	builder.WriteString(source)
	builder.WriteString("startup issue")
	builder.WriteString(plural)
	var breakdown []string
	if mcpCount > 0 && mcpCount < count {
		breakdown = append(breakdown, strconv.Itoa(mcpCount)+" MCP")
	}
	if signInCount > 0 {
		verb := "need"
		if signInCount == 1 {
			verb = "needs"
		}
		breakdown = append(breakdown, strconv.Itoa(signInCount)+" "+verb+" sign-in")
	}
	if len(breakdown) > 0 {
		builder.WriteString(" (")
		builder.WriteString(strings.Join(breakdown, "; "))
		builder.WriteString(")")
	}
	if hint := strings.TrimSpace(c.TranscriptHint); hint != "" {
		builder.WriteString(" \u00b7 ")
		builder.WriteString(hint)
		builder.WriteString(" for details")
	}
	return builder.String()
}

// TranscriptLines mirrors display_lines for the Ctrl+T transcript: every
// warning is rendered as a prefixed warning event.
func (c StartupWarningsCell) TranscriptLines(width int) []string {
	var out []string
	for _, message := range c.Messages {
		out = append(out, NewWarningEvent(message).DisplayLines(width)...)
	}
	return out
}

func (c StartupWarningsCell) RawLines() []string {
	var out []string
	for _, message := range c.Messages {
		out = append(out, rawLinesFromSource(message)...)
	}
	return out
}

func cloneWarningSet(values map[string]bool) map[string]bool {
	if len(values) == 0 {
		return nil
	}
	out := make(map[string]bool, len(values))
	for key := range values {
		out[key] = true
	}
	return out
}

func containsWarningMessage(messages []string, message string) bool {
	for _, existing := range messages {
		if existing == message {
			return true
		}
	}
	return false
}
