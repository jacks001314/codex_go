package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"codex_go/appserver"
	"codex_go/session"
	codextea "codex_go/tui/tea"
)

const interactiveSideConnectionID = "local-tui-side"

type interactiveLocalSideCoordinator struct {
	mu            sync.Mutex
	store         *session.Store
	sideByParent  map[string]string
	parentBySide  map[string]string
	storageBySide map[string]codextea.SideChatStorage
}

func newInteractiveLocalSideCoordinator(store *session.Store) *interactiveLocalSideCoordinator {
	if store == nil {
		store = newSessionStore()
	}
	return &interactiveLocalSideCoordinator{
		store:         store,
		sideByParent:  map[string]string{},
		parentBySide:  map[string]string{},
		storageBySide: map[string]codextea.SideChatStorage{},
	}
}

func (c *interactiveLocalSideCoordinator) Start(params codextea.SideStartParams) (codextea.SideStartResponse, error) {
	if c == nil || c.store == nil {
		return codextea.SideStartResponse{}, errors.New("local side conversation store is unavailable")
	}
	parentThreadID := strings.TrimSpace(params.ParentThreadID)
	if parentThreadID == "" {
		return codextea.SideStartResponse{}, errors.New("local thread/fork requires a parent thread id")
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if previous := strings.TrimSpace(c.sideByParent[parentThreadID]); previous != "" && !c.storageBySide[previous].Attached() {
		if err := c.deleteSideLocked(previous); err != nil {
			return codextea.SideStartResponse{}, fmt.Errorf("close previous side conversation %s: %w", previous, err)
		}
		c.forgetSideLocked(previous)
	}

	existingInstructions := ""
	var parentWorkspaceRoots []string
	if parent, err := c.store.Read(session.ThreadID(parentThreadID), true, true); err == nil && parent != nil {
		existingInstructions = parent.Metadata.Instructions
		parentWorkspaceRoots = stringSliceFromAny(parent.Metadata.Extra["runtime_workspace_roots"])
	}
	configValues := map[string]any{}
	if effort := strings.TrimSpace(params.ReasoningEffort); effort != "" {
		configValues["model_reasoning_effort"] = effort
	}
	// Rust #60319 side_fork_config: a side conversation forks the parent config
	// with Daybreak disabled, regardless of the parent's saved preference.
	configValues["daybreak"] = false
	// Rust #46494 ForkConfigSource::Session: the side conversation forks the
	// active session, so forward its server-resolved runtime workspace roots
	// rather than letting the CWD override drop them. The TUI supplies the roots
	// it adopted from the server; otherwise the parent record's saved roots are
	// the authoritative selection.
	runtimeWorkspaceRoots := params.RuntimeWorkspaceRoots
	if len(runtimeWorkspaceRoots) == 0 {
		runtimeWorkspaceRoots = parentWorkspaceRoots
	}

	forkAndPrepare := func(storage codextea.SideChatStorage) (*appserver.ThreadForkResponse, error) {
		// Attached forks need the state runtime for their lineage filters, but a
		// temporary side conversation must keep working for paginated parents
		// that have no indexed rollout yet (Rust sidebar_fork).
		var router *appserver.Router
		var closeRouter func()
		if storage.Attached() {
			var stateErr error
			router, closeRouter, stateErr = openLocalSessionRouter(c.store)
			if stateErr != nil {
				return nil, fmt.Errorf("attached thread store unavailable: %w", stateErr)
			}
		} else {
			router = appserver.NewRouter(c.store)
			closeRouter = func() { _ = router.Close() }
		}
		defer closeRouter()

		developerInstructions := codextea.SideDeveloperInstructionsFor(existingInstructions, storage)
		forkParams := appserver.ThreadForkParams{
			ThreadID:              parentThreadID,
			HistoryMode:           session.ForkAll,
			ApprovalPolicy:        localSideOptionalString(params.ApprovalPolicy),
			DeveloperInstructions: &developerInstructions,
			Config:                configValues,
			Sandbox:               localSideOptionalString(params.Sandbox),
			AttachToSource:        storage.Attached(),
			ExcludeTurns:          storage.Attached(),
		}
		if cwd := strings.TrimSpace(params.CWD); cwd != "" {
			forkParams.CWD = &cwd
		}
		if model := strings.TrimSpace(params.Model); model != "" {
			forkParams.Model = &model
		}
		if serviceTier := strings.TrimSpace(params.ServiceTier); serviceTier != "" {
			forkParams.ServiceTier = &serviceTier
			forkParams.ServiceTierSet = true
		}
		if len(runtimeWorkspaceRoots) > 0 {
			forkParams.RuntimeWorkspaceRoots = append([]string(nil), runtimeWorkspaceRoots...)
		}
		// Rust #59767: the side conversation inherits the parent's named
		// permission profile. thread/fork rejects a profile combined with a
		// sandbox override, so the profile replaces the sandbox value.
		if profile := strings.TrimSpace(params.PermissionProfile); profile != "" {
			forkParams.Permissions = &profile
			forkParams.Sandbox = nil
		}
		source := appserver.ThreadSourceUser
		if storage.Attached() {
			source = appserver.ThreadSource(codextea.AttachedSideChatThreadSource)
		}
		forkParams.ThreadSource = &source

		result, err := localSideRequest(router, appserver.IntID(1), appserver.MethodThreadFork, forkParams)
		if err != nil {
			return nil, err
		}
		forked, ok := result.(*appserver.ThreadForkResponse)
		if !ok || forked == nil || forked.Thread == nil || strings.TrimSpace(forked.Thread.ID) == "" {
			return nil, errors.New("thread/fork response did not include a side thread id")
		}
		sideThreadID := strings.TrimSpace(forked.Thread.ID)
		inject := appserver.ThreadInjectItemsParams{
			ThreadID: sideThreadID,
			Items: []json.RawMessage{
				codextea.SideConversationPolicyItem(storage),
				codextea.SideBoundaryPromptItem(),
			},
		}
		if !storage.Attached() {
			inject.SessionItems = c.liveParentItems(parentThreadID, sideThreadID)
		}
		if _, err := localSideRequest(router, appserver.IntID(2), appserver.MethodThreadInjectItems, inject); err != nil {
			_, _ = localSideRequest(router, appserver.IntID(3), appserver.MethodThreadDelete, appserver.ThreadDeleteParams{ThreadID: sideThreadID})
			return nil, fmt.Errorf("prepare side conversation %s: %w", sideThreadID, err)
		}
		if storage.Attached() {
			record, err := c.store.Read(session.ThreadID(sideThreadID), true, true)
			if err != nil || record == nil {
				_, _ = localSideRequest(router, appserver.IntID(4), appserver.MethodThreadDelete, appserver.ThreadDeleteParams{ThreadID: sideThreadID})
				return nil, fmt.Errorf("prepare side conversation %s: Side conversation context was not saved", sideThreadID)
			}
			if _, ok := sideChatContextCheckpoint(record.Items); !ok {
				_, _ = localSideRequest(router, appserver.IntID(4), appserver.MethodThreadDelete, appserver.ThreadDeleteParams{ThreadID: sideThreadID})
				return nil, fmt.Errorf("prepare side conversation %s: Side conversation context was not saved", sideThreadID)
			}
		}
		return forked, nil
	}

	storage := params.Storage
	if !storage.Attached() {
		storage = codextea.SideChatStorageTemporary
	}
	forked, err := forkAndPrepare(storage)
	if err != nil && storage.Attached() && isAttachedSideChatUnsupportedError(err) {
		storage = codextea.SideChatStorageTemporary
		forked, err = forkAndPrepare(storage)
	}
	if err != nil {
		return codextea.SideStartResponse{}, err
	}
	sideThreadID := strings.TrimSpace(forked.Thread.ID)
	inheritedName := ""
	if forked.Thread.Name != nil {
		inheritedName = strings.TrimSpace(*forked.Thread.Name)
	}
	if record, readErr := c.store.Read(session.ThreadID(sideThreadID), true, true); readErr == nil && record != nil {
		extra := make(map[string]any, len(record.Metadata.Extra)+2)
		for key, value := range record.Metadata.Extra {
			extra[key] = value
		}
		extra["tui_side_conversation"] = true
		if storage.Attached() {
			delete(extra, "ephemeral")
		} else {
			extra["ephemeral"] = true
		}
		patch := &session.MetadataPatch{Extra: extra}
		if approvalPolicy := strings.TrimSpace(params.ApprovalPolicy); approvalPolicy != "" {
			patch.ApprovalPolicy = &approvalPolicy
		}
		if sandboxPolicy := strings.TrimSpace(params.Sandbox); sandboxPolicy != "" && strings.TrimSpace(params.PermissionProfile) == "" {
			patch.SandboxPolicy = &sandboxPolicy
		}
		if profile := strings.TrimSpace(params.PermissionProfile); profile != "" {
			patch.ActivePermissionProfile = &profile
		}
		if serviceTier := strings.TrimSpace(params.ServiceTier); serviceTier != "" {
			patch.ServiceTier = &serviceTier
		}
		if _, updateErr := c.store.UpdateMetadata(record.ID, patch, true); updateErr != nil {
			deleteLocalSideThread(c.store, sideThreadID)
			return codextea.SideStartResponse{}, fmt.Errorf("mark side conversation %s: %w", sideThreadID, updateErr)
		}
	}
	c.sideByParent[parentThreadID] = sideThreadID
	c.parentBySide[sideThreadID] = parentThreadID
	c.storageBySide[sideThreadID] = storage
	return codextea.SideStartResponse{
		ParentThreadID: parentThreadID,
		SideThreadID:   sideThreadID,
		Storage:        storage,
		InheritedName:  inheritedName,
	}, nil
}

// liveParentItems returns the running parent turn's completed items that the
// fork's inherited history does not already contain. Rust forks read the live
// thread store, so a side conversation started mid-turn sees the assistant and
// tool transcript the parent has produced before the boundary.
func (c *interactiveLocalSideCoordinator) liveParentItems(parentThreadID string, sideThreadID string) []session.Item {
	if c == nil || c.store == nil {
		return nil
	}
	live := c.store.LiveItems(session.ThreadID(parentThreadID))
	if live == nil {
		return nil
	}
	items := live.Snapshot()
	if len(items) == 0 {
		return nil
	}
	if record, err := c.store.Read(session.ThreadID(sideThreadID), true, true); err == nil && record != nil {
		items = session.MissingItems(record.Items, items)
	}
	return items
}

func (c *interactiveLocalSideCoordinator) Close(params codextea.SideCloseParams) (codextea.SideCloseResponse, error) {
	if c == nil || c.store == nil {
		return codextea.SideCloseResponse{}, errors.New("local side conversation store is unavailable")
	}
	sideThreadID := strings.TrimSpace(params.SideThreadID)
	if sideThreadID == "" {
		return codextea.SideCloseResponse{}, errors.New("local thread/delete requires a side thread id")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	storage := params.Storage
	if !storage.Attached() {
		storage = c.storageBySide[sideThreadID]
	}
	if storage.Attached() {
		if err := c.archiveSideLocked(sideThreadID); err != nil {
			return codextea.SideCloseResponse{}, err
		}
	} else if err := c.deleteSideLocked(sideThreadID); err != nil {
		return codextea.SideCloseResponse{}, err
	}
	c.forgetSideLocked(sideThreadID)
	return codextea.SideCloseResponse{}, nil
}

func (c *interactiveLocalSideCoordinator) CloseAll() error {
	if c == nil || c.store == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	sideThreadIDs := make([]string, 0, len(c.parentBySide))
	for sideThreadID := range c.parentBySide {
		sideThreadIDs = append(sideThreadIDs, sideThreadID)
	}
	var firstErr error
	for _, sideThreadID := range sideThreadIDs {
		if c.storageBySide[sideThreadID].Attached() {
			c.forgetSideLocked(sideThreadID)
			continue
		}
		if err := c.deleteSideLocked(sideThreadID); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		c.forgetSideLocked(sideThreadID)
	}
	return firstErr
}

// CloseRelated shuts down the side conversations whose parent belongs to the
// given thread subtree and reports whether they all closed. Conversations owned
// by unrelated threads stay open (Rust #58237 shutdown_side_threads root).
func (c *interactiveLocalSideCoordinator) CloseRelated(parentThreadIDs map[string]struct{}) bool {
	if c == nil || c.store == nil {
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	closed := true
	for sideThreadID, parentThreadID := range c.parentBySide {
		if _, related := parentThreadIDs[strings.TrimSpace(parentThreadID)]; !related {
			continue
		}
		if c.storageBySide[sideThreadID].Attached() {
			c.forgetSideLocked(sideThreadID)
			continue
		}
		if err := c.deleteSideLocked(sideThreadID); err != nil {
			closed = false
			continue
		}
		c.forgetSideLocked(sideThreadID)
	}
	return closed
}

func (c *interactiveLocalSideCoordinator) Instructions(threadID string) (string, bool) {
	if c == nil || c.store == nil {
		return "", false
	}
	threadID = strings.TrimSpace(threadID)
	c.mu.Lock()
	_, active := c.parentBySide[threadID]
	c.mu.Unlock()
	if !active {
		return "", false
	}
	record, err := c.store.Read(session.ThreadID(threadID), true, true)
	if err != nil || record == nil {
		return codextea.SideDeveloperInstructionText, true
	}
	instructions := strings.TrimSpace(record.Metadata.Instructions)
	if instructions == "" {
		instructions = codextea.SideDeveloperInstructionText
	}
	return instructions, true
}

func (c *interactiveLocalSideCoordinator) deleteSideLocked(sideThreadID string) error {
	router := appserver.NewRouter(c.store)
	defer router.Close()
	_, err := localSideRequest(router, appserver.IntID(5), appserver.MethodThreadDelete, appserver.ThreadDeleteParams{ThreadID: strings.TrimSpace(sideThreadID)})
	return err
}

func (c *interactiveLocalSideCoordinator) archiveSideLocked(sideThreadID string) error {
	router := appserver.NewRouter(c.store)
	defer router.Close()
	_, err := localSideRequest(router, appserver.IntID(6), appserver.MethodThreadArchive, appserver.ThreadArchiveParams{ThreadID: strings.TrimSpace(sideThreadID)})
	return err
}

func (c *interactiveLocalSideCoordinator) forgetSideLocked(sideThreadID string) {
	parentThreadID := c.parentBySide[sideThreadID]
	delete(c.parentBySide, sideThreadID)
	delete(c.storageBySide, sideThreadID)
	if c.sideByParent[parentThreadID] == sideThreadID {
		delete(c.sideByParent, parentThreadID)
	}
}

// sideChatContextCheckpoint returns when the saved child rollout recorded the
// developer policy followed by the side-chat boundary (Rust
// side_context_checkpoint). A saved side chat without that checkpoint must not
// appear in the sidebar.
func sideChatContextCheckpoint(items []session.Item) (time.Time, bool) {
	_, at, ok := sideChatContextCheckpointIndex(items)
	return at, ok
}

// sideChatContextCheckpointIndex locates the injected boundary in the child's
// own rollout. Items before it are inherited or bootstrap context and must not
// label the saved chat or seed its first prompt.
func sideChatContextCheckpointIndex(items []session.Item) (int, time.Time, bool) {
	seenPolicy := false
	for index, item := range items {
		text := strings.TrimSpace(item.Text)
		role := strings.ToLower(strings.TrimSpace(item.Role))
		if role == "developer" && sideConversationPolicyMatches(text) {
			seenPolicy = true
			continue
		}
		if seenPolicy && role == "user" && firstItemLine(text) == codextea.SideBoundaryTitle {
			return index, item.CreatedAt, true
		}
	}
	return -1, time.Time{}, false
}

func sideConversationPolicyMatches(text string) bool {
	trimmed := strings.TrimSpace(text)
	return strings.HasPrefix(strings.ToLower(trimmed), "<side_conversation>") &&
		strings.HasSuffix(strings.ToLower(trimmed), "</side_conversation>")
}

func firstItemLine(text string) string {
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		text = text[:index]
	}
	return strings.TrimSpace(text)
}

// isAttachedSideChatUnsupportedError reports the app-server refusals that make
// a sidebar fork fall back to a temporary side conversation (Rust
// sidebar_fork).
func isAttachedSideChatUnsupportedError(err error) bool {
	if err == nil {
		return false
	}
	message := err.Error()
	return strings.Contains(message, "attachToSource requires a paginated parent") ||
		strings.Contains(message, "attached threads is not supported yet") ||
		strings.Contains(message, "attached thread store")
}

func deleteLocalSideThread(store *session.Store, sideThreadID string) {
	router := appserver.NewRouter(store)
	defer router.Close()
	_, _ = localSideRequest(router, appserver.IntID(5), appserver.MethodThreadDelete, appserver.ThreadDeleteParams{ThreadID: strings.TrimSpace(sideThreadID)})
}

func localSideRequest(router *appserver.Router, id appserver.RequestID, method appserver.Method, params any) (any, error) {
	if router == nil {
		return nil, errors.New(string(method) + " failed in TUI: app-server is unavailable")
	}
	raw, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("%s failed in TUI: %w", method, err)
	}
	response := router.Handle(&appserver.Request{
		JSONRPC:        "2.0",
		ID:             id,
		Method:         method,
		Params:         raw,
		ConnectionID:   interactiveSideConnectionID,
		InternalParams: params,
	})
	if response == nil {
		return nil, errors.New(string(method) + " failed in TUI: no response")
	}
	if response.Error != nil {
		return nil, fmt.Errorf("%s failed in TUI: %s", method, strings.TrimSpace(response.Error.Message))
	}
	return response.Result, nil
}

func localSideOptionalString(value string) any {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return value
}
