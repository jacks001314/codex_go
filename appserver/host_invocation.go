package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"codex_go/agent"
	"codex_go/config"
	"codex_go/execpolicy"
	"codex_go/features"
	"codex_go/sandbox"
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
// (Rust CodexThread::create_child). Reserved startup always happens on the
// invoking thread's manager, so there is no prepared-start entry point
// (Rust #58390 removed ThreadManager::create_prepared_child).
func (r *RuntimeRouter) CreateChild(ctx context.Context, threadID string, invocation *agent.ToolInvocationContext, request agent.ThreadCreateRequest, reservation agent.HostThreadSpawnReservation) (*agent.SpawnAgentResult, error) {
	created, err := r.prepareChildContext(ctx, threadID, invocation)
	if err != nil {
		return nil, err
	}
	return r.createReservedChild(ctx, threadID, created, request, reservation, invocation)
}

// PrepareChild captures the parent inputs from a live invocation so a host can
// start or fork a child with its own destination services. Hosts choose the
// child's name, role, and history and apply the requested child settings
// (Rust CodexThread::prepare_child).
func (r *RuntimeRouter) PrepareChild(ctx context.Context, threadID string, invocation *agent.ToolInvocationContext) (*agent.PreparedChildContext, error) {
	return r.prepareChildContext(ctx, threadID, invocation)
}

// prepareChildContext captures the parent inputs a host needs before choosing a
// child request, keeping only the invocation-lifetime checks of Rust's
// with_live_invocation.
func (r *RuntimeRouter) prepareChildContext(ctx context.Context, threadID string, invocation *agent.ToolInvocationContext) (*agent.PreparedChildContext, error) {
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
	cwd := firstNonEmpty(strings.TrimSpace(params.CWD), r.childPreparationCWD(threadID))
	instructions := ""
	if record, recordErr := r.threadRecord(session.ThreadID(threadID), true, false); recordErr == nil && record != nil {
		instructions = record.Metadata.Instructions
	}
	return &agent.PreparedChildContext{
		Originator:                   strings.TrimSpace(params.Originator),
		UserInstructions:             instructions,
		CWD:                          cwd,
		Environments:                 cloneMapSlice(params.Environments),
		SelectedCapabilityRoots:      preparedChildCapabilityRoots(r.readySelectedCapabilityRootsForStep(threadID, params)),
		ResolvedPermissionProfile:    r.preparedChildPermissionProfile(cfg, cwd, params),
		ApprovalPolicy:               string(turnApprovalPolicyForTurn(cfg, params)),
		ApprovalsReviewer:            stringPtrValue(params.ApprovalsReviewer),
		ExecPolicy:                   r.managedCommandPolicyForThread(threadID),
		ExecPolicySourceRequirements: r.managedExecPolicyRequirements(),
		DynamicTools:                 r.inheritedChildDynamicTools(threadID, cfg),
		DisabledPluginIDs:            r.threadDisabledPluginIDs(threadID),
	}, nil
}

// preparedChildCapabilityRoots projects the step's roots into the host-facing
// prepared context.
func preparedChildCapabilityRoots(roots []SelectedCapabilityRoot) []agent.PreparedChildCapabilityRoot {
	if len(roots) == 0 {
		return nil
	}
	out := make([]agent.PreparedChildCapabilityRoot, 0, len(roots))
	for _, root := range roots {
		out = append(out, agent.PreparedChildCapabilityRoot{
			ID:            root.ID,
			EnvironmentID: root.Location.EnvironmentID,
			Path:          root.Location.Path,
		})
	}
	return out
}

func (r *RuntimeRouter) preparedChildPermissionProfile(cfg *config.Config, cwd string, params *turn.TurnStartParams) *sandbox.PermissionProfile {
	resolution, err := turnSandboxPermissionProfile(cfg, cwd, params)
	if err != nil || resolution == nil || resolution.Profile == nil {
		return nil
	}
	profile := *resolution.Profile
	return &profile
}

// managedExecPolicyRequirements returns the managed `[rules]` requirement used
// to load the session policy (Rust ExecPolicyManager source requirements).
// Go reloads it from the host configuration, so it survives per-thread MCP
// configuration refreshes that no longer carry the requirement.
func (r *RuntimeRouter) managedExecPolicyRequirements() *execpolicy.RequirementsPolicy {
	if r == nil || r.services.Config == nil {
		return nil
	}
	read := r.services.Config.Requirements()
	if read == nil || read.Requirements == nil {
		return nil
	}
	return read.Requirements.Rules
}

// inheritedChildDynamicTools copies the live parent declarations a fresh V2
// child may inherit (Rust inherited_dynamic_tools).
func (r *RuntimeRouter) inheritedChildDynamicTools(threadID string, cfg *config.Config) []json.RawMessage {
	if r == nil || cfg == nil || !features.Enabled(cfg.FeatureSettings(), "multi_agent_v2_dynamic_tools") {
		return nil
	}
	agentsConfig, _ := r.configAgentsConfig(cfg)
	if r.runtimeMultiAgentVersionForTurn(threadID, cfg, agentsConfig) != agent.VersionV2 {
		return nil
	}
	record, err := r.threadRecord(session.ThreadID(threadID), true, false)
	if err != nil || record == nil {
		return nil
	}
	return cloneRawMessages(record.Metadata.DynamicTools)
}

func (r *RuntimeRouter) createReservedChild(ctx context.Context, threadID string, created *agent.PreparedChildContext, request agent.ThreadCreateRequest, reservation agent.HostThreadSpawnReservation, invocation *agent.ToolInvocationContext) (*agent.SpawnAgentResult, error) {
	if r == nil {
		return nil, errors.New("agent runtime is unavailable")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if created == nil {
		return nil, errors.New("prepared child context is unavailable")
	}
	callerThreadID := strings.TrimSpace(threadID)
	reservedThreadID := session.ThreadID(strings.TrimSpace(reservation.ThreadID))
	if reservedThreadID == "" {
		return nil, errors.New("prepared child startup requires a reserved thread ID")
	}
	if string(reservedThreadID) == callerThreadID {
		return nil, errors.New("reservation does not match prepared child")
	}
	if r.threads != nil && r.threads.HasLiveThread(reservedThreadID) {
		return nil, fmt.Errorf("thread %s is already running", reservedThreadID)
	}
	if invocation != nil && !invocation.Active() {
		return nil, fmt.Errorf("spawning tool call finished")
	}
	params := r.activeTurnParams(callerThreadID)
	if params == nil {
		params = &turn.TurnStartParams{ThreadID: callerThreadID, CWD: created.CWD}
	}
	cfg, err := r.effectiveConfigForTurn(params)
	if err != nil {
		return nil, fmt.Errorf("cannot prepare child: %w", err)
	}
	agentsConfig, agentsErr := r.configAgentsConfig(cfg)
	if agentsErr != nil {
		return nil, agentsErr
	}
	version := r.runtimeMultiAgentVersionForTurn(callerThreadID, cfg, agentsConfig)
	if version != agent.VersionV2 {
		return nil, fmt.Errorf("host child creation requires multi-agent v2")
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
	controller := newRuntimeAgentControllerForTurn(r, callerThreadID, "", "", "", "", created.CWD, maxThreads, version, created.Environments)
	runtimeController, _ := controller.(*runtimeAgentController)
	if runtimeController == nil {
		return nil, errors.New("agent runtime is unavailable")
	}
	runtimeController.disabledPluginIDs = append([]string{}, created.DisabledPluginIDs...)
	taskName := strings.TrimSpace(request.TaskName)
	if taskName == "" {
		taskName = "agent_" + strings.ToLower(safeIdentifier(string(newThreadID())))
	}
	request.TaskName = taskName
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
	if args.Model == nil && strings.TrimSpace(params.Model) != "" {
		value := strings.TrimSpace(params.Model)
		args.Model = &value
	}
	if args.ReasoningEffort == nil && params.Effort != nil && strings.TrimSpace(*params.Effort) != "" {
		value := strings.TrimSpace(*params.Effort)
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
