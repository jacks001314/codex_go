package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"

	bubbletea "github.com/charmbracelet/bubbletea"

	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	contextfrag "codex_go/context"
	"codex_go/doctor"
	"codex_go/eventmap"
	codexexec "codex_go/exec"
	"codex_go/features"
	"codex_go/mcp"
	modelpkg "codex_go/model"
	"codex_go/plugin"
	promptctx "codex_go/prompt"
	"codex_go/protocol"
	"codex_go/session"
	"codex_go/tool"
	codextui "codex_go/tui"
	tuiapp "codex_go/tui/app"
	bottompane "codex_go/tui/bottom_pane"
	chatwidget "codex_go/tui/chatwidget"
	historycell "codex_go/tui/history_cell"
	idecontext "codex_go/tui/ide_context"
	codextea "codex_go/tui/tea"
	"codex_go/turn"
)

const remoteAddressUsage = "expected `ws://host:port`, `wss://host:port`, `unix://`, or `unix://PATH`"

type interactiveSession struct {
	Root     cli.RootOptions
	Runner   *codexexec.Runner
	Reader   *bufio.Scanner
	Stdout   io.Writer
	Stderr   io.Writer
	ThreadID string
	UI       *codextui.State
}

type interactiveTurnRunner interface {
	RunContext(ctx context.Context, req *codexexec.Request, stdin io.Reader, stdout, stderr io.Writer) (*codexexec.Result, error)
}

type interactiveApprovalBroker struct {
	mu           sync.Mutex
	next         int
	pending      map[string]chan codextea.ModalResponse
	allowSession bool
}

type interactiveElicitationBroker struct {
	mu      sync.Mutex
	next    int
	pending map[string]chan codextea.ModalResponse
}

type interactiveUserInputBroker struct {
	mu      sync.Mutex
	next    int
	pending map[string]chan codextea.ModalResponse
}

type interactiveInterruptController struct {
	mu     sync.Mutex
	cancel context.CancelFunc
	seq    int64
}

func newInteractiveApprovalBroker() *interactiveApprovalBroker {
	return &interactiveApprovalBroker{pending: map[string]chan codextea.ModalResponse{}}
}

func newInteractiveElicitationBroker() *interactiveElicitationBroker {
	return &interactiveElicitationBroker{pending: map[string]chan codextea.ModalResponse{}}
}

func newInteractiveUserInputBroker() *interactiveUserInputBroker {
	return &interactiveUserInputBroker{pending: map[string]chan codextea.ModalResponse{}}
}

func newInteractiveInterruptController() *interactiveInterruptController {
	return &interactiveInterruptController{}
}

func (c *interactiveInterruptController) begin(parent context.Context) (context.Context, func()) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	if c == nil {
		return ctx, cancel
	}
	c.mu.Lock()
	c.seq++
	token := c.seq
	c.cancel = cancel
	c.mu.Unlock()
	done := func() {
		c.mu.Lock()
		if c.seq == token {
			c.cancel = nil
		}
		c.mu.Unlock()
		cancel()
	}
	return ctx, done
}

func (c *interactiveInterruptController) interruptCommand() bubbletea.Cmd {
	return func() bubbletea.Msg {
		if c == nil || !c.interrupt() {
			return codextea.TurnInterruptedMsg{Err: errors.New("no active turn to interrupt")}
		}
		return codextea.TurnInterruptedMsg{}
	}
}

func (c *interactiveInterruptController) interrupt() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	cancel := c.cancel
	c.mu.Unlock()
	if cancel == nil {
		return false
	}
	cancel()
	return true
}

func (b *interactiveApprovalBroker) shellApprovalFunc(send func(bubbletea.Msg)) tool.ShellApprovalFunc {
	return func(ctx context.Context, request *tool.ShellApprovalRequest) (tool.ShellApprovalDecision, error) {
		if b == nil {
			return tool.ShellApprovalDecision{}, nil
		}
		if b.sessionApproved() {
			return tool.ShellApprovalDecision{Approved: true, AllowSession: true}, nil
		}
		if send == nil {
			return tool.ShellApprovalDecision{}, errors.New("approval UI is unavailable")
		}
		id, responses := b.registerRequest()
		send(codextea.ApprovalRequestMsg{
			ID:      id,
			Title:   "Run command?",
			Body:    interactiveShellApprovalBody(request),
			Command: interactiveShellApprovalCommand(request),
		})
		select {
		case response := <-responses:
			return b.approvalDecision(response), nil
		case <-ctx.Done():
			b.forgetRequest(id)
			return tool.ShellApprovalDecision{}, ctx.Err()
		}
	}
}

func (b *interactiveApprovalBroker) respond(response codextea.ModalResponse) {
	if b == nil {
		return
	}
	b.mu.Lock()
	ch := b.pending[response.ID]
	delete(b.pending, response.ID)
	b.mu.Unlock()
	if ch != nil {
		ch <- response
		close(ch)
	}
}

func (b *interactiveApprovalBroker) registerRequest() (string, <-chan codextea.ModalResponse) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	id := fmt.Sprintf("approval-%d", b.next)
	ch := make(chan codextea.ModalResponse, 1)
	b.pending[id] = ch
	return id, ch
}

func (b *interactiveApprovalBroker) forgetRequest(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.pending, id)
}

func (b *interactiveApprovalBroker) sessionApproved() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.allowSession
}

func (b *interactiveApprovalBroker) approvalDecision(response codextea.ModalResponse) tool.ShellApprovalDecision {
	if response.Cancelled {
		return tool.ShellApprovalDecision{}
	}
	switch response.OptionID {
	case "allow_once":
		return tool.ShellApprovalDecision{Approved: true}
	case "allow_session":
		b.mu.Lock()
		b.allowSession = true
		b.mu.Unlock()
		return tool.ShellApprovalDecision{Approved: true, AllowSession: true}
	default:
		return tool.ShellApprovalDecision{}
	}
}

func (b *interactiveElicitationBroker) mcpElicitationFunc(send func(bubbletea.Msg)) mcp.MCPElicitationHandlerFunc {
	return func(ctx context.Context, request *mcp.MCPElicitationRequest) (*mcp.MCPElicitationResponse, error) {
		if b == nil || request == nil {
			return &mcp.MCPElicitationResponse{Action: mcp.MCPElicitationActionCancel}, nil
		}
		if send == nil {
			return &mcp.MCPElicitationResponse{Action: mcp.MCPElicitationActionCancel}, nil
		}
		id, responses := b.registerRequest()
		send(codextea.ElicitationRequestMsg{
			ID:              id,
			ServerName:      request.ServerName,
			RequestID:       interactiveMCPRequestID(request),
			ThreadID:        request.ThreadID,
			TurnID:          request.TurnID,
			Message:         request.Message,
			URL:             request.URL,
			RequestedSchema: request.RequestedSchema,
			Meta:            interactiveMCPMetaMap(request.Meta),
		})
		if ctx == nil {
			ctx = context.Background()
		}
		select {
		case response := <-responses:
			return b.elicitationResponse(response), nil
		case <-ctx.Done():
			b.forgetRequest(id)
			return nil, ctx.Err()
		}
	}
}

func (b *interactiveElicitationBroker) respond(response codextea.ModalResponse) {
	if b == nil || response.Kind != codextea.ModalKindElicitation {
		return
	}
	b.mu.Lock()
	ch := b.pending[response.ID]
	delete(b.pending, response.ID)
	b.mu.Unlock()
	if ch != nil {
		ch <- response
		close(ch)
	}
}

func (b *interactiveElicitationBroker) registerRequest() (string, <-chan codextea.ModalResponse) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	id := fmt.Sprintf("elicitation-%d", b.next)
	ch := make(chan codextea.ModalResponse, 1)
	b.pending[id] = ch
	return id, ch
}

func (b *interactiveElicitationBroker) forgetRequest(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.pending, id)
}

func (b *interactiveElicitationBroker) elicitationResponse(response codextea.ModalResponse) *mcp.MCPElicitationResponse {
	if response.Cancelled || response.Elicitation == nil {
		return &mcp.MCPElicitationResponse{Action: mcp.MCPElicitationActionCancel}
	}
	result := &mcp.MCPElicitationResponse{
		Action:  mcpElicitationAction(response.Elicitation.Action),
		Content: cloneAnyMapApp(response.Elicitation.Content),
	}
	if strings.TrimSpace(response.Elicitation.Persist) != "" {
		result.Meta = map[string]any{"persist": strings.TrimSpace(response.Elicitation.Persist)}
	}
	return result
}

func mcpElicitationAction(action string) mcp.MCPElicitationAction {
	switch strings.TrimSpace(action) {
	case string(mcp.MCPElicitationActionAccept):
		return mcp.MCPElicitationActionAccept
	case string(mcp.MCPElicitationActionDecline):
		return mcp.MCPElicitationActionDecline
	default:
		return mcp.MCPElicitationActionCancel
	}
}

func (b *interactiveUserInputBroker) userInputResponder(send func(bubbletea.Msg)) tool.UserInputResponder {
	return func(ctx context.Context, args *tool.RequestUserInputArgs) (*tool.UserInputResponse, error) {
		if b == nil || args == nil {
			return &tool.UserInputResponse{Answers: map[string]string{}}, nil
		}
		if send == nil {
			return nil, errors.New("request_user_input UI is unavailable")
		}
		id, responses := b.registerRequest()
		send(codextea.RequestUserInputMsg{
			ID:               id,
			Questions:        interactiveUserInputQuestions(args.Questions),
			AutoResolutionMS: cloneIntPtrApp(args.AutoResolutionMS),
		})
		if ctx == nil {
			ctx = context.Background()
		}
		select {
		case response := <-responses:
			return b.userInputResponse(response), nil
		case <-ctx.Done():
			b.forgetRequest(id)
			return nil, ctx.Err()
		}
	}
}

func (b *interactiveUserInputBroker) respond(response codextea.ModalResponse) {
	if b == nil || response.Kind != codextea.ModalKindUserInput {
		return
	}
	b.mu.Lock()
	ch := b.pending[response.ID]
	delete(b.pending, response.ID)
	b.mu.Unlock()
	if ch != nil {
		ch <- response
		close(ch)
	}
}

func (b *interactiveUserInputBroker) registerRequest() (string, <-chan codextea.ModalResponse) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.next++
	id := fmt.Sprintf("user-input-%d", b.next)
	ch := make(chan codextea.ModalResponse, 1)
	b.pending[id] = ch
	return id, ch
}

func (b *interactiveUserInputBroker) forgetRequest(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.pending, id)
}

func (b *interactiveUserInputBroker) userInputResponse(response codextea.ModalResponse) *tool.UserInputResponse {
	if response.Cancelled || response.UserInput == nil {
		return &tool.UserInputResponse{Answers: map[string]string{}}
	}
	return &tool.UserInputResponse{
		Answers:           cloneStringMapApp(response.UserInput.Answers),
		StructuredAnswers: cloneStringSlicesMapApp(response.UserInput.AnswerLists),
		TimedOut:          response.UserInput.TimedOut,
	}
}

func interactiveUserInputQuestions(questions []tool.UserInputQuestion) []codextui.RequestUserInputQuestion {
	out := make([]codextui.RequestUserInputQuestion, 0, len(questions))
	for _, question := range questions {
		options := make([]codextui.RequestUserInputChoice, 0, len(question.Options))
		for _, option := range question.Options {
			options = append(options, codextui.RequestUserInputChoice{
				Label:       option.Label,
				Description: option.Description,
			})
		}
		out = append(out, codextui.RequestUserInputQuestion{
			Header:   question.Header,
			ID:       question.ID,
			Question: question.Question,
			IsOther:  question.IsOther,
			IsSecret: question.IsSecret,
			Options:  options,
		})
	}
	return out
}

func cloneStringMapApp(values map[string]string) map[string]string {
	if values == nil {
		return map[string]string{}
	}
	out := make(map[string]string, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func cloneStringSlicesMapApp(values map[string][]string) map[string][]string {
	if values == nil {
		return map[string][]string{}
	}
	out := make(map[string][]string, len(values))
	for key, value := range values {
		out[key] = append([]string(nil), value...)
	}
	return out
}

func cloneIntPtrApp(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func interactiveMCPRequestID(request *mcp.MCPElicitationRequest) string {
	if request == nil {
		return ""
	}
	if strings.TrimSpace(request.ElicitationID) != "" {
		return strings.TrimSpace(request.ElicitationID)
	}
	raw := strings.TrimSpace(string(request.ID))
	raw = strings.Trim(raw, `"`)
	return raw
}

func interactiveMCPMetaMap(value any) map[string]any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneAnyMapApp(typed)
	default:
		return nil
	}
}

func cloneAnyMapApp(values map[string]any) map[string]any {
	if values == nil {
		return nil
	}
	out := make(map[string]any, len(values))
	for key, value := range values {
		out[key] = value
	}
	return out
}

func interactiveShellApprovalBody(request *tool.ShellApprovalRequest) string {
	if request == nil || request.Request == nil {
		return "Command requested elevated permissions."
	}
	req := request.Request
	lines := []string{}
	if strings.TrimSpace(req.ApprovalReason) != "" {
		lines = append(lines, "Reason: "+strings.TrimSpace(req.ApprovalReason))
	}
	if strings.TrimSpace(req.Justification) != "" {
		lines = append(lines, "Justification: "+strings.TrimSpace(req.Justification))
	}
	if strings.TrimSpace(req.CWD) != "" {
		lines = append(lines, "Working directory: "+strings.TrimSpace(req.CWD))
	}
	if req.SandboxPermissions != "" {
		lines = append(lines, "Sandbox permissions: "+string(req.SandboxPermissions))
	}
	if len(req.PrefixRule) > 0 {
		lines = append(lines, "Persistable prefix: "+strings.Join(req.PrefixRule, " "))
	}
	if len(lines) == 0 {
		return "Command requested elevated permissions."
	}
	return strings.Join(lines, "\n")
}

func interactiveShellApprovalCommand(request *tool.ShellApprovalRequest) string {
	if request == nil || request.Request == nil {
		return ""
	}
	req := request.Request
	if strings.TrimSpace(req.HookCommand) != "" {
		return strings.TrimSpace(req.HookCommand)
	}
	return strings.Join(req.Command, " ")
}

func runInteractive(ctx context.Context, root *cli.RootOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	if root == nil {
		root = &cli.RootOptions{}
	}
	cleanRoot := *root
	cleanRoot.Prompt = normalizeInteractivePrompt(cleanRoot.Prompt)
	root = &cleanRoot
	if err := guardInteractiveDumbTerminal(stdin, stderr, os.Getenv("TERM")); err != nil {
		return interactiveFatalExit(stderr, err.Error())
	}
	remoteEndpoint, err := resolveInteractiveRemoteEndpoint(root)
	if err != nil {
		return interactiveFatalExit(stderr, err.Error())
	}
	if remoteEndpoint != nil {
		if strings.TrimSpace(root.Prompt) != "" {
			return runInteractiveRemotePrompt(ctx, root, remoteEndpoint, stdout, stderr)
		}
		if shouldRunInteractiveTUI(stdin, stdout) {
			return runInteractiveRemoteTUI(ctx, root, remoteEndpoint, stdin, stdout)
		}
		return errors.New("interactive remote app-server TUI requires a real terminal")
	}
	if strings.TrimSpace(root.Prompt) != "" {
		return runInteractivePrompt(ctx, root, stdin, stdout, stderr)
	}
	if shouldRunInteractiveTUI(stdin, stdout) {
		return runInteractiveTUI(ctx, root, stdin, stdout)
	}
	session := &interactiveSession{
		Root:   *root,
		Runner: newCodexExecRunner(auth.DefaultCodexHome()),
		Reader: bufio.NewScanner(stdin),
		Stdout: stdout,
		Stderr: stderr,
		UI:     interactiveUIState(root),
	}
	return session.Run(ctx)
}

func shouldRunInteractiveTUI(stdin io.Reader, stdout io.Writer) bool {
	return isRealTerminal(stdin) && isRealTerminal(stdout)
}

func isRealTerminal(value any) bool {
	file, ok := value.(*os.File)
	if !ok {
		return false
	}
	info, err := file.Stat()
	return err == nil && info.Mode()&os.ModeCharDevice != 0
}

func runInteractiveTUI(ctx context.Context, root *cli.RootOptions, stdin io.Reader, stdout io.Writer) error {
	state := interactiveUIState(root)
	settings := interactiveTUISettings(root)
	accountDisplay, hasChatGPTAccount := interactiveStatusAccount(root)
	state.AccountDisplay = accountDisplay
	state.HasChatGPTAccount = hasChatGPTAccount
	runner := newCodexExecRunner(auth.DefaultCodexHome())
	sideRunner := newCodexExecRunner(auth.DefaultCodexHome())
	sideCoordinator := newInteractiveLocalSideCoordinator(newSessionStore())
	defer sideCoordinator.CloseAll()
	pluginService := interactivePluginService()
	mcpService, mcpStatuses, mcpExpectedServers := interactiveMCPRuntime(root)
	if runner != nil && mcpService != nil {
		runner.MCPService = mcpService
	}
	if sideRunner != nil && mcpService != nil {
		sideRunner.MCPService = mcpService
	}
	var initialMessages <-chan bubbletea.Msg
	var cancelMCPStartup context.CancelFunc
	if runner != nil && mcpService != nil && len(mcpExpectedServers) > 0 {
		startupCtx := ctx
		if startupCtx == nil {
			startupCtx = context.Background()
		}
		startupCtx, cancelMCPStartup = context.WithCancel(startupCtx)
		initialMessages = interactiveMCPStartupMessages(startupCtx, mcpService, runner, mcpExpectedServers)
	}
	approvalBroker := newInteractiveApprovalBroker()
	elicitationBroker := newInteractiveElicitationBroker()
	userInputBroker := newInteractiveUserInputBroker()
	interrupts := newInteractiveInterruptController()
	readGoal, setGoal, clearGoal := interactiveLocalGoalCallbacks(nil)
	readAgents, switchAgent := interactiveLocalAgentCallbacks(nil)
	options := codextea.Options{
		NoAltScreen:                root != nil && root.Shared.NoAltScreen,
		SessionPickerItems:         interactiveSessionPickerItems(root),
		SessionPickerCWD:           interactiveSessionPickerCWD(root),
		SessionPickerView:          settings.SessionPickerView,
		ShowSessionHeader:          true,
		SessionHeaderVersion:       doctor.Version(),
		OnSessionAction:            interactiveSessionActionHandler(root),
		OnResumeSession:            interactiveResumeSessionHandler(root),
		OnRenameThread:             interactiveRenameThreadHandler(),
		OnLogout:                   interactiveLogoutHandler(ctx, root),
		OnOpenDesktopThread:        interactiveOpenDesktopThread,
		KeymapConfig:               interactiveKeymapConfig(root),
		OnKeymapEdit:               interactiveKeymapEditHandler(root),
		OnWriteSettings:            interactiveSettingsWriteHandler(root),
		OnWriteMemorySettings:      interactiveMemorySettingsWriteHandler(root),
		OnResetMemories:            interactiveMemoryResetHandler(),
		OnSubmitFeedback:           interactiveFeedbackSubmitHandler(),
		OnReadIDEContext:           interactiveIDEContextReader,
		OnApproveAutoReviewDenial:  interactiveApproveAutoReviewDenialHandler(),
		OnStartWindowsSandboxSetup: interactiveWindowsSandboxSetupHandler(root),
		OnSandboxReadDir:           interactiveSandboxReadDirHandler(root),
		FeatureSettings:            settings.FeatureSettings,
		UseMemories:                settings.UseMemories,
		GenerateMemories:           settings.GenerateMemories,
		FeedbackEnabled:            settings.FeedbackEnabled,
		ServiceTierCommands:        interactiveServiceTierCommands(state.Model),
		Personality:                settings.Personality,
		Notifications:              settings.Notifications,
		NotificationMethod:         settings.NotificationMethod,
		NotificationCondition:      settings.NotificationCondition,
		PermissionRequirements:     settings.PermissionRequirements,
		MCPServers:                 mcpStatuses,
		OnReadMCPInventory: func(detail bool) ([]historycell.McpServerStatus, error) {
			if mcpService == nil {
				return nil, nil
			}
			mode := mcp.MCPServerStatusDetailToolsAndAuthOnly
			if detail {
				mode = mcp.MCPServerStatusDetailFull
			}
			response, err := mcpService.ListStatusChecked(&mcp.MCPListServerStatusParams{
				Detail: &mcp.MCPServerStatusDetail{Mode: mode},
			})
			if err != nil {
				return nil, err
			}
			if response == nil {
				return nil, nil
			}
			return interactiveHistoryMCPStatuses(response.Data), nil
		},
		OnReadRateLimits:          interactiveLocalRateLimitsReader(),
		MCPStartupExpectedServers: mcpExpectedServers,
		InitialMessages:           initialMessages,
		HideRateLimitModelNudge:   settings.HideRateLimitModelNudge,
		TUITheme:                  settings.TUITheme,
		TUIPet:                    settings.TUIPet,
		OnPostNotification:        interactiveNotificationPoster(stdout),
		OnReadDebugConfig:         interactiveDebugConfigReader(root),
		OnReadGoal:                readGoal,
		OnSetGoal:                 setGoal,
		OnClearGoal:               clearGoal,
		OnReadAgents:              readAgents,
		OnSwitchAgent:             switchAgent,
		OnDetectExternalAgent:     interactiveExternalAgentDetectHandler(root),
		OnImportExternalAgent:     interactiveExternalAgentImportHandler(root),
		OnReadHooks:               interactiveHooksReader(root),
		OnWriteHookConfig:         interactiveHookConfigWriter(root),
		OnReadPlugins:             interactivePluginReader(root, pluginService),
		OnReadPlugin:              interactivePluginReaderByID(pluginService),
		OnInstallPlugin:           interactivePluginInstaller(pluginService),
		OnUninstallPlugin:         interactivePluginUninstaller(pluginService),
		OnWritePluginEnabled:      interactivePluginEnabledWriter(root),
		OnAddMarketplace:          interactiveMarketplaceAdder(pluginService),
		OnRemoveMarketplace:       interactiveMarketplaceRemover(pluginService),
		OnUpgradeMarketplace:      interactiveMarketplaceUpgrader(pluginService),
		OnOpenPluginURL:           auth.OpenBrowser,
		PluginUserMarketplaces:    settings.PluginUserMarketplaces,
		PluginGitMarketplaces:     settings.PluginGitMarketplaces,
		OnReadSkills:              interactiveSkillsReader(root),
		OnWriteSkillEnabled:       interactiveSkillEnabledWriter(root),
		OnFuzzyFileSearch:         interactiveFuzzyFileSearchReader(root),
		OnStartReviewCommand:      interactiveLocalReviewStartCommand(ctx, state, interrupts, nil),
		OnStartCompactCommand:     interactiveLocalCompactStartCommand(ctx, state, nil),
		OnStartSide:               sideCoordinator.Start,
		OnCloseSide:               sideCoordinator.Close,
		OnSubmitRequest: func(request codextea.SubmitRequest) bubbletea.Cmd {
			turnRunner := interactiveTurnRunner(runner)
			if instructions, side := sideCoordinator.Instructions(state.ThreadID); side {
				request.AdditionalInstructions = strings.Join(nonEmptyStringsApp([]string{instructions, request.AdditionalInstructions}), "\n\n")
				turnRunner = sideRunner
			}
			return interactiveTurnCommandWithRequest(ctx, root, turnRunner, state, request, approvalBroker, elicitationBroker, userInputBroker, interrupts)
		},
		OnInterrupt: func() bubbletea.Cmd {
			return interrupts.interruptCommand()
		},
		OnInterruptMCPStartup: func() bubbletea.Cmd {
			if cancelMCPStartup != nil {
				cancelMCPStartup()
			}
			return func() bubbletea.Msg { return codextea.MCPStartupFinishAfterLagMsg{} }
		},
		OnModalResponse: func(response codextea.ModalResponse) bubbletea.Cmd {
			approvalBroker.respond(response)
			elicitationBroker.respond(response)
			userInputBroker.respond(response)
			return nil
		},
		HasChatGPTAccount: hasChatGPTAccount,
	}
	_, err := codextea.Run(ctx, state, options, stdin, stdout)
	return err
}

func interactiveMCPRuntime(root *cli.RootOptions) (*mcp.MCPService, []historycell.McpServerStatus, []string) {
	codexHome := auth.DefaultCodexHome()
	loaded, err := config.LoadEffectiveWithOptions(codexHome, interactiveKeymapLoadOptions(root))
	if err != nil || loaded == nil {
		return nil, nil, nil
	}
	storeOptions := auth.StoreOptionsFromConfig(loaded.CLIAuthCredentialsStoreMode(), loaded.SecretAuthStorageEnabled())
	var runtimeAuth *mcp.RuntimeAuth
	if resolved, resolveErr := auth.NewStoreWithOptions(codexHome, storeOptions).Resolve(); resolveErr == nil && resolved != nil {
		runtimeAuth = mcp.RuntimeAuthFromSnapshot(&resolved.Auth)
	}
	runtimeConfig := mcp.RuntimeConfigFromValuesWithAuthAndRequirements(loaded.Values, codexHome, runtimeAuth, loaded.Requirements)
	if runtimeConfig == nil || len(runtimeConfig.Servers) == 0 {
		return nil, nil, nil
	}
	service := mcp.NewMCPService(runtimeConfig)
	configuredStatuses := service.ConfiguredStatuses()
	statuses := interactiveHistoryMCPStatuses(configuredStatuses)
	expectedServers := make([]string, 0, len(configuredStatuses))
	for i := range configuredStatuses {
		if name := mcp.RuntimeServerNameFromStatus(&configuredStatuses[i]); name != "" {
			expectedServers = append(expectedServers, name)
		}
	}
	return service, statuses, expectedServers
}

func interactiveStatusAccount(root *cli.RootOptions) (string, bool) {
	codexHome := auth.DefaultCodexHome()
	loaded, err := config.LoadEffectiveWithOptions(codexHome, interactiveKeymapLoadOptions(root))
	if err != nil || loaded == nil {
		return "", false
	}
	storeOptions := auth.StoreOptionsFromConfig(loaded.CLIAuthCredentialsStoreMode(), loaded.SecretAuthStorageEnabled())
	resolved, err := auth.NewStoreWithOptions(codexHome, storeOptions).Resolve()
	if err != nil || resolved == nil {
		return "", false
	}
	return interactiveAccountDisplay(auth.AccountFromAuth(&resolved.Auth))
}

func interactiveAccountDisplay(account *auth.Account) (string, bool) {
	if account == nil {
		return "", false
	}
	switch account.Type {
	case auth.AccountAPIKey:
		return "API key configured (run codex login to use ChatGPT)", false
	case auth.AccountChatGPT:
		email := ""
		if account.Email != nil {
			email = strings.TrimSpace(*account.Email)
		}
		plan := strings.TrimSpace(string(account.PlanType))
		if plan == "" || account.PlanType == auth.PlanUnknown {
			plan = ""
		}
		switch {
		case email != "" && plan != "":
			return email + " (" + plan + ")", true
		case email != "":
			return email, true
		case plan != "":
			return plan, true
		default:
			return "ChatGPT", true
		}
	default:
		return "", false
	}
}

func interactiveMCPStartupMessages(ctx context.Context, service *mcp.MCPService, runner *codexexec.Runner, expectedServers []string) <-chan bubbletea.Msg {
	capacity := len(expectedServers)*3 + 2
	if capacity < 2 {
		capacity = 2
	}
	messages := make(chan bubbletea.Msg, capacity)
	go func() {
		defer close(messages)
		send := func(message bubbletea.Msg) bool {
			select {
			case messages <- message:
				return true
			case <-ctx.Done():
				return false
			}
		}
		for _, name := range expectedServers {
			if !send(codextea.MCPStartupUpdateMsg{Name: name, Status: chatwidget.McpStartupStatus{Kind: chatwidget.McpStartupStarting}}) {
				return
			}
		}
		expectedSet := make(map[string]bool, len(expectedServers))
		settledExpected := make(map[string]bool, len(expectedServers))
		for _, name := range expectedServers {
			expectedSet[name] = true
		}
		var finalUpdate *codextea.MCPStartupUpdateMsg
		response, err := service.ListStatusCheckedWithObserver(&mcp.MCPListServerStatusParams{
			Detail: &mcp.MCPServerStatusDetail{Mode: mcp.MCPServerStatusDetailFull},
		}, func(name string, status mcp.MCPServerStartupState, startupErr error) {
			kind := chatwidget.McpStartupStatusKind(status)
			if kind == "" {
				kind = chatwidget.McpStartupFailed
			}
			message := ""
			if startupErr != nil {
				message = startupErr.Error()
			}
			update := codextea.MCPStartupUpdateMsg{Name: name, Status: chatwidget.McpStartupStatus{Kind: kind, Error: message}}
			if kind != chatwidget.McpStartupStarting && expectedSet[name] {
				settledExpected[name] = true
			}
			if expectedSet[name] && len(settledExpected) == len(expectedSet) && kind != chatwidget.McpStartupStarting {
				cloned := update
				finalUpdate = &cloned
				return
			}
			_ = send(update)
		})
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			for _, name := range expectedServers {
				if !send(codextea.MCPStartupUpdateMsg{Name: name, Status: chatwidget.McpStartupStatus{Kind: chatwidget.McpStartupFailed, Error: err.Error()}}) {
					return
				}
			}
			return
		}
		if response == nil {
			return
		}
		runner.MCPTools = mcp.RuntimeToolsFromStatuses(response.Data)
		if finalUpdate != nil && !send(*finalUpdate) {
			return
		}
		_ = send(codextea.MCPStartupInventoryMsg{Servers: interactiveHistoryMCPStatuses(response.Data)})
	}()
	return messages
}

func interactiveHistoryMCPStatuses(statuses []mcp.MCPServerStatus) []historycell.McpServerStatus {
	out := make([]historycell.McpServerStatus, 0, len(statuses))
	for i := range statuses {
		status := statuses[i]
		name := mcp.RuntimeServerNameFromStatus(&status)
		if name == "" {
			continue
		}
		out = append(out, historycell.McpServerStatus{
			Name:              name,
			Auth:              interactiveMCPAuthStatusDisplay(status.AuthStatus),
			Tools:             interactiveMCPToolNames(status.Tools),
			Resources:         interactiveMCPResources(status.Resources),
			ResourceTemplates: interactiveMCPResourceTemplates(status.ResourceTemplates),
		})
	}
	return out
}

func interactiveMCPAuthStatusDisplay(status mcp.MCPAuthStatus) string {
	switch status {
	case mcp.MCPAuthBearerToken:
		return "Bearer token"
	case mcp.MCPAuthOAuth:
		return "OAuth"
	case mcp.MCPAuthNotLoggedIn:
		return "Not logged in"
	case mcp.MCPAuthUnsupported, "":
		return "Unsupported"
	default:
		return string(status)
	}
}

func interactiveMCPToolNames(tools []mcp.MCPToolInfo) []string {
	out := make([]string, 0, len(tools))
	for i := range tools {
		name := strings.TrimSpace(tools[i].Name)
		if name != "" && !mcp.ToolSyntheticLink(tools[i].Meta) {
			out = append(out, name)
		}
	}
	return out
}

func interactiveMCPResources(resources []mcp.MCPResource) []historycell.McpResource {
	out := make([]historycell.McpResource, 0, len(resources))
	for i := range resources {
		out = append(out, historycell.McpResource{
			Name:  strings.TrimSpace(resources[i].Name),
			Title: strings.TrimSpace(resources[i].Title),
			URI:   strings.TrimSpace(resources[i].URI),
		})
	}
	return out
}

func interactiveMCPResourceTemplates(templates []mcp.MCPResourceTemplate) []historycell.McpResourceTemplate {
	out := make([]historycell.McpResourceTemplate, 0, len(templates))
	for i := range templates {
		out = append(out, historycell.McpResourceTemplate{
			Name:        strings.TrimSpace(templates[i].Name),
			Title:       strings.TrimSpace(templates[i].Title),
			URITemplate: strings.TrimSpace(templates[i].URITemplate),
		})
	}
	return out
}

func interactiveNotificationPoster(stdout io.Writer) codextea.NotificationPostFunc {
	return func(message string, method codextui.NotificationMethod) bubbletea.Cmd {
		if method == "" {
			method = codextui.NotificationMethodAuto
		}
		sequence := codextui.NotificationSequenceForEnv(method, message, os.Getenv)
		if sequence == "" || stdout == nil {
			return nil
		}
		return func() bubbletea.Msg {
			if _, err := io.WriteString(stdout, sequence); err != nil {
				return codextea.StatusMsg{Status: "warning: failed to post desktop notification: " + err.Error()}
			}
			return nil
		}
	}
}

func interactiveKeymapConfig(root *cli.RootOptions) *codextui.KeymapConfig {
	loaded, err := config.LoadEffectiveWithOptions(auth.DefaultCodexHome(), interactiveKeymapLoadOptions(root))
	if err != nil {
		return codextui.NewKeymapConfig()
	}
	keymap, err := codextui.KeymapConfigFromConfigValues(loaded.Values)
	if err != nil {
		return codextui.NewKeymapConfig()
	}
	return keymap
}

func interactiveKeymapEditHandler(root *cli.RootOptions) codextea.KeymapEditFunc {
	return func(edit codextui.KeymapEdit) (*codextui.KeymapConfig, string, error) {
		if err := edit.Validate(); err != nil {
			return nil, "", err
		}
		codexHome := auth.DefaultCodexHome()
		service := config.NewConfigService(codexHome)
		if root != nil && strings.TrimSpace(root.Shared.Profile) != "" {
			service.SetProfile(root.Shared.Profile)
		}
		response, err := service.WriteValue(&config.ConfigValueWriteParams{
			KeyPath: edit.KeyPath(),
			Value:   edit.ConfigValue(),
		})
		if err != nil {
			return nil, "", err
		}
		loaded, err := config.LoadEffectiveWithOptions(codexHome, interactiveKeymapLoadOptions(root))
		if err != nil {
			return nil, "", err
		}
		keymap, err := codextui.KeymapConfigFromConfigValues(loaded.Values)
		if err != nil {
			return nil, "", err
		}
		message := interactiveKeymapEditMessage(edit)
		if response != nil && strings.TrimSpace(response.FilePath) != "" {
			message += " Saved to " + response.FilePath + "."
		}
		return keymap, message, nil
	}
}

func interactiveTUISettings(root *cli.RootOptions) codextea.SettingsWriteResult {
	result, err := interactiveLoadSettings(root)
	if err != nil {
		return codextea.SettingsWriteResult{}
	}
	return result
}

func interactiveSettingsWriteHandler(root *cli.RootOptions) codextea.SettingsWriteFunc {
	return func(edits []codextea.SettingsEdit) (codextea.SettingsWriteResult, error) {
		if len(edits) == 0 {
			return interactiveLoadSettings(root)
		}
		configEdits := make([]config.ConfigEdit, 0, len(edits))
		for _, edit := range edits {
			keyPath := strings.TrimSpace(edit.KeyPath)
			if keyPath == "" {
				continue
			}
			configEdits = append(configEdits, config.ConfigEdit{
				KeyPath:       keyPath,
				Value:         edit.Value,
				MergeStrategy: config.MergeReplace,
			})
		}
		if len(configEdits) == 0 {
			return interactiveLoadSettings(root)
		}
		service := interactiveConfigService(root)
		response, err := service.BatchWrite(&config.ConfigBatchWriteParams{Edits: configEdits})
		if err != nil {
			return codextea.SettingsWriteResult{}, err
		}
		result, err := interactiveLoadSettings(root)
		if err != nil {
			return codextea.SettingsWriteResult{}, err
		}
		if response != nil {
			result.FilePath = response.FilePath
		}
		return result, nil
	}
}

func interactiveMemorySettingsWriteHandler(root *cli.RootOptions) codextea.MemorySettingsWriteFunc {
	return func(threadID string, useMemories bool, generateMemories bool, generateChanged bool) (codextea.SettingsWriteResult, error) {
		result, err := interactiveSettingsWriteHandler(root)([]codextea.SettingsEdit{
			{KeyPath: "memories.use_memories", Value: useMemories},
			{KeyPath: "memories.generate_memories", Value: generateMemories},
		})
		if err != nil || !generateChanged || strings.TrimSpace(threadID) == "" {
			return result, err
		}
		params, err := json.Marshal(appserver.ThreadMemoryModeSetParams{
			ThreadID: strings.TrimSpace(threadID),
			Mode:     interactiveThreadMemoryMode(generateMemories),
		})
		if err != nil {
			return result, fmt.Errorf("Saved memory settings, but failed to update the current thread: %w", err)
		}
		router := appserver.NewRouter(newSessionStore())
		defer router.Close()
		response := router.Handle(&appserver.Request{JSONRPC: "2.0", ID: appserver.IntID(1), Method: appserver.MethodThreadMemoryModeSet, Params: params})
		if response.Error != nil {
			return result, fmt.Errorf("Saved memory settings, but failed to update the current thread: %s", response.Error.Message)
		}
		return result, nil
	}
}

func interactiveMemoryResetHandler() codextea.MemoryResetFunc {
	return func() error {
		router := appserver.NewRouter(newSessionStore())
		defer router.Close()
		response := router.Handle(&appserver.Request{JSONRPC: "2.0", ID: appserver.IntID(1), Method: appserver.MethodMemoryReset})
		if response.Error != nil {
			return errors.New(response.Error.Message)
		}
		return nil
	}
}

func interactiveFeedbackSubmitHandler() codextea.FeedbackSubmitFunc {
	return func(params appserver.FeedbackUploadParams) (appserver.FeedbackUploadResponse, error) {
		if err := params.Validate(); err != nil {
			return appserver.FeedbackUploadResponse{}, err
		}
		threadID := "feedback-local"
		if params.ThreadID != nil && strings.TrimSpace(*params.ThreadID) != "" {
			threadID = strings.TrimSpace(*params.ThreadID)
		}
		snapshot := &appserver.FeedbackSnapshot{ThreadID: threadID}
		snapshot.PrepareUpload(&appserver.FeedbackUploadOptions{
			Classification: params.Classification,
			Reason:         params.Reason,
			ClientTags:     params.Tags,
			IncludeLogs:    params.IncludeLogs,
		})
		return appserver.FeedbackUploadResponse{ThreadID: threadID}, nil
	}
}

func interactiveThreadMemoryMode(enabled bool) appserver.ThreadMemoryMode {
	if enabled {
		return appserver.ThreadMemoryModeEnabled
	}
	return appserver.ThreadMemoryModeDisabled
}

func interactiveLoadSettings(root *cli.RootOptions) (codextea.SettingsWriteResult, error) {
	loaded, err := config.LoadEffectiveWithOptions(auth.DefaultCodexHome(), interactiveKeymapLoadOptions(root))
	if err != nil {
		return codextea.SettingsWriteResult{}, err
	}
	return interactiveSettingsFromConfig(loaded), nil
}

func interactiveSettingsFromConfig(loaded *config.Config) codextea.SettingsWriteResult {
	var values map[string]any
	featureSettings := map[string]bool{}
	if loaded != nil {
		values = loaded.Values
		featureSettings = loaded.FeatureSettings()
	}
	userMarketplaces, gitMarketplaces := interactivePluginMarketplacesFromConfig(values)
	return codextea.SettingsWriteResult{
		FeatureSettings:         featureSettings,
		UseMemories:             interactiveMemoryBoolFromConfig(values, "use_memories"),
		GenerateMemories:        interactiveMemoryBoolFromConfig(values, "generate_memories"),
		FeedbackEnabled:         interactiveFeedbackEnabledFromConfig(values),
		Personality:             interactivePersonalityFromConfig(values),
		Notifications:           interactiveNotificationSettingsFromConfig(values),
		NotificationMethod:      interactiveNotificationMethodFromConfig(values),
		NotificationCondition:   interactiveNotificationConditionFromConfig(values),
		PermissionRequirements:  interactivePermissionRequirementsFromConfig(values),
		HideRateLimitModelNudge: interactiveHideRateLimitModelNudgeFromConfig(values),
		TUITheme:                interactiveTUIStringFromConfig(values, "theme"),
		TUIPet:                  interactiveTUIStringFromConfig(values, "pet"),
		SessionPickerView:       interactiveTUIStringFromConfig(values, "session_picker_view"),
		PluginUserMarketplaces:  userMarketplaces,
		PluginGitMarketplaces:   gitMarketplaces,
	}
}

func interactivePluginMarketplacesFromConfig(values map[string]any) (map[string]bool, map[string]bool) {
	userMarketplaces := map[string]bool{}
	gitMarketplaces := map[string]bool{}
	marketplaces, _ := values["marketplaces"].(map[string]any)
	for name, raw := range marketplaces {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		userMarketplaces[name] = true
		entry, _ := raw.(map[string]any)
		gitMarketplaces[name] = strings.TrimSpace(fmt.Sprint(entry["source_type"])) == string(plugin.MarketplaceSourceGit)
	}
	return userMarketplaces, gitMarketplaces
}

func interactiveMemoryBoolFromConfig(values map[string]any, key string) *bool {
	value := true
	if memories, ok := values["memories"].(map[string]any); ok {
		if configured, ok := memories[key].(bool); ok {
			value = configured
		}
	}
	return &value
}

func interactiveFeedbackEnabledFromConfig(values map[string]any) *bool {
	value := true
	if feedback, ok := values["feedback"].(map[string]any); ok {
		if configured, ok := feedback["enabled"].(bool); ok {
			value = configured
		}
	}
	return &value
}

func interactiveConfigService(root *cli.RootOptions) *config.ConfigService {
	service := config.NewConfigService(auth.DefaultCodexHome())
	if root != nil && strings.TrimSpace(root.Shared.Profile) != "" {
		service.SetProfile(root.Shared.Profile)
	}
	return service
}

func interactiveSkillsReader(root *cli.RootOptions) codextea.SkillsListReaderFunc {
	codexHome := auth.DefaultCodexHome()
	service := appserver.NewSkillsServiceWithOptions(&appserver.SkillsServiceOptions{
		Config:              interactiveConfigService(root),
		CodexHome:           codexHome,
		IncludeDefaultRoots: true,
	})
	return func(cwd string, forceReload bool) (appserver.SkillsListResponse, error) {
		cwd = strings.TrimSpace(cwd)
		if cwd == "" {
			cwd = interactiveSessionPickerCWD(root)
		}
		params := &appserver.SkillsListParams{ForceReload: forceReload}
		if cwd != "" {
			params.CWDs = []string{cwd}
		}
		response, err := service.List(params)
		if err != nil || response == nil {
			return appserver.SkillsListResponse{}, err
		}
		return *response, nil
	}
}

func interactiveSkillEnabledWriter(root *cli.RootOptions) codextea.SkillEnabledWriteFunc {
	service := appserver.NewSkillsServiceWithOptions(&appserver.SkillsServiceOptions{
		Config:              interactiveConfigService(root),
		CodexHome:           auth.DefaultCodexHome(),
		IncludeDefaultRoots: true,
	})
	return func(path string, enabled bool) (bool, error) {
		response, err := service.WriteConfig(&appserver.SkillsConfigWriteParams{
			Path:    strings.TrimSpace(path),
			Enabled: enabled,
		})
		if err != nil {
			return false, err
		}
		if response == nil {
			return false, errors.New("skills/config/write returned no response")
		}
		return response.EffectiveEnabled, nil
	}
}

func interactivePluginService() *plugin.PluginService {
	service := plugin.NewPluginService()
	service.SetCodexHome(auth.DefaultCodexHome())
	return service
}

func interactivePluginReader(root *cli.RootOptions, service *plugin.PluginService) codextea.PluginListReaderFunc {
	return func(cwd string, forceRefetch bool) (plugin.PluginListResponse, error) {
		cwd = strings.TrimSpace(cwd)
		if cwd == "" {
			cwd = interactiveSessionPickerCWD(root)
		}
		params := &plugin.PluginListParams{IncludeInstalled: true, ForceRefetch: forceRefetch}
		if cwd != "" {
			params.CWDs = []string{cwd}
		}
		return *service.List(params), nil
	}
}

func interactivePluginReaderByID(service *plugin.PluginService) codextea.PluginReadFunc {
	return func(params plugin.PluginReadParams) (plugin.PluginReadResponse, error) {
		response, err := service.Read(&params)
		if err != nil {
			return plugin.PluginReadResponse{}, err
		}
		if response == nil {
			return plugin.PluginReadResponse{}, errors.New("plugin/read returned no response")
		}
		return *response, nil
	}
}

func interactivePluginInstaller(service *plugin.PluginService) codextea.PluginInstallFunc {
	return func(params plugin.PluginInstallParams) (plugin.PluginInstallResponse, error) {
		response, err := service.Install(&params)
		if err != nil {
			return plugin.PluginInstallResponse{}, err
		}
		if response == nil {
			return plugin.PluginInstallResponse{}, errors.New("plugin/install returned no response")
		}
		return *response, nil
	}
}

func interactivePluginUninstaller(service *plugin.PluginService) codextea.PluginUninstallFunc {
	return func(params plugin.PluginUninstallParams) (plugin.PluginUninstallResponse, error) {
		response, err := service.Uninstall(&params)
		if err != nil {
			return plugin.PluginUninstallResponse{}, err
		}
		if response == nil {
			return plugin.PluginUninstallResponse{}, errors.New("plugin/uninstall returned no response")
		}
		return *response, nil
	}
}

func interactivePluginEnabledWriter(root *cli.RootOptions) codextea.PluginEnabledWriteFunc {
	service := interactiveConfigService(root)
	return func(pluginID string, enabled bool) error {
		_, err := service.WriteValue(&config.ConfigValueWriteParams{
			KeyPath:       "plugins." + strings.TrimSpace(pluginID),
			Value:         map[string]any{"enabled": enabled},
			MergeStrategy: config.MergeUpsert,
		})
		return err
	}
}

func interactiveMarketplaceAdder(service *plugin.PluginService) codextea.MarketplaceAddFunc {
	return func(params plugin.MarketplaceAddParams) (plugin.MarketplaceAddResponse, error) {
		response, err := service.AddMarketplace(&params)
		if err != nil {
			return plugin.MarketplaceAddResponse{}, err
		}
		if response == nil {
			return plugin.MarketplaceAddResponse{}, errors.New("marketplace/add returned no response")
		}
		return *response, nil
	}
}

func interactiveMarketplaceRemover(service *plugin.PluginService) codextea.MarketplaceRemoveFunc {
	return func(params plugin.MarketplaceRemoveParams) (plugin.MarketplaceRemoveResponse, error) {
		response, err := service.RemoveMarketplace(&params)
		if err != nil {
			return plugin.MarketplaceRemoveResponse{}, err
		}
		if response == nil {
			return plugin.MarketplaceRemoveResponse{}, errors.New("marketplace/remove returned no response")
		}
		return *response, nil
	}
}

func interactiveMarketplaceUpgrader(service *plugin.PluginService) codextea.MarketplaceUpgradeFunc {
	return func(params plugin.MarketplaceUpgradeParams) (plugin.MarketplaceUpgradeResponse, error) {
		response, err := service.UpgradeMarketplace(&params)
		if err != nil {
			return plugin.MarketplaceUpgradeResponse{}, err
		}
		if response == nil {
			return plugin.MarketplaceUpgradeResponse{}, errors.New("marketplace/upgrade returned no response")
		}
		return *response, nil
	}
}

func interactiveFuzzyFileSearchReader(root *cli.RootOptions) codextea.FuzzyFileSearchReaderFunc {
	service := appserver.NewMiscService()
	return func(query string, cwd string, cancellationToken string) (appserver.FuzzyFileSearchResponse, error) {
		cwd = strings.TrimSpace(cwd)
		if cwd == "" {
			cwd = interactiveSessionPickerCWD(root)
		}
		params := &appserver.FuzzyFileSearchParams{Query: query}
		if cwd != "" {
			params.Roots = []string{cwd}
		}
		if cancellationToken = strings.TrimSpace(cancellationToken); cancellationToken != "" {
			params.CancellationToken = &cancellationToken
		}
		response, err := service.FuzzyFileSearch(context.Background(), params)
		if err != nil || response == nil {
			return appserver.FuzzyFileSearchResponse{}, err
		}
		return *response, nil
	}
}

func interactiveDebugConfigReader(root *cli.RootOptions) codextea.DebugConfigReaderFunc {
	return func() ([]string, error) {
		service := interactiveConfigService(root)
		params := &config.ConfigReadParams{IncludeLayers: true}
		if cwd := interactiveSessionPickerCWD(root); cwd != "" {
			params.CWD = &cwd
		}
		read, err := service.Read(params)
		if err != nil {
			return nil, err
		}
		requirements := service.Requirements()
		var effectiveRequirements *config.ConfigRequirements
		if requirements != nil {
			effectiveRequirements = requirements.Requirements
		}
		if effectiveRequirements == nil {
			loadedRequirements, err := config.LoadRequirementsFile(filepath.Join(service.CodexHome(), "requirements.toml"))
			if err != nil {
				return nil, err
			}
			effectiveRequirements = loadedRequirements
		}
		return codextui.NewDebugConfigOutput(read, effectiveRequirements, nil), nil
	}
}

func interactivePersonalityFromConfig(values map[string]any) chatwidget.Personality {
	if values == nil {
		return ""
	}
	value, ok := values["personality"].(string)
	if !ok {
		return ""
	}
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(chatwidget.PersonalityFriendly):
		return chatwidget.PersonalityFriendly
	case string(chatwidget.PersonalityPragmatic):
		return chatwidget.PersonalityPragmatic
	case string(chatwidget.PersonalityNone):
		return chatwidget.PersonalityNone
	default:
		return ""
	}
}

func interactiveHideRateLimitModelNudgeFromConfig(values map[string]any) *bool {
	if values == nil {
		return nil
	}
	notices, ok := values["notices"].(map[string]any)
	if !ok {
		return nil
	}
	value, ok := notices["hide_rate_limit_model_nudge"].(bool)
	if !ok {
		return nil
	}
	return &value
}

func interactiveNotificationSettingsFromConfig(values map[string]any) *chatwidget.NotificationsSetting {
	tui := interactiveTUIConfig(values)
	raw, ok := tui["notifications"]
	if !ok {
		return &chatwidget.NotificationsSetting{Enabled: true}
	}
	switch typed := raw.(type) {
	case bool:
		return &chatwidget.NotificationsSetting{Enabled: typed}
	case []string:
		return &chatwidget.NotificationsSetting{Custom: copyNotificationTypes(typed), CustomSet: true}
	case []any:
		items := make([]string, 0, len(typed))
		for _, item := range typed {
			if text, ok := item.(string); ok {
				items = append(items, text)
			}
		}
		return &chatwidget.NotificationsSetting{Custom: copyNotificationTypes(items), CustomSet: true}
	case string:
		return &chatwidget.NotificationsSetting{Custom: []string{typed}, CustomSet: true}
	default:
		return &chatwidget.NotificationsSetting{Enabled: true}
	}
}

func interactiveNotificationMethodFromConfig(values map[string]any) codextui.NotificationMethod {
	tui := interactiveTUIConfig(values)
	value, _ := tui["notification_method"].(string)
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(codextui.NotificationMethodOSC9):
		return codextui.NotificationMethodOSC9
	case string(codextui.NotificationMethodBEL):
		return codextui.NotificationMethodBEL
	default:
		return codextui.NotificationMethodAuto
	}
}

func interactiveNotificationConditionFromConfig(values map[string]any) codextui.NotificationCondition {
	tui := interactiveTUIConfig(values)
	value, _ := tui["notification_condition"].(string)
	switch strings.ToLower(strings.TrimSpace(value)) {
	case string(codextui.NotificationConditionAlways):
		return codextui.NotificationConditionAlways
	default:
		return codextui.NotificationConditionUnfocused
	}
}

func interactiveTUIConfig(values map[string]any) map[string]any {
	if values == nil {
		return map[string]any{}
	}
	tui, ok := values["tui"].(map[string]any)
	if !ok || tui == nil {
		return map[string]any{}
	}
	return tui
}

func interactiveTUIStringFromConfig(values map[string]any, key string) string {
	tui := interactiveTUIConfig(values)
	value, _ := tui[key].(string)
	return strings.TrimSpace(value)
}

func normalizeNotificationTypes(values []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func copyNotificationTypes(values []string) []string {
	return append([]string(nil), values...)
}

func interactivePermissionRequirementsFromConfig(values map[string]any) *chatwidget.PermissionRequirements {
	requirements, ok := values["requirements"].(map[string]any)
	if !ok || requirements == nil {
		return nil
	}
	out := chatwidget.PermissionRequirements{}
	has := false
	if policies := interactiveStringListFromEitherKey(requirements, "allowed_approval_policies", "allowedApprovalPolicies"); len(policies) > 0 {
		has = true
		out.AllowedApprovalPolicies = interactiveApprovalPoliciesFromStrings(policies)
	}
	if reviewers := interactiveStringListFromEitherKey(requirements, "allowed_approvals_reviewers", "allowedApprovalsReviewers"); len(reviewers) > 0 {
		has = true
		out.AllowedReviewers = interactiveApprovalReviewersFromStrings(reviewers)
	}
	if profiles, ok := interactiveBoolMapFromEitherKey(requirements, "allowed_permission_profiles", "allowedPermissionProfiles"); ok {
		has = true
		out.AllowedProfiles = profiles
	}
	if modes := interactiveStringListFromEitherKey(requirements, "allowed_windows_sandbox_implementations", "allowedWindowsSandboxImplementations"); len(modes) > 0 {
		has = true
		out.AllowedWindowsSandboxModes = interactiveWindowsSandboxModesFromStrings(modes)
	}
	if !has {
		return nil
	}
	return &out
}

func interactivePermissionRequirementsFromConfigRequirements(requirements *config.ConfigRequirements) *chatwidget.PermissionRequirements {
	if requirements == nil {
		return nil
	}
	out := chatwidget.PermissionRequirements{}
	has := false
	if len(requirements.AllowedApprovalPolicies) > 0 {
		values := make([]string, 0, len(requirements.AllowedApprovalPolicies))
		for _, policy := range requirements.AllowedApprovalPolicies {
			values = append(values, string(policy))
		}
		has = true
		out.AllowedApprovalPolicies = interactiveApprovalPoliciesFromStrings(values)
	}
	if len(requirements.AllowedApprovalsReviewers) > 0 {
		values := make([]string, 0, len(requirements.AllowedApprovalsReviewers))
		for _, reviewer := range requirements.AllowedApprovalsReviewers {
			values = append(values, string(reviewer))
		}
		has = true
		out.AllowedReviewers = interactiveApprovalReviewersFromStrings(values)
	}
	if requirements.AllowedPermissionProfiles != nil {
		has = true
		out.AllowedProfiles = make(map[string]bool, len(requirements.AllowedPermissionProfiles))
		for key, value := range requirements.AllowedPermissionProfiles {
			if strings.TrimSpace(key) != "" {
				out.AllowedProfiles[strings.TrimSpace(key)] = value
			}
		}
	}
	if len(requirements.AllowedWindowsSandboxImplementations) > 0 {
		values := make([]string, 0, len(requirements.AllowedWindowsSandboxImplementations))
		for _, mode := range requirements.AllowedWindowsSandboxImplementations {
			values = append(values, string(mode))
		}
		has = true
		out.AllowedWindowsSandboxModes = interactiveWindowsSandboxModesFromStrings(values)
	}
	if !has {
		return nil
	}
	return &out
}

func interactiveApprovalPoliciesFromStrings(values []string) []chatwidget.ApprovalPolicy {
	out := []chatwidget.ApprovalPolicy{}
	seen := map[chatwidget.ApprovalPolicy]bool{}
	for _, value := range values {
		var policy chatwidget.ApprovalPolicy
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "on-request", "on_request":
			policy = chatwidget.ApprovalOnRequest
		case "never":
			policy = chatwidget.ApprovalNever
		default:
			continue
		}
		if !seen[policy] {
			seen[policy] = true
			out = append(out, policy)
		}
	}
	return out
}

func interactiveApprovalReviewersFromStrings(values []string) []chatwidget.ApprovalsReviewer {
	out := []chatwidget.ApprovalsReviewer{}
	seen := map[chatwidget.ApprovalsReviewer]bool{}
	for _, value := range values {
		var reviewer chatwidget.ApprovalsReviewer
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "user":
			reviewer = chatwidget.ApprovalsReviewerUser
		case "auto_review", "auto-review":
			reviewer = chatwidget.ApprovalsReviewerAutoReview
		default:
			continue
		}
		if !seen[reviewer] {
			seen[reviewer] = true
			out = append(out, reviewer)
		}
	}
	return out
}

func interactiveWindowsSandboxModesFromStrings(values []string) []chatwidget.WindowsSandboxMode {
	out := []chatwidget.WindowsSandboxMode{}
	seen := map[chatwidget.WindowsSandboxMode]bool{}
	for _, value := range values {
		var mode chatwidget.WindowsSandboxMode
		switch strings.ToLower(strings.TrimSpace(value)) {
		case string(chatwidget.WindowsSandboxModeDefault):
			mode = chatwidget.WindowsSandboxModeDefault
		case string(chatwidget.WindowsSandboxModeElevated):
			mode = chatwidget.WindowsSandboxModeElevated
		case string(chatwidget.WindowsSandboxModeUnelevated):
			mode = chatwidget.WindowsSandboxModeUnelevated
		case string(chatwidget.WindowsSandboxModeDisabled):
			mode = chatwidget.WindowsSandboxModeDisabled
		default:
			continue
		}
		if !seen[mode] {
			seen[mode] = true
			out = append(out, mode)
		}
	}
	return out
}

func interactiveStringListFromEitherKey(values map[string]any, snake string, camel string) []string {
	if list := interactiveStringListFromConfigValue(values[snake]); len(list) > 0 {
		return list
	}
	return interactiveStringListFromConfigValue(values[camel])
}

func interactiveStringListFromConfigValue(raw any) []string {
	switch typed := raw.(type) {
	case []string:
		return normalizeNotificationTypes(typed)
	case []any:
		out := make([]string, 0, len(typed))
		for _, item := range typed {
			out = append(out, fmt.Sprint(item))
		}
		return normalizeNotificationTypes(out)
	case string:
		return normalizeNotificationTypes([]string{typed})
	default:
		return nil
	}
}

func interactiveBoolMapFromEitherKey(values map[string]any, snake string, camel string) (map[string]bool, bool) {
	if out, ok := interactiveBoolMapFromConfigValue(values[snake]); ok {
		return out, true
	}
	return interactiveBoolMapFromConfigValue(values[camel])
}

func interactiveBoolMapFromConfigValue(raw any) (map[string]bool, bool) {
	switch typed := raw.(type) {
	case map[string]bool:
		out := make(map[string]bool, len(typed))
		for key, value := range typed {
			if strings.TrimSpace(key) != "" {
				out[strings.TrimSpace(key)] = value
			}
		}
		return out, true
	case map[string]any:
		out := make(map[string]bool, len(typed))
		for key, rawValue := range typed {
			value, ok := rawValue.(bool)
			if ok && strings.TrimSpace(key) != "" {
				out[strings.TrimSpace(key)] = value
			}
		}
		return out, true
	default:
		return nil, false
	}
}

func interactiveKeymapLoadOptions(root *cli.RootOptions) *config.EffectiveOptions {
	if root == nil {
		return nil
	}
	return &config.EffectiveOptions{
		Profile:         root.Shared.Profile,
		CWD:             root.Shared.CWD,
		RawOverrides:    append([]string(nil), root.ConfigOverrides...),
		EnableFeatures:  root.EnableFeatures,
		DisableFeatures: root.DisableFeatures,
	}
}

func interactiveKeymapEditMessage(edit codextui.KeymapEdit) string {
	switch edit.Operation {
	case codextui.KeymapEditUnbind:
		return "Unbound " + edit.Context + "." + edit.Action + "."
	case codextui.KeymapEditUnset:
		return "Reset " + edit.Context + "." + edit.Action + " to defaults."
	default:
		return "Updated " + edit.Context + "." + edit.Action + " to " + strings.Join(edit.Bindings, ", ") + "."
	}
}

func interactiveSessionPickerItems(root *cli.RootOptions) []codextui.SessionSummary {
	items, err := codextui.LoadSessionSummariesFromStore(newSessionStore(), codextui.SessionSourceOptions{
		CWD:             interactiveSessionPickerCWD(root),
		IncludeArchived: true,
	})
	if err != nil {
		return nil
	}
	return items
}

func interactiveSessionActionHandler(root *cli.RootOptions) codextea.SessionActionFunc {
	return func(selection codextui.SessionSelection) (*codextui.SessionSummary, error) {
		store := newSessionStore()
		threadID := session.ThreadID(strings.TrimSpace(selection.Target.ThreadID))
		if threadID == "" {
			return nil, errors.New("session action requires a thread id")
		}
		switch selection.Kind {
		case codextui.SessionSelectionFork:
			record, err := store.Fork(threadID, session.ForkOptions{Mode: session.ForkAll})
			if err != nil {
				return nil, err
			}
			return firstSessionSummary(store, record), nil
		case codextui.SessionSelectionArchive:
			if err := store.Archive(threadID); err != nil {
				return nil, err
			}
			return nil, nil
		case codextui.SessionSelectionUnarchive:
			record, err := store.Unarchive(threadID)
			if err != nil {
				return nil, err
			}
			return firstSessionSummary(store, record), nil
		case codextui.SessionSelectionDelete:
			threadIDs, err := store.SubtreeThreadIDs(threadID)
			if err != nil {
				return nil, err
			}
			for _, id := range session.DeleteOrderForSubtree(threadIDs) {
				if err := store.Delete(id); err != nil && !errors.Is(err, session.ErrThreadNotFound) {
					return nil, err
				}
			}
			return nil, nil
		default:
			return nil, nil
		}
	}
}

func interactiveRenameThreadHandler() codextea.ThreadRenameFunc {
	return func(threadID string, name string) error {
		threadID = strings.TrimSpace(threadID)
		name = strings.TrimSpace(name)
		if threadID == "" {
			return errors.New("rename requires a thread id")
		}
		if name == "" {
			return errors.New("thread name must not be empty")
		}
		store := newSessionStore()
		record, err := store.Read(session.ThreadID(threadID), false, true)
		if err != nil {
			return err
		}
		extra := make(map[string]any, len(record.Metadata.Extra)+1)
		for key, value := range record.Metadata.Extra {
			extra[key] = value
		}
		extra["thread_name_explicit"] = true
		_, err = store.UpdateMetadata(session.ThreadID(threadID), &session.MetadataPatch{Title: &name, Extra: extra}, false)
		return err
	}
}

func interactiveLogoutHandler(ctx context.Context, root *cli.RootOptions) codextea.LogoutFunc {
	return func() error {
		if ctx == nil {
			ctx = context.Background()
		}
		var overrides []string
		if root != nil {
			overrides = root.ConfigOverrides
		}
		codexHome := auth.DefaultCodexHome()
		storeOptions, err := authStoreOptionsFromConfig(codexHome, overrides)
		if err != nil {
			return err
		}
		_, err = auth.LogoutWithRevoke(ctx, codexHome, storeOptions)
		return err
	}
}

func interactiveOpenDesktopThread(threadID string) error {
	url := tuiapp.DesktopThreadURL(strings.TrimSpace(threadID))
	command := exec.Command("powershell.exe", "-NoProfile", "-Command", tuiapp.WindowsDesktopAppLaunchScript(url))
	_, err := command.Output()
	if err == nil {
		return nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		if stderr := strings.TrimSpace(string(exitErr.Stderr)); stderr != "" {
			return errors.New(stderr)
		}
		return fmt.Errorf("failed to launch the Desktop app through PowerShell with %s", exitErr.ProcessState)
	}
	return fmt.Errorf("failed to launch the Desktop app through PowerShell: %w", err)
}

func interactiveResumeSessionHandler(root *cli.RootOptions) codextea.SessionResumeFunc {
	return func(selection codextui.SessionSelection) (codextea.SessionResumeResponse, error) {
		store := newSessionStore()
		threadID := session.ThreadID(strings.TrimSpace(selection.Target.ThreadID))
		if threadID == "" {
			return codextea.SessionResumeResponse{}, errors.New("resume requires a thread id")
		}
		record, err := store.Read(threadID, true, true)
		if err != nil {
			return codextea.SessionResumeResponse{}, err
		}
		if interactiveRepairImageGenerationItems(record, auth.DefaultCodexHome()) {
			_ = store.Save(record)
		}
		var tokenUsage *protocol.ThreadTokenUsage
		if restored := appserver.RestoredTokenUsageForRecord(record); restored != nil {
			value := remoteThreadTokenUsage(*restored)
			tokenUsage = &value
		}
		return codextea.SessionResumeResponse{
			Summary:    firstSessionSummary(store, record),
			Messages:   interactiveSessionMessagesFromRecord(record),
			Status:     "idle",
			TokenUsage: tokenUsage,
		}, nil
	}
}

func firstSessionSummary(store *session.Store, record *session.Record) *codextui.SessionSummary {
	if record == nil {
		return nil
	}
	summaries := codextui.SessionSummariesFromRecords(store, []session.Record{*record})
	if len(summaries) == 0 {
		return nil
	}
	return &summaries[0]
}

func interactiveSessionMessagesFromRecord(record *session.Record) []codextui.Message {
	if record == nil {
		return nil
	}
	messages := make([]codextui.Message, 0, len(record.Items))
	inReviewMode := false
	for i := range record.Items {
		item := record.Items[i]
		itemType := normalizeInteractiveSessionItemType(item.Type)
		switch itemType {
		case "enteredreviewmode":
			hint := strings.TrimSpace(firstNonEmptyLocal(item.Text, interactiveSessionItemDataString(item, "review"), remoteTUIAnyString(item.Metadata["review"])))
			if hint == "" {
				hint = "current changes"
			}
			text := ">> Code review started: " + hint + " <<"
			messages = append(messages, codextui.Message{Role: codextui.RoleHistory, Text: text, RawText: text})
			inReviewMode = true
			continue
		case "exitedreviewmode":
			if inReviewMode {
				text := "<< Code review finished >>"
				messages = append(messages, codextui.Message{Role: codextui.RoleHistory, Text: text, RawText: text})
			}
			inReviewMode = false
			continue
		case "contextcompaction":
			text := "Context compacted"
			messages = append(messages, codextui.Message{Role: codextui.RoleHistory, Text: text, RawText: text})
			continue
		}
		if interactiveSessionItemIsHiddenContextInstruction(item) || interactiveSessionItemIsReviewUserMessage(item) || (inReviewMode && normalizeInteractiveSessionItemRole(item.Role) == "user") {
			continue
		}
		message, ok := interactiveSessionMessageFromItem(item)
		if ok {
			messages = append(messages, message)
		}
	}
	return messages
}

func interactiveSessionItemIsReviewUserMessage(item session.Item) bool {
	kind := firstNonEmptyLocal(interactiveSessionItemDataString(item, "kind"), remoteTUIAnyString(item.Metadata["kind"]))
	return strings.TrimSpace(kind) == "review_rollout_user"
}

func interactiveSessionItemIsHiddenContextInstruction(item session.Item) bool {
	kind := firstNonEmptyLocal(
		interactiveSessionItemDataString(item, "kind"),
		remoteTUIAnyString(item.Metadata["kind"]),
	)
	if kind == "skill_instructions" || kind == "image_generation_instructions" {
		return true
	}
	id := strings.TrimSpace(item.ID)
	if strings.HasPrefix(id, "skill-instructions-") || strings.HasPrefix(id, "image-generation-instructions-") {
		return true
	}
	return false
}

func interactiveSessionMessageFromItem(item session.Item) (codextui.Message, bool) {
	itemType := normalizeInteractiveSessionItemType(item.Type)
	role := normalizeInteractiveSessionItemRole(item.Role)
	switch {
	case itemType == "usermessage" || role == "user":
		text := interactiveSessionItemUserText(item)
		return codextui.Message{Role: codextui.RoleUser, Text: text, RawText: text}, strings.TrimSpace(text) != ""
	case itemType == "imagegeneration" || itemType == "imagegenerationcall":
		text := interactiveSessionItemImageGenerationText(item)
		if text == "" {
			return codextui.Message{}, false
		}
		return codextui.Message{Role: codextui.RoleHistory, Text: text, RawText: text}, true
	case itemType == "agentmessage" || itemType == "assistantmessage" || role == "assistant":
		text := strings.TrimSpace(item.Text)
		return codextui.Message{Role: codextui.RoleAssistant, Text: text, RawText: text}, text != ""
	case itemType == "plan":
		text := strings.TrimSpace(item.Text)
		if text == "" {
			return codextui.Message{}, false
		}
		return codextui.Message{Role: codextui.RoleAssistant, Text: text, RawText: text}, true
	case itemType == "reasoning":
		text := interactiveSessionItemReasoningText(item)
		if text == "" {
			return codextui.Message{}, false
		}
		return codextui.Message{Role: codextui.RoleHistory, Text: "Reasoning\n" + text, RawText: text}, true
	case itemType == "commandexecution" || itemType == "mcptoolcall" || itemType == "dynamictoolcall" || itemType == "collabagenttoolcall" || itemType == "subagentactivity" || strings.Contains(itemType, "tool"):
		text := interactiveSessionItemToolText(item)
		if text == "" {
			return codextui.Message{}, false
		}
		return codextui.Message{Role: codextui.RoleHistory, Text: text, RawText: text}, true
	case role == "system":
		text := strings.TrimSpace(item.Text)
		return codextui.Message{Role: codextui.RoleSystem, Text: text, RawText: text}, text != ""
	default:
		text := strings.TrimSpace(firstNonEmptyLocal(item.Text, interactiveSessionItemDataString(item, "text", "output", "input")))
		if text == "" {
			return codextui.Message{}, false
		}
		return codextui.Message{Role: codextui.RoleHistory, Text: text, RawText: text}, true
	}
}

func normalizeInteractiveSessionItemType(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "_", "")
	value = strings.ReplaceAll(value, "-", "")
	return strings.ToLower(value)
}

func normalizeInteractiveSessionItemRole(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func interactiveRepairImageGenerationItems(record *session.Record, codexHome string) bool {
	if record == nil {
		return false
	}
	codexHome = strings.TrimSpace(codexHome)
	changed := false
	threadID := string(record.ID)
	for i := range record.Items {
		item := &record.Items[i]
		itemType := normalizeInteractiveSessionItemType(item.Type)
		if itemType != "imagegeneration" && itemType != "imagegenerationcall" {
			continue
		}
		if item.Data == nil {
			item.Data = map[string]any{}
		}
		result := firstNonEmptyLocal(
			interactiveSessionItemDataString(*item, "result"),
			strings.TrimSpace(item.Text),
		)
		status := modelpkg.NormalizeImageGenerationStatus(firstNonEmptyLocal(strings.TrimSpace(item.Status), interactiveSessionItemDataString(*item, "status")), result)
		revisedPrompt := firstNonEmptyLocal(
			interactiveSessionItemDataString(*item, "revisedPrompt", "revised_prompt"),
			remoteTUIAnyString(item.Metadata["revisedPrompt"]),
			remoteTUIAnyString(item.Metadata["revised_prompt"]),
		)
		itemID := strings.TrimSpace(item.ID)
		if itemID == "" {
			itemID = "image-generation-" + strings.TrimSpace(threadID)
			item.ID = itemID
			changed = true
		}
		if saved := interactiveSessionItemDataString(*item, "savedPath", "saved_path"); saved == "" && codexHome != "" && result != "" {
			if path, err := eventmap.SaveImageGenerationResult(codexHome, threadID, itemID, result); err == nil {
				item.Data["savedPath"] = path
				item.Data["saved_path"] = path
				changed = true
			}
		}
		if item.Type != "imageGeneration" {
			item.Type = "imageGeneration"
			changed = true
		}
		if item.Role != "" {
			item.Role = ""
			changed = true
		}
		if item.Status != status {
			item.Status = status
			changed = true
		}
		if result != "" {
			item.Data["result"] = result
		}
		if revisedPrompt != "" {
			item.Data["revisedPrompt"] = revisedPrompt
			item.Data["revised_prompt"] = revisedPrompt
			if item.Text != revisedPrompt {
				item.Text = revisedPrompt
				changed = true
			}
			if len(item.Content) != 0 {
				item.Content = nil
				changed = true
			}
		} else if result != "" && item.Text == result {
			item.Text = ""
			changed = true
			if len(item.Content) != 0 {
				item.Content = nil
				changed = true
			}
		}
	}
	return changed
}

func interactiveSessionItemUserText(item session.Item) string {
	parts := []string{}
	if strings.TrimSpace(item.Text) != "" {
		parts = append(parts, strings.TrimSpace(item.Text))
	}
	for _, content := range item.Content {
		if strings.TrimSpace(content.Text) != "" {
			parts = append(parts, strings.TrimSpace(content.Text))
		}
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func interactiveSessionItemReasoningText(item session.Item) string {
	parts := []string{}
	for _, key := range []string{"summary", "reasoningContent", "content"} {
		parts = append(parts, remoteTUIAnyStrings(item.Data[key])...)
	}
	if strings.TrimSpace(item.Text) != "" {
		parts = append(parts, strings.TrimSpace(item.Text))
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

func interactiveSessionItemToolText(item session.Item) string {
	title := firstNonEmptyLocal(
		interactiveSessionItemDataString(item, "command", "cmd", "tool", "name"),
		strings.TrimSpace(item.Name),
		strings.TrimSpace(item.Type),
		"tool",
	)
	lines := []string{title}
	if status := firstNonEmptyLocal(strings.TrimSpace(item.Status), interactiveSessionItemDataString(item, "status")); status != "" {
		lines = append(lines, "status: "+status)
	}
	for _, key := range []string{"arguments", "input", "output", "aggregatedOutput", "formattedOutput", "result"} {
		if value := interactiveSessionItemDataString(item, key); value != "" {
			lines = append(lines, value)
			break
		}
	}
	if strings.TrimSpace(item.Text) != "" {
		lines = append(lines, strings.TrimSpace(item.Text))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func interactiveSessionItemImageGenerationText(item session.Item) string {
	result := interactiveSessionItemDataString(item, "result")
	text := strings.TrimSpace(item.Text)
	if result != "" && text == result {
		text = ""
	}
	detail := firstNonEmptyLocal(
		interactiveSessionItemDataString(item, "revisedPrompt", "revised_prompt"),
		text,
		strings.TrimSpace(item.ID),
	)
	if detail == "" {
		return ""
	}
	lines := []string{"Generated Image", detail}
	if saved := interactiveSessionItemDataString(item, "savedPath", "saved_path"); saved != "" {
		lines = append(lines, "Saved to: "+saved)
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func interactiveSessionItemDataString(item session.Item, keys ...string) string {
	for _, key := range keys {
		if value := remoteTUIAnyString(item.Data[key]); value != "" {
			return value
		}
	}
	return ""
}

func interactiveSessionPickerCWD(root *cli.RootOptions) string {
	if root != nil {
		if cwd := strings.TrimSpace(root.Shared.CWD); cwd != "" {
			return cwd
		}
	}
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cwd)
}

func interactiveIDEContextReader(cwd string) (*idecontext.IdeContext, error) {
	return idecontext.FetchIDEContext(cwd, auth.DefaultCodexHome())
}

func interactiveApproveAutoReviewDenialHandler() codextea.AutoReviewDenialApproveFunc {
	return func(threadID string, entry chatwidget.AutoReviewDenialEntry) error {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" {
			return errors.New("auto-review approval requires a thread id")
		}
		if len(entry.Event) == 0 {
			return errors.New("auto-review denial event is unavailable")
		}
		threadRouter := appserver.NewRouter(newSessionStore())
		router := appserver.NewRuntimeRouter(appserver.RuntimeServices{ThreadRouter: threadRouter, SteerMailbox: turn.NewSteerMailbox()})
		defer router.Close()
		params, err := json.Marshal(appserver.ThreadApproveGuardianDeniedActionParams{ThreadID: threadID, Event: entry.Event})
		if err != nil {
			return err
		}
		response := router.Handle(&appserver.Request{JSONRPC: "2.0", ID: appserver.IntID(1), Method: appserver.MethodThreadApproveGuardianDeniedAction, Params: params})
		if response.Error != nil {
			return errors.New(response.Error.Message)
		}
		return nil
	}
}

func interactiveTurnCommand(ctx context.Context, root *cli.RootOptions, runner interactiveTurnRunner, state *codextui.State, prompt string, approvalBroker *interactiveApprovalBroker, elicitationBroker *interactiveElicitationBroker, userInputBroker *interactiveUserInputBroker) bubbletea.Cmd {
	return interactiveTurnCommandWithRequest(ctx, root, runner, state, codextea.SubmitRequest{Prompt: prompt}, approvalBroker, elicitationBroker, userInputBroker)
}

func interactiveTurnCommandWithRequest(ctx context.Context, root *cli.RootOptions, runner interactiveTurnRunner, state *codextui.State, request codextea.SubmitRequest, approvalBroker *interactiveApprovalBroker, elicitationBroker *interactiveElicitationBroker, userInputBroker *interactiveUserInputBroker, interrupts ...*interactiveInterruptController) bubbletea.Cmd {
	turnState := cloneInteractiveTurnState(state)
	threadID := ""
	if turnState != nil {
		threadID = strings.TrimSpace(turnState.ThreadID)
	}
	return func() bubbletea.Msg {
		messages := make(chan bubbletea.Msg, 256)
		turnCtx := ctx
		var done func()
		if len(interrupts) > 0 && interrupts[0] != nil {
			turnCtx, done = interrupts[0].begin(ctx)
		}
		go func() {
			if done != nil {
				defer done()
			}
			runInteractiveTurn(turnCtx, root, runner, turnState, request, threadID, messages, approvalBroker, elicitationBroker, userInputBroker)
		}()
		return codextea.StreamStartedMsg{Messages: messages}
	}
}

func runInteractiveTurn(ctx context.Context, root *cli.RootOptions, runner interactiveTurnRunner, state *codextui.State, request codextea.SubmitRequest, requestedThreadID string, messages chan<- bubbletea.Msg, approvalBroker *interactiveApprovalBroker, elicitationBroker *interactiveElicitationBroker, userInputBroker *interactiveUserInputBroker) {
	defer close(messages)
	send := func(message bubbletea.Msg) {
		select {
		case messages <- message:
		case <-ctx.Done():
		}
	}
	sendAfterCancel := func(message bubbletea.Msg) {
		select {
		case messages <- message:
		default:
		}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if root == nil {
		root = &cli.RootOptions{}
	}
	if runner == nil {
		send(codextea.TurnCompletedMsg{ThreadID: requestedThreadID, Err: errors.New("interactive runner is nil")})
		return
	}
	if request.CollaborationMode == nil {
		request.CollaborationMode = interactiveCollaborationModeFromState(state)
	}
	if concrete, ok := runner.(*codexexec.Runner); ok && approvalBroker != nil {
		previousApproval := concrete.ShellApproval
		concrete.ShellApproval = approvalBroker.shellApprovalFunc(send)
		defer func() {
			concrete.ShellApproval = previousApproval
		}()
	}
	if concrete, ok := runner.(*codexexec.Runner); ok && elicitationBroker != nil {
		previousElicitation := concrete.MCPElicitation
		concrete.MCPElicitation = elicitationBroker.mcpElicitationFunc(send)
		defer func() {
			concrete.MCPElicitation = previousElicitation
		}()
	}
	if concrete, ok := runner.(*codexexec.Runner); ok && userInputBroker != nil {
		previousUserInput := concrete.UserInput
		concrete.UserInput = userInputBroker.userInputResponder(send)
		defer func() {
			concrete.UserInput = previousUserInput
		}()
	}
	inputs := interactiveSubmitInputs(request)
	prompt := strings.TrimSpace(request.Prompt)
	turnRoot := *root
	turnRoot.Shared = interactiveSharedOptionsFromState(turnRoot.Shared, state)
	if state != nil {
		if tier := strings.TrimSpace(state.ServiceTier); tier != "" {
			turnRoot.ConfigOverrides = append(append([]string(nil), turnRoot.ConfigOverrides...), "service_tier="+tier)
		}
	}
	if state != nil && strings.TrimSpace(state.Personality) != "" {
		turnRoot.ConfigOverrides = append(append([]string(nil), turnRoot.ConfigOverrides...), "personality="+strings.TrimSpace(state.Personality))
	}
	additionalInstructions := ""
	var additionalInputItems []any
	if _, ok := runner.(*codexexec.Runner); ok {
		var skillErr error
		additionalInstructions, additionalInputItems, skillErr = interactiveLocalSkillContextForRequest(root, request, inputs, turnRoot.Shared.CWD)
		if skillErr != nil {
			send(codextea.TurnCompletedMsg{Err: skillErr})
			return
		}
	}
	additionalInstructions = strings.Join(nonEmptyStringsApp([]string{
		request.AdditionalInstructions,
		interactiveCollaborationModeInstructions(request.CollaborationMode),
		additionalInstructions,
	}), "\n\n")
	if request.IDEContext != nil {
		if prompt != "" {
			inputs = append([]turn.TurnUserInput{{Type: "text", Text: prompt}}, inputs...)
			prompt = ""
		}
		idecontext.ApplyIDEContextToUserInput(request.IDEContext, &inputs)
	} else if len(inputs) > 0 && prompt != "" {
		inputs = append(inputs, turn.TurnUserInput{Type: "text", Text: prompt})
		prompt = ""
	}
	execOpts := cli.ExecOptions{
		Prompt:                strings.TrimSpace(prompt),
		Shared:                turnRoot.Shared,
		Color:                 "auto",
		JSON:                  true,
		StreamAssistantDeltas: true,
	}
	if state != nil && strings.TrimSpace(state.ThreadID) != "" {
		execOpts.Subcommand = "resume"
		execOpts.Resume = cli.ExecResumeOptions{
			SessionID: state.ThreadID,
			Prompt:    strings.TrimSpace(prompt),
		}
		execOpts.Prompt = ""
	}
	streamWriter := newInteractiveStreamEventWriter(send, interactivePlanMode(request.CollaborationMode))
	streamWriter.threadID = strings.TrimSpace(requestedThreadID)
	streamOutput := io.Writer(streamWriter)
	var internalEventHandler func(protocol.ThreadEvent)
	if _, ok := runner.(*codexexec.Runner); ok {
		streamOutput = io.Discard
		internalEventHandler = streamWriter.HandleEvent
	}
	result, err := runner.RunContext(ctx, &codexexec.Request{
		Root:                   turnRoot,
		Exec:                   execOpts,
		Input:                  inputs,
		CollaborationMode:      interactiveCollaborationModePayload(request.CollaborationMode),
		AdditionalInstructions: additionalInstructions,
		AdditionalInputItems:   additionalInputItems,
		InternalEventHandler:   internalEventHandler,
	}, strings.NewReader(""), streamOutput, io.Discard)
	streamWriter.Flush()
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil {
			sendAfterCancel(codextea.TurnInterruptedMsg{ThreadID: requestedThreadID, Err: ctx.Err()})
			return
		}
		send(codextea.TurnCompletedMsg{ThreadID: requestedThreadID, Err: err})
		return
	}
	msg := codextea.TurnCompletedMsg{ThreadID: requestedThreadID}
	if result != nil {
		if strings.TrimSpace(msg.ThreadID) == "" {
			msg.ThreadID = result.ThreadID
		}
		if result.TokenUsage != nil && !streamWriter.SawTokenUsage() {
			streamWriter.HandleEvent(protocol.TokenUsageUpdated(*result.TokenUsage))
		}
		if !streamWriter.SawAssistantOutput() {
			msg.AssistantMessage = result.LastMessage
		}
	}
	send(msg)
}

func interactiveSubmitInputs(request codextea.SubmitRequest) []turn.TurnUserInput {
	inputs := make([]turn.TurnUserInput, 0, len(request.Attachments)+len(request.MentionBindings))
	for _, attachment := range request.Attachments {
		switch attachment.Kind {
		case bottompane.AttachmentImage:
			if path := strings.TrimSpace(attachment.Path); path != "" {
				inputs = append(inputs, turn.TurnUserInput{Type: "localImage", Path: path})
			}
		case bottompane.AttachmentRemoteImage:
			if url := strings.TrimSpace(attachment.URL); url != "" {
				inputs = append(inputs, turn.TurnUserInput{Type: "image", URL: url})
			}
		default:
			if path := strings.TrimSpace(attachment.Path); path != "" {
				inputs = append(inputs, turn.TurnUserInput{Type: "text", Text: "Attached file: " + path})
			}
		}
	}
	for _, input := range interactiveSkillInputs(request) {
		inputs = append(inputs, input)
	}
	return inputs
}

func interactiveSkillInputs(request codextea.SubmitRequest) []turn.TurnUserInput {
	message := chatwidget.UserMessage{
		Text:            strings.TrimSpace(request.Prompt),
		MentionBindings: append([]string(nil), request.MentionBindings...),
	}
	decision := chatwidget.DecideUserMessageSubmission(message, chatwidget.UserMessageTextHistoryRecord(), chatwidget.SubmissionOptions{
		SessionConfigured:     true,
		CurrentModelHasImages: true,
		MentionCatalog:        request.MentionCatalog,
	})
	inputs := make([]turn.TurnUserInput, 0)
	for _, item := range decision.Items {
		if item.Kind != chatwidget.SubmittedInputSkill {
			continue
		}
		if strings.TrimSpace(item.Name) == "" || strings.TrimSpace(item.Path) == "" {
			continue
		}
		inputs = append(inputs, turn.TurnUserInput{
			Type: "skill",
			Name: strings.TrimSpace(item.Name),
			Path: strings.TrimSpace(item.Path),
		})
	}
	return inputs
}

func interactiveLocalSkillContextForRequest(root *cli.RootOptions, request codextea.SubmitRequest, inputs []turn.TurnUserInput, cwd string) (string, []any, error) {
	codexHome := auth.DefaultCodexHome()
	service := appserver.NewSkillsServiceWithOptions(&appserver.SkillsServiceOptions{
		Config:              interactiveConfigService(root),
		CodexHome:           codexHome,
		IncludeDefaultRoots: true,
	})
	params := &appserver.SkillsListParams{}
	if strings.TrimSpace(cwd) != "" {
		params.CWDs = []string{strings.TrimSpace(cwd)}
	}
	response, err := service.List(params)
	if err != nil || response == nil {
		return "", nil, err
	}
	skills := interactivePromptSkillMetadataFromEntries(response.Skills)
	available := promptctx.RenderAvailableSkills(skills, promptctx.DefaultSkillMetadataBudget(0))
	instructions := ""
	if available != nil {
		instructions = strings.TrimSpace(available.Body)
	}
	selected := promptctx.CollectExplicitSkillMentions(&promptctx.ExplicitSkillMentionOptions{
		Inputs: interactiveSkillMentionInputs(request, inputs),
		Skills: skills,
	})
	inputItems := make([]any, 0, len(selected))
	for _, skill := range selected {
		if item := interactiveSkillInstructionsInputItem(skill); item != nil {
			inputItems = append(inputItems, item)
		}
	}
	return instructions, inputItems, nil
}

func interactiveSkillMentionInputs(request codextea.SubmitRequest, inputs []turn.TurnUserInput) []promptctx.SkillMentionInput {
	out := make([]promptctx.SkillMentionInput, 0, len(inputs)+1)
	if prompt := strings.TrimSpace(request.Prompt); prompt != "" {
		out = append(out, promptctx.SkillMentionInput{Type: "text", Text: prompt})
	}
	for _, input := range inputs {
		out = append(out, promptctx.SkillMentionInput{
			Type: input.Type,
			Text: input.Text,
			Name: input.Name,
			Path: input.Path,
		})
	}
	return out
}

func interactivePromptSkillMetadataFromEntries(entries []appserver.SkillsListEntry) []promptctx.InstructionsSkillMetadata {
	out := make([]promptctx.InstructionsSkillMetadata, 0, len(entries))
	var walk func([]appserver.SkillsListEntry)
	walk = func(values []appserver.SkillsListEntry) {
		for _, entry := range values {
			if len(entry.Skills) > 0 {
				walk(entry.Skills)
				continue
			}
			if !entry.Enabled || strings.TrimSpace(entry.Name) == "" {
				continue
			}
			description := firstNonEmptyLocal(entry.ShortDescription, entry.Description)
			var allowImplicit *bool
			if entry.Policy != nil && entry.Policy.AllowImplicitInvocation != nil {
				value := *entry.Policy.AllowImplicitInvocation
				allowImplicit = &value
			}
			out = append(out, promptctx.InstructionsSkillMetadata{
				Name:                    strings.TrimSpace(entry.Name),
				Scope:                   strings.TrimSpace(entry.Scope),
				Path:                    strings.TrimSpace(entry.Path),
				Description:             strings.TrimSpace(description),
				PluginID:                strings.TrimSpace(entry.PluginID),
				RemotePluginID:          strings.TrimSpace(entry.RemotePluginID),
				Contents:                entry.Contents,
				AllowImplicitInvocation: allowImplicit,
			})
		}
	}
	walk(entries)
	return out
}

func interactiveSkillInstructionsInputItem(skill promptctx.InstructionsSkillMetadata) any {
	contents := skill.Contents
	if strings.TrimSpace(contents) == "" {
		data, err := os.ReadFile(skill.Path)
		if err != nil || strings.TrimSpace(string(data)) == "" {
			return nil
		}
		contents = string(data)
	}
	renderPath := firstNonEmptyLocal(skill.LocatorPath, skill.Path)
	name, renderPath, contents, _ := promptctx.TruncateSkillInstructionFields(skill.Name, renderPath, contents)
	rendered := contextfrag.Render(contextfrag.NewSkillInstructions(name, renderPath, contents))
	if rendered == nil || strings.TrimSpace(rendered.Content) == "" {
		return nil
	}
	role := strings.TrimSpace(rendered.Role)
	if role == "" {
		role = contextfrag.RoleUser
	}
	return map[string]any{
		"type": "message",
		"role": role,
		"content": []map[string]any{{
			"type": "input_text",
			"text": rendered.Content,
		}},
	}
}

type interactiveStreamEventWriter struct {
	mu                 sync.Mutex
	buffer             []byte
	send               func(bubbletea.Msg)
	sawAssistantOutput bool
	sawTokenUsage      bool
	planMode           bool
	planStreams        map[string]*interactivePlanStreamState
	threadID           string
}

type interactivePlanStreamState struct {
	parser      *appserver.ProposedPlanStreamParser
	planItemID  string
	planText    strings.Builder
	planStarted bool
	sawDelta    bool
}

func newInteractiveStreamEventWriter(send func(bubbletea.Msg), planMode ...bool) *interactiveStreamEventWriter {
	enabled := len(planMode) > 0 && planMode[0]
	return &interactiveStreamEventWriter{send: send, planMode: enabled, planStreams: map[string]*interactivePlanStreamState{}}
}

func (w *interactiveStreamEventWriter) Write(data []byte) (int, error) {
	if w == nil {
		return len(data), nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buffer = append(w.buffer, data...)
	for {
		index := bytes.IndexByte(w.buffer, '\n')
		if index < 0 {
			break
		}
		line := bytes.TrimSpace(w.buffer[:index])
		w.buffer = append([]byte(nil), w.buffer[index+1:]...)
		w.emitLine(line)
	}
	return len(data), nil
}

func (w *interactiveStreamEventWriter) Flush() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	line := bytes.TrimSpace(w.buffer)
	w.buffer = nil
	w.emitLine(line)
}

func (w *interactiveStreamEventWriter) SawAssistantOutput() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sawAssistantOutput
}

func (w *interactiveStreamEventWriter) SawTokenUsage() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.sawTokenUsage
}

func (w *interactiveStreamEventWriter) emitLine(line []byte) {
	if len(line) == 0 || w.send == nil {
		return
	}
	var event protocol.ThreadEvent
	if err := json.Unmarshal(line, &event); err != nil {
		w.sendEvent(protocol.ErrorEvent("failed to parse stream event: " + err.Error()))
		return
	}
	w.handleEventLocked(event)
}

func (w *interactiveStreamEventWriter) HandleEvent(event protocol.ThreadEvent) {
	if w == nil || w.send == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	w.handleEventLocked(event)
}

func (w *interactiveStreamEventWriter) handleEventLocked(event protocol.ThreadEvent) {
	if w.planMode && w.handlePlanModeEventLocked(event) {
		return
	}
	if interactiveThreadEventHasAssistantOutput(event) {
		w.sawAssistantOutput = true
	}
	if event.Type == "thread.token_usage.updated" && event.TokenUsage != nil {
		w.sawTokenUsage = true
	}
	w.sendEvent(event)
}

func (w *interactiveStreamEventWriter) handlePlanModeEventLocked(event protocol.ThreadEvent) bool {
	if event.Type == "item.delta" && event.Delta != nil && event.Delta.Text != "" {
		state := w.planStreamState(event.Delta.ItemID)
		state.sawDelta = true
		w.emitPlanSegmentsLocked(event.Delta.ItemID, state, state.parser.Push(event.Delta.Text))
		return true
	}
	if event.Type != "item.completed" || event.Item == nil || event.Item.Type != "agent_message" {
		return false
	}
	itemID := strings.TrimSpace(event.Item.ID)
	state := w.planStreamState(itemID)
	if !state.sawDelta && event.Item.Text != "" {
		w.emitPlanSegmentsLocked(itemID, state, state.parser.Push(event.Item.Text))
	}
	w.emitPlanSegmentsLocked(itemID, state, state.parser.Finish())
	if state.planStarted {
		w.sendEvent(protocol.ItemCompleted(protocol.PlanItem(state.planItemID, state.planText.String())))
	}
	delete(w.planStreams, itemID)
	return true
}

func (w *interactiveStreamEventWriter) planStreamState(itemID string) *interactivePlanStreamState {
	itemID = strings.TrimSpace(itemID)
	if state := w.planStreams[itemID]; state != nil {
		return state
	}
	planItemID := itemID + "-plan"
	if itemID == "" {
		planItemID = "proposed-plan"
	}
	state := &interactivePlanStreamState{parser: appserver.NewProposedPlanStreamParser(), planItemID: planItemID}
	w.planStreams[itemID] = state
	return state
}

func (w *interactiveStreamEventWriter) emitPlanSegmentsLocked(itemID string, state *interactivePlanStreamState, segments []appserver.ProposedPlanSegment) {
	for _, segment := range segments {
		switch segment.Kind {
		case appserver.ProposedPlanSegmentNormal:
			if segment.Text != "" {
				w.sawAssistantOutput = true
				w.sendEvent(protocol.AgentMessageDelta(itemID, segment.Text))
			}
		case appserver.ProposedPlanSegmentStart:
			if !state.planStarted {
				state.planStarted = true
				w.sawAssistantOutput = true
				w.sendEvent(protocol.ItemStarted(protocol.PlanItem(state.planItemID, "")))
			}
		case appserver.ProposedPlanSegmentDelta:
			if segment.Text != "" {
				state.planText.WriteString(segment.Text)
				w.sawAssistantOutput = true
				w.sendEvent(protocol.PlanDelta(state.planItemID, segment.Text))
			}
		}
	}
}

func (w *interactiveStreamEventWriter) sendEvent(event protocol.ThreadEvent) {
	if w == nil || w.send == nil {
		return
	}
	if threadID := strings.TrimSpace(w.threadID); threadID != "" {
		w.send(codextea.ThreadScopedEventMsg{ThreadID: threadID, Event: event})
		return
	}
	w.send(codextea.ThreadEventMsg{Event: event})
}

func cloneInteractiveTurnState(state *codextui.State) *codextui.State {
	if state == nil {
		return nil
	}
	cloned := *state
	cloned.Messages = append([]codextui.Message(nil), state.Messages...)
	cloned.RateLimits = append([]codextui.RateLimitStatus(nil), state.RateLimits...)
	if state.ModelContextWindow != nil {
		value := *state.ModelContextWindow
		cloned.ModelContextWindow = &value
	}
	return &cloned
}

func interactiveThreadEventHasAssistantOutput(event protocol.ThreadEvent) bool {
	switch event.Type {
	case "item.delta":
		return event.Delta != nil && strings.TrimSpace(event.Delta.Text) != ""
	case "item.completed":
		return event.Item != nil && event.Item.Type == "agent_message" && strings.TrimSpace(event.Item.Text) != ""
	default:
		return false
	}
}

func interactiveSharedOptionsFromState(base cli.SharedOptions, state *codextui.State) cli.SharedOptions {
	if state == nil {
		return base
	}
	base.Model = state.Model
	base.ModelReasoningEffort = state.EffectiveReasoningEffort()
	base.ApprovalPolicy = state.ApprovalPolicy
	base.Sandbox = state.Sandbox
	if cwd := strings.TrimSpace(state.CWD); cwd != "" {
		base.CWD = cwd
	}
	base.Search = state.Search
	base.NoAltScreen = state.NoAltScreen
	return base
}

func interactiveCollaborationModePayload(mode *chatwidget.CollaborationMode) map[string]any {
	if mode == nil {
		return nil
	}
	settings := map[string]any{
		"model":                  strings.TrimSpace(mode.Settings.Model),
		"reasoning_effort":       nil,
		"developer_instructions": nil,
	}
	if mode.Settings.ReasoningEffort != nil {
		settings["reasoning_effort"] = strings.TrimSpace(*mode.Settings.ReasoningEffort)
	}
	if mode.Settings.DeveloperInstructions != nil {
		settings["developer_instructions"] = strings.TrimSpace(*mode.Settings.DeveloperInstructions)
	}
	return map[string]any{
		"mode":     string(mode.Mode),
		"settings": settings,
	}
}

func interactiveCollaborationModeFromState(state *codextui.State) *chatwidget.CollaborationMode {
	if state == nil {
		return nil
	}
	kind := chatwidget.CollaborationModeKindDefault
	if state.PlanMode {
		kind = chatwidget.CollaborationModeKindPlan
	}
	mode := chatwidget.NewCollaborationMode(
		kind,
		state.Model,
		state.EffectiveReasoningEffort(),
		chatwidget.CollaborationModeInstructions(kind),
	)
	return &mode
}

func interactiveCollaborationModeInstructions(mode *chatwidget.CollaborationMode) string {
	if mode == nil || mode.Settings.DeveloperInstructions == nil {
		return ""
	}
	instructions := strings.TrimSpace(*mode.Settings.DeveloperInstructions)
	if instructions == "" {
		return ""
	}
	return "<collaboration_mode>\n" + instructions + "\n</collaboration_mode>"
}

func interactivePlanMode(mode *chatwidget.CollaborationMode) bool {
	return mode != nil && mode.Mode == chatwidget.CollaborationModeKindPlan
}

func nonEmptyStringsApp(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func interactiveUIState(root *cli.RootOptions) *codextui.State {
	options := &codextui.Options{}
	if root != nil {
		options.Model = strings.TrimSpace(root.Shared.Model)
		options.ReasoningEffort = strings.TrimSpace(root.Shared.ModelReasoningEffort)
		options.ApprovalPolicy = strings.TrimSpace(root.Shared.ApprovalPolicy)
		options.Sandbox = strings.TrimSpace(root.Shared.Sandbox)
		options.Search = root.Shared.Search
		options.NoAltScreen = root.Shared.NoAltScreen
	}
	if loaded, err := config.LoadEffectiveWithOptions(auth.DefaultCodexHome(), interactiveKeymapLoadOptions(root)); err == nil && loaded != nil {
		values := loaded.Values
		options.Model = firstNonEmptyLocal(options.Model, interactiveStringFromConfig(values, "model"))
		options.ReasoningEffort = firstNonEmptyLocal(options.ReasoningEffort, interactiveStringFromConfig(values, "model_reasoning_effort"))
		options.Provider = firstNonEmptyLocal(options.Provider, interactiveStringFromConfig(values, "model_provider"))
		options.ApprovalPolicy = firstNonEmptyLocal(options.ApprovalPolicy, interactiveStringFromConfig(values, "approval_policy"))
		options.Sandbox = firstNonEmptyLocal(options.Sandbox, interactiveStringFromConfig(values, "sandbox_mode"))
		options.ServiceTier = firstNonEmptyLocal(options.ServiceTier, interactiveStringFromConfig(values, "service_tier"))
	}
	options.Model = firstNonEmptyLocal(options.Model, interactiveDefaultModel())
	options.CWD = interactiveSessionPickerCWD(root)
	return codextui.NewState(options)
}

func interactiveStringFromConfig(values map[string]any, key string) string {
	if values == nil {
		return ""
	}
	value, _ := values[key].(string)
	return strings.TrimSpace(value)
}

func interactiveDefaultModel() string {
	manager := modelpkg.NewStaticModelsManager(modelpkg.BundledModelsResponse())
	return strings.TrimSpace(manager.GetDefaultModel("", true, modelpkg.RefreshOffline))
}

func interactiveServiceTierCommands(modelID string) []bottompane.ServiceTierCommand {
	manager := modelpkg.NewStaticModelsManager(modelpkg.BundledModelsResponse())
	info := manager.GetModelInfo(strings.TrimSpace(modelID), nil)
	commands := make([]bottompane.ServiceTierCommand, 0, len(info.ServiceTiers))
	for _, id := range info.ServiceTiers {
		id = strings.TrimSpace(id)
		if id == "" || id == modelpkg.ServiceTierDefaultRequestValue {
			continue
		}
		name := id
		if id == chatwidget.ServiceTierFastRequestValue {
			name = "fast"
		}
		commands = append(commands, bottompane.ServiceTierCommand{ID: id, Name: name, Description: "Fastest inference with increased plan usage"})
	}
	return commands
}

func interactiveFatalExit(stderr io.Writer, message string) error {
	if stderr == nil {
		stderr = io.Discard
	}
	fmt.Fprintf(stderr, "ERROR: %s\n", message)
	return silentExitCode(1)
}

const interactiveDumbTerminalNoTTYMessage = "TERM is set to \"dumb\". Refusing to start the interactive TUI because no terminal is available for a confirmation prompt (stdin/stderr is not a TTY). Run in a supported terminal or unset TERM."
const interactiveDumbTerminalRefusedMessage = "Refusing to start the interactive TUI because TERM is set to \"dumb\". Run in a supported terminal or unset TERM."

func guardInteractiveDumbTerminal(stdin io.Reader, stderr io.Writer, term string) error {
	if strings.TrimSpace(term) != "dumb" {
		return nil
	}
	if !isSessionTerminal(stdin) || !isSessionTerminal(stderr) {
		return errors.New(interactiveDumbTerminalNoTTYMessage)
	}
	if stderr == nil {
		stderr = io.Discard
	}
	fmt.Fprintln(stderr, "WARNING: TERM is set to \"dumb\". Codex's interactive TUI may not work in this terminal.")
	fmt.Fprintln(stderr, "Continue anyway? [y/N]: ")
	reader := bufio.NewReader(stdin)
	answer, err := reader.ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	answer = strings.TrimSpace(answer)
	if strings.EqualFold(answer, "y") || strings.EqualFold(answer, "yes") {
		return nil
	}
	return errors.New(interactiveDumbTerminalRefusedMessage)
}

func runInteractivePrompt(ctx context.Context, root *cli.RootOptions, stdin io.Reader, stdout, stderr io.Writer) error {
	execOpts := cli.ExecOptions{
		Prompt: root.Prompt,
		Shared: root.Shared,
		Color:  "auto",
	}
	_, err := newCodexExecRunner(auth.DefaultCodexHome()).RunContext(ctx, &codexexec.Request{
		Root: *root,
		Exec: execOpts,
	}, stdin, stdout, stderr)
	return err
}

func normalizeInteractivePrompt(prompt string) string {
	if prompt == "" {
		return ""
	}
	prompt = strings.ReplaceAll(prompt, "\r\n", "\n")
	return strings.ReplaceAll(prompt, "\r", "\n")
}

func resolveInteractiveRemoteEndpoint(root *cli.RootOptions) (*appserverdaemon.RemoteAppServerEndpoint, error) {
	if root == nil {
		return nil, nil
	}
	var endpoint *appserverdaemon.RemoteAppServerEndpoint
	if strings.TrimSpace(root.Remote) != "" {
		parsed, err := resolveInteractiveRemoteAddress(root.Remote)
		if err != nil {
			return nil, err
		}
		endpoint = parsed
	}
	if strings.TrimSpace(root.RemoteAuthEnv) == "" {
		return endpoint, nil
	}
	if endpoint == nil {
		return nil, errors.New("`--remote-auth-token-env` requires `--remote`.")
	}
	if endpoint.Kind != appserverdaemon.RemoteEndpointWebSocket || !appserverdaemon.WebSocketURLSupportsAuthToken(endpoint.WebSocketURL) {
		return nil, errors.New("`--remote-auth-token-env` requires a `wss://` or loopback `ws://` remote.")
	}
	authToken, err := readRemoteAuthTokenFromEnvVar(root.RemoteAuthEnv)
	if err != nil {
		return nil, err
	}
	endpoint.AuthToken = &authToken
	return endpoint, nil
}

func resolveInteractiveRemoteAddress(addr string) (*appserverdaemon.RemoteAppServerEndpoint, error) {
	if strings.HasPrefix(addr, "unix://") {
		socketPath := strings.TrimPrefix(addr, "unix://")
		if socketPath == "" {
			return appserverdaemon.NewUnixSocketEndpoint(appserver.AppServerControlSocketPath(auth.DefaultCodexHome())), nil
		}
		abs, err := absoluteInteractiveRemoteSocketPath(socketPath)
		if err != nil {
			return nil, err
		}
		return appserverdaemon.NewUnixSocketEndpoint(abs), nil
	}

	parsed, err := url.Parse(addr)
	if err != nil || !isValidInteractiveRemoteWebSocketURL(parsed) {
		return nil, invalidInteractiveRemoteAddress(addr)
	}
	return appserverdaemon.NewWebSocketEndpoint(normalizeInteractiveRemoteWebSocketURL(parsed), nil), nil
}

func absoluteInteractiveRemoteSocketPath(path string) (string, error) {
	clean := filepath.Clean(path)
	if filepath.IsAbs(clean) {
		return clean, nil
	}
	abs, err := filepath.Abs(clean)
	if err != nil {
		return "", err
	}
	return filepath.Clean(abs), nil
}

func isValidInteractiveRemoteWebSocketURL(parsed *url.URL) bool {
	if parsed == nil {
		return false
	}
	if parsed.Scheme != "ws" && parsed.Scheme != "wss" {
		return false
	}
	if parsed.Hostname() == "" || parsed.Port() == "" {
		return false
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return false
	}
	return parsed.RawQuery == "" && parsed.Fragment == ""
}

func normalizeInteractiveRemoteWebSocketURL(parsed *url.URL) string {
	host := parsed.Hostname()
	port := parsed.Port()
	normalizedHost := net.JoinHostPort(host, port)
	if isDefaultWebSocketPort(parsed.Scheme, port) {
		normalizedHost = host
		if strings.Contains(host, ":") {
			normalizedHost = "[" + host + "]"
		}
	}
	return (&url.URL{
		Scheme: parsed.Scheme,
		Host:   normalizedHost,
		Path:   "/",
	}).String()
}

func isDefaultWebSocketPort(scheme string, port string) bool {
	return (scheme == "ws" && port == "80") || (scheme == "wss" && port == "443")
}

func invalidInteractiveRemoteAddress(addr string) error {
	return fmt.Errorf("invalid remote address `%s`; %s", addr, remoteAddressUsage)
}

func readRemoteAuthTokenFromEnvVar(envVarName string) (string, error) {
	return readRemoteAuthTokenFromEnvVarWith(envVarName, os.LookupEnv)
}

func readRemoteAuthTokenFromEnvVarWith(envVarName string, getVar func(string) (string, bool)) (string, error) {
	raw, ok := getVar(envVarName)
	if !ok {
		return "", fmt.Errorf("environment variable `%s` is not set", envVarName)
	}
	authToken := strings.TrimSpace(raw)
	if authToken == "" {
		return "", fmt.Errorf("environment variable `%s` is empty", envVarName)
	}
	return authToken, nil
}

func (s *interactiveSession) Run(ctx context.Context) error {
	if s == nil {
		return errors.New("interactive session is nil")
	}
	if s.Runner == nil {
		s.Runner = newCodexExecRunner(auth.DefaultCodexHome())
	}
	if s.Reader == nil {
		return errors.New("interactive reader is nil")
	}
	if s.Stdout == nil {
		s.Stdout = io.Discard
	}
	if s.Stderr == nil {
		s.Stderr = io.Discard
	}
	if s.UI == nil {
		root := s.Root
		s.UI = interactiveUIState(&root)
	}
	if _, err := fmt.Fprint(s.Stdout, s.UI.RenderWelcome()); err != nil {
		return err
	}
	for {
		if _, err := fmt.Fprint(s.Stdout, s.UI.RenderPrompt()); err != nil {
			return err
		}
		if !s.Reader.Scan() {
			break
		}
		input := strings.TrimSpace(s.Reader.Text())
		if input == "" {
			continue
		}
		handled, exit, err := s.HandleCommand(input)
		if err != nil {
			return err
		}
		if exit {
			break
		}
		if handled {
			continue
		}
		if err := s.RunTurn(ctx, input); err != nil {
			return err
		}
	}
	if err := s.Reader.Err(); err != nil {
		return err
	}
	return nil
}

func (s *interactiveSession) HandleCommand(input string) (handled bool, exit bool, err error) {
	invocation, ok := codextui.ParseCommand(input)
	if !ok {
		return false, false, nil
	}
	if s.UI == nil {
		root := s.Root
		s.UI = interactiveUIState(&root)
	}
	switch invocation.Command {
	case codextui.CommandExit:
		return true, true, nil
	case codextui.CommandHelp:
		_, err = fmt.Fprint(s.Stdout, s.UI.RenderHelp())
	case codextui.CommandKeymap:
		err = s.handleKeymapCommand(invocation.Args)
	case codextui.CommandStatus:
		_, err = fmt.Fprintln(s.Stdout, s.UI.RenderStatusLine())
	case codextui.CommandNew:
		s.ThreadID = ""
		s.UI.ResetThread()
		_, err = fmt.Fprintln(s.Stdout, "Started a new local thread.")
	case codextui.CommandClear:
		s.ThreadID = ""
		s.UI.ResetThread()
		_, err = fmt.Fprintln(s.Stdout, "Started a fresh session.")
	case codextui.CommandModel:
		err = s.handleModelCommand(invocation.Args)
	case codextui.CommandApproval:
		err = s.handleApprovalCommand(invocation.Args)
	case codextui.CommandSandbox:
		err = s.handleSandboxCommand(invocation.Args)
	case codextui.CommandPermissions:
		err = s.handlePermissionsCommand(invocation.Args)
	case codextui.CommandPersonality:
		err = s.handlePersonalityCommand(invocation.Args)
	case codextui.CommandExperimental:
		err = s.handleExperimentalCommand(invocation.Args)
	default:
		_, err = fmt.Fprintf(s.Stdout, "Unknown command %s. Type /help for commands.\n", invocation.Name)
	}
	return true, false, err
}

func (s *interactiveSession) handleKeymapCommand(args string) error {
	current := interactiveKeymapConfig(&s.Root)
	handler := interactiveKeymapEditHandler(&s.Root)
	result, err := codextui.HandleKeymapCommand(args, current, func(edit codextui.KeymapEdit) (*codextui.KeymapConfig, string, error) {
		return handler(edit)
	})
	if err != nil {
		_, writeErr := fmt.Fprintln(s.Stdout, "Keymap:", err)
		if writeErr != nil {
			return writeErr
		}
		return nil
	}
	_, err = fmt.Fprintln(s.Stdout, result.Text)
	return err
}

func (s *interactiveSession) handleModelCommand(args string) error {
	if strings.TrimSpace(args) != "" {
		value := strings.TrimSpace(args)
		s.Root.Shared.Model = value
		s.UI.Model = value
	}
	_, err := fmt.Fprint(s.Stdout, s.UI.RenderSetting("Model", s.UI.Model))
	return err
}

func (s *interactiveSession) handleApprovalCommand(args string) error {
	if strings.TrimSpace(args) != "" {
		value := strings.TrimSpace(args)
		if !codextui.ValidApprovalPolicy(value) {
			_, err := fmt.Fprintln(s.Stdout, "Approval must be one of untrusted, on-request, never.")
			return err
		}
		s.Root.Shared.ApprovalPolicy = value
		s.UI.ApprovalPolicy = value
	}
	_, err := fmt.Fprint(s.Stdout, s.UI.RenderSetting("Approval", s.UI.ApprovalPolicy))
	return err
}

func (s *interactiveSession) handleSandboxCommand(args string) error {
	if strings.TrimSpace(args) != "" {
		value := strings.TrimSpace(args)
		s.Root.Shared.Sandbox = value
		s.UI.Sandbox = value
	}
	_, err := fmt.Fprint(s.Stdout, s.UI.RenderSetting("Sandbox", s.UI.Sandbox))
	return err
}

func (s *interactiveSession) handlePermissionsCommand(args string) error {
	value := strings.ToLower(strings.TrimSpace(args))
	switch value {
	case "", "show", "status":
	case "read-only", ":read-only":
		s.Root.Shared.ApprovalPolicy = string(chatwidget.ApprovalOnRequest)
		s.Root.Shared.Sandbox = chatwidget.ReadOnlyProfile
		s.UI.ApprovalPolicy = string(chatwidget.ApprovalOnRequest)
		s.UI.Sandbox = chatwidget.ReadOnlyProfile
	case "default", "auto", "workspace", ":workspace":
		s.Root.Shared.ApprovalPolicy = string(chatwidget.ApprovalOnRequest)
		s.Root.Shared.Sandbox = chatwidget.WorkspaceProfile
		s.UI.ApprovalPolicy = string(chatwidget.ApprovalOnRequest)
		s.UI.Sandbox = chatwidget.WorkspaceProfile
	case "full-access", ":danger-full-access":
		s.Root.Shared.ApprovalPolicy = string(chatwidget.ApprovalNever)
		s.Root.Shared.Sandbox = chatwidget.DangerFullAccessProfile
		s.UI.ApprovalPolicy = string(chatwidget.ApprovalNever)
		s.UI.Sandbox = chatwidget.DangerFullAccessProfile
	default:
		_, err := fmt.Fprintln(s.Stdout, "Permissions must be one of read-only, default, full-access.")
		return err
	}
	_, err := fmt.Fprintf(s.Stdout, "Permissions: approval=%s sandbox=%s\n", displayInteractiveValue(s.UI.ApprovalPolicy), displayInteractiveValue(s.UI.Sandbox))
	return err
}

func (s *interactiveSession) handlePersonalityCommand(args string) error {
	value := strings.ToLower(strings.TrimSpace(args))
	if value == "" || value == "show" || value == "status" {
		current := s.UI.Personality
		if strings.TrimSpace(current) == "" {
			settings := interactiveTUISettings(&s.Root)
			current = string(settings.Personality)
		}
		_, err := fmt.Fprint(s.Stdout, s.UI.RenderSetting("Personality", current))
		return err
	}
	personality, ok := interactiveParsePersonality(value)
	if !ok {
		_, err := fmt.Fprintln(s.Stdout, "Personality must be one of friendly, pragmatic, none.")
		return err
	}
	_, err := interactiveSettingsWriteHandler(&s.Root)([]codextea.SettingsEdit{{
		KeyPath: "personality",
		Value:   string(personality),
	}})
	if err != nil {
		_, writeErr := fmt.Fprintln(s.Stdout, "Personality:", err)
		if writeErr != nil {
			return writeErr
		}
		return nil
	}
	s.UI.Personality = string(personality)
	_, err = fmt.Fprintf(s.Stdout, "Personality set to %s\n", chatwidget.PersonalityLabel(personality))
	return err
}

func interactiveParsePersonality(value string) (chatwidget.Personality, bool) {
	fields := strings.Fields(strings.ToLower(strings.TrimSpace(value)))
	if len(fields) == 0 {
		return "", false
	}
	switch fields[0] {
	case string(chatwidget.PersonalityFriendly):
		return chatwidget.PersonalityFriendly, true
	case string(chatwidget.PersonalityPragmatic):
		return chatwidget.PersonalityPragmatic, true
	case string(chatwidget.PersonalityNone):
		return chatwidget.PersonalityNone, true
	default:
		return "", false
	}
}

func (s *interactiveSession) handleExperimentalCommand(args string) error {
	fields := strings.Fields(strings.TrimSpace(args))
	if len(fields) == 0 {
		settings := interactiveTUISettings(&s.Root)
		lines := []string{"Experimental features:"}
		for _, spec := range features.Registry {
			if spec.Stage != features.StageExperimental {
				continue
			}
			state := "off"
			if features.Enabled(settings.FeatureSettings, spec.Key) {
				state = "on"
			}
			lines = append(lines, "  "+spec.Key+": "+state)
		}
		_, err := fmt.Fprintln(s.Stdout, strings.Join(lines, "\n"))
		return err
	}
	key := strings.TrimSpace(fields[0])
	if !interactiveExperimentalFeatureVisible(key) {
		_, err := fmt.Fprintf(s.Stdout, "Unknown experimental feature: %s\n", key)
		return err
	}
	settings := interactiveTUISettings(&s.Root)
	enabled := !features.Enabled(settings.FeatureSettings, key)
	if len(fields) > 1 {
		parsed, ok := interactiveParseFeatureToggle(fields[1], enabled)
		if !ok {
			_, err := fmt.Fprintln(s.Stdout, "Experimental usage: /experimental FEATURE on|off|toggle")
			return err
		}
		enabled = parsed
	}
	_, err := interactiveSettingsWriteHandler(&s.Root)([]codextea.SettingsEdit{{
		KeyPath: "features." + key,
		Value:   enabled,
	}})
	if err != nil {
		_, writeErr := fmt.Fprintln(s.Stdout, "Experimental:", err)
		if writeErr != nil {
			return writeErr
		}
		return nil
	}
	state := "disabled"
	if enabled {
		state = "enabled"
	}
	_, err = fmt.Fprintf(s.Stdout, "Feature %s %s.\n", key, state)
	return err
}

func interactiveExperimentalFeatureVisible(key string) bool {
	key = strings.TrimSpace(key)
	for _, spec := range features.Registry {
		if spec.Key == key && spec.Stage == features.StageExperimental {
			return true
		}
	}
	return false
}

func interactiveParseFeatureToggle(value string, toggleValue bool) (bool, bool) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "on", "true", "enable", "enabled", "yes":
		return true, true
	case "off", "false", "disable", "disabled", "no":
		return false, true
	case "toggle":
		return toggleValue, true
	default:
		return false, false
	}
}

func displayInteractiveValue(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "default"
	}
	return value
}

func (s *interactiveSession) RunTurn(ctx context.Context, prompt string) error {
	if s.UI == nil {
		root := s.Root
		s.UI = interactiveUIState(&root)
	}
	s.UI.AddMessage(codextui.RoleUser, prompt)
	s.UI.SetStatus("running")
	if _, err := fmt.Fprintln(s.Stdout, s.UI.RenderStatusLine()); err != nil {
		return err
	}
	execOpts := cli.ExecOptions{
		Prompt: prompt,
		Shared: s.Root.Shared,
		Color:  "auto",
	}
	if s.ThreadID != "" {
		execOpts.Subcommand = "resume"
		execOpts.Resume = cli.ExecResumeOptions{
			SessionID: s.ThreadID,
			Prompt:    prompt,
		}
		execOpts.Prompt = ""
	}
	result, err := s.Runner.RunContext(ctx, &codexexec.Request{
		Root: s.Root,
		Exec: execOpts,
	}, strings.NewReader(""), s.Stdout, s.Stderr)
	if err != nil {
		s.UI.SetStatus("error")
		return err
	}
	if result != nil && result.ThreadID != "" {
		s.ThreadID = result.ThreadID
		s.UI.SetThreadID(result.ThreadID)
	}
	if result != nil && result.LastMessage != "" {
		s.UI.AddMessage(codextui.RoleAssistant, result.LastMessage)
	}
	s.UI.SetStatus("idle")
	return nil
}

func isInteractiveExitCommand(input string) bool {
	invocation, ok := codextui.ParseCommand(input)
	return ok && invocation.Command == codextui.CommandExit
}
