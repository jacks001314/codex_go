package appserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	pathpkg "path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"codex_go/agent"
	"codex_go/compact"
	"codex_go/model"
	"codex_go/rollout"
	"codex_go/sandbox"
	"codex_go/session"

	"github.com/google/uuid"
)

type Router struct {
	store      *session.Store
	now        func() time.Time
	spawnGraph agent.Store
	writersMu  sync.Mutex
	writers    map[session.ThreadID]*session.WriterLock
}

func NewRouter(store *session.Store) *Router {
	return &Router{store: store, now: time.Now, writers: map[session.ThreadID]*session.WriterLock{}}
}

func (r *Router) Close() error {
	if r == nil {
		return nil
	}
	r.writersMu.Lock()
	locks := make([]*session.WriterLock, 0, len(r.writers))
	for threadID, lock := range r.writers {
		locks = append(locks, lock)
		delete(r.writers, threadID)
	}
	r.writersMu.Unlock()
	var closeErr error
	for _, lock := range locks {
		if err := lock.Close(); closeErr == nil && err != nil {
			closeErr = err
		}
	}
	return closeErr
}

func (r *Router) retainPaginatedWriter(record *session.Record) error {
	if !threadUsesPaginatedHistory(record) {
		return nil
	}
	r.writersMu.Lock()
	defer r.writersMu.Unlock()
	if _, ok := r.writers[record.ID]; ok {
		return nil
	}
	lock, err := r.store.AcquireWriter(record.ID)
	if err != nil {
		return writerOwnershipError(err)
	}
	r.writers[record.ID] = lock
	return nil
}

func (r *Router) acquireLifecycleWriters(threadIDs []session.ThreadID) ([]*session.WriterLock, error) {
	r.writersMu.Lock()
	defer r.writersMu.Unlock()
	missing := make([]session.ThreadID, 0, len(threadIDs))
	for _, threadID := range threadIDs {
		if _, ok := r.writers[threadID]; !ok {
			missing = append(missing, threadID)
		}
	}
	locks, err := r.store.AcquireWriters(missing)
	if err != nil {
		return nil, writerOwnershipError(err)
	}
	return locks, nil
}

func (r *Router) releaseRetainedWriters(threadIDs []session.ThreadID) {
	r.writersMu.Lock()
	locks := make([]*session.WriterLock, 0, len(threadIDs))
	for _, threadID := range threadIDs {
		if lock := r.writers[threadID]; lock != nil {
			locks = append(locks, lock)
			delete(r.writers, threadID)
		}
	}
	r.writersMu.Unlock()
	for _, lock := range locks {
		_ = lock.Close()
	}
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
	return rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:               codexHome,
		SessionID:               record.SessionID,
		SessionPrefix:           record.Metadata.SessionPrefix,
		ThreadID:                string(record.ID),
		ForkedFromID:            string(record.ForkedFromID),
		Source:                  record.Metadata.Source,
		ThreadSource:            record.Metadata.ThreadSource,
		Originator:              record.Metadata.Originator,
		CWD:                     record.Metadata.CWD,
		Model:                   record.Metadata.Model,
		ModelProvider:           record.Metadata.ModelProvider,
		HistoryMode:             record.Metadata.HistoryMode,
		MemoryMode:              record.Metadata.MemoryMode,
		ParentThreadID:          string(record.ParentThreadID),
		BaseInstructions:        record.Metadata.BaseInstructions,
		AgentNickname:           record.Metadata.AgentNickname,
		AgentRole:               record.Metadata.AgentRole,
		AgentPath:               record.Metadata.AgentPath,
		DynamicTools:            record.Metadata.DynamicTools,
		SelectedCapabilityRoots: record.Metadata.SelectedCapabilityRoots,
		MultiAgentVersion:       record.Metadata.MultiAgentVersion,
		ContextWindow:           record.Metadata.ContextWindow,
		CLIVersion:              record.Metadata.CLIVersion,
		Git:                     record.Metadata.Git,
		Extra:                   record.Metadata.Extra,
		Now:                     now,
	})
}

func (r *Router) appendThreadMetadataRollout(record *session.Record, now time.Time) error {
	if r == nil || r.store == nil || record == nil {
		return nil
	}
	codexHome := codexHomeFromSessionStore(r.store)
	if codexHome == "" {
		return nil
	}
	path, err := rollout.FindThreadPath(codexHome, string(record.ID), record.Archived)
	if err != nil {
		return nil
	}
	recorder, err := rollout.Resume(path)
	if err != nil {
		return err
	}
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
		ID:                      string(record.ID),
		SessionID:               record.SessionID,
		SessionPrefix:           record.Metadata.SessionPrefix,
		ForkedFromID:            string(record.ForkedFromID),
		Timestamp:               createdAt.Format(time.RFC3339),
		CWD:                     record.Metadata.CWD,
		Model:                   record.Metadata.Model,
		Source:                  record.Metadata.Source,
		ThreadSource:            record.Metadata.ThreadSource,
		Originator:              record.Metadata.Originator,
		ModelProvider:           record.Metadata.ModelProvider,
		HistoryMode:             record.Metadata.HistoryMode,
		MemoryMode:              record.Metadata.MemoryMode,
		ParentThreadID:          string(record.ParentThreadID),
		BaseInstructions:        record.Metadata.BaseInstructions,
		AgentNickname:           record.Metadata.AgentNickname,
		AgentRole:               record.Metadata.AgentRole,
		AgentPath:               record.Metadata.AgentPath,
		DynamicTools:            cloneRawMessages(record.Metadata.DynamicTools),
		SelectedCapabilityRoots: cloneRawMessages(record.Metadata.SelectedCapabilityRoots),
		MultiAgentVersion:       record.Metadata.MultiAgentVersion,
		ContextWindow:           append(json.RawMessage(nil), record.Metadata.ContextWindow...),
		CLIVersion:              record.Metadata.CLIVersion,
		Git:                     cloneStringMap(record.Metadata.Git),
		Extra:                   cloneAnyMapForRouter(record.Metadata.Extra),
	}
}

func (r *Router) appendThreadRollout(threadID session.ThreadID, items []session.Item, now time.Time) error {
	if r == nil || r.store == nil {
		return nil
	}
	codexHome := codexHomeFromSessionStore(r.store)
	path, err := rollout.FindThreadPath(codexHome, string(threadID), false)
	if err != nil {
		return nil
	}
	recorder, err := rollout.Resume(path)
	if err != nil {
		return err
	}
	defer recorder.Close()
	return rollout.AppendSessionItems(recorder, items, now)
}

func (r *Router) appendThreadRollback(threadID session.ThreadID, numTurns int, now time.Time) error {
	if r == nil || r.store == nil || numTurns < 0 {
		return nil
	}
	codexHome := codexHomeFromSessionStore(r.store)
	path, err := rollout.FindThreadPath(codexHome, string(threadID), false)
	if err != nil {
		return nil
	}
	recorder, err := rollout.Resume(path)
	if err != nil {
		return err
	}
	defer recorder.Close()
	return recorder.AppendThreadRolledBack(uint32(numTurns), now)
}

func (r *Router) appendThreadCompacted(threadID session.ThreadID, message string, replacement []session.Item, now time.Time) error {
	if r == nil || r.store == nil {
		return nil
	}
	codexHome := codexHomeFromSessionStore(r.store)
	path, err := rollout.FindThreadPath(codexHome, string(threadID), false)
	if err != nil {
		return nil
	}
	recorder, err := rollout.Resume(path)
	if err != nil {
		return err
	}
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
	path, err := rollout.FindThreadPath(codexHome, string(record.ID), record.Archived)
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
	case MethodThreadList:
		return r.handleThreadList(request)
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
			CWD:                     cwd,
			Model:                   params.Model,
			ModelProvider:           params.ModelProvider,
			ServiceTier:             serviceTier,
			Source:                  string(SessionSourceAppServer),
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
	if err := r.retainPaginatedWriter(record); err != nil {
		_ = r.store.Delete(record.ID)
		return nil, err
	}
	if len(record.Items) > 0 {
		if err := r.createThreadRollout(record, now); err != nil {
			r.releaseRetainedWriters([]session.ThreadID{record.ID})
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
		record, err = r.store.Read(sourceID, true, includeTurns)
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
	if includeTurns {
		r.attachRolloutTurnSnapshots(record)
	}
	if err := r.retainPaginatedWriter(record); err != nil {
		return nil, err
	}
	path := r.threadRolloutPath(record)
	if params.Path != nil && strings.TrimSpace(*params.Path) != "" {
		path = strings.TrimSpace(*params.Path)
	}
	cwd := firstNonEmpty(stringPtrValue(params.CWD), record.Metadata.CWD)
	runtimeWorkspaceRoots := threadRecordRuntimeWorkspaceRoots(record, cwd, params.RuntimeWorkspaceRoots)
	thread := BuildThread(record, path, includeTurns)
	if thread != nil {
		thread.Status = IdleStatus()
	}
	response := &ThreadResumeResponse{
		Thread:                  thread,
		CWD:                     cwd,
		Model:                   firstNonEmpty(stringPtrValue(params.Model), record.Metadata.Model),
		ModelProvider:           firstNonEmpty(stringPtrValue(params.ModelProvider), record.Metadata.ModelProvider),
		ServiceTier:             resumeServiceTier(&params, record),
		ApprovalPolicy:          params.ApprovalPolicy,
		ApprovalsReviewer:       cloneString(params.ApprovalsReviewer),
		Sandbox:                 params.Sandbox,
		RuntimeWorkspaceRoots:   runtimeWorkspaceRoots,
		InstructionSources:      threadRecordInstructionSources(record),
		ActivePermissionProfile: activePermissionProfileFromID(params.Permissions),
	}
	cursorRecord := record
	if !includeTurns {
		if params.Path != nil && strings.TrimSpace(*params.Path) != "" {
			cursorRecord, err = r.readThreadRecordFromRolloutPath(*params.Path, true, true)
		} else {
			cursorRecord, err = r.store.Read(sourceID, true, true)
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
	if ShouldRedactThreadResumePayloads(params.ClientName) && response.Thread != nil {
		response.Thread.Turns = RedactThreadResumePayloads(response.Thread.Turns)
	}
	if params.InitialTurnsPage != nil {
		pageRecord := record
		if !includeTurns {
			if params.Path != nil && strings.TrimSpace(*params.Path) != "" {
				pageRecord, err = r.readThreadRecordFromRolloutPath(*params.Path, true, true)
				if err != nil {
					return nil, err
				}
			} else {
				pageRecord, err = r.store.Read(sourceID, true, true)
				if err != nil {
					pageRecord, err = r.readThreadRecordFromRollout(sourceID, true, true)
					if err != nil {
						return nil, err
					}
				}
			}
			r.attachRolloutTurnSnapshots(pageRecord)
		}
		page, err := BuildTurnsResponse(pageRecord, &ThreadTurnsListParams{
			ThreadID:      string(sourceID),
			Limit:         params.InitialTurnsPage.Limit,
			SortDirection: params.InitialTurnsPage.SortDirection,
			ItemsView:     params.InitialTurnsPage.ItemsView,
		})
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
			Source:        string(SessionSourceAppServer),
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
	response.TurnsBackwardsCursor, response.ItemsBackwardsCursor = threadResumeHeadCursors(record)
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
	codexHome := codexHomeFromSessionStore(r.store)
	for _, archived := range []bool{false, true} {
		if archived && !includeArchived {
			continue
		}
		path, err := rollout.FindThreadPath(codexHome, string(threadID), archived)
		if err != nil {
			continue
		}
		record, err := rollout.RecordFromPath(path, archived)
		if err != nil {
			return nil, err
		}
		if !includeHistory {
			record.Items = nil
		}
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
	record, err := rollout.RecordFromPath(trimmed, archived)
	if err != nil {
		return nil, err
	}
	if !includeHistory {
		record.Items = nil
	}
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
	if err := r.store.Save(record); err != nil {
		return nil, err
	}
	return record, nil
}

func (r *Router) repairThreadRecordFromActiveRollout(threadID session.ThreadID) (*session.Record, error) {
	record, err := r.readThreadRecordFromRollout(threadID, false, true)
	if err != nil {
		return nil, err
	}
	if err := r.store.Save(record); err != nil {
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
	record, err := r.store.Read(session.ThreadID(params.ThreadID), true, params.IncludeTurns)
	if err != nil {
		record, err = r.readThreadRecordFromRollout(session.ThreadID(params.ThreadID), true, params.IncludeTurns)
		if err != nil {
			return nil, threadReadError(params.ThreadID, err)
		}
	}
	if params.IncludeTurns {
		if unmaterializedThread(record) {
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
	var sourceRecord *session.Record
	var err error
	if params.Path != nil && strings.TrimSpace(*params.Path) != "" {
		sourceRecord, err = r.readThreadRecordFromRolloutPath(*params.Path, true, true)
		if err != nil {
			return nil, err
		}
	} else {
		sourceID, err := threadForkSourceID(&params)
		if err != nil {
			return nil, err
		}
		sourceRecord, err = r.store.Read(sourceID, true, true)
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
	if unmaterializedThread(sourceRecord) {
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("no rollout found for thread id %s", sourceRecord.ID))
	}
	r.attachRolloutTurnSnapshots(sourceRecord)
	record, err := r.store.ForkRecord(sourceRecord, session.ForkOptions{
		Mode:         mode,
		LastN:        params.LastN,
		LastTurnID:   params.LastTurnID,
		BeforeTurnID: params.BeforeTurnID,
		Ephemeral:    params.Ephemeral,
		Now:          r.now().UTC(),
	})
	if err != nil {
		return nil, threadForkRecordError(err)
	}
	if !params.Ephemeral {
		if err := r.retainPaginatedWriter(record); err != nil {
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
		if err := r.store.Save(record); err != nil {
			return nil, err
		}
	}
	if !params.Ephemeral {
		if err := r.createThreadRollout(record, record.CreatedAt); err != nil {
			return nil, err
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
		threadID, err := rollout.ThreadIDFromPath(*params.Path)
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
	if rootRecord, readErr := r.store.Read(session.ThreadID(params.ThreadID), true, false); readErr == nil && unmaterializedThread(rootRecord) {
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
	r.releaseRetainedWriters(archivedThreadIDs)
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
	path, err := rollout.FindThreadPath(codexHome, string(threadID), !archived)
	if err != nil {
		return false, false
	}
	if archived {
		_, err = rollout.Archive(path, codexHome)
		return true, err == nil
	}
	_, err = rollout.Unarchive(path, codexHome)
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
	for _, threadID := range session.DeleteOrderForSubtree(threadIDs) {
		if err := r.store.Delete(threadID); err != nil && !errors.Is(err, session.ErrThreadNotFound) {
			return nil, err
		}
		r.deleteThreadRollouts(threadID)
	}
	r.releaseRetainedWriters(threadIDs)
	return &ThreadDeleteResponse{}, nil
}

func threadDeleteError(threadID string, err error) error {
	if errors.Is(err, session.ErrThreadNotFound) {
		return jsonRPCInvalidRequest(fmt.Sprintf("thread not found: %s", strings.TrimSpace(threadID)))
	}
	return err
}

func (r *Router) deleteThreadRollouts(threadID session.ThreadID) {
	if r == nil || r.store == nil || threadID == "" {
		return
	}
	codexHome := codexHomeFromSessionStore(r.store)
	if codexHome == "" {
		return
	}
	for _, archived := range []bool{false, true} {
		path, err := rollout.FindThreadPath(codexHome, string(threadID), archived)
		if err != nil {
			continue
		}
		_ = rollout.Delete(path)
	}
}

func (r *Router) threadRolloutExists(threadID session.ThreadID, archived bool) bool {
	if r == nil || r.store == nil || threadID == "" {
		return false
	}
	codexHome := codexHomeFromSessionStore(r.store)
	if codexHome == "" {
		return false
	}
	_, err := rollout.FindThreadPath(codexHome, string(threadID), archived)
	return err == nil
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
	record, err := r.store.Read(session.ThreadID(params.ThreadID), true, true)
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
	if err := r.store.Save(record); err != nil {
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
	record, err := r.store.Read(session.ThreadID(params.ThreadID), true, true)
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
	if err := r.store.Save(record); err != nil {
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
	record, err := r.store.UpdateMetadata(threadID, patch, false)
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
		record, err = r.store.UpdateMetadata(threadID, patch, false)
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
	if unmaterializedThread(record) {
		if err := r.createThreadRollout(record, record.CreatedAt); err != nil {
			return nil, err
		}
	}
	return &ThreadSetNameResponse{}, nil
}

func (r *Router) markExplicitThreadName(record *session.Record) error {
	if r == nil || r.store == nil || record == nil {
		return nil
	}
	record.Metadata.Extra = ensureRecordExtra(record.Metadata.Extra)
	record.Metadata.Extra[explicitThreadNameExtraKey] = true
	return r.store.Save(record)
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
	record, err := r.store.Read(threadID, false, true)
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
	if err := r.store.Save(record); err != nil {
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
	if err := clearRustMemoriesSQLiteData(codexHome); err != nil {
		return nil, fmt.Errorf("failed to clear memory rows in memories db: %w", err)
	}
	if err := clearMemoryRootContents(codexHome); err != nil {
		return nil, fmt.Errorf("failed to clear memory directories under %s: %w", codexHome, err)
	}
	return &MemoryResetResponse{}, nil
}

func clearMemoryRootContents(codexHome string) error {
	codexHome = strings.TrimSpace(codexHome)
	if codexHome == "" {
		return nil
	}
	memoryRoot := filepath.Join(codexHome, "memories")
	entries, err := os.ReadDir(memoryRoot)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	for _, entry := range entries {
		if err := os.RemoveAll(filepath.Join(memoryRoot, entry.Name())); err != nil {
			return err
		}
	}
	return nil
}

func (r *Router) handleThreadCompactStart(request *Request) (*ThreadCompactStartResponse, error) {
	var params ThreadCompactStartParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	if err := params.Validate(); err != nil {
		return nil, err
	}
	record, err := r.store.Read(session.ThreadID(params.ThreadID), true, true)
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
	if err := r.store.Save(record); err != nil {
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
	current, err := r.store.Read(session.ThreadID(params.ThreadID), true, true)
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
	record, err := r.store.UpdateMetadata(session.ThreadID(params.ThreadID), &patch, true)
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

func (r *Router) handleThreadList(request *Request) (*ThreadListResponse, error) {
	var params ThreadListParams
	if err := request.DecodeParams(&params); err != nil {
		return nil, err
	}
	options, err := BuildListOptions(&params)
	if err != nil {
		return nil, err
	}
	records, err := r.store.AllRecords()
	if err != nil {
		return nil, err
	}
	records = materializedThreadRecords(records)
	var page *session.Page
	if params.UseStateDBOnly {
		page, err = session.ListRecords(records, options)
	} else {
		page, err = r.listRecordsIncludingRollouts(records, options, false)
	}
	if err != nil {
		return nil, err
	}
	return BuildListResponse(page, r.store, false)
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
	record, err := r.store.Read(session.ThreadID(params.ThreadID), true, true)
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
	record, err := r.store.Read(session.ThreadID(params.ThreadID), true, true)
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
	record, err := r.store.Read(session.ThreadID(strings.TrimSpace(params.ThreadID)), true, true)
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
	record, err := r.store.Read(session.ThreadID(params.ThreadID), true, true)
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
	if err := r.store.Save(record); err != nil {
		return nil, err
	}
	_ = r.appendThreadRollback(record.ID, params.NumTurns, record.UpdatedAt)
	path := r.threadRolloutPath(record)
	return &ThreadRollbackResponse{Thread: BuildThread(record, path, true)}, nil
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
		items = append(items, item)
	}
	record, err := r.store.AppendItems(session.ThreadID(params.ThreadID), items)
	if err != nil {
		if !errors.Is(err, session.ErrThreadNotFound) {
			return nil, err
		}
		if _, repairErr := r.repairThreadRecordFromRollout(session.ThreadID(params.ThreadID)); repairErr != nil {
			return nil, repairErr
		}
		record, err = r.store.AppendItems(session.ThreadID(params.ThreadID), items)
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
