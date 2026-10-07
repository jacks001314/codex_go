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
	"codex_go/features"
	"codex_go/model"
	"codex_go/session"
	"codex_go/telemetry"
	"codex_go/tool"
	"codex_go/turn"
)

type runtimeAgentController struct {
	router       *RuntimeRouter
	parentID     string
	parentTurnID string
	rootTurnID   string
	// turnTrigger attributes delegated usage to the turn that initiated it
	// (Rust #44659).
	turnTrigger string
	// cyberAccessProgram is the initiating turn's resolved core program, which a
	// spawned or continued child turn inherits (Rust SpawnAgentOptions /
	// SendInputOptions, #44893).
	cyberAccessProgram string
	rootID             string
	scopePath          string
	cwd                string
	maxThreads         int
	depth              int
	maxDepth           int
	version            agent.MultiAgentVersion
	environments       []map[string]any
	registry           *agent.Registry
}

func newRuntimeAgentController(router *RuntimeRouter, parentID string, cwd string, maxThreads int) agent.ToolController {
	return newRuntimeAgentControllerWithVersion(router, parentID, cwd, maxThreads, agent.VersionV1)
}

func newRuntimeAgentControllerWithVersion(router *RuntimeRouter, parentID string, cwd string, maxThreads int, version agent.MultiAgentVersion) agent.ToolController {
	return newRuntimeAgentControllerWithEnvironmentSelections(router, parentID, cwd, maxThreads, version, nil)
}

func newRuntimeAgentControllerWithEnvironmentSelections(router *RuntimeRouter, parentID string, cwd string, maxThreads int, version agent.MultiAgentVersion, environments []map[string]any) agent.ToolController {
	return newRuntimeAgentControllerForTurn(router, parentID, "", "", "", "", cwd, maxThreads, version, environments)
}

func newRuntimeAgentControllerForTurn(router *RuntimeRouter, parentID string, parentTurnID string, rootTurnID string, turnTrigger string, cyberAccessProgram string, cwd string, maxThreads int, version agent.MultiAgentVersion, environments []map[string]any) agent.ToolController {
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
		router:             router,
		parentID:           strings.TrimSpace(parentID),
		parentTurnID:       strings.TrimSpace(parentTurnID),
		rootTurnID:         strings.TrimSpace(rootTurnID),
		turnTrigger:        strings.TrimSpace(turnTrigger),
		cyberAccessProgram: strings.TrimSpace(cyberAccessProgram),
		rootID:             rootID,
		scopePath:          scopePath,
		cwd:                strings.TrimSpace(cwd),
		maxThreads:         maxThreads,
		depth:              depth,
		maxDepth:           config.DefaultAgentMaxDepth,
		version:            version,
		environments:       cloneMapSlice(environments),
		registry:           registry,
	}
}

func (c *runtimeAgentController) SpawnAgent(ctx context.Context, args *agent.SpawnAgentArgs) (*agent.SpawnAgentResult, error) {
	if args == nil {
		args = &agent.SpawnAgentArgs{}
	}
	// Rust's record_collab_spawn_failure labels every spawn failure with the fork
	// mode the spawn would have used, so the label is resolved before the first
	// failure can be reported.
	forkMode := telemetry.AgentSpawnFailureForkModeNone
	if c != nil {
		forkMode = spawnFailureForkMode(c.version, args.ForkContext, runtimeForkTurns(args.ForkTurns))
	}
	if c == nil || c.router == nil || c.router.services.ThreadRouter == nil || c.router.services.ThreadRouter.store == nil {
		return nil, recordRuntimeAgentSpawnFailure(c, ctx, errAgentRuntimeUnavailable, tool.AgentErrorContextManagerUnavailable, forkMode)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c.version == agent.VersionV1 && c.maxDepth >= 0 && c.depth+1 > c.maxDepth {
		return nil, agent.ErrAgentDepthLimitReached
	}
	// Rust `prepare_agent_spawn_config`: resolve the requested model/effort (or
	// the configured subagent defaults) before the child thread is created.
	if err := c.resolveSpawnModelOverrides(args); err != nil {
		return nil, err
	}
	registry := c.registry
	if registry == nil {
		registry = c.router.runtimeAgentRegistry(c.rootID)
	}
	reservation, err := registry.ReserveSpawnSlot(c.maxThreads)
	if err != nil {
		return nil, recordRuntimeAgentSpawnFailure(c, ctx, err, tool.AgentErrorContextRegistryCapacity, forkMode)
	}
	committed := false
	defer func() {
		if !committed {
			reservation.Cancel()
		}
	}()
	nickname, err := reservation.ReserveAgentNickname(args.NicknameCandidates, "")
	if err != nil && len(args.NicknameCandidates) > 0 {
		return nil, recordRuntimeAgentSpawnFailure(c, ctx, err, tool.AgentErrorContextNicknameUnavailable, forkMode)
	}
	agentPath := ""
	if c.version == agent.VersionV2 {
		taskName := strings.TrimSpace(args.TaskName)
		if taskName == "" {
			taskName = "agent_" + strings.ToLower(safeIdentifier(string(newThreadID())))
		}
		agentPath = runtimeCanonicalAgentPath(c.scopePath, taskName)
		if err := reservation.ReserveAgentPath(agent.AgentPath(agentPath)); err != nil {
			return nil, recordRuntimeAgentSpawnFailure(c, ctx, err, tool.AgentErrorContextDuplicatePath, forkMode)
		}
	}
	threadID := newThreadID()
	now := time.Now().UTC()
	// Rust #46075: build the child from the invoking step's captured settings.
	// A settings update during the active turn changes the turn's live model,
	// effort and summary, and spawned agents must inherit those rather than the
	// thread record's older values.
	capturedModel, capturedEffort, capturedSummary := c.capturedSpawnSettings()
	modelID := agentStringValue(args.Model)
	if modelID == "" {
		modelID = capturedModel
	}
	providerID := ""
	developerInstructions := ""
	var parentDynamicTools []json.RawMessage
	if parent, readErr := c.router.threadRecord(session.ThreadID(c.parentID), false, false); readErr == nil && parent != nil {
		if modelID == "" {
			modelID = parent.Metadata.Model
		}
		providerID = parent.Metadata.ModelProvider
		developerInstructions = parent.Metadata.Instructions
		parentDynamicTools = parent.Metadata.DynamicTools
	}
	if args.DeveloperInstructions != nil {
		developerInstructions = *args.DeveloperInstructions
	}
	// Rust #49075 `TurnEnvironmentSnapshot::inheritable_selections`: the child
	// keeps the spawning step's ready and still-starting attachments and drops a
	// failed one instead of inheriting it verbatim.
	inheritedEnvironments := inheritableEnvironmentSelections(c.environments)
	extra := map[string]any{}
	if len(inheritedEnvironments) > 0 {
		extra[runtimeEnvironmentSelectionsExtraKey] = cloneMapSlice(inheritedEnvironments)
	}
	var record *session.Record
	forkTurns := runtimeForkTurns(args.ForkTurns)
	if c.version == agent.VersionV2 && forkTurns != "none" {
		// Rust #51329: partial-history forks are gone. `all` and the legacy
		// positive-integer spellings both inherit the parent's full history, so the
		// child keeps the cached prompt prefix and inherits the scrub below.
		if forkTurns != "all" {
			count, parseErr := strconv.ParseUint(forkTurns, 10, 64)
			if parseErr != nil || count == 0 {
				return nil, recordRuntimeAgentSpawnFailure(c, ctx, fmt.Errorf("fork_turns must be `none` or `all`"), tool.AgentErrorContextForkHistory, forkMode)
			}
		}
		parent, readErr := c.router.threadRecord(session.ThreadID(c.parentID), true, true)
		if readErr != nil || parent == nil {
			return nil, recordRuntimeAgentSpawnFailure(c, ctx, firstNonNilError(readErr, fmt.Errorf("parent thread %s is unavailable", c.parentID)), tool.AgentErrorContextForkHistory, forkMode)
		}
		forkOptions := session.ForkOptions{NewID: threadID, ParentThreadID: session.ThreadID(c.parentID), Now: now, Mode: session.ForkAll}
		forked, forkErr := c.router.services.ThreadRouter.store.ForkRecord(parent, forkOptions)
		if forkErr != nil {
			return nil, recordRuntimeAgentSpawnFailure(c, ctx, forkErr, tool.AgentErrorContextForkHistory, forkMode)
		}
		record = forked
		record.Items = filterInheritedCurrentTimeReminders(record.Items)
		// Rust spawn.rs's forked-item provenance: persist the scope of every copied
		// conversational message, so a resume cannot recapture the parent's
		// authorization as the child's own.
		markInheritedUserMessages(record.Items)
	} else {
		record = &session.Record{ID: threadID, SessionID: string(threadID), ParentThreadID: session.ThreadID(c.parentID), CreatedAt: now, UpdatedAt: now, RecencyAt: now}
		if err := c.router.services.ThreadRouter.store.Create(record); err != nil {
			return nil, recordRuntimeAgentSpawnFailure(c, ctx, err, tool.AgentErrorContextChildStartup, forkMode)
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
	// Rust #50082: a fresh V2 subagent inherits the parent's client-defined
	// dynamic tools when the disabled-by-default feature is enabled.
	if c.version == agent.VersionV2 && forkTurns == "none" && len(parentDynamicTools) > 0 && c.multiAgentV2DynamicToolsEnabled() {
		record.Metadata.DynamicTools = cloneRawMessages(parentDynamicTools)
	}
	record.Metadata.SessionPrefix = session.PrefixForSessionID(string(threadID))
	record.Metadata.Extra = extra
	if err := c.router.runtimeSaveThreadRecord(record); err != nil {
		_ = c.router.services.ThreadRouter.store.Delete(threadID)
		return nil, recordRuntimeAgentSpawnFailure(c, ctx, err, tool.AgentErrorContextChildStartup, forkMode)
	}
	if c.router.services.SpawnGraph != nil {
		if err := c.router.services.SpawnGraph.UpsertThreadSpawnEdge(c.parentID, string(threadID), agent.ThreadSpawnEdgeOpen); err != nil {
			_ = c.router.services.ThreadRouter.store.Delete(threadID)
			return nil, recordRuntimeAgentSpawnFailure(c, ctx, err, tool.AgentErrorContextChildStartup, forkMode)
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
		params := &turn.TurnStartParams{ThreadID: string(threadID), CWD: c.cwd, Model: modelID, Environments: cloneMapSlice(inheritedEnvironments), ParentTurnID: c.parentTurnID, RootTurnID: c.rootTurnID, TurnTrigger: c.turnTrigger, CoreCyberAccessProgram: c.cyberAccessProgram}
		if c.version == agent.VersionV2 {
			// Rust #51402 `tasks/mod.rs`: a turn triggered by an inter-agent
			// communication records that communication's author as its
			// initiating agent path.
			params.InitiatingAgentPath = c.scopePath
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
		} else if capturedEffort != "" {
			effort := capturedEffort
			params.Effort = &effort
		}
		if capturedSummary != "" {
			summary := capturedSummary
			params.Summary = &summary
		}
		// Rust #41308: subagents follow the root thread's service tier, not a
		// per-spawn override. appServiceTierForTurn drops the tier if the child
		// model does not support it and is gated on fast_mode, mirroring Rust.
		if rootTier := c.rootServiceTierForSpawn(); rootTier != "" {
			params.ServiceTier = stringPtrIfNotEmpty(rootTier)
			params.ServiceTierSet = true
		}
		if _, err := c.router.handleTurnStart(requestWithInternalParams(MethodTurnStart, params)); err != nil {
			registry.ReleaseSpawnedThread(string(threadID))
			_ = c.router.services.ThreadRouter.store.Delete(threadID)
			return nil, recordRuntimeAgentSpawnFailure(c, ctx, err, tool.AgentErrorContextInputAdmission, forkMode)
		}
	}
	return &agent.SpawnAgentResult{
		AgentID:         string(threadID),
		TaskName:        agentPath,
		Nickname:        stringPtrIfNotEmpty(nickname),
		Model:           modelID,
		ReasoningEffort: c.resolvedSpawnReasoningEffort(modelID, args, capturedEffort),
	}, nil
}

// resolvedSpawnReasoningEffort mirrors Rust #51463: the child's resolved startup
// effort, falling back to the model's default reasoning level when neither a
// requested nor an inherited effort applies.
func (c *runtimeAgentController) resolvedSpawnReasoningEffort(modelID string, args *agent.SpawnAgentArgs, capturedEffort string) string {
	if args != nil && args.ReasoningEffort != nil {
		if value := strings.TrimSpace(*args.ReasoningEffort); value != "" {
			return value
		}
	}
	if value := strings.TrimSpace(capturedEffort); value != "" {
		return value
	}
	if strings.TrimSpace(modelID) == "" {
		return ""
	}
	manager := c.modelsManagerForSpawn()
	if manager == nil {
		return ""
	}
	return strings.TrimSpace(manager.GetModelInfo(modelID, nil).DefaultReasoningLevel)
}

// modelsManagerForSpawn mirrors the exec lane's accessor: the running agent's
// catalog when it exposes one, otherwise the bundled static catalog.
func (c *runtimeAgentController) modelsManagerForSpawn() model.ModelsManager {
	if c == nil || c.router == nil {
		return nil
	}
	if runner, ok := c.router.services.Agent.(*model.ResponsesAgentRunner); ok && runner != nil && runner.ModelsManager != nil {
		return runner.ModelsManager
	}
	return model.NewStaticModelsManager(model.BundledModelsResponse())
}

// spawnAgentDefaults returns the invoking turn's configured subagent defaults
// (Rust `config.agent_default_subagent_model` and
// `agent_default_subagent_reasoning_effort`, parsed from the `[agents]` table).
func (c *runtimeAgentController) spawnAgentDefaults() (string, string) {
	if c == nil || c.router == nil {
		return "", ""
	}
	params := c.router.activeTurnParams(c.parentID)
	if params == nil {
		return "", ""
	}
	cfg, err := c.router.effectiveConfigForTurn(params)
	if err != nil || cfg == nil {
		return "", ""
	}
	agentsConfig, err := cfg.AgentsConfig(c.router.configBaseDirForAgents())
	if err != nil || agentsConfig == nil {
		return "", ""
	}
	return strings.TrimSpace(agentsConfig.DefaultSubagentModel), strings.TrimSpace(agentsConfig.DefaultSubagentReasoningEffort)
}

// resolveSpawnModelOverrides mirrors Rust's
// `apply_requested_spawn_agent_model_overrides`: a requested model - or the
// configured `agents.default_subagent_model` - must exist for the active
// multi-agent backend, and a requested effort - or
// `agents.default_subagent_reasoning_effort` - must be supported by the resolved
// model. When only a model is requested its default reasoning level applies.
// Explicit tool arguments win over the configured defaults, and both win over
// the invoking step's captured settings.
func (c *runtimeAgentController) resolveSpawnModelOverrides(args *agent.SpawnAgentArgs) error {
	if c == nil || args == nil {
		return nil
	}
	manager := c.modelsManagerForSpawn()
	if manager == nil {
		return nil
	}
	requestedModel := ""
	if args.Model != nil {
		requestedModel = strings.TrimSpace(*args.Model)
	}
	requestedEffort := ""
	if args.ReasoningEffort != nil {
		requestedEffort = strings.TrimSpace(*args.ReasoningEffort)
	}
	defaultModel, defaultEffort := c.spawnAgentDefaults()
	if requestedModel == "" {
		requestedModel = defaultModel
	}
	if requestedEffort == "" {
		requestedEffort = defaultEffort
	}
	if requestedModel == "" && requestedEffort == "" {
		return nil
	}

	capturedModel, _, _ := c.capturedSpawnSettings()
	selectedModel := capturedModel
	var selectedPreset *model.ModelPreset
	if requestedModel != "" {
		presets := manager.ListModels(model.RefreshOffline)
		name, err := model.SpawnAgentModelName(presets, requestedModel, string(c.version))
		if err != nil {
			return err
		}
		selectedModel = name
		for i := range presets {
			if presets[i].Model == name {
				selectedPreset = &presets[i]
				break
			}
		}
		value := selectedModel
		args.Model = &value
	}
	if requestedEffort != "" {
		info := manager.GetModelInfo(selectedModel, nil)
		if err := model.ValidateSpawnAgentReasoningEffort(selectedModel, info.SupportedReasoningLevels, requestedEffort); err != nil {
			return err
		}
		value := requestedEffort
		args.ReasoningEffort = &value
	} else if selectedPreset != nil && strings.TrimSpace(selectedPreset.DefaultReasoningLevel) != "" {
		value := strings.TrimSpace(selectedPreset.DefaultReasoningLevel)
		args.ReasoningEffort = &value
	}
	return nil
}

// capturedSpawnSettings returns the invoking turn's captured model, effective
// reasoning effort and reasoning summary (Rust #46075's `step_context.settings`).
// A settings update during the active turn rewrites those live values, so a
// spawn must read them instead of the thread record's older snapshot. Empty
// strings mean the invoking turn is not live and callers should fall back.
func (c *runtimeAgentController) capturedSpawnSettings() (string, string, string) {
	if c == nil || c.router == nil || strings.TrimSpace(c.parentID) == "" {
		return "", "", ""
	}
	active := c.router.threads.ActiveTurn(c.parentID)
	if active == nil || active.Params == nil {
		return "", "", ""
	}
	// Only the turn that owns this controller carries its captured settings.
	if expected := strings.TrimSpace(c.parentTurnID); expected != "" && strings.TrimSpace(active.TurnID) != expected {
		return "", "", ""
	}
	params := active.Params
	effort := ""
	if cfg, err := c.router.effectiveConfigForTurn(params); err == nil {
		effort = appReasoningEffortForTurn(cfg, params)
	}
	return strings.TrimSpace(params.Model), effort, stringPtrValue(params.Summary)
}

// multiAgentV2DynamicToolsEnabled reports whether the disabled-by-default
// `multi_agent_v2_dynamic_tools` feature is enabled for the active parent turn
// (Rust #50082).
func (c *runtimeAgentController) multiAgentV2DynamicToolsEnabled() bool {
	if c == nil || c.router == nil {
		return false
	}
	active := c.router.threads.ActiveTurn(c.parentID)
	if active == nil || active.Params == nil {
		return false
	}
	cfg, err := c.router.effectiveConfigForTurn(active.Params)
	if err != nil || cfg == nil {
		return false
	}
	return features.Enabled(cfg.FeatureSettings(), "multi_agent_v2_dynamic_tools")
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
	response, err := c.router.handleTurnStart(requestWithInternalParams(MethodTurnStart, turn.TurnStartParams{ThreadID: target, Prompt: prompt, AdditionalInputItems: append([]any(nil), args.Items...), ParentTurnID: c.parentTurnID, CoreCyberAccessProgram: c.cyberAccessProgram}))
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
	// reachable from the spawn tree (shutdown_agent_tree). Rust #51515: the
	// teardown records a bounded, payload-free failure report instead of
	// dropping cleanup errors; the report travels back in the result.
	state := agent.NewAgentTreeShutdownState(firstNonEmpty(c.rootID, target))
	closeIDs := []string{target}
	if c.router.services.SpawnGraph != nil {
		openStatus := agent.ThreadSpawnEdgeOpen
		if descendants, listErr := c.router.services.SpawnGraph.ListThreadSpawnDescendants(target, &openStatus); listErr == nil {
			closeIDs = append(closeIDs, descendants...)
		} else {
			state.RecordFailure(agent.AgentTreeShutdownOperationFailed("list_descendants", "list_open_descendants", target, agentShutdownErrorKind(listErr)))
		}
	}
	for _, id := range closeIDs {
		c.closeAgentThread(state, id)
	}
	result := &agent.CloseAgentResult{PreviousStatus: previous}
	if report := state.Report(); !report.Empty() {
		result.ShutdownReport = &report
	}
	return result, nil
}

// agentShutdownErrorKind maps an error to a stable, payload-free category for the
// shutdown report (Rust #51515's thread_store_error_kind uses the same idea over
// ThreadStoreError variants).
func agentShutdownErrorKind(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, session.ErrThreadNotFound):
		return "thread_store_not_found"
	case errors.Is(err, session.ErrThreadArchived):
		return "thread_store_archived"
	case errors.Is(err, session.ErrThreadSectionMissing):
		return "thread_store_section_missing"
	case errors.Is(err, session.ErrConflict):
		return "thread_store_conflict"
	case errors.Is(err, context.Canceled):
		return "cancelled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline_exceeded"
	default:
		return "operation_error"
	}
}

func (c *runtimeAgentController) closeAgentThread(state *agent.AgentTreeShutdownState, threadID string) {
	if active := c.router.activeRuntimeTurnSnapshot(threadID); active != nil {
		if _, err := c.router.handleTurnInterrupt(requestWithInternalParams(MethodTurnInterrupt, turn.TurnInterruptParams{ThreadID: threadID, TurnID: active.ID})); err != nil {
			state.RecordFailure(agent.AgentTreeShutdownOperationFailed("turn_interrupt", "stop_active_turn", threadID, agentShutdownErrorKind(err)))
		}
	}
	previous := c.status(threadID)
	if previous.Kind != agent.AgentMessageStatusNotFound {
		c.registry.ReleaseSpawnedThread(threadID)
		if c.router.agentRegistry != c.registry {
			c.router.agentRegistry.ReleaseSpawnedThread(threadID)
		}
		if c.router.services.SpawnGraph != nil {
			if err := c.router.services.SpawnGraph.SetThreadSpawnEdgeStatus(threadID, agent.ThreadSpawnEdgeClosed); err != nil {
				state.RecordFailure(agent.AgentTreeShutdownOperationFailed("close_spawn_edge", "set_spawn_edge_closed", threadID, agentShutdownErrorKind(err)))
			}
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
	// Rust #51402 `tasks/mod.rs`: the triggered inter-agent communication's
	// author is this turn's initiating agent path.
	params := turn.TurnStartParams{ThreadID: threadID, CWD: c.cwd, ParentTurnID: c.parentTurnID, RootTurnID: c.rootTurnID, TurnTrigger: c.turnTrigger, CoreCyberAccessProgram: c.cyberAccessProgram, InitiatingAgentPath: c.scopePath, AdditionalInputItems: append(queued, item)}
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
	// Rust's wait_agent V2 handler records the elapsed wait with the outcome it
	// observed; a wait that was dropped (cancelled) has no outcome and is left
	// out (Rust #51332, core/src/tools/handlers/multi_agents_v2/wait.rs).
	started := time.Now()
	recordWait := func(outcome string) {
		if c == nil || c.router == nil || c.router.services.TurnMetrics == nil {
			return
		}
		c.router.services.TurnMetrics.RecordDuration(
			telemetry.MultiAgentWaitDurationMetric,
			time.Since(started),
			map[string]string{"outcome": outcome},
		)
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case message := <-mailbox:
		recordWait(telemetry.MultiAgentWaitOutcomeMailbox)
		return &agent.WaitForActivityResult{Message: firstNonEmpty(message, "Wait completed.")}, nil
	case <-timer.C:
		recordWait(telemetry.MultiAgentWaitOutcomeTimedOut)
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

// rootServiceTierForSpawn returns the root thread's currently selected service
// tier so a subagent follows the root agent tree's tier instead of a per-spawn
// override (Rust #41308). An empty result leaves the subagent's tier to its own
// config default; a non-empty tier is applied at turn start subject to the child
// model's support (via RuntimeRouter.appServiceTierForTurn, which drops an
// unsupported tier and is gated on fast_mode).
func (c *runtimeAgentController) rootServiceTierForSpawn() string {
	if c == nil || c.router == nil || strings.TrimSpace(c.rootID) == "" {
		return ""
	}
	record, err := c.router.threadRecord(session.ThreadID(c.rootID), true, false)
	if err != nil || record == nil {
		return ""
	}
	return strings.TrimSpace(record.Metadata.ServiceTier)
}

// subagentRootServiceTier returns the service tier of the root thread that a
// subagent `threadID` descends from, walking the parent-thread chain. It returns
// "" when threadID is not a subagent (no parent) or the root has no configured
// service tier, so the subagent keeps its own config default. Used to make every
// subagent turn follow the root tier (Rust #41308 Session::get_or_prepare_turn).
func (r *RuntimeRouter) subagentRootServiceTier(threadID string) string {
	if r == nil || strings.TrimSpace(threadID) == "" {
		return ""
	}
	record, err := r.threadRecord(session.ThreadID(strings.TrimSpace(threadID)), true, false)
	if err != nil || record == nil {
		return ""
	}
	if record.ParentThreadID == "" {
		return ""
	}
	for record.ParentThreadID != "" {
		parent, parentErr := r.threadRecord(record.ParentThreadID, true, false)
		if parentErr != nil || parent == nil {
			return ""
		}
		record = parent
	}
	return strings.TrimSpace(record.Metadata.ServiceTier)
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
	return &Request{
		JSONRPC:        "2.0",
		ID:             StringID("internal-agent-" + string(newThreadID())),
		Method:         method,
		Params:         data,
		Internal:       true,
		InternalParams: params,
	}
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
//
// Rust #51329 additionally strips by harness-owned classification so that
// persisted role/usage hints whose wording predates the current bundled
// instructions are dropped even though they carry no marker tag; the
// positional `content_item_kinds` metadata is the Go equivalent of Rust's
// `AnnotatedContent::kind()`.
func retainForkedDeveloperMessage(item *session.Item) bool {
	if item == nil {
		return false
	}
	isDeveloper := sessionItemRole(item) == "developer"
	var kinds []string
	if isDeveloper {
		kinds = sessionItemContentItemKinds(item)
	}
	if len(item.Content) == 0 {
		if forkExcludedContentItemKind(kinds, 0) || isForkExcludedDeveloperText(item.Text) {
			return false
		}
		if sessionItemIsCurrentTimeReminder(item) {
			return false
		}
		return true
	}
	retained := item.Content[:0]
	droppedByKind := false
	retainedKinds := make([]string, 0, len(item.Content))
	for index, part := range item.Content {
		if forkExcludedContentItemKind(kinds, index) {
			droppedByKind = true
			continue
		}
		if part.Type == "input_text" && isForkExcludedDeveloperText(part.Text) {
			continue
		}
		retained = append(retained, part)
		if kinds != nil {
			kind := "unknown"
			if index < len(kinds) && kinds[index] != "" {
				kind = kinds[index]
			}
			retainedKinds = append(retainedKinds, kind)
		}
	}
	item.Content = retained
	if droppedByKind {
		rewriteSessionItemContentItemKinds(item, retainedKinds)
	}
	if len(retained) > 0 {
		return true
	}
	if sessionItemIsCurrentTimeReminder(item) {
		return false
	}
	return strings.TrimSpace(item.Text) != ""
}

// forkExcludedContentItemKinds mirrors Rust #51329's kind-based scrub of
// inherited developer content: a persisted multi-agent role or usage hint can
// predate the bundled wording and therefore carry no marker tag, but a
// harness-authored message still classifies every content item through
// `internal_chat_message_metadata_passthrough.content_item_kinds`.
var forkExcludedContentItemKinds = map[string]struct{}{
	"multi_agent.role_instructions": {},
	"multi_agent.usage_hint":        {},
}

func forkExcludedContentItemKind(kinds []string, index int) bool {
	if index < 0 || index >= len(kinds) {
		return false
	}
	_, excluded := forkExcludedContentItemKinds[kinds[index]]
	return excluded
}

func sessionItemRole(item *session.Item) string {
	if item == nil {
		return ""
	}
	if role := strings.TrimSpace(item.Role); role != "" {
		return role
	}
	for _, object := range sessionItemObjects(item) {
		if role, _ := object["role"].(string); strings.TrimSpace(role) != "" {
			return strings.TrimSpace(role)
		}
	}
	return ""
}

// sessionItemContentItemKinds returns the positional content classifications a
// persisted response item carries (Rust `to_annotated_content`, #51329).
func sessionItemContentItemKinds(item *session.Item) []string {
	for _, object := range sessionItemObjects(item) {
		metadata, _ := object["internal_chat_message_metadata_passthrough"].(map[string]any)
		if metadata == nil {
			continue
		}
		values, ok := metadata["content_item_kinds"].([]any)
		if !ok {
			continue
		}
		kinds := make([]string, 0, len(values))
		for _, value := range values {
			kind, _ := value.(string)
			kinds = append(kinds, strings.TrimSpace(kind))
		}
		return kinds
	}
	return nil
}

// rewriteSessionItemContentItemKinds realigns the positional classifications
// after content items are dropped, mirroring Rust `set_annotated_content`
// (#51329).
func rewriteSessionItemContentItemKinds(item *session.Item, kinds []string) {
	if item == nil {
		return
	}
	values := make([]any, 0, len(kinds))
	for _, kind := range kinds {
		values = append(values, kind)
	}
	if len(item.Raw) > 0 {
		var raw map[string]any
		if json.Unmarshal(item.Raw, &raw) == nil {
			if applyContentItemKinds(raw, values) {
				if encoded, err := json.Marshal(raw); err == nil {
					item.Raw = encoded
				}
			}
		}
	}
	applyContentItemKinds(item.Data, values)
}

func applyContentItemKinds(object map[string]any, values []any) bool {
	if object == nil {
		return false
	}
	applied := false
	targets := []map[string]any{object}
	if inner, ok := object["item"].(map[string]any); ok {
		targets = append(targets, inner)
	}
	for _, target := range targets {
		metadata, _ := target["internal_chat_message_metadata_passthrough"].(map[string]any)
		if metadata == nil {
			continue
		}
		if _, ok := metadata["content_item_kinds"]; !ok {
			continue
		}
		metadata["content_item_kinds"] = values
		applied = true
	}
	return applied
}

func sessionItemObjects(item *session.Item) []map[string]any {
	if item == nil {
		return nil
	}
	objects := make([]map[string]any, 0, 3)
	if len(item.Raw) > 0 {
		var raw map[string]any
		if json.Unmarshal(item.Raw, &raw) == nil {
			objects = append(objects, raw)
			if inner, ok := raw["item"].(map[string]any); ok {
				objects = append(objects, inner)
			}
		}
	}
	if item.Data != nil {
		objects = append(objects, item.Data)
	}
	return objects
}

func isForkExcludedDeveloperText(text string) bool {
	text = strings.TrimSpace(text)
	return strings.Contains(text, "<multi_agent_role>") ||
		strings.Contains(text, "<multi_agent_mode>") ||
		strings.Contains(text, "<multi_agent_usage_hint>") ||
		strings.Contains(text, "<current_time_reminder>") ||
		// Rust #46006: the nonfatal clock-failure notice is fork-excluded too.
		strings.Contains(text, "<current_time_unavailable>")
}

func firstNonNilError(err error, fallback error) error {
	if err != nil {
		return err
	}
	return fallback
}

var _ agent.ToolController = (*runtimeAgentController)(nil)
var _ agent.V2ToolController = (*runtimeAgentController)(nil)
