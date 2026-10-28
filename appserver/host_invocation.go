package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"codex_go/agent"
	"codex_go/config"
	"codex_go/mcp"
	"codex_go/session"
	"codex_go/turn"
)

// invocationRegistryForThread returns the per-thread live-call registry,
// creating it on first use. Rust stores the same state in thread extension data
// (Rust #57586 HostToolInvocationLookup); Go keeps it on the router so the host
// API can look calls up by thread ID.
func (r *RuntimeRouter) invocationRegistryForThread(threadID string) *agent.InvocationRegistry {
	if r == nil {
		return nil
	}
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return nil
	}
	r.invocationRegistriesMu.Lock()
	defer r.invocationRegistriesMu.Unlock()
	if r.invocationRegistries == nil {
		r.invocationRegistries = map[string]*agent.InvocationRegistry{}
	}
	registry := r.invocationRegistries[threadID]
	if registry == nil {
		registry = agent.NewInvocationRegistry()
		r.invocationRegistries[threadID] = registry
	}
	return registry
}

// ToolInvocation returns the live V2 tool call identified by turnID and callID
// on threadID. It fails when the call never opted in, has not been published,
// or dispatch already ended (Rust CodexThread::tool_invocation).
func (r *RuntimeRouter) ToolInvocation(threadID string, turnID string, callID string) (*agent.ToolInvocationContext, error) {
	if r == nil {
		return nil, agent.ErrToolCallNotActive
	}
	threadID = strings.TrimSpace(threadID)
	r.invocationRegistriesMu.Lock()
	registry := r.invocationRegistries[threadID]
	r.invocationRegistriesMu.Unlock()
	if registry == nil {
		return nil, agent.ErrToolCallNotActive
	}
	return registry.LookupToolInvocation(threadID, strings.TrimSpace(turnID), callID)
}

// CreateChild creates an idle V2 child here from a live invocation, using the
// host-reserved thread ID. Compatible children share the parent's exec policy
// and live bindings. Ending the invocation aborts pending startup
// (Rust CodexThread::create_child).
func (r *RuntimeRouter) CreateChild(ctx context.Context, threadID string, invocation *agent.ToolInvocationContext, request agent.ThreadCreateRequest, reservation agent.HostThreadSpawnReservation) (*agent.SpawnAgentResult, error) {
	prepared, err := r.prepareChild(ctx, threadID, invocation, request, false)
	if err != nil {
		return nil, err
	}
	return r.startPreparedChild(ctx, prepared, reservation, invocation)
}

// PrepareChild captures inputs from a live invocation so a host can start the
// child later, even after the parent shuts down. Detached preparation rejects
// managed application network, auth, cloud config, EMA-authenticated MCP and
// unresolved environment bindings that Go cannot transfer
// (Rust CodexThread::prepare_child).
func (r *RuntimeRouter) PrepareChild(ctx context.Context, threadID string, invocation *agent.ToolInvocationContext, request agent.ThreadCreateRequest) (*agent.PreparedThreadSpawn, error) {
	return r.prepareChild(ctx, threadID, invocation, request, true)
}

// StartPreparedChild starts a prepared child on this router using the
// host-reserved thread ID. On error any provisional child is cleaned up; already
// durable history may remain (Rust ThreadManager::create_prepared_child).
func (r *RuntimeRouter) StartPreparedChild(ctx context.Context, prepared *agent.PreparedThreadSpawn, reservation agent.HostThreadSpawnReservation) (*agent.SpawnAgentResult, error) {
	return r.startPreparedChild(ctx, prepared, reservation, nil)
}

func (r *RuntimeRouter) prepareChild(ctx context.Context, threadID string, invocation *agent.ToolInvocationContext, request agent.ThreadCreateRequest, detached bool) (*agent.PreparedThreadSpawn, error) {
	if r == nil {
		return nil, errors.New("agent runtime is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	threadID = strings.TrimSpace(threadID)
	if invocation == nil {
		return nil, fmt.Errorf("tool call is not active")
	}
	if invocation.ThreadID != threadID {
		return nil, fmt.Errorf("tool invocation belongs to a different runtime")
	}
	if !invocation.Active() {
		return nil, fmt.Errorf("spawning tool call finished")
	}
	params := r.activeTurnParams(threadID)
	if params == nil {
		params = &turn.TurnStartParams{ThreadID: threadID, CWD: r.childPreparationCWD(threadID)}
	}
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil {
		return nil, fmt.Errorf("cannot prepare child: %w", err)
	}
	agentsConfig, agentsErr := r.configAgentsConfig(cfg)
	if agentsErr != nil {
		return nil, agentsErr
	}
	version := r.runtimeMultiAgentVersionForTurn(threadID, cfg, agentsConfig)
	if version != agent.VersionV2 {
		return nil, fmt.Errorf("host child creation requires multi-agent v2")
	}
	if detached {
		if err := r.detachedChildRejection(cfg, params); err != nil {
			return nil, err
		}
	}
	rootID, scopePath := r.runtimeAgentIdentity(threadID)
	taskName := strings.TrimSpace(request.TaskName)
	if taskName == "" {
		taskName = "agent_" + strings.ToLower(safeIdentifier(string(newThreadID())))
	}
	request.TaskName = taskName
	agentPath := runtimeCanonicalAgentPath(scopePath, taskName)
	if rootID == "" {
		return nil, errors.New("agent runtime is unavailable")
	}
	maxThreads := 0
	if agentsConfig != nil {
		maxThreads = agentsConfig.MaxConcurrentThreadsPerSession
	}
	if cfg != nil {
		if v2Config, v2Err := cfg.MultiAgentV2Config(maxThreads); v2Err == nil && v2Config != nil {
			maxThreads = v2Config.MaxConcurrentThreadsPerSession
		}
	}
	model := strings.TrimSpace(params.Model)
	reasoningEffort := ""
	if params.Effort != nil {
		reasoningEffort = strings.TrimSpace(*params.Effort)
	}
	return &agent.PreparedThreadSpawn{
		CallerThreadID:    threadID,
		AgentPath:         agentPath,
		Request:           request,
		Version:           version,
		Model:             model,
		ReasoningEffort:   reasoningEffort,
		CWD:               firstNonEmpty(strings.TrimSpace(params.CWD), r.childPreparationCWD(threadID)),
		MaxThreads:        maxThreads,
		Environments:      cloneMapSlice(params.Environments),
		DisabledPluginIDs: r.threadDisabledPluginIDs(threadID),
	}, nil
}

func (r *RuntimeRouter) startPreparedChild(ctx context.Context, prepared *agent.PreparedThreadSpawn, reservation agent.HostThreadSpawnReservation, invocation *agent.ToolInvocationContext) (*agent.SpawnAgentResult, error) {
	if r == nil {
		return nil, errors.New("agent runtime is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if prepared == nil {
		return nil, errors.New("prepared child is unavailable")
	}
	reservedThreadID := session.ThreadID(strings.TrimSpace(reservation.ThreadID))
	if reservedThreadID == "" {
		return nil, errors.New("prepared child startup requires a reserved thread ID")
	}
	if string(reservedThreadID) == prepared.CallerThreadID {
		return nil, errors.New("reservation does not match prepared child")
	}
	if r.threads != nil && r.threads.HasLiveThread(reservedThreadID) {
		return nil, fmt.Errorf("thread %s is already running", reservedThreadID)
	}
	if invocation != nil && !invocation.Active() {
		return nil, fmt.Errorf("spawning tool call finished")
	}
	controller := newRuntimeAgentControllerForTurn(r, prepared.CallerThreadID, "", "", "", "", prepared.CWD, prepared.MaxThreads, prepared.Version, prepared.Environments)
	runtimeController, _ := controller.(*runtimeAgentController)
	if runtimeController == nil {
		return nil, errors.New("agent runtime is unavailable")
	}
	runtimeController.disabledPluginIDs = append([]string{}, prepared.DisabledPluginIDs...)
	request := prepared.Request
	// Rust's ThreadCreateRequest.fork_mode is None for a fresh child and
	// FullHistory for a fork. Go's spawn args default a nil ForkTurns to "all",
	// so a fresh host child must say "none" explicitly.
	forkTurns := request.ForkTurns
	if forkTurns == nil {
		value := "none"
		if request.ForkContext {
			value = "all"
		}
		forkTurns = &value
	}
	args := &agent.SpawnAgentArgs{
		TaskName:        request.TaskName,
		AgentType:       request.AgentType,
		Model:           request.Model,
		ReasoningEffort: request.ReasoningEffort,
		ForkContext:     request.ForkContext,
		ForkTurns:       forkTurns,
	}
	if args.Model == nil && prepared.Model != "" {
		value := prepared.Model
		args.Model = &value
	}
	if args.ReasoningEffort == nil && prepared.ReasoningEffort != "" {
		value := prepared.ReasoningEffort
		args.ReasoningEffort = &value
	}
	result, err := runtimeController.SpawnReservedAgent(ctx, args, reservedThreadID)
	if err != nil {
		return nil, err
	}
	if invocation != nil && !invocation.Active() {
		runtimeController.cleanupProvisionalSpawn(string(reservedThreadID))
		return nil, fmt.Errorf("spawning tool call finished")
	}
	return result, nil
}

// detachedChildRejection mirrors Rust prepare_spawn's detached checks: every
// binding below belongs to the source runtime and Go cannot hand it to a
// destination manager.
func (r *RuntimeRouter) detachedChildRejection(cfg *config.Config, params *turn.TurnStartParams) error {
	if cfg == nil {
		return nil
	}
	if policy := cfg.ApplicationNetworkPolicy; policy != nil && policy.IsManaged() {
		return errors.New("prepared spawn requires destination binding for host-managed application authority")
	}
	if cfg.CloudConfigBinding() != nil {
		return errors.New("prepared spawn requires destination binding for host-managed application authority")
	}
	if configHasEnabledEMAMCPServer(cfg) {
		return errors.New("prepared spawn requires destination binding for EMA-authenticated MCP servers")
	}
	if params != nil && environmentsHavePendingConfiguration(params.Environments) {
		return errors.New("prepared spawn requires resolved environment configuration")
	}
	return nil
}

func configHasEnabledEMAMCPServer(cfg *config.Config) bool {
	if cfg == nil || cfg.Values == nil {
		return false
	}
	runtimeConfig := mcp.RuntimeConfigFromValues(cfg.Values, "")
	// Rust prepare_spawn gates this check on the XAA feature and a configured
	// enterprise MCP IdP.
	if runtimeConfig == nil || runtimeConfig.EnterpriseManagedAuth == nil {
		return false
	}
	for _, registration := range runtimeConfig.Servers {
		if registration.Config.Enabled && registration.Config.Auth == config.MCPServerAuthEMAAuth {
			return true
		}
	}
	return false
}

func environmentsHavePendingConfiguration(environments []map[string]any) bool {
	for _, selection := range environments {
		state, err := environmentConfigStateFromAnyMap(selection)
		if err != nil {
			continue
		}
		if state.Kind == EnvironmentConfigPending {
			return true
		}
	}
	return false
}

func (r *RuntimeRouter) configAgentsConfig(cfg *config.Config) (*config.AgentsConfig, error) {
	if r == nil || cfg == nil {
		return nil, nil
	}
	agentsConfig, err := cfg.AgentsConfig(r.configBaseDirForAgents())
	if err != nil {
		return nil, fmt.Errorf("cannot prepare child: %w", err)
	}
	return agentsConfig, nil
}

func (r *RuntimeRouter) childPreparationCWD(threadID string) string {
	if r == nil {
		return ""
	}
	if record, err := r.threadRecord(session.ThreadID(strings.TrimSpace(threadID)), true, false); err == nil && record != nil {
		if cwd := strings.TrimSpace(record.Metadata.CWD); cwd != "" {
			return cwd
		}
	}
	return strings.TrimSpace(r.services.DefaultCWD)
}

// turnInvocationRegistry decides whether a turn opts into live tool-call
// lookup: Rust installs HostToolInvocationLookup on V2 threads and spawns
// children from them with the same opt-in (Rust #57586).
func (r *RuntimeRouter) turnInvocationRegistry(threadID string, cfg *config.Config) *agent.InvocationRegistry {
	if r == nil || cfg == nil {
		return nil
	}
	agentsConfig, err := cfg.AgentsConfig(r.configBaseDirForAgents())
	if err != nil {
		agentsConfig = nil
	}
	if r.runtimeMultiAgentVersionForTurn(threadID, cfg, agentsConfig) != agent.VersionV2 {
		return nil
	}
	return r.invocationRegistryForThread(threadID)
}
