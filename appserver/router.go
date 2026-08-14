package appserver

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"codex_go/agent"
	"codex_go/compact"
	"codex_go/memories"
	"codex_go/model"
	"codex_go/rollout"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/state"

	"github.com/google/uuid"
)

type Router struct {
	store      *session.Store
	now        func() time.Time
	spawnGraph agent.Store
	threads    *ThreadManager
	state      *state.StateRuntime
	// retainClientDeveloperMessages reports whether client-authored developer
	// messages should be marked in persisted rollout history (#38243). The
	// lower-level Router is feature-agnostic, so the runtime installs this
	// predicate.
	retainClientDeveloperMessages func() bool
}

func markClientAuthoredDeveloperItem(item *session.Item) {
	if item == nil || !strings.EqualFold(strings.TrimSpace(item.Role), "developer") {
		return
	}
	if item.Data == nil {
		item.Data = map[string]any{}
	}
	var metadata map[string]any
	switch value := item.Data["harness_metadata"].(type) {
	case json.RawMessage:
		_ = json.Unmarshal(value, &metadata)
	case string:
		if strings.TrimSpace(value) != "" {
			_ = json.Unmarshal([]byte(value), &metadata)
		}
	case map[string]any:
		metadata = cloneAnyMapForRouter(value)
	}
	if metadata == nil {
		metadata = map[string]any{}
	}
	metadata["client_authored"] = true
	if encoded, err := json.Marshal(metadata); err == nil {
		item.Data["harness_metadata"] = json.RawMessage(encoded)
	}
}

func NewRouter(store *session.Store) *Router {
	threads := NewThreadManager(nil)
	router := &Router{store: store, now: time.Now, threads: threads}
	if store != nil {
		codexHome := codexHomeFromSessionStore(store)
		store.SetPhysicalHistoryResolver(func(position session.HistoryPosition) (*session.ResolvedPhysicalHistory, error) {
			items, turns, err := rollout.ResolveHistoryPrefix(codexHome, rollout.HistoryPosition{
				ThreadID: string(position.ThreadID), EndOrdinalExclusive: position.EndOrdinalExclusive, EndByteOffset: position.EndByteOffset,
			})
			if err != nil {
				return nil, err
			}
			return &session.ResolvedPhysicalHistory{Items: items, RolloutTurns: turns}, nil
		})
	}
	return router
}

func (r *Router) Close() error {
	if r == nil {
		return nil
	}
	return r.threadManager().CloseLiveThreads()
}

func (r *Router) threadManager() *ThreadManager {
	if r.threads == nil {
		r.threads = NewThreadManager(nil)
	}
	return r.threads
}

func (r *Router) retainLiveThread(record *session.Record) error {
	return r.threadManager().RetainLiveThread(r.store, record)
}

func (r *Router) acquireLifecycleWriters(threadIDs []session.ThreadID) ([]*session.WriterLock, error) {
	return r.threadManager().AcquireLifecycleWriters(r.store, threadIDs)
}

func (r *Router) releaseLiveThreads(threadIDs []session.ThreadID) {
	r.threadManager().ReleaseLiveThreads(threadIDs)
}

func (r *Router) readThreadRecord(threadID session.ThreadID, includeArchived bool, includeHistory bool) (*session.Record, error) {
	var record *session.Record
	var err error
	if liveThread := r.threadManager().LiveThread(threadID); liveThread != nil {
		record, err = liveThread.Read(includeArchived, includeHistory)
	} else {
		record, err = r.store.Read(threadID, includeArchived, includeHistory)
	}
	if err == nil {
		r.applyIndexedThreadName(record)
	}
	return record, err
}

func (r *Router) saveThreadRecord(record *session.Record) error {
	if record != nil {
		if liveThread := r.threadManager().LiveThread(record.ID); liveThread != nil {
			return liveThread.Save(record)
		}
	}
	return r.store.Save(record)
}

func (r *Router) updateThreadMetadata(threadID session.ThreadID, patch *session.MetadataPatch, includeArchived bool) (*session.Record, error) {
	if liveThread := r.threadManager().LiveThread(threadID); liveThread != nil {
		return liveThread.UpdateMetadata(patch, includeArchived)
	}
	return r.store.UpdateMetadata(threadID, patch, includeArchived)
}

func (r *Router) appendThreadItems(threadID session.ThreadID, items []session.Item) (*session.Record, error) {
	if liveThread := r.threadManager().LiveThread(threadID); liveThread != nil {
		return liveThread.AppendItems(items)
	}
	return r.store.AppendItems(threadID, items)
}

func closeTemporaryWriters(locks []*session.WriterLock) {
	for i := len(locks) - 1; i >= 0; i-- {
		_ = locks[i].Close()
	}
}

func writerOwnershipError(err error) error {
	if errors.Is(err, session.ErrConflict) {
		return jsonRPCInvalidRequest(strings.TrimPrefix(err.Error(), session.ErrConflict.Error()+": "))
	}
	return err
}

func (r *Router) SetClock(clock func() time.Time) {
	if clock == nil {
		r.now = time.Now
		return
	}
	r.now = clock
}

func (r *Router) SetSpawnGraph(store agent.Store) {
	if r == nil {
		return
	}
	r.spawnGraph = store
}

func (r *Router) SetStateRuntime(runtime *state.StateRuntime) {
	if r != nil {
		r.state = runtime
	}
}

func (r *Router) createThreadRollout(record *session.Record, now time.Time) error {
	recorder, err := r.newThreadRolloutRecorder(record, now)
	if err != nil || recorder == nil {
		return err
	}
	defer recorder.Close()
	return appendThreadRolloutRecord(recorder, record, now)
}

func appendThreadRolloutRecord(recorder *rollout.Recorder, record *session.Record, now time.Time) error {
	if recorder == nil || record == nil {
		return nil
	}
	record = session.LocalRecord(record)
	if len(record.Metadata.RolloutTurns) == 0 {
		return rollout.AppendSessionItems(recorder, record.Items, now)
	}
	snapshots := make(map[string]session.TurnSnapshot, len(record.Metadata.RolloutTurns))
	for _, snapshot := range record.Metadata.RolloutTurns {
		turnID := strings.TrimSpace(snapshot.ID)
		if turnID == "" {
			continue
		}
		snapshot.ID = turnID
		snapshots[turnID] = snapshot
	}
	if len(snapshots) == 0 {
		return rollout.AppendSessionItems(recorder, record.Items, now)
	}
	for _, group := range runtimeForkTurnItemGroups(record.Items, record.CreatedAt) {
		snapshot, ok := snapshots[group.TurnID]
		if !ok {
			if err := recorder.AppendTurnStarted(group.TurnID, group.StartedAt); err != nil {
				return err
			}
			if err := rollout.AppendSessionItems(recorder, group.Items, now); err != nil {
				return err
			}
			if err := recorder.AppendTurnComplete(group.TurnID, group.CompletedAt, runtimeForkDurationMS(group.StartedAt, group.CompletedAt)); err != nil {
				return err
			}
			continue
		}
		startedAt := timeFromUnixSnapshot(snapshot.StartedAt, group.StartedAt)
		completedAt := timeFromUnixSnapshot(snapshot.CompletedAt, group.CompletedAt)
		durationMS := int64FromSnapshot(snapshot.DurationMS, runtimeForkDurationMS(startedAt, completedAt))
		if err := recorder.AppendTurnStarted(group.TurnID, startedAt); err != nil {
			return err
		}
		if err := rollout.AppendSessionItems(recorder, group.Items, now); err != nil {
			return err
		}
		switch turnStatusFromSnapshot(snapshot.Status) {
		case TurnStatusFailed:
			if err := recorder.AppendTurnError(snapshot.ErrorMessage, completedAt); err != nil {
				return err
			}
			if err := recorder.AppendTurnComplete(group.TurnID, completedAt, durationMS); err != nil {
				return err
			}
		case TurnStatusInterrupted, TurnStatusInProgress:
			if err := recorder.AppendTurnAborted(group.TurnID, "interrupted", completedAt, durationMS); err != nil {
				return err
			}
		default:
			if err := recorder.AppendTurnComplete(group.TurnID, completedAt, durationMS); err != nil {
				return err
			}
		}
	}
	return nil
}

func timeFromUnixSnapshot(value *int64, fallback time.Time) time.Time {
	if value != nil && *value > 0 {
		return time.Unix(*value, 0).UTC()
	}
	if !fallback.IsZero() {
		return fallback.UTC()
	}
	return time.Now().UTC()
}

func int64FromSnapshot(value *int64, fallback int64) int64 {
	if value != nil {
		return *value
	}
	return fallback
}

func (r *Router) newThreadRolloutRecorder(record *session.Record, now time.Time) (*rollout.Recorder, error) {
	if r == nil || r.store == nil || record == nil {
		return nil, nil
	}
	codexHome := codexHomeFromSessionStore(r.store)
	if codexHome == "" {
		return nil, nil
	}
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:                  codexHome,
		SessionID:                  record.SessionID,
		SessionPrefix:              record.Metadata.SessionPrefix,
		ThreadID:                   string(record.ID),
		ForkedFromID:               string(record.ForkedFromID),
		Source:                     record.Metadata.Source,
		ThreadSource:               record.Metadata.ThreadSource,
		Originator:                 record.Metadata.Originator,
		CWD:                        record.Metadata.CWD,
		Model:                      record.Metadata.Model,
		ModelProvider:              record.Metadata.ModelProvider,
		HistoryMode:                record.Metadata.HistoryMode,
		HistoryBase:                rolloutHistoryPositionFromRecord(record.HistoryBase),
		MemoryMode:                 record.Metadata.MemoryMode,
		ParentThreadID:             string(record.ParentThreadID),
		BaseInstructions:           record.Metadata.BaseInstructions,
		BaseInstructionsProvenance: cloneSessionBaseInstructionsProvenance(record.Metadata.BaseInstructionsProvenance),
		AgentNickname:              record.Metadata.AgentNickname,
		AgentRole:                  record.Metadata.AgentRole,
		AgentPath:                  record.Metadata.AgentPath,
		DynamicTools:               record.Metadata.DynamicTools,
		SelectedCapabilityRoots:    record.Metadata.SelectedCapabilityRoots,
		MultiAgentVersion:          record.Metadata.MultiAgentVersion,
		ContextWindow:              record.Metadata.ContextWindow,
		CLIVersion:                 record.Metadata.CLIVersion,
		Git:                        record.Metadata.Git,
		Extra:                      record.Metadata.Extra,
		Now:                        now,
	})
	if err != nil {
		return nil, err
	}
	r.configureThreadHistoryRecorder(recorder, record.ID)
	return recorder, nil
}

func rolloutHistoryPositionFromRecord(value *session.HistoryPosition) *rollout.HistoryPosition {
	if value == nil || value.EndOrdinalExclusive == 0 || value.EndByteOffset == 0 {
		return nil
	}
	return &rollout.HistoryPosition{
		ThreadID:            string(value.ThreadID),
		EndOrdinalExclusive: value.EndOrdinalExclusive,
		EndByteOffset:       value.EndByteOffset,
	}
}

func (r *Router) configureThreadHistoryRecorder(recorder *rollout.Recorder, threadID session.ThreadID) {
	if r == nil || recorder == nil {
		return
	}
	paginated := recorder.IsPaginated()
	recorder.SetAfterFlush(func(path string) {
		if r.state != nil {
			_ = r.state.ReconcileRollout(context.Background(), path, false)
		}
		if paginated {
			_ = r.materializeThreadHistory(threadID, path)
		}
	})
}

func (r *Router) materializeThreadHistory(threadID session.ThreadID, path string) error {
	if r == nil || strings.TrimSpace(string(threadID)) == "" || strings.TrimSpace(path) == "" {
		return nil
	}
	codexHome := codexHomeFromSessionStore(r.store)
	if codexHome == "" {
		return nil
	}
	initialOrdinal := uint64(0)
	var subagentHistoryStartOrdinal *uint64
	if meta, err := rollout.FirstSessionMeta(path); err == nil && meta != nil {
		if meta.HistoryBase != nil {
			initialOrdinal = meta.HistoryBase.EndOrdinalExclusive
		}
		subagentHistoryStartOrdinal = meta.SubagentHistoryStartOrdinal
	}
	if r.state != nil {
		db, err := r.state.ThreadHistoryDB(context.Background())
		if err != nil {
			return err
		}
		return state.MaterializeThreadHistory(context.Background(), db, string(threadID), path, initialOrdinal, subagentHistoryStartOrdinal)
	}
	sqliteHome := rustSQLiteHome(codexHome)
	db, err := state.OpenThreadHistoryDB(context.Background(), sqliteHome)
	if err != nil {
		return err
	}
	defer db.Close()
	return state.MaterializeThreadHistory(context.Background(), db, string(threadID), path, initialOrdinal, subagentHistoryStartOrdinal)
}

func (r *Router) appendThreadMetadataRollout(record *session.Record, now time.Time) error {
	if r == nil || r.store == nil || record == nil {
		return nil
	}
	codexHome := codexHomeFromSessionStore(r.store)
	if codexHome == "" {
		return nil
	}
	path, err := r.findThreadRolloutPath(record.ID, record.Archived)
	if err != nil {
		return nil
	}
	recorder, err := rollout.Resume(path)
	if err != nil {
		return err
	}
	r.configureThreadHistoryRecorder(recorder, record.ID)
	defer recorder.Close()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return recorder.AppendLine(rollout.Line{
		Type:      "session_meta",
		Timestamp: now.UTC().Format(time.RFC3339Nano),
		Meta:      rolloutSessionMetaFromRecord(record),
	})
}

func rolloutSessionMetaFromRecord(record *session.Record) *rollout.SessionMeta {
	if record == nil {
		return nil
	}
	createdAt := record.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = time.Now().UTC()
	}
	return &rollout.SessionMeta{
		ID:                         string(record.ID),
		SessionID:                  record.SessionID,
		SessionPrefix:              record.Metadata.SessionPrefix,
		ForkedFromID:               string(record.ForkedFromID),
		Timestamp:                  createdAt.Format(time.RFC3339),
		CWD:                        record.Metadata.CWD,
		Model:                      record.Metadata.Model,
		Source:                     record.Metadata.Source,
		ThreadSource:               record.Metadata.ThreadSource,
		Originator:                 record.Metadata.Originator,
		ModelProvider:              record.Metadata.ModelProvider,
		HistoryMode:                record.Metadata.HistoryMode,
		HistoryBase:                rolloutHistoryPositionFromRecord(record.HistoryBase),
		MemoryMode:                 record.Metadata.MemoryMode,
		ParentThreadID:             string(record.ParentThreadID),
		BaseInstructions:           record.Metadata.BaseInstructions,
		BaseInstructionsProvenance: cloneSessionBaseInstructionsProvenance(record.Metadata.BaseInstructionsProvenance),
		AgentNickname:              record.Metadata.AgentNickname,
		AgentRole:                  record.Metadata.AgentRole,
		AgentPath:                  record.Metadata.AgentPath,
		DynamicTools:               cloneRawMessages(record.Metadata.DynamicTools),
		SelectedCapabilityRoots:    cloneRawMessages(record.Metadata.SelectedCapabilityRoots),
		MultiAgentVersion:          record.Metadata.MultiAgentVersion,
		ContextWindow:              append(json.RawMessage(nil), record.Metadata.ContextWindow...),
		CLIVersion:                 record.Metadata.CLIVersion,
		Git:                        cloneStringMap(record.Metadata.Git),
		Extra:                      cloneAnyMapForRouter(record.Metadata.Extra),
	}
}

func (r *Router) appendThreadRollout(threadID session.ThreadID, items []session.Item, now time.Time) error {
	if r == nil || r.store == nil {
		return nil
	}
	path, err := r.findThreadRolloutPath(threadID, false)
	if err != nil {
		return nil
	}
	recorder, err := rollout.Resume(path)
	if err != nil {
		return err
	}
	r.configureThreadHistoryRecorder(recorder, threadID)
	defer recorder.Close()
	return rollout.AppendSessionItems(recorder, items, now)
}

func (r *Router) appendThreadRollback(threadID session.ThreadID, numTurns int, now time.Time) error {
	if r == nil || r.store == nil || numTurns < 0 {
		return nil
	}
	path, err := r.findThreadRolloutPath(threadID, false)
	if err != nil {
		return nil
	}
	recorder, err := rollout.Resume(path)
	if err != nil {
		return err
	}
	r.configureThreadHistoryRecorder(recorder, threadID)
	defer recorder.Close()
	return recorder.AppendThreadRolledBack(uint32(numTurns), now)
}

func (r *Router) appendThreadSettingsApplied(threadID session.ThreadID, approvalPolicy string, now time.Time) error {
	if r == nil || r.store == nil || strings.TrimSpace(approvalPolicy) == "" {
		return nil
	}
	path, err := r.findThreadRolloutPath(threadID, false)
	if err != nil {
		return nil
	}
	recorder, err := rollout.Resume(path)
	if err != nil {
		return err
	}
	r.configureThreadHistoryRecorder(recorder, threadID)
	defer recorder.Close()
	return recorder.AppendThreadSettingsApplied(approvalPolicy, now)
}

func (r *Router) latestPersistedApprovalPolicy(record *session.Record) (string, bool) {
	if r == nil || record == nil {
		return "", false
	}
	path := r.threadRolloutPath(record)
	if strings.TrimSpace(path) == "" {
		return "", false
	}
	lines, _, err := rollout.Load(path)
	if err != nil {
		return "", false
	}
	return rollout.LatestPersistedApprovalPolicy(lines)
}

func (r *Router) appendThreadCompacted(threadID session.ThreadID, message string, replacement []session.Item, now time.Time) error {
	if r == nil || r.store == nil {
		return nil
	}
	path, err := r.findThreadRolloutPath(threadID, false)
	if err != nil {
		return nil
	}
	recorder, err := rollout.Resume(path)
	if err != nil {
		return err
	}
	r.configureThreadHistoryRecorder(recorder, threadID)
	defer recorder.Close()
	items := make([]rollout.Item, 0, len(replacement))
	for i := range replacement {
		item := rollout.ItemFromSessionItem(&replacement[i])
		if item == nil {
			continue
		}
		items = append(items, *item)
	}
	return recorder.AppendCompacted(message, items, now)
}

func (r *Router) threadRolloutPath(record *session.Record) string {
	if r == nil || r.store == nil || record == nil {
		return ""
	}
	codexHome := codexHomeFromSessionStore(r.store)
	if codexHome == "" {
		return ""
	}
	path, err := r.findThreadRolloutPath(record.ID, record.Archived)
	if err == nil {
		return path
	}
	if value, ok := record.Metadata.Extra["rollout_path"].(string); ok && strings.TrimSpace(value) != "" {
		return strings.TrimSpace(value)
	}
	return ""
}

func (r *Router) attachRolloutTurnSnapshots(record *session.Record) {
	if r == nil || record == nil || len(record.Metadata.RolloutTurns) > 0 {
		return
	}
	path := r.threadRolloutPath(record)
	if strings.TrimSpace(path) == "" {
		return
	}
	rolloutRecord, err := rollout.RecordFromPath(path, record.Archived)
	if err != nil || rolloutRecord == nil || len(rolloutRecord.Metadata.RolloutTurns) == 0 {
		return
	}
	record.Metadata.RolloutTurns = rolloutRecord.Metadata.RolloutTurns
}

func codexHomeFromSessionStore(store *session.Store) string {
	if store == nil {
		return ""
	}
	root := store.Root()
	if filepath.Base(root) == "sessions" {
		return filepath.Dir(root)
	}
	return root
}

func (r *Router) applyIndexedThreadName(record *session.Record) {
	if r == nil || record == nil {
		return
	}
	name, found, err := rollout.FindThreadNameByID(codexHomeFromSessionStore(r.store), string(record.ID))
	if err != nil || !found {
		return
	}
	record.Title = name
	record.Metadata.Extra = ensureRecordExtra(record.Metadata.Extra)
	if strings.TrimSpace(name) == "" {
		delete(record.Metadata.Extra, explicitThreadNameExtraKey)
	} else {
		record.Metadata.Extra[explicitThreadNameExtraKey] = true
	}
}

func compactItemsFromSessionItems(items []session.Item) []compact.Item {
	out := make([]compact.Item, 0, len(items))
	for i := range items {
		item := items[i]
		compactItem := compact.Item{
			ID:      item.ID,
			Type:    item.Type,
			Role:    item.Role,
			Text:    item.Text,
			Kind:    compactKindFromSessionItem(&item),
			Created: item.CreatedAt,
			Data:    cloneAnyMapForRouter(item.Data),
			Raw:     append(json.RawMessage(nil), item.Raw...),
		}
		for j := range item.Content {
			compactItem.Content = append(compactItem.Content, compact.ContentPart{
				Type:     item.Content[j].Type,
				Text:     item.Content[j].Text,
				ImageURL: item.Content[j].ImageURL,
				Detail:   item.Content[j].Detail,
			})
		}
		out = append(out, compactItem)
	}
	return out
}

func compactKindFromSessionItem(item *session.Item) string {
	if item == nil {
		return ""
	}
	if kind := firstNonEmpty(stringFromMap(item.Metadata, "kind"), stringFromMap(item.Data, "kind")); kind != "" {
		return kind
	}
	if item.Type == "message" && item.Role == "user" {
		return "user_message"
	}
	return item.Type
}

func sessionItemsFromCompactItems(items []compact.Item, now time.Time) []session.Item {
	out := make([]session.Item, 0, len(items))
	for i := range items {
		item := items[i]
		sessionItem := session.Item{
			ID:        firstNonEmpty(item.ID, "compact-"+safeIdentifier(fmt.Sprintf("%d", i))),
			Type:      item.Type,
			Role:      item.Role,
			Text:      compact.ItemText(&item),
			CreatedAt: item.Created,
			Metadata: map[string]any{
				"compact": true,
				"kind":    item.Kind,
			},
			Data: cloneAnyMapForRouter(item.Data),
			Raw:  append(json.RawMessage(nil), item.Raw...),
		}
		if sessionItem.CreatedAt.IsZero() {
			sessionItem.CreatedAt = now
		}
		for j := range item.Content {
			sessionItem.Content = append(sessionItem.Content, session.ContentPart{
				Type:     item.Content[j].Type,
				Text:     item.Content[j].Text,
				ImageURL: item.Content[j].ImageURL,
				Detail:   item.Content[j].Detail,
			})
		}
		out = append(out, sessionItem)
	}
	return out
}

func conversationSummaryFromRecord(record *session.Record, maxChars int) string {
	if record == nil {
		return ""
	}
	if record.Metadata.Extra != nil {
		if summary, ok := record.Metadata.Extra["compaction_summary"].(string); ok && strings.TrimSpace(summary) != "" {
			return strings.TrimSpace(summary)
		}
	}
	summary := strings.TrimSpace(compact.SummarizeLocally(compactItemsFromSessionItems(record.Items), maxChars))
	if summary != "" {
		return summary
	}
	return strings.TrimSpace(record.Preview)
}

func conversationSummaryDataFromRecord(record *session.Record, path string, maxChars int) *ConversationSummary {
	if record == nil {
		return conversationSummaryFromText("", "")
	}
	summaryText := conversationSummaryFromRecord(record, maxChars)
	preview := strings.TrimSpace(record.Preview)
	if preview == "" {
		preview = summaryText
	}
	updatedAt := conversationSummaryTime(record.UpdatedAt)
	if updatedAt == nil {
		updatedAt = conversationSummaryTime(record.CreatedAt)
	}
	return &ConversationSummary{
		ConversationID: string(record.ID),
		Path:           path,
		Preview:        preview,
		Timestamp:      conversationSummaryTime(record.CreatedAt),
		UpdatedAt:      updatedAt,
		ModelProvider:  firstNonEmpty(record.Metadata.ModelProvider, record.Metadata.Model),
		CWD:            record.Metadata.CWD,
		CLIVersion:     record.Metadata.CLIVersion,
		Source:         SessionSourceFromString(record.Metadata.Source),
		GitInfo:        conversationGitInfoFromMap(record.Metadata.Git),
	}
}

func conversationSummaryFromText(text string, conversationID string) *ConversationSummary {
	return &ConversationSummary{
		ConversationID: strings.TrimSpace(conversationID),
		Preview:        strings.TrimSpace(text),
		Source:         SessionSourceUnknown,
	}
}

func conversationSummaryTime(value time.Time) *string {
	if value.IsZero() {
		return nil
	}
	formatted := value.UTC().Format("2006-01-02T15:04:05.000Z")
	return &formatted
}

func conversationGitInfoFromMap(values map[string]string) *ConversationGitInfo {
	if len(values) == 0 {
		return nil
	}
	info := &ConversationGitInfo{
		SHA:       stringPtrIfNotEmpty(values["sha"]),
		Branch:    stringPtrIfNotEmpty(values["branch"]),
		OriginURL: stringPtrIfNotEmpty(firstNonEmpty(values["origin_url"], values["originUrl"])),
	}
	if info.SHA == nil && info.Branch == nil && info.OriginURL == nil {
		return nil
	}
	return info
}

func cloneAnyMapForRouter(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func threadStartExtra(params *ThreadStartParams) map[string]any {
	if params == nil {
		return nil
	}
	extra := map[string]any{}
	extra[pendingSessionStartSourceExtraKey] = string(threadStartSessionStartSource(params))
	if params.ExperimentalRawEvents {
		extra[experimentalRawEventsExtraKey] = true
	}
	if params.BaseInstructions != nil && strings.TrimSpace(*params.BaseInstructions) == "" {
		extra["suppress_model_instructions"] = true
	}
	if len(params.DynamicTools) > 0 {
		tools := jsonValuesFromRawMessages(params.DynamicTools)
		if len(tools) > 0 {
			extra["dynamic_tools"] = tools
		}
	}
	if len(params.Config) > 0 {
		extra = ensureRecordExtra(extra)
		extra["config"] = cloneAnyMapForRouter(params.Config)
	}
	if len(extra) == 0 {
		return nil
	}
	return extra
}

func threadStartHistoryModeError(params *ThreadStartParams) error {
	if params == nil {
		return nil
	}
	historyMode := strings.TrimSpace(string(params.HistoryMode))
	if historyMode == "" || strings.EqualFold(historyMode, string(ThreadHistoryLegacy)) || strings.EqualFold(historyMode, string(ThreadHistoryPaginated)) {
		return nil
	}
	return jsonRPCInvalidRequest(fmt.Sprintf("unsupported historyMode %q", params.HistoryMode))
}

func threadLifecycleSandboxPermissionsError(permissions *string, sandbox any) error {
	if permissions != nil && turnStartSandboxPolicyPresent(sandbox) {
		return jsonRPCInvalidRequest("`permissions` cannot be combined with `sandbox`")
	}
	return nil
}

func jsonValuesFromRawMessages(values []json.RawMessage) []any {
	if len(values) == 0 {
		return nil
	}
	out := make([]any, 0, len(values))
	for _, raw := range values {
		if len(raw) == 0 {
			continue
		}
		var value any
		if err := json.Unmarshal(raw, &value); err == nil && value != nil {
			out = append(out, value)
		}
	}
	return out
}

func cloneRawMessages(values []json.RawMessage) []json.RawMessage {
	if len(values) == 0 {
		return nil
	}
	out := make([]json.RawMessage, 0, len(values))
	for _, value := range values {
		if len(value) == 0 {
			continue
		}
		out = append(out, append(json.RawMessage(nil), value...))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func rawSelectedCapabilityRoots(values []SelectedCapabilityRoot) []json.RawMessage {
	if len(values) == 0 {
		return nil
	}
	out := make([]json.RawMessage, 0, len(values))
	for i := range values {
		data, err := json.Marshal(values[i])
		if err != nil || len(data) == 0 {
			continue
		}
		out = append(out, json.RawMessage(data))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func (r *Router) Handle(request *Request) *Response {
	if err := request.Validate(); err != nil {
		if request == nil {
			return ErrorResponse(RequestID{}, -32600, err.Error(), nil)
		}
		return ErrorResponse(request.ID, -32600, err.Error(), nil)
	}
	if r == nil || r.store == nil {
		return ErrorResponse(request.ID, -32603, "thread store is not configured", nil)
	}
	result, err := r.dispatch(request)
	if err != nil {
		return ErrorResponse(request.ID, errorCode(err), err.Error(), jsonRPCErrorData(err))
	}
	return OK(request.ID, result)
}

func (r *Router) dispatch(request *Request) (any, error) {
	switch request.Method {
	case MethodThreadStart:
		return r.handleThreadStart(request)
	case MethodThreadResume:
		return r.handleThreadResume(request)
	case MethodThreadRead:
		return r.handleThreadRead(request)
	case MethodThreadFork:
		return r.handleThreadFork(request)
	case MethodThreadArchive:
		return r.handleThreadArchive(request)
	case MethodThreadUnarchive:
		return r.handleThreadUnarchive(request)
	case MethodThreadDelete:
		return r.handleThreadDelete(request)
	case MethodThreadIncrementElicitation, MethodThreadIncrementElicitationLegacy:
		return r.handleThreadIncrementElicitation(request)
	case MethodThreadDecrementElicitation, MethodThreadDecrementElicitationLegacy:
		return r.handleThreadDecrementElicitation(request)
	case MethodThreadSetName, MethodThreadNameSet:
		return r.handleThreadSetName(request)
	case MethodThreadUnsubscribe:
		return r.handleThreadUnsubscribe(request)
	case MethodThreadMemoryModeSet:
		return r.handleThreadMemoryModeSet(request)
	case MethodMemoryReset:
		return r.handleMemoryReset(request)
	case MethodThreadCompactStart:
		return r.handleThreadCompactStart(request)
	case MethodThreadApproveGuardianDeniedAction:
		return r.handleThreadApproveGuardianDeniedAction(request)
	case MethodThreadMetadataUpdate:
		return r.handleThreadMetadataUpdate(request)
	case MethodThreadSectionMove:
		return r.handleThreadSectionMove(request)
	case MethodThreadSectionCreate:
		return r.handleThreadSectionCreate(request)
	case MethodThreadSectionUpdate:
		return r.handleThreadSectionUpdate(request)
	case MethodThreadSectionDelete:
		return r.handleThreadSectionDelete(request)
	case MethodThreadList:
		return r.handleThreadList(request)
	case MethodThreadSectionList:
		return r.handleThreadSectionList(request)
	case MethodThreadSearch:
		return r.handleThreadSearch(request)
	case MethodThreadSearchOccurrences:
		return r.handleThreadSearchOccurrences(request)
	case MethodThreadLoadedList:
		return r.handleThreadLoadedList(request)
	case MethodThreadItemsList:
		return r.handleThreadItemsList(request)
	case MethodThreadTurnsList:
		return r.handleThreadTurnsList(request)
	case MethodThreadRollback:
		return r.handleThreadRollback(request)
	case MethodThreadRevert:
		return r.handleThreadRevert(request)
	case MethodThreadQueueAdd:
		return r.handleThreadQueueAdd(request)
	case MethodThreadQueueList:
		return r.handleThreadQueueList(request)
	case MethodThreadQueueUpdate:
		return r.handleThreadQueueUpdate(request)
	case MethodThreadQueueDelete:
		return r.handleThreadQueueDelete(request)
	case MethodThreadQueueReorder:
		return r.handleThreadQueueReorder(request)
	case MethodThreadInjectItems:
		return r.handleThreadInjectItems(request)
	default:
		return nil, fmt.Errorf("%w: %s", ErrUnknownMethod, request.Method)
	}
}

func (r *Router) handleThreadStart(request *Request) (*ThreadStartResponse, error) {
	var params ThreadStartParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if err := threadStartHistoryModeError(&params); err != nil {
		return nil, err
	}
	if err := threadLifecycleSandboxPermissionsError(params.Permissions, params.Sandbox); err != nil {
		return nil, err
	}
	threadID := newThreadID()
	now := r.now().UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	historyMode := string(params.HistoryMode)
	if historyMode == "" {
		historyMode = string(ThreadHistoryLegacy)
	}
	threadSource := ""
	if params.ThreadSource != nil {
		threadSource = string(*params.ThreadSource)
	}
	cwd := effectiveThreadStartCWD(&params, routerDefaultCWD(r))
	precomputedPath := rollout.PathForThread(codexHomeFromSessionStore(r.store), string(threadID), now)
	extra := threadStartExtra(&params)
	runtimeWorkspaceRoots := threadRuntimeWorkspaceRoots(cwd, params.RuntimeWorkspaceRoots)
	if strings.TrimSpace(precomputedPath) != "" {
		extra = ensureRecordExtra(extra)
		extra["rollout_path"] = precomputedPath
	}
	if len(runtimeWorkspaceRoots) > 0 {
		extra = ensureRecordExtra(extra)
		extra["runtime_workspace_roots"] = append([]string(nil), runtimeWorkspaceRoots...)
	}
	serviceTier := threadLifecycleServiceTierForModel(nil, params.ServiceTierSet, params.ServiceTier, params.Model)
	record := &session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		Preview:   params.Prompt,
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:           cwd,
			Model:         params.Model,
			ModelProvider: params.ModelProvider,
			ServiceTier:   serviceTier,
			// Rust's stdio app-server transport defaults every new thread's
			// session source to VSCode, and Go's raw app-server mirrors that
			// client-agnostic default so thread/list and session metadata agree.
			Source:                  string(SessionSourceVsCode),
			ThreadSource:            threadSource,
			HistoryMode:             historyMode,
			SessionPrefix:           session.PrefixForSessionID(string(threadID)),
			DynamicTools:            cloneRawMessages(params.DynamicTools),
			SelectedCapabilityRoots: rawSelectedCapabilityRoots(params.SelectedCapabilityRoots),
			Extra:                   extra,
		},
	}
	if err := r.store.Create(record); err != nil {
		return nil, err
	}
	if err := r.retainLiveThread(record); err != nil {
		_ = r.store.Delete(record.ID)
		return nil, err
	}
	if len(record.Items) > 0 {
		if err := r.createThreadRollout(record, now); err != nil {
			r.releaseLiveThreads([]session.ThreadID{record.ID})
			_ = r.store.Delete(record.ID)
			return nil, err
		}
	}
	path := r.threadRolloutPath(record)
	thread := BuildThread(record, path, true)
	if thread != nil {
		thread.Status = IdleStatus()
	}
	return &ThreadStartResponse{
		Thread:                  thread,
		CWD:                     cwd,
		RuntimeWorkspaceRoots:   runtimeWorkspaceRoots,
		ActivePermissionProfile: activePermissionProfileFromID(params.Permissions),
		ServiceTier:             stringPtrIfNotEmpty(serviceTier),
	}, nil
}

func newThreadID() session.ThreadID {
	if id, err := uuid.NewV7(); err == nil {
		return session.ThreadID(id.String())
	}
	return session.ThreadID(uuid.NewString())
}

func effectiveThreadStartCWD(params *ThreadStartParams, fallback string) string {
	if params != nil {
		if cwd := strings.TrimSpace(params.CWD); cwd != "" {
			return cwd
		}
	}
	return strings.TrimSpace(fallback)
}

func routerDefaultCWD(r *Router) string {
	if cwd := processCWD(); cwd != "" {
		return cwd
	}
	if r != nil && r.store != nil {
		return strings.TrimSpace(codexHomeFromSessionStore(r.store))
	}
	return ""
}

func processCWD() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cwd)
}

func (r *Router) handleThreadResume(request *Request) (*ThreadResumeResponse, error) {
	var params ThreadResumeParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if err := threadLifecycleSandboxPermissionsError(params.Permissions, params.Sandbox); err != nil {
		return nil, err
	}
	includeTurns := !params.ExcludeTurns
	if params.HistorySet {
		return r.handleThreadResumeHistory(request, &params, includeTurns)
	}
	var sourceID session.ThreadID
	var record *session.Record
	var err error
	if params.Path != nil && strings.TrimSpace(*params.Path) != "" {
		record, err = r.readThreadRecordFromRolloutPath(*params.Path, true, includeTurns)
		if err != nil {
			return nil, err
		}
		sourceID = record.ID
		if record != nil && record.Archived {
			return nil, threadResumeArchivedError(sourceID)
		}
	} else {
		sourceID, err = threadResumeSourceID(&params)
		if err != nil {
			return nil, err
		}
		record, err = r.readThreadRecord(sourceID, true, includeTurns)
		if err == nil && record != nil && record.Archived {
			return nil, threadResumeArchivedError(sourceID)
		}
		if err == nil && unmaterializedThread(record) {
			return nil, jsonRPCInvalidRequest(fmt.Sprintf("no rollout found for thread id %s", sourceID))
		}
		if err != nil {
			record, err = r.readThreadRecordFromRollout(sourceID, true, includeTurns)
			if err != nil {
				return nil, err
			}
			if record != nil && record.Archived {
				return nil, threadResumeArchivedError(sourceID)
			}
		}
	}
	paginatedResume := false
	if r.state != nil {
		mode, found, modeErr := r.threadHistoryModeWithRepair(sourceID)
		if modeErr != nil {
			return nil, modeErr
		}
		paginatedResume = found && strings.EqualFold(strings.TrimSpace(mode), string(ThreadHistoryPaginated))
	}
	if includeTurns && !paginatedResume {
		r.attachRolloutTurnSnapshots(record)
	}
	var paginatedTurns []Turn
	if includeTurns && paginatedResume {
		paginatedTurns, err = r.loadPaginatedThreadFullTurns(string(sourceID))
		if err != nil {
			return nil, err
		}
	}
	if err := r.retainLiveThread(record); err != nil {
		return nil, err
	}
	path := r.threadRolloutPath(record)
	if params.Path != nil && strings.TrimSpace(*params.Path) != "" {
		path = strings.TrimSpace(*params.Path)
	}
	cwd := firstNonEmpty(stringPtrValue(params.CWD), record.Metadata.CWD)
	runtimeWorkspaceRoots := threadRecordRuntimeWorkspaceRoots(record, cwd, params.RuntimeWorkspaceRoots)
	thread := BuildThread(record, path, includeTurns && !paginatedResume)
	if thread != nil {
		thread.Status = IdleStatus()
		if includeTurns && paginatedResume {
			thread.Turns = paginatedTurns
		}
	}
	approvalPolicy := params.ApprovalPolicy
	if approvalPolicy == nil {
		if value, ok := r.latestPersistedApprovalPolicy(record); ok {
			approvalPolicy = value
		}
	}
	if approvalPolicy == nil {
		if value, ok := params.Config["approval_policy"]; ok {
			if _, valid := parseTurnApprovalPolicy(value); valid {
				approvalPolicy = value
			}
		}
	}
	if approvalPolicy == nil && record != nil && strings.TrimSpace(record.Metadata.ApprovalPolicy) != "" {
		approvalPolicy = record.Metadata.ApprovalPolicy
	}
	response := &ThreadResumeResponse{
		Thread:                  thread,
		CWD:                     cwd,
		Model:                   firstNonEmpty(stringPtrValue(params.Model), record.Metadata.Model),
		ModelProvider:           firstNonEmpty(stringPtrValue(params.ModelProvider), record.Metadata.ModelProvider),
		ServiceTier:             resumeServiceTier(&params, record),
		ApprovalPolicy:          approvalPolicy,
		ApprovalsReviewer:       cloneString(params.ApprovalsReviewer),
		Sandbox:                 params.Sandbox,
		RuntimeWorkspaceRoots:   runtimeWorkspaceRoots,
		InstructionSources:      threadRecordInstructionSources(record),
		ActivePermissionProfile: activePermissionProfileFromID(params.Permissions),
	}
	if paginatedResume {
		response.TurnsBackwardsCursor, response.ItemsBackwardsCursor, err = r.paginatedResumeBackwardsCursors(string(sourceID))
		if err != nil {
			return nil, err
		}
	} else if r.state == nil {
		cursorRecord := record
		if !includeTurns {
			if params.Path != nil && strings.TrimSpace(*params.Path) != "" {
				cursorRecord, err = r.readThreadRecordFromRolloutPath(*params.Path, true, true)
			} else {
				cursorRecord, err = r.readThreadRecord(sourceID, true, true)
				if err != nil {
					cursorRecord, err = r.readThreadRecordFromRollout(sourceID, true, true)
				}
			}
			if err != nil {
				return nil, err
			}
			r.attachRolloutTurnSnapshots(cursorRecord)
		}
		response.TurnsBackwardsCursor, response.ItemsBackwardsCursor = threadResumeHeadCursors(cursorRecord)
	}
	if ShouldRedactThreadResumePayloads(params.ClientName) && response.Thread != nil {
		response.Thread.Turns = RedactThreadResumePayloads(response.Thread.Turns)
	}
	if params.InitialTurnsPage != nil {
		var page *TurnsPage
		if paginatedResume {
			page, err = r.buildPaginatedResumeInitialTurnsPage(string(sourceID), params.InitialTurnsPage, nil)
		} else {
			pageRecord := record
			if !includeTurns {
				if params.Path != nil && strings.TrimSpace(*params.Path) != "" {
					pageRecord, err = r.readThreadRecordFromRolloutPath(*params.Path, true, true)
				} else {
					pageRecord, err = r.readThreadRecord(sourceID, true, true)
					if err != nil {
						pageRecord, err = r.readThreadRecordFromRollout(sourceID, true, true)
					}
				}
				if err == nil {
					r.attachRolloutTurnSnapshots(pageRecord)
				}
			}
			if err == nil {
				page, err = BuildTurnsResponse(pageRecord, &ThreadTurnsListParams{
					ThreadID:      string(sourceID),
					Limit:         params.InitialTurnsPage.Limit,
					SortDirection: params.InitialTurnsPage.SortDirection,
					ItemsView:     params.InitialTurnsPage.ItemsView,
				})
			}
		}
		if err != nil {
			return nil, err
		}
		if ShouldRedactThreadResumePayloads(params.ClientName) {
			page = RedactTurnsPagePayloads(page)
		}
		response.InitialTurnsPage = page
	}
	return response, nil
}

func (r *Router) paginatedResumeBackwardsCursors(threadID string) (*string, *string, error) {
	turns, err := r.state.ListThreadHistoryTurns(context.Background(), state.ThreadHistoryListTurnsParams{
		ThreadID: threadID, PageSize: 1, SortDirection: state.ThreadHistorySortDesc, ItemsView: state.ThreadHistoryItemsNotLoaded,
	})
	if err != nil {
		return nil, nil, paginatedThreadHistoryError(err, false)
	}
	items, err := r.state.ListThreadHistoryItems(context.Background(), state.ThreadHistoryListItemsParams{
		ThreadID: threadID, PageSize: 1, SortDirection: state.ThreadHistorySortDesc,
	})
	if err != nil {
		return nil, nil, paginatedThreadHistoryError(err, false)
	}
	return turns.BackwardsCursor, items.BackwardsCursor, nil
}

func (r *Router) buildPaginatedResumeInitialTurnsPage(threadID string, params *ThreadInitialPageParams, options *turnsResponseOptions) (*TurnsPage, error) {
	if params == nil {
		return nil, nil
	}
	return r.buildPaginatedThreadTurnsResponse(&ThreadTurnsListParams{
		ThreadID: threadID, Limit: params.Limit, SortDirection: params.SortDirection, ItemsView: params.ItemsView,
	}, options)
}

func (r *Router) loadPaginatedThreadFullTurns(threadID string) ([]Turn, error) {
	return r.loadPaginatedThreadFullTurnsWithOptions(threadID, nil)
}

func (r *Router) loadPaginatedThreadFullTurnsWithOptions(threadID string, options *turnsResponseOptions) ([]Turn, error) {
	limit := threadTurnsMaxLimit
	var cursor *string
	turns := []Turn{}
	for {
		page, err := r.buildPaginatedThreadTurnsResponse(&ThreadTurnsListParams{
			ThreadID: threadID, Cursor: cursor, Limit: &limit, SortDirection: SortAsc, ItemsView: TurnItemsFull,
		}, options)
		if err != nil {
			return nil, err
		}
		turns = append(turns, page.Data...)
		if page.NextCursor == nil {
			return turns, nil
		}
		if cursor != nil && *cursor == *page.NextCursor {
			return nil, fmt.Errorf("failed to load full thread turns for %s: thread store returned a repeated cursor", threadID)
		}
		cursor = page.NextCursor
	}
}

func (r *Router) handleThreadResumeHistory(request *Request, params *ThreadResumeParams, includeTurns bool) (*ThreadResumeResponse, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("%w: router is not configured", ErrInvalidRequest)
	}
	now := r.now().UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	items, err := sessionItemsFromResumeHistory(params.History, now)
	if err != nil {
		return nil, err
	}
	threadID := session.ThreadID("thread-history-" + safeIdentifier(request.ID.String()) + "-" + safeIdentifier(fmt.Sprintf("%d", now.UnixNano())))
	modelID := stringPtrValue(params.Model)
	serviceTier := threadLifecycleServiceTierForModel(nil, params.ServiceTierSet, params.ServiceTier, modelID)
	cwd := stringPtrValue(params.CWD)
	runtimeWorkspaceRoots := threadRuntimeWorkspaceRoots(cwd, params.RuntimeWorkspaceRoots)
	extra := map[string]any{"history_resume": true}
	if len(runtimeWorkspaceRoots) > 0 {
		extra["runtime_workspace_roots"] = append([]string(nil), runtimeWorkspaceRoots...)
	}
	record := &session.Record{
		ID:        threadID,
		SessionID: string(threadID),
		Preview:   historyPreview(items),
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:           cwd,
			Model:         modelID,
			ModelProvider: stringPtrValue(params.ModelProvider),
			ServiceTier:   serviceTier,
			Source:        string(SessionSourceVsCode),
			HistoryMode:   string(ThreadHistoryLegacy),
			SessionPrefix: session.PrefixForSessionID(string(threadID)),
			Extra:         extra,
		},
		Items: items,
	}
	if err := r.store.Create(record); err != nil {
		return nil, err
	}
	if err := r.createThreadRollout(record, now); err != nil {
		return nil, err
	}
	path := r.threadRolloutPath(record)
	response := &ThreadResumeResponse{
		Thread:                  BuildThread(record, path, true),
		Model:                   modelID,
		ModelProvider:           stringPtrValue(params.ModelProvider),
		CWD:                     cwd,
		ServiceTier:             stringPtrIfNotEmpty(serviceTier),
		RuntimeWorkspaceRoots:   runtimeWorkspaceRoots,
		ActivePermissionProfile: activePermissionProfileFromID(params.Permissions),
	}
	if r.state == nil {
		response.TurnsBackwardsCursor, response.ItemsBackwardsCursor = threadResumeHeadCursors(record)
	}
	if params.InitialTurnsPage != nil {
		page, err := BuildTurnsResponse(record, &ThreadTurnsListParams{
			ThreadID:      string(threadID),
			Limit:         params.InitialTurnsPage.Limit,
			SortDirection: params.InitialTurnsPage.SortDirection,
			ItemsView:     params.InitialTurnsPage.ItemsView,
		})
		if err != nil {
			return nil, err
		}
		response.InitialTurnsPage = page
	}
	if !includeTurns && response.Thread != nil {
		response.Thread.Turns = []Turn{}
	}
	if ShouldRedactThreadResumePayloads(params.ClientName) && response.Thread != nil {
		response.Thread.Turns = RedactThreadResumePayloads(response.Thread.Turns)
		if response.InitialTurnsPage != nil {
			response.InitialTurnsPage.Data = RedactThreadResumePayloads(response.InitialTurnsPage.Data)
		}
	}
	return response, nil
}

func threadResumeHeadCursors(record *session.Record) (*string, *string) {
	if record == nil {
		return nil, nil
	}
	turns := turnsFromRecord(record)
	var turnsCursor *string
	if len(turns) > 0 {
		if cursor, err := serializeThreadTurnsCursor(turns[len(turns)-1].ID, true); err == nil {
			turnsCursor = stringPtrIfNotEmpty(cursor)
		}
	}
	hasItems := false
	for i := range record.Items {
		if !sessionItemIsHiddenThreadItem(&record.Items[i]) {
			hasItems = true
			break
		}
	}
	var itemsCursor *string
	if hasItems {
		itemsCursor = stringPtrIfNotEmpty("0")
	}
	return turnsCursor, itemsCursor
}

func resumeServiceTier(params *ThreadResumeParams, record *session.Record) *string {
	if params != nil && (params.ServiceTierSet || params.ServiceTier != nil) {
		modelID := ""
		if record != nil {
			modelID = record.Metadata.Model
		}
		if params.Model != nil {
			modelID = stringPtrValue(params.Model)
		}
		return stringPtrIfNotEmpty(threadLifecycleServiceTierForModel(nil, params.ServiceTierSet, params.ServiceTier, modelID))
	}
	if record != nil && strings.TrimSpace(record.Metadata.ServiceTier) != "" {
		return cloneString(&record.Metadata.ServiceTier)
	}
	return nil
}

func threadLifecycleServiceTier(set bool, value *string) string {
	if set && value == nil {
		return model.ServiceTierDefaultRequestValue
	}
	return stringPtrValue(value)
}

func threadLifecycleServiceTierForModel(service *model.ModelService, set bool, value *string, modelID string) string {
	return threadLifecycleServiceTierForRequest(service, threadLifecycleServiceTier(set, value), modelID)
}

func threadLifecycleServiceTierForRequest(service *model.ModelService, requested string, modelID string) string {
	requested = strings.TrimSpace(requested)
	if requested == "" {
		return ""
	}
	if requested == model.ServiceTierDefaultRequestValue {
		return model.ServiceTierDefaultRequestValue
	}
	if service == nil {
		service = model.NewModelService(nil)
	}
	info := service.Info(&model.ModelInfoReadParams{Model: strings.TrimSpace(modelID), RefreshStrategy: string(model.RefreshOffline)})
	return model.ServiceTierForRequest(info, requested)
}

func threadRuntimeWorkspaceRoots(cwd string, roots []string) []string {
	values := roots
	if len(values) == 0 {
		values = []string{cwd}
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		cleaned := cleanRuntimeWorkspaceRoot(value)
		if cleaned == "" {
			continue
		}
		seen := false
		for _, existing := range out {
			if sameAppPath(existing, cleaned) {
				seen = true
				break
			}
		}
		if !seen {
			out = append(out, cleaned)
		}
	}
	if out == nil {
		return []string{}
	}
	return out
}

func cleanRuntimeWorkspaceRoot(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if strings.HasPrefix(value, "/") {
		return pathpkg.Clean(strings.ReplaceAll(value, `\`, "/"))
	}
	if !isAbsoluteAppPath(value) {
		if absolute, err := filepath.Abs(value); err == nil {
			value = absolute
		}
	}
	return filepath.Clean(value)
}

func threadRecordRuntimeWorkspaceRoots(record *session.Record, cwd string, roots []string) []string {
	if len(roots) > 0 {
		return threadRuntimeWorkspaceRoots(cwd, roots)
	}
	if record != nil {
		if stored := stringSliceFromAny(record.Metadata.Extra["runtime_workspace_roots"]); len(stored) > 0 {
			return threadRuntimeWorkspaceRoots(cwd, stored)
		}
	}
	return threadRuntimeWorkspaceRoots(cwd, nil)
}

func setThreadRecordRuntimeWorkspaceRoots(record *session.Record, roots []string) {
	if record == nil {
		return
	}
	record.Metadata.Extra = ensureRecordExtra(record.Metadata.Extra)
	if len(roots) > 0 {
		record.Metadata.Extra["runtime_workspace_roots"] = append([]string(nil), roots...)
		return
	}
	delete(record.Metadata.Extra, "runtime_workspace_roots")
}

func threadRecordInstructionSources(record *session.Record) []string {
	if record == nil {
		return []string{}
	}
	raw := stringSliceFromAny(record.Metadata.Extra["instruction_sources"])
	if len(raw) == 0 {
		return []string{}
	}
	sources := []string{}
	for _, source := range raw {
		sources = appendInstructionSource(sources, source)
	}
	if sources == nil {
		return []string{}
	}
	return sources
}

func setThreadRecordInstructionSources(record *session.Record, sources []string) {
	if record == nil {
		return
	}
	cleaned := []string{}
	for _, source := range sources {
		cleaned = appendInstructionSource(cleaned, source)
	}
	if len(cleaned) == 0 {
		if len(record.Metadata.Extra) > 0 {
			delete(record.Metadata.Extra, "instruction_sources")
		}
		return
	}
	record.Metadata.Extra = ensureRecordExtra(record.Metadata.Extra)
	record.Metadata.Extra["instruction_sources"] = append([]string(nil), cleaned...)
}

func threadResumeSourceID(params *ThreadResumeParams) (session.ThreadID, error) {
	if params == nil {
		return "", fmt.Errorf("%w: threadId is required", ErrInvalidRequest)
	}
	if params.Path != nil && strings.TrimSpace(*params.Path) != "" {
		threadID, err := rollout.ThreadIDFromPath(*params.Path)
		if err != nil {
			return "", err
		}
		return session.ThreadID(threadID), nil
	}
	return session.ThreadID(params.ThreadID), nil
}

func threadResumeArchivedError(threadID session.ThreadID) error {
	id := strings.TrimSpace(string(threadID))
	return jsonRPCInvalidRequest(fmt.Sprintf("session %s is archived. Run `codex unarchive %s` to unarchive it first.", id, id))
}

func (r *Router) readThreadRecordFromRollout(threadID session.ThreadID, includeArchived bool, includeHistory bool) (*session.Record, error) {
	if r == nil || r.store == nil {
		return nil, session.ErrThreadNotFound
	}
	for _, archived := range []bool{false, true} {
		if archived && !includeArchived {
			continue
		}
		path, err := r.findThreadRolloutPath(threadID, archived)
		if err != nil {
			continue
		}
		record, err := rollout.RecordFromPathResolved(codexHomeFromSessionStore(r.store), path, archived)
		if err != nil {
			return nil, err
		}
		if !includeHistory {
			record.Items = nil
		}
		r.applyIndexedThreadName(record)
		return record, nil
	}
	return nil, fmt.Errorf("%w: %s", session.ErrThreadNotFound, threadID)
}

func (r *Router) readThreadRecordFromRolloutPath(path string, includeArchived bool, includeHistory bool) (*session.Record, error) {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return nil, fmt.Errorf("%w: rollout path is required", ErrInvalidRequest)
	}
	info, err := os.Stat(trimmed)
	if err != nil {
		return nil, err
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%w: path is a directory: %s", ErrInvalidRequest, trimmed)
	}
	archived := rolloutPathArchived(trimmed)
	if archived && !includeArchived {
		return nil, fmt.Errorf("%w: %s", session.ErrThreadArchived, trimmed)
	}
	record, err := rollout.RecordFromPathResolved(codexHomeFromSessionStore(r.store), trimmed, archived)
	if err != nil {
		return nil, err
	}
	if !includeHistory {
		record.Items = nil
	}
	r.applyIndexedThreadName(record)
	return record, nil
}

func rolloutPathArchived(path string) bool {
	cleaned := filepath.ToSlash(filepath.Clean(path))
	for _, part := range strings.Split(cleaned, "/") {
		if part == rollout.ArchivedSessionsSubdir {
			return true
		}
	}
	return false
}

func (r *Router) repairThreadRecordFromRollout(threadID session.ThreadID) (*session.Record, error) {
	record, err := r.readThreadRecordFromRollout(threadID, true, true)
	if err != nil {
		return nil, err
	}
	if err := r.saveThreadRecord(record); err != nil {
		return nil, err
	}
	return record, nil
}

func (r *Router) repairThreadRecordFromActiveRollout(threadID session.ThreadID) (*session.Record, error) {
	record, err := r.readThreadRecordFromRollout(threadID, false, true)
	if err != nil {
		return nil, err
	}
	if err := r.saveThreadRecord(record); err != nil {
		return nil, err
	}
	return record, nil
}

func (r *Router) handleThreadRead(request *Request) (*ThreadReadResponse, error) {
	var params ThreadReadParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	record, err := r.readThreadRecord(session.ThreadID(params.ThreadID), true, params.IncludeTurns)
	if err != nil {
		record, err = r.readThreadRecordFromRollout(session.ThreadID(params.ThreadID), true, params.IncludeTurns)
		if err != nil {
			return nil, threadReadError(params.ThreadID, err)
		}
	}
	if params.IncludeTurns {
		if unmaterializedThread(record) && !threadUsesPaginatedHistory(record) {
			return nil, jsonRPCInvalidRequest(fmt.Sprintf("thread %s is not materialized yet; includeTurns is unavailable before first user message", record.ID))
		}
		r.attachRolloutTurnSnapshots(record)
	}
	path := r.threadRolloutPath(record)
	return &ThreadReadResponse{Thread: BuildThread(record, path, params.IncludeTurns)}, nil
}

func threadReadError(threadID string, err error) error {
	if errors.Is(err, session.ErrThreadNotFound) {
		return jsonRPCInvalidRequest(fmt.Sprintf("thread not loaded: %s", strings.TrimSpace(threadID)))
	}
	return err
}

func (r *Router) handleThreadFork(request *Request) (*ThreadForkResponse, error) {
	var params ThreadForkParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if err := threadLifecycleSandboxPermissionsError(params.Permissions, params.Sandbox); err != nil {
		return nil, err
	}
	mode := params.HistoryMode
	if mode == "" {
		mode = session.ForkAll
	}
	sourceID, err := threadForkSourceID(&params)
	if err != nil {
		return nil, err
	}
	sourceLocks, err := r.acquireLifecycleWriters([]session.ThreadID{sourceID})
	if err != nil {
		return nil, err
	}
	defer closeTemporaryWriters(sourceLocks)
	var sourceRecord *session.Record
	if params.Path != nil && strings.TrimSpace(*params.Path) != "" {
		sourceRecord, err = r.readThreadRecordFromRolloutPath(*params.Path, true, true)
		if err != nil {
			return nil, err
		}
	} else {
		sourceRecord, err = r.readThreadRecord(sourceID, true, true)
		if err != nil {
			sourceRecord, err = r.readThreadRecordFromRollout(sourceID, true, true)
			if err != nil {
				return nil, err
			}
		}
	}
	if sourceRecord != nil && sourceRecord.Archived {
		return nil, threadResumeArchivedError(sourceRecord.ID)
	}
	if unmaterializedThread(sourceRecord) && !threadUsesPaginatedHistory(sourceRecord) {
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("no rollout found for thread id %s", sourceRecord.ID))
	}
	if err := validatePaginatedForkParams(sourceRecord, &params); err != nil {
		return nil, err
	}
	r.attachRolloutTurnSnapshots(sourceRecord)
	forkOptions := session.ForkOptions{
		Mode:         mode,
		LastN:        params.LastN,
		LastTurnID:   params.LastTurnID,
		BeforeTurnID: params.BeforeTurnID,
		Ephemeral:    params.Ephemeral,
		Now:          r.now().UTC(),
	}
	if historyBase, prepared, prepareErr := r.preparePaginatedForkHistoryBase(sourceRecord, &params); prepareErr != nil {
		return nil, prepareErr
	} else if prepared {
		forkOptions.HistoryBase = historyBase
		forkOptions.HistoryBaseSet = true
	}
	record, err := r.store.ForkRecord(sourceRecord, forkOptions)
	if err != nil {
		return nil, threadForkRecordError(err)
	}
	// Rust's app-server fork does not inherit the source thread's session
	// source; the new thread is created under the app-server transport's
	// default source (VSCode for the stdio transport).
	record.Metadata.Source = string(SessionSourceVsCode)
	if !params.Ephemeral {
		if err := r.retainLiveThread(record); err != nil {
			_ = r.store.Delete(record.ID)
			return nil, err
		}
	}
	applyThreadForkName(record, sourceRecord)
	if params.ThreadSource != nil {
		value := string(*params.ThreadSource)
		record.Metadata.ThreadSource = value
	}
	applyThreadForkOverrides(record, &params)
	setThreadRecordPendingSessionStartSource(record, SessionStartSourceStartup)
	runtimeWorkspaceRoots := threadRecordRuntimeWorkspaceRoots(record, record.Metadata.CWD, nil)
	if !params.Ephemeral {
		if err := r.saveThreadRecord(record); err != nil {
			r.rollbackThreadForkInitialization(record)
			return nil, err
		}
	}
	if !params.Ephemeral {
		if err := r.createThreadRollout(record, record.CreatedAt); err != nil {
			r.rollbackThreadForkInitialization(record)
			return nil, err
		}
	}
	if !params.Ephemeral && explicitThreadName(record) {
		if err := rollout.AppendThreadName(codexHomeFromSessionStore(r.store), string(record.ID), record.Title); err != nil {
			r.rollbackThreadForkInitialization(record)
			return nil, err
		}
	}
	if !params.Ephemeral && params.DeferGoalContinuation {
		if _, err := r.inheritThreadGoalSnapshot(sourceRecord.ID, record.ID); err != nil {
			r.rollbackThreadForkInitialization(record)
			return nil, fmt.Errorf("failed to inherit source thread goal: %w", err)
		}
	}
	if params.ExcludeTurns {
		record.Items = nil
	}
	path := ""
	if !params.Ephemeral {
		path = r.threadRolloutPath(record)
	}
	thread := BuildThread(record, path, !params.ExcludeTurns)
	if thread != nil {
		thread.Status = IdleStatus()
	}
	return &ThreadForkResponse{
		Thread:                  thread,
		ApprovalPolicy:          params.ApprovalPolicy,
		ApprovalsReviewer:       cloneString(params.ApprovalsReviewer),
		CWD:                     record.Metadata.CWD,
		Model:                   record.Metadata.Model,
		ModelProvider:           record.Metadata.ModelProvider,
		Sandbox:                 params.Sandbox,
		ServiceTier:             stringPtrIfNotEmpty(record.Metadata.ServiceTier),
		RuntimeWorkspaceRoots:   runtimeWorkspaceRoots,
		ActivePermissionProfile: activePermissionProfileFromID(params.Permissions),
	}, nil
}

func validatePaginatedForkParams(source *session.Record, params *ThreadForkParams) error {
	if threadUsesPaginatedHistory(source) && params != nil && params.Ephemeral && !params.ExcludeTurns {
		return jsonRPCInvalidRequest("ephemeral paginated thread/fork requires `excludeTurns: true`")
	}
	return nil
}

func (r *Router) preparePaginatedForkHistoryBase(source *session.Record, params *ThreadForkParams) (*session.HistoryPosition, bool, error) {
	if !threadUsesPaginatedHistory(source) || r == nil || r.state == nil {
		return nil, false, nil
	}
	// A fresh paginated thread has a planned rollout path but no physical
	// history yet. Its latest fork boundary is the empty prefix, so querying the
	// SQLite lineage would incorrectly classify the absent rollout as corrupt.
	if unmaterializedThread(source) && (params == nil || (strings.TrimSpace(params.LastTurnID) == "" && strings.TrimSpace(params.BeforeTurnID) == "")) {
		return nil, true, nil
	}
	kind := state.ThreadHistoryForkLatest
	turnID := ""
	if params != nil {
		if strings.TrimSpace(params.LastTurnID) != "" {
			kind, turnID = state.ThreadHistoryForkThroughTurn, strings.TrimSpace(params.LastTurnID)
		} else if strings.TrimSpace(params.BeforeTurnID) != "" {
			kind, turnID = state.ThreadHistoryForkBeforeTurn, strings.TrimSpace(params.BeforeTurnID)
		}
	}
	position, err := r.state.PreparePaginatedFork(context.Background(), string(source.ID), kind, turnID)
	if err != nil {
		return nil, true, paginatedForkPrepareError(err)
	}
	if position == nil {
		return nil, true, nil
	}
	return &session.HistoryPosition{
		ThreadID: session.ThreadID(position.ThreadID), EndOrdinalExclusive: position.EndOrdinalExclusive, EndByteOffset: position.EndByteOffset,
	}, true, nil
}

func paginatedForkPrepareError(err error) error {
	var historyErr *state.ThreadHistoryError
	if errors.As(err, &historyErr) {
		switch historyErr.Kind {
		case state.ThreadHistoryInvalidRequest:
			return jsonRPCInvalidRequest(historyErr.Error())
		case state.ThreadHistoryUnsupported:
			return methodNotFound("paginated_threads is not supported yet")
		case state.ThreadHistoryNotFound:
			return jsonRPCInvalidRequest("no rollout found for thread id " + historyErr.ThreadID)
		}
	}
	return fmt.Errorf("failed to prepare paginated fork: %w", err)
}

func (r *Router) rollbackThreadForkInitialization(record *session.Record) {
	if r == nil || r.store == nil || record == nil {
		return
	}
	r.releaseLiveThreads([]session.ThreadID{record.ID})
	r.deleteThreadRollouts(record.ID)
	_ = r.store.Delete(record.ID)
}

func threadForkRecordError(err error) error {
	if errors.Is(err, session.ErrInvalidThreadID) && (strings.Contains(err.Error(), "lastTurnId") || strings.Contains(err.Error(), "beforeTurnId")) {
		message := strings.TrimPrefix(err.Error(), session.ErrInvalidThreadID.Error()+": ")
		return jsonRPCInvalidRequest(message)
	}
	return err
}

func activePermissionProfileFromID(value *string) *sandbox.ActivePermissionProfile {
	if value == nil || strings.TrimSpace(*value) == "" {
		return nil
	}
	return &sandbox.ActivePermissionProfile{ID: strings.TrimSpace(*value)}
}

func applyThreadForkOverrides(record *session.Record, params *ThreadForkParams) {
	if record == nil || params == nil {
		return
	}
	if params.CWD != nil {
		record.Metadata.CWD = stringPtrValue(params.CWD)
	}
	if params.Model != nil {
		record.Metadata.Model = stringPtrValue(params.Model)
	}
	if params.ModelProvider != nil {
		record.Metadata.ModelProvider = stringPtrValue(params.ModelProvider)
	}
	if params.BaseInstructions != nil {
		record.Metadata.BaseInstructions = stringPtrValue(params.BaseInstructions)
		record.Metadata.BaseInstructionsProvenance = &session.BaseInstructionsProvenance{Type: session.BaseInstructionsProvenanceCustom}
	}
	if params.DeveloperInstructions != nil {
		record.Metadata.Instructions = stringPtrValue(params.DeveloperInstructions)
	}
	if params.Permissions != nil {
		record.Metadata.Extra = ensureRecordExtra(record.Metadata.Extra)
		record.Metadata.Extra["permissions"] = stringPtrValue(params.Permissions)
	}
	runtimeWorkspaceRoots := threadRecordRuntimeWorkspaceRoots(record, record.Metadata.CWD, nil)
	if params.CWD != nil || len(params.RuntimeWorkspaceRoots) > 0 {
		runtimeWorkspaceRoots = threadRuntimeWorkspaceRoots(record.Metadata.CWD, params.RuntimeWorkspaceRoots)
	}
	setThreadRecordRuntimeWorkspaceRoots(record, runtimeWorkspaceRoots)
	if params.Config != nil {
		record.Metadata.Extra = ensureRecordExtra(record.Metadata.Extra)
		record.Metadata.Extra["config"] = cloneAnyMap(params.Config)
	}
	if params.ServiceTierSet || params.ServiceTier != nil {
		record.Metadata.ServiceTier = threadLifecycleServiceTierForModel(nil, params.ServiceTierSet, params.ServiceTier, record.Metadata.Model)
	}
}

func cloneSessionBaseInstructionsProvenance(value *session.BaseInstructionsProvenance) *session.BaseInstructionsProvenance {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

const explicitThreadNameExtraKey = "thread_name_explicit"

func applyThreadForkName(record *session.Record, source *session.Record) {
	if record == nil {
		return
	}
	if explicitThreadName(source) {
		record.Title = strings.TrimSpace(source.Title)
		record.Metadata.Extra = ensureRecordExtra(record.Metadata.Extra)
		record.Metadata.Extra[explicitThreadNameExtraKey] = true
		return
	}
	record.Title = ""
	if record.Metadata.Extra != nil {
		delete(record.Metadata.Extra, explicitThreadNameExtraKey)
	}
}

func explicitThreadName(record *session.Record) bool {
	if record == nil || strings.TrimSpace(record.Title) == "" {
		return false
	}
	return boolFromMap(record.Metadata.Extra, explicitThreadNameExtraKey)
}

func ensureRecordExtra(values map[string]any) map[string]any {
	if values != nil {
		return values
	}
	return map[string]any{}
}

func threadForkSourceID(params *ThreadForkParams) (session.ThreadID, error) {
	if params == nil {
		return "", fmt.Errorf("%w: threadId or path is required", ErrInvalidRequest)
	}
	if params.Path != nil && strings.TrimSpace(*params.Path) != "" {
		path := strings.TrimSpace(*params.Path)
		info, err := os.Stat(path)
		if err != nil {
			return "", err
		}
		if info.IsDir() {
			return "", fmt.Errorf("%w: path is a directory: %s", ErrInvalidRequest, path)
		}
		threadID, err := rollout.ThreadIDFromPath(path)
		if err != nil {
			return "", err
		}
		return session.ThreadID(threadID), nil
	}
	return session.ThreadID(params.ThreadID), nil
}

func (r *Router) archiveSubtreeThreadIDs(root session.ThreadID) ([]session.ThreadID, error) {
	threadIDs, err := r.store.SubtreeThreadIDs(root)
	if err != nil {
		if !errors.Is(err, session.ErrThreadNotFound) || !r.threadRolloutExists(root, false) {
			return nil, err
		}
		threadIDs = []session.ThreadID{root}
	}
	return r.appendSpawnGraphDescendants(threadIDs, root)
}

func (r *Router) deleteSubtreeThreadIDs(root session.ThreadID) ([]session.ThreadID, error) {
	threadIDs, err := r.store.SubtreeThreadIDs(root)
	if err != nil {
		if !errors.Is(err, session.ErrThreadNotFound) {
			return nil, err
		}
		threadIDs = []session.ThreadID{root}
		withGraph, graphErr := r.appendSpawnGraphDescendants(threadIDs, root)
		if graphErr != nil {
			return nil, graphErr
		}
		if len(withGraph) == 1 && !r.threadRolloutExistsAny(root) {
			return nil, err
		}
		return withGraph, nil
	}
	return r.appendSpawnGraphDescendants(threadIDs, root)
}

func (r *Router) appendSpawnGraphDescendants(ids []session.ThreadID, root session.ThreadID) ([]session.ThreadID, error) {
	if r == nil || r.spawnGraph == nil || root == "" {
		return ids, nil
	}
	descendants, err := r.spawnGraph.ListThreadSpawnDescendants(string(root), nil)
	if err != nil {
		return nil, err
	}
	if len(descendants) == 0 {
		return ids, nil
	}
	out := append([]session.ThreadID(nil), ids...)
	seen := make(map[session.ThreadID]struct{}, len(out)+len(descendants))
	for _, id := range out {
		seen[id] = struct{}{}
	}
	for _, descendant := range descendants {
		threadID := session.ThreadID(strings.TrimSpace(descendant))
		if threadID == "" {
			continue
		}
		if _, ok := seen[threadID]; ok {
			continue
		}
		seen[threadID] = struct{}{}
		out = append(out, threadID)
	}
	return out, nil
}

func (r *Router) handleThreadArchive(request *Request) (*ThreadArchiveResponse, error) {
	var params ThreadArchiveParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	threadIDs, err := r.archiveSubtreeThreadIDs(session.ThreadID(params.ThreadID))
	if err != nil {
		return nil, err
	}
	lifecycleLocks, err := r.acquireLifecycleWriters(threadIDs)
	if err != nil {
		return nil, err
	}
	defer closeTemporaryWriters(lifecycleLocks)
	if rootRecord, readErr := r.readThreadRecord(session.ThreadID(params.ThreadID), true, false); readErr == nil && unmaterializedThread(rootRecord) {
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("no rollout found for thread id %s", params.ThreadID))
	} else if readErr != nil && !errors.Is(readErr, session.ErrThreadNotFound) {
		return nil, readErr
	}
	archivedThreadIDs := make([]session.ThreadID, 0, len(threadIDs))
	for _, threadID := range threadIDs {
		storeArchived := false
		if err := r.store.Archive(threadID); err != nil && !errors.Is(err, session.ErrThreadNotFound) {
			return nil, err
		} else if err == nil {
			storeArchived = true
		}
		rolloutFound, rolloutArchived := r.archiveThreadRollout(threadID, true)
		if rolloutFound && !rolloutArchived {
			if storeArchived {
				_, _ = r.store.Unarchive(threadID)
			}
			continue
		}
		if storeArchived || rolloutArchived {
			archivedThreadIDs = append(archivedThreadIDs, threadID)
		}
	}
	r.releaseLiveThreads(archivedThreadIDs)
	return &ThreadArchiveResponse{archivedThreadIDs: archivedThreadIDs}, nil
}

func (r *Router) handleThreadUnarchive(request *Request) (*ThreadUnarchiveResponse, error) {
	var params ThreadUnarchiveParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	record, err := r.store.Unarchive(session.ThreadID(params.ThreadID))
	if err != nil {
		if !errors.Is(err, session.ErrThreadNotFound) {
			return nil, err
		}
		archivedRecord, readErr := r.readThreadRecordFromRollout(session.ThreadID(params.ThreadID), true, false)
		if readErr != nil || !archivedRecord.Archived {
			return nil, err
		}
		r.archiveThreadRollout(archivedRecord.ID, false)
		if restoredRecord, restoreErr := r.readThreadRecordFromRollout(archivedRecord.ID, false, false); restoreErr == nil {
			archivedRecord = restoredRecord
		} else {
			archivedRecord.Archived = false
		}
		path := r.threadRolloutPath(archivedRecord)
		return &ThreadUnarchiveResponse{Thread: BuildThread(archivedRecord, path, false)}, nil
	}
	r.archiveThreadRollout(record.ID, false)
	path := r.threadRolloutPath(record)
	return &ThreadUnarchiveResponse{Thread: BuildThread(record, path, false)}, nil
}

func (r *Router) archiveThreadRollout(threadID session.ThreadID, archived bool) (bool, bool) {
	if r == nil || r.store == nil || threadID == "" {
		return false, false
	}
	codexHome := codexHomeFromSessionStore(r.store)
	if codexHome == "" {
		return false, false
	}
	path, err := r.findThreadRolloutPath(threadID, !archived)
	if err != nil {
		return false, false
	}
	if archived {
		var archivedPath string
		archivedPath, err = rollout.Archive(path, codexHome)
		if err == nil && r.state != nil {
			_ = r.state.MarkThreadArchived(context.Background(), string(threadID), archivedPath, time.Now().UTC())
		}
		return true, err == nil
	}
	var restoredPath string
	restoredPath, err = rollout.Unarchive(path, codexHome)
	if err == nil && r.state != nil {
		_ = r.state.MarkThreadUnarchived(context.Background(), string(threadID), restoredPath)
	}
	return true, err == nil
}

func (r *Router) handleThreadDelete(request *Request) (*ThreadDeleteResponse, error) {
	var params ThreadDeleteParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	threadIDs, err := r.deleteSubtreeThreadIDs(session.ThreadID(params.ThreadID))
	if err != nil {
		return nil, threadDeleteError(params.ThreadID, err)
	}
	lifecycleLocks, err := r.acquireLifecycleWriters(threadIDs)
	if err != nil {
		return nil, err
	}
	defer closeTemporaryWriters(lifecycleLocks)
	deleteOrder := session.DeleteOrderForSubtree(threadIDs)
	for _, threadID := range deleteOrder {
		if err := r.store.Delete(threadID); err != nil && !errors.Is(err, session.ErrThreadNotFound) {
			return nil, err
		}
		if r.state != nil {
			if err := r.state.DeleteThreadHistory(context.Background(), string(threadID)); err != nil {
				return nil, err
			}
		}
		if err := r.deleteThreadRolloutsStrict(threadID); err != nil {
			return nil, err
		}
		if err := rollout.RemoveThreadNameEntries(codexHomeFromSessionStore(r.store), string(threadID)); err != nil {
			return nil, err
		}
	}
	if r.state != nil {
		ids := make([]string, 0, len(threadIDs))
		for _, threadID := range threadIDs {
			ids = append(ids, string(threadID))
		}
		if _, err := r.state.DeleteThreadsStrict(context.Background(), ids); err != nil {
			return nil, err
		}
	}
	r.releaseLiveThreads(threadIDs)
	return &ThreadDeleteResponse{}, nil
}

func threadDeleteError(threadID string, err error) error {
	if errors.Is(err, session.ErrThreadNotFound) {
		return jsonRPCInvalidRequest(fmt.Sprintf("thread not found: %s", strings.TrimSpace(threadID)))
	}
	return err
}

func (r *Router) deleteThreadRollouts(threadID session.ThreadID) {
	_ = r.deleteThreadRolloutsStrict(threadID)
}

func (r *Router) deleteThreadRolloutsStrict(threadID session.ThreadID) error {
	if r == nil || r.store == nil || threadID == "" {
		return nil
	}
	codexHome := codexHomeFromSessionStore(r.store)
	if codexHome == "" {
		return nil
	}
	for _, archived := range []bool{false, true} {
		path, err := r.findThreadRolloutPath(threadID, archived)
		if err != nil {
			continue
		}
		if err := rollout.Delete(path); err != nil && !strings.Contains(err.Error(), "rollout not found:") {
			return err
		}
	}
	return nil
}

func (r *Router) threadRolloutExists(threadID session.ThreadID, archived bool) bool {
	if r == nil || r.store == nil || threadID == "" {
		return false
	}
	codexHome := codexHomeFromSessionStore(r.store)
	if codexHome == "" {
		return false
	}
	_, err := r.findThreadRolloutPath(threadID, archived)
	return err == nil
}

func (r *Router) findThreadRolloutPath(threadID session.ThreadID, archived bool) (string, error) {
	if r == nil || r.store == nil || strings.TrimSpace(string(threadID)) == "" {
		return "", session.ErrThreadNotFound
	}
	codexHome := codexHomeFromSessionStore(r.store)
	if codexHome == "" {
		return "", session.ErrThreadNotFound
	}
	var unverified string
	if r.state != nil {
		if dbPath, found, err := r.state.FindRolloutPathByID(context.Background(), string(threadID), &archived); err == nil && found {
			if existing, ok := rollout.ExistingRolloutPath(dbPath); ok {
				if actualID, verifyErr := rollout.ThreadIDFromPath(existing); verifyErr == nil {
					if actualID == string(threadID) {
						return existing, nil
					}
				} else {
					unverified = existing
				}
			}
		}
	}
	path, err := rollout.FindThreadPath(codexHome, string(threadID), archived)
	if err == nil {
		if r.state != nil {
			_ = r.state.ReadRepairRolloutPath(context.Background(), string(threadID), path, archived)
		}
		return path, nil
	}
	if unverified != "" {
		return unverified, nil
	}
	return "", err
}

func (r *Router) threadRolloutExistsAny(threadID session.ThreadID) bool {
	return r.threadRolloutExists(threadID, false) || r.threadRolloutExists(threadID, true)
}

func (r *Router) handleThreadIncrementElicitation(request *Request) (*ThreadIncrementElicitationResponse, error) {
	var params ThreadIncrementElicitationParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	record, err := r.readThreadRecord(session.ThreadID(params.ThreadID), true, true)
	if err != nil {
		if !errors.Is(err, session.ErrThreadNotFound) {
			return nil, err
		}
		record, err = r.repairThreadRecordFromRollout(session.ThreadID(params.ThreadID))
		if err != nil {
			return nil, err
		}
	}
	record.Metadata.ElicitationCount++
	record.UpdatedAt = r.now().UTC()
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = time.Now().UTC()
	}
	if err := r.saveThreadRecord(record); err != nil {
		return nil, err
	}
	count := record.Metadata.ElicitationCount
	return &ThreadIncrementElicitationResponse{Count: count, Paused: count > 0}, nil
}

func (r *Router) handleThreadDecrementElicitation(request *Request) (*ThreadDecrementElicitationResponse, error) {
	var params ThreadDecrementElicitationParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	record, err := r.readThreadRecord(session.ThreadID(params.ThreadID), true, true)
	if err != nil {
		if !errors.Is(err, session.ErrThreadNotFound) {
			return nil, err
		}
		record, err = r.repairThreadRecordFromRollout(session.ThreadID(params.ThreadID))
		if err != nil {
			return nil, err
		}
	}
	if record.Metadata.ElicitationCount <= 0 {
		return nil, fmt.Errorf("%w: out-of-band elicitation counter is already zero", ErrInvalidRequest)
	}
	record.Metadata.ElicitationCount--
	record.UpdatedAt = r.now().UTC()
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = time.Now().UTC()
	}
	if err := r.saveThreadRecord(record); err != nil {
		return nil, err
	}
	count := record.Metadata.ElicitationCount
	return &ThreadDecrementElicitationResponse{Count: count, Paused: count > 0}, nil
}

func (r *Router) handleThreadSetName(request *Request) (*ThreadSetNameResponse, error) {
	var params ThreadSetNameParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	patch := &session.MetadataPatch{Title: &params.Name}
	threadID := session.ThreadID(params.ThreadID)
	record, err := r.updateThreadMetadata(threadID, patch, false)
	if err != nil {
		if errors.Is(err, session.ErrThreadArchived) {
			return nil, threadResumeArchivedError(threadID)
		}
		if !errors.Is(err, session.ErrThreadNotFound) {
			return nil, err
		}
		if _, repairErr := r.repairThreadRecordFromActiveRollout(threadID); repairErr != nil {
			return nil, threadMetadataWriteError(params.ThreadID, repairErr)
		}
		record, err = r.updateThreadMetadata(threadID, patch, false)
		if err != nil {
			if errors.Is(err, session.ErrThreadArchived) {
				return nil, threadResumeArchivedError(threadID)
			}
			return nil, threadMetadataWriteError(params.ThreadID, err)
		}
	}
	if err := r.markExplicitThreadName(record); err != nil {
		return nil, err
	}
	return &ThreadSetNameResponse{}, nil
}

func (r *Router) markExplicitThreadName(record *session.Record) error {
	if r == nil || r.store == nil || record == nil {
		return nil
	}
	record.Metadata.Extra = ensureRecordExtra(record.Metadata.Extra)
	record.Metadata.Extra[explicitThreadNameExtraKey] = true
	if err := r.saveThreadRecord(record); err != nil {
		return err
	}
	codexHome := codexHomeFromSessionStore(r.store)
	if err := updateRustStateThreadName(codexHome, string(record.ID), record.Title); err != nil {
		return fmt.Errorf("failed to update sqlite thread name: %w", err)
	}
	if err := rollout.AppendThreadName(codexHome, string(record.ID), record.Title); err != nil {
		return fmt.Errorf("failed to index thread name: %w", err)
	}
	return nil
}

func threadMetadataWriteError(threadID string, err error) error {
	if errors.Is(err, session.ErrThreadNotFound) {
		return jsonRPCInvalidRequest(fmt.Sprintf("thread not found: %s", strings.TrimSpace(threadID)))
	}
	return err
}

func (r *Router) handleThreadUnsubscribe(request *Request) (*ThreadUnsubscribeResponse, error) {
	var params ThreadUnsubscribeParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if _, err := r.store.Load(session.ThreadID(params.ThreadID)); err != nil {
		if errors.Is(err, session.ErrThreadNotFound) {
			return &ThreadUnsubscribeResponse{Status: ThreadUnsubscribeStatusNotLoaded}, nil
		}
		return nil, err
	}
	return &ThreadUnsubscribeResponse{Status: ThreadUnsubscribeStatusNotSubscribed}, nil
}

func (r *Router) handleThreadMemoryModeSet(request *Request) (*ThreadMemoryModeSetResponse, error) {
	var params ThreadMemoryModeSetParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	threadID := session.ThreadID(params.ThreadID)
	record, err := r.readThreadRecord(threadID, false, true)
	if err != nil {
		if errors.Is(err, session.ErrThreadArchived) {
			return nil, threadResumeArchivedError(threadID)
		}
		if !errors.Is(err, session.ErrThreadNotFound) {
			return nil, err
		}
		record, err = r.repairThreadRecordFromActiveRollout(threadID)
		if err != nil {
			return nil, threadMetadataWriteError(params.ThreadID, err)
		}
	}
	record.Metadata.MemoryMode = string(params.Mode)
	record.UpdatedAt = r.now().UTC()
	record.RecencyAt = record.UpdatedAt
	if err := r.saveThreadRecord(record); err != nil {
		return nil, err
	}
	if err := r.appendThreadMetadataRollout(record, r.now().UTC()); err != nil {
		return nil, err
	}
	if err := updateRustStateThreadMemoryMode(codexHomeFromSessionStore(r.store), params.ThreadID, params.Mode); err != nil {
		return nil, fmt.Errorf("failed to update sqlite thread memory mode: %w", err)
	}
	return &ThreadMemoryModeSetResponse{}, nil
}

func (r *Router) handleMemoryReset(request *Request) (*MemoryResetResponse, error) {
	if r == nil || r.store == nil {
		return nil, fmt.Errorf("%w: router is not configured", ErrInvalidRequest)
	}
	codexHome := codexHomeFromSessionStore(r.store)
	var err error
	if r.state != nil {
		err = r.state.ClearMemoryData(context.Background())
	} else {
		err = clearRustMemoriesSQLiteData(codexHome)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to clear memory rows in memories db: %w", err)
	}
	if err := memories.ClearRootsContents(codexHome); err != nil {
		return nil, fmt.Errorf("failed to clear memory directories under %s: %w", codexHome, err)
	}
	return &MemoryResetResponse{}, nil
}

func (r *Router) handleThreadCompactStart(request *Request) (*ThreadCompactStartResponse, error) {
	var params ThreadCompactStartParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	record, err := r.readThreadRecord(session.ThreadID(params.ThreadID), true, true)
	if err != nil {
		if !errors.Is(err, session.ErrThreadNotFound) {
			return nil, err
		}
		record, err = r.repairThreadRecordFromRollout(session.ThreadID(params.ThreadID))
		if err != nil {
			return nil, err
		}
	}
	compacted, err := compact.CompactLocally(&compact.Request{
		ThreadID: params.ThreadID,
		Trigger:  compact.TriggerManual,
		Reason:   compact.ReasonUserRequested,
		Phase:    compact.PhaseStandaloneTurn,
		History:  compactItemsFromSessionItems(record.Items),
	}, 4000, nil, false)
	if err != nil {
		return nil, err
	}
	now := r.now().UTC()
	record.Items = sessionItemsFromCompactItems(compacted.NewHistory, now)
	record.UpdatedAt = now
	record.RecencyAt = now
	if record.Metadata.Extra == nil {
		record.Metadata.Extra = map[string]any{}
	}
	record.Metadata.Extra["compacted_at"] = now.Format(time.RFC3339Nano)
	record.Metadata.Extra["compaction_summary"] = compacted.Summary
	if err := r.saveThreadRecord(record); err != nil {
		return nil, err
	}
	_ = r.appendThreadCompacted(record.ID, compacted.Summary, record.Items, now)
	return &ThreadCompactStartResponse{}, nil
}

func (r *Router) handleThreadApproveGuardianDeniedAction(request *Request) (*ThreadApproveGuardianDeniedActionResponse, error) {
	var params ThreadApproveGuardianDeniedActionParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if _, err := r.store.Load(session.ThreadID(params.ThreadID)); err != nil {
		return nil, err
	}
	return &ThreadApproveGuardianDeniedActionResponse{}, nil
}

func (r *Router) handleThreadMetadataUpdate(request *Request) (*ThreadMetadataUpdateResponse, error) {
	var params ThreadMetadataUpdateParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	current, err := r.readThreadRecord(session.ThreadID(params.ThreadID), true, true)
	if err != nil {
		if !errors.Is(err, session.ErrThreadNotFound) {
			return nil, err
		}
		current, err = r.repairThreadRecordFromRollout(session.ThreadID(params.ThreadID))
		if err != nil {
			return nil, threadMetadataWriteError(params.ThreadID, err)
		}
	}
	patch, err := MetadataPatchToSessionWithExisting(&params, current.Metadata.Git)
	if err != nil {
		return nil, err
	}
	record, err := r.updateThreadMetadata(session.ThreadID(params.ThreadID), &patch, true)
	if err != nil {
		return nil, threadMetadataWriteError(params.ThreadID, err)
	}
	if params.GitInfo != nil {
		if err := r.appendThreadMetadataRollout(record, r.now().UTC()); err != nil {
			return nil, err
		}
		if err := updateRustStateThreadGitInfo(codexHomeFromSessionStore(r.store), params.ThreadID, record.Metadata.Git); err != nil {
			return nil, fmt.Errorf("failed to update sqlite thread git metadata: %w", err)
		}
	}
	path := r.threadRolloutPath(record)
	return &ThreadMetadataUpdateResponse{Thread: BuildThread(record, path, false)}, nil
}

func (r *Router) handleThreadSectionMove(request *Request) (*ThreadSectionMoveResponse, error) {
	var params ThreadSectionMoveParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	var beforeThreadID *session.ThreadID
	if params.BeforeThreadID != nil {
		value := session.ThreadID(*params.BeforeThreadID)
		beforeThreadID = &value
	}
	_, err := r.store.MoveThreadToSection(session.ThreadID(params.ThreadID), params.SectionID.Value, beforeThreadID)
	if err != nil {
		switch {
		case errors.Is(err, session.ErrThreadNotFound):
			return nil, jsonRPCInvalidRequest(fmt.Sprintf("thread not found: %s", strings.TrimSpace(params.ThreadID)))
		case errors.Is(err, session.ErrThreadSectionMissing):
			sectionID := ""
			if params.SectionID.Value != nil {
				sectionID = strings.TrimSpace(*params.SectionID.Value)
			}
			return nil, jsonRPCInvalidRequest(fmt.Sprintf("thread section not found: %s", sectionID))
		default:
			return nil, jsonRPCInvalidRequest(err.Error())
		}
	}
	return &ThreadSectionMoveResponse{}, nil
}

func (r *Router) handleThreadSectionCreate(request *Request) (*ThreadSectionCreateResponse, error) {
	var params ThreadSectionCreateParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	section, err := r.store.CreateSection(params.Name, sessionAppearanceFromProtocol(params.Appearance))
	if err != nil {
		return nil, jsonRPCInvalidRequest(err.Error())
	}
	return &ThreadSectionCreateResponse{Section: protocolSectionFromStore(section)}, nil
}

func (r *Router) handleThreadSectionUpdate(request *Request) (*ThreadSectionUpdateResponse, error) {
	var params ThreadSectionUpdateParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	section, err := r.store.UpdateSection(params.SectionID, params.Name, sessionAppearanceFromProtocol(params.Appearance), params.AppearanceSet)
	if err != nil {
		if errors.Is(err, session.ErrThreadSectionMissing) {
			return nil, jsonRPCInvalidRequest(fmt.Sprintf("thread section not found: %s", strings.TrimSpace(params.SectionID)))
		}
		return nil, jsonRPCInvalidRequest(err.Error())
	}
	return &ThreadSectionUpdateResponse{Section: protocolSectionFromStore(section)}, nil
}

func (r *Router) handleThreadSectionDelete(request *Request) (*ThreadSectionDeleteResponse, error) {
	var params ThreadSectionDeleteParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if err := r.store.DeleteSection(params.SectionID); err != nil {
		if errors.Is(err, session.ErrThreadSectionMissing) {
			return nil, jsonRPCInvalidRequest(fmt.Sprintf("thread section not found: %s", strings.TrimSpace(params.SectionID)))
		}
		return nil, jsonRPCInvalidRequest(err.Error())
	}
	return &ThreadSectionDeleteResponse{Deleted: true}, nil
}

func sessionAppearanceFromProtocol(appearance *ThreadSectionAppearance) *session.ThreadSectionAppearance {
	if appearance == nil {
		return nil
	}
	return &session.ThreadSectionAppearance{
		Icon:  cloneStringPtrAppserver(appearance.Icon),
		Color: cloneStringPtrAppserver(appearance.Color),
	}
}

func protocolSectionFromStore(section *session.ThreadSection) ThreadSection {
	if section == nil {
		return ThreadSection{}
	}
	out := ThreadSection{ID: section.ID, Name: section.Name}
	if section.Appearance != nil {
		out.Appearance = &ThreadSectionAppearance{
			Icon:  cloneStringPtrAppserver(section.Appearance.Icon),
			Color: cloneStringPtrAppserver(section.Appearance.Color),
		}
	}
	return out
}

func (r *Router) handleThreadList(request *Request) (*ThreadListResponse, error) {
	var params ThreadListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	options, err := BuildListOptions(&params)
	if err != nil {
		return nil, err
	}
	var page *session.Page
	if r.state != nil {
		if !params.UseStateDBOnly {
			r.repairStateThreadListing(options.Search != "")
		}
		page, err = r.listRecordsFromState(options)
	} else {
		records, readErr := r.store.AllRecords()
		if readErr != nil {
			return nil, readErr
		}
		records = materializedThreadRecords(records)
		if params.UseStateDBOnly {
			page, err = session.ListRecords(records, options)
		} else {
			page, err = r.listRecordsIncludingRollouts(records, options, false)
		}
	}
	if err != nil {
		return nil, err
	}
	return BuildListResponse(page, r.store, false)
}

func (r *Router) repairStateThreadListing(fullReconcile bool) {
	if r == nil || r.state == nil || r.store == nil {
		return
	}
	codexHome := codexHomeFromSessionStore(r.store)
	for _, archived := range []bool{false, true} {
		paths, err := rollout.CollectRolloutPaths(filepath.Join(codexHome, map[bool]string{false: rollout.SessionsSubdir, true: rollout.ArchivedSessionsSubdir}[archived]))
		if err != nil {
			continue
		}
		for _, path := range paths {
			threadID, err := rollout.ThreadIDFromPath(path)
			if err != nil {
				continue
			}
			if fullReconcile {
				_ = r.state.ReconcileRollout(context.Background(), path, archived)
			} else {
				_ = r.state.ReadRepairRolloutPath(context.Background(), threadID, path, archived)
			}
		}
	}
}

func (r *Router) listRecordsFromState(options session.ListOptions) (*session.Page, error) {
	rows, err := r.state.ListThreadRows(context.Background())
	if err != nil {
		return nil, err
	}
	records := make([]session.Record, 0, len(rows))
	for i := range rows {
		row := &rows[i]
		path, exists := rollout.ExistingRolloutPath(row.RolloutPath)
		if !exists {
			continue
		}
		record := session.Record{
			ID:             session.ThreadID(row.ID),
			SessionID:      row.ID,
			ParentThreadID: session.ThreadID(nullString(row.ParentThreadID)),
			Title:          nullString(row.Name),
			Preview:        nullString(row.Preview),
			Archived:       row.Archived,
			CreatedAt:      time.UnixMilli(row.CreatedAtMS).UTC(),
			UpdatedAt:      time.UnixMilli(row.UpdatedAtMS).UTC(),
			RecencyAt:      time.UnixMilli(row.RecencyAtMS).UTC(),
			Metadata: session.Metadata{
				CWD:            nullString(row.CWD),
				Model:          nullString(row.Model),
				ModelProvider:  nullString(row.ModelProvider),
				Source:         row.Source,
				ThreadSource:   nullString(row.ThreadSource),
				HistoryMode:    row.HistoryMode,
				MemoryMode:     nullString(row.MemoryMode),
				AgentNickname:  nullString(row.AgentNickname),
				AgentRole:      nullString(row.AgentRole),
				AgentPath:      nullString(row.AgentPath),
				CLIVersion:     nullString(row.CLIVersion),
				SandboxPolicy:  nullString(row.SandboxPolicy),
				ApprovalPolicy: nullString(row.ApprovalMode),
				Extra: map[string]any{
					"rollout_path":       path,
					"rollout_title":      nullString(row.Title),
					"first_user_message": nullString(row.FirstUserMessage),
					"reasoning_effort":   nullString(row.ReasoningEffort),
					"tokens_used":        row.TokensUsed,
				},
			},
		}
		if row.Name.Valid && strings.TrimSpace(row.Name.String) != "" {
			record.Metadata.Extra[explicitThreadNameExtraKey] = true
		}
		if row.GitSHA.Valid || row.GitBranch.Valid || row.GitOriginURL.Valid {
			record.Metadata.Git = map[string]string{
				"sha":        nullString(row.GitSHA),
				"branch":     nullString(row.GitBranch),
				"origin_url": nullString(row.GitOriginURL),
			}
		}
		if row.SectionID.Valid {
			record.Section = &session.ThreadSection{ID: row.SectionID.String, Name: nullString(row.SectionName)}
		}
		if row.SectionPosition.Valid {
			position := row.SectionPosition.Int64
			record.SectionPosition = &position
		}
		if row.SectionEnteredAtMS.Valid {
			entered := time.UnixMilli(row.SectionEnteredAtMS.Int64).UTC()
			record.SectionEnteredAt = &entered
		}
		records = append(records, record)
	}
	return session.ListRecords(records, options)
}

func nullString(value sql.NullString) string {
	if value.Valid {
		return value.String
	}
	return ""
}

func (r *Router) handleThreadSectionList(request *Request) (*ThreadSectionListResponse, error) {
	var params ThreadSectionListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	limit := defaultPageSize
	if params.Limit != nil {
		limit = *params.Limit
		if limit < 1 {
			limit = 1
		}
		if limit > maxThreadListPageSize {
			limit = maxThreadListPageSize
		}
	}
	cursor := ""
	if params.Cursor != nil {
		cursor = strings.TrimSpace(*params.Cursor)
	}
	sections, next, err := r.store.ListSections(cursor, limit)
	if err != nil {
		return nil, err
	}
	data := make([]ThreadSection, 0, len(sections))
	for _, section := range sections {
		data = append(data, ThreadSection{ID: section.ID, Name: section.Name})
	}
	return &ThreadSectionListResponse{Data: data, NextCursor: stringPtrIfNotEmpty(next)}, nil
}

func materializedThreadRecords(records []session.Record) []session.Record {
	out := make([]session.Record, 0, len(records))
	for i := range records {
		if !unmaterializedThread(&records[i]) {
			out = append(out, records[i])
		}
	}
	return out
}

func (r *Router) listRecordsIncludingRollouts(records []session.Record, options session.ListOptions, includeHistory bool) (*session.Page, error) {
	records = append([]session.Record(nil), records...)
	seen := make(map[session.ThreadID]bool, len(records))
	for i := range records {
		seen[records[i].ID] = true
	}
	codexHome := codexHomeFromSessionStore(r.store)
	for _, archived := range []bool{false, true} {
		rolloutPage, err := rollout.ListThreads(codexHome, rollout.ListOptions{Archived: archived, PageSize: 0})
		if err != nil {
			continue
		}
		for i := range rolloutPage.Items {
			threadID := session.ThreadID(rolloutPage.Items[i].ThreadID)
			if threadID == "" || seen[threadID] {
				continue
			}
			record, err := rollout.RecordFromPath(rolloutPage.Items[i].Path, archived)
			if err != nil {
				continue
			}
			if !includeHistory {
				record.Items = nil
			}
			records = append(records, *record)
			seen[record.ID] = true
		}
	}
	return session.ListRecords(records, options)
}

func (r *Router) handleThreadItemsList(request *Request) (*ThreadItemsListResponse, error) {
	var params ThreadItemsListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if r.state != nil {
		mode, _, err := r.threadHistoryModeWithRepair(session.ThreadID(params.ThreadID))
		if err != nil {
			return nil, err
		}
		if strings.EqualFold(strings.TrimSpace(mode), string(ThreadHistoryPaginated)) {
			return r.buildPaginatedThreadItemsResponse(&params)
		}
		return nil, methodNotFound("thread/items/list is not supported yet")
	}
	record, err := r.readThreadRecord(session.ThreadID(params.ThreadID), true, true)
	if err != nil {
		record, err = r.readThreadRecordFromRollout(session.ThreadID(params.ThreadID), true, true)
		if err != nil {
			return nil, threadItemsListReadError(params.ThreadID, err)
		}
	}
	if unmaterializedThread(record) {
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("thread %s is not materialized yet; thread/items/list is unavailable before first user message", record.ID))
	}
	return BuildItemsResponse(record, &params)
}

func (r *Router) handleThreadTurnsList(request *Request) (*TurnsPage, error) {
	var params ThreadTurnsListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if r.state != nil {
		mode, found, err := r.threadHistoryModeWithRepair(session.ThreadID(params.ThreadID))
		if err != nil {
			return nil, err
		}
		if found && strings.EqualFold(strings.TrimSpace(mode), string(ThreadHistoryPaginated)) {
			return r.buildPaginatedThreadTurnsResponse(&params, nil)
		}
	}
	record, err := r.readThreadRecord(session.ThreadID(params.ThreadID), true, true)
	if err != nil {
		record, err = r.readThreadRecordFromRollout(session.ThreadID(params.ThreadID), true, true)
		if err != nil {
			return nil, threadTurnsListReadError(params.ThreadID, err)
		}
	}
	if unmaterializedThread(record) {
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("thread %s is not materialized yet; thread/turns/list is unavailable before first user message", record.ID))
	}
	r.attachRolloutTurnSnapshots(record)
	return BuildTurnsResponse(record, &params)
}

func (r *Router) threadHistoryModeWithRepair(threadID session.ThreadID) (string, bool, error) {
	if r == nil || r.state == nil {
		return "", false, nil
	}
	ctx := context.Background()
	mode, found, err := r.state.ThreadHistoryMode(ctx, string(threadID))
	if err != nil || found {
		return mode, found, err
	}
	for _, archived := range []bool{false, true} {
		if _, findErr := r.findThreadRolloutPath(threadID, archived); findErr == nil {
			break
		}
	}
	return r.state.ThreadHistoryMode(ctx, string(threadID))
}

func (r *Router) buildPaginatedThreadItemsResponse(params *ThreadItemsListParams) (*ThreadItemsListResponse, error) {
	limit := threadItemsDefaultLimit
	if params.Limit != nil {
		limit = *params.Limit
	}
	if limit < 1 {
		limit = 1
	}
	if limit > threadItemsMaxLimit {
		limit = threadItemsMaxLimit
	}
	direction := state.ThreadHistorySortAsc
	if params.SortDirection == SortDesc {
		direction = state.ThreadHistorySortDesc
	}
	page, err := r.state.ListThreadHistoryItems(context.Background(), state.ThreadHistoryListItemsParams{
		ThreadID:      params.ThreadID,
		TurnID:        params.TurnID,
		Cursor:        params.Cursor,
		PageSize:      limit,
		SortDirection: direction,
	})
	if err != nil {
		return nil, paginatedThreadHistoryError(err, true)
	}
	data := make([]ThreadItemEntry, 0, len(page.Items))
	for _, stored := range page.Items {
		item, err := deserializeStateThreadItem(stored)
		if err != nil {
			return nil, err
		}
		data = append(data, ThreadItemEntry{TurnID: stored.TurnID, Item: item})
	}
	return &ThreadItemsListResponse{Data: data, NextCursor: page.NextCursor, BackwardsCursor: page.BackwardsCursor}, nil
}

func (r *Router) buildPaginatedThreadTurnsResponse(params *ThreadTurnsListParams, options *turnsResponseOptions) (*TurnsPage, error) {
	limit := threadTurnsDefaultLimit
	if params.Limit != nil {
		limit = *params.Limit
	}
	if limit < 1 {
		limit = 1
	}
	if limit > threadTurnsMaxLimit {
		limit = threadTurnsMaxLimit
	}
	direction := state.ThreadHistorySortDesc
	if params.SortDirection == SortAsc {
		direction = state.ThreadHistorySortAsc
	}
	itemsView := params.ItemsView
	if itemsView == "" {
		itemsView = TurnItemsSummary
	}
	storedView := state.ThreadHistoryItemsNotLoaded
	if itemsView == TurnItemsSummary {
		storedView = state.ThreadHistoryItemsSummary
	}
	page, err := r.state.ListThreadHistoryTurns(context.Background(), state.ThreadHistoryListTurnsParams{
		ThreadID:      params.ThreadID,
		Cursor:        params.Cursor,
		PageSize:      limit,
		SortDirection: direction,
		ItemsView:     storedView,
	})
	if err != nil {
		return nil, paginatedThreadHistoryError(err, false)
	}
	turns := make([]Turn, 0, len(page.Turns))
	for _, stored := range page.Turns {
		turn, err := stateThreadTurnToAPI(stored, itemsView)
		if err != nil {
			return nil, err
		}
		if itemsView == TurnItemsFull {
			items, err := r.loadPaginatedTurnFullItems(params.ThreadID, turn.ID)
			if err != nil {
				return nil, err
			}
			turn.Items = items
		}
		turns = append(turns, turn)
	}
	if options == nil {
		normalizeThreadTurnsStatus(turns, IdleStatus(), false)
	} else {
		normalizeThreadTurnsStatus(turns, options.LoadedStatus, options.HasLiveRunningThread)
	}
	return &TurnsPage{Data: turns, NextCursor: page.NextCursor, BackwardsCursor: page.BackwardsCursor}, nil
}

func (r *Router) loadPaginatedTurnFullItems(threadID, turnID string) ([]ThreadItem, error) {
	var cursor *string
	items := []ThreadItem{}
	for {
		page, err := r.state.ListThreadHistoryItems(context.Background(), state.ThreadHistoryListItemsParams{
			ThreadID: threadID, TurnID: &turnID, Cursor: cursor, PageSize: threadItemsMaxLimit, SortDirection: state.ThreadHistorySortAsc,
		})
		if err != nil {
			return nil, paginatedThreadHistoryError(err, false)
		}
		for _, stored := range page.Items {
			item, err := deserializeStateThreadItem(stored)
			if err != nil {
				return nil, err
			}
			items = append(items, item)
		}
		if page.NextCursor == nil {
			return items, nil
		}
		if cursor != nil && *cursor == *page.NextCursor {
			return nil, fmt.Errorf("failed to load full turn items for %s: thread store returned a repeated cursor", turnID)
		}
		cursor = page.NextCursor
	}
}

func deserializeStateThreadItem(stored state.ThreadHistoryItem) (ThreadItem, error) {
	var item ThreadItem
	if err := json.Unmarshal(stored.ItemJSON, &item); err != nil {
		return ThreadItem{}, fmt.Errorf("failed to deserialize stored thread item %s: %w", stored.ItemID, err)
	}
	return item, nil
}

func stateThreadTurnToAPI(stored state.ThreadHistoryTurn, itemsView TurnItemsView) (Turn, error) {
	turn := Turn{
		ID: stored.TurnID, Items: []ThreadItem{}, ItemsView: itemsView, Status: TurnStatus(stored.Status),
		StartedAt: stored.StartedAt, CompletedAt: stored.CompletedAt, DurationMS: stored.DurationMS,
	}
	if len(stored.ErrorJSON) > 0 {
		var decoded struct {
			Message                string         `json:"message"`
			CodexErrorInfo         CodexErrorInfo `json:"codexErrorInfo"`
			CodexErrorInfoSnake    CodexErrorInfo `json:"codex_error_info"`
			AdditionalDetails      *string        `json:"additionalDetails"`
			AdditionalDetailsSnake *string        `json:"additional_details"`
		}
		if err := json.Unmarshal(stored.ErrorJSON, &decoded); err != nil {
			return Turn{}, fmt.Errorf("failed to deserialize stored turn error %s: %w", stored.TurnID, err)
		}
		info := decoded.CodexErrorInfo
		if info == nil {
			info = decoded.CodexErrorInfoSnake
		}
		details := decoded.AdditionalDetails
		if details == nil {
			details = decoded.AdditionalDetailsSnake
		}
		turn.Error = &TurnError{Message: decoded.Message, CodexErrorInfo: info, AdditionalDetails: details}
	}
	for _, storedItem := range stored.Items {
		item, err := deserializeStateThreadItem(storedItem)
		if err != nil {
			return Turn{}, err
		}
		turn.Items = append(turn.Items, item)
	}
	return turn, nil
}

func paginatedThreadHistoryError(err error, itemsMethod bool) error {
	var historyErr *state.ThreadHistoryError
	if errors.As(err, &historyErr) {
		switch historyErr.Kind {
		case state.ThreadHistoryInvalidRequest:
			return jsonRPCInvalidRequest(historyErr.Error())
		case state.ThreadHistoryUnsupported:
			if itemsMethod {
				return methodNotFound("thread/items/list is not supported yet")
			}
			return methodNotFound(historyErr.Operation + " is not supported yet")
		case state.ThreadHistoryNotFound:
			return jsonRPCInvalidRequest("no rollout found for thread id " + historyErr.ThreadID)
		}
	}
	return fmt.Errorf("failed to list thread history: %w", err)
}

func threadItemsListReadError(threadID string, err error) error {
	if errors.Is(err, session.ErrThreadNotFound) {
		return jsonRPCInvalidRequest(fmt.Sprintf("no rollout found for thread id %s", strings.TrimSpace(threadID)))
	}
	return err
}

func threadTurnsListReadError(threadID string, err error) error {
	if errors.Is(err, session.ErrThreadNotFound) {
		return jsonRPCInvalidRequest(fmt.Sprintf("thread not loaded: %s", strings.TrimSpace(threadID)))
	}
	return err
}

func (r *Router) handleThreadSearch(request *Request) (*ThreadSearchResponse, error) {
	var params ThreadSearchParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	searchTerm := strings.TrimSpace(params.SearchTerm)
	listParams := &ThreadListParams{
		Cursor:        params.Cursor,
		Limit:         params.Limit,
		SortKey:       params.SortKey,
		SortDirection: params.SortDirection,
		SourceKinds:   append([]ThreadSourceKind(nil), params.SourceKinds...),
		Archived:      params.Archived,
		SearchTerm:    &searchTerm,
	}
	options, err := BuildListOptions(listParams)
	if err != nil {
		return nil, err
	}
	records, err := r.store.AllRecords()
	if err != nil {
		return nil, err
	}
	page, err := r.listRecordsIncludingRollouts(records, options, false)
	if err != nil {
		return nil, err
	}
	data := make([]ThreadSearchResult, 0, len(page.Records))
	for i := range page.Records {
		record := &page.Records[i]
		path := r.threadRolloutPath(record)
		thread := BuildThread(record, path, false)
		if thread == nil {
			continue
		}
		data = append(data, ThreadSearchResult{Thread: *thread, Snippet: searchSnippet(record, searchTerm)})
	}
	var next *string
	if page.NextCursor != "" {
		next = &page.NextCursor
	}
	var backwards *string
	if page.BackwardsCursor != "" {
		backwards = &page.BackwardsCursor
	}
	return &ThreadSearchResponse{Data: data, NextCursor: next, BackwardsCursor: backwards}, nil
}

func (r *Router) handleThreadSearchOccurrences(request *Request) (*ThreadSearchOccurrencesResponse, error) {
	var params ThreadSearchOccurrencesParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if r.state != nil {
		mode, found, err := r.threadHistoryModeWithRepair(session.ThreadID(strings.TrimSpace(params.ThreadID)))
		if err != nil {
			return nil, err
		}
		if !found {
			return nil, jsonRPCInvalidRequest("no rollout found for thread id " + strings.TrimSpace(params.ThreadID))
		}
		if !strings.EqualFold(strings.TrimSpace(mode), string(ThreadHistoryPaginated)) {
			return nil, methodNotFound("thread/searchOccurrences is not supported yet")
		}
		return r.buildPaginatedThreadSearchOccurrencesResponse(&params)
	}
	record, err := r.readThreadRecord(session.ThreadID(strings.TrimSpace(params.ThreadID)), true, true)
	if err != nil {
		if !errors.Is(err, session.ErrThreadNotFound) {
			return nil, err
		}
		record, err = r.repairThreadRecordFromRollout(session.ThreadID(strings.TrimSpace(params.ThreadID)))
		if err != nil {
			return nil, err
		}
	}
	return buildThreadSearchOccurrences(record, &params)
}

func (r *Router) buildPaginatedThreadSearchOccurrencesResponse(params *ThreadSearchOccurrencesParams) (*ThreadSearchOccurrencesResponse, error) {
	pageSize := threadSearchOccurrencesDefaultLimit
	if params.Limit != nil {
		pageSize = int(*params.Limit)
	}
	if pageSize < 1 {
		pageSize = 1
	}
	if pageSize > threadSearchOccurrencesMaxLimit {
		pageSize = threadSearchOccurrencesMaxLimit
	}
	page, err := r.state.SearchThreadHistoryOccurrences(context.Background(), state.ThreadHistorySearchOccurrencesParams{
		ThreadID: params.ThreadID, SearchTerm: params.SearchTerm, Cursor: params.Cursor, PageSize: pageSize,
	})
	if err != nil {
		var historyErr *state.ThreadHistoryError
		if errors.As(err, &historyErr) {
			switch historyErr.Kind {
			case state.ThreadHistoryInvalidRequest:
				return nil, jsonRPCInvalidRequest(historyErr.Error())
			case state.ThreadHistoryUnsupported:
				return nil, methodNotFound(historyErr.Operation + " is not supported yet")
			case state.ThreadHistoryNotFound:
				return nil, jsonRPCInvalidRequest("no rollout found for thread id " + historyErr.ThreadID)
			}
		}
		return nil, fmt.Errorf("failed to search thread occurrences: %w", err)
	}
	data := make([]ThreadSearchOccurrence, 0, len(page.Items))
	for _, item := range page.Items {
		data = append(data, ThreadSearchOccurrence{
			TurnID: item.TurnID, ItemID: item.ItemID, Snippet: item.Snippet,
			SnippetMatchRange: ThreadSearchTextRange{Start: item.SnippetMatchRange.Start, End: item.SnippetMatchRange.End},
			TurnCursor:        item.TurnCursor,
		})
	}
	return &ThreadSearchOccurrencesResponse{Data: data, NextCursor: page.NextCursor}, nil
}

func (r *Router) handleThreadLoadedList(request *Request) (*ThreadLoadedListResponse, error) {
	var params ThreadLoadedListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	page, err := r.store.List(session.ListOptions{PageSize: 0, SortKey: session.SortCreatedAt, SortDirection: session.SortAsc})
	if err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(page.Records))
	for i := range page.Records {
		ids = append(ids, string(page.Records[i].ID))
	}
	sort.Strings(ids)
	total := len(ids)
	start := 0
	if params.Cursor != nil && strings.TrimSpace(*params.Cursor) != "" {
		cursor := strings.TrimSpace(*params.Cursor)
		index := sort.SearchStrings(ids, cursor)
		if index < total && ids[index] == cursor {
			start = index + 1
		} else {
			start = index
		}
	}
	if start >= total {
		return &ThreadLoadedListResponse{Data: []string{}}, nil
	}
	limit := total - start
	if params.Limit != nil {
		limit = *params.Limit
		if limit < 1 {
			limit = 1
		}
	}
	end := start + limit
	if end > total {
		end = total
	}
	var next *string
	if end < total {
		value := ids[end-1]
		next = &value
	}
	return &ThreadLoadedListResponse{Data: append([]string(nil), ids[start:end]...), NextCursor: next}, nil
}

func (r *Router) handleThreadRollback(request *Request) (*ThreadRollbackResponse, error) {
	var params ThreadRollbackParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	record, err := r.readThreadRecord(session.ThreadID(params.ThreadID), true, true)
	if err != nil {
		if !errors.Is(err, session.ErrThreadNotFound) {
			return nil, err
		}
		record, err = r.repairThreadRecordFromRollout(session.ThreadID(params.ThreadID))
		if err != nil {
			return nil, err
		}
	}
	if threadUsesPaginatedHistory(record) {
		return nil, jsonRPCInvalidRequest("paginated threads do not support thread/rollback")
	}
	record.Items = rollbackItems(record.Items, params.NumTurns)
	record.UpdatedAt = r.now().UTC()
	record.RecencyAt = record.UpdatedAt
	if err := r.saveThreadRecord(record); err != nil {
		return nil, err
	}
	_ = r.appendThreadRollback(record.ID, params.NumTurns, record.UpdatedAt)
	path := r.threadRolloutPath(record)
	return &ThreadRollbackResponse{Thread: BuildThread(record, path, true)}, nil
}

func (r *Router) handleThreadRevert(request *Request) (*ThreadRevertResponse, error) {
	var params ThreadRevertParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	threadID := session.ThreadID(strings.TrimSpace(params.ThreadID))
	record, err := r.store.Revert(threadID, params.BeforeTurnID)
	if err != nil {
		if errors.Is(err, session.ErrThreadNotFound) {
			return nil, jsonRPCInvalidRequest(fmt.Sprintf("thread %s not found", threadID))
		}
		if errors.Is(err, session.ErrInvalidThreadID) {
			return nil, jsonRPCInvalidRequest(err.Error())
		}
		return nil, err
	}
	path := r.threadRolloutPath(record)
	turnsCursor, itemsCursor := threadResumeHeadCursors(record)
	return &ThreadRevertResponse{
		Thread:               BuildThread(record, path, false),
		TurnsBackwardsCursor: turnsCursor,
		ItemsBackwardsCursor: itemsCursor,
	}, nil
}

func (r *Router) handleThreadQueueAdd(request *Request) (*ThreadQueueAddResponse, error) {
	var params ThreadQueueAddParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	submission, err := r.store.EnqueueSubmission(session.ThreadID(strings.TrimSpace(params.ThreadID)), session.QueuedSubmission{
		Input:               append([]any(nil), params.Input...),
		ClientUserMessageID: strings.TrimSpace(params.ClientUserMessageID),
	})
	if err != nil {
		return nil, threadQueueError(err)
	}
	return &ThreadQueueAddResponse{QueuedSubmission: queuedSubmissionFromSession(submission)}, nil
}

func (r *Router) handleThreadQueueList(request *Request) (*ThreadQueueListResponse, error) {
	var params ThreadQueueListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	cursor := ""
	if params.Cursor != nil {
		cursor = *params.Cursor
	}
	limit := 0
	if params.Limit != nil {
		limit = *params.Limit
	}
	submissions, nextCursor, err := r.store.ListQueueSubmissions(session.ThreadID(strings.TrimSpace(params.ThreadID)), cursor, limit)
	if err != nil {
		return nil, threadQueueError(err)
	}
	data := make([]QueuedSubmission, len(submissions))
	for i := range submissions {
		data[i] = *queuedSubmissionFromSession(&submissions[i])
	}
	response := &ThreadQueueListResponse{Data: data}
	if strings.TrimSpace(nextCursor) != "" {
		response.NextCursor = stringPtrIfNotEmpty(nextCursor)
	}
	return response, nil
}

func (r *Router) handleThreadQueueUpdate(request *Request) (*ThreadQueueUpdateResponse, error) {
	var params ThreadQueueUpdateParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	submission, err := r.store.UpdateQueueSubmission(session.ThreadID(strings.TrimSpace(params.ThreadID)), strings.TrimSpace(params.QueuedSubmissionID), params.Input)
	if err != nil {
		return nil, threadQueueError(err)
	}
	return &ThreadQueueUpdateResponse{QueuedSubmission: queuedSubmissionFromSession(submission)}, nil
}

func (r *Router) handleThreadQueueDelete(request *Request) (*ThreadQueueDeleteResponse, error) {
	var params ThreadQueueDeleteParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	deleted, err := r.store.DeleteQueueSubmission(session.ThreadID(strings.TrimSpace(params.ThreadID)), strings.TrimSpace(params.QueuedSubmissionID))
	if err != nil {
		return nil, threadQueueError(err)
	}
	return &ThreadQueueDeleteResponse{Deleted: deleted}, nil
}

func (r *Router) handleThreadQueueReorder(request *Request) (*ThreadQueueReorderResponse, error) {
	var params ThreadQueueReorderParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if err := r.store.ReorderQueueSubmissions(session.ThreadID(strings.TrimSpace(params.ThreadID)), append([]string(nil), params.QueuedSubmissionIDs...)); err != nil {
		return nil, threadQueueError(err)
	}
	return &ThreadQueueReorderResponse{}, nil
}

func queuedSubmissionFromSession(submission *session.QueuedSubmission) *QueuedSubmission {
	if submission == nil {
		return nil
	}
	return &QueuedSubmission{
		ID:                  submission.ID,
		Input:               append([]any(nil), submission.Input...),
		ClientUserMessageID: submission.ClientUserMessageID,
	}
}

func threadQueueError(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, session.ErrThreadNotFound) {
		return jsonRPCInvalidRequest(err.Error())
	}
	if errors.Is(err, session.ErrConflict) || errors.Is(err, session.ErrInvalidThreadID) {
		return jsonRPCInvalidRequest(err.Error())
	}
	return err
}

func (r *Router) handleThreadInjectItems(request *Request) (*ThreadInjectItemsResponse, error) {
	var params ThreadInjectItemsParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if err := validateResponseItemImageURLs(params.Items); err != nil {
		return nil, err
	}
	now := r.now().UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	items := make([]session.Item, 0, len(params.Items))
	for i, raw := range params.Items {
		item, err := sessionItemFromRaw(raw, now, i)
		if err != nil {
			return nil, err
		}
		if r != nil && r.retainClientDeveloperMessages != nil && r.retainClientDeveloperMessages() {
			markClientAuthoredDeveloperItem(&item)
		}
		items = append(items, item)
	}
	record, err := r.appendThreadItems(session.ThreadID(params.ThreadID), items)
	if err != nil {
		if !errors.Is(err, session.ErrThreadNotFound) {
			return nil, err
		}
		if _, repairErr := r.repairThreadRecordFromRollout(session.ThreadID(params.ThreadID)); repairErr != nil {
			return nil, repairErr
		}
		record, err = r.appendThreadItems(session.ThreadID(params.ThreadID), items)
		if err != nil {
			return nil, err
		}
	}
	_ = r.appendThreadRollout(record.ID, items, now)
	return &ThreadInjectItemsResponse{}, nil
}

func ParseRequest(data []byte) (*Request, error) {
	var request Request
	if err := json.Unmarshal(data, &request); err != nil {
		return nil, err
	}
	if err := request.Validate(); err != nil {
		return nil, err
	}
	return &request, nil
}

func MarshalMessage(value any) ([]byte, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func errorCode(err error) int {
	switch {
	case errors.Is(err, ErrUnknownMethod):
		return JSONRPCMethodNotFoundErrorCode
	case errors.Is(err, ErrJSONRPCInvalidRequest):
		return JSONRPCInvalidRequestErrorCode
	case errors.Is(err, ErrInvalidRequest), errors.Is(err, session.ErrInvalidThreadID):
		return JSONRPCInvalidParamsErrorCode
	case errors.Is(err, session.ErrThreadNotFound):
		return -32004
	case errors.Is(err, session.ErrConflict):
		return -32009
	default:
		return JSONRPCInternalErrorCode
	}
}

type methodNotFoundError struct {
	message string
}

func (e *methodNotFoundError) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func (e *methodNotFoundError) Unwrap() error {
	return ErrUnknownMethod
}

func (e *methodNotFoundError) Is(target error) bool {
	return target == ErrUnknownMethod
}

func methodNotFound(message string) error {
	return &methodNotFoundError{message: message}
}

func paginatedRolloutHistory(record *session.Record) bool {
	return record != nil && record.FromRollout && strings.EqualFold(strings.TrimSpace(record.Metadata.HistoryMode), string(ThreadHistoryPaginated))
}

func threadUsesPaginatedHistory(record *session.Record) bool {
	return record != nil && strings.EqualFold(strings.TrimSpace(record.Metadata.HistoryMode), string(ThreadHistoryPaginated))
}

func unmaterializedThread(record *session.Record) bool {
	if record == nil || record.FromRollout || len(record.Items) > 0 {
		return false
	}
	path, _ := record.Metadata.Extra["rollout_path"].(string)
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return os.IsNotExist(err)
}

func safeIdentifier(value string) string {
	if value == "" {
		return "0"
	}
	var builder strings.Builder
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z':
			builder.WriteRune(char)
		case char >= 'A' && char <= 'Z':
			builder.WriteRune(char)
		case char >= '0' && char <= '9':
			builder.WriteRune(char)
		case char == '-' || char == '_':
			builder.WriteRune(char)
		default:
			builder.WriteByte('-')
		}
	}
	result := strings.Trim(builder.String(), "-_")
	if result == "" {
		return "0"
	}
	return result
}
