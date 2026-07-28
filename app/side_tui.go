package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"codex_go/appserver"
	"codex_go/session"
	codextea "codex_go/tui/tea"
)

const interactiveSideConnectionID = "local-tui-side"

type interactiveLocalSideCoordinator struct {
	mu           sync.Mutex
	store        *session.Store
	sideByParent map[string]string
	parentBySide map[string]string
}

func newInteractiveLocalSideCoordinator(store *session.Store) *interactiveLocalSideCoordinator {
	if store == nil {
		store = newSessionStore()
	}
	return &interactiveLocalSideCoordinator{
		store:        store,
		sideByParent: map[string]string{},
		parentBySide: map[string]string{},
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
	if previous := strings.TrimSpace(c.sideByParent[parentThreadID]); previous != "" {
		if err := c.deleteSideLocked(previous); err != nil {
			return codextea.SideStartResponse{}, fmt.Errorf("close previous side conversation %s: %w", previous, err)
		}
		c.forgetSideLocked(previous)
	}

	existingInstructions := ""
	if parent, err := c.store.Read(session.ThreadID(parentThreadID), true, true); err == nil && parent != nil {
		existingInstructions = parent.Metadata.Instructions
	}
	developerInstructions := codextea.SideDeveloperInstructions(existingInstructions)
	configValues := map[string]any{}
	if effort := strings.TrimSpace(params.ReasoningEffort); effort != "" {
		configValues["model_reasoning_effort"] = effort
	}
	if personality := strings.TrimSpace(params.Personality); personality != "" {
		configValues["personality"] = personality
	}
	if len(configValues) == 0 {
		configValues = nil
	}
	forkParams := appserver.ThreadForkParams{
		ThreadID:              parentThreadID,
		HistoryMode:           session.ForkAll,
		ApprovalPolicy:        localSideOptionalString(params.ApprovalPolicy),
		DeveloperInstructions: &developerInstructions,
		Config:                configValues,
		Sandbox:               localSideOptionalString(params.Sandbox),
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
	source := appserver.ThreadSourceUser
	forkParams.ThreadSource = &source

	router := appserver.NewRouter(c.store)
	defer router.Close()
	result, err := localSideRequest(router, appserver.IntID(1), appserver.MethodThreadFork, forkParams)
	if err != nil {
		return codextea.SideStartResponse{}, err
	}
	forked, ok := result.(*appserver.ThreadForkResponse)
	if !ok || forked == nil || forked.Thread == nil || strings.TrimSpace(forked.Thread.ID) == "" {
		return codextea.SideStartResponse{}, errors.New("thread/fork response did not include a side thread id")
	}
	sideThreadID := strings.TrimSpace(forked.Thread.ID)
	_, err = localSideRequest(router, appserver.IntID(2), appserver.MethodThreadInjectItems, appserver.ThreadInjectItemsParams{
		ThreadID: sideThreadID,
		Items:    []json.RawMessage{codextea.SideBoundaryPromptItem()},
	})
	if err != nil {
		_, _ = localSideRequest(router, appserver.IntID(3), appserver.MethodThreadDelete, appserver.ThreadDeleteParams{ThreadID: sideThreadID})
		return codextea.SideStartResponse{}, fmt.Errorf("prepare side conversation %s: %w", sideThreadID, err)
	}
	if record, readErr := c.store.Read(session.ThreadID(sideThreadID), true, true); readErr == nil && record != nil {
		extra := make(map[string]any, len(record.Metadata.Extra)+2)
		for key, value := range record.Metadata.Extra {
			extra[key] = value
		}
		extra["ephemeral"] = true
		extra["tui_side_conversation"] = true
		patch := &session.MetadataPatch{Extra: extra}
		if approvalPolicy := strings.TrimSpace(params.ApprovalPolicy); approvalPolicy != "" {
			patch.ApprovalPolicy = &approvalPolicy
		}
		if sandboxPolicy := strings.TrimSpace(params.Sandbox); sandboxPolicy != "" {
			patch.SandboxPolicy = &sandboxPolicy
		}
		if serviceTier := strings.TrimSpace(params.ServiceTier); serviceTier != "" {
			patch.ServiceTier = &serviceTier
		}
		if _, updateErr := c.store.UpdateMetadata(record.ID, patch, true); updateErr != nil {
			_, _ = localSideRequest(router, appserver.IntID(4), appserver.MethodThreadDelete, appserver.ThreadDeleteParams{ThreadID: sideThreadID})
			return codextea.SideStartResponse{}, fmt.Errorf("mark side conversation %s ephemeral: %w", sideThreadID, updateErr)
		}
	}
	c.sideByParent[parentThreadID] = sideThreadID
	c.parentBySide[sideThreadID] = parentThreadID
	return codextea.SideStartResponse{ParentThreadID: parentThreadID, SideThreadID: sideThreadID}, nil
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
	if err := c.deleteSideLocked(sideThreadID); err != nil {
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

func (c *interactiveLocalSideCoordinator) forgetSideLocked(sideThreadID string) {
	parentThreadID := c.parentBySide[sideThreadID]
	delete(c.parentBySide, sideThreadID)
	if c.sideByParent[parentThreadID] == sideThreadID {
		delete(c.sideByParent, parentThreadID)
	}
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
		JSONRPC:      "2.0",
		ID:           id,
		Method:       method,
		Params:       raw,
		ConnectionID: interactiveSideConnectionID,
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
