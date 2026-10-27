package appserver

// Go port of codex-rs/app-server/src/request_processors/thread_memory.rs
// (Rust #56465): read `memory_summary.md` from a loaded thread's resolved
// memory version, honoring thread-scoped config overrides without falling back
// to another version. Missing, unreadable, or blank summaries are null.

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codex_go/config"
	"codex_go/features"
	"codex_go/memories"
	"codex_go/session"
	"codex_go/turn"
)

func (r *RuntimeRouter) handleThreadMemoryRead(request *Request) (*ThreadMemoryReadResponse, error) {
	var params ThreadMemoryReadParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	threadID := strings.TrimSpace(params.ThreadID)
	// Rust's `load_thread` resolves the core conversation handle from the
	// thread manager, so a thread that was never started or resumed in this
	// process is not readable.
	if err := r.requireLoadedThreadForRuntimeOp(threadID); err != nil {
		return nil, err
	}
	record, err := r.threadRecord(session.ThreadID(threadID), false, false)
	if err != nil {
		if errors.Is(err, session.ErrThreadNotFound) || strings.Contains(err.Error(), "thread not found") {
			return nil, jsonRPCInvalidRequest(fmt.Sprintf("thread not found: %s", threadID))
		}
		return nil, err
	}
	cfg, err := r.effectiveConfigForTurn(&turn.TurnStartParams{
		ThreadID: threadID,
		CWD:      strings.TrimSpace(record.Metadata.CWD),
		Config:   threadRecordConfigOverrides(record),
	})
	if err != nil {
		return nil, err
	}
	return &ThreadMemoryReadResponse{Summary: r.threadMemorySummary(cfg)}, nil
}

// threadMemorySummary mirrors Rust's `thread_memory_read` body: the memory
// summary is available only when the memory tool feature and
// `memories.use_memories` are both enabled.
func (r *RuntimeRouter) threadMemorySummary(cfg *config.Config) *string {
	if r == nil || cfg == nil || r.services.Config == nil {
		return nil
	}
	if !features.Enabled(cfg.FeatureSettings(), "memories") || !cfg.Memories().UseMemories {
		return nil
	}
	codexHome := strings.TrimSpace(r.services.Config.CodexHome())
	if codexHome == "" {
		return nil
	}
	path := filepath.Join(memories.RootForVersion(codexHome, cfg.Memories().MemoryVersion()), memories.MemorySummaryFilename)
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	summary := string(data)
	if strings.TrimSpace(summary) == "" {
		return nil
	}
	return &summary
}
