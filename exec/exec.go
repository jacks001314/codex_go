package exec

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	multiagent "codex_go/agent"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/codemode"
	"codex_go/codexapi"
	"codex_go/compact"
	"codex_go/config"
	"codex_go/doctor"
	"codex_go/eventmap"
	"codex_go/features"
	"codex_go/install"
	"codex_go/mcp"
	"codex_go/model"
	"codex_go/network"
	"codex_go/prompt"
	"codex_go/protocol"
	"codex_go/review"
	"codex_go/rollout"
	"codex_go/sandbox"
	"codex_go/session"
	"codex_go/tool"
	"codex_go/turn"

	"github.com/google/uuid"
)

type Request struct {
	Root                   cli.RootOptions
	Exec                   cli.ExecOptions
	Input                  []turn.TurnUserInput
	CollaborationMode      map[string]any
	AdditionalInstructions string
	AdditionalInputItems   []any
	// InternalEventHandler receives the complete in-process event before its
	// public JSON representation is encoded. Local UI consumers use this to
	// retain non-wire details such as file-change diffs while the SDK JSON shape
	// remains Rust-compatible.
	InternalEventHandler func(protocol.ThreadEvent)
	SteerMailbox         *turn.SteerMailbox
	OnTurnStarted        func(threadID string, turnID string)
	OnSteerCommitted     func(count int)
	subagent             *execSubagentContext
	multiAgentVersion    multiagent.MultiAgentVersion
}

// execCompactionWarningMessage mirrors the Rust core compaction warning event
// text so the SDK/exec JSON stream emits the same error item after a
// compaction turn.
const execCompactionWarningMessage = "Heads up: Long threads and multiple compactions can cause the model to be less accurate. Start a new thread when possible to keep threads small and targeted."

type Result struct {
	ThreadID    string
	TurnID      string
	SessionPath string
	LastMessage string
	Prompt      string
	Events      []protocol.ThreadEvent
	TokenUsage  *protocol.ThreadTokenUsage
}

type Runner struct {
	CodexHome        string
	Agent            model.AgentRunner
	ToolRouter       *tool.Router
	UnifiedExec      *tool.UnifiedExecManager
	Hooks            tool.HookRunner
	ShellApproval    tool.ShellApprovalFunc
	UserInput        tool.UserInputResponder
	MCPService       *mcp.MCPService
	MCPTools         []mcp.RuntimeToolInfo
	MCPConnectors    []mcp.RuntimeConnector
	MCPElicitation   mcp.MCPElicitationHandler
	CodeModeProvider tool.CodeModeRemoteProvider
	MaxToolTurns     int
	UseResponsesAPI  bool
	HTTPClient       model.HTTPDoer
	Now              func() time.Time

	goalMu       *sync.Mutex
	goalThreadID string
	goalTurnID   string
}

func NewRunner(codexHome string) *Runner {
	return &Runner{
		CodexHome:       codexHome,
		UseResponsesAPI: true,
		Now:             time.Now,
		UnifiedExec:     tool.NewUnifiedExecManager(),
		goalMu:          &sync.Mutex{},
	}
}

func NewLocalRunner(codexHome string) *Runner {
	runner := NewRunner(codexHome)
	runner.UseResponsesAPI = false
	return runner
}

func (r *Runner) Run(req Request, stdin io.Reader, stdout, stderr io.Writer) (*Result, error) {
	return r.RunContext(context.Background(), &req, stdin, stdout, stderr)
}

func (r *Runner) RunContext(ctx context.Context, req *Request, stdin io.Reader, stdout, stderr io.Writer) (*Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if r == nil {
		return nil, errors.New("exec runner is nil")
	}
	if req == nil {
		return nil, errors.New("exec request is nil")
	}
	if req.Exec.Subcommand != "" && req.Exec.Subcommand != "review" && req.Exec.Subcommand != "resume" && req.Exec.Subcommand != "fork" {
		return nil, fmt.Errorf("unknown exec subcommand %s", req.Exec.Subcommand)
	}
	if err := validateExecWorkingDirectory(requestCWD(req)); err != nil {
		return nil, err
	}
	cfg, err := config.LoadEffective(
		r.CodexHome,
		mergedOverrides(req.Root.ConfigOverrides, req.Exec.ConfigOverrides),
		req.Root.EnableFeatures,
		req.Root.DisableFeatures,
		requestCWD(req),
	)
	if err != nil {
		return nil, err
	}
	authStoreOptions := auth.StoreOptionsFromConfig(cfg.CLIAuthCredentialsStoreMode(), cfg.SecretAuthStorageEnabled())
	resolvedAuth, err := auth.NewStoreWithOptions(r.CodexHome, authStoreOptions).Resolve()
	if err != nil {
		return nil, err
	}
	instructions, err := baseInstructionsForRequest(req, cfg)
	if err != nil {
		return nil, err
	}
	if extra := strings.TrimSpace(req.AdditionalInstructions); extra != "" {
		instructions = strings.Join(nonEmptyStrings([]string{extra, instructions}), "\n\n")
	}
	var outputSchema any
	if !execForkOnlyRequest(req) {
		outputSchema, err = loadOutputSchema(req.Exec.OutputSchema)
		if err != nil {
			return nil, err
		}
	}

	prompt, resumeContext, err := r.promptForRequest(req, cfg, stdin)
	if err != nil {
		return nil, err
	}
	requestInputs := requestTurnUserInputs(req)
	if strings.TrimSpace(prompt) == "" && len(requestInputs) == 0 && len(req.AdditionalInputItems) == 0 &&
		(resumeContext == nil || !resumeContext.ForkOnly) {
		return nil, errors.New("No prompt provided. Either specify one as an argument or pipe the prompt into stdin.")
	}
	identityPrompt := firstNonEmpty(prompt, turnUserInputsSummary(requestInputs), execAdditionalInputIdentity(req.AdditionalInputItems))

	threadID := newThreadID()
	if req.subagent != nil && strings.TrimSpace(req.subagent.ThreadID) != "" {
		threadID = strings.TrimSpace(req.subagent.ThreadID)
	}
	if resumeContext != nil && resumeContext.Record != nil {
		threadID = string(resumeContext.Record.ID)
	}
	turnID := deterministicTurnID(identityPrompt)
	installationID := ""
	if codexHome := strings.TrimSpace(r.CodexHome); codexHome != "" {
		installationID, _ = install.ResolveInstallationID(codexHome)
	}
	modelID := effectiveModel(req, cfg)
	modelInfo := execModelInfo(modelID, cfg)
	nodeReplAutoReviewRequired := modelInfo.NodeReplAutoReviewRequired
	nodeReplDisabled := modelInfo.NodeReplDisabled
	parallelToolCalls := modelSupportsParallelToolCalls(modelID)
	useResponsesLite := modelUsesResponsesLite(modelID)
	reasoningEffort := effectiveReasoningEffort(req, cfg)
	concurrentReasoningSummaries := features.Enabled(cfg.FeatureSettings(), "concurrent_reasoning_summaries")
	modelVerbosity := effectiveModelVerbosity(cfg)
	includeTimingMetrics := effectiveIncludeTimingMetrics(cfg)
	betaFeaturesHeader := features.ModelClientBetaFeaturesHeader(cfg.FeatureSettings())
	itemIDsEnabled := cfg.FeatureSettings()["item_ids"]
	serviceTier := effectiveServiceTier(cfg, modelID)
	providerID, err := effectiveProvider(req, cfg)
	if err != nil {
		return nil, err
	}
	permissionProfile, err := resolveExecSandboxPermissionProfile(cfg, req)
	if err != nil {
		return nil, err
	}
	approvalPolicy := effectiveExecApprovalPolicy(cfg, req)
	taskKind := taskKind(req)
	agent, err := r.agentForRun(cfg, resolvedAuth, providerID, authStoreOptions)
	if err != nil {
		return nil, err
	}
	if !req.Exec.JSON && stderr != nil {
		writeHumanConfigSummary(stderr, req, cfg, identityPrompt, threadID, modelID, providerID, approvalPolicy, permissionProfile, reasoningEffort)
	}
	eventSink := newExecEventSink(stdout, req.Exec.JSON)
	eventSink.internalHandler = req.InternalEventHandler
	if !req.Exec.JSON && stderr != nil {
		eventSink.human = newExecHumanRenderer(stderr, execColorFlagValue(req.Exec))
	}
	if err := eventSink.Emit(protocol.ThreadStarted(threadID)); err != nil {
		return nil, err
	}
	if resumeContext != nil && resumeContext.ForkOnly {
		sessionPath, pathErr := session.NewStore(filepath.Join(r.CodexHome, "sessions")).Path(session.ThreadID(threadID))
		if pathErr != nil && !errors.Is(pathErr, session.ErrThreadNotFound) {
			return nil, pathErr
		}
		return &Result{ThreadID: threadID, SessionPath: sessionPath, Events: eventSink.Events()}, nil
	}
	for index, usage := range cfg.LegacyFeatureUsages() {
		notice := features.NoticeForLegacyFeatureUsage(usage)
		if err := eventSink.Emit(protocol.ItemCompleted(protocol.ErrorItem(fmt.Sprintf("item_%d", index), notice.Message()))); err != nil {
			return nil, err
		}
	}
	if req.OnTurnStarted != nil {
		req.OnTurnStarted(threadID, turnID)
	}
	if err := eventSink.Emit(protocol.TurnStarted()); err != nil {
		return nil, err
	}
	compactedThisTurn := false
	if err := r.persistSessionTurnStart(req, threadID, turnID, prompt, requestInputs, resumeContext, modelID, providerID); err != nil {
		return nil, err
	}
	if resumeContext != nil && resumeContext.Record != nil {
		var err error
		compactedThisTurn, err = r.compactResumeBeforeTurn(ctx, resumeContext, threadID, turnID, modelID, providerID, cfg, agent, eventSink)
		if err != nil {
			_ = eventSink.Emit(protocol.ErrorEvent(err.Error()))
			_ = eventSink.Emit(protocol.TurnFailed(err.Error()))
			return nil, err
		}
	}
	runPrompt := prompt
	execStartupItems := execStartupInputItems(req, permissionProfile, approvalPolicy, r.now())
	inputItems := append([]any(nil), execStartupItems...)
	inputItems = append(inputItems, resumeInputItems(resumeContext)...)
	inputItems = append(inputItems, req.AdditionalInputItems...)
	if len(requestInputs) > 0 {
		if item := userMessageInputItemFromTurnInputs(prompt, requestInputs, requestCWD(req)); item != nil {
			inputItems = append(inputItems, item)
		}
		if _, local := agent.(*model.LocalAgentRunner); local {
			runPrompt = firstNonEmpty(prompt, turnUserInputsSummary(requestInputs))
		} else {
			runPrompt = ""
		}
	}
	streamCollector := &execStreamEventCollector{sink: eventSink, workingDirectory: requestCWD(req)}
	streamCollector.streamAssistantDeltas = req.Exec.StreamAssistantDeltas
	mcpService, mcpTools, mcpConnectors := r.configuredMCPRuntimeForConfig(cfg, resolvedAuth)
	var webSearchOptions *turn.WebSearchOptions
	var imageGenerationOptions *turn.ImageGenerationOptions
	var hostedTools []any
	if req.Exec.Subcommand != "review" {
		webSearchInputItems := append([]any(nil), inputItems...)
		if current := model.UserMessageInputItem(runPrompt); current != nil {
			webSearchInputItems = append(webSearchInputItems, current)
		}
		webSearchOptions, err = r.webSearchOptionsForRun(cfg, resolvedAuth, providerID, modelID, threadID, webSearchInputItems, req.Root.Shared.Search)
		if err != nil {
			_ = eventSink.Emit(protocol.ErrorEvent(err.Error()))
			_ = eventSink.Emit(protocol.TurnFailed(err.Error()))
			return nil, err
		}
		imageGenerationOptions, err = r.imageGenerationOptionsForRun(cfg, resolvedAuth, providerID, modelID, threadID, inputItems)
		if err != nil {
			_ = eventSink.Emit(protocol.ErrorEvent(err.Error()))
			_ = eventSink.Emit(protocol.TurnFailed(err.Error()))
			return nil, err
		}
		hostedTools, err = r.hostedToolsForRun(cfg, resolvedAuth, providerID, modelID, req.Root.Shared.Search, webSearchOptions, imageGenerationOptions)
		if err != nil {
			_ = eventSink.Emit(protocol.ErrorEvent(err.Error()))
			_ = eventSink.Emit(protocol.TurnFailed(err.Error()))
			return nil, err
		}
	}
	subagentHeader, subagentKind := execReviewSubagentMetadata(req)
	multiAgentTools, err := r.multiAgentToolsForRun(ctx, req, cfg, threadID, turnID, agent)
	if err != nil {
		_ = eventSink.Emit(protocol.ErrorEvent(err.Error()))
		_ = eventSink.Emit(protocol.TurnFailed(err.Error()))
		return nil, err
	}
	defer closeExecMultiAgentTools(multiAgentTools)
	if usageHint := execMultiAgentV2UsageHint(req, multiAgentTools); usageHint != "" {
		instructions = strings.Join(nonEmptyStrings([]string{instructions, usageHint}), "\n\n")
	}
	agentWaitDefault, agentWaitMin, agentWaitMax, agentHideSpawnMetadata, agentExposeSpawnOverrides := execAgentWaitConfigFromTools(multiAgentTools)
	clientMetadata := turn.BuildResponsesClientMetadata(&turn.ResponsesClientMetadataOptions{
		InstallationID:             installationID,
		SessionID:                  execSessionID(req, threadID),
		ThreadID:                   threadID,
		TurnID:                     turnID,
		WindowID:                   threadID + ":1",
		RequestKind:                codexapi.ClientRequestTurn,
		SubagentHeader:             subagentHeader,
		SubagentKind:               subagentKind,
		ParentThreadID:             execParentThreadID(req),
		ThreadSource:               execThreadSource(req),
		NodeReplAutoReviewRequired: &nodeReplAutoReviewRequired,
		NodeReplDisabled:           &nodeReplDisabled,
		Extra:                      cfg.ResponsesAPIClientMetadata(),
		UseResponsesLite:           useResponsesLite,
	})
	originator := execAgentOriginator(req)
	if webSearchOptions != nil {
		webSearchOptions.Originator = originator
		webSearchOptions.TurnMetadata = clientMetadata[codexapi.ClientCodexTurnMetadataHeader]
	}
	turnResult, err := r.runAgentTurn(ctx, req, agent, &agentRunConfig{
		Config:                         cfg,
		Prompt:                         runPrompt,
		InputItems:                     inputItems,
		Model:                          modelID,
		ToolMode:                       model.ResolveToolMode(modelInfo.ToolMode, cfg.FeatureSettings()),
		CodeModeHostEnabled:            features.Enabled(cfg.FeatureSettings(), "code_mode_host"),
		DisableCodeModeFallback:        cfg.DisableCodeModeInProcessFallback(),
		CodeModeDefaultExecYieldTime:   cfg.CodeModeDefaultExecYieldTime(),
		ProviderID:                     providerID,
		TaskKind:                       taskKind,
		ThreadID:                       threadID,
		TurnID:                         turnID,
		Originator:                     originator,
		PreviousResponseID:             resumePreviousResponseID(resumeContext),
		ParallelToolCalls:              parallelToolCalls,
		ReasoningEffort:                reasoningEffort,
		ConcurrentReasoningSummaries:   concurrentReasoningSummaries,
		ModelVerbosity:                 modelVerbosity,
		IncludeTimingMetrics:           includeTimingMetrics,
		BetaFeaturesHeader:             betaFeaturesHeader,
		ItemIDsEnabled:                 itemIDsEnabled,
		PromptCacheKey:                 threadID,
		ServiceTier:                    serviceTier,
		Instructions:                   instructions,
		ClientMetadata:                 clientMetadata,
		OutputSchema:                   outputSchema,
		ApprovalPolicy:                 approvalPolicy,
		StreamEvents:                   streamCollector,
		PermissionProfileID:            sandboxPermissionProfileID(permissionProfile),
		PermissionProfile:              sandboxPermissionProfile(permissionProfile),
		MCPService:                     mcpService,
		MCPTools:                       mcpTools,
		MCPConnectors:                  mcpConnectors,
		WebSearch:                      webSearchOptions,
		ImageGeneration:                imageGenerationOptions,
		ViewImage:                      execViewImageOptions(requestCWD(req), &modelInfo),
		HostedTools:                    hostedTools,
		DisableHostedImageGeneration:   req.Exec.Subcommand == "review",
		ToolOutputTokenLimit:           cfg.ToolOutputTokenLimit(),
		UnifiedExecEnabled:             features.Enabled(cfg.FeatureSettings(), "unified_exec"),
		ExecPermissionApprovals:        features.Enabled(cfg.FeatureSettings(), "exec_permission_approvals"),
		AgentController:                execAgentControllerFromTools(multiAgentTools),
		AgentExposure:                  execAgentExposureFromTools(multiAgentTools),
		AgentVersion:                   execAgentVersionFromTools(multiAgentTools),
		AgentNamespace:                 execAgentNamespaceFromTools(multiAgentTools),
		AgentUsageHintText:             execAgentUsageHintFromTools(multiAgentTools),
		AgentWaitDefault:               agentWaitDefault,
		AgentWaitMin:                   agentWaitMin,
		AgentWaitMax:                   agentWaitMax,
		AgentWaitConfigured:            multiAgentTools != nil,
		AgentHideSpawnMetadata:         agentHideSpawnMetadata,
		AgentExposeSpawnModelOverrides: agentExposeSpawnOverrides,
		AgentRoles:                     execAgentRolesFromTools(multiAgentTools),
		AgentDefaults:                  execAgentDefaultsFromTools(multiAgentTools),
		DisableWaitAgent:               execAgentWaitDisabledFromTools(multiAgentTools),
		SteerMailbox:                   firstSteerMailbox(req.SteerMailbox, execAgentSteerMailboxFromTools(req, multiAgentTools)),
		OnSteerCommitted:               req.OnSteerCommitted,
		SamplingFollowUp:               r.execAutoCompactFallbackFollowUp(cfg, modelID),
		SamplingCompaction:             r.execMidTurnSamplingCompaction(cfg, modelID, providerID, agent, req, threadID, turnID, prompt, requestInputs, resumeContext, eventSink, execStartupItems, req.AdditionalInputItems),
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			_ = r.persistInterruptedSession(threadID, turnID, err)
		} else {
			_ = r.persistFailedSession(threadID, turnID, err)
		}
		if execIsContextWindowExceeded(err) && resumeContext != nil && resumeContext.Record != nil {
			_ = r.persistExecContextWindowExceeded(resumeContext.Record)
		}
		_ = eventSink.Emit(protocol.ErrorEvent(err.Error()))
		_ = eventSink.Emit(protocol.TurnFailed(err.Error()))
		return nil, err
	}
	if err := eventSink.Err(); err != nil {
		return nil, err
	}
	lastMessage, hasLastMessage := finalMessageForRequest(req, turnResult)
	tokenUsage := execTokenUsageForResult(resumeContext, turnResult, modelID, cfg)
	sessionPath, err := r.persistSession(req, threadID, turnID, prompt, requestInputs, turnResult, resumeContext, tokenUsage)
	if err != nil {
		return nil, err
	}

	if req.Exec.LastMessageFile != "" {
		if err := os.WriteFile(req.Exec.LastMessageFile, []byte(lastMessage), 0o600); err != nil {
			return nil, err
		}
		if !hasLastMessage && stderr != nil {
			fmt.Fprintf(stderr, "Warning: no last agent message; wrote empty content to %s\n", req.Exec.LastMessageFile)
		}
	}

	if err := emitFinalEventsFromAgentResult(eventSink, turnResult, compactedThisTurn); err != nil {
		return nil, err
	}
	events := eventSink.Events()
	if !req.Exec.JSON {
		if stderr != nil {
			if total := blendedHumanTokenTotal(agentUsageForResult(turnResult)); total > 0 {
				fmt.Fprintf(stderr, "tokens used\n%s\n", formatIntWithSeparators(total))
			}
		}
		if hasLastMessage {
			if err := writeHumanFinalMessage(stdout, stderr, lastMessage); err != nil {
				return nil, err
			}
		}
	}

	return &Result{
		ThreadID:    threadID,
		TurnID:      turnID,
		SessionPath: sessionPath,
		LastMessage: lastMessage,
		Prompt:      prompt,
		Events:      events,
		TokenUsage:  tokenUsage,
	}, nil
}

func validateExecWorkingDirectory(cwd string) error {
	info, err := os.Stat(cwd)
	if err != nil {
		return fmt.Errorf("working directory %q is invalid: %w", cwd, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("working directory %q is not a directory", cwd)
	}
	return nil
}

func execReviewSubagentMetadata(req *Request) (string, string) {
	if req != nil && req.subagent != nil {
		return codexapi.ClientSubagentMetadataFromSource(execSessionSource(req))
	}
	if req != nil && req.Exec.Subcommand == "review" {
		return string(model.AgentTaskReview), string(model.AgentTaskReview)
	}
	return "", ""
}

type agentRunConfig struct {
	Config                         *config.Config
	Prompt                         string
	Instructions                   string
	InputItems                     []any
	Model                          string
	ToolMode                       string
	CodeModeHostEnabled            bool
	DisableCodeModeFallback        bool
	CodeModeDefaultExecYieldTime   time.Duration
	CodeModeProvider               tool.CodeModeRemoteProvider
	CodeModeRuntime                *tool.CodeModeRuntime
	ProviderID                     string
	TaskKind                       model.AgentTaskKind
	ThreadID                       string
	TurnID                         string
	Originator                     string
	PreviousResponseID             string
	ParallelToolCalls              bool
	ReasoningEffort                string
	ReasoningSummary               string
	ConcurrentReasoningSummaries   bool
	ModelVerbosity                 string
	IncludeTimingMetrics           bool
	BetaFeaturesHeader             string
	ItemIDsEnabled                 bool
	PromptCacheKey                 string
	ServiceTier                    string
	ClientMetadata                 map[string]string
	OutputSchema                   any
	ApprovalPolicy                 sandbox.AskForApproval
	StreamEvents                   *execStreamEventCollector
	PermissionProfileID            string
	PermissionProfile              *sandbox.PermissionProfile
	MCPService                     *mcp.MCPService
	MCPTools                       []mcp.RuntimeToolInfo
	MCPConnectors                  []mcp.RuntimeConnector
	WebSearch                      *turn.WebSearchOptions
	ImageGeneration                *turn.ImageGenerationOptions
	ViewImage                      *tool.ViewImageOptions
	HostedTools                    []any
	DisableHostedImageGeneration   bool
	ToolOutputTokenLimit           *int
	UnifiedExecEnabled             bool
	ExecPermissionApprovals        bool
	AgentController                multiagent.ToolController
	AgentExposure                  tool.Exposure
	AgentVersion                   multiagent.MultiAgentVersion
	AgentNamespace                 string
	AgentUsageHintText             *string
	AgentWaitDefault               time.Duration
	AgentWaitMin                   time.Duration
	AgentWaitMax                   time.Duration
	AgentWaitConfigured            bool
	AgentHideSpawnMetadata         bool
	AgentExposeSpawnModelOverrides bool
	AgentRoles                     map[string]multiagent.RoleConfig
	AgentDefaults                  multiagent.SpawnDefaults
	DisableWaitAgent               bool
	SteerMailbox                   *turn.SteerMailbox
	OnSteerCommitted               func(count int)
	SamplingFollowUp               turn.SamplingFollowUp
	SamplingCompaction             turn.SamplingCompaction
}

func firstSteerMailbox(values ...*turn.SteerMailbox) *turn.SteerMailbox {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

type execStreamEventCollector struct {
	sink                  *execEventSink
	workingDirectory      string
	events                []protocol.ThreadEvent
	streamAssistantDeltas bool
	streamedAgentText     map[string]string
	completedAgentItems   map[string]bool
	retrying              bool
	warningCount          int
}

func (c *execStreamEventCollector) Handle(event *model.ResponsesStreamEvent) {
	if c == nil || event == nil {
		return
	}
	switch event.Kind {
	case model.ResponsesStreamEventRetrying:
		// Rust renders the retry counter as the Activity header and the transport
		// failure as transient details. The row is removed when output resumes.
		c.retrying = true
		c.emit(protocol.Reconnecting(event.RetryAttempt, event.RetryMax, event.RetryError))
	case model.ResponsesStreamEventOutputAdded:
		if event.Item == nil || event.Item.Type == "" || event.Item.Type == "agent_message" || event.Item.Type == "reasoning" {
			return
		}
		if event.Item.Name == tool.DefaultApplyPatchToolName {
			// Rust renders apply_patch exclusively through the FileChange lifecycle.
			return
		}
		if event.Item.Type == "tool_search_call" {
			return
		}
		if streamAgentItemLooksLikeWebSearch(event.Item) {
			// Standalone web.run uses the canonical web_search lifecycle emitted
			// by ToolStarted/ToolCompleted, not a generic function-call cell.
			return
		}
		// Rust creates command cells from the execution lifecycle, after the
		// complete command is known. The model's output-added event can carry
		// empty arguments and must not create a generic exec_command cell.
		if tool.IsShellCommandToolName(tool.PlainName(event.Item.Name)) {
			return
		}
		if event.Item.Name == tool.CodeModeExecToolName {
			// Rust renders code-mode's nested commands through the canonical
			// command_execution lifecycle, not as a separate outer tool_call.
			return
		}
		// MCP calls get their canonical lifecycle item from ToolStarted, once
		// routing has resolved the raw server and tool names.
		if streamAgentItemLooksLikeMCP(event.Item) {
			return
		}
		if streamAgentItemLooksLikeCollaboration(event.Item) {
			// Collaboration calls have a dedicated lifecycle. Exposing the raw
			// function call here duplicates that lifecycle and, for encrypted
			// message arguments, can leak ciphertext into SDK/TUI output.
			return
		}
		item := protocolItemFromStreamAgentItem(event.Item)
		if item.ID != "" {
			c.emit(protocol.ItemStarted(item))
		}
	case model.ResponsesStreamEventOutputText:
		if !c.streamAssistantDeltas && c.sink != nil && c.sink.encoder != nil {
			return
		}
		if event.Delta == "" {
			return
		}
		if c.retrying {
			c.retrying = false
			c.emit(protocol.Reconnected())
		}
		if c.streamedAgentText == nil {
			c.streamedAgentText = map[string]string{}
		}
		itemID := firstNonEmpty(event.ItemID, "agent-message")
		c.streamedAgentText[itemID] += event.Delta
		c.emit(protocol.AgentMessageDelta(itemID, event.Delta))
	case model.ResponsesStreamEventOutputDone:
		if !c.streamAssistantDeltas && c.sink != nil && c.sink.encoder != nil {
			return
		}
		if event.Item == nil || (event.Item.Type != "message" && event.Item.Type != "agent_message") {
			return
		}
		itemID := firstNonEmpty(event.ItemID, event.Item.ID, "agent-message")
		if text := event.Item.Text; strings.TrimSpace(text) != "" {
			streamed := c.streamedAgentText[itemID]
			if streamed == "" {
				if c.streamedAgentText == nil {
					c.streamedAgentText = map[string]string{}
				}
				c.streamedAgentText[itemID] = text
				c.emit(protocol.AgentMessageDelta(itemID, text))
			} else if strings.HasPrefix(text, streamed) && len(text) > len(streamed) {
				c.streamedAgentText[itemID] = text
				c.emit(protocol.AgentMessageDelta(itemID, text[len(streamed):]))
			}
		}
		if c.completedAgentItems == nil {
			c.completedAgentItems = map[string]bool{}
		}
		if !c.completedAgentItems[itemID] {
			c.completedAgentItems[itemID] = true
			c.emit(protocol.ItemCompleted(protocol.AgentMessageItemWithPhase(itemID, event.Item.Text, agentMessagePhase(event.Item))))
		}
	case model.ResponsesStreamEventModelReroute:
		if message := modelRerouteErrorMessage(event.Reroute); message != "" {
			c.emit(protocol.ItemCompleted(protocol.ErrorItem("model-reroute", message)))
		}
	}
}

func (c *execStreamEventCollector) ToolStarted(_ context.Context, invocation *tool.Invocation, _ time.Time) {
	if c == nil || invocation == nil {
		return
	}
	if invocationLooksLikeMCP(invocation) {
		c.emit(protocol.ItemStarted(mcpToolCallProtocolItem(&turn.ToolExecutionResult{Invocation: invocation}, "in_progress")))
		return
	}
	if isExecWebSearchInvocation(invocation) {
		c.emit(protocol.ItemStarted(protocol.WebSearchItem(
			firstNonEmpty(invocation.CallID, "web-search"),
			"",
			map[string]any{"type": "other"},
		)))
		return
	}
	if isExecImageGenerationInvocation(invocation) {
		c.emit(protocol.ItemStarted(protocol.ImageGenerationItem(
			firstNonEmpty(invocation.CallID, "image-generation"),
			"in_progress",
			"",
			"",
		)))
		return
	}
	if invocation.ToolName.Key() == tool.DefaultApplyPatchToolName {
		// Rust begins the public FileChange lifecycle only after validation.
		// Final execution mapping emits started/completed for validated patches.
		return
	}
	if !tool.IsShellCommandToolName(invocation.ToolName) {
		return
	}
	command := commandFromShellInvocation(invocation)
	if command == "" {
		return
	}
	c.emit(protocol.ItemStarted(protocol.CommandExecutionItem(
		firstNonEmpty(invocation.CallID, "command-execution"),
		command,
		"",
		nil,
		"in_progress",
	)))
}

func (c *execStreamEventCollector) ToolCompleted(_ context.Context, execution *turn.ToolExecutionResult) {
	if c == nil || execution == nil {
		return
	}
	if item, ok := subAgentActivityProtocolItem(execution); ok {
		c.emitInternal(protocol.ItemStarted(item))
		c.emitInternal(protocol.ItemCompleted(item))
	}
	for _, event := range eventsFromToolExecution(execution) {
		c.emit(event)
	}
}

func (c *execStreamEventCollector) emitInternal(event protocol.ThreadEvent) {
	if c == nil || c.sink == nil {
		return
	}
	c.sink.EmitInternal(event)
}

func (c *execStreamEventCollector) CodeModeNotify(_ context.Context, callID string, text string) {
	if c == nil || strings.TrimSpace(text) == "" {
		return
	}
	// Rust injects notify into model history immediately. It does not create a
	// separate SDK thread item, so keep the public event stream free of duplicates.
	_ = callID
}

func (c *execStreamEventCollector) Warning(message string) {
	if c == nil || strings.TrimSpace(message) == "" {
		return
	}
	c.warningCount++
	c.emit(protocol.ItemCompleted(protocol.ErrorItem(fmt.Sprintf("warning-%d", c.warningCount), message)))
}

func (c *execStreamEventCollector) AssistantMessage(response *model.AgentResponse, _ int, hasToolCalls bool) {
	if c == nil || response == nil || !hasToolCalls {
		return
	}
	for i := range response.Items {
		item := response.Items[i]
		if item.Type != "" && item.Type != "agent_message" && item.Type != "message" {
			continue
		}
		if strings.TrimSpace(item.Text) == "" {
			continue
		}
		c.emit(protocol.ItemCompleted(protocol.AgentMessageItemWithPhase(firstNonEmpty(item.ID, "agent-message"), item.Text, agentMessagePhase(&item))))
	}
}

func isExecWebSearchInvocation(invocation *tool.Invocation) bool {
	if invocation == nil {
		return false
	}
	if invocation.ToolName.Namespace == turn.WebSearchNamespace &&
		invocation.ToolName.Name == turn.WebSearchRunTool {
		return true
	}
	return strings.TrimSpace(invocation.ToolName.Key()) == turn.WebSearchNamespace+"."+turn.WebSearchRunTool
}

func isExecImageGenerationInvocation(invocation *tool.Invocation) bool {
	return invocation != nil &&
		invocation.ToolName.Namespace == turn.ImageGenerationNamespace &&
		invocation.ToolName.Name == turn.ImageGenerationToolName
}

func (c *execStreamEventCollector) emit(event protocol.ThreadEvent) {
	if c == nil {
		return
	}
	if c.sink != nil {
		_ = c.sink.Emit(event)
		return
	}
	c.events = append(c.events, event)
}

func (c *execStreamEventCollector) Events() []protocol.ThreadEvent {
	if c == nil {
		return nil
	}
	if c.sink != nil {
		return c.sink.Events()
	}
	return append([]protocol.ThreadEvent(nil), c.events...)
}

func modelRerouteErrorMessage(reroute *model.ResponsesModelReroute) string {
	if reroute == nil {
		return ""
	}
	fromModel := strings.TrimSpace(reroute.FromModel)
	toModel := strings.TrimSpace(reroute.ToModel)
	reason := rustStyleModelRerouteReason(reroute.Reason)
	if fromModel == "" && toModel == "" {
		return ""
	}
	if reason != "" {
		return fmt.Sprintf("model rerouted: %s -> %s (%s)", fromModel, toModel, reason)
	}
	return fmt.Sprintf("model rerouted: %s -> %s", fromModel, toModel)
}

func rustStyleModelRerouteReason(reason string) string {
	reason = strings.TrimSpace(reason)
	if reason == "" {
		return ""
	}
	parts := strings.FieldsFunc(reason, func(r rune) bool {
		return r == '_' || r == '-' || r == ' '
	})
	var out strings.Builder
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lower := strings.ToLower(part)
		out.WriteString(strings.ToUpper(lower[:1]))
		if len(lower) > 1 {
			out.WriteString(lower[1:])
		}
	}
	if out.Len() == 0 {
		return reason
	}
	return out.String()
}

type execEventSink struct {
	mu              sync.Mutex
	events          []protocol.ThreadEvent
	encoder         *json.Encoder
	human           *execHumanRenderer
	internalHandler func(protocol.ThreadEvent)
	err             error
}

func newExecEventSink(stdout io.Writer, encodeJSON bool) *execEventSink {
	sink := &execEventSink{}
	if encodeJSON && stdout != nil {
		sink.encoder = json.NewEncoder(stdout)
		sink.encoder.SetEscapeHTML(false)
	}
	return sink
}

func (s *execEventSink) Emit(event protocol.ThreadEvent) error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if (event.Type == "item.started" || event.Type == "item.completed") && event.Item != nil {
		for i := range s.events {
			previous := s.events[i]
			if previous.Type == event.Type && previous.Item != nil && previous.Item.ID == event.Item.ID && previous.Item.Type == event.Item.Type {
				return s.err
			}
		}
	}
	s.events = append(s.events, event)
	if s.internalHandler != nil {
		s.internalHandler(event)
	}
	if s.err != nil {
		return s.err
	}
	if s.encoder != nil {
		if err := s.encoder.Encode(event); err != nil {
			s.err = err
			return err
		}
	}
	if s.human != nil {
		s.human.HandleEvent(event)
	}
	return nil
}

func (s *execEventSink) EmitInternal(event protocol.ThreadEvent) {
	if s == nil || s.internalHandler == nil {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.internalHandler(event)
}

func (s *execEventSink) Events() []protocol.ThreadEvent {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]protocol.ThreadEvent(nil), s.events...)
}

func (s *execEventSink) Err() error {
	if s == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

func (r *Runner) runAgentTurn(ctx context.Context, req *Request, agent model.AgentRunner, run *agentRunConfig) (*turn.AgentLoopResult, error) {
	if agent == nil {
		return nil, errors.New("agent runner is nil")
	}
	if run == nil {
		return nil, errors.New("agent run config is nil")
	}
	r.setGoalTurnContext(strings.TrimSpace(run.ThreadID), strings.TrimSpace(run.TurnID))
	runForRouter := *run
	var ownedCodeModeProvider interface{ Close() error }
	if r.ToolRouter == nil {
		provider := r.CodeModeProvider
		if provider == nil {
			if run.CodeModeHostEnabled || run.DisableCodeModeFallback {
				provider = codemode.NewProcessProvider(install.Current().CodeModeHostProgram())
			} else {
				provider = codemode.NewDisabledProvider()
			}
			ownedCodeModeProvider, _ = provider.(interface{ Close() error })
		}
		runForRouter.CodeModeProvider = provider
		runForRouter.CodeModeRuntime = tool.NewCodeModeRuntime(provider, run.DisableCodeModeFallback)
	}
	if runForRouter.CodeModeRuntime != nil {
		defer runForRouter.CodeModeRuntime.Close()
	}
	if ownedCodeModeProvider != nil {
		defer ownedCodeModeProvider.Close()
	}
	router, err := r.toolRouterForRequest(req, &runForRouter)
	if err != nil {
		return nil, err
	}
	if run.StreamEvents != nil {
		if responsesAgent, ok := agent.(*model.ResponsesAgentRunner); ok && responsesAgent != nil {
			agent = responsesAgent.WithStreamHandler(run.StreamEvents.Handle)
		}
	}
	return turn.NewRuntime(&turn.RuntimeOptions{
		Agent:        agent,
		Router:       router,
		Hooks:        r.Hooks,
		SteerMailbox: run.SteerMailbox,
		Now:          r.now,
		MaxTurns:     r.MaxToolTurns,
	}).Run(ctx, &turn.AgentLoopRequest{
		Prompt:                       run.Prompt,
		Instructions:                 run.Instructions,
		InputItems:                   append([]any(nil), run.InputItems...),
		HostedTools:                  append([]any(nil), run.HostedTools...),
		Model:                        run.Model,
		ToolMode:                     run.ToolMode,
		DisableCodeModeFallback:      run.DisableCodeModeFallback,
		ProviderID:                   run.ProviderID,
		TaskKind:                     run.TaskKind,
		ThreadID:                     run.ThreadID,
		TurnID:                       run.TurnID,
		Originator:                   run.Originator,
		PreviousResponseID:           run.PreviousResponseID,
		ParallelToolCalls:            run.ParallelToolCalls,
		ReasoningEffort:              run.ReasoningEffort,
		ReasoningSummary:             run.ReasoningSummary,
		ConcurrentReasoningSummaries: run.ConcurrentReasoningSummaries,
		ModelVerbosity:               run.ModelVerbosity,
		IncludeTimingMetrics:         run.IncludeTimingMetrics,
		BetaFeaturesHeader:           run.BetaFeaturesHeader,
		ItemIDsEnabled:               run.ItemIDsEnabled,
		PromptCacheKey:               run.PromptCacheKey,
		ServiceTier:                  run.ServiceTier,
		ClientMetadata:               cloneStringMap(run.ClientMetadata),
		OutputSchema:                 run.OutputSchema,
		DisableHostedImageGeneration: run.DisableHostedImageGeneration,
		OnToolStarted:                run.StreamEvents.ToolStarted,
		OnToolCompleted:              run.StreamEvents.ToolCompleted,
		EmitCodeModeNestedLifecycle:  true,
		OnCodeModeNotify:             run.StreamEvents.CodeModeNotify,
		OnWarning:                    run.StreamEvents.Warning,
		OnAssistantMessage:           run.StreamEvents.AssistantMessage,
		OnSteerCommitted:             run.OnSteerCommitted,
		SamplingFollowUp:             run.SamplingFollowUp,
		SamplingCompaction:           run.SamplingCompaction,
	})
}

func (r *Runner) toolRouterForRequest(req *Request, run *agentRunConfig) (*tool.Router, error) {
	if r == nil {
		return nil, errors.New("exec runner is nil")
	}
	goalExecutors := r.goalToolExecutorsForRequest(req, run)
	if r.ToolRouter != nil {
		if run != nil && run.ViewImage != nil {
			if err := r.ToolRouter.RegisterIfAbsent(tool.NewViewImageHandler(*run.ViewImage)); err != nil {
				return nil, err
			}
		}
		return r.ToolRouter, nil
	}
	options := turn.DefaultToolRegistryOptions(requestCWD(req))
	options.UnifiedExec = r.UnifiedExec
	if run != nil {
		options.EnableUnifiedExec = run.UnifiedExecEnabled
		options.CodeModeProvider = run.CodeModeProvider
		options.CodeModeRuntime = run.CodeModeRuntime
		options.CodeModeDefaultExecYieldTime = run.CodeModeDefaultExecYieldTime
		options.DisableCodeModeFallback = run.DisableCodeModeFallback
	}
	if options.Shell != nil {
		options.Shell.Approval = r.ShellApproval
		if run != nil {
			options.Shell.MaxOutputTokens = run.ToolOutputTokenLimit
			options.Shell.Validation.AdditionalPermissionsAllowed = run.ExecPermissionApprovals
		}
	}
	options.UserInputResponder = r.UserInput
	if options.Shell != nil && run != nil && run.PermissionProfile != nil {
		options.Shell.Validation.PermissionProfileID = run.PermissionProfileID
		options.Shell.Validation.PermissionProfile = run.PermissionProfile
	}
	if options.Shell != nil && run != nil && run.ApprovalPolicy != "" {
		options.Shell.Validation.ApprovalPolicy = run.ApprovalPolicy
	}
	options.EnableMCP = false
	mcpService := r.MCPService
	mcpTools := append([]mcp.RuntimeToolInfo(nil), r.MCPTools...)
	mcpConnectors := append([]mcp.RuntimeConnector(nil), r.MCPConnectors...)
	if mcpService == nil && len(mcpTools) == 0 && len(mcpConnectors) == 0 && run != nil {
		mcpService = run.MCPService
		mcpTools = append([]mcp.RuntimeToolInfo(nil), run.MCPTools...)
		mcpConnectors = append([]mcp.RuntimeConnector(nil), run.MCPConnectors...)
	}
	if mcpService != nil || len(mcpTools) > 0 || len(mcpConnectors) > 0 {
		options.EnableMCP = true
		options.MCPService = mcpService
		if options.MCPService != nil && r.MCPElicitation != nil {
			options.MCPService.SetElicitationHandler(r.MCPElicitation)
		}
		options.MCPTools = mcpTools
		options.MCPConnectors = mcpConnectors
	}
	if run != nil {
		options.WebSearch = run.WebSearch
		options.ImageGeneration = run.ImageGeneration
		options.ViewImage = run.ViewImage
	}
	if run != nil && strings.TrimSpace(run.Model) != "" {
		// Forward the issuing model's confirmation-policy documents to actor MCP
		// calls (#41072); resolved from the same catalog as the turn model.
		info := execModelInfo(strings.TrimSpace(run.Model), run.Config)
		if info.ModelMessages != nil {
			options.ModelConfirmationPolicies = info.ModelMessages.ConfirmationPolicies
		}
	}
	// Guardian review sessions omit the confirmation-policies request metadata.
	options.SuppressActorConfirmationPolicies = req != nil && strings.EqualFold(strings.TrimSpace(req.Exec.Subcommand), "review")
	options.EnableAgents = run != nil && run.AgentController != nil
	if options.EnableAgents {
		options.AgentController = run.AgentController
		options.AgentExposure = run.AgentExposure
		options.AgentVersion = run.AgentVersion
		options.AgentNamespace = run.AgentNamespace
		options.AgentUsageHintText = run.AgentUsageHintText
		options.AgentWaitDefault = run.AgentWaitDefault
		options.AgentWaitMin = run.AgentWaitMin
		options.AgentWaitMax = run.AgentWaitMax
		options.AgentWaitConfigured = run.AgentWaitConfigured
		options.AgentHideSpawnMetadata = run.AgentHideSpawnMetadata
		options.AgentExposeSpawnModelOverrides = run.AgentExposeSpawnModelOverrides
		options.AgentRoles = run.AgentRoles
		options.AgentDefaults = run.AgentDefaults
		options.DisableWaitAgent = run.DisableWaitAgent
	}
	options.ExtraTools = goalExecutors
	return turn.BuildToolRouter(options)
}

func (r *Runner) configuredMCPRuntimeForConfig(cfg *config.Config, resolvedAuth ...*auth.ResolvedAuth) (*mcp.MCPService, []mcp.RuntimeToolInfo, []mcp.RuntimeConnector) {
	if r == nil || cfg == nil {
		return nil, nil, nil
	}
	if r.MCPService != nil || len(r.MCPTools) > 0 || len(r.MCPConnectors) > 0 {
		return r.MCPService, append([]mcp.RuntimeToolInfo(nil), r.MCPTools...), append([]mcp.RuntimeConnector(nil), r.MCPConnectors...)
	}
	var runtimeAuth *mcp.RuntimeAuth
	if len(resolvedAuth) > 0 && resolvedAuth[0] != nil {
		runtimeAuth = mcp.RuntimeAuthFromSnapshot(&resolvedAuth[0].Auth)
	}
	runtimeConfig := mcp.RuntimeConfigFromValuesWithAuthAndRequirements(cfg.Values, r.CodexHome, runtimeAuth, cfg.Requirements)
	if runtimeConfig == nil || len(runtimeConfig.Servers) == 0 {
		return nil, nil, nil
	}
	service := mcp.NewMCPService(runtimeConfig)
	response, err := service.ListStatusChecked(&mcp.MCPListServerStatusParams{
		Detail: &mcp.MCPServerStatusDetail{Mode: mcp.MCPServerStatusDetailToolsAndAuthOnly},
	})
	if err != nil || response == nil {
		return service, nil, nil
	}
	return service, mcp.RuntimeToolsFromStatuses(response.Data), nil
}

func (r *Runner) imageGenerationOptionsForRun(cfg *config.Config, resolvedAuth *auth.ResolvedAuth, providerID string, modelID string, threadID string, inputItems []any) (*turn.ImageGenerationOptions, error) {
	if r == nil || !r.UseResponsesAPI {
		return nil, nil
	}
	var snapshot *auth.AuthDotJSON
	if resolvedAuth != nil {
		snapshot = &resolvedAuth.Auth
	}
	provider, err := model.ProviderForConfigID(configValues(cfg), providerID, stringConfigValue(cfg, "openai_base_url"))
	if err != nil {
		return nil, err
	}
	runtimeProvider := model.CreateRuntimeProviderForID(providerID, *provider, snapshot)
	modelInfo := model.NewStaticModelsManager(model.BundledModelsResponse()).GetModelInfo(modelID, nil)
	if !imageGenerationStandaloneEnabled(*provider, runtimeProvider.Capabilities(), &modelInfo, snapshot, cfg.FeatureSettings()) {
		return nil, nil
	}
	apiProvider, err := runtimeProvider.APIProvider()
	if err != nil {
		return nil, err
	}
	authHeaders, err := runtimeProvider.APIAuth()
	if err != nil {
		return nil, err
	}
	return &turn.ImageGenerationOptions{
		SessionID:  threadID,
		CodexHome:  r.CodexHome,
		Provider:   apiProvider,
		Auth:       authHeaders,
		HTTPClient: r.httpClientForConfig(cfg),
		InputItems: append([]any(nil), inputItems...),
	}, nil
}

func (r *Runner) hostedToolsForRun(cfg *config.Config, resolvedAuth *auth.ResolvedAuth, providerID string, modelID string, forceLiveWebSearch bool, standaloneWebSearch *turn.WebSearchOptions, standaloneImageGeneration *turn.ImageGenerationOptions) ([]any, error) {
	if r == nil || !r.UseResponsesAPI {
		return nil, nil
	}
	var snapshot *auth.AuthDotJSON
	if resolvedAuth != nil {
		snapshot = &resolvedAuth.Auth
	}
	provider, err := model.ProviderForConfigID(configValues(cfg), providerID, stringConfigValue(cfg, "openai_base_url"))
	if err != nil {
		return nil, err
	}
	runtimeProvider := model.CreateRuntimeProviderForID(providerID, *provider, snapshot)
	modelInfo := model.NewStaticModelsManager(model.BundledModelsResponse()).GetModelInfo(modelID, nil)
	capabilities := runtimeProvider.Capabilities()
	tools := []any{}
	if standaloneWebSearch == nil && !modelInfo.UseResponsesLite && capabilities.WebSearch {
		mode := execWebSearchMode(cfg, forceLiveWebSearch)
		if hosted := turn.HostedWebSearchTool(mode, codexapi.SearchSettingsForMode(mode, execWebSearchToolConfig(cfg)), modelInfo.WebSearchToolType); hosted != nil {
			tools = append(tools, hosted)
		}
	}
	if standaloneImageGeneration == nil && imageGenerationHostedEnabledExec(*provider, capabilities, &modelInfo, snapshot, cfg.FeatureSettings()) {
		tools = append(tools, turn.HostedImageGenerationTool("png"))
	}
	return tools, nil
}

func imageGenerationStandaloneEnabled(provider model.ProviderInfo, capabilities model.ProviderCapabilities, info *model.ModelInfo, snapshot *auth.AuthDotJSON, featureSettings map[string]bool) bool {
	if info == nil {
		return false
	}
	if !capabilities.NamespaceTools || !capabilities.ImageGeneration {
		return false
	}
	if !modelInfoSupportsImageInputExec(info) {
		return false
	}
	if !features.Enabled(featureSettings, "image_generation") {
		return false
	}
	return imageGenerationStandaloneAuthEnabledExec(provider, snapshot)
}

func imageGenerationHostedEnabledExec(provider model.ProviderInfo, capabilities model.ProviderCapabilities, info *model.ModelInfo, snapshot *auth.AuthDotJSON, featureSettings map[string]bool) bool {
	if info == nil || info.UseResponsesLite {
		return false
	}
	if !capabilities.ImageGeneration {
		return false
	}
	if !modelInfoSupportsImageInputExec(info) {
		return false
	}
	if !features.Enabled(featureSettings, "image_generation") {
		return false
	}
	return imageGenerationAuthEnabledExec(provider, snapshot)
}

func imageGenerationStandaloneAuthEnabledExec(provider model.ProviderInfo, snapshot *auth.AuthDotJSON) bool {
	if provider.UsesOpenAIActorAuthorization() {
		return true
	}
	if snapshot == nil {
		return false
	}
	if authSnapshotUsesCodexBackendExec(snapshot) {
		return provider.RequiresOpenAIAuth || provider.IsOpenAI()
	}
	return false
}

func imageGenerationAuthEnabledExec(provider model.ProviderInfo, snapshot *auth.AuthDotJSON) bool {
	if provider.UsesOpenAIActorAuthorization() {
		return true
	}
	if snapshot == nil {
		return false
	}
	if authSnapshotUsesCodexBackendExec(snapshot) {
		return provider.RequiresOpenAIAuth || provider.IsOpenAI()
	}
	return provider.IsOpenAI() && snapshot.Mode() == "api-key"
}

func modelInfoSupportsImageInputExec(info *model.ModelInfo) bool {
	if info == nil {
		return false
	}
	for _, modality := range info.InputModalities {
		if strings.EqualFold(strings.TrimSpace(modality), "image") {
			return true
		}
	}
	return false
}

func execViewImageOptions(cwd string, info *model.ModelInfo) *tool.ViewImageOptions {
	if !modelInfoSupportsImageInputExec(info) {
		return nil
	}
	return &tool.ViewImageOptions{
		CWD:                      cwd,
		CanRequestOriginalDetail: info.SupportsImageDetailOriginal,
	}
}

func authSnapshotUsesCodexBackendExec(snapshot *auth.AuthDotJSON) bool {
	if snapshot == nil {
		return false
	}
	switch snapshot.Mode() {
	case "chatgpt", "chatgptAuthTokens", "personal-access-token", "agent-identity":
		return true
	default:
		return false
	}
}

func resolveExecSandboxPermissionProfile(cfg *config.Config, req *Request) (*config.SandboxPermissionProfileResolution, error) {
	if cfg == nil {
		return nil, nil
	}
	return cfg.ResolveSandboxPermissionProfile(execSandboxProfileID(req), requestCWD(req))
}

func execSandboxProfileID(req *Request) string {
	if req == nil {
		return ""
	}
	if req.Exec.Shared.DangerouslyBypassApprovalsAndSandbox || req.Root.Shared.DangerouslyBypassApprovalsAndSandbox {
		return sandbox.BuiltInPermissionProfileDangerFullAccess
	}
	if value := strings.TrimSpace(req.Exec.Shared.Sandbox); value != "" {
		return value
	}
	return strings.TrimSpace(req.Root.Shared.Sandbox)
}

func sandboxPermissionProfileID(resolution *config.SandboxPermissionProfileResolution) string {
	if resolution == nil {
		return ""
	}
	return strings.TrimSpace(resolution.ID)
}

func sandboxPermissionProfile(resolution *config.SandboxPermissionProfileResolution) *sandbox.PermissionProfile {
	if resolution == nil || resolution.Profile == nil {
		return nil
	}
	return resolution.Profile
}

func effectiveExecApprovalPolicy(cfg *config.Config, req *Request) sandbox.AskForApproval {
	if req != nil && req.Exec.Subcommand == "review" {
		// Rust tasks/review.rs + 95aada11c4 (#38205): review delegates run
		// with approval policy `never`; approval-requiring commands are denied
		// inside the delegate instead of prompting or forwarding to the
		// parent session.
		return sandbox.ApprovalNever
	}
	if req != nil && (req.Exec.Shared.DangerouslyBypassApprovalsAndSandbox ||
		req.Root.Shared.DangerouslyBypassApprovalsAndSandbox) {
		return sandbox.ApprovalNever
	}
	if req != nil {
		for _, value := range []string{req.Exec.Shared.ApprovalPolicy, req.Root.Shared.ApprovalPolicy} {
			switch policy := sandbox.AskForApproval(strings.TrimSpace(value)); policy {
			case sandbox.ApprovalUnlessTrusted, sandbox.ApprovalOnRequest, sandbox.ApprovalGranular, sandbox.ApprovalNever:
				return policy
			}
		}
	}
	if strings.EqualFold(stringConfigValue(cfg, "approvals_reviewer"), string(config.ApprovalsReviewerAutoReview)) {
		if value := strings.TrimSpace(stringConfigValue(cfg, "approval_policy")); value != "" {
			switch sandbox.AskForApproval(value) {
			case sandbox.ApprovalUnlessTrusted, sandbox.ApprovalOnRequest, sandbox.ApprovalGranular, sandbox.ApprovalNever:
				return sandbox.AskForApproval(value)
			}
		}
		return sandbox.ApprovalOnRequest
	}
	return sandbox.ApprovalNever
}

func (r *Runner) agentForRun(cfg *config.Config, resolvedAuth *auth.ResolvedAuth, providerID string, storeOptions *auth.StoreOptions) (model.AgentRunner, error) {
	if r != nil && r.Agent != nil {
		return r.Agent, nil
	}
	if r != nil && r.UseResponsesAPI {
		var snapshot *auth.AuthDotJSON
		if resolvedAuth != nil {
			snapshot = &resolvedAuth.Auth
		}
		provider, err := model.ProviderForConfigID(configValues(cfg), providerID, stringConfigValue(cfg, "openai_base_url"))
		if err != nil {
			return nil, err
		}
		if snapshot == nil && provider.RequiresOpenAIAuth && !providerHasStandaloneAuth(*provider) {
			return nil, errors.New("OpenAI authentication is required; run `codex login` or set OPENAI_API_KEY")
		}
		runtimeProvider := model.CreateRuntimeProviderForID(providerID, *provider, snapshot)
		agent, err := model.NewResponsesAgentRunnerFromRuntimeProviderWithAuth(providerID, runtimeProvider, r.httpClientForConfig(cfg), r.CodexHome, snapshot)
		if err != nil {
			return nil, err
		}
		agent.StoreOptions = storeOptions
		agent.AuthIssuer = cfg.ChatGPTBaseURL()
		agent.AgentIdentity = agentIdentityOptionsForExec(cfg)
		agent.EnableRequestCompression = features.Enabled(cfg.FeatureSettings(), "enable_request_compression")
		return agent, nil
	}
	return model.NewLocalAgentRunner(), nil
}

func providerHasStandaloneAuth(provider model.ProviderInfo) bool {
	return strings.TrimSpace(provider.EnvKey) != "" ||
		strings.TrimSpace(provider.ExperimentalBearerToken) != "" ||
		provider.Auth != nil
}

func agentIdentityOptionsForExec(cfg *config.Config) *model.AgentIdentityOptions {
	if cfg == nil || !cfg.FeatureSettings()["use_agent_identity"] {
		return nil
	}
	return &model.AgentIdentityOptions{
		Enabled:                   true,
		ChatGPTBaseURL:            cfg.ChatGPTBaseURL(),
		ForcedChatGPTWorkspaceIDs: cfg.ForcedChatGPTWorkspaceIDs(),
		SessionSource:             "cli",
	}
}

func (r *Runner) httpClientForConfig(cfg *config.Config) model.HTTPDoer {
	if r != nil && r.HTTPClient != nil {
		return r.HTTPClient
	}
	return network.NewHTTPClient(cfg.RespectSystemProxyEnabled(), 0)
}

type execResumeContext struct {
	Record     *session.Record
	UserPrompt string
	ForkOnly   bool
}

func resumeInputItems(ctx *execResumeContext) []any {
	if ctx == nil || ctx.Record == nil {
		return nil
	}
	return session.InputItemsFromRecord(ctx.Record, &session.HistoryBuildOptions{IncludeToolOutputs: true, CWD: strings.TrimSpace(ctx.Record.Metadata.CWD)})
}

func resumePreviousResponseID(ctx *execResumeContext) string {
	// Rust only uses previous_response_id for an incremental request on the
	// same live WebSocket connection. A fresh-process resume reconstructs the
	// complete normalized rollout history instead of referencing an old server
	// response chain.
	return ""
}

func (r *Runner) promptForRequest(req *Request, cfg *config.Config, stdin io.Reader) (string, *execResumeContext, error) {
	if req.Exec.Subcommand == "review" {
		prompt, _, err := review.BuildPromptFromOptions(req.Exec.Review, stdin, &review.GitDiffProvider{
			Dir: requestCWD(req),
		})
		if err != nil {
			return "", nil, err
		}
		return prompt, nil, nil
	}
	if req.Exec.Subcommand == "resume" {
		record, err := r.resolveExecResumeRecord(req)
		if err != nil {
			return "", nil, err
		}
		if req.Exec.Resume.Prompt == "" && (len(req.Input) > 0 || len(req.AdditionalInputItems) > 0) {
			return "", &execResumeContext{Record: record}, nil
		}
		resumePrompt, err := resolveExecResumePrompt(req.Exec.Resume.Prompt, stdin)
		if err != nil {
			return "", nil, err
		}
		return resumePrompt, &execResumeContext{Record: record, UserPrompt: resumePrompt}, nil
	}
	if req.Exec.Subcommand == "fork" {
		promptArg := strings.TrimSpace(req.Exec.Fork.Prompt)
		forkOnly := execForkOnlyRequest(req)
		if forkOnly && len(requestImagePaths(req)) > 0 {
			return "", nil, errors.New("Forking with images requires a prompt")
		}
		if forkOnly && (strings.TrimSpace(req.Exec.OutputSchema) != "" || strings.TrimSpace(req.Exec.LastMessageFile) != "") {
			return "", nil, errors.New("Forking with output options requires a prompt")
		}
		if forkOnly && req.Exec.Ephemeral {
			return "", nil, errors.New("Ephemeral forks require a prompt")
		}
		promptText := ""
		if !forkOnly && promptArg != "" {
			var err error
			promptText, err = resolveExecResumePrompt(promptArg, stdin)
			if err != nil {
				return "", nil, err
			}
		} else if !forkOnly && len(req.Input) == 0 && len(req.AdditionalInputItems) == 0 {
			if promptArg == "" {
				promptArg = "-"
			}
			var err error
			promptText, err = resolveExecResumePrompt(promptArg, stdin)
			if err != nil {
				return "", nil, err
			}
		}
		resumeReq := *req
		resumeReq.Exec.Subcommand = "resume"
		resumeReq.Exec.Resume = cli.ExecResumeOptions{
			SessionID: strings.TrimSpace(req.Exec.Fork.SessionID),
			All:       true,
		}
		source, err := r.resolveExecResumeRecord(&resumeReq)
		if err != nil {
			return "", nil, err
		}
		if source.Archived {
			return "", nil, fmt.Errorf("session %s is archived", source.ID)
		}
		store := session.NewStore(filepath.Join(r.CodexHome, "sessions"))
		now := r.now().UTC()
		forked, err := store.ForkRecord(source, session.ForkOptions{
			Mode:      session.ForkAll,
			Ephemeral: req.Exec.Ephemeral,
			Now:       now,
		})
		if err != nil {
			return "", nil, err
		}
		forked.Metadata.CWD = requestCWD(req)
		forked.Metadata.Model = effectiveModel(req, cfg)
		provider, providerErr := effectiveProvider(req, cfg)
		if providerErr != nil {
			_ = store.Delete(forked.ID)
			return "", nil, providerErr
		}
		if provider != "" {
			forked.Metadata.ModelProvider = provider
		}
		forked.Metadata.Source = execSessionSource(req)
		forked.Metadata.ThreadSource = execThreadSource(req)
		forked.Metadata.Originator = execAgentOriginator(req)
		forked.Metadata.CLIVersion = doctor.Version()
		if !req.Exec.Ephemeral {
			if err := store.Save(forked); err != nil {
				return "", nil, err
			}
			if err := r.createExecRollout(forked, now); err != nil {
				_ = store.Delete(forked.ID)
				return "", nil, err
			}
		}
		if forkOnly {
			return "", &execResumeContext{Record: forked, ForkOnly: true}, nil
		}
		return promptText, &execResumeContext{Record: forked, UserPrompt: promptText}, nil
	}
	if req.Exec.Prompt == "" && (len(req.Input) > 0 || len(req.AdditionalInputItems) > 0) {
		return "", nil, nil
	}
	resolved, err := prompt.Resolve(req.Exec.Prompt, stdin)
	return resolved, nil, err
}

func execForkOnlyRequest(req *Request) bool {
	return req != nil && req.Exec.Subcommand == "fork" && strings.TrimSpace(req.Exec.Fork.Prompt) == "" && len(req.Input) == 0 && len(req.AdditionalInputItems) == 0
}

func resolveExecResumePrompt(promptArg string, stdin io.Reader) (string, error) {
	if promptArg != "" && promptArg != "-" {
		return promptArg, nil
	}
	return prompt.Resolve(promptArg, stdin)
}

func execAdditionalInputIdentity(items []any) string {
	if len(items) == 0 {
		return ""
	}
	encoded, err := json.Marshal(items)
	if err != nil {
		return fmt.Sprintf("additional-input-items:%d", len(items))
	}
	return string(encoded)
}

func requestTurnUserInputs(req *Request) []turn.TurnUserInput {
	inputs := cloneTurnUserInputs(req.Input)
	for _, path := range requestImagePaths(req) {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		inputs = append(inputs, turn.TurnUserInput{Type: "localImage", Path: path})
	}
	return inputs
}

func requestImagePaths(req *Request) []string {
	if req == nil {
		return nil
	}
	images := make([]string, 0, len(req.Root.Shared.Images)+len(req.Exec.Shared.Images)+len(req.Exec.Fork.Images))
	images = append(images, req.Root.Shared.Images...)
	images = append(images, req.Exec.Shared.Images...)
	images = append(images, req.Exec.Fork.Images...)
	return images
}

func cloneTurnUserInputs(values []turn.TurnUserInput) []turn.TurnUserInput {
	if len(values) == 0 {
		return nil
	}
	out := make([]turn.TurnUserInput, len(values))
	for i := range values {
		out[i] = values[i]
		out[i].TextElements = append([]turn.TextElement(nil), values[i].TextElements...)
		if values[i].Detail != nil {
			detail := *values[i].Detail
			out[i].Detail = &detail
		}
	}
	return out
}

func userMessageInputItemFromTurnInputs(prompt string, inputs []turn.TurnUserInput, cwd string) any {
	content := inputContentBlocksFromTurnInputs(prompt, inputs, cwd)
	if len(content) == 0 {
		return nil
	}
	return map[string]any{
		"type":    "message",
		"role":    "user",
		"content": content,
	}
}

func UserMessageInputItemFromTurnInputs(prompt string, inputs []turn.TurnUserInput, cwd string) any {
	return userMessageInputItemFromTurnInputs(prompt, inputs, cwd)
}

func inputContentBlocksFromTurnInputs(prompt string, inputs []turn.TurnUserInput, cwd string) []map[string]any {
	content := []map[string]any{}
	if text := strings.TrimSpace(prompt); text != "" {
		content = append(content, map[string]any{"type": "input_text", "text": text})
	}
	imageIndex := 0
	for i := range inputs {
		input := inputs[i]
		inputType := strings.TrimSpace(input.Type)
		if text := strings.TrimSpace(input.Text); text != "" {
			content = append(content, map[string]any{"type": "input_text", "text": text})
			continue
		}
		if imageURL := strings.TrimSpace(input.URL); imageURL != "" && (inputType == "" || strings.EqualFold(inputType, "image")) {
			imageIndex++
			content = append(content, inputImageContentBlock(imageURL, inputDetail(input)))
			continue
		}
		if path := strings.TrimSpace(input.Path); path != "" && (inputType == "" || strings.EqualFold(inputType, "localImage")) {
			imageIndex++
			content = append(content, localImageInputContentBlocks(path, cwd, inputDetail(input), imageIndex)...)
		}
	}
	return content
}

func inputImageContentBlock(imageURL string, detail string) map[string]any {
	if strings.TrimSpace(detail) == "" {
		detail = "high"
	}
	return map[string]any{"type": "input_image", "image_url": imageURL, "detail": detail}
}

func inputDetail(input turn.TurnUserInput) string {
	if input.Detail != nil && strings.TrimSpace(*input.Detail) != "" {
		return strings.TrimSpace(*input.Detail)
	}
	return "high"
}

func localImageInputContentBlocks(path string, cwd string, detail string, imageIndex int) []map[string]any {
	resolvedPath := path
	if !filepath.IsAbs(resolvedPath) && strings.TrimSpace(cwd) != "" {
		resolvedPath = filepath.Join(cwd, resolvedPath)
	}
	resolvedPath = filepath.Clean(resolvedPath)
	data, err := os.ReadFile(resolvedPath)
	if err != nil {
		return []map[string]any{{
			"type": "input_text",
			"text": fmt.Sprintf("Codex could not read the local image at `%s`: %v", path, err),
		}}
	}
	_, format, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil {
		return []map[string]any{{
			"type": "input_text",
			"text": fmt.Sprintf("Codex could not decode the local image at `%s`: %v", path, err),
		}}
	}
	mimeType := imageFormatMIME(format)
	if mimeType == "" {
		return []map[string]any{{
			"type": "input_text",
			"text": fmt.Sprintf("Codex does not support the local image format at `%s`: %s", path, format),
		}}
	}
	return []map[string]any{
		{"type": "input_text", "text": fmt.Sprintf("<image name=[Image #%d] path=\"%s\">", imageIndex, path)},
		inputImageContentBlock(dataURLFromBytes(mimeType, data), detail),
		{"type": "input_text", "text": "</image>"},
	}
}

func imageFormatMIME(format string) string {
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "png":
		return "image/png"
	case "jpeg":
		return "image/jpeg"
	case "gif":
		return "image/gif"
	default:
		return ""
	}
}

func dataURLFromBytes(mimeType string, data []byte) string {
	return "data:" + mimeType + ";base64," + base64.StdEncoding.EncodeToString(data)
}

func turnUserInputsSummary(inputs []turn.TurnUserInput) string {
	parts := []string{}
	for i := range inputs {
		input := inputs[i]
		if text := strings.TrimSpace(input.Text); text != "" {
			parts = append(parts, text)
			continue
		}
		if url := strings.TrimSpace(input.URL); url != "" {
			parts = append(parts, "Attached image: "+url)
			continue
		}
		if path := strings.TrimSpace(input.Path); path != "" {
			parts = append(parts, "Attached local image: "+path)
		}
	}
	return strings.Join(parts, "\n")
}

func requestCWD(req *Request) string {
	if req == nil {
		return "."
	}
	if req.Exec.Shared.CWD != "" {
		return req.Exec.Shared.CWD
	}
	if req.Root.Shared.CWD != "" {
		return req.Root.Shared.CWD
	}
	return "."
}

func execStartupInputItems(req *Request, permissions *config.SandboxPermissionProfileResolution, approvalPolicy sandbox.AskForApproval, now time.Time) []any {
	items := make([]any, 0, 2)
	if item := developerMessageInputItem(execPermissionsInstructions(permissions, approvalPolicy)); item != nil {
		items = append(items, item)
	}
	if item := model.UserMessageInputItem(execEnvironmentContext(req, permissions, now)); item != nil {
		items = append(items, item)
	}
	return items
}

func developerMessageInputItem(text string) any {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}
	return map[string]any{
		"type": "message",
		"role": "developer",
		"content": []map[string]any{{
			"type": "input_text",
			"text": text,
		}},
	}
}

func execPermissionsInstructions(permissions *config.SandboxPermissionProfileResolution, approvalPolicy sandbox.AskForApproval) string {
	mode := sandboxPermissionProfileID(permissions)
	if mode == "" {
		mode = "default"
	}
	network := "restricted"
	if permissions == nil || permissions.Profile == nil || permissions.Profile.AllowsNetwork() {
		network = "enabled"
	}
	var detail string
	switch {
	case permissions != nil && permissions.Profile != nil && permissions.Profile.Disabled:
		detail = "No filesystem sandboxing - all commands are permitted."
	case mode == sandbox.BuiltInPermissionProfileReadOnly || mode == ":read-only":
		detail = "Filesystem access is read-only unless additional permissions are granted."
	case mode == sandbox.BuiltInPermissionProfileWorkspace || mode == ":workspace" || mode == "workspace-write":
		detail = "Filesystem writes are restricted to the current workspace unless additional permissions are granted."
	default:
		detail = "Filesystem access follows the configured permission profile."
	}
	approval := ""
	if approvalPolicy == sandbox.ApprovalNever {
		// Match Rust PermissionsInstructions: when approvals can never be
		// requested, explicitly prevent the model from adding a per-command
		// sandbox override. Without this instruction models commonly add
		// require_escalated to network commands even when the active profile
		// already grants network access, and the command is then rejected.
		approval = "\nApproval policy is currently never. Do not provide the `sandbox_permissions` for any reason, commands will be rejected."
	}
	return fmt.Sprintf("<permissions instructions>\nFilesystem sandboxing defines which files can be read or written. `sandbox_mode` is `%s`: %s Network access is %s.%s\n</permissions instructions>", mode, detail, network, approval)
}

func execEnvironmentContext(req *Request, permissions *config.SandboxPermissionProfileResolution, now time.Time) string {
	cwd := absoluteRequestCWD(req)
	shell := defaultEnvironmentShellName()
	if now.IsZero() {
		now = time.Now()
	}
	timezone := now.Location().String()
	if timezone == "" {
		timezone = time.Local.String()
	}
	var b strings.Builder
	b.WriteString("<environment_context>\n")
	fmt.Fprintf(&b, "  <cwd>%s</cwd>\n", xmlEscape(cwd))
	fmt.Fprintf(&b, "  <shell>%s</shell>\n", xmlEscape(shell))
	fmt.Fprintf(&b, "  <current_date>%s</current_date>\n", xmlEscape(now.Format("2006-01-02")))
	fmt.Fprintf(&b, "  <timezone>%s</timezone>\n", xmlEscape(timezone))
	fmt.Fprintf(&b, "  <filesystem><workspace_roots><root>%s</root></workspace_roots>%s</filesystem>\n", xmlEscape(cwd), execPermissionProfileXML(permissions))
	b.WriteString("</environment_context>")
	return b.String()
}

func absoluteRequestCWD(req *Request) string {
	cwd := requestCWD(req)
	if strings.TrimSpace(cwd) == "" {
		cwd = "."
	}
	abs, err := filepath.Abs(cwd)
	if err != nil {
		return filepath.Clean(cwd)
	}
	return filepath.Clean(abs)
}

func defaultEnvironmentShellName() string {
	if runtime.GOOS == "windows" {
		return "powershell"
	}
	if shell := strings.TrimSpace(os.Getenv("SHELL")); shell != "" {
		return filepath.Base(shell)
	}
	return "bash"
}

func execPermissionProfileXML(permissions *config.SandboxPermissionProfileResolution) string {
	if permissions != nil && permissions.Profile != nil && permissions.Profile.Disabled {
		return `<permission_profile type="disabled"><file_system type="unrestricted" /></permission_profile>`
	}
	mode := sandboxPermissionProfileID(permissions)
	if mode == "" {
		mode = "default"
	}
	network := "restricted"
	if permissions == nil || permissions.Profile == nil || permissions.Profile.AllowsNetwork() {
		network = "enabled"
	}
	return fmt.Sprintf(`<permission_profile type="%s" network="%s" />`, xmlEscape(mode), xmlEscape(network))
}

func xmlEscape(value string) string {
	var b strings.Builder
	if err := xml.EscapeText(&b, []byte(value)); err != nil {
		return value
	}
	return b.String()
}

func loadOutputSchema(path string) (any, error) {
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("Failed to read output schema file %s: %w", path, err)
	}
	var value any
	if err := json.Unmarshal(data, &value); err != nil {
		return nil, fmt.Errorf("Output schema file %s is not valid JSON: %w", path, err)
	}
	return value, nil
}

func baseInstructionsForConfig(cfg *config.Config) (string, error) {
	if path := stringConfigValue(cfg, "model_instructions_file"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return "", fmt.Errorf("failed to read model instructions file %s: %w", path, err)
		}
		text := strings.TrimSpace(string(data))
		if text == "" {
			return "", fmt.Errorf("model instructions file is empty: %s", path)
		}
		return text, nil
	}
	return stringConfigValue(cfg, "instructions"), nil
}

func baseInstructionsForRequest(req *Request, cfg *config.Config) (string, error) {
	if req != nil && req.Exec.Subcommand == "review" {
		return review.ReviewPrompt, nil
	}
	return baseInstructionsForConfig(cfg)
}

func newThreadID() string {
	// Match Rust ThreadId::new(): fresh threads receive a UUIDv7. In
	// particular, the ID must not be derived from the prompt because it is also
	// used as the Responses prompt_cache_key and must not be shared by separate
	// sessions that happen to start with identical text.
	if id, err := uuid.NewV7(); err == nil {
		return id.String()
	}
	return uuid.NewString()
}

func deterministicTurnID(prompt string) string {
	return "turn-" + uuid.NewString()
}

func mergedOverrides(root, exec []string) []string {
	out := make([]string, 0, len(root)+len(exec))
	out = append(out, root...)
	out = append(out, exec...)
	return out
}

func emitFinalEventsFromAgentResult(sink *execEventSink, result *turn.AgentLoopResult, compactedThisTurn bool) error {
	if sink == nil {
		return errors.New("event sink is nil")
	}
	if result == nil || result.Response == nil {
		return errors.New("agent response is nil")
	}
	usage := agentUsageForResult(result)
	executionIndex := 0
	todoLists := &execTodoListState{}
	for _, response := range result.ModelResponses() {
		if response == nil {
			continue
		}
		toolItemCount := 0
		for i := range response.Items {
			item := response.Items[i]
			if isToolAgentItemForExecEvents(&item) {
				toolItemCount++
				continue
			}
			protocolItem := protocolItemFromStreamAgentItem(&item)
			if protocolItem.ID == "" {
				continue
			}
			if err := sink.Emit(protocol.ItemCompleted(protocolItem)); err != nil {
				return err
			}
		}
		toolExecutions := execToolExecutionsForResponse(result.ToolExecutions, executionIndex, toolItemCount)
		for i := range toolExecutions {
			for _, event := range eventsFromToolCallExecution(&toolExecutions[i]) {
				if err := sink.Emit(event); err != nil {
					return err
				}
			}
		}
		for i := range toolExecutions {
			if isPlanUpdateExecution(&toolExecutions[i]) {
				for _, event := range todoLists.eventsForPlanUpdate(&toolExecutions[i]) {
					if err := sink.Emit(event); err != nil {
						return err
					}
				}
				continue
			}
			if event, ok := eventFromToolOutputExecution(&toolExecutions[i]); ok {
				if err := sink.Emit(event); err != nil {
					return err
				}
			}
		}
		executionIndex += len(toolExecutions)
		if len(response.Items) == 0 && strings.TrimSpace(response.Message) != "" {
			item := protocol.AgentMessageItem("agent-message", response.Message)
			if err := sink.Emit(protocol.ItemCompleted(item)); err != nil {
				return err
			}
		}
	}
	for executionIndex < len(result.ToolExecutions) {
		if isPlanUpdateExecution(&result.ToolExecutions[executionIndex]) {
			for _, event := range todoLists.eventsForPlanUpdate(&result.ToolExecutions[executionIndex]) {
				if err := sink.Emit(event); err != nil {
					return err
				}
			}
			executionIndex++
			continue
		}
		for _, event := range eventsFromToolExecution(&result.ToolExecutions[executionIndex]) {
			if err := sink.Emit(event); err != nil {
				return err
			}
		}
		executionIndex++
	}
	if event, ok := todoLists.completionEvent(); ok {
		if err := sink.Emit(event); err != nil {
			return err
		}
	}
	if compactedThisTurn {
		// Rust emits the compaction warning as an error item after the resumed
		// turn's final message and before turn.completed. Emit it in the same
		// position so the SDK event sequence matches.
		if err := sink.Emit(protocol.ItemCompleted(protocol.ErrorItem("compaction-warning", execCompactionWarningMessage))); err != nil {
			return err
		}
	}
	return sink.Emit(protocol.TurnCompleted(protocol.Usage{
		InputTokens:           usage.InputTokens,
		CachedInputTokens:     usage.CachedInputTokens,
		CacheWriteInputTokens: usage.CacheWriteInputTokens,
		OutputTokens:          usage.OutputTokens,
		ReasoningOutputTokens: usage.ReasoningOutputTokens,
	}))
}

func isToolAgentItemForExecEvents(item *model.AgentItem) bool {
	if item == nil {
		return false
	}
	switch item.Type {
	case "function_call", "custom_tool_call":
		return true
	default:
		return false
	}
}

func agentUsageForResult(result *turn.AgentLoopResult) model.AgentUsage {
	if result == nil {
		return model.AgentUsage{}
	}
	usage := result.Usage
	if usage == (model.AgentUsage{}) && result.Response != nil {
		usage = result.Response.Usage
	}
	return usage
}

func blendedHumanTokenTotal(usage model.AgentUsage) int64 {
	cachedInput := usage.CachedInputTokens
	if cachedInput < 0 {
		cachedInput = 0
	}
	nonCachedInput := usage.InputTokens - cachedInput
	if nonCachedInput < 0 {
		nonCachedInput = 0
	}
	output := usage.OutputTokens
	if output < 0 {
		output = 0
	}
	return nonCachedInput + output
}

func formatIntWithSeparators(value int64) string {
	if value < 0 {
		return "-" + formatIntWithSeparators(-value)
	}
	text := fmt.Sprintf("%d", value)
	if len(text) <= 3 {
		return text
	}
	first := len(text) % 3
	if first == 0 {
		first = 3
	}
	var out strings.Builder
	out.Grow(len(text) + (len(text)-1)/3)
	out.WriteString(text[:first])
	for i := first; i < len(text); i += 3 {
		out.WriteByte(',')
		out.WriteString(text[i : i+3])
	}
	return out.String()
}

func writeHumanConfigSummary(stderr io.Writer, req *Request, cfg *config.Config, promptSummary string, threadID string, modelID string, providerID string, approvalPolicy sandbox.AskForApproval, permissions *config.SandboxPermissionProfileResolution, reasoningEffort string) {
	if stderr == nil {
		return
	}
	fmt.Fprintf(stderr, "gcode v%s\n", execHumanVersion())
	fmt.Fprintln(stderr, "--------")
	fmt.Fprintf(stderr, "workdir: %s\n", absoluteRequestCWD(req))
	fmt.Fprintf(stderr, "model: %s\n", displayHumanConfigValue(modelID, "default"))
	fmt.Fprintf(stderr, "provider: %s\n", displayHumanConfigValue(providerID, model.OpenAIProviderID))
	fmt.Fprintf(stderr, "approval: %s\n", approvalPolicy)
	fmt.Fprintf(stderr, "sandbox: %s\n", execHumanSandboxSummary(permissions, requestCWD(req)))
	fmt.Fprintf(stderr, "reasoning effort: %s\n", displayHumanConfigValue(reasoningEffort, "none"))
	fmt.Fprintf(stderr, "reasoning summaries: %s\n", displayHumanConfigValue(effectiveReasoningSummary(cfg), "none"))
	fmt.Fprintf(stderr, "session id: %s\n", threadID)
	fmt.Fprintln(stderr, "--------")
	fmt.Fprintln(stderr, "user")
	fmt.Fprintln(stderr, promptSummary)
}

func execHumanVersion() string {
	return "dev"
}

func displayHumanConfigValue(value string, fallback string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	return value
}

func execHumanSandboxSummary(resolution *config.SandboxPermissionProfileResolution, cwd string) string {
	profile := sandboxPermissionProfile(resolution)
	if profile == nil {
		return "read-only"
	}
	if profile.Disabled {
		return "danger-full-access"
	}
	policy := profile.LegacySandboxPolicy()
	summary := sandbox.SandboxPolicyTag(policy, cwd)
	if policy != nil && policy.Kind == "external-sandbox" && policy.HasFullNetworkAccess() {
		return summary + " (network access enabled)"
	}
	return summary
}

func effectiveReasoningSummary(cfg *config.Config) string {
	return firstNonEmpty(
		stringConfigValue(cfg, "model_reasoning_summary"),
		stringConfigValue(cfg, "modelReasoningSummary"),
		stringConfigValue(cfg, "reasoning_summary"),
		stringConfigValue(cfg, "reasoningSummary"),
	)
}

func writeHumanFinalMessage(stdout io.Writer, stderr io.Writer, message string) error {
	if message == "" {
		return nil
	}
	if isTerminalWriter(stdout) && isTerminalWriter(stderr) {
		_, err := fmt.Fprintf(stderr, "codex\n%s\n", message)
		return err
	}
	_, err := fmt.Fprintln(stdout, message)
	return err
}

func isTerminalWriter(writer io.Writer) bool {
	if writer == nil {
		return false
	}
	terminal, ok := writer.(interface{ IsTerminal() bool })
	return ok && terminal.IsTerminal()
}

func finalMessageForAgentResult(result *turn.AgentLoopResult) (string, bool) {
	if result == nil {
		return "", false
	}
	responses := result.ModelResponses()
	for i := len(responses) - 1; i >= 0; i-- {
		if message, ok := finalMessageFromAgentResponse(responses[i]); ok {
			return message, true
		}
	}
	return finalMessageFromAgentResponse(result.Response)
}

func finalMessageForRequest(req *Request, result *turn.AgentLoopResult) (string, bool) {
	message, ok := finalMessageForAgentResult(result)
	if !ok {
		return message, ok
	}
	if req != nil && req.Exec.Subcommand == "review" {
		return review.RenderOutputText(review.ParseOutputEvent(message)), true
	}
	return message, true
}

func finalMessageFromAgentResponse(response *model.AgentResponse) (string, bool) {
	if response == nil {
		return "", false
	}
	if response.Message != "" {
		return response.Message, true
	}
	for i := len(response.Items) - 1; i >= 0; i-- {
		item := response.Items[i]
		if item.Type == "" || item.Type == "agent_message" {
			return item.Text, true
		}
	}
	for i := len(response.Items) - 1; i >= 0; i-- {
		item := response.Items[i]
		if item.Type == "plan" {
			return item.Text, true
		}
	}
	return "", false
}

func eventsFromAgentResponse(threadID string, response *model.AgentResponse, executions []turn.ToolExecutionResult, streamEvents []protocol.ThreadEvent) []protocol.ThreadEvent {
	events := []protocol.ThreadEvent{
		protocol.ThreadStarted(threadID),
		protocol.TurnStarted(),
	}
	events = append(events, streamEvents...)
	todoLists := &execTodoListState{}
	for i := range executions {
		if isPlanUpdateExecution(&executions[i]) {
			events = append(events, todoLists.eventsForPlanUpdate(&executions[i])...)
			continue
		}
		events = append(events, eventsFromToolExecution(&executions[i])...)
	}
	for _, item := range response.Items {
		if item.Type == "" || item.Type == "agent_message" {
			events = append(events, protocol.ItemCompleted(protocol.AgentMessageItemWithPhase(item.ID, item.Text, agentMessagePhase(&item))))
		}
	}
	if event, ok := todoLists.completionEvent(); ok {
		events = append(events, event)
	}
	events = append(events, protocol.TurnCompleted(protocol.Usage{
		InputTokens:           response.Usage.InputTokens,
		CachedInputTokens:     response.Usage.CachedInputTokens,
		CacheWriteInputTokens: response.Usage.CacheWriteInputTokens,
		OutputTokens:          response.Usage.OutputTokens,
		ReasoningOutputTokens: response.Usage.ReasoningOutputTokens,
	}))
	return events
}

func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func protocolItemFromStreamAgentItem(item *model.AgentItem) protocol.ThreadItem {
	if item == nil {
		return protocol.ThreadItem{}
	}
	switch item.Type {
	case "tool_search_call", "web_search_call":
		return protocolWebSearchItemFromAgentItem(item)
	case "function_call", "custom_tool_call":
		return protocol.ToolCallItemWithCallID(
			firstNonEmpty(item.ID, "tool-call-"+safeSessionItemID(item.CallID)),
			item.CallID,
			firstNonEmpty(item.Name, item.Namespace, item.Type),
			firstNonEmpty(item.Arguments, item.Input),
		)
	case "", "agent_message":
		return protocol.AgentMessageItemWithPhase(firstNonEmpty(item.ID, "agent-message"), item.Text, agentMessagePhase(item))
	case "reasoning":
		text := reasoningSummaryText(item.Data)
		if strings.TrimSpace(text) == "" {
			return protocol.ThreadItem{}
		}
		return protocol.ThreadItem{
			ID:   firstNonEmpty(item.ID, "reasoning"),
			Type: "reasoning",
			Text: text,
		}
	case "image_generation_call":
		return protocolImageGenerationItemFromAgentItem(item)
	default:
		return protocol.ThreadItem{
			ID:   firstNonEmpty(item.ID, item.CallID),
			Type: item.Type,
			Text: firstNonEmpty(item.Text, item.Arguments, item.Input),
		}
	}
}

func agentMessagePhase(item *model.AgentItem) string {
	if item == nil {
		return ""
	}
	for _, key := range []string{"phase", "messagePhase", "message_phase"} {
		if value, ok := item.Data[key].(string); ok {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func streamAgentItemLooksLikeMCP(item *model.AgentItem) bool {
	if item == nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(item.Namespace), mcp.LegacyMCPToolNamePrefix) ||
		strings.HasPrefix(strings.TrimSpace(item.Name), mcp.LegacyMCPToolNamePrefix)
}

func streamAgentItemLooksLikeWebSearch(item *model.AgentItem) bool {
	return item != nil &&
		strings.TrimSpace(item.Namespace) == turn.WebSearchNamespace &&
		strings.TrimSpace(item.Name) == turn.WebSearchRunTool
}

func streamAgentItemLooksLikeCollaboration(item *model.AgentItem) bool {
	if item == nil || (item.Type != "function_call" && item.Type != "custom_tool_call") {
		return false
	}
	namespace := strings.TrimSpace(item.Namespace)
	if namespace == multiagent.MultiAgentV1Namespace || namespace == multiagent.MultiAgentV2Namespace {
		return true
	}
	name := strings.TrimSpace(item.Name)
	return strings.HasPrefix(name, multiagent.MultiAgentV1Namespace+".") ||
		strings.HasPrefix(name, multiagent.MultiAgentV2Namespace+".")
}

func invocationLooksLikeMCP(invocation *tool.Invocation) bool {
	return invocation != nil && strings.HasPrefix(
		strings.TrimSpace(invocation.ToolName.Namespace),
		mcp.LegacyMCPToolNamePrefix,
	)
}

func protocolWebSearchItemFromAgentItem(item *model.AgentItem) protocol.ThreadItem {
	if item == nil {
		return protocol.ThreadItem{}
	}
	query, action := webSearchActionFromAgentItem(item)
	return protocol.WebSearchItem(firstNonEmpty(item.ID, item.CallID, "web-search"), query, action)
}

func webSearchActionFromAgentItem(item *model.AgentItem) (string, map[string]any) {
	if item == nil {
		return "", map[string]any{"type": "other"}
	}
	search := cloneMap(item.Search)
	if len(search) == 0 {
		search = responseArgumentsMapForExecEvents(item.Arguments)
	}
	if nested, ok := search["action"].(map[string]any); ok {
		action := cloneMap(nested)
		return firstNonEmpty(execStringFromAny(search["query"]), execStringFromAny(action["query"])), webSearchActionWithDefault(action)
	}
	query := execStringFromAny(search["query"])
	queries := stringListFromAny(search["queries"])
	if query != "" || len(queries) > 0 {
		action := map[string]any{"type": "search"}
		if query != "" {
			action["query"] = query
		}
		if len(queries) > 0 {
			action["queries"] = queries
			if query == "" {
				query = queries[0]
			}
		}
		return query, action
	}
	url := execStringFromAny(search["url"])
	pattern := execStringFromAny(search["pattern"])
	if url != "" && pattern != "" {
		return "", map[string]any{"type": "find_in_page", "url": url, "pattern": pattern}
	}
	if url != "" {
		return "", map[string]any{"type": "open_page", "url": url}
	}
	return "", map[string]any{"type": "other"}
}

func responseArgumentsMapForExecEvents(arguments string) map[string]any {
	if strings.TrimSpace(arguments) == "" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(arguments), &out); err != nil {
		return nil
	}
	return out
}

func webSearchActionWithDefault(action map[string]any) map[string]any {
	if len(action) == 0 {
		return map[string]any{"type": "other"}
	}
	if execStringFromAny(action["type"]) == "" {
		action["type"] = "other"
	}
	return action
}

func stringListFromAny(value any) []string {
	switch typed := value.(type) {
	case []string:
		return append([]string(nil), typed...)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			text := execStringFromAny(item)
			if text != "" {
				out = append(out, text)
			}
		}
		return out
	default:
		return nil
	}
}

func protocolImageGenerationItemFromAgentItem(item *model.AgentItem) protocol.ThreadItem {
	if item == nil {
		return protocol.ThreadItem{}
	}
	data := item.Data
	result := firstNonEmpty(execStringFromAny(data["result"]), strings.TrimSpace(item.Text))
	status := model.NormalizeImageGenerationStatus(firstNonEmpty(strings.TrimSpace(item.Status), execStringFromAny(data["status"])), result)
	revisedPrompt := firstNonEmpty(execStringFromAny(data["revisedPrompt"]), execStringFromAny(data["revised_prompt"]))
	savedPath := firstNonEmpty(execStringFromAny(data["savedPath"]), execStringFromAny(data["saved_path"]))
	transparentBackground := execBoolPtrFromAny(firstExecAny(data, "transparentBackground", "transparent_background"))
	built := protocol.ImageGenerationItem(firstNonEmpty(item.ID, item.CallID, "image-generation"), status, revisedPrompt, savedPath, transparentBackground)
	// Rust #38024: usage-limit failures ride on the item as a typed failure.
	if failure := data["failure"]; failure != nil {
		if built.Metadata == nil {
			built.Metadata = map[string]any{}
		}
		built.Metadata["failure"] = failure
	}
	return built
}

func firstExecAny(data map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := data[key]; ok {
			return value
		}
	}
	return nil
}

func reasoningSummaryText(data map[string]any) string {
	values := reasoningSummaryStrings(data)
	if len(values) == 0 {
		return ""
	}
	return strings.Join(values, "\n")
}

func reasoningSummaryStrings(data map[string]any) []string {
	if data == nil {
		return nil
	}
	value, ok := data["summary"]
	if !ok {
		return nil
	}
	switch typed := value.(type) {
	case string:
		if strings.TrimSpace(typed) == "" {
			return nil
		}
		return []string{typed}
	case []string:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			if strings.TrimSpace(item) != "" {
				out = append(out, item)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			switch value := item.(type) {
			case string:
				if strings.TrimSpace(value) != "" {
					out = append(out, value)
				}
			case map[string]any:
				if text, ok := value["text"].(string); ok && strings.TrimSpace(text) != "" {
					out = append(out, text)
				}
			}
		}
		return out
	default:
		return nil
	}
}

type execTodoListState struct {
	id    string
	items []protocol.TodoItem
}

func (s *execTodoListState) eventsForPlanUpdate(execution *turn.ToolExecutionResult) []protocol.ThreadEvent {
	if s == nil || execution == nil || execution.Output == nil {
		return nil
	}
	items := todoItemsFromPlanUpdateOutput(execution.Output)
	if s.id == "" {
		s.id = todoListIDFromPlanUpdateExecution(execution)
		s.items = append([]protocol.TodoItem(nil), items...)
		return []protocol.ThreadEvent{protocol.ItemStarted(protocol.TodoListItem(s.id, s.items))}
	}
	s.items = append([]protocol.TodoItem(nil), items...)
	return []protocol.ThreadEvent{protocol.ItemUpdated(protocol.TodoListItem(s.id, s.items))}
}

func (s *execTodoListState) completionEvent() (protocol.ThreadEvent, bool) {
	if s == nil || s.id == "" {
		return protocol.ThreadEvent{}, false
	}
	items := append([]protocol.TodoItem(nil), s.items...)
	return protocol.ItemCompleted(protocol.TodoListItem(s.id, items)), true
}

func todoListIDFromPlanUpdateExecution(execution *turn.ToolExecutionResult) string {
	if execution == nil || execution.Invocation == nil {
		return "todo-list"
	}
	if execution.Output != nil {
		if id := firstNonEmpty(
			execStringFromAny(execution.Output.Data["item_id"]),
			execStringFromAny(execution.Output.Data["itemId"]),
		); id != "" {
			return id
		}
	}
	return "todo-list-" + safeSessionItemID(execution.Invocation.CallID)
}

func eventsFromToolExecution(execution *turn.ToolExecutionResult) []protocol.ThreadEvent {
	if isViewImageExecution(execution) {
		return nil
	}
	if isExecWebSearchExecution(execution) {
		query, action := webSearchActionFromToolExecution(execution)
		return []protocol.ThreadEvent{protocol.ItemCompleted(protocol.WebSearchItem(
			firstNonEmpty(execution.Invocation.CallID, "web-search"),
			query,
			action,
		))}
	}
	if execution != nil && execution.Invocation != nil && execution.Output != nil && execution.Invocation.ToolName.Name == tool.CodeModeExecToolName {
		// Rust keeps nested code-mode tool calls inside the outer exec delegate
		// boundary. They are observable in the exec result and rollout trace, but
		// are not emitted as top-level SDK command/file/MCP items.
		return nil
	}
	if isPlanUpdateExecution(execution) {
		todoLists := &execTodoListState{}
		events := todoLists.eventsForPlanUpdate(execution)
		if event, ok := todoLists.completionEvent(); ok {
			events = append(events, event)
		}
		return events
	}
	events := eventsFromToolCallExecution(execution)
	if event, ok := eventFromToolOutputExecution(execution); ok {
		events = append(events, event)
	}
	return events
}

func isExecWebSearchExecution(execution *turn.ToolExecutionResult) bool {
	return execution != nil && isExecWebSearchInvocation(execution.Invocation)
}

func webSearchActionFromToolExecution(execution *turn.ToolExecutionResult) (string, map[string]any) {
	if execution == nil || execution.Output == nil {
		return "", map[string]any{"type": "other"}
	}
	action, _ := execution.Output.Data["web_search_action"].(map[string]any)
	action = cloneMap(action)
	switch execStringFromAny(action["type"]) {
	case "openPage":
		action["type"] = "open_page"
	case "findInPage":
		action["type"] = "find_in_page"
	}
	query := execStringFromAny(action["query"])
	if query == "" {
		if queries := stringListFromAny(action["queries"]); len(queries) != 0 {
			query = queries[0]
		}
	}
	return query, webSearchActionWithDefault(action)
}

func eventsFromToolCallExecution(execution *turn.ToolExecutionResult) []protocol.ThreadEvent {
	if execution == nil || execution.Invocation == nil {
		return nil
	}
	if isViewImageExecution(execution) {
		return nil
	}
	if isExecWebSearchExecution(execution) {
		return []protocol.ThreadEvent{protocol.ItemStarted(protocol.WebSearchItem(
			firstNonEmpty(execution.Invocation.CallID, "web-search"),
			"",
			map[string]any{"type": "other"},
		))}
	}
	if isCollabExecution(execution) {
		item := collabToolCallProtocolItem(execution, "in_progress")
		return []protocol.ThreadEvent{protocol.ItemStarted(item)}
	}
	if isCollaborationExecution(execution) {
		// Rust's TypeScript SDK currently exposes v2 wait_agent as a
		// collab_tool_call, but does not surface the other v2 collaboration
		// functions as generic public SDK items.
		return nil
	}
	if isMCPExecution(execution) {
		item := mcpToolCallProtocolItem(execution, "in_progress")
		return []protocol.ThreadEvent{protocol.ItemStarted(item)}
	}
	if execution.Invocation.Payload.Kind == tool.PayloadToolSearch {
		return nil
	}
	if execution.Invocation.ToolName.Name == tool.CodeModeExecToolName {
		return nil
	}
	if execution.Invocation.ToolName.Namespace == "" && execution.Invocation.ToolName.Name == "wait" {
		return nil
	}
	if isWriteStdinExecution(execution) {
		return nil
	}
	if isCommandExecution(execution) {
		item := commandExecutionProtocolItem(execution, "in_progress")
		return []protocol.ThreadEvent{protocol.ItemStarted(item)}
	}
	if isPlanUpdateExecution(execution) {
		return nil
	}
	if isFileChangeExecution(execution) {
		item := protocol.FileChangeItem(
			"file-change-"+safeSessionItemID(execution.Invocation.CallID),
			fileChangesFromToolOutput(execution.Output),
			"in_progress",
		)
		autoApproved := true
		item.AutoApproved = &autoApproved
		return []protocol.ThreadEvent{protocol.ItemStarted(item)}
	}
	if isApplyPatchExecution(execution) {
		return nil
	}
	callID := execution.Invocation.CallID
	toolName := execution.Invocation.ToolName.Key()
	input := toolInvocationText(execution.Invocation)
	return []protocol.ThreadEvent{
		protocol.ItemStarted(protocol.ToolCallItemWithCallID("tool-call-"+safeSessionItemID(callID), callID, toolName, input)),
		protocol.ItemCompleted(protocol.ToolCallItemWithCallID("tool-call-"+safeSessionItemID(callID), callID, toolName, input)),
	}
}

func eventFromToolOutputExecution(execution *turn.ToolExecutionResult) (protocol.ThreadEvent, bool) {
	if execution == nil || execution.Invocation == nil || execution.Output == nil {
		return protocol.ThreadEvent{}, false
	}
	if isViewImageExecution(execution) {
		return protocol.ThreadEvent{}, false
	}
	if isExecWebSearchExecution(execution) {
		query, action := webSearchActionFromToolExecution(execution)
		return protocol.ItemCompleted(protocol.WebSearchItem(
			firstNonEmpty(execution.Invocation.CallID, "web-search"),
			query,
			action,
		)), true
	}
	if isPlanUpdateExecution(execution) {
		items := todoItemsFromPlanUpdateOutput(execution.Output)
		return protocol.ItemCompleted(protocol.TodoListItem("todo-list-"+safeSessionItemID(execution.Invocation.CallID), items)), true
	}
	if execution.Invocation.Payload.Kind == tool.PayloadToolSearch {
		return protocol.ThreadEvent{}, false
	}
	if execution.Invocation.ToolName.Name == tool.CodeModeExecToolName {
		return protocol.ThreadEvent{}, false
	}
	if execution.Invocation.ToolName.Namespace == "" && execution.Invocation.ToolName.Name == "wait" {
		return protocol.ThreadEvent{}, false
	}
	if isFileChangeExecution(execution) {
		changes := fileChangesFromToolOutput(execution.Output)
		status := fileChangeStatusFromToolOutput(execution.Output)
		return protocol.ItemCompleted(protocol.FileChangeItemWithOutput(
			"file-change-"+safeSessionItemID(execution.Invocation.CallID),
			changes,
			status,
			execStringFromAny(execution.Output.Data["stdout"]),
			firstNonEmpty(execStringFromAny(execution.Output.Data["stderr"]), execution.Output.Error),
		)), true
	}
	if isApplyPatchExecution(execution) {
		return protocol.ThreadEvent{}, false
	}
	if isCollabExecution(execution) {
		return protocol.ItemCompleted(collabToolCallProtocolItem(execution, collabToolCallStatusFromOutput(execution.Output))), true
	}
	if isCollaborationExecution(execution) {
		return protocol.ThreadEvent{}, false
	}
	if isMCPExecution(execution) {
		return protocol.ItemCompleted(mcpToolCallProtocolItem(execution, mcpToolCallStatusFromOutput(execution.Output))), true
	}
	if isWriteStdinExecution(execution) {
		if _, running := intFromAny(execution.Output.Data["process_id"]); running {
			return protocol.ThreadEvent{}, false
		}
		if _, exited := intFromAny(execution.Output.Data["exit_code"]); exited {
			return protocol.ItemCompleted(commandExecutionProtocolItemFromOutput(execution.Output, "completed")), true
		}
	}
	if isCommandExecution(execution) {
		if _, running := intFromAny(execution.Output.Data["process_id"]); running {
			return protocol.ThreadEvent{}, false
		}
		return protocol.ItemCompleted(commandExecutionProtocolItem(execution, commandExecutionStatusFromOutput(execution.Output))), true
	}
	callID := execution.Invocation.CallID
	toolName := execution.Invocation.ToolName.Key()
	return protocol.ItemCompleted(protocol.ToolOutputItemWithCallID("tool-output-"+safeSessionItemID(callID), callID, toolName, execution.Output.Body, execution.Output.Success, cloneEventMetadata(execution.Output.Data))), true
}

func isViewImageExecution(execution *turn.ToolExecutionResult) bool {
	return execution != nil && execution.Invocation != nil && execution.Invocation.ToolName.Key() == tool.ViewImageToolName
}

func cloneEventMetadata(metadata map[string]any) map[string]any {
	if len(metadata) == 0 {
		return nil
	}
	out := make(map[string]any, len(metadata))
	for key, value := range metadata {
		out[key] = value
	}
	return out
}

func isPlanUpdateExecution(execution *turn.ToolExecutionResult) bool {
	if execution == nil || execution.Invocation == nil || execution.Output == nil {
		return false
	}
	if execution.Invocation.ToolName.Key() != "update_plan" && execution.Output.ToolName.Key() != "update_plan" {
		return false
	}
	marker, _ := execution.Output.Data["planUpdate"].(bool)
	return marker
}

func isCommandExecution(execution *turn.ToolExecutionResult) bool {
	if execution == nil || execution.Invocation == nil || execution.Output == nil {
		return false
	}
	if !tool.IsShellCommandToolName(execution.Invocation.ToolName) {
		return false
	}
	if _, ok := intFromAny(execution.Output.Data["exit_code"]); ok {
		return true
	}
	if _, ok := intFromAny(execution.Output.Data["process_id"]); ok {
		return true
	}
	return !execution.Output.Success && strings.Contains(strings.ToLower(outputBodyText(execution.Output)), "approval policy is never")
}

func outputBodyText(output *tool.Output) string {
	if output == nil {
		return ""
	}
	return output.Body
}

func isWriteStdinExecution(execution *turn.ToolExecutionResult) bool {
	return execution != nil && execution.Invocation != nil && execution.Output != nil && execution.Invocation.ToolName.Key() == tool.DefaultWriteStdinToolName
}

func commandExecutionProtocolItem(execution *turn.ToolExecutionResult, status string) protocol.ThreadItem {
	if execution == nil || execution.Invocation == nil {
		return protocol.ThreadItem{}
	}
	command := commandFromShellInvocation(execution.Invocation)
	aggregated := ""
	var exitCode *int
	if execution.Output != nil && status != "in_progress" {
		aggregated = commandExecutionAggregatedOutput(execution.Output)
		if code, ok := intFromAny(execution.Output.Data["exit_code"]); ok {
			exitCode = &code
		}
	}
	return protocol.CommandExecutionItem(
		firstNonEmpty(execution.Invocation.CallID, "command-execution"),
		command,
		aggregated,
		exitCode,
		status,
	)
}

func commandExecutionAggregatedOutput(output *tool.Output) string {
	if output == nil {
		return ""
	}
	stdout, stdoutPresent := output.Data["stdout"].(string)
	stderr, stderrPresent := output.Data["stderr"].(string)
	if stdoutPresent || stderrPresent {
		return stdout + stderr
	}
	if hookResponse, ok := output.Data["hook_response"].(string); ok {
		return hookResponse
	}
	return output.Body
}

func commandExecutionProtocolItemFromOutput(output *tool.Output, status string) protocol.ThreadItem {
	if output == nil {
		return protocol.ThreadItem{}
	}
	callID, _ := output.Data["event_call_id"].(string)
	command, _ := output.Data["hook_command"].(string)
	aggregated, _ := output.Data["hook_response"].(string)
	var exitCode *int
	if code, ok := intFromAny(output.Data["exit_code"]); ok {
		exitCode = &code
	}
	return protocol.CommandExecutionItem(firstNonEmpty(callID, output.CallID, "command-execution"), command, aggregated, exitCode, status)
}

func commandFromShellInvocation(invocation *tool.Invocation) string {
	if invocation == nil {
		return ""
	}
	var args struct {
		Cmd     string   `json:"cmd"`
		Command string   `json:"command"`
		Args    []string `json:"args"`
	}
	if raw := strings.TrimSpace(invocation.Payload.Arguments); raw != "" {
		if err := json.Unmarshal([]byte(raw), &args); err == nil {
			if strings.TrimSpace(args.Cmd) != "" {
				return strings.TrimSpace(args.Cmd)
			}
			if strings.TrimSpace(args.Command) != "" {
				return strings.TrimSpace(args.Command)
			}
			if len(args.Args) > 0 {
				return strings.Join(args.Args, " ")
			}
		}
	}
	return strings.TrimSpace(invocation.Payload.Input)
}

func commandExecutionStatusFromOutput(output *tool.Output) string {
	if output == nil {
		return "in_progress"
	}
	if exitCode, ok := intFromAny(output.Data["exit_code"]); ok {
		if exitCode == 0 {
			return "completed"
		}
		return "failed"
	}
	if output.Success {
		return "completed"
	}
	return "failed"
}

func intFromAny(value any) (int, bool) {
	switch typed := value.(type) {
	case int:
		return typed, true
	case int64:
		return int(typed), true
	case float64:
		return int(typed), true
	case json.Number:
		number, err := typed.Int64()
		if err != nil {
			return 0, false
		}
		return int(number), true
	default:
		return 0, false
	}
}

func isCollabExecution(execution *turn.ToolExecutionResult) bool {
	if execution == nil {
		return false
	}
	if execution.Output != nil {
		if marker, _ := execution.Output.Data["collabToolCall"].(bool); marker {
			return true
		}
	}
	if execution.Invocation != nil {
		if _, ok := normalizeCollabToolName(execution.Invocation.ToolName); ok {
			return true
		}
	}
	if execution.Output != nil {
		if _, ok := normalizeCollabToolName(execution.Output.ToolName); ok {
			return true
		}
	}
	return false
}

func isCollaborationExecution(execution *turn.ToolExecutionResult) bool {
	if execution == nil {
		return false
	}
	for _, name := range []tool.ToolName{
		func() tool.ToolName {
			if execution.Invocation != nil {
				return execution.Invocation.ToolName
			}
			return tool.ToolName{}
		}(),
		func() tool.ToolName {
			if execution.Output != nil {
				return execution.Output.ToolName
			}
			return tool.ToolName{}
		}(),
	} {
		namespace := strings.TrimSpace(name.Namespace)
		if namespace == multiagent.MultiAgentV1Namespace || namespace == multiagent.MultiAgentV2Namespace {
			return true
		}
		key := strings.TrimSpace(name.Key())
		if strings.HasPrefix(key, multiagent.MultiAgentV1Namespace+".") ||
			strings.HasPrefix(key, multiagent.MultiAgentV2Namespace+".") {
			return true
		}
	}
	return false
}

func collabToolCallProtocolItem(execution *turn.ToolExecutionResult, status string) protocol.ThreadItem {
	if execution == nil || execution.Invocation == nil {
		return protocol.ThreadItem{}
	}
	toolName, _ := collabToolNameFromExecution(execution)
	prompt := collabPromptFromExecution(execution)
	receiverThreadIDs := collabReceiverThreadIDsFromExecution(execution, status)
	return protocol.CollabToolCallItem(
		firstNonEmpty(execution.Invocation.CallID, "collab-tool-call"),
		toolName,
		collabSenderThreadIDFromExecution(execution),
		receiverThreadIDs,
		prompt,
		collabAgentStatesFromExecution(execution, receiverThreadIDs),
		status,
	)
}

func collabToolNameFromExecution(execution *turn.ToolExecutionResult) (string, bool) {
	if execution != nil && execution.Output != nil {
		if toolName, ok := normalizeCollabToolString(execStringFromAny(execution.Output.Data["tool"])); ok {
			return toolName, true
		}
	}
	if execution != nil && execution.Invocation != nil {
		if toolName, ok := normalizeCollabToolName(execution.Invocation.ToolName); ok {
			return toolName, true
		}
	}
	if execution != nil && execution.Output != nil {
		if toolName, ok := normalizeCollabToolName(execution.Output.ToolName); ok {
			return toolName, true
		}
	}
	return "", false
}

func normalizeCollabToolName(name tool.ToolName) (string, bool) {
	if strings.TrimSpace(name.Namespace) == multiagent.MultiAgentV2Namespace {
		if strings.TrimSpace(name.Name) == "wait_agent" {
			return "wait", true
		}
		return "", false
	}
	if strings.TrimSpace(name.Namespace) == multiagent.MultiAgentV1Namespace {
		if toolName, ok := normalizeCollabToolString(name.Name); ok {
			return toolName, true
		}
		return "", false
	}
	return normalizeCollabToolString(name.Key())
}

func normalizeCollabToolString(raw string) (string, bool) {
	value := strings.ToLower(strings.TrimSpace(raw))
	value = strings.ReplaceAll(value, ".", "_")
	value = strings.ReplaceAll(value, "-", "_")
	switch value {
	case "agent_spawn_agent", "spawn_agent", "spawnagent":
		return "spawn_agent", true
	case "multi_agent_v1_spawn_agent":
		return "spawn_agent", true
	case "agent_send_input", "send_input", "sendinput":
		return "send_input", true
	case "multi_agent_v1_send_input":
		return "send_input", true
	case "agent_wait_agent", "wait_agent", "agent_wait", "waitagent":
		return "wait", true
	case "multi_agent_v1_wait_agent":
		return "wait", true
	case "agent_close_agent", "close_agent", "closeagent":
		return "close_agent", true
	case "multi_agent_v1_close_agent":
		return "close_agent", true
	default:
		return "", false
	}
}

func collabSenderThreadIDFromExecution(execution *turn.ToolExecutionResult) string {
	if execution == nil {
		return ""
	}
	if execution.Output != nil {
		if sender := firstNonEmpty(
			execStringFromAny(execution.Output.Data["sender_thread_id"]),
			execStringFromAny(execution.Output.Data["senderThreadId"]),
		); sender != "" {
			return sender
		}
	}
	if execution.Invocation != nil && execution.Invocation.Context != nil {
		return firstNonEmpty(
			execStringFromAny(execution.Invocation.Context["sender_thread_id"]),
			execStringFromAny(execution.Invocation.Context["senderThreadId"]),
			execStringFromAny(execution.Invocation.Context["thread_id"]),
			execStringFromAny(execution.Invocation.Context["threadId"]),
		)
	}
	return ""
}

func collabPromptFromExecution(execution *turn.ToolExecutionResult) *string {
	if execution == nil {
		return nil
	}
	if execution.Output != nil {
		if prompt := firstNonEmpty(
			execStringFromAny(execution.Output.Data["prompt"]),
			execStringFromAny(execution.Output.Data["message"]),
		); prompt != "" && !looksLikeEncryptedCollaborationText(prompt) {
			return stringPointerIfNotEmpty(prompt)
		}
	}
	if execution.Invocation == nil {
		return nil
	}
	args := toolInvocationArgumentsMap(execution.Invocation)
	if prompt := firstNonEmpty(execStringFromAny(args["prompt"]), execStringFromAny(args["message"])); prompt != "" && !looksLikeEncryptedCollaborationText(prompt) {
		return stringPointerIfNotEmpty(prompt)
	}
	return nil
}

func looksLikeEncryptedCollaborationText(value string) bool {
	return strings.HasPrefix(strings.TrimSpace(value), "gAAAA")
}

func subAgentActivityProtocolItem(execution *turn.ToolExecutionResult) (protocol.ThreadItem, bool) {
	if execution == nil || execution.Invocation == nil || execution.Output == nil || execution.Output.Data == nil {
		return protocol.ThreadItem{}, false
	}
	raw, ok := execution.Output.Data["subAgentActivity"].(map[string]any)
	if !ok {
		return protocol.ThreadItem{}, false
	}
	kind := strings.TrimSpace(execStringFromAny(raw["kind"]))
	if kind == "" {
		return protocol.ThreadItem{}, false
	}
	return protocol.SubAgentActivityItem(
		firstNonEmpty(execution.Invocation.CallID, "sub-agent-activity"),
		kind,
		execStringFromAny(firstNonNil(raw["agent_thread_id"], raw["agentThreadId"])),
		execStringFromAny(firstNonNil(raw["agent_path"], raw["agentPath"])),
	), true
}

func collabReceiverThreadIDsFromExecution(execution *turn.ToolExecutionResult, status string) []string {
	if execution == nil {
		return nil
	}
	if status == "in_progress" {
		return nil
	}
	if execution.Output != nil {
		if values := stringListFromAny(firstNonNil(
			execution.Output.Data["receiver_thread_ids"],
			execution.Output.Data["receiverThreadIds"],
		)); len(values) > 0 {
			return values
		}
		if result, ok := execution.Output.Data["result"]; ok {
			if values := collabReceiverThreadIDsFromResult(result, execution); len(values) > 0 {
				return values
			}
		}
	}
	if execution.Invocation == nil {
		return nil
	}
	args := toolInvocationArgumentsMap(execution.Invocation)
	if values := stringListFromAny(firstNonNil(args["targets"], args["receiver_thread_ids"], args["receiverThreadIds"])); len(values) > 0 {
		return values
	}
	target := firstNonEmpty(execStringFromAny(args["target"]), execStringFromAny(args["id"]))
	if target != "" {
		return []string{target}
	}
	return nil
}

func collabReceiverThreadIDsFromResult(result any, execution *turn.ToolExecutionResult) []string {
	switch typed := result.(type) {
	case map[string]any:
		if id := firstNonEmpty(
			execStringFromAny(typed["agent_id"]),
			execStringFromAny(typed["agentId"]),
			execStringFromAny(typed["target"]),
			execStringFromAny(typed["id"]),
		); id != "" {
			return []string{id}
		}
	case fmt.Stringer:
		value := strings.TrimSpace(typed.String())
		if value != "" {
			return []string{value}
		}
	default:
		data, err := json.Marshal(typed)
		if err == nil {
			var decoded map[string]any
			if err := json.Unmarshal(data, &decoded); err == nil {
				return collabReceiverThreadIDsFromResult(decoded, execution)
			}
		}
	}
	if execution == nil || execution.Invocation == nil {
		return nil
	}
	args := toolInvocationArgumentsMap(execution.Invocation)
	target := firstNonEmpty(execStringFromAny(args["target"]), execStringFromAny(args["id"]))
	if target != "" {
		return []string{target}
	}
	return nil
}

func collabAgentStatesFromExecution(execution *turn.ToolExecutionResult, receiverThreadIDs []string) map[string]protocol.CollabAgentState {
	if execution == nil || execution.Output == nil {
		return nil
	}
	if states := collabAgentStatesFromAny(firstNonNil(
		execution.Output.Data["agents_states"],
		execution.Output.Data["agentsStates"],
	)); len(states) > 0 {
		return states
	}
	if result, ok := execution.Output.Data["result"]; ok {
		if states := collabAgentStatesFromResult(result); len(states) > 0 {
			return states
		}
	}
	if execution.Output.Success {
		if toolName, ok := collabToolNameFromExecution(execution); ok && toolName == "spawn_agent" && len(receiverThreadIDs) > 0 {
			states := map[string]protocol.CollabAgentState{}
			for _, threadID := range receiverThreadIDs {
				states[threadID] = protocol.CollabAgentState{Status: "running"}
			}
			return states
		}
	}
	return nil
}

func collabAgentStatesFromResult(result any) map[string]protocol.CollabAgentState {
	switch typed := result.(type) {
	case map[string]any:
		if status, ok := typed["status"]; ok {
			return collabAgentStatesFromAny(status)
		}
	case map[string]protocol.CollabAgentState:
		return collabAgentStatesFromAny(typed)
	default:
		data, err := json.Marshal(typed)
		if err == nil {
			var decoded map[string]any
			if err := json.Unmarshal(data, &decoded); err == nil {
				return collabAgentStatesFromResult(decoded)
			}
		}
	}
	return nil
}

func collabAgentStatesFromAny(value any) map[string]protocol.CollabAgentState {
	switch typed := value.(type) {
	case nil:
		return nil
	case map[string]protocol.CollabAgentState:
		out := make(map[string]protocol.CollabAgentState, len(typed))
		for key, state := range typed {
			out[key] = protocol.CollabAgentState{
				Status:  normalizeCollabAgentStatus(state.Status),
				Message: cloneExecStringPointer(state.Message),
			}
		}
		return out
	case map[string]any:
		out := make(map[string]protocol.CollabAgentState, len(typed))
		for key, raw := range typed {
			state, ok := collabAgentStateFromAny(raw)
			if ok {
				out[key] = state
			}
		}
		return out
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return nil
		}
		var decoded map[string]any
		if err := json.Unmarshal(data, &decoded); err != nil {
			return nil
		}
		return collabAgentStatesFromAny(decoded)
	}
}

func collabAgentStateFromAny(value any) (protocol.CollabAgentState, bool) {
	switch typed := value.(type) {
	case protocol.CollabAgentState:
		return protocol.CollabAgentState{
			Status:  normalizeCollabAgentStatus(typed.Status),
			Message: cloneExecStringPointer(typed.Message),
		}, true
	case map[string]any:
		return protocol.CollabAgentState{
			Status:  normalizeCollabAgentStatus(execStringFromAny(typed["status"])),
			Message: stringPointerIfNotEmpty(execStringFromAny(typed["message"])),
		}, true
	default:
		status := normalizeCollabAgentStatus(execStringFromAny(typed))
		if status == "" {
			return protocol.CollabAgentState{}, false
		}
		return protocol.CollabAgentState{Status: status}, true
	}
}

func normalizeCollabAgentStatus(status string) string {
	value := strings.ToLower(strings.TrimSpace(status))
	value = strings.ReplaceAll(value, "-", "_")
	switch value {
	case "pendinginit", "pending_init":
		return "pending_init"
	case "running", "interrupted", "completed", "errored", "shutdown":
		return value
	case "notfound", "not_found":
		return "not_found"
	default:
		return value
	}
}

func collabToolCallStatusFromOutput(output *tool.Output) string {
	if output == nil {
		return "in_progress"
	}
	status := normalizeCollabToolCallStatus(execStringFromAny(output.Data["status"]))
	if status != "" {
		return status
	}
	if output.Success {
		return "completed"
	}
	return "failed"
}

func normalizeCollabToolCallStatus(status string) string {
	value := strings.ToLower(strings.TrimSpace(status))
	value = strings.ReplaceAll(value, "-", "_")
	switch value {
	case "inprogress", "in_progress":
		return "in_progress"
	case "completed":
		return "completed"
	case "failed", "declined":
		return "failed"
	default:
		return ""
	}
}

func toolInvocationArgumentsMap(invocation *tool.Invocation) map[string]any {
	if invocation == nil {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(invocation.Payload.Arguments)), &out); err != nil {
		return nil
	}
	return out
}

func firstNonNil(values ...any) any {
	for _, value := range values {
		if value != nil {
			return value
		}
	}
	return nil
}

func cloneExecStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

func isMCPExecution(execution *turn.ToolExecutionResult) bool {
	if execution == nil {
		return false
	}
	if isCollabExecution(execution) {
		return false
	}
	if execution.Invocation != nil && strings.HasPrefix(
		strings.TrimSpace(execution.Invocation.ToolName.Namespace),
		mcp.LegacyMCPToolNamePrefix,
	) {
		return true
	}
	if execution.Output != nil {
		marker, _ := execution.Output.Data["mcpToolCall"].(bool)
		return marker
	}
	return false
}

func mcpToolCallProtocolItem(execution *turn.ToolExecutionResult, status string) protocol.ThreadItem {
	if execution == nil || execution.Invocation == nil {
		return protocol.ThreadItem{}
	}
	invocation := execution.Invocation
	server := ""
	toolName := ""
	if execution.Output != nil {
		server = execStringFromAny(execution.Output.Data["server"])
		toolName = execStringFromAny(execution.Output.Data["tool"])
	}
	if server == "" {
		server = strings.TrimPrefix(strings.TrimSpace(invocation.ToolName.Namespace), mcp.LegacyMCPToolNamePrefix)
	}
	if toolName == "" {
		toolName = strings.TrimSpace(invocation.ToolName.Name)
	}
	var result *protocol.MCPToolResult
	var callErr *protocol.MCPToolError
	if execution.Output != nil && status == "completed" {
		result = mcpToolResultFromOutput(execution.Output)
	}
	if execution.Output != nil && status == "failed" {
		callErr = &protocol.MCPToolError{Message: firstNonEmpty(strings.TrimSpace(execution.Output.Body), "MCP tool call failed")}
	}
	return protocol.MCPToolCallItem(
		firstNonEmpty(invocation.CallID, "mcp-tool-call"),
		server,
		toolName,
		toolInvocationArgumentsAny(invocation),
		result,
		callErr,
		status,
	)
}

func mcpToolCallStatusFromOutput(output *tool.Output) string {
	if output == nil {
		return "in_progress"
	}
	if output.Success {
		return "completed"
	}
	return "failed"
}

func mcpToolResultFromOutput(output *tool.Output) *protocol.MCPToolResult {
	result := &protocol.MCPToolResult{
		Content: anySliceFromAny(output.Data["content"]),
	}
	if result.Content == nil {
		result.Content = []any{}
	}
	if meta, ok := output.Data["_meta"]; ok {
		result.Meta = meta
	}
	if structured, ok := output.Data["structuredContent"]; ok {
		result.StructuredContent = structured
	} else if structured, ok := output.Data["structured_content"]; ok {
		result.StructuredContent = structured
	}
	return result
}

func anySliceFromAny(value any) []any {
	switch typed := value.(type) {
	case []any:
		return append([]any(nil), typed...)
	case []map[string]any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, cloneMap(item))
		}
		return out
	default:
		return nil
	}
}

func toolInvocationArgumentsAny(invocation *tool.Invocation) any {
	if invocation == nil {
		return nil
	}
	if raw := strings.TrimSpace(invocation.Payload.Arguments); raw != "" {
		var value any
		if err := json.Unmarshal([]byte(raw), &value); err == nil {
			return value
		}
		return raw
	}
	return nil
}

func isFileChangeExecution(execution *turn.ToolExecutionResult) bool {
	if execution == nil || execution.Output == nil {
		return false
	}
	marker, _ := execution.Output.Data["fileChange"].(bool)
	return marker
}

func isApplyPatchExecution(execution *turn.ToolExecutionResult) bool {
	return execution != nil && execution.Invocation != nil &&
		execution.Invocation.ToolName.Key() == tool.DefaultApplyPatchToolName
}

func fileChangesFromToolOutput(output *tool.Output) []protocol.FileChange {
	if output == nil {
		return nil
	}
	return fileChangesFromAny(output.Data["changes"])
}

func fileChangesFromAny(value any) []protocol.FileChange {
	switch typed := value.(type) {
	case []map[string]any:
		out := make([]protocol.FileChange, 0, len(typed))
		for _, item := range typed {
			if change, ok := fileChangeFromMap(item); ok {
				out = append(out, change)
			}
		}
		return out
	case []any:
		out := make([]protocol.FileChange, 0, len(typed))
		for _, raw := range typed {
			item, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if change, ok := fileChangeFromMap(item); ok {
				out = append(out, change)
			}
		}
		return out
	default:
		return nil
	}
}

func fileChangeFromMap(item map[string]any) (protocol.FileChange, bool) {
	path := execStringFromAny(item["path"])
	kind := fileChangeKindFromAny(item["kind"])
	if path == "" || kind == "" {
		return protocol.FileChange{}, false
	}
	movePath := ""
	if kindData, ok := item["kind"].(map[string]any); ok {
		movePath = execStringFromAny(kindData["move_path"])
		if movePath == "" {
			movePath = execStringFromAny(kindData["movePath"])
		}
	}
	return protocol.FileChange{Path: path, Kind: kind, Diff: execStringFromAny(item["diff"]), MovePath: movePath}, true
}

func fileChangeKindFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return normalizeFileChangeKind(typed)
	case map[string]any:
		return normalizeFileChangeKind(execStringFromAny(typed["type"]))
	default:
		return ""
	}
}

func normalizeFileChangeKind(kind string) string {
	switch strings.ToLower(strings.TrimSpace(kind)) {
	case "add", "delete", "update":
		return strings.ToLower(strings.TrimSpace(kind))
	default:
		return ""
	}
}

func fileChangeStatusFromToolOutput(output *tool.Output) string {
	if output == nil {
		return "failed"
	}
	status := strings.ToLower(strings.TrimSpace(execStringFromAny(output.Data["status"])))
	switch status {
	case "completed", "in_progress":
		return status
	case "failed", "declined":
		return status
	}
	if output.Success {
		return "completed"
	}
	return "failed"
}

func todoItemsFromPlanUpdateOutput(output *tool.Output) []protocol.TodoItem {
	if output == nil {
		return nil
	}
	return todoItemsFromPlanValue(output.Data["plan"])
}

func todoItemsFromPlanValue(value any) []protocol.TodoItem {
	switch typed := value.(type) {
	case []tool.PlanItem:
		out := make([]protocol.TodoItem, 0, len(typed))
		for _, item := range typed {
			if todo, ok := todoItemFromPlanFields(item.Step, string(item.Status)); ok {
				out = append(out, todo)
			}
		}
		return out
	case []any:
		out := make([]protocol.TodoItem, 0, len(typed))
		for _, raw := range typed {
			if item, ok := raw.(tool.PlanItem); ok {
				if todo, ok := todoItemFromPlanFields(item.Step, string(item.Status)); ok {
					out = append(out, todo)
				}
				continue
			}
			if item, ok := raw.(map[string]any); ok {
				step, _ := item["step"].(string)
				status, _ := item["status"].(string)
				if todo, ok := todoItemFromPlanFields(step, status); ok {
					out = append(out, todo)
				}
			}
		}
		return out
	default:
		return nil
	}
}

func todoItemFromPlanFields(step string, status string) (protocol.TodoItem, bool) {
	step = strings.TrimSpace(step)
	if step == "" {
		return protocol.TodoItem{}, false
	}
	return protocol.TodoItem{
		Text:      step,
		Completed: status == string(tool.PlanCompleted) || status == "completed",
	}, true
}

func (r *Runner) persistSession(req *Request, threadID string, turnID string, userPrompt string, userInputs []turn.TurnUserInput, result *turn.AgentLoopResult, resumeContext *execResumeContext, tokenUsage *protocol.ThreadTokenUsage) (string, error) {
	if req.Exec.Ephemeral {
		return "", nil
	}
	if result == nil || result.Response == nil {
		return "", errors.New("agent response is nil")
	}
	response := result.Response
	now := r.now().UTC()
	store := session.NewStore(filepath.Join(r.CodexHome, "sessions"))
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		return "", err
	}
	resumed := resumeContext != nil && resumeContext.Record != nil
	newUserPrompt := userPrompt
	var itemMetadata map[string]any
	if resumed {
		newUserPrompt = firstNonEmpty(resumeContext.UserPrompt, userPrompt)
		itemMetadata = map[string]any{"resumed": true}
	}
	items := execAdditionalInputSessionItems(turnID, req.AdditionalInputItems, now, itemMetadata)
	items = append(items, sessionItemsForTurnWithMode(turnID, newUserPrompt, userInputs, result, now, itemMetadata, &execImageGenerationContext{
		CodexHome: r.CodexHome,
		ThreadID:  threadID,
	}, execRequestPlanMode(req))...)
	delta := execMissingSessionItems(record.Items, items)
	record.Items = append(record.Items, delta...)
	if strings.TrimSpace(record.SessionID) == "" {
		record.SessionID = execSessionID(req, threadID)
	}
	if strings.TrimSpace(record.Preview) == "" {
		record.Preview = firstLine(firstNonEmpty(newUserPrompt, turnUserInputsSummary(userInputs)))
	}
	if strings.TrimSpace(response.Model) != "" {
		record.Metadata.Model = response.Model
	}
	if strings.TrimSpace(response.ProviderID) != "" {
		record.Metadata.ModelProvider = response.ProviderID
	}
	if strings.TrimSpace(response.ResponseID) != "" {
		record.Metadata.LastResponseID = response.ResponseID
	}
	if strings.TrimSpace(record.Metadata.SessionPrefix) == "" {
		record.Metadata.SessionPrefix = session.PrefixForSessionID(firstNonEmpty(record.SessionID, threadID))
	}
	record.Metadata.Extra = execTokenUsageMetadata(record.Metadata.Extra, tokenUsage)
	execCompleteTurnSnapshot(&record.Metadata, turnID, now)
	record.UpdatedAt = now
	record.RecencyAt = now
	if err := store.Save(record); err != nil {
		return "", err
	}
	if err := r.appendExecRollout(record.ID, delta, record, now); err != nil {
		return "", fmt.Errorf("append exec rollout: %w", err)
	}
	if err := r.appendExecTurnComplete(record, turnID, now); err != nil {
		return "", fmt.Errorf("complete exec rollout turn: %w", err)
	}
	path, err := store.Path(record.ID)
	if err != nil {
		return "", err
	}
	return path, nil
}

func execCompleteTurnSnapshot(metadata *session.Metadata, turnID string, now time.Time) {
	if metadata == nil || strings.TrimSpace(turnID) == "" {
		return
	}
	completedAt := now.Unix()
	for i := range metadata.RolloutTurns {
		if metadata.RolloutTurns[i].ID == turnID {
			metadata.RolloutTurns[i].Status = "completed"
			metadata.RolloutTurns[i].CompletedAt = &completedAt
			return
		}
	}
	metadata.RolloutTurns = append(metadata.RolloutTurns, session.TurnSnapshot{ID: turnID, Status: "completed", CompletedAt: &completedAt})
}

func execMissingSessionItems(existing []session.Item, candidates []session.Item) []session.Item {
	seen := make(map[string]int, len(existing))
	for i := range existing {
		seen[execSessionItemIdentity(&existing[i])]++
	}
	missing := make([]session.Item, 0, len(candidates))
	for i := range candidates {
		key := execSessionItemIdentity(&candidates[i])
		if seen[key] > 0 {
			seen[key]--
			continue
		}
		missing = append(missing, candidates[i])
	}
	return missing
}

func execSessionItemIdentity(item *session.Item) string {
	if item == nil {
		return ""
	}
	turnID := firstNonEmpty(execStringFromAny(item.Metadata["turnId"]), execStringFromAny(item.Metadata["turn_id"]), execStringFromAny(item.Data["turnId"]), execStringFromAny(item.Data["turn_id"]))
	return strings.Join([]string{strings.TrimSpace(item.ID), strings.TrimSpace(item.Type), strings.TrimSpace(item.Role), turnID, strings.TrimSpace(item.Text)}, "\x00")
}

func (r *Runner) persistSessionTurnStart(req *Request, threadID string, turnID string, userPrompt string, userInputs []turn.TurnUserInput, resumeContext *execResumeContext, modelID string, providerID string) error {
	if r == nil || req == nil || req.Exec.Ephemeral {
		return nil
	}
	now := r.now().UTC()
	store := session.NewStore(filepath.Join(r.CodexHome, "sessions"))
	record, err := store.Read(session.ThreadID(threadID), true, true)
	fresh := errors.Is(err, session.ErrThreadNotFound)
	if err != nil && !fresh {
		return err
	}
	if record == nil {
		record = &session.Record{ID: session.ThreadID(threadID), CreatedAt: now}
	}
	priorItems := append([]session.Item(nil), record.Items...)
	resumed := resumeContext != nil && resumeContext.Record != nil
	var itemMetadata map[string]any
	if resumed {
		itemMetadata = map[string]any{"resumed": true}
	}
	items := execAdditionalInputSessionItems(turnID, req.AdditionalInputItems, now, itemMetadata)
	items = append(items, sessionItemsForTurn(turnID, userPrompt, userInputs, nil, now, itemMetadata, nil)...)
	delta := execMissingSessionItems(record.Items, items)
	record.Items = append(record.Items, delta...)
	if strings.TrimSpace(record.SessionID) == "" {
		record.SessionID = execSessionID(req, threadID)
	}
	if record.ParentThreadID == "" {
		record.ParentThreadID = session.ThreadID(execParentThreadID(req))
	}
	if strings.TrimSpace(record.Preview) == "" {
		record.Preview = firstLine(firstNonEmpty(userPrompt, turnUserInputsSummary(userInputs)))
	}
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	record.RecencyAt = now
	record.Metadata.CWD = firstNonEmpty(record.Metadata.CWD, requestCWD(req))
	record.Metadata.Model = firstNonEmpty(modelID, record.Metadata.Model)
	record.Metadata.ModelProvider = firstNonEmpty(providerID, record.Metadata.ModelProvider)
	if strings.TrimSpace(record.Metadata.Source) == "" {
		record.Metadata.Source = execSessionSource(req)
	}
	if strings.TrimSpace(record.Metadata.ThreadSource) == "" {
		record.Metadata.ThreadSource = execThreadSource(req)
	}
	if strings.TrimSpace(record.Metadata.Originator) == "" {
		record.Metadata.Originator = execAgentOriginator(req)
	}
	if strings.TrimSpace(record.Metadata.CLIVersion) == "" {
		record.Metadata.CLIVersion = doctor.Version()
	}
	record.Metadata.HistoryMode = execPersistedHistoryMode(record, fresh)
	if strings.TrimSpace(record.Metadata.SessionPrefix) == "" {
		record.Metadata.SessionPrefix = session.PrefixForSessionID(firstNonEmpty(record.SessionID, threadID))
	}
	if strings.TrimSpace(record.Metadata.AgentNickname) == "" {
		record.Metadata.AgentNickname = execAgentNicknameForRequest(req)
	}
	if strings.TrimSpace(record.Metadata.AgentRole) == "" {
		record.Metadata.AgentRole = execAgentRoleForRequest(req)
	}
	if strings.TrimSpace(record.Metadata.AgentPath) == "" {
		record.Metadata.AgentPath = execAgentPathForRequest(req)
	}
	if record.Metadata.MultiAgentVersion == "" {
		record.Metadata.MultiAgentVersion = execMultiAgentVersionForRequest(req)
	}
	execStartTurnSnapshot(&record.Metadata, turnID, now)
	if err := store.Save(record); err != nil {
		return err
	}
	path, findErr := rollout.FindThreadPath(r.CodexHome, threadID, false)
	var recorder *rollout.Recorder
	created := false
	if findErr == nil {
		recorder, err = rollout.Resume(path)
	} else {
		recorder, err = r.newExecRolloutRecorder(record, now)
		created = true
	}
	if err != nil {
		return err
	}
	defer recorder.Close()
	if created && len(priorItems) > 0 {
		priorRecord := *record
		priorRecord.Items = priorItems
		if err := rollout.AppendSessionItems(recorder, execRolloutItems(&priorRecord), now); err != nil {
			return err
		}
	}
	if err := recorder.AppendTurnStarted(turnID, now); err != nil {
		return err
	}
	return rollout.AppendSessionItems(recorder, delta, now)
}

func execStartTurnSnapshot(metadata *session.Metadata, turnID string, now time.Time) {
	if metadata == nil || strings.TrimSpace(turnID) == "" {
		return
	}
	startedAt := now.Unix()
	for i := range metadata.RolloutTurns {
		if metadata.RolloutTurns[i].ID != turnID {
			continue
		}
		metadata.RolloutTurns[i].Status = "running"
		if metadata.RolloutTurns[i].StartedAt == nil {
			metadata.RolloutTurns[i].StartedAt = &startedAt
		}
		metadata.RolloutTurns[i].CompletedAt = nil
		metadata.RolloutTurns[i].DurationMS = nil
		metadata.RolloutTurns[i].ErrorMessage = ""
		metadata.RolloutTurns[i].CodexErrorInfo = nil
		return
	}
	metadata.RolloutTurns = append(metadata.RolloutTurns, session.TurnSnapshot{ID: turnID, Status: "running", StartedAt: &startedAt})
}

// execPersistedHistoryMode returns the rollout history mode to persist for an
// exec session. Fresh persistent sessions use Rust's "paginated" default
// (Rust #38774 requests paginated history when codex exec starts a persistent
// thread); an existing session keeps its persisted mode. The previous code
// reused the fork-mode value "all" (session.ForkAll), which is not a valid
// history mode: Rust's app-server only accepts "legacy"/"paginated" and
// rejects the session, which makes Desktop app handoff (/app) silently fail to
// open the thread.
func execPersistedHistoryMode(existing *session.Record, fresh bool) string {
	if fresh {
		return "paginated"
	}
	if existing != nil {
		switch strings.TrimSpace(existing.Metadata.HistoryMode) {
		case "legacy", "paginated":
			return existing.Metadata.HistoryMode
		}
	}
	return "legacy"
}

func (r *Runner) persistInterruptedSession(threadID string, turnID string, cause error) error {
	if r == nil || strings.TrimSpace(threadID) == "" || strings.TrimSpace(turnID) == "" {
		return nil
	}
	store := session.NewStore(filepath.Join(r.CodexHome, "sessions"))
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		return err
	}
	now := r.now().UTC()
	completedAt := now.Unix()
	found := false
	for i := range record.Metadata.RolloutTurns {
		if record.Metadata.RolloutTurns[i].ID == turnID {
			record.Metadata.RolloutTurns[i].Status = "interrupted"
			record.Metadata.RolloutTurns[i].CompletedAt = &completedAt
			record.Metadata.RolloutTurns[i].ErrorMessage = cause.Error()
			found = true
			break
		}
	}
	if !found {
		record.Metadata.RolloutTurns = append(record.Metadata.RolloutTurns, session.TurnSnapshot{ID: turnID, Status: "interrupted", CompletedAt: &completedAt, ErrorMessage: cause.Error()})
	}
	record.UpdatedAt = now
	record.RecencyAt = now
	if err := store.Save(record); err != nil {
		return err
	}
	path, err := rollout.FindThreadPath(r.CodexHome, threadID, false)
	if err != nil {
		return nil
	}
	recorder, err := rollout.Resume(path)
	if err != nil {
		return err
	}
	defer recorder.Close()
	return recorder.AppendTurnAborted(turnID, "interrupted", now, 0)
}

func (r *Runner) persistFailedSession(threadID string, turnID string, cause error) error {
	if r == nil || strings.TrimSpace(threadID) == "" || strings.TrimSpace(turnID) == "" || cause == nil {
		return nil
	}
	store := session.NewStore(filepath.Join(r.CodexHome, "sessions"))
	record, err := store.Read(session.ThreadID(threadID), true, true)
	if err != nil {
		return err
	}
	now := r.now().UTC()
	completedAt := now.Unix()
	found := false
	for i := range record.Metadata.RolloutTurns {
		if record.Metadata.RolloutTurns[i].ID != turnID {
			continue
		}
		record.Metadata.RolloutTurns[i].Status = "failed"
		record.Metadata.RolloutTurns[i].CompletedAt = &completedAt
		record.Metadata.RolloutTurns[i].ErrorMessage = cause.Error()
		found = true
		break
	}
	if !found {
		record.Metadata.RolloutTurns = append(record.Metadata.RolloutTurns, session.TurnSnapshot{ID: turnID, Status: "failed", CompletedAt: &completedAt, ErrorMessage: cause.Error()})
	}
	record.UpdatedAt = now
	record.RecencyAt = now
	if err := store.Save(record); err != nil {
		return err
	}
	path, err := rollout.FindThreadPath(r.CodexHome, threadID, false)
	if err != nil {
		return nil
	}
	recorder, err := rollout.Resume(path)
	if err != nil {
		return err
	}
	defer recorder.Close()
	if err := recorder.AppendTurnError(cause.Error(), now); err != nil {
		return err
	}
	return recorder.AppendTurnComplete(turnID, now, execTurnDurationMS(record, turnID, now))
}

func execTokenUsageForResult(resumeContext *execResumeContext, result *turn.AgentLoopResult, modelID string, cfg *config.Config) *protocol.ThreadTokenUsage {
	if result == nil {
		return nil
	}
	last := lastExecAgentResponseUsage(result)
	aggregate := result.Usage
	if aggregate == (model.AgentUsage{}) {
		aggregate = last
	}
	if aggregate == (model.AgentUsage{}) && last == (model.AgentUsage{}) {
		return nil
	}
	total := protocol.Usage{}
	if resumeContext != nil && resumeContext.Record != nil {
		total = execStoredTokenUsage(resumeContext.Record.Metadata.Extra).Total
		if total.TotalTokens == 0 {
			total.TotalTokens = int64(compact.EstimateTokens(execCompactItemsFromSession(resumeContext.Record.Items)))
		}
	}
	addProtocolUsage(&total, protocolUsageFromAgentUsage(aggregate))
	return &protocol.ThreadTokenUsage{
		Total:              total,
		Last:               protocolUsageFromAgentUsage(last),
		ModelContextWindow: effectiveExecModelContextWindow(modelID, cfg),
	}
}

func lastExecAgentResponseUsage(result *turn.AgentLoopResult) model.AgentUsage {
	if result == nil {
		return model.AgentUsage{}
	}
	responses := result.ModelResponses()
	for i := len(responses) - 1; i >= 0; i-- {
		if responses[i] != nil {
			return responses[i].Usage
		}
	}
	return result.Usage
}

func protocolUsageFromAgentUsage(usage model.AgentUsage) protocol.Usage {
	return protocol.Usage{
		InputTokens:           usage.InputTokens,
		CachedInputTokens:     usage.CachedInputTokens,
		CacheWriteInputTokens: usage.CacheWriteInputTokens,
		OutputTokens:          usage.OutputTokens,
		ReasoningOutputTokens: usage.ReasoningOutputTokens,
		TotalTokens:           model.AgentUsageTotalTokens(usage),
	}
}

func addProtocolUsage(total *protocol.Usage, usage protocol.Usage) {
	if total == nil {
		return
	}
	total.InputTokens += usage.InputTokens
	total.CachedInputTokens += usage.CachedInputTokens
	total.CacheWriteInputTokens += usage.CacheWriteInputTokens
	total.OutputTokens += usage.OutputTokens
	total.ReasoningOutputTokens += usage.ReasoningOutputTokens
	total.TotalTokens += usage.TotalTokens
}

func effectiveExecModelContextWindow(modelID string, cfg *config.Config) *int64 {
	info := execModelInfo(modelID, cfg)
	window := info.ContextWindow
	if window <= 0 {
		window = info.MaxContextWindow
	}
	if window <= 0 {
		return nil
	}
	percent := info.EffectiveContextWindowPercent
	if percent <= 0 {
		percent = 95
	}
	window = window * int64(percent) / 100
	if window <= 0 {
		return nil
	}
	return &window
}

func execModelInfo(modelID string, cfg *config.Config) model.ModelInfo {
	modelConfig := &model.ModelsManagerConfig{}
	if cfg != nil {
		if value, ok := intFromAny(cfg.Values["model_context_window"]); ok && value > 0 {
			modelConfig.ModelContextWindow = int64(value)
		}
		if value, ok := intFromAny(cfg.Values["model_auto_compact_token_limit"]); ok && value > 0 {
			modelConfig.ModelAutoCompactTokenLimit = int64(value)
		}
	}
	if catalog := model.ModelsCatalogFromConfigValues(configValues(cfg)); catalog != nil {
		return model.NewStaticModelsManager(*catalog).GetModelInfo(modelID, modelConfig)
	}
	return model.NewStaticModelsManager(model.BundledModelsResponse()).GetModelInfo(modelID, modelConfig)
}

func execModelTokenBudgetDefaults(info model.ModelInfo) *config.TokenBudgetDefaults {
	if info.ModelMessages == nil || info.ModelMessages.TokenBudget == nil {
		return nil
	}
	defaults := info.ModelMessages.TokenBudget
	return &config.TokenBudgetDefaults{
		ReminderThresholdTokens:         defaults.ReminderThresholdTokens,
		ReminderMessageTemplate:         defaults.ReminderMessageTemplate,
		GuidanceMessage:                 defaults.GuidanceMessage,
		AutoCompactFallbackPrompt:       defaults.AutoCompactFallbackPrompt,
		AutoCompactFallbackBufferTokens: defaults.AutoCompactFallbackBufferTokens,
	}
}

// execAutoCompactFallbackFollowUp mirrors the app-server auto-compact fallback
// follow-up (RuntimeRouter::autoCompactFallbackFollowUp) and Rust
// token_budget::maybe_record: the token-budget reminder fires once per window
// when the base window remaining drops to the reminder threshold, and the
// model-owned fallback prompt fires once per window when the base window is
// exactly exhausted without forcing compaction.
func (r *Runner) execAutoCompactFallbackFollowUp(cfg *config.Config, modelID string) turn.SamplingFollowUp {
	if cfg == nil {
		return nil
	}
	info := execModelInfo(modelID, cfg)
	tokenBudget, err := cfg.TokenBudgetConfigWithDefaults(execModelTokenBudgetDefaults(info))
	if err != nil || tokenBudget == nil || !tokenBudget.Enabled {
		return nil
	}
	prompt := strings.TrimSpace(tokenBudget.AutoCompactFallbackPrompt)
	buffer := 0
	if tokenBudget.AutoCompactFallbackBufferTokens != nil {
		buffer = *tokenBudget.AutoCompactFallbackBufferTokens
	}
	reminderThreshold := 0
	if tokenBudget.ReminderThresholdTokens != nil {
		reminderThreshold = *tokenBudget.ReminderThresholdTokens
	}
	reminderTemplate := strings.TrimSpace(tokenBudget.ReminderMessageTemplate)
	if (prompt == "" || buffer <= 0) && (reminderThreshold <= 0 || reminderTemplate == "") {
		return nil
	}
	resolvedWindow := info.ContextWindow
	if resolvedWindow <= 0 {
		resolvedWindow = info.MaxContextWindow
	}
	limit := resolvedWindow * 9 / 10
	if info.AutoCompactTokenLimit > 0 && (limit == 0 || info.AutoCompactTokenLimit < limit) {
		limit = info.AutoCompactTokenLimit
	}
	if limit <= 0 {
		return nil
	}
	reminderDelivered := false
	fallbackDelivered := false
	return func(ctx *turn.SamplingFollowUpContext) []any {
		if ctx == nil {
			return nil
		}
		status := compact.Evaluate(compact.Policy{Enabled: true, TokenLimit: int(limit), FallbackBufferTokens: buffer}, int(model.AgentUsageTotalTokens(ctx.Usage)))
		if status.BaseWindowTokensRemaining == nil {
			return nil
		}
		remaining := *status.BaseWindowTokensRemaining
		var items []any
		// The reminder fires even when compaction is already due (Rust records
		// it before the roll-over check).
		if !reminderDelivered && reminderThreshold > 0 && reminderTemplate != "" && remaining <= reminderThreshold {
			if item := model.DeveloperMessageInputItem(strings.ReplaceAll(reminderTemplate, "{n_remaining}", strconv.Itoa(remaining))); item != nil {
				items = append(items, item)
				reminderDelivered = true
			}
		}
		if !fallbackDelivered && !status.ShouldCompact && prompt != "" && buffer > 0 && remaining == 0 {
			if item := model.DeveloperMessageInputItem(prompt); item != nil {
				items = append(items, item)
				fallbackDelivered = true
			}
		}
		return items
	}
}

// execMidTurnSamplingCompaction mirrors Rust's mid-turn roll-over for exec
// runs: when a turn still needs follow-up and the auto-compact token limit is
// reached, the in-flight conversation is compacted in place and the sampling
// loop continues against the compacted history instead of executing the
// pending tool calls. The active context is measured from the last sampled
// response (Rust's context_window_token_status), not the accumulated turn
// usage.
func (r *Runner) execMidTurnSamplingCompaction(cfg *config.Config, modelID string, providerID string, agent model.AgentRunner, req *Request, threadID string, turnID string, userPrompt string, userInputs []turn.TurnUserInput, resumeContext *execResumeContext, eventSink *execEventSink, startupItems []any, additionalInputItems []any) turn.SamplingCompaction {
	if r == nil || req == nil || req.Exec.Ephemeral || cfg == nil || strings.TrimSpace(threadID) == "" {
		return nil
	}
	return func(ctx *turn.SamplingCompactionContext) (*turn.SamplingCompactionResult, error) {
		if ctx == nil || ctx.Result == nil || ctx.Response == nil {
			return nil, nil
		}
		store := session.NewStore(filepath.Join(r.CodexHome, "sessions"))
		record, err := store.Read(session.ThreadID(threadID), true, true)
		if err != nil || record == nil {
			return nil, err
		}
		status := compact.Evaluate(execCompactPolicy(record, modelID, cfg), int(model.AgentUsageTotalTokens(ctx.Response.Usage)))
		if !status.ShouldCompact {
			return nil, nil
		}
		// The conversation to compact is the persisted thread history plus
		// the items the current turn accumulated before they are persisted.
		history := execCompactItemsFromSession(record.Items)
		itemMetadata := map[string]any{}
		if resumeContext != nil && resumeContext.Record != nil {
			itemMetadata = map[string]any{"resumed": true}
		}
		turnItems := sessionItemsForTurnWithMode(turnID, userPrompt, userInputs, ctx.Result, r.now().UTC(), itemMetadata, &execImageGenerationContext{CodexHome: r.CodexHome, ThreadID: threadID}, execRequestPlanMode(req))
		seen := make(map[string]struct{}, len(history))
		for i := range history {
			if history[i].ID != "" {
				seen[history[i].ID] = struct{}{}
			}
		}
		for _, item := range execCompactItemsFromSession(turnItems) {
			if item.ID != "" {
				if _, ok := seen[item.ID]; ok {
					continue
				}
				seen[item.ID] = struct{}{}
			}
			history = append(history, item)
		}
		request := &compact.Request{
			ThreadID:  threadID,
			TurnID:    turnID,
			Trigger:   compact.TriggerAuto,
			Reason:    status.Reason,
			Phase:     compact.PhaseMidTurn,
			Prompt:    "Summarize the conversation so far, preserving user intent, decisions, file changes, commands, and unresolved work.",
			History:   history,
			StartedAt: r.now().UTC(),
		}
		if request.Reason == "" {
			request.Reason = compact.ReasonTokenLimit
		}
		var remoteRunner compact.RemoteRunner
		if execProviderSupportsRemoteCompaction(cfg, providerID) {
			remoteRunner = &execAgentCompactRunner{agent: agent, model: modelID, providerID: providerID}
		}
		eventSink.EmitInternal(protocol.Compacting())
		defer func() { eventSink.EmitInternal(protocol.Compacted()) }()
		compacted, err := compact.CompactRemotely(context.Background(), request, &compact.RemoteOptions{
			Runner:          remoteRunner,
			MaxSummaryChars: 4000,
			FallbackToLocal: true,
		})
		if err != nil {
			return nil, err
		}
		if compacted == nil || !compacted.Succeeded() {
			return nil, errors.New("context compaction did not complete")
		}
		now := r.now().UTC()
		compactedItems := execSessionItemsFromCompact(compacted.NewHistory, now)
		record.Items = compactedItems
		record.UpdatedAt = now
		record.RecencyAt = now
		extra := cloneExecAnyMap(record.Metadata.Extra)
		if extra == nil {
			extra = map[string]any{}
		}
		extra["compacted_at"] = now.Format(time.RFC3339Nano)
		extra["auto_compacted_at"] = now.Format(time.RFC3339Nano)
		extra["compaction_summary"] = compacted.Summary
		extra["compaction_reason"] = string(request.Reason)
		extra["compaction_trigger"] = string(request.Trigger)
		extra["compaction_phase"] = string(request.Phase)
		extra["compaction_status"] = string(compacted.Status)
		extra["compaction_source"] = string(compacted.Source)
		extra["token_status"] = map[string]any{
			"activeContextTokens":      compact.EstimateTokens(compacted.NewHistory),
			"shouldCompact":            false,
			"newContextWindowRequired": false,
		}
		usage := execStoredTokenUsage(extra)
		if usage.Total.TotalTokens == 0 {
			usage.Total.TotalTokens = int64(status.ActiveContextTokens)
		}
		if compacted.Usage != nil {
			last := protocol.Usage{
				InputTokens:           compacted.Usage.InputTokens,
				CachedInputTokens:     compacted.Usage.CachedInputTokens,
				CacheWriteInputTokens: compacted.Usage.CacheWriteInputTokens,
				OutputTokens:          compacted.Usage.OutputTokens,
				ReasoningOutputTokens: compacted.Usage.ReasoningOutputTokens,
				TotalTokens:           compacted.Usage.InputTokens + compacted.Usage.OutputTokens,
			}
			addProtocolUsage(&usage.Total, last)
			usage.Last = last
		} else {
			usage.Last = protocol.Usage{TotalTokens: int64(compact.EstimateTokens(compacted.NewHistory))}
		}
		usage.ModelContextWindow = effectiveExecModelContextWindow(modelID, cfg)
		record.Metadata.Extra = execTokenUsageMetadata(extra, &usage)
		if err := store.Save(record); err != nil {
			return nil, err
		}
		if err := r.appendExecCompacted(threadID, record, compacted.Summary, now); err != nil {
			return nil, err
		}
		// Preserve the run's non-conversation prefix items (permissions,
		// environment context, additional inputs) so the compacted context
		// continues with the same injected state.
		prefix := append(append([]any(nil), startupItems...), additionalInputItems...)
		compactedInputItems := session.InputItemsFromItems(compactedItems, &session.HistoryBuildOptions{
			IncludeToolOutputs: true,
			CWD:                strings.TrimSpace(record.Metadata.CWD),
		})
		return &turn.SamplingCompactionResult{
			Compacted:          true,
			InputItems:         append(prefix, compactedInputItems...),
			PreviousResponseID: "",
		}, nil
	}
}

func execTokenUsageMetadata(extra map[string]any, usage *protocol.ThreadTokenUsage) map[string]any {
	if usage == nil {
		return extra
	}
	out := cloneExecAnyMap(extra)
	if out == nil {
		out = map[string]any{}
	}
	total := execProtocolUsageMetadata(usage.Total)
	last := execProtocolUsageMetadata(usage.Last)
	out["total_token_usage"] = total
	out["last_token_usage"] = last
	if usage.ModelContextWindow != nil && *usage.ModelContextWindow > 0 {
		out["model_context_window"] = *usage.ModelContextWindow
	}
	out["token_usage_info"] = map[string]any{
		"total_token_usage":    total,
		"last_token_usage":     last,
		"model_context_window": execContextWindowMetadataValue(usage.ModelContextWindow),
	}
	return out
}

func execProtocolUsageMetadata(usage protocol.Usage) map[string]any {
	return map[string]any{
		"input_tokens":             usage.InputTokens,
		"cached_input_tokens":      usage.CachedInputTokens,
		"cache_write_input_tokens": usage.CacheWriteInputTokens,
		"output_tokens":            usage.OutputTokens,
		"reasoning_output_tokens":  usage.ReasoningOutputTokens,
		"total_tokens":             usage.TotalTokens,
	}
}

func execContextWindowMetadataValue(value *int64) any {
	if value == nil || *value <= 0 {
		return nil
	}
	return *value
}

func execStoredTokenUsage(extra map[string]any) protocol.ThreadTokenUsage {
	if extra == nil {
		return protocol.ThreadTokenUsage{}
	}
	info, _ := extra["token_usage_info"].(map[string]any)
	totalRaw := execFirstMapValue(info, "total_token_usage", "totalTokenUsage", "total")
	lastRaw := execFirstMapValue(info, "last_token_usage", "lastTokenUsage", "last")
	windowRaw := execFirstMapValue(info, "model_context_window", "modelContextWindow")
	if totalRaw == nil {
		totalRaw = execFirstMapValue(extra, "total_token_usage", "totalTokenUsage", "total")
	}
	if lastRaw == nil {
		lastRaw = execFirstMapValue(extra, "last_token_usage", "lastTokenUsage", "last")
	}
	if windowRaw == nil {
		windowRaw = execFirstMapValue(extra, "model_context_window", "modelContextWindow")
	}
	usage := protocol.ThreadTokenUsage{Total: execProtocolUsageFromAny(totalRaw), Last: execProtocolUsageFromAny(lastRaw)}
	if window := execInt64FromAny(windowRaw); window > 0 {
		usage.ModelContextWindow = &window
	}
	return usage
}

func execProtocolUsageFromAny(value any) protocol.Usage {
	values, _ := value.(map[string]any)
	usage := protocol.Usage{
		InputTokens:           execInt64FromAny(execFirstMapValue(values, "input_tokens", "inputTokens")),
		CachedInputTokens:     execInt64FromAny(execFirstMapValue(values, "cached_input_tokens", "cachedInputTokens")),
		CacheWriteInputTokens: execInt64FromAny(execFirstMapValue(values, "cache_write_input_tokens", "cacheWriteInputTokens")),
		OutputTokens:          execInt64FromAny(execFirstMapValue(values, "output_tokens", "outputTokens")),
		ReasoningOutputTokens: execInt64FromAny(execFirstMapValue(values, "reasoning_output_tokens", "reasoningOutputTokens")),
		TotalTokens:           execInt64FromAny(execFirstMapValue(values, "total_tokens", "totalTokens")),
	}
	if usage.TotalTokens == 0 {
		usage.TotalTokens = usage.InputTokens + usage.OutputTokens
	}
	return usage
}

func execFirstMapValue(values map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := values[key]; ok {
			return value
		}
	}
	return nil
}

func execInt64FromAny(value any) int64 {
	switch typed := value.(type) {
	case int:
		return int64(typed)
	case int32:
		return int64(typed)
	case int64:
		return typed
	case float32:
		return int64(typed)
	case float64:
		return int64(typed)
	case json.Number:
		parsed, _ := typed.Int64()
		return parsed
	default:
		return 0
	}
}

func cloneExecAnyMap(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func (r *Runner) compactResumeBeforeTurn(ctx context.Context, resumeContext *execResumeContext, threadID string, turnID string, modelID string, providerID string, cfg *config.Config, agent model.AgentRunner, sink *execEventSink) (bool, error) {
	if resumeContext == nil || resumeContext.Record == nil {
		return false, nil
	}
	record := resumeContext.Record
	status := execCompactStatus(record, modelID, cfg)
	if !status.ShouldCompact {
		return false, nil
	}
	// Rust does not surface compaction lifecycle events in the exec JSON/SDK
	// stream; it emits a single warning error item after the resumed turn. Keep
	// the compacting/compacted status events internal for the TUI only so the
	// public event stream matches Rust.
	sink.EmitInternal(protocol.Compacting())
	defer func() { sink.EmitInternal(protocol.Compacted()) }()
	request := &compact.Request{
		ThreadID:  threadID,
		TurnID:    turnID,
		Trigger:   compact.TriggerAuto,
		Reason:    status.Reason,
		Phase:     compact.PhasePreTurn,
		Prompt:    "Summarize the conversation so far, preserving user intent, decisions, file changes, commands, and unresolved work.",
		History:   execCompactItemsFromSession(record.Items),
		StartedAt: r.now().UTC(),
	}
	if request.Reason == "" {
		request.Reason = compact.ReasonTokenLimit
	}
	var remoteRunner compact.RemoteRunner
	if execProviderSupportsRemoteCompaction(cfg, providerID) {
		remoteRunner = &execAgentCompactRunner{agent: agent, model: modelID, providerID: providerID}
	}
	compacted, err := compact.CompactRemotely(ctx, request, &compact.RemoteOptions{
		Runner:          remoteRunner,
		MaxSummaryChars: 4000,
		FallbackToLocal: true,
	})
	if err != nil {
		return false, err
	}
	if compacted == nil || !compacted.Succeeded() {
		return false, errors.New("context compaction did not complete")
	}
	now := r.now().UTC()
	record.Items = execSessionItemsFromCompact(compacted.NewHistory, now)
	record.UpdatedAt = now
	record.RecencyAt = now
	extra := cloneExecAnyMap(record.Metadata.Extra)
	if extra == nil {
		extra = map[string]any{}
	}
	extra["compacted_at"] = now.Format(time.RFC3339Nano)
	extra["auto_compacted_at"] = now.Format(time.RFC3339Nano)
	extra["compaction_summary"] = compacted.Summary
	extra["compaction_reason"] = string(request.Reason)
	extra["compaction_trigger"] = string(request.Trigger)
	extra["compaction_phase"] = string(request.Phase)
	extra["compaction_status"] = string(compacted.Status)
	extra["compaction_source"] = string(compacted.Source)
	extra["token_status"] = map[string]any{
		"activeContextTokens":      compact.EstimateTokens(compacted.NewHistory),
		"shouldCompact":            false,
		"newContextWindowRequired": false,
	}
	usage := execStoredTokenUsage(extra)
	if usage.Total.TotalTokens == 0 {
		usage.Total.TotalTokens = int64(status.ActiveContextTokens)
	}
	if compacted.Usage != nil {
		last := protocol.Usage{
			InputTokens:           compacted.Usage.InputTokens,
			CachedInputTokens:     compacted.Usage.CachedInputTokens,
			CacheWriteInputTokens: compacted.Usage.CacheWriteInputTokens,
			OutputTokens:          compacted.Usage.OutputTokens,
			ReasoningOutputTokens: compacted.Usage.ReasoningOutputTokens,
			TotalTokens:           compacted.Usage.InputTokens + compacted.Usage.OutputTokens,
		}
		addProtocolUsage(&usage.Total, last)
		usage.Last = last
	} else {
		usage.Last = protocol.Usage{TotalTokens: int64(compact.EstimateTokens(compacted.NewHistory))}
	}
	usage.ModelContextWindow = effectiveExecModelContextWindow(modelID, cfg)
	record.Metadata.Extra = execTokenUsageMetadata(extra, &usage)
	if err := session.NewStore(filepath.Join(r.CodexHome, "sessions")).Save(record); err != nil {
		return false, err
	}
	if err := r.appendExecCompacted(threadID, record, compacted.Summary, now); err != nil {
		return false, err
	}
	return true, nil
}

func execProviderSupportsRemoteCompaction(cfg *config.Config, providerID string) bool {
	provider, err := model.ProviderForConfigID(configValues(cfg), providerID, stringConfigValue(cfg, "openai_base_url"))
	return err == nil && provider != nil && provider.SupportsRemoteCompaction()
}

func execCompactStatus(record *session.Record, modelID string, cfg *config.Config) compact.TokenStatus {
	if record == nil {
		return compact.TokenStatus{}
	}
	policy := execCompactPolicy(record, modelID, cfg)
	stored := execStoredTokenUsage(record.Metadata.Extra)
	active := stored.Last.TotalTokens
	if active <= 0 {
		active = int64(compact.EstimateTokens(execCompactItemsFromSession(record.Items)))
	} else {
		// Rust derives the active context from the last server-reported total
		// plus an estimate of any local items recorded after the last
		// model-generated item (for example a persisted prompt from an
		// interrupted turn).
		active = int64(compact.EstimateActiveContextTokens(execCompactItemsFromSession(record.Items), int(active)))
	}
	status := compact.Evaluate(policy, int(active))
	if execStoredContextWindowRequired(record.Metadata.Extra) {
		status.ShouldCompact = true
		status.Reason = compact.ReasonContextWindowExceeded
		status.NewContextWindowRequired = true
	}
	return status
}

// execCompactPolicy mirrors Rust ModelInfo::auto_compact_token_limit and
// TurnContext::model_context_window for exec runs. The model's resolved
// window supplies the hard cap; the auto-compact limit is the smaller of the
// model's configured limit and 9/10 of the resolved window.
func execCompactPolicy(record *session.Record, modelID string, cfg *config.Config) compact.Policy {
	info := execModelInfo(modelID, cfg)
	resolvedWindow := info.ContextWindow
	if resolvedWindow <= 0 {
		resolvedWindow = info.MaxContextWindow
	}
	window := int64(0)
	if effective := effectiveExecModelContextWindow(modelID, cfg); effective != nil {
		window = *effective
	}
	limit := resolvedWindow * 9 / 10
	if info.AutoCompactTokenLimit > 0 && (limit == 0 || info.AutoCompactTokenLimit < limit) {
		limit = info.AutoCompactTokenLimit
	}
	policy := compact.Policy{Enabled: true, TokenLimit: int(limit), WindowTokens: int(window)}
	if stringConfigValue(cfg, "model_auto_compact_token_limit_scope") == "body_after_prefix" {
		// Mirrors Rust AutoCompactTokenLimitScope::BodyAfterPrefix: charge only
		// tokens grown after the carried window prefix against the limit.
		// Rust apply_rollout_reconstruction re-estimates the carried prefix
		// from the reconstructed history on every resume, so a persisted
		// server-observed baseline from a previous process does not survive.
		policy.Scope = compact.ScopeBodyAfterPrefix
		policy.PrefillTokens = compact.EstimateTokens(execCompactItemsFromSession(record.Items))
	}
	return policy
}

func execStoredContextWindowRequired(extra map[string]any) bool {
	status, _ := extra["token_status"].(map[string]any)
	for _, key := range []string{"newContextWindowRequired", "new_context_window_required"} {
		if value, ok := status[key].(bool); ok && value {
			return true
		}
	}
	return false
}

func execIsContextWindowExceeded(err error) bool {
	var apiErr *codexapi.APIError
	return errors.As(err, &apiErr) && apiErr != nil && apiErr.Kind == codexapi.ErrorContextWindowExceeded
}

func (r *Runner) persistExecContextWindowExceeded(record *session.Record) error {
	if r == nil || record == nil {
		return nil
	}
	extra := cloneExecAnyMap(record.Metadata.Extra)
	if extra == nil {
		extra = map[string]any{}
	}
	status, _ := extra["token_status"].(map[string]any)
	status = cloneExecAnyMap(status)
	if status == nil {
		status = map[string]any{}
	}
	status["shouldCompact"] = true
	status["reason"] = string(compact.ReasonContextWindowExceeded)
	status["newContextWindowRequired"] = true
	extra["token_status"] = status
	record.Metadata.Extra = extra
	return session.NewStore(filepath.Join(r.CodexHome, "sessions")).Save(record)
}

type execAgentCompactRunner struct {
	agent      model.AgentRunner
	model      string
	providerID string
}

func (r *execAgentCompactRunner) Compact(ctx context.Context, request *compact.Request) (*compact.Result, error) {
	if r == nil || r.agent == nil || request == nil {
		return nil, nil
	}
	response, err := r.agent.Run(ctx, &model.AgentRequest{
		Prompt:       strings.TrimSpace(request.Prompt),
		Instructions: execRemoteCompactInstructions(),
		InputItems:   session.InputItemsFromItems(execSessionItemsFromCompact(request.History, time.Now().UTC()), &session.HistoryBuildOptions{IncludeToolOutputs: true}),
		Model:        r.model,
		ProviderID:   r.providerID,
		TaskKind:     model.AgentTaskRegular,
		ThreadID:     request.ThreadID,
		TurnID:       request.TurnID,
		Store:        false,
	})
	if err != nil {
		return nil, err
	}
	summary := strings.TrimSpace(response.Message)
	if summary == "" {
		for i := range response.Items {
			if text := strings.TrimSpace(response.Items[i].Text); text != "" {
				summary = text
				break
			}
		}
	}
	if summary == "" {
		return nil, nil
	}
	return &compact.Result{
		Status:      compact.StatusCompleted,
		Summary:     summary,
		NewHistory:  compact.BuildCompactedHistory(nil, execLastUserCompactItems(request.History, 1), summary),
		CompletedAt: time.Now().UTC(),
		Source:      compact.SourceRemote,
		ResponseID:  response.ResponseID,
		Model:       response.Model,
		ProviderID:  response.ProviderID,
		Usage: &compact.Usage{
			InputTokens: response.Usage.InputTokens, CachedInputTokens: response.Usage.CachedInputTokens,
			CacheWriteInputTokens: response.Usage.CacheWriteInputTokens, OutputTokens: response.Usage.OutputTokens,
			ReasoningOutputTokens: response.Usage.ReasoningOutputTokens,
		},
	}, nil
}

func execRemoteCompactInstructions() string {
	return strings.TrimSpace(`You are compacting a Codex conversation for future continuation.
Write a concise but high-fidelity summary that preserves the user's objective, constraints, decisions, file changes, commands, unresolved work, and risks.
Return only the summary.`)
}

func execCompactItemsFromSession(items []session.Item) []compact.Item {
	out := make([]compact.Item, 0, len(items))
	for i := range items {
		item := items[i]
		kind := item.Type
		if item.Metadata != nil {
			if value, ok := item.Metadata["kind"].(string); ok && strings.TrimSpace(value) != "" {
				kind = strings.TrimSpace(value)
			}
		}
		compactItem := compact.Item{ID: item.ID, Type: item.Type, Role: item.Role, Text: item.Text, Kind: kind, Data: cloneExecAnyMap(item.Data), Raw: append(json.RawMessage(nil), item.Raw...), Created: item.CreatedAt}
		for j := range item.Content {
			compactItem.Content = append(compactItem.Content, compact.ContentPart{Type: item.Content[j].Type, Text: item.Content[j].Text, ImageURL: item.Content[j].ImageURL, Detail: item.Content[j].Detail})
		}
		out = append(out, compactItem)
	}
	return out
}

func execSessionItemsFromCompact(items []compact.Item, now time.Time) []session.Item {
	out := make([]session.Item, 0, len(items))
	for i := range items {
		item := items[i]
		created := item.Created
		if created.IsZero() {
			created = now
		}
		sessionItem := session.Item{ID: firstNonEmpty(item.ID, fmt.Sprintf("compact-%d", i)), Type: item.Type, Role: item.Role, Text: compact.ItemText(&item), CreatedAt: created, Data: cloneExecAnyMap(item.Data), Raw: append(json.RawMessage(nil), item.Raw...), Metadata: map[string]any{"compact": true, "kind": item.Kind}}
		for j := range item.Content {
			sessionItem.Content = append(sessionItem.Content, session.ContentPart{Type: item.Content[j].Type, Text: item.Content[j].Text, ImageURL: item.Content[j].ImageURL, Detail: item.Content[j].Detail})
		}
		out = append(out, sessionItem)
	}
	return out
}

func execLastUserCompactItems(items []compact.Item, count int) []compact.Item {
	out := make([]compact.Item, 0, count)
	for i := len(items) - 1; i >= 0 && len(out) < count; i-- {
		if items[i].Role == "user" && items[i].Kind != "compaction_summary" {
			out = append(out, items[i])
		}
	}
	for left, right := 0, len(out)-1; left < right; left, right = left+1, right-1 {
		out[left], out[right] = out[right], out[left]
	}
	return out
}

func (r *Runner) resolveExecResumeRecord(req *Request) (*session.Record, error) {
	if r == nil {
		return nil, errors.New("exec runner is nil")
	}
	store := session.NewStore(filepath.Join(r.CodexHome, "sessions"))
	resume := &req.Exec.Resume
	var threadID session.ThreadID
	if resume.Last {
		record, err := latestExecResumeRecord(store, resume, requestCWD(req))
		if err == nil {
			threadID = record.ID
		} else if !errors.Is(err, session.ErrThreadNotFound) {
			return nil, err
		} else {
			imported, importErr := latestExecResumeRolloutRecord(r.CodexHome, resume, requestCWD(req))
			if importErr != nil {
				if errors.Is(importErr, session.ErrThreadNotFound) {
					return nil, err
				}
				return nil, importErr
			}
			if saveErr := store.Save(imported); saveErr != nil {
				return nil, saveErr
			}
			return imported, nil
		}
	} else {
		target := strings.TrimSpace(resume.SessionID)
		if target == "" {
			return nil, errors.New("exec resume requires SESSION_ID or --last")
		}
		resolved, err := execResumeThreadIDForTarget(store, resume, target, requestCWD(req))
		if err != nil {
			imported, importErr := namedExecResumeRolloutRecord(r.CodexHome, resume, target, requestCWD(req))
			if importErr != nil {
				if errors.Is(importErr, session.ErrThreadNotFound) {
					return nil, err
				}
				return nil, importErr
			}
			if saveErr := store.Save(imported); saveErr != nil {
				return nil, saveErr
			}
			return imported, nil
		}
		threadID = resolved
	}
	record, err := store.Read(threadID, true, true)
	if err == nil {
		return record, nil
	}
	if !errors.Is(err, session.ErrThreadNotFound) {
		return nil, err
	}
	// Rust persists canonical session metadata in rollout JSONL. Import it
	// when the Go-specific physical session record is absent, then continue
	// through the normal Go resume path.
	path, pathErr := rollout.FindThreadPath(r.CodexHome, string(threadID), false)
	if pathErr != nil {
		path, pathErr = rollout.FindThreadPath(r.CodexHome, string(threadID), true)
	}
	if pathErr != nil {
		return nil, err
	}
	imported, importErr := rollout.RecordFromPathResolved(r.CodexHome, path, strings.Contains(filepath.ToSlash(path), "/archived_sessions/"))
	if importErr != nil {
		return nil, importErr
	}
	if imported == nil {
		return nil, err
	}
	if saveErr := store.Save(imported); saveErr != nil {
		return nil, saveErr
	}
	return imported, nil
}

func latestExecResumeRolloutRecord(codexHome string, resume *cli.ExecResumeOptions, cwd string) (*session.Record, error) {
	page, err := rollout.ListThreads(codexHome, rollout.ListOptions{
		SortKey:       rollout.SortUpdatedAt,
		SortDirection: rollout.SortDesc,
		Archived:      false,
	})
	if err != nil {
		return nil, err
	}
	for i := range page.Items {
		record, recordErr := rollout.RecordFromPathResolved(codexHome, page.Items[i].Path, false)
		if recordErr != nil || record == nil {
			continue
		}
		if (resume != nil && resume.All) || execResumeCWDMatches(cwd, record.Metadata.CWD) {
			return record, nil
		}
	}
	return nil, session.ErrThreadNotFound
}

func namedExecResumeRolloutRecord(codexHome string, resume *cli.ExecResumeOptions, target string, cwd string) (*session.Record, error) {
	page, err := rollout.ListThreads(codexHome, rollout.ListOptions{
		SortKey:       rollout.SortUpdatedAt,
		SortDirection: rollout.SortDesc,
		Archived:      false,
	})
	if err != nil {
		return nil, err
	}
	threadIDs := make(map[string]struct{}, len(page.Items))
	for i := range page.Items {
		threadIDs[page.Items[i].ThreadID] = struct{}{}
	}
	names, err := rollout.FindThreadNamesByIDs(codexHome, threadIDs)
	if err != nil {
		return nil, err
	}
	for i := range page.Items {
		item := &page.Items[i]
		if names[item.ThreadID] != target && item.ThreadID != target {
			continue
		}
		record, recordErr := rollout.RecordFromPathResolved(codexHome, item.Path, false)
		if recordErr != nil || record == nil {
			continue
		}
		if (resume != nil && resume.All) || execResumeCWDMatches(cwd, record.Metadata.CWD) {
			return record, nil
		}
	}
	return nil, session.ErrThreadNotFound
}

func execResumeCWDMatches(expected string, actual string) bool {
	expected = filepath.Clean(strings.TrimSpace(expected))
	actual = filepath.Clean(strings.TrimSpace(actual))
	if expected == "." || actual == "." {
		return expected == actual
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(expected, actual)
	}
	return expected == actual
}

func (r *Runner) createExecRollout(record *session.Record, now time.Time) error {
	if r == nil || record == nil {
		return nil
	}
	recorder, err := r.newExecRolloutRecorder(record, now)
	if err != nil {
		return err
	}
	defer recorder.Close()
	return rollout.AppendSessionItems(recorder, execRolloutItems(record), now)
}

func (r *Runner) newExecRolloutRecorder(record *session.Record, now time.Time) (*rollout.Recorder, error) {
	if r == nil || record == nil {
		return nil, errors.New("exec rollout record is nil")
	}
	return rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:                  r.CodexHome,
		SessionID:                  record.SessionID,
		SessionPrefix:              record.Metadata.SessionPrefix,
		ThreadID:                   string(record.ID),
		ForkedFromID:               string(record.ForkedFromID),
		Source:                     record.Metadata.Source,
		ThreadSource:               record.Metadata.ThreadSource,
		Originator:                 record.Metadata.Originator,
		CWD:                        record.Metadata.CWD,
		Model:                      record.Metadata.Model,
		ModelProvider:              record.Metadata.ModelProvider,
		HistoryMode:                record.Metadata.HistoryMode,
		MemoryMode:                 record.Metadata.MemoryMode,
		ParentThreadID:             string(record.ParentThreadID),
		BaseInstructions:           record.Metadata.BaseInstructions,
		BaseInstructionsProvenance: cloneExecBaseInstructionsProvenance(record.Metadata.BaseInstructionsProvenance),
		AgentNickname:              record.Metadata.AgentNickname,
		AgentRole:                  record.Metadata.AgentRole,
		AgentPath:                  record.Metadata.AgentPath,
		MultiAgentVersion:          record.Metadata.MultiAgentVersion,
		CLIVersion:                 record.Metadata.CLIVersion,
		Now:                        now,
	})
}

func cloneExecBaseInstructionsProvenance(value *session.BaseInstructionsProvenance) *session.BaseInstructionsProvenance {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func execRolloutItems(record *session.Record) []session.Item {
	if record == nil {
		return nil
	}
	items := append([]session.Item(nil), record.Items...)
	if strings.TrimSpace(record.Metadata.HistoryMode) != "paginated" {
		return items
	}
	fallbackTurnID := "turn-imported-" + safeSessionItemID(string(record.ID))
	for i := range items {
		items[i].Metadata = cloneExecAnyMap(items[i].Metadata)
		if items[i].Metadata == nil {
			items[i].Metadata = map[string]any{}
		}
		if firstNonEmpty(execStringFromAny(items[i].Metadata["turnId"]), execStringFromAny(items[i].Metadata["turn_id"]), execStringFromAny(items[i].Data["turnId"]), execStringFromAny(items[i].Data["turn_id"])) == "" {
			items[i].Metadata["turnId"] = fallbackTurnID
		}
	}
	return items
}

func (r *Runner) appendExecRollout(threadID session.ThreadID, items []session.Item, record *session.Record, now time.Time) error {
	if r == nil {
		return nil
	}
	path, err := rollout.FindThreadPath(r.CodexHome, string(threadID), false)
	if err != nil {
		path, err = rollout.FindThreadPath(r.CodexHome, string(threadID), true)
	}
	if err != nil {
		return r.createExecRollout(record, now)
	}
	recorder, err := rollout.Resume(path)
	if err != nil {
		return err
	}
	defer recorder.Close()
	return rollout.AppendSessionItems(recorder, items, now)
}

func (r *Runner) appendExecTurnComplete(record *session.Record, turnID string, now time.Time) error {
	if r == nil || record == nil || strings.TrimSpace(turnID) == "" {
		return nil
	}
	path, err := rollout.FindThreadPath(r.CodexHome, string(record.ID), false)
	if err != nil {
		return err
	}
	recorder, err := rollout.Resume(path)
	if err != nil {
		return err
	}
	defer recorder.Close()
	return recorder.AppendTurnComplete(turnID, now, execTurnDurationMS(record, turnID, now))
}

func execTurnDurationMS(record *session.Record, turnID string, completedAt time.Time) int64 {
	if record == nil || strings.TrimSpace(turnID) == "" {
		return 0
	}
	for i := range record.Metadata.RolloutTurns {
		turn := &record.Metadata.RolloutTurns[i]
		if turn.ID != turnID || turn.StartedAt == nil {
			continue
		}
		duration := completedAt.UTC().Sub(time.Unix(*turn.StartedAt, 0).UTC()).Milliseconds()
		if duration > 0 {
			return duration
		}
		return 0
	}
	return 0
}

// appendExecCompacted persists the compacted rollout marker with the same shape
// as Rust's RolloutItem::Compacted so resume and cross-implementation replay
// see the compaction boundary. The replacement history is the post-compaction
// session items, and the message mirrors Rust's compaction summary message.
func (r *Runner) appendExecCompacted(threadID string, record *session.Record, summary string, now time.Time) error {
	if r == nil || record == nil {
		return nil
	}
	path, err := rollout.FindThreadPath(r.CodexHome, threadID, false)
	if err != nil {
		path, err = rollout.FindThreadPath(r.CodexHome, threadID, true)
	}
	if err != nil {
		return err
	}
	recorder, err := rollout.Resume(path)
	if err != nil {
		return err
	}
	defer recorder.Close()
	message := strings.TrimSpace(compact.SummaryPrefix + "\n" + summary)
	replacement := make([]rollout.Item, 0, len(record.Items))
	for i := range record.Items {
		if item := rollout.ItemFromSessionItem(&record.Items[i]); item != nil {
			replacement = append(replacement, *item)
		}
	}
	return recorder.AppendCompacted(message, replacement, now)
}

func latestExecResumeRecord(store *session.Store, resume *cli.ExecResumeOptions, cwd string) (*session.Record, error) {
	if store == nil {
		return nil, errors.New("session store is nil")
	}
	options := session.ListOptions{
		SortKey:        session.SortUpdatedAt,
		SortDirection:  session.SortDesc,
		Archived:       false,
		IncludeHistory: false,
	}
	if resume == nil || !resume.All {
		if strings.TrimSpace(cwd) != "" {
			options.CWDs = []string{cwd}
		}
	}
	activePage, err := store.List(options)
	if err != nil {
		return nil, err
	}
	if len(activePage.Records) == 0 {
		return nil, session.ErrThreadNotFound
	}
	return &activePage.Records[0], nil
}

func execResumeThreadIDForTarget(store *session.Store, resume *cli.ExecResumeOptions, target string, cwd string) (session.ThreadID, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", errors.New("exec resume requires SESSION_ID or --last")
	}
	if execResumeTargetIsUUID(target) {
		return session.ThreadID(target), nil
	}
	if record, err := execResumeRecordByExactName(store, resume, target, cwd); err == nil {
		return record.ID, nil
	} else if !errors.Is(err, session.ErrThreadNotFound) {
		return "", err
	}
	if record, err := store.Read(session.ThreadID(target), true, false); err == nil {
		return record.ID, nil
	} else if !errors.Is(err, session.ErrThreadNotFound) && !errors.Is(err, session.ErrInvalidThreadID) {
		return "", err
	}
	return "", fmt.Errorf("No session found matching '%s'.", target)
}

func execResumeRecordByExactName(store *session.Store, resume *cli.ExecResumeOptions, name string, cwd string) (*session.Record, error) {
	if store == nil {
		return nil, errors.New("session store is nil")
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, session.ErrThreadNotFound
	}
	options := session.ListOptions{
		SortKey:        session.SortUpdatedAt,
		SortDirection:  session.SortDesc,
		Archived:       false,
		Search:         name,
		IncludeHistory: false,
	}
	if resume == nil || !resume.All {
		if strings.TrimSpace(cwd) != "" {
			options.CWDs = []string{cwd}
		}
	}
	page, err := store.List(options)
	if err != nil {
		return nil, err
	}
	for i := range page.Records {
		if strings.TrimSpace(page.Records[i].Title) == name {
			return &page.Records[i], nil
		}
	}
	return nil, session.ErrThreadNotFound
}

func execResumeTargetIsUUID(value string) bool {
	_, err := uuid.Parse(strings.TrimSpace(value))
	return err == nil
}

const execSkillInstructionsKind = "skill_instructions"

func execAdditionalInputSessionItems(turnID string, inputItems []any, createdAt time.Time, extraMetadata map[string]any) []session.Item {
	if len(inputItems) == 0 {
		return nil
	}
	items := make([]session.Item, 0, len(inputItems))
	for i, item := range inputItems {
		if communication, ok := execAgentCommunicationSessionItem(turnID, i, item, createdAt, extraMetadata); ok {
			items = append(items, communication)
			continue
		}
		role, text, ok := execSkillInstructionInputItemText(item)
		if !ok {
			continue
		}
		metadata := sessionMetadata(turnID, extraMetadata)
		metadata["kind"] = execSkillInstructionsKind
		items = append(items, session.Item{
			ID:        fmt.Sprintf("skill-instructions-%s-%d", safeSessionItemID(turnID), i+1),
			Type:      "message",
			Role:      role,
			Text:      text,
			Content:   []session.ContentPart{{Type: "input_text", Text: text}},
			CreatedAt: createdAt,
			Data:      map[string]any{"kind": execSkillInstructionsKind},
			Metadata:  metadata,
		})
	}
	return items
}

func execAgentCommunicationSessionItem(turnID string, index int, item any, createdAt time.Time, extraMetadata map[string]any) (session.Item, bool) {
	raw, ok := item.(map[string]any)
	if !ok || strings.TrimSpace(execStringFromAny(raw["type"])) != "agent_message" {
		return session.Item{}, false
	}
	encoded, err := json.Marshal(raw)
	if err != nil {
		return session.Item{}, false
	}
	metadata := sessionMetadata(turnID, extraMetadata)
	metadata["author"] = strings.TrimSpace(execStringFromAny(raw["author"]))
	metadata["recipient"] = strings.TrimSpace(execStringFromAny(raw["recipient"]))
	return session.Item{
		ID:        fmt.Sprintf("agent-message-%s-%d", safeSessionItemID(turnID), index+1),
		Type:      "agent_message",
		Raw:       encoded,
		CreatedAt: createdAt,
		Metadata:  metadata,
	}, true
}

func execSkillInstructionInputItemText(item any) (string, string, bool) {
	raw, ok := item.(map[string]any)
	if !ok {
		return "", "", false
	}
	if strings.TrimSpace(execStringFromAny(raw["type"])) != "message" {
		return "", "", false
	}
	role := strings.TrimSpace(execStringFromAny(raw["role"]))
	if role == "" {
		role = "user"
	}
	text := strings.TrimSpace(execTextFromInputItemContent(raw["content"]))
	if text == "" || !strings.Contains(text, "<skill>") || !strings.Contains(text, "</skill>") {
		return "", "", false
	}
	return role, text, true
}

func execTextFromInputItemContent(content any) string {
	switch typed := content.(type) {
	case []map[string]any:
		parts := make([]string, 0, len(typed))
		for _, block := range typed {
			if text := strings.TrimSpace(execStringFromAny(block["text"])); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	case []any:
		parts := make([]string, 0, len(typed))
		for _, raw := range typed {
			block, ok := raw.(map[string]any)
			if !ok {
				continue
			}
			if text := strings.TrimSpace(execStringFromAny(block["text"])); text != "" {
				parts = append(parts, text)
			}
		}
		return strings.Join(parts, "\n")
	default:
		return ""
	}
}

func execStringFromAny(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case fmt.Stringer:
		return typed.String()
	case nil:
		return ""
	default:
		return fmt.Sprint(typed)
	}
}

func execBoolPtrFromAny(value any) *bool {
	switch typed := value.(type) {
	case bool:
		return &typed
	case *bool:
		return typed
	default:
		return nil
	}
}

type execImageGenerationContext struct {
	CodexHome string
	ThreadID  string
}

func sessionItemsForTurn(turnID string, userPrompt string, userInputs []turn.TurnUserInput, result *turn.AgentLoopResult, createdAt time.Time, extraMetadata map[string]any, imageContext *execImageGenerationContext) []session.Item {
	return sessionItemsForTurnWithMode(turnID, userPrompt, userInputs, result, createdAt, extraMetadata, imageContext, false)
}

func sessionItemsForTurnWithMode(turnID string, userPrompt string, userInputs []turn.TurnUserInput, result *turn.AgentLoopResult, createdAt time.Time, extraMetadata map[string]any, imageContext *execImageGenerationContext, planMode bool) []session.Item {
	suffix := strings.TrimPrefix(turnID, "turn-")
	items := []session.Item{}
	if content := sessionContentForTurnInputs(userPrompt, userInputs); len(content) > 0 {
		items = append(items, session.Item{
			ID:        "user-" + suffix,
			Type:      "message",
			Role:      "user",
			Text:      userPrompt,
			Content:   content,
			CreatedAt: createdAt,
			Metadata:  sessionMetadata(turnID, extraMetadata),
		})
	}
	if result == nil {
		return items
	}
	executionIndex := 0
	for responseIndex, response := range result.ModelResponses() {
		if response == nil {
			continue
		}
		responseExtraMetadata := mergeSessionMetadata(extraMetadata, model.AgentResponseMetadata(response))
		fallbackAssistantID := fallbackExecAssistantSessionItemID(suffix, responseIndex)
		toolItemCount := 0
		if len(response.Items) > 0 {
			for i := range response.Items {
				if isToolAgentItemForSession(&response.Items[i]) {
					toolItemCount++
				}
				item := sessionItemFromAgentItem(turnID, fallbackAssistantID, response.ResponseID, &response.Items[i], result.TimingProfile, createdAt, responseExtraMetadata, imageContext)
				if item.ID != "" {
					items = append(items, execSessionItemsForCollaborationMode(turnID, item, planMode)...)
					if instructions, ok := execImageGenerationInstructionsSessionItem(turnID, &item, createdAt, responseExtraMetadata); ok {
						items = append(items, instructions)
					}
				}
			}
		}
		if len(response.Items) == 0 && strings.TrimSpace(response.Message) != "" {
			item := session.Item{
				ID:         fallbackAssistantID,
				Type:       "agent_message",
				Role:       "assistant",
				Text:       response.Message,
				Content:    []session.ContentPart{{Type: "output_text", Text: response.Message}},
				CreatedAt:  createdAt,
				ResponseID: response.ResponseID,
				Metadata:   addTimingProfileMetadata(sessionMetadata(turnID, responseExtraMetadata), result.TimingProfile),
			}
			items = append(items, execSessionItemsForCollaborationMode(turnID, item, planMode)...)
		}
		toolExecutions := execToolExecutionsForResponse(result.ToolExecutions, executionIndex, toolItemCount)
		for i := range toolExecutions {
			if item, ok := sessionItemForToolOutput(turnID, suffix, &toolExecutions[i], createdAt, responseExtraMetadata); ok {
				items = append(items, item)
			}
		}
		executionIndex += len(toolExecutions)
	}
	for executionIndex < len(result.ToolExecutions) {
		items = append(items, sessionItemsForToolExecution(turnID, suffix, &result.ToolExecutions[executionIndex], createdAt, extraMetadata)...)
		executionIndex++
	}
	items = insertExecSteerSessionItems(items, execSteerSessionItems(turnID, result.SteerInputItems, createdAt, extraMetadata))
	return insertExecInterAgentCompletionItems(items, execInterAgentCompletionSessionItems(turnID, currentTurnInputItems(result), createdAt, extraMetadata))
}

func execSteerSessionItems(turnID string, inputItems []any, createdAt time.Time, extraMetadata map[string]any) []session.Item {
	out := make([]session.Item, 0)
	for index, inputItem := range inputItems {
		raw, ok := inputItem.(map[string]any)
		if !ok || !strings.EqualFold(strings.TrimSpace(execStringFromAny(raw["type"])), "message") || !strings.EqualFold(strings.TrimSpace(execStringFromAny(raw["role"])), "user") {
			continue
		}
		text := strings.TrimSpace(execTextFromInputItemContent(raw["content"]))
		content := execSessionContentFromInputItem(raw["content"])
		if text == "" && len(content) == 0 {
			continue
		}
		metadata := sessionMetadata(turnID, extraMetadata)
		metadata["steered"] = true
		out = append(out, session.Item{
			ID:        fmt.Sprintf("steer-%s-%d", safeSessionItemID(turnID), index+1),
			Type:      "message",
			Role:      "user",
			Text:      text,
			Content:   content,
			CreatedAt: createdAt,
			Metadata:  metadata,
		})
	}
	return out
}

func execSessionContentFromInputItem(value any) []session.ContentPart {
	blocks := make([]map[string]any, 0)
	switch typed := value.(type) {
	case []map[string]any:
		blocks = append(blocks, typed...)
	case []any:
		for _, value := range typed {
			if block, ok := value.(map[string]any); ok {
				blocks = append(blocks, block)
			}
		}
	}
	content := make([]session.ContentPart, 0, len(blocks))
	for _, block := range blocks {
		switch strings.TrimSpace(execStringFromAny(block["type"])) {
		case "input_text", "text":
			if text := strings.TrimSpace(execStringFromAny(block["text"])); text != "" {
				content = append(content, session.ContentPart{Type: "input_text", Text: text})
			}
		case "input_image", "image":
			if imageURL := strings.TrimSpace(execStringFromAny(block["image_url"])); imageURL != "" {
				content = append(content, session.ContentPart{Type: "image", ImageURL: imageURL})
			}
		case "input_audio", "audio":
			if audioURL := strings.TrimSpace(execStringFromAny(block["audio_url"])); audioURL != "" {
				content = append(content, session.ContentPart{Type: "audio", AudioURL: audioURL})
			}
		}
	}
	return content
}

func insertExecSteerSessionItems(items []session.Item, steers []session.Item) []session.Item {
	if len(steers) == 0 {
		return items
	}
	insertAt := len(items)
	for index := len(items) - 1; index >= 0; index-- {
		if items[index].Type == "agent_message" && items[index].Role == "assistant" {
			insertAt = index
			break
		}
	}
	out := make([]session.Item, 0, len(items)+len(steers))
	out = append(out, items[:insertAt]...)
	out = append(out, steers...)
	out = append(out, items[insertAt:]...)
	return out
}

func currentTurnInputItems(result *turn.AgentLoopResult) []any {
	if result == nil || result.InitialInputCount < 0 || result.InitialInputCount >= len(result.InputItems) {
		return nil
	}
	return result.InputItems[result.InitialInputCount:]
}

func execInterAgentCompletionSessionItems(turnID string, inputItems []any, createdAt time.Time, extraMetadata map[string]any) []session.Item {
	out := make([]session.Item, 0)
	for index, inputItem := range inputItems {
		raw, ok := inputItem.(map[string]any)
		if !ok || strings.TrimSpace(execStringFromAny(raw["type"])) != "agent_message" {
			continue
		}
		text := strings.TrimSpace(execTextFromInputItemContent(raw["content"]))
		if !strings.HasPrefix(text, "Message Type: FINAL_ANSWER\n") {
			continue
		}
		item, ok := execAgentCommunicationSessionItem(turnID, index, inputItem, createdAt, extraMetadata)
		if ok {
			out = append(out, item)
		}
	}
	return out
}

func insertExecInterAgentCompletionItems(items []session.Item, completions []session.Item) []session.Item {
	if len(completions) == 0 {
		return items
	}
	insertAt := len(items)
	for index := len(items) - 1; index >= 0; index-- {
		if items[index].Type == "agent_message" && items[index].Role == "assistant" {
			insertAt = index
			break
		}
	}
	out := make([]session.Item, 0, len(items)+len(completions))
	out = append(out, items[:insertAt]...)
	out = append(out, completions...)
	out = append(out, items[insertAt:]...)
	return out
}

func execRequestPlanMode(req *Request) bool {
	if req == nil || req.CollaborationMode == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(fmt.Sprint(req.CollaborationMode["mode"])), "plan")
}

func execSessionItemsForCollaborationMode(turnID string, item session.Item, planMode bool) []session.Item {
	if !planMode || item.Type != "agent_message" {
		return []session.Item{item}
	}
	visible, plan, ok := splitExecProposedPlanText(item.Text)
	if !ok {
		return []session.Item{item}
	}
	out := make([]session.Item, 0, 2)
	if strings.TrimSpace(visible) != "" {
		item.Text = visible
		item.Content = []session.ContentPart{{Type: "output_text", Text: visible}}
		out = append(out, item)
	}
	out = append(out, session.Item{
		ID:         safeSessionItemID(turnID) + "-plan",
		Type:       "plan",
		Text:       plan,
		CreatedAt:  item.CreatedAt,
		ResponseID: item.ResponseID,
		Metadata:   cloneExecAnyMap(item.Metadata),
	})
	return out
}

func splitExecProposedPlanText(text string) (string, string, bool) {
	openStart, openEnd, ok := findExecPlanTagLine(text, "<proposed_plan>")
	if !ok {
		return text, "", false
	}
	closeStart, closeEnd, closed := findExecPlanTagLine(text[openEnd:], "</proposed_plan>")
	if !closed {
		return text[:openStart], text[openEnd:], true
	}
	closeStart += openEnd
	closeEnd += openEnd
	return text[:openStart] + text[closeEnd:], text[openEnd:closeStart], true
}

func findExecPlanTagLine(text string, tag string) (int, int, bool) {
	searchFrom := 0
	for {
		index := strings.Index(text[searchFrom:], tag)
		if index < 0 {
			return 0, 0, false
		}
		index += searchFrom
		if index > 0 && text[index-1] != '\n' {
			searchFrom = index + 1
			continue
		}
		after := index + len(tag)
		if after == len(text) {
			return index, after, true
		}
		switch text[after] {
		case '\n':
			return index, after + 1, true
		case '\r':
			if after+1 < len(text) && text[after+1] == '\n' {
				return index, after + 2, true
			}
			return index, after + 1, true
		default:
			searchFrom = index + 1
		}
	}
}

func sessionContentForTurnInputs(userPrompt string, inputs []turn.TurnUserInput) []session.ContentPart {
	content := []session.ContentPart{}
	if text := strings.TrimSpace(userPrompt); text != "" {
		content = append(content, session.ContentPart{Type: "input_text", Text: text})
	}
	for i := range inputs {
		input := inputs[i]
		inputType := strings.TrimSpace(input.Type)
		if text := strings.TrimSpace(input.Text); text != "" {
			content = append(content, session.ContentPart{Type: "input_text", Text: text})
			continue
		}
		if imageURL := strings.TrimSpace(input.URL); imageURL != "" && (inputType == "" || strings.EqualFold(inputType, "image")) {
			content = append(content, session.ContentPart{Type: "image", ImageURL: imageURL, Detail: cloneStringPointer(input.Detail)})
			continue
		}
		if path := strings.TrimSpace(input.Path); path != "" && (inputType == "" || strings.EqualFold(inputType, "localImage")) {
			content = append(content, session.ContentPart{Type: "localImage", ImageURL: path, Detail: cloneStringPointer(input.Detail)})
		}
	}
	if len(content) == 0 {
		if strings.TrimSpace(userPrompt) == "" && len(inputs) == 0 {
			return nil
		}
		return []session.ContentPart{{Type: "input_text", Text: userPrompt}}
	}
	return content
}

func fallbackExecAssistantSessionItemID(suffix string, responseIndex int) string {
	base := "assistant-" + suffix
	if responseIndex <= 0 {
		return base
	}
	return fmt.Sprintf("%s-%d", base, responseIndex+1)
}

func sessionItemsForToolExecution(turnID string, suffix string, execution *turn.ToolExecutionResult, createdAt time.Time, extraMetadata map[string]any) []session.Item {
	items := []session.Item{}
	if item, ok := sessionItemForToolCall(turnID, suffix, execution, createdAt, extraMetadata); ok {
		items = append(items, item)
	}
	if item, ok := sessionItemForToolOutput(turnID, suffix, execution, createdAt, extraMetadata); ok {
		items = append(items, item)
	}
	return items
}

func sessionItemForToolCall(turnID string, suffix string, execution *turn.ToolExecutionResult, createdAt time.Time, extraMetadata map[string]any) (session.Item, bool) {
	if execution == nil || execution.Invocation == nil {
		return session.Item{}, false
	}
	callID := execToolExecutionCallID(execution, createdAt)
	metadata := addTimingMetadata(sessionMetadata(turnID, extraMetadata), execution.StartedAt, time.Time{})
	metadata["toolName"] = execution.Invocation.ToolName.Key()
	metadata["payloadKind"] = string(execution.Invocation.Payload.Kind)
	metadata["source"] = execution.Invocation.Source
	metadata["callId"] = callID
	return session.Item{
		ID:        "tool-call-" + suffix + "-" + safeSessionItemID(callID),
		Type:      toolSessionItemType(execution.Invocation.Payload.Kind),
		Name:      execution.Invocation.ToolName.Key(),
		CallID:    callID,
		Text:      toolInvocationText(execution.Invocation),
		Data:      toolInvocationData(execution.Invocation),
		CreatedAt: createdAt,
		Metadata:  metadata,
	}, true
}

func sessionItemForToolOutput(turnID string, suffix string, execution *turn.ToolExecutionResult, createdAt time.Time, extraMetadata map[string]any) (session.Item, bool) {
	if execution == nil || execution.Invocation == nil || execution.Output == nil {
		return session.Item{}, false
	}
	callID := execToolExecutionCallID(execution, createdAt)
	outputMetadata := addTimingMetadata(sessionMetadata(turnID, extraMetadata), execution.StartedAt, execution.FinishedAt)
	outputMetadata["toolName"] = execution.Invocation.ToolName.Key()
	// Rust persists the concrete ResponseItem variant for both sides of a tool
	// exchange. Preserve the payload kind on the output too, otherwise a
	// tool-search output is reconstructed as a function_call_output, discarded
	// as an orphan, and replaced with a synthetic empty search result on resume.
	outputMetadata["payloadKind"] = string(execution.Invocation.Payload.Kind)
	outputMetadata["success"] = execution.Output.Success
	outputMetadata["callId"] = callID
	if strings.TrimSpace(execution.Output.Error) != "" {
		outputMetadata["error"] = execution.Output.Error
	}
	outputCreatedAt := execution.Output.CompletedAt
	if outputCreatedAt.IsZero() {
		outputCreatedAt = createdAt
	}
	return session.Item{
		ID:        newToolOutputResponseItemID(execution.Invocation.Payload.Kind),
		Type:      "tool_output",
		CallID:    callID,
		Text:      execution.Output.Body,
		Data:      toolOutputData(execution.Output),
		CreatedAt: outputCreatedAt,
		Metadata:  outputMetadata,
	}, true
}

func newToolOutputResponseItemID(kind tool.PayloadKind) string {
	prefix := "fco"
	switch kind {
	case tool.PayloadCustom:
		prefix = "ctco"
	case tool.PayloadToolSearch:
		prefix = "tso"
	}
	if id, err := uuid.NewV7(); err == nil {
		return prefix + "_" + id.String()
	}
	return prefix + "_" + uuid.NewString()
}

func execToolExecutionCallID(execution *turn.ToolExecutionResult, createdAt time.Time) string {
	if execution == nil || execution.Invocation == nil {
		return fmt.Sprintf("tool-%d", createdAt.UnixNano())
	}
	return firstNonEmpty(execution.Invocation.CallID, fmt.Sprintf("tool-%d", createdAt.UnixNano()))
}

func execToolExecutionsForResponse(executions []turn.ToolExecutionResult, start int, count int) []turn.ToolExecutionResult {
	if start < 0 || count <= 0 || start >= len(executions) {
		return nil
	}
	end := start + count
	if end > len(executions) {
		end = len(executions)
	}
	return executions[start:end]
}

func sessionMetadata(turnID string, extra map[string]any) map[string]any {
	metadata := map[string]any{"turnId": turnID}
	for key, value := range extra {
		metadata[key] = value
	}
	return metadata
}

func mergeSessionMetadata(left map[string]any, right map[string]any) map[string]any {
	if len(left) == 0 && len(right) == 0 {
		return nil
	}
	merged := map[string]any{}
	for key, value := range left {
		merged[key] = value
	}
	for key, value := range right {
		merged[key] = value
	}
	return merged
}

func addTimingMetadata(metadata map[string]any, started time.Time, completed time.Time) map[string]any {
	if metadata == nil {
		metadata = map[string]any{}
	}
	if !started.IsZero() {
		value := started.UTC().UnixMilli()
		metadata["startedAtMs"] = value
		metadata["started_at_ms"] = value
	}
	if !completed.IsZero() {
		value := completed.UTC().UnixMilli()
		metadata["completedAtMs"] = value
		metadata["completed_at_ms"] = value
	}
	if !started.IsZero() && !completed.IsZero() && !completed.Before(started) {
		value := completed.Sub(started).Milliseconds()
		metadata["durationMs"] = value
		metadata["duration_ms"] = value
	}
	return metadata
}

func sessionItemFromAgentItem(turnID string, fallbackID string, responseID string, item *model.AgentItem, timingProfile *turn.Profile, createdAt time.Time, extraMetadata map[string]any, imageContext *execImageGenerationContext) session.Item {
	if item == nil {
		return session.Item{}
	}
	if item.Type == "image_generation_call" {
		return sessionItemFromImageGenerationAgentItem(turnID, fallbackID, responseID, item, timingProfile, createdAt, extraMetadata, imageContext)
	}
	if item.Type == "web_search_call" {
		query, action := webSearchActionFromAgentItem(item)
		raw, _ := json.Marshal(item)
		return session.Item{
			ID:         firstNonEmpty(item.ID, item.CallID, fallbackID),
			Type:       "webSearch",
			Role:       "assistant",
			Text:       query,
			CreatedAt:  createdAt,
			Data:       map[string]any{"query": query, "action": action},
			Metadata:   addTimingProfileMetadata(sessionMetadata(turnID, extraMetadata), timingProfile),
			Raw:        raw,
			ResponseID: responseID,
		}
	}
	text := firstNonEmpty(item.Text, item.Arguments, item.Input)
	metadata := addTimingProfileMetadata(sessionMetadata(turnID, extraMetadata), timingProfile)
	if item.Name != "" {
		metadata["toolName"] = item.Name
	}
	for key, value := range item.Data {
		metadata[key] = value
	}
	data := cloneMap(item.Data)
	if isToolSessionItemType(item.Type) {
		if data == nil {
			data = map[string]any{}
		}
		// Mirror appserver addShellCommandData so shell-command tool calls
		// convert to CommandExecution in paginated threads (Rust #38774).
		addExecShellCommandData(data, item.Arguments)
		if item.Arguments != "" {
			data["arguments"] = item.Arguments
		}
		if item.Input != "" {
			data["input"] = item.Input
		}
	}
	raw, _ := json.Marshal(item)
	out := session.Item{
		ID:         firstNonEmpty(item.ID, item.CallID, fallbackID),
		Type:       firstNonEmpty(item.Type, "agent_message"),
		Role:       "assistant",
		Text:       text,
		Name:       item.Name,
		Namespace:  item.Namespace,
		CallID:     item.CallID,
		CreatedAt:  createdAt,
		Data:       data,
		Metadata:   metadata,
		Raw:        raw,
		ResponseID: responseID,
	}
	if out.Type == "message" {
		out.Type = "agent_message"
	}
	if text != "" {
		out.Content = []session.ContentPart{{Type: "output_text", Text: text}}
	}
	return out
}

func sessionItemFromImageGenerationAgentItem(turnID string, fallbackID string, responseID string, item *model.AgentItem, timingProfile *turn.Profile, createdAt time.Time, extraMetadata map[string]any, imageContext *execImageGenerationContext) session.Item {
	itemID := firstNonEmpty(strings.TrimSpace(item.ID), strings.TrimSpace(item.CallID), fallbackID)
	if itemID == "" {
		itemID = "image-generation-" + safeSessionItemID(turnID)
	}
	data := cloneMap(item.Data)
	if data == nil {
		data = map[string]any{}
	}
	result := firstNonEmpty(execStringFromAny(data["result"]), strings.TrimSpace(item.Text))
	status := model.NormalizeImageGenerationStatus(firstNonEmpty(strings.TrimSpace(item.Status), execStringFromAny(data["status"])), result)
	revisedPrompt := firstNonEmpty(execStringFromAny(data["revisedPrompt"]), execStringFromAny(data["revised_prompt"]))
	data["status"] = status
	data["result"] = result
	if revisedPrompt != "" {
		data["revisedPrompt"] = revisedPrompt
		data["revised_prompt"] = revisedPrompt
	}
	if result != "" && firstNonEmpty(execStringFromAny(data["savedPath"]), execStringFromAny(data["saved_path"])) == "" && imageContext != nil {
		if codexHome := strings.TrimSpace(imageContext.CodexHome); codexHome != "" {
			threadID := firstNonEmpty(strings.TrimSpace(imageContext.ThreadID), turnID)
			if savedPath, err := eventmap.SaveImageGenerationResult(codexHome, threadID, itemID, result); err == nil {
				data["savedPath"] = savedPath
				data["saved_path"] = savedPath
			}
		}
	}
	item.Data = cloneMap(data)
	item.Status = status
	raw, _ := json.Marshal(item)
	metadata := addTimingProfileMetadata(sessionMetadata(turnID, extraMetadata), timingProfile)
	for key, value := range data {
		if key == "result" {
			continue
		}
		metadata[key] = value
	}
	return session.Item{
		ID:         itemID,
		Type:       "imageGeneration",
		Status:     status,
		Text:       revisedPrompt,
		CreatedAt:  createdAt,
		Data:       data,
		Metadata:   metadata,
		Raw:        raw,
		ResponseID: responseID,
	}
}

const execImageGenerationInstructionsKind = "image_generation_instructions"

func execImageGenerationInstructionsSessionItem(turnID string, imageItem *session.Item, createdAt time.Time, extraMetadata map[string]any) (session.Item, bool) {
	if imageItem == nil || imageItem.Type != "imageGeneration" {
		return session.Item{}, false
	}
	savedPath := firstNonEmpty(execStringFromAny(imageItem.Data["savedPath"]), execStringFromAny(imageItem.Data["saved_path"]))
	if strings.TrimSpace(savedPath) == "" {
		return session.Item{}, false
	}
	outputDir := filepath.Dir(savedPath)
	outputPath := filepath.Join(outputDir, "<image_id>.png")
	text := fmt.Sprintf("Generated images are saved to %s as %s by default.\nIf you need to use a generated image at another path, copy it and leave the original in place unless the user explicitly asks you to delete it.", outputDir, outputPath)
	if len(text) > 1024 {
		return session.Item{}, false
	}
	metadata := sessionMetadata(turnID, extraMetadata)
	metadata["kind"] = execImageGenerationInstructionsKind
	return session.Item{
		ID:        "image-generation-instructions-" + safeSessionItemID(firstNonEmpty(imageItem.ID, turnID)),
		Type:      "message",
		Role:      "developer",
		Text:      text,
		Content:   []session.ContentPart{{Type: "input_text", Text: text}},
		CreatedAt: createdAt,
		Data: map[string]any{
			"kind":                execImageGenerationInstructionsKind,
			"imageGenerationId":   imageItem.ID,
			"image_generation_id": imageItem.ID,
			"savedPath":           savedPath,
			"saved_path":          savedPath,
		},
		Metadata:   metadata,
		ResponseID: imageItem.ResponseID,
	}, true
}

func addTimingProfileMetadata(metadata map[string]any, profile *turn.Profile) map[string]any {
	if metadata == nil {
		metadata = map[string]any{}
	}
	if profile == nil {
		return metadata
	}
	metadata["timingProfile"] = timingProfileCamelMap(profile)
	metadata["timing_profile"] = timingProfileSnakeMap(profile)
	return metadata
}

func timingProfileCamelMap(profile *turn.Profile) map[string]any {
	if profile == nil {
		return nil
	}
	return map[string]any{
		"beforeFirstSamplingMs":      profile.BeforeFirstSamplingMS,
		"samplingMs":                 profile.SamplingMS,
		"betweenSamplingOverheadMs":  profile.BetweenSamplingOverheadMS,
		"toolBlockingMs":             profile.ToolBlockingMS,
		"pendingIdleAfterSamplingMs": profile.PendingIdleAfterSamplingMS,
		"samplingRequestCount":       profile.SamplingRequestCount,
		"samplingRetryCount":         profile.SamplingRetryCount,
		"totalMs":                    profile.TotalMS,
	}
}

func timingProfileSnakeMap(profile *turn.Profile) map[string]any {
	if profile == nil {
		return nil
	}
	return map[string]any{
		"before_first_sampling_ms":       profile.BeforeFirstSamplingMS,
		"sampling_ms":                    profile.SamplingMS,
		"between_sampling_overhead_ms":   profile.BetweenSamplingOverheadMS,
		"tool_blocking_ms":               profile.ToolBlockingMS,
		"pending_idle_after_sampling_ms": profile.PendingIdleAfterSamplingMS,
		"sampling_request_count":         profile.SamplingRequestCount,
		"sampling_retry_count":           profile.SamplingRetryCount,
		"total_ms":                       profile.TotalMS,
	}
}

func isToolAgentItemForSession(item *model.AgentItem) bool {
	if item == nil {
		return false
	}
	switch item.Type {
	case "function_call", "custom_tool_call", "tool_search_call":
		return true
	default:
		return false
	}
}

func toolSessionItemType(kind tool.PayloadKind) string {
	switch kind {
	case tool.PayloadCustom:
		return "custom_tool_call"
	case tool.PayloadToolSearch:
		return "tool_search_call"
	default:
		return "function_call"
	}
}

func toolInvocationData(invocation *tool.Invocation) map[string]any {
	if invocation == nil {
		return nil
	}
	data := map[string]any{
		"name":         invocation.ToolName.Key(),
		"call_id":      invocation.CallID,
		"payload_kind": string(invocation.Payload.Kind),
	}
	if invocation.Payload.Arguments != "" {
		data["arguments"] = invocation.Payload.Arguments
		addExecShellCommandData(data, invocation.Payload.Arguments)
	}
	if invocation.Payload.Input != "" {
		data["input"] = invocation.Payload.Input
	}
	if invocation.Payload.Search != nil {
		data["search"] = invocation.Payload.Search
		data["arguments_map"] = invocation.Payload.Search
	}
	return data
}

func isToolSessionItemType(itemType string) bool {
	switch strings.TrimSpace(itemType) {
	case "function_call", "custom_tool_call", "tool_search_call", "function_call_output", "custom_tool_call_output", "tool_search_output", "tool_output":
		return true
	default:
		return false
	}
}

// addExecShellCommandData mirrors appserver addShellCommandData: when tool
// arguments are JSON with a "cmd"/"cwd"/"workdir" key, the parsed shell
// command is recorded on the item data so the paginated converter recognizes
// the tool as a CommandExecution (Rust #38774).
func addExecShellCommandData(data map[string]any, arguments string) {
	if data == nil || strings.TrimSpace(arguments) == "" {
		return
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return
	}
	if command, ok := args["cmd"].(string); ok && strings.TrimSpace(command) != "" {
		data["command"] = command
	}
	if cwd, ok := args["cwd"].(string); ok && strings.TrimSpace(cwd) != "" {
		data["cwd"] = cwd
	}
	if workdir, ok := args["workdir"].(string); ok && strings.TrimSpace(workdir) != "" {
		data["cwd"] = workdir
	}
}

func toolOutputData(output *tool.Output) map[string]any {
	if output == nil {
		return nil
	}
	data := map[string]any{
		"call_id": output.CallID,
		"success": output.Success,
	}
	if output.Body != "" {
		data["output"] = output.Body
	}
	if output.Error != "" {
		data["error"] = output.Error
	}
	for key, value := range output.Data {
		data[key] = value
	}
	return data
}

func toolInvocationText(invocation *tool.Invocation) string {
	if invocation == nil {
		return ""
	}
	switch invocation.Payload.Kind {
	case tool.PayloadCustom:
		return invocation.Payload.Input
	case tool.PayloadToolSearch:
		data, err := json.Marshal(invocation.Payload.Search)
		if err != nil {
			return ""
		}
		return string(data)
	default:
		return invocation.Payload.Arguments
	}
}

func safeSessionItemID(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	var builder strings.Builder
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			builder.WriteRune(r)
		default:
			builder.WriteByte('-')
		}
	}
	out := strings.Trim(builder.String(), "-")
	if out == "" {
		return "unknown"
	}
	return out
}

func stringPointerIfEmpty(existing string, value string) *string {
	if strings.TrimSpace(existing) != "" || strings.TrimSpace(value) == "" {
		return nil
	}
	value = strings.TrimSpace(value)
	return &value
}

func stringPointerIfNotEmpty(value string) *string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	return &value
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func (r *Runner) now() time.Time {
	if r == nil || r.Now == nil {
		return time.Now()
	}
	return r.Now()
}

func taskKind(req *Request) model.AgentTaskKind {
	if req != nil && req.Exec.Subcommand == "review" {
		return model.AgentTaskReview
	}
	return model.AgentTaskRegular
}

func effectiveModel(req *Request, cfg *config.Config) string {
	if req != nil && req.Exec.Subcommand == "review" {
		if value := stringConfigValue(cfg, "review_model"); value != "" {
			return value
		}
	}
	if req != nil {
		if value := firstNonEmpty(req.Exec.Shared.Model, req.Root.Shared.Model); value != "" {
			return value
		}
	}
	if value := stringConfigValue(cfg, "model"); value != "" {
		return value
	}
	manager := model.NewStaticModelsManager(model.BundledModelsResponse())
	return manager.GetDefaultModel("", true, model.RefreshOffline)
}

func modelSupportsParallelToolCalls(modelID string) bool {
	if strings.TrimSpace(modelID) == "" {
		return false
	}
	manager := model.NewStaticModelsManager(model.BundledModelsResponse())
	info := manager.GetModelInfo(modelID, nil)
	return info.SupportsParallelToolCalls && !info.UseResponsesLite
}

func modelUsesResponsesLite(modelID string) bool {
	if strings.TrimSpace(modelID) == "" {
		return false
	}
	manager := model.NewStaticModelsManager(model.BundledModelsResponse())
	info := manager.GetModelInfo(modelID, nil)
	return info.UseResponsesLite
}

func effectiveReasoningEffort(req *Request, cfg *config.Config) string {
	return firstNonEmpty(
		reqSharedValue(req, func(shared cli.SharedOptions) string { return shared.ModelReasoningEffort }),
		stringConfigValue(cfg, "model_reasoning_effort"),
		stringConfigValue(cfg, "modelReasoningEffort"),
		stringConfigValue(cfg, "reasoning_effort"),
		stringConfigValue(cfg, "reasoningEffort"),
	)
}

func reqSharedValue(req *Request, value func(cli.SharedOptions) string) string {
	if req == nil || value == nil {
		return ""
	}
	return firstNonEmpty(value(req.Exec.Shared), value(req.Root.Shared))
}

func effectiveModelVerbosity(cfg *config.Config) string {
	return firstNonEmpty(
		stringConfigValue(cfg, "model_verbosity"),
		stringConfigValue(cfg, "modelVerbosity"),
	)
}

func effectiveServiceTier(cfg *config.Config, modelID string) string {
	settings := map[string]bool{}
	if cfg != nil {
		settings = cfg.FeatureSettings()
	}
	if !features.Enabled(settings, "fast_mode") {
		return ""
	}
	value := firstNonEmpty(
		stringConfigValue(cfg, "service_tier"),
		stringConfigValue(cfg, "serviceTier"),
	)
	if value == "" {
		return ""
	}
	manager := model.NewStaticModelsManager(model.BundledModelsResponse())
	info := manager.GetModelInfo(modelID, nil)
	return model.ServiceTierForRequest(&info, value)
}

func effectiveIncludeTimingMetrics(cfg *config.Config) bool {
	return boolConfigValue(cfg, "include_timing_metrics") ||
		boolConfigValue(cfg, "includeTimingMetrics") ||
		boolConfigValue(cfg, "responsesapi_include_timing_metrics") ||
		boolConfigValue(cfg, "responsesapiIncludeTimingMetrics")
}

func effectiveProvider(req *Request, cfg *config.Config) (string, error) {
	if req != nil {
		if value := firstNonEmpty(req.Exec.Shared.OSSProvider, req.Root.Shared.OSSProvider); value != "" {
			if err := model.ValidateProviderID(value); err != nil {
				return "", err
			}
			return value, nil
		}
		if req.Exec.Shared.OSS || req.Root.Shared.OSS {
			if value := stringConfigValue(cfg, "oss_provider"); value != "" {
				if err := model.ValidateProviderID(value); err != nil {
					return "", err
				}
				return value, nil
			}
			return model.OllamaOSSProviderID, nil
		}
	}
	if value := stringConfigValue(cfg, "model_provider"); value != "" {
		if err := model.ValidateProviderID(value); err != nil {
			return "", err
		}
		return value, nil
	}
	return model.OpenAIProviderID, nil
}

func stringConfigValue(cfg *config.Config, key string) string {
	if cfg == nil || cfg.Values == nil {
		return ""
	}
	value, ok := cfg.Values[key].(string)
	if !ok {
		return ""
	}
	return strings.TrimSpace(value)
}

func boolConfigValue(cfg *config.Config, key string) bool {
	if cfg == nil || cfg.Values == nil {
		return false
	}
	switch value := cfg.Values[key].(type) {
	case bool:
		return value
	case string:
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "true", "1", "yes", "on":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

func configValues(cfg *config.Config) map[string]any {
	if cfg == nil || cfg.Values == nil {
		return map[string]any{}
	}
	return cfg.Values
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func nonEmptyStrings(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			out = append(out, value)
		}
	}
	return out
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	if index := strings.IndexByte(value, '\n'); index >= 0 {
		return strings.TrimSpace(value[:index])
	}
	return value
}
