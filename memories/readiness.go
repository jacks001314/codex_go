package memories

// Rust parity: codex-rs/state/src/runtime/memory_readiness.rs and
// codex-rs/protocol/src/memory_version.rs / memories/write/src/workspace.rs
// (#43827). v2 memory context is selected only after background consolidation
// has produced enough distinct threads and a valid summary.

import (
	"path/filepath"
	"strings"
)

// MemoryVersionV2Directory is the sibling root for v2 memory artifacts
// (Rust MemoryVersion::V2.directory_name()).
const MemoryVersionV2Directory = "memories_v2"

// maxV2SummaryBytes mirrors Rust's is_valid_v2_summary length bound.
const maxV2SummaryBytes = 10_000

var v2SummaryHeadings = []string{
	"## User Profile",
	"## User preferences",
	"## General Tips",
	"## What's in Memory",
}

// V2Root returns the v2 memory root for a Codex home.
func V2Root(codexHome string) string {
	return filepath.Join(strings.TrimSpace(codexHome), MemoryVersionV2Directory)
}

// IsValidV2Summary reports whether a v2 memory summary is complete enough to
// supply context (Rust is_valid_v2_summary, #43827): the first line is `v1`,
// the summary is under 10,000 bytes, and it contains every required heading.
func IsValidV2Summary(summary string) bool {
	if len(summary) >= maxV2SummaryBytes {
		return false
	}
	lines := splitV2SummaryLines(summary)
	if len(lines) == 0 || lines[0] != "v1" {
		return false
	}
	for _, heading := range v2SummaryHeadings {
		found := false
		for _, line := range lines {
			if strings.TrimSpace(line) == heading {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

// splitV2SummaryLines mirrors Rust str::lines() for the summary check: it splits
// on \n and drops a trailing \r from each line (including the first).
func splitV2SummaryLines(summary string) []string {
	lines := strings.Split(summary, "\n")
	for i := range lines {
		lines[i] = strings.TrimSuffix(lines[i], "\r")
	}
	return lines
}
