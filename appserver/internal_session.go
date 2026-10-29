package appserver

import (
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"codex_go/config"
	"codex_go/session"
	"codex_go/turn"
)

// internalSessionMultiAgentVersionDisabled mirrors Rust's
// MultiAgentVersion::Disabled. Host-owned internal sessions never expose the
// collaboration tool surface, even when their parent runs multi-agent V2.
const internalSessionMultiAgentVersionDisabled = "disabled"

// guardianThreadContextKey stores GuardianThreadContext in a thread's
// host-owned extension data.
const guardianThreadContextKey = "guardian.thread_context"

// GuardianThreadContext is the thread-local Guardian state captured after the
// host registers a thread (Rust #39994). ParentModel is the thread's effective
// model after provider fallback.
type GuardianThreadContext struct {
	ParentThreadID string
	ParentModel    string
}

// ExtensionStore is a host-owned, concurrency-safe extension bucket. Rust
// supplies typed ExtensionData to thread lifecycle contributors; Go keeps the
// same lifetime scoping with string-keyed values.
type ExtensionStore struct {
	mu     sync.RWMutex
	values map[string]any
}

// Get returns the value stored under key.
func (s *ExtensionStore) Get(key string) (any, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	value, ok := s.values[strings.TrimSpace(key)]
	return value, ok
}

// Set stores value under key.
func (s *ExtensionStore) Set(key string, value any) {
	if s == nil {
		return
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = map[string]any{}
	}
	s.values[key] = value
}

// Delete removes key.
func (s *ExtensionStore) Delete(key string) {
	if s == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.values, strings.TrimSpace(key))
}

// ThreadReadyInput mirrors Rust's ThreadReadyInput: host configuration visible
// after thread registration, the session source, and the session/thread-scoped
// extension stores.
type ThreadReadyInput struct {
	ThreadID       string
	ParentThreadID string
	SessionSource  string
	Config         *config.Config
	SessionStore   *ExtensionStore
	ThreadStore    *ExtensionStore
}

// internalSessionHost owns host-side state for private internal sessions.
type internalSessionHost struct {
	threadIDs sync.Map

	mu            sync.RWMutex
	readyHandlers []func(ThreadReadyInput)
	sessionStores map[string]*ExtensionStore
	threadStores  map[string]*ExtensionStore
}

func (h *internalSessionHost) markThread(threadID string) {
	threadID = strings.TrimSpace(threadID)
	if h == nil || threadID == "" {
		return
	}
	h.threadIDs.Store(threadID, struct{}{})
}

func (h *internalSessionHost) unmarkThread(threadID string) {
	if h == nil {
		return
	}
	h.threadIDs.Delete(strings.TrimSpace(threadID))
}

func (h *internalSessionHost) isMarked(threadID string) bool {
	if h == nil {
		return false
	}
	_, ok := h.threadIDs.Load(strings.TrimSpace(threadID))
	return ok
}

func (h *internalSessionHost) addReadyHandler(handler func(ThreadReadyInput)) {
	if h == nil || handler == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	h.readyHandlers = append(h.readyHandlers, handler)
}

func (h *internalSessionHost) readyHandlersSnapshot() []func(ThreadReadyInput) {
	if h == nil {
		return nil
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	return append([]func(ThreadReadyInput){}, h.readyHandlers...)
}

func (h *internalSessionHost) sessionStore(sessionID string) *ExtensionStore {
	if h == nil {
		return nil
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.sessionStores == nil {
		h.sessionStores = map[string]*ExtensionStore{}
	}
	store := h.sessionStores[sessionID]
	if store == nil {
		store = &ExtensionStore{}
		h.sessionStores[sessionID] = store
	}
	return store
}

func (h *internalSessionHost) threadStore(threadID string) *ExtensionStore {
	if h == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.threadStores == nil {
		h.threadStores = map[string]*ExtensionStore{}
	}
	store := h.threadStores[threadID]
	if store == nil {
		store = &ExtensionStore{}
		h.threadStores[threadID] = store
	}
	return store
}

func (h *internalSessionHost) forgetThread(threadID string) {
	if h == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	h.mu.Lock()
	delete(h.threadStores, threadID)
	h.mu.Unlock()
	h.unmarkThread(threadID)
}

// InternalSessionOptions is the host-owned request shape for spawning a private
// internal session. History is only honored by ForkInternalSession; fresh
// spawns always start without the parent's history.
type InternalSessionOptions struct {
	Source                InternalSessionSource
	ThreadSource          ThreadSource
	CWD                   string
	Model                 string
	ModelProvider         string
	ApprovalPolicy        any
	ApprovalsReviewer     *string
	Config                map[string]any
	Sandbox               any
	Permissions           *string
	ServiceName           *string
	ServiceTier           *string
	ServiceTierSet        bool
	RuntimeWorkspaceRoots []string
	Environments          []map[string]any
	// History is only honored by ForkInternalSession. SpawnInternalSession
	// intentionally ignores it, mirroring Rust's spawn_internal_session, which
	// forces InitialHistory::New even when the caller supplies a fork.
	History []session.Item
}

// InternalSession is the host handle for a spawned internal session.
type InternalSession struct {
	ThreadID       string
	SessionID      string
	ParentThreadID string
	Source         InternalSessionSource
	Response       *ThreadStartResponse
}

// RegisterThreadReadyHandler installs a host-owned lifecycle contributor that
// runs after a thread is registered (Rust #39994 ThreadReadyInput).
func (r *RuntimeRouter) RegisterThreadReadyHandler(handler func(ThreadReadyInput)) {
	if r == nil {
		return
	}
	r.internalHost.addReadyHandler(handler)
}

// SpawnInternalSession starts a fresh host-owned internal session bound to
// parentThreadID. Like Rust's ThreadManager::spawn_internal_session it never
// inherits the parent's history, while parent lineage, session identity,
// originator, and shared session controls are preserved.
func (r *RuntimeRouter) SpawnInternalSession(parentThreadID string, options InternalSessionOptions) (*InternalSession, error) {
	return r.spawnInternalSession(parentThreadID, options, nil, false)
}

// ForkInternalSession starts an internal session from an explicitly selected,
// committed history (Rust ThreadManager::fork_internal_session).
func (r *RuntimeRouter) ForkInternalSession(parentThreadID string, options InternalSessionOptions) (*InternalSession, error) {
	return r.spawnInternalSession(parentThreadID, options, options.History, true)
}

func (r *RuntimeRouter) spawnInternalSession(parentThreadID string, options InternalSessionOptions, history []session.Item, forked bool) (*InternalSession, error) {
	if r == nil {
		return nil, errors.New("internal session runtime is unavailable")
	}
	parentThreadID = strings.TrimSpace(parentThreadID)
	if parentThreadID == "" {
		return nil, jsonRPCInvalidRequest("parent thread id is required")
	}
	source := normalizedInternalSessionSource(options.Source)
	if source == "" {
		return nil, jsonRPCInvalidRequest("internal sessions require an internal session source")
	}
	parent, err := r.threadRecord(session.ThreadID(parentThreadID), true, false)
	if err != nil || parent == nil || senderSessionSourceIsInternal(parent) {
		// Rust's get_thread hides internal sessions, so an internal parent is not
		// addressable either.
		return nil, jsonRPCInvalidRequest(fmt.Sprintf("thread not found: %s", parentThreadID))
	}
	threadSource := options.ThreadSource
	if strings.TrimSpace(string(threadSource)) == "" {
		threadSource = defaultInternalSessionThreadSource(source)
	}
	configOverrides := cloneAnyMap(options.Config)
	if configOverrides == nil {
		configOverrides = threadRecordConfigOverrides(parent)
	}
	cwd := firstNonEmpty(strings.TrimSpace(options.CWD), strings.TrimSpace(parent.Metadata.CWD))
	startParams := ThreadStartParams{
		CWD:                   cwd,
		Model:                 firstNonEmpty(strings.TrimSpace(options.Model), strings.TrimSpace(parent.Metadata.Model)),
		ModelProvider:         firstNonEmpty(strings.TrimSpace(options.ModelProvider), strings.TrimSpace(parent.Metadata.ModelProvider)),
		ApprovalPolicy:        firstNonNilInternalValue(options.ApprovalPolicy, internalSessionApprovalPolicy(parent.Metadata.ApprovalPolicy)),
		ApprovalsReviewer:     cloneString(options.ApprovalsReviewer),
		Config:                configOverrides,
		Sandbox:               options.Sandbox,
		Permissions:           cloneString(options.Permissions),
		ServiceName:           cloneString(options.ServiceName),
		ServiceTier:           cloneString(options.ServiceTier),
		ServiceTierSet:        options.ServiceTierSet,
		Ephemeral:             true,
		ThreadSource:          &threadSource,
		RuntimeWorkspaceRoots: append([]string(nil), options.RuntimeWorkspaceRoots...),
		Environments:          cloneInternalSessionEnvironments(options.Environments),
	}
	startRequest, err := internalRequest(MethodThreadStart, "internal-session-thread", &startParams)
	if err != nil {
		return nil, err
	}
	response, handled, err := r.handleEphemeralThreadStartRuntime(startRequest)
	if err != nil {
		return nil, err
	}
	if !handled || response == nil || response.Thread == nil {
		return nil, errors.New("failed to create internal session thread")
	}
	threadID := strings.TrimSpace(response.Thread.ID)
	if threadID == "" {
		return nil, errors.New("internal session thread has no id")
	}
	fail := func(err error) (*InternalSession, error) {
		_ = r.CloseInternalSession(threadID)
		return nil, err
	}
	r.internalHost.markThread(threadID)
	record, err := r.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil || record == nil {
		return fail(firstError(err, errors.New("internal session thread record is unavailable")))
	}
	record.SessionID = firstNonEmpty(strings.TrimSpace(parent.SessionID), parentThreadID)
	record.ParentThreadID = session.ThreadID(parentThreadID)
	record.ForkedFromID = ""
	record.Metadata.Source = internalSessionSourceDisplay(source)
	record.Metadata.ThreadSource = string(threadSource)
	record.Metadata.Originator = strings.TrimSpace(parent.Metadata.Originator)
	record.Metadata.MultiAgentVersion = internalSessionMultiAgentVersionDisabled
	record.Metadata.Extra = ensureRecordExtra(record.Metadata.Extra)
	record.Metadata.Extra["ephemeral"] = true
	if forked {
		record.Items = cloneRuntimeSessionItems(history)
	} else {
		record.Items = nil
	}
	if !r.saveEphemeralThreadRecord(record) {
		return fail(errors.New("failed to save internal session thread"))
	}
	r.applyThreadStartConfigSnapshot(response, startRequest)
	r.applyThreadStartInstructionSources(response, startRequest)
	r.requireThreadStatus().UpsertThread(threadID, false)
	if updated, readErr := r.threadRecord(session.ThreadID(threadID), true, false); readErr == nil && updated != nil {
		if thread := BuildThread(updated, "", false); thread != nil {
			thread.Status = IdleStatus()
			response.Thread = thread
		}
	}
	r.emitThreadReadyLifecycle(threadID)
	return &InternalSession{
		ThreadID:       threadID,
		SessionID:      firstNonEmpty(strings.TrimSpace(parent.SessionID), parentThreadID),
		ParentThreadID: parentThreadID,
		Source:         InternalSessionSource(source),
		Response:       response,
	}, nil
}

// CloseInternalSession shuts down and forgets a host-owned internal session.
func (r *RuntimeRouter) CloseInternalSession(threadID string) error {
	if r == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	if active := r.threads.ActiveTurn(threadID); active != nil {
		if active.Cancel != nil {
			active.Cancel()
		}
		if turnID := strings.TrimSpace(active.TurnID); turnID != "" {
			_, _ = r.requireTurns().Interrupt(&turn.TurnInterruptParams{ThreadID: threadID, TurnID: turnID})
		}
		deadline := time.Now().Add(10 * time.Second)
		for r.threads.ActiveTurn(threadID) != nil && time.Now().Before(deadline) {
			time.Sleep(25 * time.Millisecond)
		}
		if r.threads.ActiveTurn(threadID) != nil {
			return fmt.Errorf("internal session %s shutdown timed out", threadID)
		}
	}
	r.threads.DeleteEphemeralRecord(session.ThreadID(threadID))
	r.markThreadUnloaded(threadID)
	r.internalHost.forgetThread(threadID)
	if r.mcpRuntimes != nil {
		r.mcpRuntimes.invalidateThread(threadID)
	}
	return nil
}

// GuardianThreadContext returns the context recorded for a registered thread.
func (r *RuntimeRouter) GuardianThreadContext(threadID string) (GuardianThreadContext, bool) {
	if r == nil {
		return GuardianThreadContext{}, false
	}
	store := r.internalHost.threadStore(strings.TrimSpace(threadID))
	value, ok := store.Get(guardianThreadContextKey)
	if !ok {
		return GuardianThreadContext{}, false
	}
	context, ok := value.(GuardianThreadContext)
	return context, ok
}

// isInternalSessionThread reports whether threadID belongs to a host-owned
// internal session. Internal sessions stay addressable by host code through
// threadRecord but are hidden from public thread lookups, matching Rust's
// ThreadManager::list_thread_ids/get_thread filters.
func (r *RuntimeRouter) isInternalSessionThread(threadID string) bool {
	if r == nil {
		return false
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}
	if r.internalSessionThreadHidden(threadID) {
		return true
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	return err == nil && record != nil && senderSessionSourceIsInternal(record)
}

// internalSessionThreadHidden reports whether a public app-server request must
// treat threadID as absent. Live host-owned sessions are tracked in the
// internal registry and are always ephemeral, so this avoids a thread-store
// read on every public thread-scoped request.
func (r *RuntimeRouter) internalSessionThreadHidden(threadID string) bool {
	if r == nil {
		return false
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}
	if r.internalSessionThreadRegistered(threadID) {
		return true
	}
	record, ok := r.ephemeralThreadRecord(session.ThreadID(threadID), false)
	return ok && senderSessionSourceIsInternal(record)
}

// internalSessionThreadRegistered reports whether a live host-owned internal
// session is registered in this runtime.
func (r *RuntimeRouter) internalSessionThreadRegistered(threadID string) bool {
	if r == nil {
		return false
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return false
	}
	if _, ok := r.internalMemoryThreads.Load(threadID); ok {
		return true
	}
	return r.internalHost.isMarked(threadID)
}

// emitThreadReadyLifecycle runs the host-owned contributors after a thread has
// been registered. Non-internal threads get the Guardian thread context that
// future reviewer sessions use for parent lineage and the effective model.
func (r *RuntimeRouter) emitThreadReadyLifecycle(threadID string) {
	if r == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil {
		return
	}
	cfg := r.threadReadyConfig(record)
	sessionID := firstNonEmpty(strings.TrimSpace(record.SessionID), threadID)
	input := ThreadReadyInput{
		ThreadID:       threadID,
		ParentThreadID: strings.TrimSpace(string(record.ParentThreadID)),
		SessionSource:  strings.TrimSpace(record.Metadata.Source),
		Config:         cfg,
		SessionStore:   r.internalHost.sessionStore(sessionID),
		ThreadStore:    r.internalHost.threadStore(threadID),
	}
	if senderSessionSourceIsInternal(record) {
		r.internalHost.markThread(threadID)
	} else if model := threadReadyModel(record, cfg); model != "" {
		input.ThreadStore.Set(guardianThreadContextKey, GuardianThreadContext{
			ParentThreadID: threadID,
			ParentModel:    model,
		})
	}
	for _, handler := range r.internalHost.readyHandlersSnapshot() {
		handler(input)
	}
}

func (r *RuntimeRouter) threadReadyConfig(record *session.Record) *config.Config {
	cfg := &config.Config{Values: map[string]any{}}
	if record == nil {
		return cfg
	}
	if r != nil && r.services.Config != nil {
		if read, err := r.services.Config.Read(&config.ConfigReadParams{CWD: stringPtrIfNotEmpty(record.Metadata.CWD)}); err == nil && read != nil && read.Config != nil {
			cfg.Values = read.Config
		}
	}
	applyRuntimeConfigOverrides(cfg, threadRecordConfigOverrides(record))
	return cfg
}

func threadReadyModel(record *session.Record, cfg *config.Config) string {
	if record != nil {
		if modelID := strings.TrimSpace(record.Metadata.Model); modelID != "" {
			return modelID
		}
	}
	if cfg != nil {
		return stringConfigValue(cfg, "model")
	}
	return ""
}

func normalizedInternalSessionSource(source InternalSessionSource) string {
	value := strings.ToLower(strings.TrimSpace(string(source)))
	value = strings.NewReplacer("-", "_", ":", "_", "/", "_", ".", "_").Replace(value)
	value = strings.TrimPrefix(value, "internal_")
	switch value {
	case "guardian":
		return "guardian"
	case "memory_consolidation", "memoryconsolidation":
		return "memory_consolidation"
	default:
		return ""
	}
}

func internalSessionSourceDisplay(source string) string {
	return "internal_" + normalizedInternalSessionSource(InternalSessionSource(source))
}

func defaultInternalSessionThreadSource(source string) ThreadSource {
	switch source {
	case "guardian":
		return ThreadSourceGuardianReview
	case "memory_consolidation":
		return ThreadSourceMemoryConsolidation
	default:
		return ""
	}
}

func internalSessionApprovalPolicy(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}

func firstNonNilInternalValue(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func cloneInternalSessionEnvironments(environments []map[string]any) []map[string]any {
	if len(environments) == 0 {
		return nil
	}
	cloned := make([]map[string]any, 0, len(environments))
	for _, environment := range environments {
		cloned = append(cloned, cloneAnyMap(environment))
	}
	return cloned
}
