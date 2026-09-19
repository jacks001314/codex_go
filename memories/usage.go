package memories

import (
	"strings"

	"codex_go/config"
	"codex_go/shell"
)

// Rust parity: codex-rs/memories/read/src/usage.rs - best-effort classification
// of shell reads by memory artifact and root version.

type UsageKind string

const (
	UsageKindMemoryMD         UsageKind = "memory_md"
	UsageKindMemorySummary    UsageKind = "memory_summary"
	UsageKindRawMemories      UsageKind = "raw_memories"
	UsageKindRolloutSummaries UsageKind = "rollout_summaries"
	UsageKindSkills           UsageKind = "skills"
)

// MemoryUsage pairs one classified memory artifact with the memory root version
// it was read from (Rust `memories_usage_from_command`'s tuple).
type MemoryUsage struct {
	Kind    UsageKind
	Version config.MemoryVersion
}

// UsageFromCommand mirrors Rust's `memories_usage_from_command`: classify every
// file read or search a model-supplied shell script performs, in script order,
// reporting the artifact and the memory root that holds it.
//
// Like Rust, a script with any action the parser cannot classify yields nothing
// at all: an unrecognized segment means the whole command is not a plain read
// chain, so attributing part of it would over-report.
func UsageFromCommand(command string) []MemoryUsage {
	commands := shell.ParseDisplayShellScript(command)
	for _, parsed := range commands {
		if parsed.Kind == shell.DisplayCommandUnknown {
			return nil
		}
	}
	out := []MemoryUsage{}
	for _, parsed := range commands {
		var path string
		switch parsed.Kind {
		case shell.DisplayCommandRead:
			path = parsed.Path
		case shell.DisplayCommandSearch:
			path = parsed.Path
		default:
			// A directory listing names no artifact of its own.
			continue
		}
		if strings.TrimSpace(path) == "" {
			continue
		}
		usage, ok := UsageFromPath(path)
		if !ok {
			continue
		}
		out = append(out, usage)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// UsageFromPath classifies one path as a memory artifact and reports the memory
// root version it belongs to (Rust `get_memory_usage`). Windows separators are
// normalized first, so a PowerShell read of `...\memories_v2\...` is attributed
// to v2 rather than to an unversioned path.
func UsageFromPath(path string) (MemoryUsage, bool) {
	v2Root := config.MemoryVersionV2.DirectoryName() + "/"
	normalized := strings.ReplaceAll(path, `\`, "/")
	version := config.MemoryVersionV1
	if strings.Contains(normalized, v2Root) {
		version = config.MemoryVersionV2
	}
	// Both roots are recognized (#43797): v1 `memories` and v2 `memories_v2`.
	normalized = strings.ReplaceAll(normalized, v2Root, "memories/")
	kind, ok := usageKindFromNormalizedPath(normalized)
	if !ok {
		return MemoryUsage{}, false
	}
	return MemoryUsage{Kind: kind, Version: version}, true
}

// UsageKindFromPath reports only the artifact kind a path names, for callers
// that do not need the root version.
func UsageKindFromPath(path string) (UsageKind, bool) {
	usage, ok := UsageFromPath(path)
	if !ok {
		return "", false
	}
	return usage.Kind, true
}

func usageKindFromNormalizedPath(path string) (UsageKind, bool) {
	switch {
	case strings.Contains(path, "memories/MEMORY.md"):
		return UsageKindMemoryMD, true
	case strings.Contains(path, "memories/memory_summary.md"):
		return UsageKindMemorySummary, true
	case strings.Contains(path, "memories/raw_memories.md"):
		return UsageKindRawMemories, true
	case strings.Contains(path, "memories/rollout_summaries/"):
		return UsageKindRolloutSummaries, true
	case strings.Contains(path, "memories/skills/"):
		return UsageKindSkills, true
	default:
		return "", false
	}
}
