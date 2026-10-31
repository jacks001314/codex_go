package rollout

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// MigrationJournalDirectory mirrors Rust publish.rs
// MIGRATION_JOURNAL_DIRECTORY ("rollout-migrations").
const MigrationJournalDirectory = "rollout-migrations"

// CanonicalizeRollout converts one legacy rollout into staged paginated JSONL,
// mirroring Rust LegacyRolloutCanonicalizer + publish helpers:
//
//   - reads the source rollout (plain or .jsonl.zst),
//   - replays legacy records into canonical paginated JSONL,
//   - writes a staged paginated rollout at ".{name}.paginated.tmp",
//   - writes the durable ".pending" journal under rollout-migrations/,
//   - atomically renames the staged file over the source,
//   - removes the journal only after the publish is durable.
//
// On any failure the source rollout is left untouched; a leftover staged file
// is removed so a later run can retry.
func CanonicalizeRollout(codexHome string, path string) error {
	_, err := CanonicalizeRolloutWithStats(codexHome, path)
	return err
}

// CanonicalizeRolloutWithStats migrates one legacy rollout and reports the byte
// count Rust's migration rate limiter accounts for that file: the source bytes,
// the header record read twice, and the staged rollout written and projected.
func CanonicalizeRolloutWithStats(codexHome string, path string) (uint64, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return 0, errors.New("rollout path is required")
	}
	source, err := os.Open(path)
	if err != nil {
		return 0, fmt.Errorf("open source rollout: %w", err)
	}
	sourceInfo, statErr := source.Stat()
	source.Close()
	if statErr != nil {
		return 0, fmt.Errorf("stat source rollout: %w", statErr)
	}
	if sourceInfo.Size() == 0 {
		return 0, errors.New("rollout is empty; nothing to migrate")
	}

	compressed := strings.HasSuffix(strings.ToLower(path), ".jsonl.zst")
	rawLines, decompressedBytes, err := readCanonicalReplayLines(path)
	if err != nil {
		return 0, fmt.Errorf("load legacy rollout: %w", err)
	}
	meta, headerTimestamp, err := canonicalHeaderSessionMeta(rawLines)
	if err != nil {
		return 0, err
	}
	if strings.EqualFold(strings.TrimSpace(meta.HistoryMode), "paginated") {
		return 0, errors.New("rollout is already paginated")
	}
	sourceBytes := uint64(sourceInfo.Size())
	if compressed {
		// Rust inflates a .jsonl.zst rollout twice before rewriting it: once
		// to reach the header and once to stream the canonical result. The
		// migration rate limiter charges the compressed file plus both
		// decompressed passes.
		sourceBytes += 2 * decompressedBytes
	}

	stagedPath, err := stagedRolloutPath(path)
	if err != nil {
		return 0, err
	}
	journalPath, err := migrationJournalPath(codexHome, strings.TrimSpace(meta.ID))
	if err != nil {
		return 0, err
	}
	_ = os.Remove(stagedPath)
	file, err := os.OpenFile(stagedPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, fmt.Errorf("create staged rollout: %w", err)
	}
	bytesProcessed, finalOrdinal, writeErr := writeCanonicalReplay(file, meta, headerTimestamp, rawLines, sourceBytes)
	closeErr := file.Close()
	if writeErr != nil {
		_ = os.Remove(stagedPath)
		return 0, writeErr
	}
	if closeErr != nil {
		_ = os.Remove(stagedPath)
		return 0, fmt.Errorf("close staged rollout: %w", closeErr)
	}
	if isSubagentRollout(meta) {
		// Mirrors Rust rewrite_subagent_history_boundary: a migrated subagent
		// rollout marks the ordinal from which its own history starts so
		// SQLite projection and resume skip the copied parent history prefix.
		if err := rewriteStagedSubagentBoundary(stagedPath, finalOrdinal); err != nil {
			_ = os.Remove(stagedPath)
			return 0, err
		}
	}

	if err := writeMigrationJournal(journalPath); err != nil {
		_ = os.Remove(stagedPath)
		return 0, err
	}
	if compressed {
		// Mirrors Rust compress_rollout_to_path + publish: the staged plain
		// JSONL is compressed to .{name}.paginated.zst.tmp and that artifact is
		// renamed over the compressed source.
		compressedStagedPath, err := stagedRolloutPathWithSuffix(path, "paginated.zst")
		if err != nil {
			_ = os.Remove(stagedPath)
			return 0, err
		}
		if err := compressRolloutToPath(stagedPath, compressedStagedPath); err != nil {
			_ = os.Remove(stagedPath)
			return 0, err
		}
		_ = os.Remove(stagedPath)
		if err := preserveRolloutAccessMetadata(path, compressedStagedPath, sourceInfo); err != nil {
			_ = os.Remove(compressedStagedPath)
			return 0, fmt.Errorf("preserve rollout access metadata: %w", err)
		}
		if err := os.Rename(compressedStagedPath, path); err != nil {
			_ = os.Remove(compressedStagedPath)
			return 0, fmt.Errorf("publish compressed staged rollout: %w", err)
		}
	} else {
		if err := preserveRolloutAccessMetadata(path, stagedPath, sourceInfo); err != nil {
			_ = os.Remove(stagedPath)
			return 0, fmt.Errorf("preserve rollout access metadata: %w", err)
		}
		if err := os.Rename(stagedPath, path); err != nil {
			_ = os.Remove(stagedPath)
			return 0, fmt.Errorf("publish staged rollout: %w", err)
		}
	}
	if err := os.Remove(journalPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return 0, fmt.Errorf("remove migration journal: %w", err)
	}
	return bytesProcessed, nil
}

// canonicalHeaderSessionMeta reads the head session metadata record that the
// canonical writer replays first.
func canonicalHeaderSessionMeta(rawLines [][]byte) (*SessionMeta, string, error) {
	for _, raw := range rawLines {
		var line Line
		if err := unmarshalLine(raw, &line); err != nil {
			continue
		}
		if line.Meta == nil {
			continue
		}
		return line.Meta, line.Timestamp, nil
	}
	return nil, "", errors.New("rollout contains no session metadata")
}

func firstSessionMetaFromLines(lines []Line) *SessionMeta {
	for i := range lines {
		if lines[i].Meta != nil {
			return lines[i].Meta
		}
	}
	return nil
}

// stagedRolloutPath mirrors Rust staged_rollout_path: the staged file sits next
// to the source as ".{name}.paginated.tmp".
func stagedRolloutPath(rolloutPath string) (string, error) {
	return stagedRolloutPathWithSuffix(rolloutPath, "paginated")
}

func stagedRolloutPathWithSuffix(rolloutPath string, suffix string) (string, error) {
	name := filepath.Base(rolloutPath)
	if strings.TrimSpace(name) == "" {
		return "", errors.New("rollout path has no valid filename")
	}
	return filepath.Join(filepath.Dir(rolloutPath), "."+name+"."+suffix+".tmp"), nil
}

func compressRolloutToPath(plainPath string, compressedPath string) error {
	data, err := os.ReadFile(plainPath)
	if err != nil {
		return fmt.Errorf("read staged rollout for compression: %w", err)
	}
	encoder, err := zstd.NewWriter(nil)
	if err != nil {
		return fmt.Errorf("create zstd encoder: %w", err)
	}
	defer encoder.Close()
	encoded := encoder.EncodeAll(data, nil)
	if err := os.WriteFile(compressedPath, encoded, 0o600); err != nil {
		return fmt.Errorf("write compressed staged rollout: %w", err)
	}
	return nil
}

// migrationJournalPath mirrors Rust migration_journal_path.
func migrationJournalPath(codexHome string, threadID string) (string, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return "", errors.New("rollout thread id is required for the migration journal")
	}
	return filepath.Join(codexHome, MigrationJournalDirectory, threadID+".pending"), nil
}

// writeMigrationJournal durably marks a thread as pending migration.
func writeMigrationJournal(journalPath string) error {
	parent := filepath.Dir(journalPath)
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return fmt.Errorf("create migration journal directory: %w", err)
	}
	file, err := os.OpenFile(journalPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create migration journal: %w", err)
	}
	if err := file.Sync(); err != nil {
		file.Close()
		return fmt.Errorf("sync migration journal: %w", err)
	}
	return file.Close()
}

// isSubagentRollout mirrors Rust migrate_rollout_path's RolloutMigrationKind
// detection: a non-memory-consolidation session whose source is a subagent or
// whose thread_source is "subagent" is treated as a subagent rollout.
func isSubagentRollout(meta *SessionMeta) bool {
	if meta == nil {
		return false
	}
	source := strings.ToLower(strings.TrimSpace(meta.Source))
	threadSource := strings.ToLower(strings.TrimSpace(meta.ThreadSource))
	if source == "internal_memory_consolidation" || threadSource == "memory_consolidation" {
		return false
	}
	if strings.HasPrefix(source, "subagent") || strings.Contains(source, "sub_agent") {
		return true
	}
	if threadSource == "subagent" || threadSource == "subagentreview" || threadSource == "subagentcompact" || threadSource == "subagentthreadspawn" || threadSource == "subagentother" {
		return true
	}
	return false
}

// rewriteStagedSubagentBoundary mirrors Rust rewrite_subagent_history_boundary:
// it rewrites the staged rollout's head session_meta to carry the ordinal from
// which the subagent's own history starts (the canonical writer's final
// ordinal, i.e. the first ordinal after all copied parent history).
func rewriteStagedSubagentBoundary(stagedPath string, boundary uint64) error {
	data, err := os.ReadFile(stagedPath)
	if err != nil {
		return fmt.Errorf("read staged rollout for subagent boundary: %w", err)
	}
	lines := strings.SplitN(string(data), "\n", 2)
	if len(lines) == 0 {
		return errors.New("staged rollout is empty")
	}
	var head Line
	if err := json.Unmarshal([]byte(lines[0]), &head); err != nil {
		return fmt.Errorf("parse staged head: %w", err)
	}
	// The staged head is serialized by Line.MarshalJSON: session_meta lines
	// carry the SessionMeta under the "payload" key.
	if head.Meta == nil && len(head.Payload) == 0 {
		return errors.New("staged rollout head is not session metadata")
	}
	meta := head.Meta
	if meta == nil {
		var parsed SessionMeta
		if err := json.Unmarshal(head.Payload, &parsed); err != nil {
			return fmt.Errorf("parse staged head session metadata: %w", err)
		}
		meta = &parsed
	}
	meta.SubagentHistoryStartOrdinal = &boundary
	head.Meta = meta
	encoded, err := json.Marshal(head)
	if err != nil {
		return fmt.Errorf("marshal rewritten head: %w", err)
	}
	rewritten := string(encoded)
	if len(lines) == 2 {
		rewritten += "\n" + lines[1]
	}
	if err := os.WriteFile(stagedPath, []byte(rewritten), 0o600); err != nil {
		return fmt.Errorf("rewrite staged subagent boundary: %w", err)
	}
	return nil
}
