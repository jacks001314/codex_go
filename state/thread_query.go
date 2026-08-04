package state

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

type ThreadListRow struct {
	ID, RolloutPath                                   string
	CreatedAtMS, UpdatedAtMS, RecencyAtMS             int64
	Source, HistoryMode                               string
	ThreadSource, AgentNickname, AgentRole, AgentPath sql.NullString
	ModelProvider, Model, ReasoningEffort             sql.NullString
	CWD, CLIVersion, Title, Name, Preview             sql.NullString
	SandboxPolicy, ApprovalMode, FirstUserMessage     sql.NullString
	MemoryMode                                        sql.NullString
	TokensUsed                                        int64
	Archived                                          bool
	ArchivedAt                                        sql.NullInt64
	GitSHA, GitBranch, GitOriginURL                   sql.NullString
	SectionID, SectionName                            sql.NullString
	SectionPosition, SectionEnteredAtMS               sql.NullInt64
	ParentThreadID                                    sql.NullString
}

func (r *StateRuntime) SetThreadPreviewIfEmpty(ctx context.Context, threadID, preview string) (bool, error) {
	if r == nil || r.stateDB == nil {
		return false, errors.New("state runtime is nil")
	}
	preview = strings.TrimSpace(preview)
	if preview == "" {
		return false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	result, err := r.stateDB.ExecContext(ctx, `UPDATE threads SET preview = ? WHERE id = ? AND preview = ''`, preview, strings.TrimSpace(threadID))
	if err != nil {
		return false, fmt.Errorf("set empty thread preview: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("read thread preview update count: %w", err)
	}
	return rows > 0, nil
}

func (r *StateRuntime) ListThreadRows(ctx context.Context) ([]ThreadListRow, error) {
	if r == nil || r.stateDB == nil {
		return nil, errors.New("state runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return r.queryThreadRows(ctx, "", "")
}

// ListThreadRowsByName returns threads whose stored name or title exactly
// matches name within one archive collection, ordered by recency. It is the
// state-DB-only lookup used by local session archive commands before falling
// back to scanning rollouts (Rust 9c8f9ce897).
func (r *StateRuntime) ListThreadRowsByName(ctx context.Context, name string, archived bool) ([]ThreadListRow, error) {
	if r == nil || r.stateDB == nil {
		return nil, errors.New("state runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return r.queryThreadRows(ctx, "WHERE threads.archived = ? AND (threads.name = ? OR threads.title = ?)", "ORDER BY COALESCE(NULLIF(threads.recency_at_ms, 0), threads.updated_at_ms, threads.updated_at * 1000) DESC", archived, strings.TrimSpace(name), strings.TrimSpace(name))
}

func (r *StateRuntime) queryThreadRows(ctx context.Context, where string, order string, args ...any) ([]ThreadListRow, error) {
	query := `
SELECT
    threads.id,
    threads.rollout_path,
    COALESCE(threads.created_at_ms, threads.created_at * 1000),
    COALESCE(threads.updated_at_ms, threads.updated_at * 1000),
    COALESCE(NULLIF(threads.recency_at_ms, 0), threads.updated_at_ms, threads.updated_at * 1000),
    threads.source,
    threads.history_mode,
    threads.thread_source,
    threads.agent_nickname,
    threads.agent_role,
    threads.agent_path,
    threads.model_provider,
    threads.model,
    threads.reasoning_effort,
    threads.cwd,
    threads.cli_version,
    threads.title,
    threads.name,
    threads.preview,
    threads.sandbox_policy,
    threads.approval_mode,
    threads.tokens_used,
    threads.first_user_message,
    threads.memory_mode,
    threads.archived,
    threads.archived_at,
    threads.git_sha,
    threads.git_branch,
    threads.git_origin_url,
    threads.thread_section_id,
    thread_sections.name,
    threads.section_position,
    threads.section_entered_at_ms,
    thread_spawn_edges.parent_thread_id
FROM threads
LEFT JOIN thread_sections ON thread_sections.id = threads.thread_section_id
LEFT JOIN thread_spawn_edges ON thread_spawn_edges.child_thread_id = threads.id
` + where + "\n" + order
	rows, err := r.stateDB.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list thread rows: %w", err)
	}
	defer rows.Close()
	out := make([]ThreadListRow, 0)
	for rows.Next() {
		var item ThreadListRow
		if err := rows.Scan(
			&item.ID, &item.RolloutPath, &item.CreatedAtMS, &item.UpdatedAtMS, &item.RecencyAtMS,
			&item.Source, &item.HistoryMode, &item.ThreadSource, &item.AgentNickname, &item.AgentRole, &item.AgentPath,
			&item.ModelProvider, &item.Model, &item.ReasoningEffort, &item.CWD, &item.CLIVersion,
			&item.Title, &item.Name, &item.Preview, &item.SandboxPolicy, &item.ApprovalMode,
			&item.TokensUsed, &item.FirstUserMessage, &item.MemoryMode, &item.Archived, &item.ArchivedAt,
			&item.GitSHA, &item.GitBranch, &item.GitOriginURL, &item.SectionID, &item.SectionName,
			&item.SectionPosition, &item.SectionEnteredAtMS, &item.ParentThreadID,
		); err != nil {
			return nil, fmt.Errorf("scan thread row: %w", err)
		}
		out = append(out, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list thread rows: %w", err)
	}
	return out, nil
}
