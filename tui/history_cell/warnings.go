package historycell

import (
	"sort"
	"strings"
)

// Rust parity: codex-rs/tui/src/history_cell/warnings.rs. Retained diagnostics
// keep their source details for the transcript while exposing stable identities
// to the footer count and the warnings viewer (#48205/#48206). Message
// identities deduplicate replay; MCP identities count affected servers rather
// than summary rows.

// WarningIdKind distinguishes the two retained diagnostic identities.
type WarningIdKind string

const (
	// WarningIdMessage identifies a diagnostic by its message text.
	WarningIdMessage WarningIdKind = "message"
	// WarningIdMCPServer identifies a diagnostic by the MCP server it concerns.
	WarningIdMCPServer WarningIdKind = "mcp_server"
)

// WarningId is a retained diagnostic's stable identity, independent of wrapping
// and duplicate delivery.
type WarningId struct {
	Kind  WarningIdKind
	Value string
}

// WarningEntry is one retained diagnostic with its source label and details.
type WarningEntry struct {
	ID      WarningId
	Source  string
	Details string
}

// WarningKey is the identity a footer counts; it deduplicates replayed cells.
type WarningKey struct {
	Kind  WarningIdKind
	Value string
}

// WarningCell is a history cell that retains diagnostics.
type WarningCell interface {
	WarningKeys() []WarningKey
	WarningEntries() []WarningEntry
}

// WarningEntries mirrors `warning_entries`: cells are visited in order and
// entries sharing an identity merge, appending details that are not already
// present.
func WarningEntries(cells []WarningCell) []WarningEntry {
	entries := []WarningEntry{}
	indices := map[WarningId]int{}
	for _, cell := range cells {
		if cell == nil {
			continue
		}
		for _, entry := range cell.WarningEntries() {
			if index, ok := indices[entry.ID]; ok {
				existing := &entries[index]
				if !strings.Contains(existing.Details, entry.Details) {
					existing.Details += "\n\n" + entry.Details
				}
				continue
			}
			indices[entry.ID] = len(entries)
			entries = append(entries, entry)
		}
	}
	return entries
}

// WarningCount mirrors `warning_count`: the number of distinct identities across
// the cells.
func WarningCount(cells []WarningCell) int {
	seen := map[WarningKey]bool{}
	for _, cell := range cells {
		if cell == nil {
			continue
		}
		for _, key := range cell.WarningKeys() {
			seen[key] = true
		}
	}
	return len(seen)
}

// sortedWarningSetKeys returns a set's members in the sorted order Rust's
// `BTreeSet` iteration produces.
func sortedWarningSetKeys(values map[string]bool) []string {
	if len(values) == 0 {
		return nil
	}
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
