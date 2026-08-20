package state

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"codex_go/rollout"
)

type BackfillStatus string

const (
	BackfillPending  BackfillStatus = "pending"
	BackfillRunning  BackfillStatus = "running"
	BackfillComplete BackfillStatus = "complete"
)

const (
	defaultBackfillLeaseSeconds = int64(900)
	defaultBackfillBatchSize    = 200
	defaultBackfillWaitTimeout  = 30 * time.Second
	defaultBackfillPollInterval = time.Second
)

type BackfillState struct {
	Status        BackfillStatus
	LastWatermark *string
	LastSuccessAt *int64
}

type BackfillStats struct {
	Scanned  int
	Upserted int
	Failed   int
}

type RolloutBackfillOptions struct {
	LeaseSeconds int64
	BatchSize    int
	WaitTimeout  time.Duration
	PollInterval time.Duration
}

func (o RolloutBackfillOptions) withDefaults() RolloutBackfillOptions {
	if o.LeaseSeconds <= 0 {
		o.LeaseSeconds = defaultBackfillLeaseSeconds
	}
	if o.BatchSize <= 0 {
		o.BatchSize = defaultBackfillBatchSize
	}
	if o.WaitTimeout <= 0 {
		o.WaitTimeout = defaultBackfillWaitTimeout
	}
	if o.PollInterval <= 0 {
		o.PollInterval = defaultBackfillPollInterval
	}
	return o
}

func (r *StateRuntime) GetBackfillState(ctx context.Context) (BackfillState, error) {
	if r == nil || r.stateDB == nil {
		return BackfillState{}, errors.New("state runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := r.ensureBackfillState(ctx); err != nil {
		return BackfillState{}, err
	}
	var status string
	var watermark sql.NullString
	var success sql.NullInt64
	if err := r.stateDB.QueryRowContext(ctx, `
SELECT status, last_watermark, last_success_at
FROM backfill_state
WHERE id = 1`).Scan(&status, &watermark, &success); err != nil {
		return BackfillState{}, fmt.Errorf("read backfill state: %w", err)
	}
	state := BackfillState{Status: BackfillStatus(status)}
	if watermark.Valid {
		state.LastWatermark = &watermark.String
	}
	if success.Valid {
		state.LastSuccessAt = &success.Int64
	}
	return state, nil
}

func (r *StateRuntime) TryClaimBackfill(ctx context.Context, leaseSeconds int64) (bool, error) {
	if r == nil || r.stateDB == nil {
		return false, errors.New("state runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := r.ensureBackfillState(ctx); err != nil {
		return false, err
	}
	now := time.Now().Unix()
	cutoff := now - max(leaseSeconds, 0)
	result, err := r.stateDB.ExecContext(ctx, `
UPDATE backfill_state
SET status = ?, updated_at = ?
WHERE id = 1
  AND status != ?
  AND (status != ? OR updated_at <= ?)`, BackfillRunning, now, BackfillComplete, BackfillRunning, cutoff)
	if err != nil {
		return false, fmt.Errorf("claim rollout backfill: %w", err)
	}
	rows, err := result.RowsAffected()
	return rows == 1, err
}

func (r *StateRuntime) MarkBackfillRunning(ctx context.Context) error {
	return r.updateBackfillStatus(ctx, `
UPDATE backfill_state SET status = ?, updated_at = ? WHERE id = 1`, BackfillRunning, time.Now().Unix())
}

func (r *StateRuntime) CheckpointBackfill(ctx context.Context, watermark string) error {
	return r.updateBackfillStatus(ctx, `
UPDATE backfill_state
SET status = ?, last_watermark = ?, updated_at = ?
WHERE id = 1`, BackfillRunning, watermark, time.Now().Unix())
}

func (r *StateRuntime) MarkBackfillComplete(ctx context.Context, watermark *string) error {
	if r == nil || r.stateDB == nil {
		return errors.New("state runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := r.ensureBackfillState(ctx); err != nil {
		return err
	}
	now := time.Now().Unix()
	_, err := r.stateDB.ExecContext(ctx, `
UPDATE backfill_state
SET status = ?, last_watermark = COALESCE(?, last_watermark),
    last_success_at = ?, updated_at = ?
WHERE id = 1`, BackfillComplete, nullableStringPointer(watermark), now, now)
	if err != nil {
		return fmt.Errorf("complete rollout backfill: %w", err)
	}
	return nil
}

func (r *StateRuntime) updateBackfillStatus(ctx context.Context, query string, args ...any) error {
	if r == nil || r.stateDB == nil {
		return errors.New("state runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := r.ensureBackfillState(ctx); err != nil {
		return err
	}
	if _, err := r.stateDB.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("update rollout backfill: %w", err)
	}
	return nil
}

// WaitForRolloutBackfill enforces Rust's startup gate. One process owns the
// lease while other processes poll until the projection is complete.
func WaitForRolloutBackfill(ctx context.Context, runtime *StateRuntime, codexHome string, options RolloutBackfillOptions) error {
	if ctx == nil {
		ctx = context.Background()
	}
	options = options.withDefaults()
	deadline := time.NewTimer(options.WaitTimeout)
	defer deadline.Stop()
	for {
		backfillState, err := runtime.GetBackfillState(ctx)
		if err != nil {
			return err
		}
		if backfillState.Status == BackfillComplete {
			return nil
		}
		if _, _, err := RunRolloutBackfill(ctx, runtime, codexHome, options); err != nil {
			return err
		}
		backfillState, err = runtime.GetBackfillState(ctx)
		if err != nil {
			return err
		}
		if backfillState.Status == BackfillComplete {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for state db backfill at %s after %s (status: %s)", codexHome, options.WaitTimeout, backfillState.Status)
		case <-time.After(options.PollInterval):
		}
	}
}

// RunRolloutBackfill attempts one lease-protected rollout inventory pass. The
// boolean result reports whether this runtime acquired the worker slot.
func RunRolloutBackfill(ctx context.Context, runtime *StateRuntime, codexHome string, options RolloutBackfillOptions) (BackfillStats, bool, error) {
	if runtime == nil {
		return BackfillStats{}, false, errors.New("state runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	options = options.withDefaults()
	state, err := runtime.GetBackfillState(ctx)
	if err != nil {
		return BackfillStats{}, false, err
	}
	if state.Status == BackfillComplete {
		return BackfillStats{}, false, nil
	}
	claimed, err := runtime.TryClaimBackfill(ctx, options.LeaseSeconds)
	if err != nil || !claimed {
		return BackfillStats{}, false, err
	}
	state, err = runtime.GetBackfillState(ctx)
	if err != nil {
		return BackfillStats{}, true, err
	}

	entries := collectBackfillRollouts(codexHome)
	if state.LastWatermark != nil {
		watermark := *state.LastWatermark
		entries = filterBackfillAfter(entries, watermark)
	}
	stats := BackfillStats{}
	lastWatermark := state.LastWatermark
	for start := 0; start < len(entries); start += options.BatchSize {
		end := min(start+options.BatchSize, len(entries))
		batch := entries[start:end]
		for _, entry := range batch {
			stats.Scanned++
			metadata, extractErr := extractRolloutThreadMetadata(entry.path, entry.archived, runtime.defaultProvider)
			if extractErr != nil {
				stats.Failed++
				continue
			}
			if upsertErr := runtime.upsertRolloutThread(ctx, metadata); upsertErr != nil {
				stats.Failed++
				continue
			}
			stats.Upserted++
		}
		watermark := batch[len(batch)-1].watermark
		if err := runtime.CheckpointBackfill(ctx, watermark); err == nil {
			lastWatermark = &watermark
		}
	}
	if err := runtime.MarkBackfillComplete(ctx, lastWatermark); err != nil {
		return stats, true, err
	}
	return stats, true, nil
}

// ReconcileRollout rebuilds one thread row from its authoritative rollout.
func (r *StateRuntime) ReconcileRollout(ctx context.Context, path string, archived bool) error {
	if r == nil {
		return errors.New("state runtime is nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	metadata, err := extractRolloutThreadMetadata(path, archived, r.defaultProvider)
	if err != nil {
		return err
	}
	return r.upsertRolloutThread(ctx, metadata)
}

type backfillRollout struct {
	watermark string
	path      string
	archived  bool
}

func collectBackfillRollouts(codexHome string) []backfillRollout {
	var entries []backfillRollout
	for _, root := range []struct {
		name     string
		archived bool
	}{
		{name: rollout.SessionsSubdir},
		{name: rollout.ArchivedSessionsSubdir, archived: true},
	} {
		paths, err := rollout.CollectRolloutPaths(filepath.Join(codexHome, root.name))
		if err != nil {
			continue
		}
		for _, path := range paths {
			relative, err := filepath.Rel(codexHome, path)
			if err != nil {
				relative = path
			}
			entries = append(entries, backfillRollout{
				watermark: filepath.ToSlash(relative),
				path:      path,
				archived:  root.archived,
			})
		}
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].watermark < entries[j].watermark })
	return entries
}

func filterBackfillAfter(entries []backfillRollout, watermark string) []backfillRollout {
	index := sort.Search(len(entries), func(i int) bool { return entries[i].watermark > watermark })
	return entries[index:]
}

type rolloutThreadMetadata struct {
	id, path, source, historyMode, threadSource  string
	agentNickname, agentRole, agentPath          string
	modelProvider, model, reasoningEffort, cwd   string
	cliVersion, title, preview, firstUserMessage string
	sandboxPolicy, approvalMode, memoryMode      string
	gitSHA, gitBranch, gitOriginURL              string
	createdAt, updatedAt, recencyAt              time.Time
	tokensUsed                                   int64
	archived                                     bool
}

func extractRolloutThreadMetadata(path string, archived bool, defaultProvider string) (rolloutThreadMetadata, error) {
	lines, _, err := rollout.Load(path)
	if err != nil {
		return rolloutThreadMetadata{}, err
	}
	if len(lines) == 0 {
		return rolloutThreadMetadata{}, fmt.Errorf("empty session file: %s", path)
	}
	metadata := rolloutThreadMetadata{
		path: filepath.Clean(rollout.PlainRolloutPath(path)), historyMode: "legacy",
		source: "unknown", modelProvider: defaultProvider, sandboxPolicy: "read-only",
		approvalMode: "on-request", memoryMode: "enabled", archived: archived,
	}
	for i := range lines {
		line := &lines[i]
		if line.Meta != nil {
			applySessionMetaToBackfill(&metadata, line.Meta, metadata.id == "")
			continue
		}
		if line.Type == "turn_context" && len(line.TurnContext) > 0 {
			applyTurnContextToBackfill(&metadata, line.TurnContext)
			continue
		}
		if line.Type == "event_msg" && len(line.Payload) > 0 {
			applyEventToBackfill(&metadata, line.Payload)
		}
	}
	if metadata.id == "" {
		return rolloutThreadMetadata{}, fmt.Errorf("rollout missing session metadata: %s", path)
	}
	if metadata.createdAt.IsZero() {
		if createdAt, ok := rollout.ParseTimestampFromFilename(filepath.Base(rollout.PlainRolloutPath(path))); ok {
			metadata.createdAt = createdAt
		} else {
			return rolloutThreadMetadata{}, fmt.Errorf("rollout missing creation timestamp: %s", path)
		}
	}
	metadata.updatedAt = metadata.createdAt
	if info, statErr := os.Stat(path); statErr == nil {
		metadata.updatedAt = info.ModTime().UTC()
	}
	metadata.recencyAt = metadata.updatedAt
	if archived {
		// archived_at is derived from the authoritative rollout mtime.
		metadata.archived = true
	}
	return metadata, nil
}

func applySessionMetaToBackfill(metadata *rolloutThreadMetadata, meta *rollout.SessionMeta, first bool) {
	if metadata == nil || meta == nil || (!first && meta.ID != metadata.id) {
		return
	}
	if first {
		metadata.id = meta.ID
		if parsed, err := time.Parse(time.RFC3339Nano, meta.Timestamp); err == nil {
			metadata.createdAt = parsed.UTC()
		}
		if value := strings.TrimSpace(meta.HistoryMode); value != "" {
			metadata.historyMode = value
		}
	}
	assignNonEmpty(&metadata.source, meta.Source)
	assignNonEmpty(&metadata.threadSource, meta.ThreadSource)
	metadata.agentNickname = meta.AgentNickname
	metadata.agentRole = meta.AgentRole
	metadata.agentPath = meta.AgentPath
	assignNonEmpty(&metadata.modelProvider, meta.ModelProvider)
	assignNonEmpty(&metadata.cwd, meta.CWD)
	assignNonEmpty(&metadata.cliVersion, meta.CLIVersion)
	assignNonEmpty(&metadata.memoryMode, meta.MemoryMode)
	applyGitToBackfill(metadata, meta.Git)
}

func applyTurnContextToBackfill(metadata *rolloutThreadMetadata, raw json.RawMessage) {
	var values map[string]any
	if json.Unmarshal(raw, &values) != nil {
		return
	}
	if metadata.cwd == "" {
		assignNonEmpty(&metadata.cwd, stringFromJSON(values["cwd"]))
	}
	assignNonEmpty(&metadata.model, stringFromJSON(values["model"]))
	assignNonEmpty(&metadata.reasoningEffort, stringFromJSON(values["effort"]))
	if value, ok := values["permission_profile"]; ok {
		metadata.sandboxPolicy = serializedJSONValue(value)
	} else if value, ok := values["sandbox_policy"]; ok {
		metadata.sandboxPolicy = serializedJSONValue(value)
	}
	assignNonEmpty(&metadata.approvalMode, stringFromJSON(values["approval_policy"]))
}

func applyEventToBackfill(metadata *rolloutThreadMetadata, raw json.RawMessage) {
	var event map[string]any
	if json.Unmarshal(raw, &event) != nil {
		return
	}
	kind := normalizeBackfillKind(stringFromJSON(event["type"]))
	switch kind {
	case "token_count":
		if info, ok := event["info"].(map[string]any); ok {
			if total, ok := info["total_token_usage"].(map[string]any); ok {
				metadata.tokensUsed = max(jsonInt64(total["total_tokens"]), 0)
			}
		}
	case "user_message":
		applyUserPreviewToBackfill(metadata, userTextFromEvent(event), true)
	case "item_completed":
		item, _ := event["item"].(map[string]any)
		itemType := normalizeBackfillKind(stringFromJSON(item["type"]))
		if itemType == "user_message" {
			applyUserPreviewToBackfill(metadata, userTextFromEvent(item), true)
		}
	case "thread_goal_updated":
		goal, _ := event["goal"].(map[string]any)
		if metadata.preview == "" {
			metadata.preview = strings.TrimSpace(stringFromJSON(goal["objective"]))
		}
	case "thread_settings_applied":
		settings, _ := event["thread_settings"].(map[string]any)
		assignNonEmpty(&metadata.model, stringFromJSON(settings["model"]))
		assignNonEmpty(&metadata.modelProvider, stringFromJSON(settings["model_provider_id"]))
		assignNonEmpty(&metadata.reasoningEffort, stringFromJSON(settings["reasoning_effort"]))
		assignNonEmpty(&metadata.cwd, stringFromJSON(settings["cwd"]))
		if value, ok := settings["permission_profile"]; ok {
			metadata.sandboxPolicy = serializedJSONValue(value)
		}
		assignNonEmpty(&metadata.approvalMode, stringFromJSON(settings["approval_policy"]))
	}
}

func applyUserPreviewToBackfill(metadata *rolloutThreadMetadata, value string, title bool) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if metadata.firstUserMessage == "" {
		metadata.firstUserMessage = value
	}
	if metadata.preview == "" {
		metadata.preview = value
	}
	if title && metadata.title == "" {
		metadata.title = stripUserMessageEnvelope(value)
	}
}

func userTextFromEvent(values map[string]any) string {
	if value := strings.TrimSpace(stringFromJSON(values["message"])); value != "" {
		return value
	}
	if value := strings.TrimSpace(stringFromJSON(values["text"])); value != "" {
		return value
	}
	content, _ := values["content"].([]any)
	parts := make([]string, 0, len(content))
	for _, entry := range content {
		part, _ := entry.(map[string]any)
		if value := strings.TrimSpace(stringFromJSON(part["text"])); value != "" {
			parts = append(parts, value)
		}
	}
	return strings.Join(parts, "\n")
}

func (r *StateRuntime) upsertRolloutThread(ctx context.Context, metadata rolloutThreadMetadata) error {
	var existingHistoryMode, existingTitle, existingFirstUserMessage string
	var existingGitSHA, existingGitBranch, existingGitOriginURL sql.NullString
	existing := false
	err := r.stateDB.QueryRowContext(ctx, `
SELECT history_mode, title, first_user_message, git_sha, git_branch, git_origin_url
FROM threads WHERE id = ?`, metadata.id).Scan(
		&existingHistoryMode, &existingTitle, &existingFirstUserMessage,
		&existingGitSHA, &existingGitBranch, &existingGitOriginURL,
	)
	if err == nil {
		existing = true
	} else if err != sql.ErrNoRows {
		return fmt.Errorf("read existing rollout thread %s: %w", metadata.id, err)
	}
	if existing {
		existingTitleTrimmed := strings.TrimSpace(existingTitle)
		incomingTitleTrimmed := strings.TrimSpace(metadata.title)
		if existingTitleTrimmed != "" && strings.TrimSpace(existingFirstUserMessage) != existingTitleTrimmed &&
			(incomingTitleTrimmed == "" || strings.TrimSpace(metadata.firstUserMessage) == incomingTitleTrimmed) {
			metadata.title = existingTitle
		}
		if metadata.historyMode == "paginated" && existingHistoryMode == "paginated" {
			metadata.gitSHA = nullStringValue(existingGitSHA)
			metadata.gitBranch = nullStringValue(existingGitBranch)
			metadata.gitOriginURL = nullStringValue(existingGitOriginURL)
		}
	}
	updatedMillis := r.allocateThreadTimestamp(&r.threadUpdatedAt, metadata.updatedAt.UnixMilli())
	recencyMillis := r.allocateThreadTimestamp(&r.threadRecencyAt, metadata.recencyAt.UnixMilli())
	createdMillis := metadata.createdAt.UnixMilli()
	archivedAt := any(nil)
	if metadata.archived {
		archivedAt = metadata.updatedAt.Unix()
	}
	_, err = r.stateDB.ExecContext(ctx, `
INSERT INTO threads (
    id, rollout_path, created_at, updated_at, recency_at,
    created_at_ms, updated_at_ms, recency_at_ms,
    source, history_mode, thread_source, agent_nickname, agent_role, agent_path,
    model_provider, model, reasoning_effort, cwd, cli_version, title, name, preview,
    sandbox_policy, approval_mode, tokens_used, first_user_message,
    archived, archived_at, thread_section_id, section_position, section_entered_at_ms,
    git_sha, git_branch, git_origin_url, memory_mode
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, NULL, ?, ?, ?, ?, ?, ?, ?, NULL, NULL, NULL, ?, ?, ?, ?)
ON CONFLICT(id) DO UPDATE SET
    rollout_path = excluded.rollout_path,
    created_at = excluded.created_at,
    updated_at = excluded.updated_at,
    recency_at = threads.recency_at,
    created_at_ms = excluded.created_at_ms,
    updated_at_ms = excluded.updated_at_ms,
    recency_at_ms = threads.recency_at_ms,
    source = excluded.source,
    history_mode = excluded.history_mode,
    thread_source = excluded.thread_source,
    agent_nickname = excluded.agent_nickname,
    agent_role = excluded.agent_role,
    agent_path = excluded.agent_path,
    model_provider = excluded.model_provider,
    model = excluded.model,
    reasoning_effort = excluded.reasoning_effort,
    cwd = excluded.cwd,
    cli_version = excluded.cli_version,
    title = excluded.title,
    preview = COALESCE(NULLIF(excluded.preview, ''), threads.preview),
    sandbox_policy = excluded.sandbox_policy,
    approval_mode = excluded.approval_mode,
    tokens_used = excluded.tokens_used,
    first_user_message = excluded.first_user_message,
    archived = excluded.archived,
    archived_at = excluded.archived_at,
    git_sha = COALESCE(threads.git_sha, excluded.git_sha),
    git_branch = COALESCE(threads.git_branch, excluded.git_branch),
    git_origin_url = COALESCE(threads.git_origin_url, excluded.git_origin_url)`,
		metadata.id, metadata.path,
		metadata.createdAt.Unix(), metadata.updatedAt.Unix(), metadata.recencyAt.Unix(),
		createdMillis, updatedMillis, recencyMillis,
		metadata.source, metadata.historyMode, nullableString(metadata.threadSource),
		nullableString(metadata.agentNickname), nullableString(metadata.agentRole), nullableString(metadata.agentPath),
		metadata.modelProvider, nullableString(metadata.model), nullableString(metadata.reasoningEffort),
		metadata.cwd, metadata.cliVersion, metadata.title, metadata.preview,
		metadata.sandboxPolicy, metadata.approvalMode, metadata.tokensUsed, metadata.firstUserMessage,
		metadata.archived, archivedAt,
		nullableString(metadata.gitSHA), nullableString(metadata.gitBranch), nullableString(metadata.gitOriginURL), metadata.memoryMode)
	if err != nil {
		return fmt.Errorf("upsert rollout thread %s: %w", metadata.id, err)
	}
	if !existing || metadata.historyMode == "legacy" {
		if _, err := r.stateDB.ExecContext(ctx, `UPDATE threads SET memory_mode = ? WHERE id = ?`, metadata.memoryMode, metadata.id); err != nil {
			return fmt.Errorf("restore rollout thread memory mode %s: %w", metadata.id, err)
		}
	}
	// Rust #39273: paginated threads display `name`; preserve an existing name
	// or carry over the legacy display title when promoting, and repair missing
	// names when migration encounters an already-paginated rollout.
	if metadata.historyMode == "paginated" {
		name := strings.TrimSpace(metadata.title)
		if name == "" {
			name = strings.TrimSpace(existingTitle)
		}
		if name != "" {
			if _, err := r.stateDB.ExecContext(ctx, `
UPDATE threads SET name = ? WHERE id = ? AND (name IS NULL OR trim(name) = '')`, name, metadata.id); err != nil {
				return fmt.Errorf("promote rollout thread name %s: %w", metadata.id, err)
			}
		}
	}
	return nil
}

func (r *StateRuntime) allocateThreadTimestamp(counter interface {
	Load() int64
	CompareAndSwap(old, new int64) bool
}, requested int64) int64 {
	for {
		previous := counter.Load()
		candidate := requested
		if candidate <= previous {
			candidate = previous + 1
		}
		if counter.CompareAndSwap(previous, candidate) {
			return candidate
		}
	}
}

func applyGitToBackfill(metadata *rolloutThreadMetadata, git map[string]string) {
	if metadata == nil || git == nil {
		return
	}
	metadata.gitSHA = firstNonEmptyString(git["sha"], git["commit_hash"])
	metadata.gitBranch = git["branch"]
	metadata.gitOriginURL = firstNonEmptyString(git["origin_url"], git["repository_url"])
}

func normalizeBackfillKind(value string) string {
	compact := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(value)))
	switch compact {
	case "tokencount":
		return "token_count"
	case "usermessage":
		return "user_message"
	case "itemcompleted":
		return "item_completed"
	case "threadgoalupdated":
		return "thread_goal_updated"
	case "threadsettingsapplied":
		return "thread_settings_applied"
	default:
		return strings.ToLower(strings.TrimSpace(value))
	}
}

func stripUserMessageEnvelope(value string) string {
	const begin = "<user_message>"
	const end = "</user_message>"
	value = strings.TrimSpace(value)
	if strings.HasPrefix(value, begin) && strings.HasSuffix(value, end) {
		value = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(value, begin), end))
	}
	return value
}

func serializedJSONValue(value any) string {
	if text, ok := value.(string); ok {
		return text
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	return string(encoded)
}

func stringFromJSON(value any) string {
	text, _ := value.(string)
	return text
}

func jsonInt64(value any) int64 {
	switch typed := value.(type) {
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	case int64:
		return typed
	case int:
		return int64(typed)
	default:
		return 0
	}
}

func nullableString(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func nullableStringPointer(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullStringValue(value sql.NullString) string {
	if value.Valid {
		return value.String
	}
	return ""
}

func assignNonEmpty(target *string, value string) {
	if strings.TrimSpace(value) != "" {
		*target = value
	}
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
