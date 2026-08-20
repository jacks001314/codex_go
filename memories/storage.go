package memories

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"codex_go/state"
	"github.com/google/uuid"
)

const (
	RolloutSummariesSubdir = "rollout_summaries"
	RawMemoriesFilename    = "raw_memories.md"
	MemoryFilename         = "MEMORY.md"
	MemorySummaryFilename  = "memory_summary.md"

	rolloutSlugMaxLen = 60
	shortHashSpace    = uint32(14_776_336)
)

const shortHashAlphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ"

func Root(codexHome string) string {
	return filepath.Join(codexHome, "memories")
}

func RolloutSummariesDir(root string) string {
	return filepath.Join(root, RolloutSummariesSubdir)
}

func RawMemoriesFile(root string) string {
	return filepath.Join(root, RawMemoriesFilename)
}

func EnsureLayout(root string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	if info, err := os.Lstat(root); err != nil {
		return err
	} else if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("memory root cannot be a symbolic link: %s", root)
	}
	if err := removeMemorySymlinks(root); err != nil {
		return err
	}
	return os.MkdirAll(RolloutSummariesDir(root), 0o755)
}

func RebuildRawMemoriesFile(root string, values []state.Stage1Output, limit int) error {
	if err := EnsureLayout(root); err != nil {
		return err
	}
	retained := retainedStage1Outputs(values, limit)
	var body strings.Builder
	body.WriteString("# Raw Memories\n\n")
	if len(retained) == 0 {
		body.WriteString("No raw memories yet.\n")
		return os.WriteFile(RawMemoriesFile(root), []byte(body.String()), 0o666)
	}

	body.WriteString("Merged stage-1 raw memories (stable ascending thread-id order):\n\n")
	for _, memory := range retained {
		fmt.Fprintf(&body, "## Thread `%s`\n", memory.ThreadID)
		fmt.Fprintf(&body, "updated_at: %s\n", formatMemoryTime(memory.SourceUpdatedAt))
		fmt.Fprintf(&body, "cwd: %s\n", memory.CWD)
		fmt.Fprintf(&body, "rollout_path: %s\n", memory.RolloutPath)
		fmt.Fprintf(&body, "rollout_summary_file: %s.md\n\n", RolloutSummaryFileStem(memory))
		body.WriteString(strings.TrimSpace(memory.RawMemory))
		body.WriteString("\n\n")
	}
	return os.WriteFile(RawMemoriesFile(root), []byte(body.String()), 0o666)
}

func SyncRolloutSummaries(root string, values []state.Stage1Output, limit int) error {
	if err := EnsureLayout(root); err != nil {
		return err
	}
	retained := retainedStage1Outputs(values, limit)
	keep := make(map[string]struct{}, len(retained))
	for _, memory := range retained {
		keep[RolloutSummaryFileStem(memory)] = struct{}{}
	}
	if err := pruneRolloutSummaries(root, keep); err != nil {
		return err
	}
	for _, memory := range retained {
		if err := writeRolloutSummary(root, memory); err != nil {
			return err
		}
	}
	return nil
}

func RolloutSummaryFileStem(memory state.Stage1Output) string {
	timestamp := memory.SourceUpdatedAt.UTC()
	hashSeed := fallbackThreadHash(memory.ThreadID)
	if parsed, err := uuid.Parse(memory.ThreadID); err == nil {
		hashSeed = binary.BigEndian.Uint32(parsed[12:16])
		if parsedTimestamp, ok := uuidTimestamp(parsed); ok {
			timestamp = parsedTimestamp
		}
	}
	shortHashValue := hashSeed % shortHashSpace
	shortHash := [4]byte{}
	for index := len(shortHash) - 1; index >= 0; index-- {
		shortHash[index] = shortHashAlphabet[shortHashValue%uint32(len(shortHashAlphabet))]
		shortHashValue /= uint32(len(shortHashAlphabet))
	}
	prefix := timestamp.UTC().Format("2006-01-02T15-04-05") + "-" + string(shortHash[:])
	slug := sanitizeRolloutSlug(memory.RolloutSlug)
	if slug == "" {
		return prefix
	}
	return prefix + "-" + slug
}

func retainedStage1Outputs(values []state.Stage1Output, limit int) []state.Stage1Output {
	if limit <= 0 {
		return values[:0]
	}
	if len(values) <= limit {
		return values
	}
	return values[:limit]
}

func pruneRolloutSummaries(root string, keep map[string]struct{}) error {
	entries, err := os.ReadDir(RolloutSummariesDir(root))
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	for _, entry := range entries {
		name := entry.Name()
		stem, ok := strings.CutSuffix(name, ".md")
		if !ok {
			continue
		}
		if _, exists := keep[stem]; exists {
			continue
		}
		if err := os.Remove(filepath.Join(RolloutSummariesDir(root), name)); err != nil && !os.IsNotExist(err) {
			// Rust treats individual prune failures as non-fatal so remaining
			// canonical summaries can still be synchronized.
			continue
		}
	}
	return nil
}

func writeRolloutSummary(root string, memory state.Stage1Output) error {
	var body strings.Builder
	fmt.Fprintf(&body, "thread_id: %s\n", memory.ThreadID)
	fmt.Fprintf(&body, "updated_at: %s\n", formatMemoryTime(memory.SourceUpdatedAt))
	fmt.Fprintf(&body, "rollout_path: %s\n", memory.RolloutPath)
	fmt.Fprintf(&body, "cwd: %s\n", memory.CWD)
	if memory.GitBranch != "" {
		fmt.Fprintf(&body, "git_branch: %s\n", memory.GitBranch)
	}
	body.WriteByte('\n')
	body.WriteString(memory.RolloutSummary)
	body.WriteByte('\n')
	path := filepath.Join(RolloutSummariesDir(root), RolloutSummaryFileStem(memory)+".md")
	return os.WriteFile(path, []byte(body.String()), 0o666)
}

func formatMemoryTime(value time.Time) string {
	return value.UTC().Format("2006-01-02T15:04:05.999999999+00:00")
}

func sanitizeRolloutSlug(value string) string {
	var result strings.Builder
	result.Grow(rolloutSlugMaxLen)
	for _, char := range value {
		if result.Len() >= rolloutSlugMaxLen {
			break
		}
		if char >= 'A' && char <= 'Z' {
			result.WriteRune(char + ('a' - 'A'))
		} else if (char >= 'a' && char <= 'z') || (char >= '0' && char <= '9') {
			result.WriteRune(char)
		} else {
			result.WriteByte('_')
		}
	}
	return strings.TrimRight(result.String(), "_")
}

func fallbackThreadHash(threadID string) uint32 {
	var value uint32
	for _, char := range []byte(threadID) {
		value = value*31 + uint32(char)
	}
	return value
}

func uuidTimestamp(value uuid.UUID) (time.Time, bool) {
	switch value.Version() {
	case 1, 2:
		ticks := uint64(binary.BigEndian.Uint16(value[6:8])&0x0fff)<<48 |
			uint64(binary.BigEndian.Uint16(value[4:6]))<<32 |
			uint64(binary.BigEndian.Uint32(value[0:4]))
		return gregorianUUIDTime(ticks)
	case 6:
		ticks := uint64(binary.BigEndian.Uint32(value[0:4]))<<28 |
			uint64(binary.BigEndian.Uint16(value[4:6]))<<12 |
			uint64(binary.BigEndian.Uint16(value[6:8])&0x0fff)
		return gregorianUUIDTime(ticks)
	case 7:
		milliseconds := uint64(value[0])<<40 | uint64(value[1])<<32 |
			uint64(value[2])<<24 | uint64(value[3])<<16 |
			uint64(value[4])<<8 | uint64(value[5])
		if milliseconds > uint64(^uint64(0)>>1) {
			return time.Time{}, false
		}
		return time.UnixMilli(int64(milliseconds)).UTC(), true
	default:
		return time.Time{}, false
	}
}

func gregorianUUIDTime(ticks uint64) (time.Time, bool) {
	const gregorianToUnix100ns = uint64(0x01b21dd213814000)
	if ticks < gregorianToUnix100ns {
		return time.Time{}, false
	}
	unix100ns := ticks - gregorianToUnix100ns
	seconds := unix100ns / 10_000_000
	nanoseconds := (unix100ns % 10_000_000) * 100
	if seconds > uint64(^uint64(0)>>1) {
		return time.Time{}, false
	}
	return time.Unix(int64(seconds), int64(nanoseconds)).UTC(), true
}
