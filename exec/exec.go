package exec

import (
	"bytes"
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
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
	"strings"
	"sync"
	"time"

	"codex_go/auth"
	"codex_go/cli"
	"codex_go/codexapi"
	"codex_go/config"
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
	AdditionalInstructions string
	AdditionalInputItems   []any
}

type Result struct {
	ThreadID    string
	TurnID      string
	SessionPath string
	LastMessage string
	Prompt      string
	Events      []protocol.ThreadEvent
}

type Runner struct {
	CodexHome       string
	Agent           model.AgentRunner
	ToolRouter      *tool.Router
	UnifiedExec     *tool.UnifiedExecManager
	Hooks           tool.HookRunner
	ShellApproval   tool.ShellApprovalFunc
	UserInput       tool.UserInputResponder
	MCPService      *mcp.MCPService
	MCPTools        []mcp.RuntimeToolInfo
	MCPConnectors   []mcp.RuntimeConnector
	MCPElicitation  mcp.MCPElicitationHandler
	MaxToolTurns    int
	UseResponsesAPI bool
	HTTPClient      model.HTTPDoer
	Now             func() time.Time
}

func NewRunner(codexHome string) *Runner {
	return &Runner{
		CodexHome:       codexHome,
		UseResponsesAPI: true,
		Now:             time.Now,
		UnifiedExec:     tool.NewUnifiedExecManager(),
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
	if req.Exec.Subcommand != "" && req.Exec.Subcommand != "review" && req.Exec.Subcommand != "resume" {
		return nil, fmt.Errorf("unknown exec subcommand %s", req.Exec.Subcommand)
	}
	if warning := removedFullAutoWarning(req.Exec); warning != "" {
		fmt.Fprintln(stderr, warning)
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
	outputSchema, err := loadOutputSchema(req.Exec.OutputSchema)
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

	prompt, resumeContext, err := r.promptForRequest(req, stdin)
	if err != nil {
		return nil, err
	}
	requestInputs := requestTurnUserInputs(req)
	if strings.TrimSpace(prompt) == "" && len(requestInputs) == 0 {
		return nil, errors.New("No prompt provided. Either specify one as an argument or pipe the prompt into stdin.")
	}
	identityPrompt := firstNonEmpty(prompt, turnUserInputsSummary(requestInputs))

	threadID := deterministicThreadID(identityPrompt)
	if resumeContext != nil && resumeContext.Record != nil {
		threadID = string(resumeContext.Record.ID)
	}
	turnID := deterministicTurnID(identityPrompt)
	installationID := ""
	if codexHome := strings.TrimSpace(r.CodexHome); codexHome != "" {
		installationID, _ = install.ResolveInstallationID(codexHome)
	}
	modelID := effectiveModel(req, cfg)
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
	runPrompt := prompt
	inputItems := execStartupInputItems(req, permissionProfile, r.now())
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
	if !req.Exec.JSON && stderr != nil {
		writeHumanConfigSummary(stderr, req, cfg, identityPrompt, threadID, modelID, providerID, approvalPolicy, permissionProfile, reasoningEffort)
	}
	eventSink := newExecEventSink(stdout, req.Exec.JSON)
	if !req.Exec.JSON && stderr != nil {
		eventSink.human = newExecHumanRenderer(stderr, execColorFlagValue(req.Exec))
	}
	if err := eventSink.Emit(protocol.ThreadStarted(threadID)); err != nil {
		return nil, err
	}
	if err := eventSink.Emit(protocol.TurnStarted()); err != nil {
		return nil, err
	}
	streamCollector := &execStreamEventCollector{sink: eventSink}
	streamCollector.streamAssistantDeltas = req.Exec.StreamAssistantDeltas
	mcpService, mcpTools, mcpConnectors := r.configuredMCPRuntimeForConfig(cfg, resolvedAuth)
	var imageGenerationOptions *turn.ImageGenerationOptions
	var hostedTools []any
	if req.Exec.Subcommand != "review" {
		imageGenerationOptions, err = r.imageGenerationOptionsForRun(cfg, resolvedAuth, providerID, modelID, threadID, inputItems)
		if err != nil {
			_ = eventSink.Emit(protocol.ErrorEvent(err.Error()))
			_ = eventSink.Emit(protocol.TurnFailed(err.Error()))
			return nil, err
		}
		hostedTools, err = r.hostedToolsForRun(cfg, resolvedAuth, providerID, modelID, imageGenerationOptions)
		if err != nil {
			_ = eventSink.Emit(protocol.ErrorEvent(err.Error()))
			_ = eventSink.Emit(protocol.TurnFailed(err.Error()))
			return nil, err
		}
	}
	subagentHeader, subagentKind := execReviewSubagentMetadata(req)
	turnResult, err := r.runAgentTurn(ctx, req, agent, &agentRunConfig{
		Prompt:                       runPrompt,
		InputItems:                   inputItems,
		Model:                        modelID,
		ProviderID:                   providerID,
		TaskKind:                     taskKind,
		ThreadID:                     threadID,
		TurnID:                       turnID,
		PreviousResponseID:           resumePreviousResponseID(resumeContext),
		ParallelToolCalls:            parallelToolCalls,
		ReasoningEffort:              reasoningEffort,
		ConcurrentReasoningSummaries: concurrentReasoningSummaries,
		ModelVerbosity:               modelVerbosity,
		IncludeTimingMetrics:         includeTimingMetrics,
		BetaFeaturesHeader:           betaFeaturesHeader,
		ItemIDsEnabled:               itemIDsEnabled,
		PromptCacheKey:               threadID,
		ServiceTier:                  serviceTier,
		Instructions:                 instructions,
		ClientMetadata: turn.BuildResponsesClientMetadata(&turn.ResponsesClientMetadataOptions{
			InstallationID:   installationID,
			SessionID:        threadID,
			ThreadID:         threadID,
			TurnID:           turnID,
			WindowID:         threadID + ":1",
			RequestKind:      codexapi.ClientRequestTurn,
			SubagentHeader:   subagentHeader,
			SubagentKind:     subagentKind,
			Extra:            cfg.ResponsesAPIClientMetadata(),
			UseResponsesLite: useResponsesLite,
		}),
		OutputSchema:                 outputSchema,
		ApprovalPolicy:               approvalPolicy,
		StreamEvents:                 streamCollector,
		PermissionProfileID:          sandboxPermissionProfileID(permissionProfile),
		PermissionProfile:            sandboxPermissionProfile(permissionProfile),
		MCPService:                   mcpService,
		MCPTools:                     mcpTools,
		MCPConnectors:                mcpConnectors,
		ImageGeneration:              imageGenerationOptions,
		HostedTools:                  hostedTools,
		DisableHostedImageGeneration: req.Exec.Subcommand == "review",
		ToolOutputTokenLimit:         cfg.ToolOutputTokenLimit(),
		UnifiedExecEnabled:           features.Enabled(cfg.FeatureSettings(), "unified_exec"),
	})
	if err != nil {
		_ = eventSink.Emit(protocol.ErrorEvent(err.Error()))
		_ = eventSink.Emit(protocol.TurnFailed(err.Error()))
		return nil, err
	}
	if err := eventSink.Err(); err != nil {
		return nil, err
	}
	lastMessage, hasLastMessage := finalMessageForRequest(req, turnResult)
	sessionPath, err := r.persistSession(req, threadID, turnID, prompt, requestInputs, turnResult, resumeContext)
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

	if err := emitFinalEventsFromAgentResult(eventSink, turnResult); err != nil {
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
	}, nil
}

func execReviewSubagentMetadata(req *Request) (string, string) {
	if req != nil && req.Exec.Subcommand == "review" {
		return string(model.AgentTaskReview), string(model.AgentTaskReview)
	}
	return "", ""
}

type agentRunConfig struct {
	Prompt                       string
	Instructions                 string
	InputItems                   []any
	Model                        string
	ProviderID                   string
	TaskKind                     model.AgentTaskKind
	ThreadID                     string
	TurnID                       string
	PreviousResponseID           string
	ParallelToolCalls            bool
	ReasoningEffort              string
	ReasoningSummary             string
	ConcurrentReasoningSummaries bool
	ModelVerbosity               string
	IncludeTimingMetrics         bool
	BetaFeaturesHeader           string
	ItemIDsEnabled               bool
	PromptCacheKey               string
	ServiceTier                  string
	ClientMetadata               map[string]string
	OutputSchema                 any
	ApprovalPolicy               sandbox.AskForApproval
	StreamEvents                 *execStreamEventCollector
	PermissionProfileID          string
	PermissionProfile            *sandbox.PermissionProfile
	MCPService                   *mcp.MCPService
	MCPTools                     []mcp.RuntimeToolInfo
	MCPConnectors                []mcp.RuntimeConnector
	ImageGeneration              *turn.ImageGenerationOptions
	HostedTools                  []any
	DisableHostedImageGeneration bool
	ToolOutputTokenLimit         *int
	UnifiedExecEnabled           bool
}

type execStreamEventCollector struct {
	sink                  *execEventSink
	events                []protocol.ThreadEvent
	streamAssistantDeltas bool
	streamedAgentText     map[string]string
	completedAgentItems   map[string]bool
}

func (c *execStreamEventCollector) Handle(event *model.ResponsesStreamEvent) {
	if c == nil || event == nil {
		return
	}
	switch event.Kind {
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
		// Rust creates command cells from the execution lifecycle, after the
		// complete command is known. The model's output-added event can carry
		// empty arguments and must not create a generic exec_command cell.
		if event.Item.Name == tool.DefaultExecCommandToolName {
			return
		}
		// MCP calls get their canonical lifecycle item from ToolStarted, once
		// routing has resolved the raw server and tool names.
		if streamAgentItemLooksLikeMCP(event.Item) {
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
			c.emit(protocol.ItemCompleted(protocol.AgentMessageItem(itemID, event.Item.Text)))
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
		changes := fileChangesFromAny(tool.ApplyPatchChanges(invocation, ""))
		if len(changes) > 0 {
			item := protocol.FileChangeItem(firstNonEmpty(invocation.CallID, "apply_patch"), changes, "in_progress")
			autoApproved := true
			item.AutoApproved = &autoApproved
			c.emit(protocol.ItemStarted(item))
		}
		return
	}
	if invocation.ToolName.Key() != tool.DefaultExecCommandToolName {
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

func isExecWebSearchInvocation(invocation *tool.Invocation) bool {
	return invocation != nil &&
		invocation.ToolName.Namespace == turn.WebSearchNamespace &&
		invocation.ToolName.Name == turn.WebSearchRunTool
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
	mu      sync.Mutex
	events  []protocol.ThreadEvent
	encoder *json.Encoder
	human   *execHumanRenderer
	err     error
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
	router, err := r.toolRouterForRequest(req, run)
	if err != nil {
		return nil, err
	}
	if run.StreamEvents != nil {
		if responsesAgent, ok := agent.(*model.ResponsesAgentRunner); ok && responsesAgent != nil {
			agent = responsesAgent.WithStreamHandler(run.StreamEvents.Handle)
		}
	}
	return turn.NewRuntime(&turn.RuntimeOptions{
		Agent:    agent,
		Router:   router,
		Hooks:    r.Hooks,
		Now:      r.now,
		MaxTurns: r.MaxToolTurns,
	}).Run(ctx, &turn.AgentLoopRequest{
		Prompt:                       run.Prompt,
		Instructions:                 run.Instructions,
		InputItems:                   append([]any(nil), run.InputItems...),
		HostedTools:                  append([]any(nil), run.HostedTools...),
		Model:                        run.Model,
		ProviderID:                   run.ProviderID,
		TaskKind:                     run.TaskKind,
		ThreadID:                     run.ThreadID,
		TurnID:                       run.TurnID,
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
	})
}

func (r *Runner) toolRouterForRequest(req *Request, run *agentRunConfig) (*tool.Router, error) {
	if r == nil {
		return nil, errors.New("exec runner is nil")
	}
	if r.ToolRouter != nil {
		return r.ToolRouter, nil
	}
	options := turn.DefaultToolRegistryOptions(requestCWD(req))
	options.UnifiedExec = r.UnifiedExec
	if run != nil {
		options.EnableUnifiedExec = run.UnifiedExecEnabled
	}
	if options.Shell != nil {
		options.Shell.Approval = r.ShellApproval
		if run != nil {
			options.Shell.MaxOutputTokens = run.ToolOutputTokenLimit
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
		options.ImageGeneration = run.ImageGeneration
	}
	options.EnableAgents = false
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
	runtimeConfig := mcp.RuntimeConfigFromValuesWithAuth(cfg.Values, r.CodexHome, runtimeAuth)
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

func (r *Runner) hostedToolsForRun(cfg *config.Config, resolvedAuth *auth.ResolvedAuth, providerID string, modelID string, standaloneImageGeneration *turn.ImageGenerationOptions) ([]any, error) {
	if r == nil || !r.UseResponsesAPI || standaloneImageGeneration != nil {
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
	if !imageGenerationHostedEnabledExec(*provider, runtimeProvider.Capabilities(), &modelInfo, snapshot, cfg.FeatureSettings()) {
		return nil, nil
	}
	return []any{turn.HostedImageGenerationTool("png")}, nil
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
	if req != nil && (req.Exec.Shared.DangerouslyBypassApprovalsAndSandbox ||
		req.Root.Shared.DangerouslyBypassApprovalsAndSandbox ||
		req.Exec.RemovedFullAuto) {
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
}

func resumeInputItems(ctx *execResumeContext) []any {
	if ctx == nil || ctx.Record == nil {
		return nil
	}
	return session.InputItemsFromRecord(ctx.Record, &session.HistoryBuildOptions{IncludeToolOutputs: true, CWD: strings.TrimSpace(ctx.Record.Metadata.CWD)})
}

func resumePreviousResponseID(ctx *execResumeContext) string {
	if ctx == nil || ctx.Record == nil {
		return ""
	}
	if strings.TrimSpace(ctx.Record.Metadata.LastResponseID) != "" {
		return strings.TrimSpace(ctx.Record.Metadata.LastResponseID)
	}
	return strings.TrimSpace(ctx.Record.Metadata.PreviousResponseID)
}

func (r *Runner) promptForRequest(req *Request, stdin io.Reader) (string, *execResumeContext, error) {
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
		if req.Exec.Resume.Prompt == "" && len(req.Input) > 0 {
			return "", &execResumeContext{Record: record}, nil
		}
		resumePrompt, err := resolveExecResumePrompt(req.Exec.Resume.Prompt, stdin)
		if err != nil {
			return "", nil, err
		}
		return resumePrompt, &execResumeContext{Record: record, UserPrompt: resumePrompt}, nil
	}
	if req.Exec.Prompt == "" && len(req.Input) > 0 {
		return "", nil, nil
	}
	resolved, err := prompt.Resolve(req.Exec.Prompt, stdin)
	return resolved, nil, err
}

func resolveExecResumePrompt(promptArg string, stdin io.Reader) (string, error) {
	if promptArg != "" && promptArg != "-" {
		return promptArg, nil
	}
	return prompt.Resolve(promptArg, stdin)
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
	images := make([]string, 0, len(req.Root.Shared.Images)+len(req.Exec.Shared.Images))
	images = append(images, req.Root.Shared.Images...)
	images = append(images, req.Exec.Shared.Images...)
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

func execStartupInputItems(req *Request, permissions *config.SandboxPermissionProfileResolution, now time.Time) []any {
	items := make([]any, 0, 2)
	if item := developerMessageInputItem(execPermissionsInstructions(permissions)); item != nil {
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

func execPermissionsInstructions(permissions *config.SandboxPermissionProfileResolution) string {
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
	return fmt.Sprintf("<permissions instructions>\nFilesystem sandboxing defines which files can be read or written. `sandbox_mode` is `%s`: %s Network access is %s.\n</permissions instructions>", mode, detail, network)
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

func deterministicThreadID(prompt string) string {
	sum := sha1.Sum([]byte(prompt))
	return "thread-" + hex.EncodeToString(sum[:8])
}

func deterministicTurnID(prompt string) string {
	sum := sha1.Sum([]byte("turn:" + prompt))
	return "turn-" + hex.EncodeToString(sum[:8])
}

func removedFullAutoWarning(opts cli.ExecOptions) string {
	if opts.RemovedFullAuto {
		return "warning: `--full-auto` is deprecated; use `--sandbox workspace-write` instead."
	}
	return ""
}

func mergedOverrides(root, exec []string) []string {
	out := make([]string, 0, len(root)+len(exec))
	out = append(out, root...)
	out = append(out, exec...)
	return out
}

func emitFinalEventsFromAgentResult(sink *execEventSink, result *turn.AgentLoopResult) error {
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
	fmt.Fprintf(stderr, "OpenAI Codex v%s\n", execHumanVersion())
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
			events = append(events, protocol.ItemCompleted(protocol.AgentMessageItem(item.ID, item.Text)))
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
	case "tool_search_call":
		return protocolWebSearchItemFromAgentItem(item)
	case "function_call", "custom_tool_call":
		return protocol.ToolCallItemWithCallID(
			firstNonEmpty(item.ID, "tool-call-"+safeSessionItemID(item.CallID)),
			item.CallID,
			firstNonEmpty(item.Name, item.Namespace, item.Type),
			firstNonEmpty(item.Arguments, item.Input),
		)
	case "", "agent_message":
		return protocol.AgentMessageItem(firstNonEmpty(item.ID, "agent-message"), item.Text)
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

func streamAgentItemLooksLikeMCP(item *model.AgentItem) bool {
	if item == nil {
		return false
	}
	return strings.HasPrefix(strings.TrimSpace(item.Namespace), mcp.LegacyMCPToolNamePrefix) ||
		strings.HasPrefix(strings.TrimSpace(item.Name), mcp.LegacyMCPToolNamePrefix)
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
	return protocol.ImageGenerationItem(firstNonEmpty(item.ID, item.CallID, "image-generation"), status, revisedPrompt, savedPath)
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

func eventsFromToolCallExecution(execution *turn.ToolExecutionResult) []protocol.ThreadEvent {
	if execution == nil || execution.Invocation == nil {
		return nil
	}
	if isCollabExecution(execution) {
		item := collabToolCallProtocolItem(execution, "in_progress")
		return []protocol.ThreadEvent{protocol.ItemStarted(item)}
	}
	if isMCPExecution(execution) {
		item := mcpToolCallProtocolItem(execution, "in_progress")
		return []protocol.ThreadEvent{protocol.ItemStarted(item)}
	}
	if execution.Invocation.Payload.Kind == tool.PayloadToolSearch {
		return nil
	}
	if isWriteStdinExecution(execution) {
		return nil
	}
	if isCommandExecution(execution) {
		item := commandExecutionProtocolItem(execution, "in_progress")
		return []protocol.ThreadEvent{protocol.ItemStarted(item)}
	}
	if isPlanUpdateExecution(execution) || isFileChangeExecution(execution) {
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
	if isPlanUpdateExecution(execution) {
		items := todoItemsFromPlanUpdateOutput(execution.Output)
		return protocol.ItemCompleted(protocol.TodoListItem("todo-list-"+safeSessionItemID(execution.Invocation.CallID), items)), true
	}
	if execution.Invocation.Payload.Kind == tool.PayloadToolSearch {
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
	if isCollabExecution(execution) {
		return protocol.ItemCompleted(collabToolCallProtocolItem(execution, collabToolCallStatusFromOutput(execution.Output))), true
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
	if execution.Invocation.ToolName.Key() != tool.DefaultExecCommandToolName {
		return false
	}
	if _, ok := intFromAny(execution.Output.Data["exit_code"]); ok {
		return true
	}
	_, ok := intFromAny(execution.Output.Data["process_id"])
	return ok
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
		if hookResponse, ok := execution.Output.Data["hook_response"].(string); ok && hookResponse != "" {
			aggregated = hookResponse
		} else {
			aggregated = execution.Output.Body
		}
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
	if strings.TrimSpace(name.Namespace) == "agent" {
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
	case "agent_send_input", "send_input", "sendinput":
		return "send_input", true
	case "agent_wait_agent", "wait_agent", "agent_wait", "wait", "waitagent":
		return "wait", true
	case "agent_close_agent", "close_agent", "closeagent":
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
		); prompt != "" {
			return stringPointerIfNotEmpty(prompt)
		}
	}
	if execution.Invocation == nil {
		return nil
	}
	args := toolInvocationArgumentsMap(execution.Invocation)
	if prompt := firstNonEmpty(execStringFromAny(args["prompt"]), execStringFromAny(args["message"])); prompt != "" {
		return stringPointerIfNotEmpty(prompt)
	}
	return nil
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

func (r *Runner) persistSession(req *Request, threadID string, turnID string, userPrompt string, userInputs []turn.TurnUserInput, result *turn.AgentLoopResult, resumeContext *execResumeContext) (string, error) {
	if req.Exec.Ephemeral {
		return "", nil
	}
	if result == nil || result.Response == nil {
		return "", errors.New("agent response is nil")
	}
	response := result.Response
	now := r.now().UTC()
	store := session.NewStore(filepath.Join(r.CodexHome, "sessions"))
	if resumeContext != nil && resumeContext.Record != nil {
		resumeRecord := resumeContext.Record
		newUserPrompt := firstNonEmpty(resumeContext.UserPrompt, userPrompt)
		items := execAdditionalInputSessionItems(turnID, req.AdditionalInputItems, now, map[string]any{"resumed": true})
		items = append(items, sessionItemsForTurn(turnID, newUserPrompt, userInputs, result, now, map[string]any{"resumed": true}, &execImageGenerationContext{
			CodexHome: r.CodexHome,
			ThreadID:  string(resumeRecord.ID),
		})...)
		updated, err := store.AppendItems(resumeRecord.ID, items)
		if err != nil {
			return "", err
		}
		_ = r.appendExecRollout(resumeRecord.ID, items, updated, now)
		if updated.Metadata.Model == "" || updated.Metadata.ModelProvider == "" {
			_, _ = store.UpdateMetadata(updated.ID, &session.MetadataPatch{
				Model:          stringPointerIfEmpty(updated.Metadata.Model, response.Model),
				ModelProvider:  stringPointerIfEmpty(updated.Metadata.ModelProvider, response.ProviderID),
				LastResponseID: stringPointerIfNotEmpty(response.ResponseID),
				SessionPrefix:  stringPointerIfNotEmpty(session.PrefixForSessionID(updated.SessionID)),
			}, true)
		} else if strings.TrimSpace(response.ResponseID) != "" {
			_, _ = store.UpdateMetadata(updated.ID, &session.MetadataPatch{
				LastResponseID: stringPointerIfNotEmpty(response.ResponseID),
			}, true)
		}
		return store.Path(resumeRecord.ID)
	}
	record := &session.Record{
		ID:        session.ThreadID(threadID),
		SessionID: threadID,
		Preview:   firstLine(firstNonEmpty(userPrompt, turnUserInputsSummary(userInputs))),
		CreatedAt: now,
		UpdatedAt: now,
		RecencyAt: now,
		Metadata: session.Metadata{
			CWD:            requestCWD(req),
			Model:          response.Model,
			ModelProvider:  response.ProviderID,
			Source:         "cli",
			ThreadSource:   string(taskKind(req)),
			HistoryMode:    string(session.ForkAll),
			LastResponseID: response.ResponseID,
			SessionPrefix:  session.PrefixForSessionID(threadID),
		},
		Items: append(execAdditionalInputSessionItems(turnID, req.AdditionalInputItems, now, nil), sessionItemsForTurn(turnID, userPrompt, userInputs, result, now, nil, &execImageGenerationContext{
			CodexHome: r.CodexHome,
			ThreadID:  threadID,
		})...),
	}
	if err := store.Save(record); err != nil {
		return "", err
	}
	_ = r.createExecRollout(record, now)
	path, err := store.Path(record.ID)
	if err != nil {
		return "", err
	}
	return path, nil
}

func (r *Runner) resolveExecResumeRecord(req *Request) (*session.Record, error) {
	store := session.NewStore(filepath.Join(r.CodexHome, "sessions"))
	resume := &req.Exec.Resume
	var threadID session.ThreadID
	if resume.Last {
		record, err := latestExecResumeRecord(store, resume, requestCWD(req))
		if err != nil {
			return nil, err
		}
		threadID = record.ID
	} else {
		target := strings.TrimSpace(resume.SessionID)
		if target == "" {
			return nil, errors.New("exec resume requires SESSION_ID or --last")
		}
		resolved, err := execResumeThreadIDForTarget(store, resume, target, requestCWD(req))
		if err != nil {
			return nil, err
		}
		threadID = resolved
	}
	return store.Read(threadID, true, true)
}

func (r *Runner) createExecRollout(record *session.Record, now time.Time) error {
	if r == nil || record == nil {
		return nil
	}
	recorder, err := rollout.NewRecorder(&rollout.CreateParams{
		CodexHome:     r.CodexHome,
		SessionID:     record.SessionID,
		SessionPrefix: record.Metadata.SessionPrefix,
		ThreadID:      string(record.ID),
		Source:        record.Metadata.Source,
		CWD:           record.Metadata.CWD,
		Model:         record.Metadata.Model,
		ModelProvider: record.Metadata.ModelProvider,
		HistoryMode:   record.Metadata.HistoryMode,
		CLIVersion:    record.Metadata.CLIVersion,
		Now:           now,
	})
	if err != nil {
		return err
	}
	defer recorder.Close()
	return rollout.AppendSessionItems(recorder, record.Items, now)
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

type execImageGenerationContext struct {
	CodexHome string
	ThreadID  string
}

func sessionItemsForTurn(turnID string, userPrompt string, userInputs []turn.TurnUserInput, result *turn.AgentLoopResult, createdAt time.Time, extraMetadata map[string]any, imageContext *execImageGenerationContext) []session.Item {
	suffix := strings.TrimPrefix(turnID, "turn-")
	items := []session.Item{{
		ID:        "user-" + suffix,
		Type:      "message",
		Role:      "user",
		Text:      userPrompt,
		Content:   sessionContentForTurnInputs(userPrompt, userInputs),
		CreatedAt: createdAt,
		Metadata:  sessionMetadata(turnID, extraMetadata),
	}}
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
					continue
				}
				item := sessionItemFromAgentItem(turnID, fallbackAssistantID, response.ResponseID, &response.Items[i], result.TimingProfile, createdAt, responseExtraMetadata, imageContext)
				if item.ID != "" {
					items = append(items, item)
					if instructions, ok := execImageGenerationInstructionsSessionItem(turnID, &item, createdAt, responseExtraMetadata); ok {
						items = append(items, instructions)
					}
				}
			}
		}
		if len(response.Items) == 0 && strings.TrimSpace(response.Message) != "" {
			items = append(items, session.Item{
				ID:         fallbackAssistantID,
				Type:       "agent_message",
				Role:       "assistant",
				Text:       response.Message,
				Content:    []session.ContentPart{{Type: "output_text", Text: response.Message}},
				CreatedAt:  createdAt,
				ResponseID: response.ResponseID,
				Metadata:   addTimingProfileMetadata(sessionMetadata(turnID, responseExtraMetadata), result.TimingProfile),
			})
		}
		toolExecutions := execToolExecutionsForResponse(result.ToolExecutions, executionIndex, toolItemCount)
		for i := range toolExecutions {
			if item, ok := sessionItemForToolCall(turnID, suffix, &toolExecutions[i], createdAt, responseExtraMetadata); ok {
				items = append(items, item)
			}
		}
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
	return items
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
		ID:        "tool-output-" + suffix + "-" + safeSessionItemID(callID),
		Type:      "tool_output",
		CallID:    callID,
		Text:      execution.Output.Body,
		Data:      toolOutputData(execution.Output),
		CreatedAt: outputCreatedAt,
		Metadata:  outputMetadata,
	}, true
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
	text := firstNonEmpty(item.Text, item.Arguments, item.Input)
	metadata := addTimingProfileMetadata(sessionMetadata(turnID, extraMetadata), timingProfile)
	if item.Name != "" {
		metadata["toolName"] = item.Name
	}
	for key, value := range item.Data {
		metadata[key] = value
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
		Data:       cloneMap(item.Data),
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
