package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"codex_go/internal/agent"
	"codex_go/internal/session"
	"codex_go/internal/turn"
)

type runtimeAgentController struct {
	router     *RuntimeRouter
	parentID   string
	cwd        string
	maxThreads int
}

func newRuntimeAgentController(router *RuntimeRouter, parentID string, cwd string, maxThreads int) agent.ToolController {
	return &runtimeAgentController{router: router, parentID: strings.TrimSpace(parentID), cwd: strings.TrimSpace(cwd), maxThreads: maxThreads}
}

func (c *runtimeAgentController) SpawnAgent(ctx context.Context, args *agent.SpawnAgentArgs) (*agent.SpawnAgentResult, error) {
	if c == nil || c.router == nil || c.router.services.ThreadRouter == nil || c.router.services.ThreadRouter.store == nil {
		return nil, fmt.Errorf("agent runtime is unavailable")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if args == nil {
		args = &agent.SpawnAgentArgs{}
	}
	reservation, err := c.router.agentRegistry.ReserveSpawnSlot(c.maxThreads)
	if err != nil {
		return nil, err
	}
	committed := false
	defer func() {
		if !committed {
			reservation.Cancel()
		}
	}()
	nickname, err := reservation.ReserveAgentNickname(args.NicknameCandidates, "")
	if err != nil && len(args.NicknameCandidates) > 0 {
		return nil, err
	}
	threadID := newThreadID()
	now := time.Now().UTC()
	modelID := agentStringValue(args.Model)
	providerID := ""
	if parent, readErr := c.router.threadRecord(session.ThreadID(c.parentID), false, false); readErr == nil && parent != nil {
		if modelID == "" {
			modelID = parent.Metadata.Model
		}
		providerID = parent.Metadata.ModelProvider
	}
	record := &session.Record{
		ID: threadID, SessionID: string(threadID), ParentThreadID: session.ThreadID(c.parentID),
		CreatedAt: now, UpdatedAt: now, RecencyAt: now,
		Metadata: session.Metadata{
			CWD: c.cwd, Model: modelID, ModelProvider: providerID,
			Source: string(SessionSourceAppServer), ThreadSource: "subAgentThreadSpawn",
			Originator: "subagent", AgentNickname: nickname, AgentRole: args.ResolvedRole,
			MultiAgentVersion: string(agent.VersionV1), SessionPrefix: session.PrefixForSessionID(string(threadID)),
		},
	}
	if err := c.router.services.ThreadRouter.store.Create(record); err != nil {
		return nil, err
	}
	if c.router.services.SpawnGraph != nil {
		if err := c.router.services.SpawnGraph.UpsertThreadSpawnEdge(c.parentID, string(threadID), agent.ThreadSpawnEdgeOpen); err != nil {
			_ = c.router.services.ThreadRouter.store.Delete(threadID)
			return nil, err
		}
	}
	reservation.Commit(agent.Metadata{ThreadID: string(threadID), Nickname: nickname, Role: args.ResolvedRole})
	committed = true
	c.router.notify(NotificationThreadStarted, &ThreadStartedNotification{Thread: threadStartedNotificationThread(BuildThread(record, "", true))})
	prompt := agentStringValue(args.Message)
	if prompt != "" || len(args.Items) > 0 {
		params := &turn.TurnStartParams{ThreadID: string(threadID), Prompt: prompt, CWD: c.cwd, Model: modelID}
		if args.ReasoningEffort != nil {
			effort := strings.TrimSpace(*args.ReasoningEffort)
			params.Effort = &effort
		}
		if args.ServiceTier != nil {
			params.ServiceTier = args.ServiceTier
			params.ServiceTierSet = true
		}
		if _, err := c.router.handleTurnStart(requestWithInternalParams(MethodTurnStart, params)); err != nil {
			c.router.agentRegistry.ReleaseSpawnedThread(string(threadID))
			_ = c.router.services.ThreadRouter.store.Delete(threadID)
			return nil, err
		}
	}
	return &agent.SpawnAgentResult{AgentID: string(threadID), Nickname: stringPtrIfNotEmpty(nickname)}, nil
}

func (c *runtimeAgentController) SendInput(ctx context.Context, args *agent.SendInputArgs) (*agent.SendInputResult, error) {
	if args == nil || strings.TrimSpace(args.Target) == "" {
		return nil, fmt.Errorf("target is required")
	}
	target := strings.TrimSpace(args.Target)
	if _, err := c.router.threadRecord(session.ThreadID(target), true, false); err != nil {
		if errors.Is(err, session.ErrThreadNotFound) {
			return &agent.SendInputResult{SubmissionID: ""}, nil
		}
		return nil, err
	}
	if active := c.router.activeRuntimeTurnSnapshot(target); active != nil {
		if !args.Interrupt {
			return nil, fmt.Errorf("agent %s already has an active turn", target)
		}
		if _, err := c.router.handleTurnInterrupt(requestWithInternalParams(MethodTurnInterrupt, turn.TurnInterruptParams{ThreadID: target, TurnID: active.ID})); err != nil {
			return nil, err
		}
	}
	prompt := agentStringValue(args.Message)
	if prompt == "" && len(args.Items) == 0 {
		return nil, fmt.Errorf("message or items is required")
	}
	response, err := c.router.handleTurnStart(requestWithInternalParams(MethodTurnStart, turn.TurnStartParams{ThreadID: target, Prompt: prompt}))
	if err != nil {
		return nil, err
	}
	return &agent.SendInputResult{SubmissionID: response.Turn.ID}, nil
}
func (c *runtimeAgentController) WaitAgent(ctx context.Context, args *agent.WaitAgentArgs) (*agent.WaitAgentResult, error) {
	if args == nil {
		args = &agent.WaitAgentArgs{}
	}
	if args.TimeoutMS != nil && *args.TimeoutMS > 0 {
		timer := time.NewTimer(time.Duration(*args.TimeoutMS) * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	targets := append([]string(nil), args.Targets...)
	if len(targets) == 0 {
		for _, metadata := range c.router.agentRegistry.LiveAgents() {
			targets = append(targets, metadata.ThreadID)
		}
	}
	statuses := make(map[string]agent.AgentMessageStatus, len(targets))
	for _, target := range targets {
		target = strings.TrimSpace(target)
		statuses[target] = c.status(target)
	}
	return &agent.WaitAgentResult{Status: statuses, TimedOut: false}, nil
}
func (c *runtimeAgentController) ResumeAgent(ctx context.Context, args *agent.ResumeAgentArgs) (*agent.ResumeAgentResult, error) {
	if args == nil || strings.TrimSpace(args.ID) == "" {
		return nil, fmt.Errorf("id is required")
	}
	id := strings.TrimSpace(args.ID)
	record, err := c.router.threadRecord(session.ThreadID(id), true, false)
	if errors.Is(err, session.ErrThreadNotFound) {
		return &agent.ResumeAgentResult{Status: agent.AgentMessageStatus{Kind: agent.AgentMessageStatusNotFound}}, nil
	}
	if err != nil {
		return nil, err
	}
	if record.Archived {
		if _, err := c.router.services.ThreadRouter.store.Unarchive(record.ID); err != nil {
			return nil, err
		}
	}
	c.router.agentRegistry.RegisterSpawnedThread(agent.Metadata{ThreadID: id, Nickname: record.Metadata.AgentNickname, Role: record.Metadata.AgentRole})
	if c.router.services.SpawnGraph != nil {
		_ = c.router.services.SpawnGraph.UpsertThreadSpawnEdge(string(record.ParentThreadID), id, agent.ThreadSpawnEdgeOpen)
	}
	return &agent.ResumeAgentResult{Status: c.status(id)}, nil
}
func (c *runtimeAgentController) CloseAgent(ctx context.Context, args *agent.CloseAgentArgs) (*agent.CloseAgentResult, error) {
	if args == nil || strings.TrimSpace(args.Target) == "" {
		return nil, fmt.Errorf("target is required")
	}
	target := strings.TrimSpace(args.Target)
	previous := c.status(target)
	if active := c.router.activeRuntimeTurnSnapshot(target); active != nil {
		_, _ = c.router.handleTurnInterrupt(requestWithInternalParams(MethodTurnInterrupt, turn.TurnInterruptParams{ThreadID: target, TurnID: active.ID}))
	}
	if previous.Kind != agent.AgentMessageStatusNotFound {
		c.router.agentRegistry.ReleaseSpawnedThread(target)
		if c.router.services.SpawnGraph != nil {
			_ = c.router.services.SpawnGraph.SetThreadSpawnEdgeStatus(target, agent.ThreadSpawnEdgeClosed)
		}
	}
	return &agent.CloseAgentResult{PreviousStatus: previous}, nil
}

func (c *runtimeAgentController) status(threadID string) agent.AgentMessageStatus {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return agent.AgentMessageStatus{Kind: agent.AgentMessageStatusNotFound}
	}
	if c.router.activeRuntimeTurnSnapshot(threadID) != nil {
		return agent.AgentMessageStatus{Kind: agent.AgentMessageStatusRunning}
	}
	record, err := c.router.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil {
		return agent.AgentMessageStatus{Kind: agent.AgentMessageStatusNotFound}
	}
	message := lastAgentMessage(record.Items)
	if len(record.Metadata.RolloutTurns) == 0 {
		return agent.AgentMessageStatus{Kind: agent.AgentMessageStatusPendingInit, Message: message}
	}
	last := record.Metadata.RolloutTurns[len(record.Metadata.RolloutTurns)-1]
	switch strings.ToLower(strings.TrimSpace(last.Status)) {
	case "failed", "errored":
		return agent.AgentMessageStatus{Kind: agent.AgentMessageStatusErrored, Message: firstNonEmpty(last.ErrorMessage, message)}
	case "interrupted", "aborted":
		return agent.AgentMessageStatus{Kind: agent.AgentMessageStatusInterrupted, Message: message}
	case "inprogress", "in_progress", "running":
		return agent.AgentMessageStatus{Kind: agent.AgentMessageStatusRunning, Message: message}
	default:
		return agent.AgentMessageStatus{Kind: agent.AgentMessageStatusCompleted, Message: message}
	}
}

func lastAgentMessage(items []session.Item) string {
	for i := len(items) - 1; i >= 0; i-- {
		if strings.EqualFold(items[i].Role, "assistant") && strings.TrimSpace(items[i].Text) != "" {
			return strings.TrimSpace(items[i].Text)
		}
	}
	return ""
}

func requestWithInternalParams(method Method, params any) *Request {
	data, _ := json.Marshal(params)
	return &Request{JSONRPC: "2.0", ID: StringID("internal-agent-" + string(newThreadID())), Method: method, Params: data}
}

func agentStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

var _ agent.ToolController = (*runtimeAgentController)(nil)
