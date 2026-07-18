package memories

import (
	"path/filepath"
	"strings"
)

type UsageKind string

const (
	UsageKindMemoryMD         UsageKind = "memory_md"
	UsageKindMemorySummary    UsageKind = "memory_summary"
	UsageKindRawMemories      UsageKind = "raw_memories"
	UsageKindRolloutSummaries UsageKind = "rollout_summaries"
	UsageKindSkills           UsageKind = "skills"
)

func UsageKindsFromCommand(command string) []UsageKind {
	for _, part := range splitShellFragments(command) {
		fields := strings.Fields(part)
		if len(fields) == 0 {
			continue
		}
		kinds := usageKindsFromFields(fields)
		if len(kinds) > 0 {
			return kinds
		}
	}
	return nil
}

func UsageKindFromPath(path string) (UsageKind, bool) {
	normalized := strings.ReplaceAll(path, `\`, "/")
	normalized = filepath.ToSlash(normalized)
	switch {
	case strings.Contains(normalized, "memories/MEMORY.md"):
		return UsageKindMemoryMD, true
	case strings.Contains(normalized, "memories/memory_summary.md"):
		return UsageKindMemorySummary, true
	case strings.Contains(normalized, "memories/raw_memories.md"):
		return UsageKindRawMemories, true
	case strings.Contains(normalized, "memories/rollout_summaries/"):
		return UsageKindRolloutSummaries, true
	case strings.Contains(normalized, "memories/skills/"):
		return UsageKindSkills, true
	default:
		return "", false
	}
}

func usageKindsFromFields(fields []string) []UsageKind {
	command := strings.ToLower(fields[0])
	switch command {
	case "cat", "type", "less", "more", "head", "tail", "sed", "bat":
		return usageKindsFromPaths(fields[1:])
	case "rg", "grep", "find", "fd", "ls", "dir":
		return usageKindsFromPaths(fields[1:])
	default:
		return nil
	}
}

func usageKindsFromPaths(values []string) []UsageKind {
	seen := map[UsageKind]bool{}
	out := []UsageKind{}
	for _, value := range values {
		value = strings.Trim(value, `"'`)
		if strings.HasPrefix(value, "-") {
			continue
		}
		kind, ok := UsageKindFromPath(value)
		if !ok || seen[kind] {
			continue
		}
		seen[kind] = true
		out = append(out, kind)
	}
	return out
}

func splitShellFragments(command string) []string {
	command = strings.TrimSpace(command)
	if command == "" {
		return nil
	}
	parts := strings.FieldsFunc(command, func(r rune) bool {
		return r == '\n' || r == ';' || r == '|'
	})
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}
