package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"time"

	"codex_go/auth"
	"codex_go/codexapi"
	"codex_go/config"
	"codex_go/features"
	"codex_go/gitutil"
	"codex_go/install"
	"codex_go/memories"
	"codex_go/model"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/turn"
)

const internalMemorySessionSource = "internal_memory_consolidation"

type appServerMemoryStageOne struct {
	router           *RuntimeRouter
	parentThreadID   string
	parentCWD        string
	providerID       string
	originator       string
	reasoningSummary string
	serviceTier      string
	parentProfile    *sandbox.PermissionProfile
}

func (e *appServerMemoryStageOne) ExtractMemory(ctx context.Context, request memories.StageOneExtractionRequest) (memories.StageOneExtractionResponse, error) {
	if e == nil || e.router == nil {
		return memories.StageOneExtractionResponse{}, errors.New("memory extraction runtime is unavailable")
	}
	turnParams := &turn.TurnStartParams{
		ThreadID: e.parentThreadID,
		CWD:      e.parentCWD,
		Model:    request.Model,
		Config:   map[string]any{"model_provider": e.providerID},
	}
	agent := e.router.requireAgentForTurn(turnParams)
	response, err := agent.Run(ctx, &model.AgentRequest{
		Prompt:           request.Input,
		Instructions:     request.Instructions,
		Model:            request.Model,
		ProviderID:       e.providerID,
		TaskKind:         model.AgentTaskRegular,
		ThreadID:         e.parentThreadID,
		Originator:       e.originator,
		ReasoningEffort:  "low",
		ReasoningSummary: e.reasoningSummary,
		ServiceTier:      e.serviceTier,
		OutputSchema:     request.OutputSchema,
		ClientMetadata:   e.detachedClientMetadata(ctx),
	})
	if err != nil {
		return memories.StageOneExtractionResponse{}, err
	}
	text := agentResponseText(response)
	if strings.TrimSpace(text) == "" {
		return memories.StageOneExtractionResponse{}, errors.New("memory extraction returned no output")
	}
	return memories.DecodeStageOneOutput(text)
}

func (e *appServerMemoryStageOne) detachedClientMetadata(ctx context.Context) map[string]string {
	installationID := ""
	codexHome := e.router.codexHomeForRollout()
	if codexHome != "" {
		installationID, _ = install.ResolveInstallationID(codexHome)
	}
	metadata := codexapi.NewClientMetadata(installationID, e.parentThreadID, e.parentThreadID, e.parentThreadID+":0")
	metadata.RequestKind = codexapi.ClientRequestMemory
	if record, err := e.router.threadRecord(session.ThreadID(e.parentThreadID), true, false); err == nil && record != nil {
		metadata.SubagentHeader, _ = codexapi.ClientSubagentMetadataFromSource(record.Metadata.Source)
	}
	if root, workspace, ok := detachedMemoryWorkspace(ctx, e.parentCWD); ok {
		metadata.Workspaces[root] = workspace
	}
	metadata.SandboxMode = permissionProfilePolicyTagFromProfile(e.parentProfile, e.parentCWD)
	return metadata.ClientMetadata()
}

type appServerMemoryConsolidator struct {
	router         *RuntimeRouter
	providerID     string
	originator     string
	parentProfile  *sandbox.PermissionProfile
	serviceTier    *string
	serviceTierSet bool
}

func (c *appServerMemoryConsolidator) ConsolidateMemory(ctx context.Context, request memories.ConsolidationRequest) error {
	if c == nil || c.router == nil {
		return memories.NewConsolidationSpawnError(errors.New("memory consolidation runtime is unavailable"))
	}
	spawnError := func(err error) error { return memories.NewConsolidationSpawnError(err) }
	if ctx == nil {
		ctx = context.Background()
	}
	threadSource := ThreadSourceMemoryConsolidation
	serviceName := c.originator
	startParams := ThreadStartParams{
		CWD:                   request.Root,
		Model:                 request.Model,
		ModelProvider:         c.providerID,
		ApprovalPolicy:        string(sandbox.ApprovalNever),
		Config:                memoryConsolidationConfigOverrides(),
		Sandbox:               memoryConsolidationSandbox(c.parentProfile, request.Root),
		ServiceName:           stringPtrIfNotEmpty(serviceName),
		ServiceTier:           cloneString(c.serviceTier),
		ServiceTierSet:        c.serviceTierSet,
		Ephemeral:             true,
		ThreadSource:          &threadSource,
		RuntimeWorkspaceRoots: []string{request.Root},
	}
	startRequest, err := internalRequest(MethodThreadStart, "memory-consolidation-thread", &startParams)
	if err != nil {
		return spawnError(err)
	}
	response, handled, err := c.router.handleEphemeralThreadStartRuntime(startRequest)
	if err != nil {
		return spawnError(err)
	}
	if !handled || response == nil || response.Thread == nil {
		return spawnError(errors.New("failed to create memory consolidation thread"))
	}
	threadID := strings.TrimSpace(response.Thread.ID)
	c.router.internalMemoryThreads.Store(threadID, struct{}{})
	turnID := ""
	cleanup := func() error {
		return c.router.shutdownInternalMemoryThread(threadID, turnID)
	}
	defer c.router.internalMemoryThreads.Delete(threadID)

	record, err := c.router.threadRecord(session.ThreadID(threadID), true, true)
	if err != nil || record == nil {
		_ = cleanup()
		return spawnError(firstError(err, errors.New("memory consolidation thread record is unavailable")))
	}
	record.Metadata.Source = internalMemorySessionSource
	record.Metadata.ThreadSource = string(ThreadSourceMemoryConsolidation)
	record.Metadata.Originator = strings.TrimSpace(c.originator)
	if !c.router.saveEphemeralThreadRecord(record) {
		_ = cleanup()
		return spawnError(errors.New("failed to save memory consolidation thread"))
	}
	c.router.applyThreadStartConfigSnapshot(response, startRequest)
	c.router.applyThreadStartInstructionSources(response, startRequest)
	c.router.requireThreadStatus().UpsertThread(threadID, false)

	effort := firstNonEmpty(strings.TrimSpace(request.ReasoningEffort), "medium")
	turnParams := &turn.TurnStartParams{
		ThreadID:              threadID,
		Prompt:                request.Prompt,
		CWD:                   request.Root,
		Model:                 request.Model,
		Originator:            strings.TrimSpace(c.originator),
		ApprovalPolicy:        string(sandbox.ApprovalNever),
		SandboxPolicy:         startParams.Sandbox,
		RuntimeWorkspaceRoots: []string{request.Root},
		Effort:                &effort,
		Config:                memoryConsolidationConfigOverrides(),
	}
	turnRequest, err := internalRequest(MethodTurnStart, "memory-consolidation-turn", turnParams)
	if err != nil {
		_ = cleanup()
		return spawnError(err)
	}
	turnResponse, err := c.router.handleTurnStart(turnRequest)
	if err != nil {
		_ = cleanup()
		return spawnError(err)
	}
	if turnResponse == nil || strings.TrimSpace(turnResponse.Turn.ID) == "" {
		_ = cleanup()
		return spawnError(errors.New("failed to start memory consolidation turn"))
	}
	turnID = strings.TrimSpace(turnResponse.Turn.ID)
	if err := c.router.waitForInternalMemoryTurn(ctx, threadID); err != nil {
		_ = cleanup()
		return err
	}
	return cleanup()
}

type appServerMemoryRateGuard struct{ router *RuntimeRouter }

func (g appServerMemoryRateGuard) AllowMemoryStartup(ctx context.Context, minRemainingPercent int64) bool {
	if minRemainingPercent <= 0 || g.router == nil {
		return true
	}
	if ctx != nil && ctx.Err() != nil {
		return false
	}
	request, err := internalRequest(MethodGetAccountRateLimits, "memory-rate-limits", map[string]any{})
	if err != nil {
		return true
	}
	response, err := g.router.handleGetAccountRateLimits(request)
	if err != nil || response == nil {
		return true
	}
	snapshot := response.RateLimits
	if selected, ok := response.RateLimitsByLimitID["codex"]; ok {
		snapshot = selected
	}
	return memoryRateLimitAllows(snapshot, minRemainingPercent)
}

func (r *RuntimeRouter) startMemoriesStartupTask(response *ThreadStartResponse, request *Request) {
	if r == nil || response == nil || response.Thread == nil || request == nil || r.services.StateRuntime == nil {
		return
	}
	var params ThreadStartParams
	if request.DecodeParams(&params) != nil || params.Ephemeral {
		return
	}
	record, err := r.threadRecord(session.ThreadID(response.Thread.ID), true, false)
	if err != nil || record == nil || memoryStartupNonRootRecord(record) {
		return
	}
	cfg, err := r.effectiveConfigForThreadStart(&params)
	if err != nil || cfg == nil || !features.Enabled(cfg.FeatureSettings(), "memories") {
		return
	}
	providerID := firstNonEmpty(strings.TrimSpace(params.ModelProvider), stringConfigValue(cfg, "model_provider"), model.OpenAIProviderID)
	provider := memoryRuntimeProvider(cfg, providerID)
	memoryConfig := cfg.Memories()
	extractModel := model.DefaultMemoryExtractionPreferredModel
	consolidationModel := model.DefaultMemoryConsolidationPreferredModel
	if provider != nil {
		extractModel = provider.MemoryExtractionPreferredModel()
		consolidationModel = provider.MemoryConsolidationPreferredModel()
	}
	if memoryConfig.ExtractModel != nil {
		extractModel = strings.TrimSpace(*memoryConfig.ExtractModel)
	}
	if memoryConfig.ConsolidationModel != nil {
		consolidationModel = strings.TrimSpace(*memoryConfig.ConsolidationModel)
	}
	modelInfo := model.ModelInfo{Slug: extractModel}
	if info := r.requireModels().Info(&model.ModelInfoReadParams{Model: extractModel}); info != nil {
		modelInfo = *info
	}
	reasoningSummary := firstNonEmpty(stringConfigValue(cfg, "model_reasoning_summary"), modelInfo.DefaultReasoningSummary)
	serviceTier := strings.TrimSpace(record.Metadata.ServiceTier)
	parentProfile := memoryParentPermissionProfile(cfg, record.Metadata.CWD, &params)
	pipeline := &memories.StartupPipeline{
		State:             r.services.StateRuntime,
		CodexHome:         r.codexHomeForRollout(),
		CurrentThreadID:   response.Thread.ID,
		Config:            memoryConfig,
		StageOneModel:     extractModel,
		StageOneModelInfo: modelInfo,
		PhaseTwoModel:     consolidationModel,
		Guard:             appServerMemoryRateGuard{router: r},
		StageOne: &appServerMemoryStageOne{
			router: r, parentThreadID: response.Thread.ID, parentCWD: record.Metadata.CWD,
			providerID: providerID, originator: record.Metadata.Originator,
			reasoningSummary: reasoningSummary, serviceTier: serviceTier,
			parentProfile: parentProfile,
		},
		PhaseTwo: &appServerMemoryConsolidator{
			router: r, providerID: providerID, originator: record.Metadata.Originator,
			parentProfile: parentProfile, serviceTier: stringPtrIfNotEmpty(serviceTier),
			serviceTierSet: serviceTier != "",
		},
	}
	ctx := r.memoryStartupCtx
	if ctx == nil {
		ctx = context.Background()
	}
	r.memoryStartupMu.Lock()
	if r.memoryStartupClosing || r.threads.IsClosing() || ctx.Err() != nil {
		r.memoryStartupMu.Unlock()
		return
	}
	r.memoryStartupWG.Add(1)
	r.memoryStartupMu.Unlock()
	go func() {
		defer r.memoryStartupWG.Done()
		report, runErr := pipeline.Run(ctx)
		if runErr != nil && ctx.Err() == nil {
			slog.Warn("memories startup pipeline failed", "thread_id", response.Thread.ID, "error", runErr)
			return
		}
		slog.Debug("memories startup pipeline finished",
			"thread_id", response.Thread.ID,
			"stage_one_claimed", report.StageOneClaimed,
			"stage_one_succeeded", report.StageOneSucceeded,
			"stage_one_no_output", report.StageOneSucceededEmpty,
			"stage_one_failed", report.StageOneFailed,
			"phase_two_status", report.PhaseTwoStatus)
	}()
}

func (r *RuntimeRouter) waitForInternalMemoryTurn(ctx context.Context, threadID string) error {
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if r.threads.ActiveTurn(threadID) == nil {
			switch r.requireThreadStatus().LoadedStatusForThread(threadID).Type {
			case "idle":
				return nil
			case "systemError":
				return errors.New("memory consolidation agent failed")
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (r *RuntimeRouter) shutdownInternalMemoryThread(threadID, turnID string) error {
	threadID = strings.TrimSpace(threadID)
	if r == nil || threadID == "" {
		return nil
	}
	if active := r.threads.ActiveTurn(threadID); active != nil {
		if active.Cancel != nil {
			active.Cancel()
		}
		activeTurnID := firstNonEmpty(strings.TrimSpace(turnID), strings.TrimSpace(active.TurnID))
		if activeTurnID != "" {
			_, _ = r.requireTurns().Interrupt(&turn.TurnInterruptParams{ThreadID: threadID, TurnID: activeTurnID})
		}
		deadline := time.Now().Add(10 * time.Second)
		for r.threads.ActiveTurn(threadID) != nil && time.Now().Before(deadline) {
			time.Sleep(25 * time.Millisecond)
		}
		if r.threads.ActiveTurn(threadID) != nil {
			return fmt.Errorf("memory consolidation agent %s shutdown timed out", threadID)
		}
	}
	r.threads.DeleteEphemeralRecord(session.ThreadID(threadID))
	r.threads.ClearThread(threadID)
	r.requireThreadStatus().RemoveThread(threadID)
	if r.mcpRuntimes != nil {
		r.mcpRuntimes.invalidateThread(threadID)
	}
	return nil
}

func (r *RuntimeRouter) isInternalMemoryNotification(params any) bool {
	threadID := notificationThreadID(params)
	if threadID == "" {
		return false
	}
	_, ok := r.internalMemoryThreads.Load(threadID)
	return ok
}

func notificationThreadID(params any) string {
	if params == nil {
		return ""
	}
	value := reflect.ValueOf(params)
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return ""
		}
		value = value.Elem()
	}
	if !value.IsValid() || value.Kind() != reflect.Struct {
		return ""
	}
	field := value.FieldByName("ThreadID")
	for field.IsValid() && (field.Kind() == reflect.Pointer || field.Kind() == reflect.Interface) {
		if field.IsNil() {
			return ""
		}
		field = field.Elem()
	}
	if field.IsValid() && field.Kind() == reflect.String {
		return strings.TrimSpace(field.String())
	}
	return ""
}

func memoryRuntimeProvider(cfg *config.Config, providerID string) model.RuntimeProvider {
	providerInfo, err := model.ProviderForConfigID(configValues(cfg), providerID, stringConfigValue(cfg, "openai_base_url"))
	if err != nil || providerInfo == nil {
		return nil
	}
	return model.CreateRuntimeProviderForID(providerID, *providerInfo, nil)
}

func memoryParentPermissionProfile(cfg *config.Config, cwd string, params *ThreadStartParams) *sandbox.PermissionProfile {
	turnParams := &turn.TurnStartParams{CWD: cwd}
	if params != nil {
		turnParams.Permissions = cloneString(params.Permissions)
		turnParams.SandboxPolicy = params.Sandbox
	}
	resolution, err := turnSandboxPermissionProfile(cfg, cwd, turnParams)
	if err != nil || resolution == nil || resolution.Profile == nil {
		profile := sandbox.WorkspaceWritePermissionProfile()
		return &profile
	}
	profile := *resolution.Profile
	return &profile
}

func memoryConsolidationSandbox(parent *sandbox.PermissionProfile, root string) *sandbox.SandboxPolicy {
	if parent != nil {
		if parent.Disabled {
			return sandbox.NewDangerFullAccessPolicy()
		}
		if parent.SandboxPolicy != nil && parent.SandboxPolicy.Kind == "external-sandbox" {
			network := parent.SandboxPolicy.ExternalNetwork
			if network == "" {
				network = sandbox.NetworkRestricted
			}
			return sandbox.NewExternalSandboxPolicy(network)
		}
	}
	return &sandbox.SandboxPolicy{
		Kind:                sandbox.SandboxWorkspaceWrite,
		WritableRoots:       []string{root},
		NetworkAccess:       false,
		ExcludeTmpdirEnvVar: true,
		ExcludeSlashTmp:     true,
	}
}

func memoryConsolidationConfigOverrides() map[string]any {
	return map[string]any{
		"include_apps_instructions": false,
		"mcp_servers":               map[string]any{},
		"memories": map[string]any{
			"generate_memories": false,
			"use_memories":      false,
		},
		"features": map[string]any{
			"memories":                     false,
			"multi_agent":                  false,
			"multi_agent_v2":               false,
			"apps":                         false,
			"enable_mcp_apps":              false,
			"plugins":                      false,
			"skill_mcp_dependency_install": false,
		},
	}
}

func memoryStartupNonRootRecord(record *session.Record) bool {
	if record == nil {
		return true
	}
	source := strings.ToLower(strings.TrimSpace(record.Metadata.Source))
	if strings.HasPrefix(source, "internal_") || strings.HasPrefix(source, "internal:") || runtimeSessionSourceIsSubagent(source) {
		return true
	}
	threadSource := strings.ToLower(strings.TrimSpace(record.Metadata.ThreadSource))
	return threadSource == string(ThreadSourceSubagent) || threadSource == string(ThreadSourceMemoryConsolidation)
}

func memoryRateLimitAllows(snapshot auth.RateLimitSnapshot, minRemainingPercent int64) bool {
	if snapshot.RateLimitReachedType != nil {
		return false
	}
	if minRemainingPercent < 0 {
		minRemainingPercent = 0
	}
	if minRemainingPercent > 100 {
		minRemainingPercent = 100
	}
	maxUsed := int32(100 - minRemainingPercent)
	return (snapshot.Primary == nil || snapshot.Primary.UsedPercent <= maxUsed) &&
		(snapshot.Secondary == nil || snapshot.Secondary.UsedPercent <= maxUsed)
}

func internalRequest(method Method, id string, params any) (*Request, error) {
	data, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}
	return &Request{JSONRPC: "2.0", ID: StringID(id), Method: method, Params: data}, nil
}

func agentResponseText(response *model.AgentResponse) string {
	if response == nil {
		return ""
	}
	if strings.TrimSpace(response.Message) != "" {
		return response.Message
	}
	var text strings.Builder
	for _, item := range response.Items {
		if item.Type != "" && item.Type != "agent_message" && item.Type != "message" {
			continue
		}
		text.WriteString(item.Text)
	}
	return text.String()
}

func detachedMemoryWorkspace(ctx context.Context, cwd string) (string, codexapi.ClientWorkspaceMetadata, bool) {
	cwd = strings.TrimSpace(cwd)
	if cwd == "" {
		return "", codexapi.ClientWorkspaceMetadata{}, false
	}
	run := func(args ...string) (string, error) {
		// Rust 3149fa4b99: git metadata commands must terminate the whole
		// process tree on timeout, not just the direct process.
		output, err := gitutil.RunWithTimeout(ctx, 2*time.Second, cwd, args...)
		return strings.TrimSpace(output), err
	}
	root, err := run("rev-parse", "--show-toplevel")
	if err != nil || root == "" {
		return "", codexapi.ClientWorkspaceMetadata{}, false
	}
	root, _ = filepath.Abs(root)
	workspace := codexapi.ClientWorkspaceMetadata{}
	if head, headErr := run("rev-parse", "HEAD"); headErr == nil {
		workspace.LatestGitCommitHash = head
	}
	// Rust b6c3b51533 (#37151): coalesce concurrent `git status --porcelain`
	// scans by the canonical repository root so sibling/symlink-alias
	// metadata requests share a single in-flight invocation.
	if hasChanges, statusErr := gitutil.HasChangesInRepo(ctx, cwd, root); statusErr == nil {
		workspace.HasChanges = &hasChanges
	}
	if remotes, remoteErr := run("remote"); remoteErr == nil && remotes != "" {
		workspace.AssociatedRemoteURLs = map[string]string{}
		for _, name := range strings.Fields(remotes) {
			if url, urlErr := run("remote", "get-url", name); urlErr == nil && url != "" {
				workspace.AssociatedRemoteURLs[name] = url
			}
		}
		if len(workspace.AssociatedRemoteURLs) == 0 {
			workspace.AssociatedRemoteURLs = nil
		}
	}
	return root, workspace, true
}

func waitForWaitGroup(group *sync.WaitGroup, timeout time.Duration) error {
	if group == nil {
		return nil
	}
	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-time.After(timeout):
		return errors.New("timed out waiting for memories startup tasks")
	}
}

func firstError(primary, fallback error) error {
	if primary != nil {
		return primary
	}
	return fallback
}
