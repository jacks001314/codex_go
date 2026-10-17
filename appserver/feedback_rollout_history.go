package appserver

// Go port of codex-rs/app-server/src/request_processors/feedback_rollout_history.rs
// (Rust #47653): a diagnostic report that includes logs also carries the
// inherited history its selected rollouts reference through `history_base`.
// Ancestor prefixes are read up to the recorded byte offset, deduplicated by
// immutable rollout id, and attached as decoded JSONL.

import (
	"log/slog"
	"path/filepath"
	"time"

	"codex_go/rollout"
)

const (
	// FeedbackMaxAttachmentBytes mirrors Rust `MAX_ATTACHMENT_BYTES`: one
	// attachment, and one inherited history prefix, may not exceed it.
	FeedbackMaxAttachmentBytes = 64 * 1024 * 1024
	// FeedbackMaxAttachmentsBytes mirrors Rust `MAX_ATTACHMENTS_BYTES`: the total
	// bytes of inherited history prefixes in one report.
	FeedbackMaxAttachmentsBytes = 126 * 1024 * 1024
	// FeedbackMaxHistoryRollouts mirrors Rust `MAX_HISTORY_ROLLOUTS`.
	FeedbackMaxHistoryRollouts = 64
	// feedbackHistoryLookupTimeout mirrors Rust `HISTORY_LOOKUP_TIMEOUT`.
	feedbackHistoryLookupTimeout = 5 * time.Second
)

// FeedbackHistoryBaseAttachments mirrors Rust `history_base_attachments`:
// follow nested `history_base` references from the selected rollout
// attachments, attach each ancestor's prefix up to the recorded byte offset,
// deduplicate ancestors (keeping the largest requested prefix), and skip
// rollouts that are already selected. Missing, unreadable or incomplete
// prefixes are best effort and never fail the report.
func FeedbackHistoryBaseAttachments(codexHome string, attachments []FeedbackAttachmentPath) []FeedbackAttachment {
	if len(attachments) == 0 {
		return nil
	}
	selected := make(map[string]struct{}, len(attachments))
	pending := make([]string, 0, len(attachments))
	for _, attachment := range attachments {
		if id, ok := feedbackRolloutIDFromPath(attachment.Path); ok {
			selected[id] = struct{}{}
			pending = append(pending, attachment.Path)
		}
	}
	if len(pending) == 0 {
		return nil
	}
	type historyAncestor struct {
		path string
		end  int64
	}
	positions := make(map[string]int)
	ancestors := make([]historyAncestor, 0, len(pending))
	deadline := time.Now().Add(feedbackHistoryLookupTimeout)
	for next := 0; next < len(pending); next++ {
		if time.Now().After(deadline) {
			slog.Error("feedback history lookup timed out")
			break
		}
		path := pending[next]
		meta, err := rollout.FirstSessionMeta(path)
		if err != nil {
			slog.Error("failed to read feedback history metadata", "path", path, "error", err)
			continue
		}
		if meta == nil || meta.HistoryBase == nil {
			continue
		}
		base := meta.HistoryBase
		if _, ok := selected[base.ThreadID]; ok {
			continue
		}
		// The wire field names an immutable rollout, not the owner's current thread.
		if base.EndByteOffset > uint64(FeedbackMaxAttachmentBytes) {
			continue
		}
		end := int64(base.EndByteOffset)
		if index, ok := positions[base.ThreadID]; ok {
			if end > ancestors[index].end {
				ancestors[index].end = end
			}
			continue
		}
		if len(positions) == FeedbackMaxHistoryRollouts {
			slog.Error("feedback history rollout limit reached")
			break
		}
		found, ok, err := rollout.FindRolloutPathByRolloutID(codexHome, base.ThreadID)
		if err != nil {
			slog.Error("failed to locate feedback history rollout", "rollout_id", base.ThreadID, "error", err)
			continue
		}
		if !ok {
			continue
		}
		positions[base.ThreadID] = len(ancestors)
		pending = append(pending, found)
		ancestors = append(ancestors, historyAncestor{path: found, end: end})
	}
	remaining := int64(FeedbackMaxAttachmentsBytes)
	out := make([]FeedbackAttachment, 0, len(ancestors))
	for _, ancestor := range ancestors {
		if ancestor.end > remaining {
			continue
		}
		buffer, ok, err := rollout.ReadRolloutPrefix(ancestor.path, ancestor.end)
		if err != nil {
			slog.Error("couldn't read feedback history prefix", "path", ancestor.path, "error", err)
			continue
		}
		if !ok || int64(len(buffer)) != ancestor.end {
			slog.Error("couldn't attach history: incomplete feedback history prefix", "path", ancestor.path)
			continue
		}
		remaining -= int64(len(buffer))
		out = append(out, FeedbackAttachment{
			Filename:    filepath.Base(rollout.PlainRolloutPath(ancestor.path)),
			ContentType: "application/jsonl",
			Buffer:      buffer,
		})
	}
	return out
}

// feedbackRolloutIDFromPath reports the immutable rollout id encoded by a
// rollout filename, mirroring Rust `rollout_id_from_path(..).is_some()`.
func feedbackRolloutIDFromPath(path string) (string, bool) {
	parsed, ok := rollout.ParseRolloutFileName(filepath.Base(path))
	if !ok {
		return "", false
	}
	return parsed.RolloutID, true
}
