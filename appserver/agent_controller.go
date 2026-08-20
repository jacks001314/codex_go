package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"codex_go/agent"
	"codex_go/config"
	"codex_go/session"
	"codex_go/turn"
)

type runtimeAgentController struct {
	router       *RuntimeRouter
	parentID     string
	parentTurnID string
	rootTurnID   string
	rootID       string
	scopePath    string
	cwd          string
	maxThreads   int
	depth        int
	maxDepth     int
	version      agent.MultiAgentVersion
	environments []map[string]any
	registry     *agent.Registry
}

func newRuntimeAgentController(router *RuntimeRouter, parentID string, cwd string, maxThreads int) agent.ToolController {
	return newRuntimeAgentControllerWithVersion(router, parentID, cwd, maxThreads, agent.VersionV1)
}

func newRuntimeAgentControllerWithVersion(router *RuntimeRouter, parentID string, cwd string, maxThreads int, version agent.MultiAgentVersion) agent.ToolController {
	return newRuntimeAgentControllerWithEnvironmentSelections(router, parentID, cwd, maxThreads, version, nil)
}

func newRuntimeAgentControllerWithEnvironmentSelections(router *RuntimeRouter, parentID string, cwd string, maxThreads int, version agent.MultiAgentVersion, environments []map[string]any) agent.ToolController {
	return newRuntimeAgentControllerForTurn(router, parentID, "", "", cwd, maxThreads, version, environments)
}

func newRuntimeAgentControllerForTurn(router *RuntimeRouter, parentID string, parentTurnID string, rootTurnID string, cwd string, maxThreads int, version agent.MultiAgentVersion, environments []map[string]any) agent.ToolController {
	registry := (*agent.Registry)(nil)
	rootID := strings.TrimSpace(parentID)
	scopePath := "/root"
	depth := 0
	if router != nil {
		rootID, scopePath = router.runtimeAgentIdentity(parentID)
		registry = router.runtimeAgentRegistry(rootID)
		if record, recordErr := router.threadRecord(session.ThreadID(strings.TrimSpace(parentID)), true, false); recordErr == nil && record != nil {
			depth = record.Metadata.AgentDepth
		}
	}
	return &runtimeAgentController{
		router:       router,
		parentID:     strings.TrimSpace(parentID),
		parentTurnID: strings.TrimSpace(parentTurnID),
		rootTurnID:   strings.TrimSpace(rootTurnID),
		rootID:       rootID,
		scopePath:    scopePath,
		cwd:          strings.TrimSpace(cwd),
		maxThreads:   maxThreads,
		depth:        depth,
		maxDepth:     config.DefaultAgentMaxDepth,
		version:      version,
		environments: cloneMapSlice(environments),
		registry:     registry,
	}
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
	if c.version == agent.VersionV1 && c.maxDepth >= 0 && c.depth+1 > c.maxDepth {
		return nil, agent.ErrAgentDepthLimitReached
	}
	registry := c.registry
	if registry == nil {
		registry = c.router.runtimeAgentRegistry(c.rootID)
	}
	reservation, err := registry.ReserveSpawnSlot(c.maxThreads)
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
	agentPath := ""
	if c.version == agent.VersionV2 {
		taskName := strings.TrimSpace(args.TaskName)
		if taskName == "" {
			taskName = "agent_" + strings.ToLower(safeIdentifier(string(newThreadID())))
		}
		agentPath = runtimeCanonicalAgentPath(c.scopePath, taskName)
		if err := reservation.ReserveAgentPath(agent.AgentPath(agentPath)); err != nil {
			return nil, err
		}
	}
	threadID := newThreadID()
	now := time.Now().UTC()
	modelID := agentStringValue(args.Model)
	providerID := ""
	developerInstructions := ""
	if parent, readErr := c.router.threadRecord(session.ThreadID(c.parentID), false, false); readErr == nil && parent != nil {
		if modelID == "" {
			modelID = parent.Metadata.Model
		}
		providerID = parent.Metadata.ModelProvider
		developerInstructions = parent.Metadata.Instructions
	}
	if args.DeveloperInstructions != nil {
		developerInstructions = *args.DeveloperInstructions
	}
	extra := map[string]any{}
	if len(c.environments) > 0 {
		extra[runtimeEnvironmentSelectionsExtraKey] = cloneMapSlice(c.environments)
	}
	var record *session.Record
	forkTurns := runtimeForkTurns(args.ForkTurns)
	if c.version == agent.VersionV2 && forkTurns != "none" {
		parent, readErr := c.router.threadRecord(session.ThreadID(c.parentID), true, true)
		if readErr != nil || parent == nil {
			return nil, firstNonNilError(readErr, fmt.Errorf("parent thread %s is unavailable", c.parentID))
		}
		forkOptions := session.ForkOptions{NewID: threadID, ParentThreadID: session.ThreadID(c.parentID), Now: now, Mode: session.ForkAll}
		if forkTurns != "all" {
			count, parseErr := strconv.Atoi(forkTurns)
			if parseErr != nil || count <= 0 {
				return nil, fmt.Errorf("fork_turns must be `none`, `all`, or a positive integer string")
			}
			forkOptions.Mode = session.ForkLastN
			forkOptions.LastN = count
		}
		forked, forkErr := c.router.services.ThreadRouter.store.ForkRecord(parent, forkOptions)
		if forkErr != nil {
			return nil, forkErr
		}
		record = forked
		if forkOptions.Mode == session.ForkAll {
			record.Items = filterInheritedCurrentTimeReminders(record.Items)
		}
	} else {
		record = &session.Record{ID: threadID, SessionID: string(threadID), ParentThreadID: session.ThreadID(c.parentID), CreatedAt: now, UpdatedAt: now, RecencyAt: now}
		if err := c.router.services.ThreadRouter.store.Create(record); err != nil {
			return nil, err
		}
	}
	record.Metadata.CWD = c.cwd
	record.Metadata.Model = modelID
	record.Metadata.ModelProvider = providerID
	record.Metadata.Source = string(SessionSourceAppServer)
	record.Metadata.ThreadSource = "subAgentThreadSpawn"
	record.Metadata.Originator = "subagent"
	record.Metadata.AgentNickname = nickname
	record.Metadata.AgentRole = args.ResolvedRole
	record.Metadata.AgentPath = agentPath
	record.Metadata.AgentDepth = c.depth + 1
	record.Metadata.Instructions = developerInstructions
	record.Metadata.MultiAgentVersion = string(c.version)
	record.Metadata.SessionPrefix = session.PrefixForSessionID(string(threadID))
	record.Metadata.Extra = extra
	if err := c.router.runtimeSaveThreadRecord(record); err != nil {
		_ = c.router.services.ThreadRouter.store.Delete(threadID)
		return nil, err
	}
	if c.router.services.SpawnGraph != nil {
		if err := c.router.services.SpawnGraph.UpsertThreadSpawnEdge(c.parentID, string(threadID), agent.ThreadSpawnEdgeOpen); err != nil {
			_ = c.router.services.ThreadRouter.store.Delete(threadID)
			return nil, err
		}
	}
	reservation.Commit(agent.Metadata{ThreadID: string(threadID), Path: agent.AgentPath(agentPath), Nickname: nickname, Role: args.ResolvedRole})
	if c.router.agentRegistry != registry {
		c.router.agentRegistry.RegisterSpawnedThread(agent.Metadata{ThreadID: string(threadID), Path: agent.AgentPath(agentPath), Nickname: nickname, Role: args.ResolvedRole})
	}
	committed = true
	c.router.notify(NotificationThreadStarted, &ThreadStartedNotification{Thread: threadStartedNotificationThread(BuildThread(record, "", true))})
	prompt := agentStringValue(args.Message)
	if prompt != "" || len(args.Items) > 0 {
		params := &turn.TurnStartParams{ThreadID: string(threadID), CWD: c.cwd, Model: modelID, Environments: cloneMapSlice(c.environments), ParentTurnID: c.parentTurnID, RootTurnID: c.rootTurnID}
		if c.version == agent.VersionV2 {
			params.AdditionalInputItems = append(params.AdditionalInputItems, runtimeAgentCommunicationInputItem(c.scopePath, agentPath, prompt, true, args.Plaintext))
			params.AdditionalInputItems = append(params.AdditionalInputItems, args.Items...)
		} else {
			params.Prompt = prompt
			params.AdditionalInputItems = append(params.AdditionalInputItems, args.Items...)
		}
		if args.DeveloperInstructions != nil {
			value := *args.DeveloperInstructions
			params.DeveloperInstructions = &value
		}
		if args.ReasoningEffort != nil {
			effort := strings.TrimSpace(*args.ReasoningEffort)
			params.Effort = &effort
		}
		if args.ServiceTier != nil {
			params.ServiceTier = args.ServiceTier
			params.ServiceTierSet = true
		}
		if _, err := c.router.handleTurnStart(requestWithInternalParams(MethodTurnStart, params)); err != nil {
			registry.ReleaseSpawnedThread(string(threadID))
			_ = c.router.services.ThreadRouter.store.Delete(threadID)
			return nil, err
		}
	}
	return &agent.SpawnAgentResult{AgentID: string(threadID), TaskName: agentPath, Nickname: stringPtrIfNotEmpty(nickname)}, nil
}

func (c *runtimeAgentController) SendInput(ctx context.Context, args *agent.SendInputArgs) (*agent.SendInputResult, error) {
	if args == nil || strings.TrimSpace(args.Target) == "" {
		return nil, fmt.Errorf("target is required")
	}
	target, _, err := c.resolveTarget(args.Target)
	if err != nil {
		return &agent.SendInputResult{SubmissionID: ""}, nil
	}
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
	response, err := c.router.handleTurnStart(requestWithInternalParams(MethodTurnStart, turn.TurnStartParams{ThreadID: target, Prompt: prompt, AdditionalInputItems: append([]any(nil), args.Items...), ParentTurnID: c.parentTurnID}))
	if err != nil {
		return nil, err
	}
	return &agent.SendInputResult{SubmissionID: response.Turn.ID}, nil
}
func (c *runtimeAgentController) WaitAgent(ctx context.Context, args *agent.WaitAgentArgs) (*agent.WaitAgentResult, error) {
	// Rust requires explicit targets for V1 wait_agent
	// (parse_agent_id_targets rejects empty lists) and returns only final
	// statuses; an empty result with timed_out=true means no target finished
	// before the deadline.
	if args == nil || len(args.Targets) == 0 {
		return nil, fmt.Errorf("agent ids must be non-empty")
	}
	timeout := agent.MultiAgentV1DefaultWait
	if args.TimeoutMS != nil {
		timeout = time.Duration(*args.TimeoutMS) * time.Millisecond
		if timeout <= 0 {
			return nil, fmt.Errorf("timeout_ms must be greater than zero")
		}
		if timeout < agent.MultiAgentV1MinWait {
			timeout = agent.MultiAgentV1MinWait
		}
		if timeout > agent.MultiAgentV1MaxWait {
			timeout = agent.MultiAgentV1MaxWait
		}
	}
	collectFinal := func(final map[string]agent.AgentMessageStatus) *agent.WaitAgentResult {
		if len(final) == 0 {
			return nil
		}
		return &agent.WaitAgentResult{Status: final, TimedOut: false}
	}
	final := map[string]agent.AgentMessageStatus{}
	for _, target := range args.Targets {
		target = strings.TrimSpace(target)
		status := c.status(target)
		if status.IsFinal() {
			final[target] = status
		}
	}
	if result := collectFinal(final); result != nil {
		return result, nil
	}
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		delay := time.Until(deadline)
		if delay > 200*time.Millisecond {
			delay = 200 * time.Millisecond
		}
		if delay <= 0 {
			break
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		for _, target := range args.Targets {
			target = strings.TrimSpace(target)
			status := c.status(target)
			if status.IsFinal() {
				final[target] = status
			}
		}
		if result := collectFinal(final); result != nil {
			return result, nil
		}
	}
	return &agent.WaitAgentResult{Status: map[string]agent.AgentMessageStatus{}, TimedOut: true}, nil
}
func (c *runtimeAgentController) ResumeAgent(ctx context.Context, args *agent.ResumeAgentArgs) (*agent.ResumeAgentResult, error) {
	if args == nil || strings.TrimSpace(args.ID) == "" {
		return nil, fmt.Errorf("id is required")
	}
	if c.version == agent.VersionV1 && c.maxDepth >= 0 && c.depth+1 > c.maxDepth {
		return nil, agent.ErrAgentDepthLimitReached
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
	registry := c.registry
	if registry == nil {
		registry = c.router.runtimeAgentRegistry(c.rootID)
	}
	registry.RegisterSpawnedThread(agent.Metadata{ThreadID: id, Path: agent.AgentPath(record.Metadata.AgentPath), Nickname: record.Metadata.AgentNickname, Role: record.Metadata.AgentRole})
	if c.router.agentRegistry != registry {
		c.router.agentRegistry.RegisterSpawnedThread(agent.Metadata{ThreadID: id, Path: agent.AgentPath(record.Metadata.AgentPath), Nickname: record.Metadata.AgentNickname, Role: record.Metadata.AgentRole})
	}
	if c.router.services.SpawnGraph != nil {
		_ = c.router.services.SpawnGraph.UpsertThreadSpawnEdge(string(record.ParentThreadID), id, agent.ThreadSpawnEdgeOpen)
	}
	return &agent.ResumeAgentResult{Status: c.status(id)}, nil
}
func (c *runtimeAgentController) CloseAgent(ctx context.Context, args *agent.CloseAgentArgs) (*agent.CloseAgentResult, error) {
	if args == nil || strings.TrimSpace(args.Target) == "" {
		return nil, fmt.Errorf("target is required")
	}
	target, _, err := c.resolveTarget(args.Target)
	if err != nil {
		return &agent.CloseAgentResult{PreviousStatus: agent.AgentMessageStatus{Kind: agent.AgentMessageStatusNotFound}}, nil
	}
	previous := c.status(target)
	// Rust's close_agent shuts down the target and any open descendants
	// reachable from the spawn tree (shutdown_agent_tree).
	closeIDs := []string{target}
	if c.router.services.SpawnGraph != nil {
		openStatus := agent.ThreadSpawnEdgeOpen
		if descendants, listErr := c.router.services.SpawnGraph.ListThreadSpawnDescendants(target, &openStatus); listErr == nil {
			closeIDs = append(closeIDs, descendants...)
		}
	}
	for _, id := range closeIDs {
		c.closeAgentThread(id)
	}
	return &agent.CloseAgentResult{PreviousStatus: previous}, nil
}

func (c *runtimeAgentController) closeAgentThread(threadID string) {
	if active := c.router.activeRuntimeTurnSnapshot(threadID); active != nil {
		_, _ = c.router.handleTurnInterrupt(requestWithInternalParams(MethodTurnInterrupt, turn.TurnInterruptParams{ThreadID: threadID, TurnID: active.ID}))
	}
	previous := c.status(threadID)
	if previous.Kind != agent.AgentMessageStatusNotFound {
		c.registry.ReleaseSpawnedThread(threadID)
		if c.router.agentRegistry != c.registry {
			c.router.agentRegistry.ReleaseSpawnedThread(threadID)
		}
		if c.router.services.SpawnGraph != nil {
			_ = c.router.services.SpawnGraph.SetThreadSpawnEdgeStatus(threadID, agent.ThreadSpawnEdgeClosed)
		}
	}
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

func (c *runtimeAgentController) SendMessage(ctx context.Context, args *agent.SendMessageArgs) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if args == nil || strings.TrimSpace(args.Target) == "" || strings.TrimSpace(args.Message) == "" {
		return fmt.Errorf("target and message are required")
	}
	threadID, path, err := c.resolveTarget(args.Target)
	if err != nil {
		return err
	}
	args.ResolvedThreadID, args.ResolvedPath = threadID, path
	item := runtimeAgentCommunicationInputItem(c.scopePath, path, args.Message, false, args.Plaintext)
	if active := c.router.activeRuntimeTurnSnapshot(threadID); active != nil {
		if err := c.router.requireSteerMailbox().Enqueue(&turn.SteerEnqueueParams{ThreadID: threadID, TurnID: active.ID, InputItems: []any{item}}); err != nil {
			return err
		}
	} else {
		c.router.enqueueRuntimeAgentMessage(threadID, item)
	}
	return nil
}

func (c *runtimeAgentController) FollowupTask(ctx context.Context, args *agent.FollowupTaskArgs) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if args == nil || strings.TrimSpace(args.Target) == "" || strings.TrimSpace(args.Message) == "" {
		return fmt.Errorf("target and message are required")
	}
	threadID, path, err := c.resolveTarget(args.Target)
	if err != nil {
		return err
	}
	if path == "/root" {
		return fmt.Errorf("follow-up tasks can't target the root agent")
	}
	args.ResolvedThreadID, args.ResolvedPath = threadID, path
	item := runtimeAgentCommunicationInputItem(c.scopePath, path, args.Message, true, args.Plaintext)
	if active := c.router.activeRuntimeTurnSnapshot(threadID); active != nil {
		return c.router.requireSteerMailbox().Enqueue(&turn.SteerEnqueueParams{ThreadID: threadID, TurnID: active.ID, InputItems: []any{item}})
	}
	queued := c.router.drainRuntimeAgentMessages(threadID)
	params := turn.TurnStartParams{ThreadID: threadID, CWD: c.cwd, ParentTurnID: c.parentTurnID, RootTurnID: c.rootTurnID, AdditionalInputItems: append(queued, item)}
	_, err = c.router.handleTurnStart(requestWithInternalParams(MethodTurnStart, params))
	return err
}

func (c *runtimeAgentController) WaitForActivity(ctx context.Context, args *agent.WaitForActivityArgs) (*agent.WaitForActivityResult, error) {
	timeout := agent.MultiAgentV2DefaultWait
	if args != nil && args.TimeoutMS != nil {
		timeout = time.Duration(*args.TimeoutMS) * time.Millisecond
	}
	mailbox := c.router.runtimeAgentActivityMailbox(c.rootID)
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case message := <-mailbox:
		return &agent.WaitForActivityResult{Message: firstNonEmpty(message, "Wait completed.")}, nil
	case <-timer.C:
		return &agent.WaitForActivityResult{Message: "Wait timed out.", TimedOut: true}, nil
	}
}

func (c *runtimeAgentController) InterruptAgent(ctx context.Context, args *agent.InterruptAgentArgs) (*agent.InterruptAgentResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if args == nil || strings.TrimSpace(args.Target) == "" {
		return nil, fmt.Errorf("target is required")
	}
	threadID, path, err := c.resolveTarget(args.Target)
	if err != nil {
		return nil, err
	}
	if path == "/root" {
		return nil, fmt.Errorf("root is not a spawned agent")
	}
	if threadID == c.parentID {
		return nil, fmt.Errorf("an agent cannot interrupt itself; return your result and let the parent interrupt you if needed")
	}
	args.ResolvedThreadID, args.ResolvedPath = threadID, path
	previous := c.status(threadID)
	if active := c.router.activeRuntimeTurnSnapshot(threadID); active != nil {
		if _, err := c.router.handleTurnInterrupt(requestWithInternalParams(MethodTurnInterrupt, turn.TurnInterruptParams{ThreadID: threadID, TurnID: active.ID})); err != nil {
			return nil, err
		}
	}
	c.router.notifyRuntimeAgentActivity(c.rootID, "Wait completed.")
	return &agent.InterruptAgentResult{PreviousStatus: agent.V2AgentStatusValue(previous)}, nil
}

func (c *runtimeAgentController) ListAgents(ctx context.Context, args *agent.ListAgentsArgs) (*agent.ListAgentsResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	prefix := ""
	if args != nil && args.PathPrefix != nil {
		prefix = runtimeCanonicalAgentPath(c.scopePath, *args.PathPrefix)
	}
	result := &agent.ListAgentsResult{Agents: []agent.ListedAgent{}}
	if prefix == "" || strings.HasPrefix("/root", prefix) {
		result.Agents = append(result.Agents, agent.ListedAgent{AgentName: "/root", AgentStatus: "running"})
	}
	for _, metadata := range c.registry.LiveAgents() {
		path := string(metadata.Path)
		if prefix != "" && !strings.HasPrefix(path, prefix) {
			continue
		}
		result.Agents = append(result.Agents, agent.ListedAgent{AgentName: path, AgentStatus: agent.V2AgentStatusValue(c.status(metadata.ThreadID))})
	}
	sort.Slice(result.Agents, func(i int, j int) bool { return result.Agents[i].AgentName < result.Agents[j].AgentName })
	return result, nil
}

func (r *RuntimeRouter) enqueueRuntimeAgentMessage(threadID string, item any) {
	if r == nil || strings.TrimSpace(threadID) == "" || item == nil {
		return
	}
	r.agentMessagesMu.Lock()
	r.agentMessages[threadID] = append(r.agentMessages[threadID], item)
	r.agentMessagesMu.Unlock()
}

func (r *RuntimeRouter) drainRuntimeAgentMessages(threadID string) []any {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return nil
	}
	r.agentMessagesMu.Lock()
	defer r.agentMessagesMu.Unlock()
	items := append([]any(nil), r.agentMessages[threadID]...)
	delete(r.agentMessages, threadID)
	return items
}

func (r *RuntimeRouter) runtimeAgentIdentity(threadID string) (string, string) {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" {
		return threadID, "/root"
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil {
		return threadID, "/root"
	}
	path := strings.TrimSpace(record.Metadata.AgentPath)
	if path == "" {
		path = "/root"
	}
	rootID := threadID
	for record != nil && record.ParentThreadID != "" {
		rootID = string(record.ParentThreadID)
		parent, parentErr := r.threadRecord(record.ParentThreadID, true, false)
		if parentErr != nil || parent == nil {
			break
		}
		record = parent
	}
	if !strings.HasPrefix(path, "/") {
		path = runtimeCanonicalAgentPath("/root", path)
	}
	return strings.TrimSpace(rootID), path
}

func (r *RuntimeRouter) runtimeAgentRegistry(rootID string) *agent.Registry {
	if r == nil {
		return nil
	}
	rootID = strings.TrimSpace(rootID)
	if rootID == "" {
		return r.agentRegistry
	}
	r.agentRegistryMu.Lock()
	defer r.agentRegistryMu.Unlock()
	if registry := r.agentRegistries[rootID]; registry != nil {
		return registry
	}
	registry := agent.NewRegistry()
	registry.RegisterRootThread(rootID)
	if r.services.ThreadRouter != nil && r.services.ThreadRouter.store != nil {
		records, err := r.services.ThreadRouter.store.AllRecords()
		if err == nil {
			for i := range records {
				record := records[i]
				if strings.TrimSpace(record.Metadata.AgentPath) == "" || !runtimeRecordDescendsFrom(record, session.ThreadID(rootID), records) {
					continue
				}
				registry.RegisterSpawnedThread(agent.Metadata{
					ThreadID: string(record.ID), Path: agent.AgentPath(record.Metadata.AgentPath),
					Nickname: record.Metadata.AgentNickname, Role: record.Metadata.AgentRole,
				})
			}
		}
	}
	r.agentRegistries[rootID] = registry
	return registry
}

func runtimeRecordDescendsFrom(record session.Record, rootID session.ThreadID, records []session.Record) bool {
	if record.ID == rootID {
		return true
	}
	parents := make(map[session.ThreadID]session.ThreadID, len(records))
	for i := range records {
		parents[records[i].ID] = records[i].ParentThreadID
	}
	for parent := record.ParentThreadID; parent != ""; parent = parents[parent] {
		if parent == rootID {
			return true
		}
	}
	return false
}

func runtimeCanonicalAgentPath(scopePath string, reference string) string {
	scopePath = strings.TrimSpace(strings.ReplaceAll(scopePath, "\\", "/"))
	if scopePath == "" || scopePath == "/" {
		scopePath = "/root"
	}
	reference = strings.TrimSpace(strings.ReplaceAll(reference, "\\", "/"))
	if reference == "" {
		return scopePath
	}
	if strings.HasPrefix(reference, "/") {
		parts := strings.FieldsFunc(reference, func(r rune) bool { return r == '/' })
		return "/" + strings.Join(parts, "/")
	}
	parts := strings.FieldsFunc(strings.TrimSuffix(scopePath, "/")+"/"+reference, func(r rune) bool { return r == '/' })
	return "/" + strings.Join(parts, "/")
}

func (c *runtimeAgentController) resolveTarget(target string) (string, string, error) {
	if c == nil || c.registry == nil {
		return "", "", fmt.Errorf("agent runtime is unavailable")
	}
	target = strings.TrimSpace(target)
	if target == "" {
		return "", "", fmt.Errorf("target is required")
	}
	if metadata, ok := c.registry.MetadataForThread(target); ok {
		return metadata.ThreadID, string(metadata.Path), nil
	}
	path := runtimeCanonicalAgentPath(c.scopePath, target)
	threadID, ok := c.registry.AgentIDForPath(agent.AgentPath(path))
	if !ok {
		return "", path, fmt.Errorf("agent %s not found", target)
	}
	return threadID, path, nil
}

func runtimeAgentCommunicationInputItem(author string, recipient string, message string, trigger bool, plaintext bool) map[string]any {
	messageType := "MESSAGE"
	if trigger {
		messageType = "NEW_TASK"
	}
	author = runtimeCanonicalAgentPath("/root", strings.TrimPrefix(author, "/root/"))
	recipient = runtimeCanonicalAgentPath("/root", strings.TrimPrefix(recipient, "/root/"))
	envelope := fmt.Sprintf("Message Type: %s\nTask name: %s\nSender: %s\nPayload:\n", messageType, recipient, author)
	content := []any{map[string]any{"type": "input_text", "text": envelope}}
	if plaintext {
		content[0] = map[string]any{"type": "input_text", "text": envelope + strings.TrimSpace(message)}
	} else if strings.TrimSpace(message) != "" {
		content = append(content, map[string]any{"type": "encrypted_content", "encrypted_content": strings.TrimSpace(message)})
	}
	return map[string]any{"type": "agent_message", "author": author, "recipient": recipient, "content": content}
}

func (r *RuntimeRouter) runtimeAgentActivityMailbox(rootID string) chan string {
	if r == nil {
		return nil
	}
	rootID = strings.TrimSpace(rootID)
	r.agentActivityMu.Lock()
	defer r.agentActivityMu.Unlock()
	mailbox := r.agentActivity[rootID]
	if mailbox == nil {
		mailbox = make(chan string, 32)
		r.agentActivity[rootID] = mailbox
	}
	return mailbox
}

func (r *RuntimeRouter) notifyRuntimeAgentActivity(rootID string, message string) {
	mailbox := r.runtimeAgentActivityMailbox(rootID)
	if mailbox == nil {
		return
	}
	if strings.TrimSpace(message) == "" {
		message = "Wait completed."
	}
	select {
	case mailbox <- message:
	default:
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
	return &Request{JSONRPC: "2.0", ID: StringID("internal-agent-" + string(newThreadID())), Method: method, Params: data, Internal: true}
}

func agentStringValue(value *string) string {
	if value == nil {
		return ""
	}
	return strings.TrimSpace(*value)
}

func runtimeForkTurns(value *string) string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return "all"
	}
	return strings.ToLower(strings.TrimSpace(*value))
}

// filterInheritedCurrentTimeReminders removes current-time reminder and
// multi-agent role/mode developer content copied from a parent thread into a
// full-history fork. Filtering happens per content item so unrelated content
// sharing a developer message survives; the message is dropped only when
// filtering leaves it empty (Rust #38446/#38619/#39641).
func filterInheritedCurrentTimeReminders(items []session.Item) []session.Item {
	filtered := items[:0]
	for i := range items {
		item := &items[i]
		if retainForkedDeveloperMessage(item) {
			filtered = append(filtered, *item)
		}
	}
	return filtered
}

// retainForkedDeveloperMessage filters fork-specific developer instruction
// content items out of a developer message while preserving unrelated content
// (Rust #39641). It returns false only when the message would be left empty.
func retainForkedDeveloperMessage(item *session.Item) bool {
	if item == nil {
		return false
	}
	if len(item.Content) == 0 {
		if isForkExcludedDeveloperText(item.Text) {
			return false
		}
		if sessionItemIsCurrentTimeReminder(item) {
			return false
		}
		return true
	}
	retained := item.Content[:0]
	for _, part := range item.Content {
		if part.Type == "input_text" && isForkExcludedDeveloperText(part.Text) {
			continue
		}
		retained = append(retained, part)
	}
	item.Content = retained
	if len(retained) > 0 {
		return true
	}
	if sessionItemIsCurrentTimeReminder(item) {
		return false
	}
	return strings.TrimSpace(item.Text) != ""
}

func isForkExcludedDeveloperText(text string) bool {
	text = strings.TrimSpace(text)
	return strings.Contains(text, "<multi_agent_role>") ||
		strings.Contains(text, "<multi_agent_mode>") ||
		strings.Contains(text, "<multi_agent_usage_hint>") ||
		strings.Contains(text, "<current_time_reminder>")
}

func firstNonNilError(err error, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}

var _ agent.ToolController = (*runtimeAgentController)(nil)
var _ agent.V2ToolController = (*runtimeAgentController)(nil)
