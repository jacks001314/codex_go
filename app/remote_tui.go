package app

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/coder/websocket"

	appsapi "codex_go/apps"
	"codex_go/appserver"
	"codex_go/appserverdaemon"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/config"
	"codex_go/doctor"
	"codex_go/features"
	"codex_go/plugin"
	"codex_go/protocol"
	"codex_go/realtime"
	"codex_go/review"
	"codex_go/sandbox"
	"codex_go/session"
	codextui "codex_go/tui"
	chatwidget "codex_go/tui/chatwidget"
	historycell "codex_go/tui/history_cell"
	idecontext "codex_go/tui/ide_context"
	codextea "codex_go/tui/tea"
	"codex_go/turn"
)

type remoteAppServerDialFunc func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error)
type remoteAppServerUnixDialFunc func(context.Context, string) (net.Conn, error)

const remoteTUIAccountRequestTimeout = 20 * time.Second

type remoteAppServerTransport interface {
	read(context.Context) ([]byte, error)
	write(context.Context, []byte) error
	close()
}

type remoteAppServerTUIClient struct {
	endpoint        *appserverdaemon.RemoteAppServerEndpoint
	root            *cli.RootOptions
	state           *codextui.State
	messages        chan<- bubbletea.Msg
	brokers         remoteTUIBrokers
	dial            remoteAppServerDialFunc
	unixDial        remoteAppServerUnixDialFunc
	transport       remoteAppServerTransport
	nextRequestID   int64
	codexHome       string
	turnCompleted   bool
	turnInterrupted bool
	// turnDurationMS carries the completed turn's protocol duration so the TUI
	// can show "Worked for" completion metadata (Rust #43558).
	turnDurationMS *int64
	// reasoningBuffers accumulates streaming reasoning summaries per
	// turn+item so the status row can show the latest usable line
	// (Rust #43921).
	reasoningBuffers map[string]string
	// taskTools, when set, is the session's local task-tools MCP host. The
	// client then points thread/start and thread/fork at the hosted
	// `codex_tui` MCP server instead of the app-server dynamic-tools callback
	// transport (Rust ThreadToolTransport::Mcp vs ::Dynamic).
	taskTools *taskToolsMCPHost
}

type remoteWebSocketTransport struct {
	conn *websocket.Conn
}

type remoteJSONLineTransport struct {
	conn    net.Conn
	reader  *bufio.Reader
	writeMu sync.Mutex
}

type remoteTUIBrokers struct {
	approval    *interactiveApprovalBroker
	elicitation *interactiveElicitationBroker
	userInput   *interactiveUserInputBroker
}

type remoteTUIInterruptController struct {
	ctx      context.Context
	endpoint *appserverdaemon.RemoteAppServerEndpoint
	mu       sync.Mutex
	threadID string
	turnID   string
}

func newRemoteTUIBrokers() remoteTUIBrokers {
	return remoteTUIBrokers{
		approval:    newInteractiveApprovalBroker(),
		elicitation: newInteractiveElicitationBroker(),
		userInput:   newInteractiveUserInputBroker(),
	}
}

func newRemoteTUIInterruptController(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) *remoteTUIInterruptController {
	if ctx == nil {
		ctx = context.Background()
	}
	return &remoteTUIInterruptController{ctx: ctx, endpoint: endpoint}
}

func (c *remoteTUIInterruptController) setActive(threadID string, turnID string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	c.threadID = strings.TrimSpace(threadID)
	c.turnID = strings.TrimSpace(turnID)
	c.mu.Unlock()
}

func (c *remoteTUIInterruptController) clearActive(threadID string, turnID string) {
	if c == nil {
		return
	}
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	c.mu.Lock()
	if c.threadID == threadID && c.turnID == turnID {
		c.threadID = ""
		c.turnID = ""
	}
	c.mu.Unlock()
}

func (c *remoteTUIInterruptController) interruptCommand() bubbletea.Cmd {
	return func() bubbletea.Msg {
		if err := c.interrupt(); err != nil {
			return codextea.TurnInterruptedMsg{Err: err}
		}
		return codextea.TurnInterruptedMsg{}
	}
}

func (c *remoteTUIInterruptController) interrupt() error {
	if c == nil {
		return errors.New("no active remote turn to interrupt")
	}
	c.mu.Lock()
	threadID := strings.TrimSpace(c.threadID)
	turnID := strings.TrimSpace(c.turnID)
	endpoint := c.endpoint
	ctx := c.ctx
	c.mu.Unlock()
	if threadID == "" || turnID == "" {
		return errors.New("no active remote turn to interrupt")
	}
	client, err := openRemoteSessionClient(ctx, endpoint)
	if err != nil {
		return err
	}
	defer client.close()
	var response turn.TurnInterruptResponse
	return remoteSessionRequest(ctx, client, appserver.MethodTurnInterrupt, turn.TurnInterruptParams{
		ThreadID: threadID,
		TurnID:   turnID,
	}, &response)
}

func (c *remoteTUIInterruptController) steer(request codextea.SubmitRequest, clientID string) error {
	if c == nil {
		return errors.New("no active remote turn to steer")
	}
	c.mu.Lock()
	threadID := strings.TrimSpace(c.threadID)
	turnID := strings.TrimSpace(c.turnID)
	endpoint := c.endpoint
	ctx := c.ctx
	c.mu.Unlock()
	if threadID == "" || turnID == "" {
		return errors.New("no active remote turn to steer")
	}
	params, err := remoteTurnSteerParams(threadID, turnID, clientID, request)
	if err != nil {
		return err
	}
	client, err := openRemoteSessionClient(ctx, endpoint)
	if err != nil {
		return err
	}
	defer client.close()
	var response turn.TurnSteerResponse
	return remoteSessionRequest(ctx, client, appserver.MethodTurnSteer, params, &response)
}

func (b remoteTUIBrokers) respond(response codextea.ModalResponse) {
	if b.approval != nil {
		b.approval.respond(response)
	}
	if b.elicitation != nil {
		b.elicitation.respond(response)
	}
	if b.userInput != nil {
		b.userInput.respond(response)
	}
}

type remoteAppServerMessage struct {
	JSONRPC string          `json:"jsonrpc,omitempty"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *struct {
		Code    int            `json:"code"`
		Message string         `json:"message"`
		Data    map[string]any `json:"data,omitempty"`
	} `json:"error,omitempty"`
}

func (t *remoteWebSocketTransport) read(ctx context.Context) ([]byte, error) {
	if t == nil || t.conn == nil {
		return nil, errors.New("remote app-server websocket is not connected")
	}
	messageType, data, err := t.conn.Read(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		if websocket.CloseStatus(err) == websocket.StatusNormalClosure || websocket.CloseStatus(err) == websocket.StatusGoingAway {
			return nil, io.EOF
		}
		return nil, err
	}
	if messageType != websocket.MessageText {
		return nil, errors.New("remote app-server websocket returned a non-text message")
	}
	return data, nil
}

func (t *remoteWebSocketTransport) write(ctx context.Context, data []byte) error {
	if t == nil || t.conn == nil {
		return errors.New("remote app-server websocket is not connected")
	}
	return t.conn.Write(ctx, websocket.MessageText, data)
}

func (t *remoteWebSocketTransport) close() {
	if t != nil && t.conn != nil {
		_ = t.conn.Close(websocket.StatusNormalClosure, "")
	}
}

func newRemoteJSONLineTransport(conn net.Conn) *remoteJSONLineTransport {
	return &remoteJSONLineTransport{
		conn:   conn,
		reader: bufio.NewReaderSize(conn, 64*1024),
	}
}

func (t *remoteJSONLineTransport) read(ctx context.Context) ([]byte, error) {
	if t == nil || t.conn == nil || t.reader == nil {
		return nil, errors.New("remote app-server unix socket is not connected")
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = t.conn.SetReadDeadline(deadline)
	} else {
		_ = t.conn.SetReadDeadline(time.Time{})
	}
	data, err := t.reader.ReadBytes('\n')
	if len(data) > 0 {
		return data, nil
	}
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, err
	}
	return data, nil
}

func (t *remoteJSONLineTransport) write(ctx context.Context, data []byte) error {
	if t == nil || t.conn == nil {
		return errors.New("remote app-server unix socket is not connected")
	}
	if deadline, ok := ctx.Deadline(); ok {
		_ = t.conn.SetWriteDeadline(deadline)
	} else {
		_ = t.conn.SetWriteDeadline(time.Time{})
	}
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if len(data) == 0 || data[len(data)-1] != '\n' {
		data = append(append([]byte(nil), data...), '\n')
	}
	_, err := t.conn.Write(data)
	return err
}

func (t *remoteJSONLineTransport) close() {
	if t != nil && t.conn != nil {
		_ = t.conn.Close()
	}
}

func runInteractiveRemoteTUI(ctx context.Context, root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint, stdin io.Reader, stdout io.Writer) error {
	state := interactiveUIState(root)
	settings := interactiveTUISettings(root)
	if remoteSettings, err := interactiveRemoteLoadSettings(ctx, endpoint); err == nil {
		settings = remoteSettings
	}
	keymapConfig := interactiveKeymapConfig(root)
	if remoteKeymap, err := interactiveRemoteKeymapConfig(ctx, endpoint); err == nil {
		keymapConfig = remoteKeymap
	}
	accountDisplay, hasChatGPTAccount := interactiveRemoteStatusAccount(ctx, endpoint)
	state.AccountDisplay = accountDisplay
	state.HasChatGPTAccount = hasChatGPTAccount
	showRawReasoning := interactiveShowRawAgentReasoning(root)
	// Rust #39082: query remote project config layers before starting a thread
	// and persist accepted trust through config/batchWrite on the remote server.
	interactiveRemoteTrustCheck(ctx, endpoint, root, shouldRunInteractiveTUI(stdin, stdout))
	brokers := newRemoteTUIBrokers()
	interrupts := newRemoteTUIInterruptController(ctx, endpoint)
	// A local-daemon TUI hosts its codex_tui task tools as a local MCP server
	// (Rust AppServerSession::start_dynamic_tool_mcp); a remote workspace keeps
	// the app-server dynamic-tools callback transport.
	taskToolsHost := &taskToolsMCPHost{}
	if !interactiveRemoteEndpointIsLocal(endpoint) {
		taskToolsHost = nil
	}
	defer taskToolsHost.close()
	// Rust daybreak::prefetch_notice: read the account's Daybreak eligibility in
	// the background for a local openai-provider session; the refusal copy falls
	// back to the neutral notice until the read lands.
	daybreakCache := &daybreakNoticeCache{}
	daybreakProvider := ""
	if state != nil {
		daybreakProvider = strings.TrimSpace(state.Provider)
	}
	if interactiveRemoteEndpointIsLocal(endpoint) && strings.EqualFold(daybreakProvider, "openai") {
		prefetchDaybreakNotice(ctx, daybreakCache, func(callCtx context.Context) (appserver.AuthStatusResponse, error) {
			client, err := openRemoteSessionClient(callCtx, endpoint)
			if err != nil {
				return appserver.AuthStatusResponse{}, err
			}
			defer client.close()
			includeToken := true
			refreshToken := false
			var response appserver.AuthStatusResponse
			if err := remoteSessionRequest(callCtx, client, appserver.MethodGetAuthStatus, appserver.AuthStatusParams{
				IncludeToken: &includeToken,
				RefreshToken: &refreshToken,
			}, &response); err != nil {
				return appserver.AuthStatusResponse{}, err
			}
			return response, nil
		}, interactiveDaybreakBaseURL(), auth.DefaultCodexHome())
	}
	// The TUI owns the voice helper and relays its handshake through the
	// app-server on a dedicated connection, so a media session never competes
	// with the interactive read loop.
	voice := newVoiceRuntime(voiceRuntimeOptions{
		// The packaged helper is stamped with the same version, so the
		// same-build handshake succeeds in releases and in dev builds.
		buildCommit:      doctor.Version(),
		realtimeSettings: interactiveRemoteRealtimeSettings(endpoint),
		listVoices:       interactiveRemoteRealtimeVoices(endpoint),
		startSession: func(callCtx context.Context, params realtime.StartParams) error {
			client, err := openRemoteSessionClient(callCtx, endpoint)
			if err != nil {
				return err
			}
			defer client.close()
			id, err := client.sendRequest(callCtx, appserver.MethodThreadRealtimeStart, params)
			if err != nil {
				return err
			}
			return client.waitResponse(callCtx, id, nil)
		},
		stopSession: func(callCtx context.Context, threadID string) error {
			client, err := openRemoteSessionClient(callCtx, endpoint)
			if err != nil {
				return err
			}
			defer client.close()
			id, err := client.sendRequest(callCtx, appserver.MethodThreadRealtimeStop, realtime.StopParams{ThreadID: threadID})
			if err != nil {
				return err
			}
			return client.waitResponse(callCtx, id, nil)
		},
	})
	options := codextea.Options{
		NoAltScreen:        root != nil && root.Shared.NoAltScreen,
		LocalDaemonSession: interactiveRemoteEndpointIsLocal(endpoint),
		LocalSession:       interactiveRemoteEndpointIsLocal(endpoint),
		AnimationsEnabled:  settings.AnimationsEnabled,
		QuestionEscBack:    settings.QuestionEscBack,
		AutoRecap:          settings.AutoRecap,
		ShowRawReasoning:   showRawReasoning,
		// Remote sessions only see the worktrees feature flag; managed worktree
		// operations stay local (Rust #43120/#43286).
		WorktreesEnabled: interactiveRemoteWorktreesEnabled(root),
		// Rust #43340: the connected server owns named permission profiles.
		OnListPermissionProfiles:    interactiveRemoteListPermissionProfiles(ctx, endpoint),
		OnUpdateThreadPermissions:   interactiveRemoteUpdateThreadPermissions(ctx, endpoint),
		SessionPickerItems:          interactiveRemoteSessionPickerItems(ctx, root, endpoint),
		SessionPickerCWD:            interactiveSessionPickerCWD(root),
		SessionPickerView:           settings.SessionPickerView,
		ShowSessionHeader:           true,
		SessionHeaderVersion:        doctor.Version(),
		InitialHistoryCells:         interactiveUpdateHistoryCells(root),
		WindowsSandboxStartupPrompt: interactiveRemoteWindowsSandboxStartupPrompt(ctx, root, endpoint, settings.PermissionRequirements),
		OnSessionAction:             interactiveRemoteSessionActionHandler(ctx, endpoint),
		OnResumeSession:             interactiveRemoteResumeSessionHandler(ctx, endpoint),
		OnPromptEdit:                interactiveRemotePromptEditHandler(ctx, endpoint, root, state, taskToolsHost),
		OnExportTranscript:          interactiveRemoteTranscriptExportHandler(ctx, endpoint, showRawReasoning),
		OnGenerateRecap:             interactiveRemoteRecapGenerateHandler(ctx, endpoint),
		OnDaybreakNotice: func(model string) codextui.DaybreakNotice {
			return daybreakNoticeForModel(daybreakProvider, daybreakCache, model)
		},
		OnLoadTranscriptPreview: interactiveRemoteTranscriptPreviewHandler(ctx, endpoint),
		OnReadSessionTranscript: interactiveRemoteSessionTranscriptHandler(ctx, endpoint, showRawReasoning),
		OnRenameThread:          interactiveRemoteRenameThreadHandler(ctx, endpoint),
		OnLogout:                interactiveRemoteLogoutHandler(ctx, endpoint),
		KeymapConfig:            keymapConfig,
		OnKeymapEdit:            interactiveRemoteKeymapEditHandler(ctx, endpoint),
		OnReadAgents: func(currentThreadID string) ([]codextui.AgentThreadEntry, error) {
			if strings.TrimSpace(currentThreadID) == "" && state != nil {
				currentThreadID = state.ThreadID
			}
			return interactiveRemoteAgentThreadEntries(ctx, endpoint, currentThreadID)
		},
		OnSwitchAgent: func(threadID string) (codextea.AgentThreadSwitchResponse, error) {
			return interactiveRemoteSwitchAgentThread(ctx, endpoint, threadID)
		},
		AgentsOverviewEmbedded:      false,
		OnAgentsOverviewRefresh:     interactiveRemoteAgentsOverviewRefresh(ctx, endpoint),
		OnAgentsOverviewUsage:       interactiveRemoteAgentsOverviewUsage(ctx, endpoint),
		OnAgentsOverviewNewSession:  interactiveRemoteAgentsOverviewNewSession(ctx, endpoint),
		OnAgentsOverviewNewWorktree: interactiveRemoteAgentsOverviewNewWorktree(ctx, endpoint, root),
		OnAgentsOverviewStop:        interactiveRemoteAgentsOverviewStop(ctx, endpoint),
		OnAgentsOverviewArchive:     interactiveRemoteAgentsOverviewArchive(ctx, endpoint),
		OnAgentsOverviewDelete:      interactiveRemoteAgentsOverviewDelete(ctx, endpoint),
		OnAgentsOverviewRename:      interactiveRemoteAgentsOverviewRename(ctx, endpoint),
		OnStartAgentsDaemon:         interactiveStartAgentsDaemon,
		OnWriteSettings:             interactiveRemoteSettingsWriteHandler(ctx, endpoint),
		OnUpdateCollaborationMode:   interactiveRemoteCollaborationModeUpdateHandler(ctx, endpoint),
		OnWriteMemorySettings:       interactiveRemoteMemorySettingsWriteHandler(ctx, endpoint),
		OnResetMemories:             interactiveRemoteMemoryResetHandler(ctx, endpoint),
		OnSubmitFeedback:            interactiveRemoteFeedbackSubmitHandler(ctx, endpoint),
		OnReadIDEContext:            interactiveIDEContextReader,
		OnApproveAutoReviewDenial:   interactiveRemoteApproveAutoReviewDenialHandler(ctx, endpoint),
		FeatureSettings:             settings.FeatureSettings,
		UseMemories:                 settings.UseMemories,
		GenerateMemories:            settings.GenerateMemories,
		FeedbackEnabled:             settings.FeedbackEnabled,
		ModelPickerOptions:          interactiveModelPickerOptions(root),
		ServiceTierCommands:         interactiveServiceTierCommands(state.Model),
		Personality:                 settings.Personality,
		Notifications:               settings.Notifications,
		NotificationMethod:          settings.NotificationMethod,
		NotificationCondition:       settings.NotificationCondition,
		PermissionRequirements:      settings.PermissionRequirements,
		HideRateLimitModelNudge:     settings.HideRateLimitModelNudge,
		TUITheme:                    settings.TUITheme,
		StartupConfigWarnings:       remoteTUIStartupConfigWarnings(settings.TUITheme),
		TUIPet:                      settings.TUIPet,
		CodexHome:                   auth.DefaultCodexHome(),
		PetEnv:                      environmentMapFromEnviron(os.Environ()),
		OnPostNotification:          interactiveNotificationPoster(stdout),
		OnSubmitRequest: func(request codextea.SubmitRequest) bubbletea.Cmd {
			if state.ThreadName == "" && len(state.Messages) == 0 {
				if title := interactiveAutoThreadTitle(request.Prompt); title != "" {
					if err := interactiveRemoteRenameThreadHandler(ctx, endpoint)(state.ThreadID, title); err == nil {
						state.SetThreadName(title)
					}
				}
			}
			return interactiveRemoteTurnCommandWithTaskTools(ctx, root, endpoint, state, request, brokers, taskToolsHost, interrupts)
		},
		OnSteerRequest: interrupts.steer,
		OnInterrupt: func() bubbletea.Cmd {
			return interrupts.interruptCommand()
		},
		OnSafetyBufferingRetry: func(threadID, turnID, model, prompt string) bubbletea.Cmd {
			return interactiveRemoteSafetyBufferingRetryCommand(ctx, root, endpoint, state, threadID, turnID, model, prompt, brokers, taskToolsHost)
		},
		OnModalResponse: func(response codextea.ModalResponse) bubbletea.Cmd {
			brokers.respond(response)
			return interactiveRemoteModelSelectionCommand(ctx, endpoint, state, response.Picker)
		},
		OnReadTokenActivity: func(view chatwidget.TokenActivityView) (chatwidget.TokenActivityResponse, error) {
			return interactiveRemoteReadTokenActivity(ctx, endpoint, view)
		},
		OnReadRateLimitResetCredits: func() (int64, error) {
			return interactiveRemoteReadRateLimitResetCredits(ctx, endpoint)
		},
		OnConsumeRateLimitResetCredit: func(idempotencyKey string) (chatwidget.RateLimitResetConsumeOutcome, error) {
			return interactiveRemoteConsumeRateLimitResetCredit(ctx, endpoint, idempotencyKey)
		},
		OnReadRateLimits:    interactiveRemoteRateLimitsReader(ctx, endpoint),
		OnReadBackendBanner: interactiveRemoteBackendBannerReader(ctx, endpoint),
		OnBackendBannerAction: interactiveBackendBannerActionHandler(
			interactiveRemoteAddCreditsNudgeSender(ctx, endpoint),
		),
		OnReadGoal: func(threadID string) (*appserver.Goal, error) {
			return interactiveRemoteReadGoal(ctx, endpoint, threadID)
		},
		OnSetGoal: func(threadID string, objective *string, tokenBudget *int64, status *appserver.GoalStatus) (appserver.Goal, error) {
			return interactiveRemoteSetGoal(ctx, endpoint, threadID, objective, tokenBudget, status)
		},
		OnClearGoal: func(threadID string) (bool, error) {
			return interactiveRemoteClearGoal(ctx, endpoint, threadID)
		},
		OnGoalEditText: func(threadID string, objective string) (string, error) {
			return interactiveRemoteGoalEditText(ctx, endpoint, threadID, objective)
		},
		OnGoalDraftMaterialize: func(draft codextui.GoalDraft) (string, error) {
			return interactiveRemoteGoalDraftMaterialize(ctx, endpoint, draft)
		},
		OnStartWindowsSandboxSetup: func(mode chatwidget.WindowsSandboxMode, cwd string) (codextea.WindowsSandboxSetupOutcome, error) {
			setupCWD := strings.TrimSpace(cwd)
			if setupCWD == "" {
				setupCWD = interactiveSessionPickerCWD(root)
			}
			return interactiveRemoteStartWindowsSandboxSetup(ctx, endpoint, mode, setupCWD)
		},
		OnOpenDesktopThread: interactiveOpenDesktopThread,
		OnVoiceConversationStart: func(threadID string, attemptID uint64) bubbletea.Cmd {
			return voice.startCmd(threadID, attemptID)
		},
		OnVoiceApplyAnswer: func(threadID string, attemptID uint64, answer string) bubbletea.Cmd {
			return voice.applyAnswerCmd(threadID, attemptID, answer)
		},
		OnVoiceCloseHelper: voice.close,
		OnVoiceSetMicrophoneMuted: func(muted bool) error {
			return voice.setMicrophoneMuted(muted)
		},
		OnVoicePeaks:     voice.peaks,
		OnVoiceSettings:  interactiveRemoteVoiceSettings(endpoint),
		OnVoiceSaveVoice: interactiveRemoteVoiceSaver(endpoint),
		OnVoiceAppendSpeech: interactiveRemoteSpeechSender(endpoint, func() string {
			return state.ThreadID
		}),
		OnReadRolloutPath: func(threadID string) (string, error) {
			if strings.TrimSpace(threadID) == "" {
				return "", nil
			}
			client, err := openRemoteSessionClient(ctx, endpoint)
			if err != nil {
				return "", err
			}
			defer client.close()
			thread, err := remoteTUIReadThread(ctx, client, threadID, false)
			if err != nil {
				return "", err
			}
			if thread.Path == nil {
				return "", nil
			}
			return *thread.Path, nil
		},
		OnDetectExternalAgent: interactiveRemoteExternalAgentDetectHandler(endpoint),
		OnReadHooks: func(cwd string) (appserver.HookListResponse, error) {
			hooksCWD := strings.TrimSpace(cwd)
			if hooksCWD == "" {
				hooksCWD = interactiveSessionPickerCWD(root)
			}
			return interactiveRemoteReadHooks(ctx, endpoint, hooksCWD)
		},
		OnWriteHookConfig: interactiveRemoteHookConfigWriter(ctx, endpoint),
		OnReadPlugins: func(cwd string, forceRefetch bool) (plugin.PluginListResponse, error) {
			pluginCWD := strings.TrimSpace(cwd)
			if pluginCWD == "" {
				pluginCWD = interactiveSessionPickerCWD(root)
			}
			return interactiveRemoteReadPlugins(ctx, endpoint, pluginCWD, forceRefetch)
		},
		OnReadPlugin: func(params plugin.PluginReadParams) (plugin.PluginReadResponse, error) {
			return interactiveRemotePluginCall[plugin.PluginReadResponse](ctx, endpoint, appserver.MethodPluginRead, params)
		},
		OnInstallPlugin: func(params plugin.PluginInstallParams) (plugin.PluginInstallResponse, error) {
			return interactiveRemotePluginCall[plugin.PluginInstallResponse](ctx, endpoint, appserver.MethodPluginInstall, params)
		},
		OnUninstallPlugin: func(params plugin.PluginUninstallParams) (plugin.PluginUninstallResponse, error) {
			return interactiveRemotePluginCall[plugin.PluginUninstallResponse](ctx, endpoint, appserver.MethodPluginUninstall, params)
		},
		OnWritePluginEnabled: func(pluginID string, enabled bool) error {
			_, err := interactiveRemotePluginCall[config.ConfigWriteResponse](ctx, endpoint, appserver.MethodConfigValueWrite, config.ConfigValueWriteParams{
				KeyPath:       "plugins." + strings.TrimSpace(pluginID),
				Value:         map[string]any{"enabled": enabled},
				MergeStrategy: config.MergeUpsert,
			})
			return err
		},
		OnAddMarketplace: func(params plugin.MarketplaceAddParams) (plugin.MarketplaceAddResponse, error) {
			return interactiveRemotePluginCall[plugin.MarketplaceAddResponse](ctx, endpoint, appserver.MethodMarketplaceAdd, params)
		},
		OnRemoveMarketplace: func(params plugin.MarketplaceRemoveParams) (plugin.MarketplaceRemoveResponse, error) {
			return interactiveRemotePluginCall[plugin.MarketplaceRemoveResponse](ctx, endpoint, appserver.MethodMarketplaceRemove, params)
		},
		OnUpgradeMarketplace: func(params plugin.MarketplaceUpgradeParams) (plugin.MarketplaceUpgradeResponse, error) {
			return interactiveRemotePluginCall[plugin.MarketplaceUpgradeResponse](ctx, endpoint, appserver.MethodMarketplaceUpgrade, params)
		},
		OnOpenPluginURL:        auth.OpenBrowser,
		PluginUserMarketplaces: settings.PluginUserMarketplaces,
		PluginGitMarketplaces:  settings.PluginGitMarketplaces,
		OnReadSkills: func(cwd string, forceReload bool) (appserver.SkillsListResponse, error) {
			skillsCWD := strings.TrimSpace(cwd)
			if skillsCWD == "" {
				skillsCWD = interactiveSessionPickerCWD(root)
			}
			return interactiveRemoteReadSkills(ctx, endpoint, skillsCWD, forceReload)
		},
		OnWriteSkillEnabled: interactiveRemoteSkillEnabledWriter(ctx, endpoint),
		OnFuzzyFileSearch: func(query string, cwd string, cancellationToken string) (appserver.FuzzyFileSearchResponse, error) {
			searchCWD := strings.TrimSpace(cwd)
			if searchCWD == "" {
				searchCWD = interactiveSessionPickerCWD(root)
			}
			return interactiveRemoteFuzzyFileSearch(ctx, endpoint, query, searchCWD, cancellationToken)
		},
		// The remote TUI registers the codex_tui task-management namespace with
		// this app server, so task mentions are enabled for it (Rust
		// chat_widget.set_task_mentions_enabled(task_tools_available)).
		OnSearchTasks: interactiveRemoteTaskMentionSearch(ctx, endpoint),
		// Threads started by an earlier process keep their persisted task-tool
		// capability (Rust AppServerSession::task_tools_available).
		OnTaskToolsAvailable: func(threadID string) bool {
			return remoteTaskToolThreadAvailable(auth.DefaultCodexHome(), threadID)
		},
		// /experimental is populated from this server's feature catalog (Rust
		// experimental_features::fetch) instead of the compiled registry.
		OnReadExperimentalFeatures: interactiveRemoteExperimentalFeatures(ctx, endpoint),
		OnReadApps: func(threadID string, forceRefetch bool) (appsapi.AppListResponse, error) {
			if strings.TrimSpace(threadID) == "" && state != nil {
				threadID = state.ThreadID
			}
			return interactiveRemoteReadApps(ctx, endpoint, threadID, forceRefetch)
		},
		OnStartReviewCommand:  interactiveRemoteReviewStartCommand(ctx, root, endpoint, state, brokers, interrupts),
		OnStartCompactCommand: interactiveRemoteCompactStartCommand(ctx, root, endpoint, state, brokers),
		OnStartSide: func(params codextea.SideStartParams) (codextea.SideStartResponse, error) {
			return interactiveRemoteStartSide(ctx, root, endpoint, state, params, taskToolsHost)
		},
		OnCloseSide: func(params codextea.SideCloseParams) (codextea.SideCloseResponse, error) {
			return interactiveRemoteCloseSide(ctx, endpoint, params)
		},
		HasChatGPTAccount: hasChatGPTAccount,
	}
	_, err := codextea.Run(ctx, state, options, stdin, stdout)
	return err
}

func interactiveRemoteStatusAccount(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) (string, bool) {
	reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
	defer cancel()
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return "", false
	}
	defer client.close()
	var response auth.GetAccountResponse
	if err := remoteSessionRequest(reqCtx, client, appserver.MethodGetAccount, auth.GetAccountParams{}, &response); err != nil {
		return "", false
	}
	return interactiveAccountDisplay(response.Account)
}

func interactiveRemoteKeymapConfig(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) (*codextui.KeymapConfig, error) {
	reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
	defer cancel()
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return nil, err
	}
	defer client.close()
	return interactiveRemoteReadKeymapConfig(reqCtx, client)
}

func interactiveRemoteKeymapEditHandler(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.KeymapEditFunc {
	return func(edit codextui.KeymapEdit) (*codextui.KeymapConfig, string, error) {
		if err := edit.Validate(); err != nil {
			return nil, "", err
		}
		reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
		defer cancel()
		client, err := openRemoteSessionClient(reqCtx, endpoint)
		if err != nil {
			return nil, "", err
		}
		defer client.close()
		var response config.ConfigWriteResponse
		params := config.ConfigValueWriteParams{
			KeyPath:       edit.KeyPath(),
			Value:         edit.ConfigValue(),
			MergeStrategy: config.MergeReplace,
		}
		if err := remoteSessionRequest(reqCtx, client, appserver.MethodConfigValueWrite, params, &response); err != nil {
			return nil, "", err
		}
		keymap, err := interactiveRemoteReadKeymapConfig(reqCtx, client)
		if err != nil {
			return nil, "", err
		}
		message := interactiveKeymapEditMessage(edit)
		if strings.TrimSpace(response.FilePath) != "" {
			message += " Saved to " + response.FilePath + "."
		}
		return keymap, message, nil
	}
}

func interactiveRemoteReadKeymapConfig(ctx context.Context, client *remoteAppServerTUIClient) (*codextui.KeymapConfig, error) {
	var response config.ConfigReadResponse
	if err := remoteSessionRequest(ctx, client, appserver.MethodConfigRead, config.ConfigReadParams{}, &response); err != nil {
		return nil, err
	}
	return codextui.KeymapConfigFromConfigValues(response.Config)
}

func interactiveRemoteReadTokenActivity(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, view chatwidget.TokenActivityView) (chatwidget.TokenActivityResponse, error) {
	reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
	defer cancel()
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return chatwidget.TokenActivityResponse{}, err
	}
	defer client.close()
	var response auth.GetAccountTokenUsageResponse
	if err := remoteSessionRequest(reqCtx, client, appserver.MethodGetAccountTokenUsage, map[string]any{}, &response); err != nil {
		return chatwidget.TokenActivityResponse{}, err
	}
	return remoteTokenActivityResponseFromAuth(response), nil
}

func interactiveRemoteReadRateLimitResetCredits(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) (int64, error) {
	reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
	defer cancel()
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return 0, err
	}
	defer client.close()
	var response auth.GetAccountRateLimitsResponse
	if err := remoteSessionRequest(reqCtx, client, appserver.MethodGetAccountRateLimits, map[string]any{}, &response); err != nil {
		return 0, err
	}
	if response.RateLimitResetCredits == nil || response.RateLimitResetCredits.AvailableCount <= 0 {
		return 0, nil
	}
	return response.RateLimitResetCredits.AvailableCount, nil
}

func interactiveRemoteConsumeRateLimitResetCredit(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, idempotencyKey string) (chatwidget.RateLimitResetConsumeOutcome, error) {
	reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
	defer cancel()
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return "", err
	}
	defer client.close()
	params := auth.ConsumeRateLimitResetCreditParams{IdempotencyKey: strings.TrimSpace(idempotencyKey)}
	var response auth.ConsumeRateLimitResetCreditResponse
	if err := remoteSessionRequest(reqCtx, client, appserver.MethodConsumeAccountRateLimitResetCredit, params, &response); err != nil {
		return "", err
	}
	return remoteRateLimitResetOutcomeFromAuth(response.Outcome)
}

func interactiveRemoteLoadSettings(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) (codextea.SettingsWriteResult, error) {
	reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
	defer cancel()
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return codextea.SettingsWriteResult{}, err
	}
	defer client.close()
	return interactiveRemoteReadSettings(reqCtx, client)
}

func interactiveRemoteReadGoal(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, threadID string) (*appserver.Goal, error) {
	reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
	defer cancel()
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return nil, err
	}
	defer client.close()
	var response appserver.GoalGetResponse
	params := appserver.GoalGetParams{ThreadID: strings.TrimSpace(threadID)}
	if err := remoteSessionRequest(reqCtx, client, appserver.MethodThreadGoalGet, params, &response); err != nil {
		return nil, err
	}
	return response.Goal, nil
}

func interactiveRemoteGoalEditText(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, threadID string, objective string) (string, error) {
	reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
	defer cancel()
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return objective, err
	}
	defer client.close()
	return resolveGoalObjectiveText(remoteGoalFS(reqCtx, client), client.codexHome, objective)
}

func interactiveRemoteGoalDraftMaterialize(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, draft codextui.GoalDraft) (string, error) {
	reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
	defer cancel()
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return "", err
	}
	defer client.close()
	return materializeGoalDraft(remoteGoalFS(reqCtx, client), client.codexHome, draft)
}

func interactiveRemoteSetGoal(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, threadID string, objective *string, tokenBudget *int64, status *appserver.GoalStatus) (appserver.Goal, error) {
	reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
	defer cancel()
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return appserver.Goal{}, err
	}
	defer client.close()
	params := appserver.GoalSetParams{
		ThreadID:    strings.TrimSpace(threadID),
		Objective:   trimStringPtrRemote(objective),
		TokenBudget: cloneInt64PtrRemote(tokenBudget),
		Status:      cloneGoalStatusPtrRemote(status),
	}
	if tokenBudget != nil {
		params.TokenBudgetSet = true
	}
	if params.Objective != nil {
		materialized, materializeErr := materializeOversizedGoalObjective(
			remoteGoalFS(reqCtx, client),
			client.codexHome,
			*params.Objective,
		)
		if materializeErr != nil {
			return appserver.Goal{}, materializeErr
		}
		params.Objective = &materialized
	}
	var response appserver.GoalSetResponse
	if err := remoteSessionRequest(reqCtx, client, appserver.MethodThreadGoalSet, params, &response); err != nil {
		return appserver.Goal{}, err
	}
	return response.Goal, nil
}

func interactiveRemoteClearGoal(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, threadID string) (bool, error) {
	reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
	defer cancel()
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return false, err
	}
	defer client.close()
	var response appserver.GoalClearResponse
	params := appserver.GoalClearParams{ThreadID: strings.TrimSpace(threadID)}
	if err := remoteSessionRequest(reqCtx, client, appserver.MethodThreadGoalClear, params, &response); err != nil {
		return false, err
	}
	return response.Cleared, nil
}

func interactiveRemoteSettingsWriteHandler(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.SettingsWriteFunc {
	return func(edits []codextea.SettingsEdit) (codextea.SettingsWriteResult, error) {
		reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
		defer cancel()
		client, err := openRemoteSessionClient(reqCtx, endpoint)
		if err != nil {
			return codextea.SettingsWriteResult{}, err
		}
		defer client.close()
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
			return interactiveRemoteReadSettings(reqCtx, client)
		}
		var response config.ConfigWriteResponse
		// Rust config_update::write_config_batch always asks the server to reload
		// the user config so the running session picks up the change.
		if err := remoteSessionRequest(reqCtx, client, appserver.MethodConfigBatchWrite, config.ConfigBatchWriteParams{
			Edits:            configEdits,
			ReloadUserConfig: true,
		}, &response); err != nil {
			return codextea.SettingsWriteResult{}, err
		}
		result, err := interactiveRemoteReadSettings(reqCtx, client)
		if err != nil {
			return codextea.SettingsWriteResult{}, err
		}
		result.FilePath = response.FilePath
		return result, nil
	}
}

func interactiveRemoteCollaborationModeUpdateHandler(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.CollaborationModeUpdateFunc {
	return func(threadID string, mode chatwidget.CollaborationMode) error {
		threadID = strings.TrimSpace(threadID)
		if threadID == "" {
			return nil
		}
		reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
		defer cancel()
		client, err := openRemoteSessionClient(reqCtx, endpoint)
		if err != nil {
			return err
		}
		defer client.close()
		var response appserver.SettingsUpdateResponse
		return remoteSessionRequest(reqCtx, client, appserver.MethodThreadSettingsUpdate, appserver.SettingsUpdateParams{
			ThreadID:          threadID,
			CollaborationMode: interactiveCollaborationModePayload(&mode),
		}, &response)
	}
}

func interactiveRemoteMemorySettingsWriteHandler(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.MemorySettingsWriteFunc {
	return func(threadID string, useMemories bool, generateMemories bool, generateChanged bool) (codextea.SettingsWriteResult, error) {
		result, err := interactiveRemoteSettingsWriteHandler(ctx, endpoint)([]codextea.SettingsEdit{
			{KeyPath: "memories.use_memories", Value: useMemories},
			{KeyPath: "memories.generate_memories", Value: generateMemories},
		})
		if err != nil || !generateChanged || strings.TrimSpace(threadID) == "" {
			return result, err
		}
		reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
		defer cancel()
		client, err := openRemoteSessionClient(reqCtx, endpoint)
		if err != nil {
			return result, fmt.Errorf("Saved memory settings, but failed to update the current thread: %w", err)
		}
		defer client.close()
		params := appserver.ThreadMemoryModeSetParams{ThreadID: strings.TrimSpace(threadID), Mode: interactiveThreadMemoryMode(generateMemories)}
		var response appserver.ThreadMemoryModeSetResponse
		if err := remoteSessionRequest(reqCtx, client, appserver.MethodThreadMemoryModeSet, params, &response); err != nil {
			return result, fmt.Errorf("Saved memory settings, but failed to update the current thread: %w", err)
		}
		return result, nil
	}
}

func interactiveRemoteMemoryResetHandler(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.MemoryResetFunc {
	return func() error {
		reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
		defer cancel()
		client, err := openRemoteSessionClient(reqCtx, endpoint)
		if err != nil {
			return err
		}
		defer client.close()
		var response appserver.MemoryResetResponse
		return remoteSessionRequest(reqCtx, client, appserver.MethodMemoryReset, map[string]any{}, &response)
	}
}

func interactiveRemoteFeedbackSubmitHandler(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.FeedbackSubmitFunc {
	return func(params appserver.FeedbackUploadParams) (appserver.FeedbackUploadResponse, error) {
		reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
		defer cancel()
		client, err := openRemoteSessionClient(reqCtx, endpoint)
		if err != nil {
			return appserver.FeedbackUploadResponse{}, err
		}
		defer client.close()
		var response appserver.FeedbackUploadResponse
		if err := remoteSessionRequest(reqCtx, client, appserver.MethodFeedbackUpload, params, &response); err != nil {
			return appserver.FeedbackUploadResponse{}, err
		}
		return response, nil
	}
}

func interactiveRemoteApproveAutoReviewDenialHandler(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.AutoReviewDenialApproveFunc {
	return func(threadID string, entry chatwidget.AutoReviewDenialEntry) error {
		reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
		defer cancel()
		client, err := openRemoteSessionClient(reqCtx, endpoint)
		if err != nil {
			return err
		}
		defer client.close()
		params := appserver.ThreadApproveGuardianDeniedActionParams{
			ThreadID: strings.TrimSpace(threadID),
			Event:    append(json.RawMessage(nil), entry.Event...),
		}
		var response appserver.ThreadApproveGuardianDeniedActionResponse
		return remoteSessionRequest(reqCtx, client, appserver.MethodThreadApproveGuardianDeniedAction, params, &response)
	}
}

func interactiveRemoteReadSettings(ctx context.Context, client *remoteAppServerTUIClient) (codextea.SettingsWriteResult, error) {
	var response config.ConfigReadResponse
	if err := remoteSessionRequest(ctx, client, appserver.MethodConfigRead, config.ConfigReadParams{}, &response); err != nil {
		return codextea.SettingsWriteResult{}, err
	}
	result := interactiveSettingsFromConfig(&config.Config{Values: response.Config})
	var requirements config.ConfigRequirementsReadResponse
	if err := remoteSessionRequest(ctx, client, appserver.MethodConfigRequirementsRead, map[string]any{}, &requirements); err == nil {
		result.PermissionRequirements = interactivePermissionRequirementsFromConfigRequirements(requirements.Requirements)
	}
	return result, nil
}

func interactiveRemoteStartWindowsSandboxSetup(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, mode chatwidget.WindowsSandboxMode, cwd string) (codextea.WindowsSandboxSetupOutcome, error) {
	reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
	defer cancel()
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return codextea.WindowsSandboxSetupOutcome{}, err
	}
	defer client.close()
	params := sandbox.WindowsSetupStartParams{Mode: remoteWindowsSandboxSetupMode(mode)}
	if strings.TrimSpace(cwd) != "" {
		trimmed := strings.TrimSpace(cwd)
		params.CWD = &trimmed
	}
	var response sandbox.WindowsSetupStartResponse
	if err := remoteSessionRequest(reqCtx, client, appserver.MethodWindowsSandboxSetupStart, params, &response); err != nil {
		return codextea.WindowsSandboxSetupOutcome{}, err
	}
	return codextea.WindowsSandboxSetupOutcome{Started: response.Started}, nil
}

func remoteWindowsSandboxSetupMode(mode chatwidget.WindowsSandboxMode) sandbox.WindowsSetupMode {
	switch mode {
	case chatwidget.WindowsSandboxModeUnelevated, chatwidget.WindowsSandboxModeDefault:
		return sandbox.WindowsSetupUnelevated
	default:
		return sandbox.WindowsSetupElevated
	}
}

func interactiveRemoteReadHooks(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, cwd string) (appserver.HookListResponse, error) {
	reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
	defer cancel()
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return appserver.HookListResponse{}, err
	}
	defer client.close()
	params := appserver.HookListParams{}
	if strings.TrimSpace(cwd) != "" {
		params.CWDs = []string{strings.TrimSpace(cwd)}
	}
	var response appserver.HookListResponse
	if err := remoteSessionRequest(reqCtx, client, appserver.MethodHooksList, params, &response); err != nil {
		return appserver.HookListResponse{}, err
	}
	return response, nil
}

func interactiveRemoteHookConfigWriter(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.HookConfigWriteFunc {
	return func(params config.ConfigBatchWriteParams) error {
		reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
		defer cancel()
		client, err := openRemoteSessionClient(reqCtx, endpoint)
		if err != nil {
			return err
		}
		defer client.close()
		var response config.ConfigWriteResponse
		return remoteSessionRequest(reqCtx, client, appserver.MethodConfigBatchWrite, params, &response)
	}
}

func interactiveRemoteReadPlugins(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, cwd string, forceRefetch bool) (plugin.PluginListResponse, error) {
	params := plugin.PluginListParams{IncludeInstalled: true, ForceRefetch: forceRefetch}
	if cwd = strings.TrimSpace(cwd); cwd != "" {
		params.CWDs = []string{cwd}
	}
	return interactiveRemotePluginCall[plugin.PluginListResponse](ctx, endpoint, appserver.MethodPluginList, params)
}

func interactiveRemotePluginCall[T any](ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, method appserver.Method, params any) (T, error) {
	var response T
	reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
	defer cancel()
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return response, err
	}
	defer client.close()
	if err := remoteSessionRequest(reqCtx, client, method, params, &response); err != nil {
		return response, err
	}
	return response, nil
}

func interactiveRemoteReadApps(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, threadID string, forceRefetch bool) (appsapi.AppListResponse, error) {
	reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
	defer cancel()
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return appsapi.AppListResponse{}, err
	}
	defer client.close()
	params := appsapi.AppListParams{ForceRefetch: forceRefetch}
	if strings.TrimSpace(threadID) != "" {
		threadID = strings.TrimSpace(threadID)
		params.ThreadID = &threadID
	}
	var response appsapi.AppListResponse
	if err := remoteSessionRequest(reqCtx, client, appserver.MethodAppList, params, &response); err != nil {
		return appsapi.AppListResponse{}, err
	}
	return response, nil
}

func interactiveRemoteStartReview(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, params review.StartParams) (review.StartResponse, error) {
	reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
	defer cancel()
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return review.StartResponse{}, err
	}
	defer client.close()
	var response review.StartResponse
	if err := remoteSessionRequest(reqCtx, client, appserver.MethodReviewStart, params, &response); err != nil {
		return review.StartResponse{}, err
	}
	return response, nil
}

func interactiveRemoteStartSide(ctx context.Context, root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint, state *codextui.State, params codextea.SideStartParams, taskTools ...*taskToolsMCPHost) (codextea.SideStartResponse, error) {
	var taskToolsHost *taskToolsMCPHost
	if len(taskTools) > 0 {
		taskToolsHost = taskTools[0]
	}
	reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
	defer cancel()
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return codextea.SideStartResponse{}, err
	}
	defer client.close()
	parentThreadID := strings.TrimSpace(params.ParentThreadID)
	if parentThreadID == "" && state != nil {
		parentThreadID = strings.TrimSpace(state.ThreadID)
	}
	if parentThreadID == "" {
		return codextea.SideStartResponse{}, errors.New("remote thread/fork requires a parent thread id")
	}
	forkParams, err := remoteSideThreadForkParams(root, state, parentThreadID, params.RuntimeWorkspaceRoots)
	if err != nil {
		return codextea.SideStartResponse{}, err
	}
	// A side conversation forked from a task-tools thread keeps the namespace
	// (Rust ThreadToolTransport::configure_mcp).
	mergeTaskToolsMCPConfig(&forkParams.Config, taskToolsHost)
	var forkResponse appserver.ThreadForkResponse
	if err := remoteSessionRequest(reqCtx, client, appserver.MethodThreadFork, forkParams, &forkResponse); err != nil {
		return codextea.SideStartResponse{}, err
	}
	if forkResponse.Thread == nil || strings.TrimSpace(forkResponse.Thread.ID) == "" {
		return codextea.SideStartResponse{}, errors.New("thread/fork response did not include a side thread id")
	}
	sideThreadID := strings.TrimSpace(forkResponse.Thread.ID)
	var injectResponse appserver.ThreadInjectItemsResponse
	injectParams := appserver.ThreadInjectItemsParams{
		ThreadID: sideThreadID,
		Items:    []json.RawMessage{codextea.SideBoundaryPromptItem()},
	}
	if err := remoteSessionRequest(reqCtx, client, appserver.MethodThreadInjectItems, injectParams, &injectResponse); err != nil {
		return codextea.SideStartResponse{}, fmt.Errorf("prepare side conversation %s: %w", sideThreadID, err)
	}
	return codextea.SideStartResponse{
		ParentThreadID: parentThreadID,
		SideThreadID:   sideThreadID,
	}, nil
}

func interactiveRemoteCloseSide(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, params codextea.SideCloseParams) (codextea.SideCloseResponse, error) {
	reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
	defer cancel()
	sideThreadID := strings.TrimSpace(params.SideThreadID)
	if sideThreadID == "" {
		return codextea.SideCloseResponse{}, errors.New("remote thread/unsubscribe requires a side thread id")
	}
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return codextea.SideCloseResponse{}, err
	}
	defer client.close()
	var response appserver.ThreadUnsubscribeResponse
	unsubscribeParams := appserver.ThreadUnsubscribeParams{ThreadID: sideThreadID}
	if err := remoteSessionRequest(reqCtx, client, appserver.MethodThreadUnsubscribe, unsubscribeParams, &response); err != nil {
		return codextea.SideCloseResponse{}, err
	}
	return codextea.SideCloseResponse{}, nil
}

// remoteTUIStartupConfigWarnings reports the TUI-side startup warnings for the
// remote app. Rust validates the final config's `tui.theme` when it applies the
// syntax-highlight override and pushes the notice into the same startup
// warnings list the app-server config warnings use.
func remoteTUIStartupConfigWarnings(theme string) []string {
	if warning := codextui.ThemeStartupWarning(theme, auth.DefaultCodexHome()); warning != "" {
		return []string{warning}
	}
	return nil
}

func interactiveRemoteReadSkills(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, cwd string, forceReload bool) (appserver.SkillsListResponse, error) {
	reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
	defer cancel()
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return appserver.SkillsListResponse{}, err
	}
	defer client.close()
	params := appserver.SkillsListParams{ForceReload: forceReload}
	if strings.TrimSpace(cwd) != "" {
		params.CWDs = []string{strings.TrimSpace(cwd)}
	}
	var response appserver.SkillsListResponse
	if err := remoteSessionRequest(reqCtx, client, appserver.MethodSkillsList, params, &response); err != nil {
		return appserver.SkillsListResponse{}, err
	}
	return response, nil
}

func interactiveRemoteSkillEnabledWriter(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.SkillEnabledWriteFunc {
	return func(path string, enabled bool) (bool, error) {
		reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
		defer cancel()
		client, err := openRemoteSessionClient(reqCtx, endpoint)
		if err != nil {
			return false, err
		}
		defer client.close()
		params := appserver.SkillsConfigWriteParams{Path: strings.TrimSpace(path), Enabled: enabled}
		var response appserver.SkillsConfigWriteResponse
		if err := remoteSessionRequest(reqCtx, client, appserver.MethodSkillsConfigWrite, params, &response); err != nil {
			return false, err
		}
		return response.EffectiveEnabled, nil
	}
}

func interactiveRemoteFuzzyFileSearch(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, query string, cwd string, cancellationToken string) (appserver.FuzzyFileSearchResponse, error) {
	reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
	defer cancel()
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return appserver.FuzzyFileSearchResponse{}, err
	}
	defer client.close()
	params := appserver.FuzzyFileSearchParams{Query: query}
	if cwd = strings.TrimSpace(cwd); cwd != "" {
		params.Roots = []string{cwd}
	}
	if cancellationToken = strings.TrimSpace(cancellationToken); cancellationToken != "" {
		params.CancellationToken = &cancellationToken
	}
	var response appserver.FuzzyFileSearchResponse
	if err := remoteSessionRequest(reqCtx, client, appserver.MethodFuzzyFileSearch, params, &response); err != nil {
		return appserver.FuzzyFileSearchResponse{}, err
	}
	return response, nil
}

func remoteTUIAccountRequestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, remoteTUIAccountRequestTimeout)
}

// interactiveRemoteTaskMentionSearch mirrors Rust task_mentions::spawn_search
// against the remote app server: thread/search and thread/list merged into the
// mention popup's task candidates.
func interactiveRemoteTaskMentionSearch(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.TaskMentionSearchFunc {
	return func(query string, currentThreadID string, cwd string) ([]codextui.TaskMention, error) {
		reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
		defer cancel()
		client, err := openRemoteSessionClient(reqCtx, endpoint)
		if err != nil {
			return nil, err
		}
		defer client.close()
		return searchTaskMentions(reqCtx, func(requestCtx context.Context, method appserver.Method, params any, target any) error {
			return remoteSessionRequest(requestCtx, client, method, params, target)
		}, query, strings.TrimSpace(currentThreadID), cwd), nil
	}
}

func trimStringPtrRemote(value *string) *string {
	if value == nil {
		return nil
	}
	trimmed := strings.TrimSpace(*value)
	return &trimmed
}

func cloneInt64PtrRemote(value *int64) *int64 {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func cloneGoalStatusPtrRemote(value *appserver.GoalStatus) *appserver.GoalStatus {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func remoteTokenActivityResponseFromAuth(response auth.GetAccountTokenUsageResponse) chatwidget.TokenActivityResponse {
	out := chatwidget.TokenActivityResponse{
		Summary: chatwidget.TokenActivitySummary{
			LifetimeTokens:        response.Summary.LifetimeTokens,
			PeakDailyTokens:       response.Summary.PeakDailyTokens,
			LongestRunningTurnSec: response.Summary.LongestRunningTurnSec,
			CurrentStreakDays:     response.Summary.CurrentStreakDays,
			LongestStreakDays:     response.Summary.LongestStreakDays,
		},
	}
	if response.DailyUsageBuckets != nil {
		buckets := make([]chatwidget.TokenActivityDailyBucket, 0, len(response.DailyUsageBuckets))
		for _, bucket := range response.DailyUsageBuckets {
			buckets = append(buckets, chatwidget.TokenActivityDailyBucket{
				StartDate: strings.TrimSpace(bucket.StartDate),
				Tokens:    bucket.Tokens,
			})
		}
		out.DailyUsageBuckets = &buckets
	}
	return out
}

func remoteRateLimitResetOutcomeFromAuth(outcome auth.ConsumeRateLimitResetCreditOutcome) (chatwidget.RateLimitResetConsumeOutcome, error) {
	switch outcome {
	case auth.ResetCreditOutcomeReset:
		return chatwidget.RateLimitResetOutcomeReset, nil
	case auth.ResetCreditOutcomeAlreadyRedeemed:
		return chatwidget.RateLimitResetOutcomeAlreadyRedeemed, nil
	case auth.ResetCreditOutcomeNothingToReset:
		return chatwidget.RateLimitResetOutcomeNothingToReset, nil
	case auth.ResetCreditOutcomeNoCredit:
		return chatwidget.RateLimitResetOutcomeNoCredit, nil
	default:
		return "", fmt.Errorf("unknown rate limit reset outcome %q", outcome)
	}
}

func runInteractiveRemotePrompt(ctx context.Context, root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint, stdout, stderr io.Writer) error {
	if stdout == nil {
		stdout = io.Discard
	}
	if stderr == nil {
		stderr = io.Discard
	}
	state := interactiveUIState(root)
	messages := make(chan bubbletea.Msg, 256)
	runInteractiveRemoteTurn(ctx, root, endpoint, state, codextea.SubmitRequest{Prompt: root.Prompt}, messages, remoteTUIBrokers{}, nil)
	var turnErr error
	for message := range messages {
		switch msg := message.(type) {
		case codextea.ThreadEventMsg:
			if msg.Event.Delta != nil && msg.Event.Delta.Text != "" {
				_, _ = io.WriteString(stdout, msg.Event.Delta.Text)
			}
			if msg.Event.Error != nil && strings.TrimSpace(msg.Event.Error.Message) != "" {
				turnErr = errors.New(strings.TrimSpace(msg.Event.Error.Message))
			}
		case codextea.TurnCompletedMsg:
			if msg.Err != nil {
				turnErr = msg.Err
			}
		}
	}
	if turnErr != nil {
		fmt.Fprintln(stderr, turnErr.Error())
	}
	return turnErr
}

func interactiveRemoteSessionPickerItems(ctx context.Context, root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint) []codextui.SessionSummary {
	client, err := openRemoteSessionClient(ctx, endpoint)
	if err != nil {
		return nil
	}
	defer client.close()
	items := []codextui.SessionSummary{}
	for _, archived := range []bool{false, true} {
		params := remoteTUIThreadListParams(root, archived)
		response, err := remoteThreadListWithCwdFallback(ctx, client, params)
		if err != nil {
			return nil
		}
		for i := range response.Data {
			if summary := remoteTUISessionSummaryFromThread(&response.Data[i], archived); summary != nil {
				items = append(items, *summary)
			}
		}
	}
	sort.SliceStable(items, func(i, j int) bool {
		if items[i].UpdatedAt.Equal(items[j].UpdatedAt) {
			return items[i].ThreadID > items[j].ThreadID
		}
		return items[i].UpdatedAt.After(items[j].UpdatedAt)
	})
	return items
}

func interactiveRemoteSessionActionHandler(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.SessionActionFunc {
	return func(selection codextui.SessionSelection) (*codextui.SessionSummary, error) {
		threadID := strings.TrimSpace(selection.Target.ThreadID)
		if threadID == "" {
			return nil, errors.New("remote session action requires a thread id")
		}
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return nil, err
		}
		defer client.close()
		switch selection.Kind {
		case codextui.SessionSelectionFork:
			var response appserver.ThreadForkResponse
			params := appserver.ThreadForkParams{
				ThreadID:    threadID,
				HistoryMode: session.ForkAll,
			}
			if err := remoteSessionRequest(ctx, client, appserver.MethodThreadFork, params, &response); err != nil {
				return nil, err
			}
			// Rust inherits the parent's task-tool capability for the fork.
			if response.Thread != nil {
				inheritRemoteTaskToolCapability(auth.DefaultCodexHome(), threadID, response.Thread.ID)
			}
			return remoteTUISessionSummaryFromThread(response.Thread, false), nil
		case codextui.SessionSelectionArchive:
			var response appserver.ThreadArchiveResponse
			if err := remoteSessionRequest(ctx, client, appserver.MethodThreadArchive, appserver.ThreadArchiveParams{ThreadID: threadID}, &response); err != nil {
				return nil, err
			}
			return nil, nil
		case codextui.SessionSelectionUnarchive:
			var response appserver.ThreadUnarchiveResponse
			if err := remoteSessionRequest(ctx, client, appserver.MethodThreadUnarchive, appserver.ThreadUnarchiveParams{ThreadID: threadID}, &response); err != nil {
				return nil, err
			}
			return remoteTUISessionSummaryFromThread(response.Thread, false), nil
		case codextui.SessionSelectionDelete:
			var response appserver.ThreadDeleteResponse
			if err := remoteSessionRequest(ctx, client, appserver.MethodThreadDelete, appserver.ThreadDeleteParams{ThreadID: threadID}, &response); err != nil {
				return nil, err
			}
			return nil, nil
		default:
			return nil, nil
		}
	}
}

func interactiveRemoteRenameThreadHandler(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.ThreadRenameFunc {
	return func(threadID string, name string) error {
		threadID = strings.TrimSpace(threadID)
		name = strings.TrimSpace(name)
		if threadID == "" {
			return errors.New("remote rename requires a thread id")
		}
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return err
		}
		defer client.close()
		var response appserver.ThreadSetNameResponse
		return remoteSessionRequest(ctx, client, appserver.MethodThreadNameSet, appserver.ThreadSetNameParams{ThreadID: threadID, Name: name}, &response)
	}
}

func interactiveRemoteLogoutHandler(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.LogoutFunc {
	return func() error {
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return err
		}
		defer client.close()
		var response auth.LogoutAccountResponse
		return remoteSessionRequest(ctx, client, appserver.MethodLogoutAccount, map[string]any{}, &response)
	}
}

func interactiveRemoteResumeSessionHandler(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) codextea.SessionResumeFunc {
	return func(selection codextui.SessionSelection) (codextea.SessionResumeResponse, error) {
		threadID := strings.TrimSpace(selection.Target.ThreadID)
		if threadID == "" {
			return codextea.SessionResumeResponse{}, errors.New("remote resume requires a thread id")
		}
		client, err := openRemoteSessionClient(ctx, endpoint)
		if err != nil {
			return codextea.SessionResumeResponse{}, err
		}
		defer client.close()
		// Rust #43253: resume through the app server so the server owns the
		// writer and reports the thread's saved settings. When another app owns
		// the conversation the resume fails with the active-writer conflict and
		// the TUI falls back to a read-only history snapshot.
		var resumed appserver.ThreadResumeResponse
		resumeErr := remoteSessionRequest(ctx, client, appserver.MethodThreadResume, appserver.ThreadResumeParams{ThreadID: threadID}, &resumed)
		if resumeErr == nil && resumed.Thread != nil {
			response := remoteTUIResumeResponseFromThread(resumed.Thread)
			response.ThreadSettings = remoteTUISettingsFromResume(&resumed)
			return response, nil
		}
		if !remoteTUIResumeConflict(resumeErr) {
			return codextea.SessionResumeResponse{}, resumeErr
		}
		thread, err := remoteTUIReadThread(ctx, client, threadID, true)
		if err != nil {
			return codextea.SessionResumeResponse{}, err
		}
		response := remoteTUIResumeResponseFromThread(thread)
		response.ReadOnly = true
		return response, nil
	}
}

// remoteTUIResumeResponseFromThread builds the TUI resume response from a
// thread snapshot shared by the resume and read-only fallback paths.
func remoteTUIResumeResponseFromThread(thread *appserver.Thread) codextea.SessionResumeResponse {
	return codextea.SessionResumeResponse{
		Summary:                remoteTUISessionSummaryFromThread(thread, false),
		Messages:               remoteTUIThreadMessagesFromThread(thread, reasoningProjectionChatWidget, false),
		Status:                 remoteTUIStatusFromThread(thread),
		TokenUsage:             remoteThreadTokenUsageFromThread(thread),
		WorkingStatusHeader:    remoteTUIThreadActiveReasoningHeading(thread),
		WorkingReasoningTurnID: remoteTUIThreadActiveReasoningTurnID(thread),
		WorkingReasoningItemID: remoteTUIThreadActiveReasoningItemID(thread),
		CompletedTurns:         remoteTUICompletedTurnCount(thread),
	}
}

// remoteTUICompletedTurnCount counts a thread's completed turns for the
// automatic-recap accounting (Rust RecapProgress::from_turns).
func remoteTUICompletedTurnCount(thread *appserver.Thread) int {
	if thread == nil {
		return 0
	}
	count := 0
	for _, turn := range thread.Turns {
		if strings.EqualFold(strings.TrimSpace(string(turn.Status)), string(appserver.TurnStatusCompleted)) {
			count++
		}
	}
	return count
}

// remoteTUIResumeConflict reports whether a resume failed because another owner
// holds the thread's writer (Rust #43253 external writer detection).
func remoteTUIResumeConflict(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "already has an active writer")
}

// remoteTUISettingsFromResume converts a resume response's thread settings into
// the settings shape the TUI applies (Rust #43330/#43340).
func remoteTUISettingsFromResume(resumed *appserver.ThreadResumeResponse) *appserver.Settings {
	if resumed == nil {
		return nil
	}
	settings := appserver.Settings{
		CWD:                   strings.TrimSpace(resumed.CWD),
		Model:                 strings.TrimSpace(resumed.Model),
		ModelProvider:         strings.TrimSpace(resumed.ModelProvider),
		ApprovalPolicy:        remoteSettingsString(resumed.ApprovalPolicy),
		SandboxPolicy:         remoteSettingsString(resumed.Sandbox),
		Effort:                resumed.ReasoningEffort,
		ServiceTier:           resumed.ServiceTier,
		DisabledPluginIDs:     append([]string(nil), resumed.DisabledPluginIDs...),
		RuntimeWorkspaceRoots: append([]string(nil), resumed.RuntimeWorkspaceRoots...),
	}
	if resumed.ApprovalsReviewer != nil {
		settings.ApprovalsReviewer = strings.TrimSpace(*resumed.ApprovalsReviewer)
	}
	if resumed.ActivePermissionProfile != nil {
		if id := strings.TrimSpace(resumed.ActivePermissionProfile.ID); id != "" {
			settings.ActivePermissionProfile = &id
		}
	}
	return &settings
}

// remoteTUISettingsFromStartedThread maps a freshly started thread's settings
// into the dashboard's switch response (Rust #45255 attaches the destination
// settings for a session started from the command center).
func remoteTUISettingsFromStartedThread(started *appserver.ThreadStartResponse) *appserver.Settings {
	if started == nil {
		return nil
	}
	settings := appserver.Settings{
		CWD:                   strings.TrimSpace(started.CWD),
		Model:                 strings.TrimSpace(started.Model),
		ModelProvider:         strings.TrimSpace(started.ModelProvider),
		ApprovalPolicy:        remoteSettingsString(started.ApprovalPolicy),
		SandboxPolicy:         remoteSettingsString(started.Sandbox),
		Effort:                started.ReasoningEffort,
		ServiceTier:           started.ServiceTier,
		DisabledPluginIDs:     append([]string(nil), started.DisabledPluginIDs...),
		RuntimeWorkspaceRoots: append([]string(nil), started.RuntimeWorkspaceRoots...),
	}
	if started.ApprovalsReviewer != nil {
		settings.ApprovalsReviewer = strings.TrimSpace(*started.ApprovalsReviewer)
	}
	if started.ActivePermissionProfile != nil {
		if id := strings.TrimSpace(started.ActivePermissionProfile.ID); id != "" {
			settings.ActivePermissionProfile = &id
		}
	}
	return &settings
}

// remoteTUIAgentSwitchResponseForStartedSession builds the dashboard's switch
// response from a thread started without a turn. An untouched thread has no
// rollout, so thread/resume would fail; the start response carries everything
// the attach needs (Rust #45255 blank_sessions).
func remoteTUIAgentSwitchResponseForStartedSession(started *appserver.ThreadStartResponse) codextea.AgentThreadSwitchResponse {
	var thread *appserver.Thread
	if started != nil {
		thread = started.Thread
	}
	response := remoteTUIAgentSwitchResponseFromThread(thread)
	response.ThreadSettings = remoteTUISettingsFromStartedThread(started)
	return response
}

// remoteSettingsString normalizes a settings value that may arrive as a plain
// string or a structured object.
func remoteSettingsString(value any) string {
	if text, ok := value.(string); ok {
		return strings.TrimSpace(text)
	}
	return ""
}

func remoteTUIThreadListParams(root *cli.RootOptions, archived bool) appserver.ThreadListParams {
	limit := codextui.SessionPickerPageSize
	params := appserver.ThreadListParams{
		Limit:         &limit,
		SortKey:       appserver.SortRecencyAt,
		SortDirection: appserver.SortDesc,
		Archived:      &archived,
		SourceKinds: []appserver.ThreadSourceKind{
			appserver.ThreadSourceKindCli,
			appserver.ThreadSourceKindVsCode,
		},
	}
	if cwd := interactiveSessionPickerCWD(root); cwd != "" {
		settings := interactiveTUISettings(root)
		params.CWD = &appserver.ThreadListCwdFilter{
			Values: codextui.SessionPickerWorktreeCWDs(cwd, features.Enabled(settings.FeatureSettings, "worktrees")),
		}
	}
	return params
}

// remoteThreadListWithCwdFallback lists threads, retrying once with the
// originally requested directory when an older daemon rejects the array-valued
// cwd filter (Rust #43279 app_server_session/thread_list.rs). Discovery orders
// the requested directory first, so the first value is the fallback.
func remoteThreadListWithCwdFallback(ctx context.Context, client *remoteAppServerTUIClient, params appserver.ThreadListParams) (*appserver.ThreadListResponse, error) {
	for {
		var response appserver.ThreadListResponse
		err := remoteSessionRequest(ctx, client, appserver.MethodThreadList, params, &response)
		if err == nil {
			return &response, nil
		}
		var rpc *remoteRPCError
		if !errors.As(err, &rpc) {
			return nil, err
		}
		if rpc.Code != appserver.JSONRPCInvalidRequestErrorCode &&
			rpc.Code != appserver.JSONRPCInvalidParamsErrorCode {
			return nil, err
		}
		if !strings.Contains(rpc.Message, "invalid type: sequence") ||
			!strings.Contains(rpc.Message, "expected a string") {
			return nil, err
		}
		// Only a multi-directory filter is retried; Rust matches
		// ThreadListCwdFilter::Many here, and discovery orders the requested
		// directory first.
		if params.CWD == nil || len(params.CWD.Values) < 2 {
			return nil, err
		}
		params.CWD = &appserver.ThreadListCwdFilter{Values: params.CWD.Values[:1]}
	}
}

func remoteTUISessionSummaryFromThread(thread *appserver.Thread, archived bool) *codextui.SessionSummary {
	if thread == nil || strings.TrimSpace(thread.ID) == "" {
		return nil
	}
	record := sessionRecordFromAppServerThread(thread, archived)
	path := ""
	if thread.Path != nil {
		path = strings.TrimSpace(*thread.Path)
	}
	branch := ""
	if thread.GitInfo != nil && thread.GitInfo.Branch != nil {
		branch = strings.TrimSpace(*thread.GitInfo.Branch)
	}
	return &codextui.SessionSummary{
		ThreadID:  string(record.ID),
		Path:      path,
		Title:     record.Title,
		Preview:   record.Preview,
		CWD:       record.Metadata.CWD,
		Branch:    branch,
		Provider:  record.Metadata.ModelProvider,
		CreatedAt: record.CreatedAt,
		UpdatedAt: sessionRecordRecency(record),
		Archived:  record.Archived,
	}
}

func interactiveRemoteAgentThreadEntries(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, currentThreadID string) ([]codextui.AgentThreadEntry, error) {
	currentThreadID = strings.TrimSpace(currentThreadID)
	if currentThreadID == "" {
		return nil, nil
	}
	client, err := openRemoteSessionClient(ctx, endpoint)
	if err != nil {
		return nil, err
	}
	defer client.close()

	primaryThreadID, seedThreads, err := remoteTUIResolveAgentPrimaryThread(ctx, client, currentThreadID)
	if err != nil {
		return nil, err
	}
	threadsByID := map[string]*appserver.Thread{}
	for i := range seedThreads {
		thread := seedThreads[i]
		if thread != nil && strings.TrimSpace(thread.ID) != "" {
			threadsByID[strings.TrimSpace(thread.ID)] = thread
		}
	}
	loadedIDs, err := remoteTUILoadedThreadIDs(ctx, client)
	if err == nil {
		for _, threadID := range loadedIDs {
			threadID = strings.TrimSpace(threadID)
			if threadID == "" || threadsByID[threadID] != nil {
				continue
			}
			thread, readErr := remoteTUIReadThread(ctx, client, threadID, false)
			if readErr != nil || thread == nil {
				continue
			}
			threadsByID[threadID] = thread
		}
	}

	entries := []codextui.AgentThreadEntry{}
	if primary := threadsByID[primaryThreadID]; primary != nil {
		entries = appendAgentEntryUnique(entries, remoteTUIAgentEntryFromThread(primary, primaryThreadID))
	}
	if current := threadsByID[currentThreadID]; current != nil {
		entries = appendAgentEntryUnique(entries, remoteTUIAgentEntryFromThread(current, primaryThreadID))
	}
	for _, threadID := range loadedIDs {
		threadID = strings.TrimSpace(threadID)
		thread := threadsByID[threadID]
		if thread == nil || threadID == primaryThreadID || threadID == currentThreadID {
			continue
		}
		if !remoteTUIAgentThreadDescendsFrom(thread, primaryThreadID, threadsByID) {
			continue
		}
		entries = appendAgentEntryUnique(entries, remoteTUIAgentEntryFromThread(thread, primaryThreadID))
	}
	return entries, nil
}

func interactiveRemoteSwitchAgentThread(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, threadID string) (codextea.AgentThreadSwitchResponse, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return codextea.AgentThreadSwitchResponse{}, errors.New("agent switch requires a thread id")
	}
	client, err := openRemoteSessionClient(ctx, endpoint)
	if err != nil {
		return codextea.AgentThreadSwitchResponse{}, err
	}
	defer client.close()
	// Rust #44969: attach through thread/resume so the server owns the writer and
	// reports the thread's settings. When another app server already owns the
	// task the resume fails with the active-writer conflict, so fall back to a
	// frozen read-only history snapshot instead of refusing to open it.
	var resumed appserver.ThreadResumeResponse
	resumeErr := remoteSessionRequest(ctx, client, appserver.MethodThreadResume, appserver.ThreadResumeParams{ThreadID: threadID}, &resumed)
	if resumeErr == nil && resumed.Thread != nil {
		response := remoteTUIAgentSwitchResponseFromThread(resumed.Thread)
		response.ThreadSettings = remoteTUISettingsFromResume(&resumed)
		return response, nil
	}
	if !remoteTUIResumeConflict(resumeErr) {
		return codextea.AgentThreadSwitchResponse{}, resumeErr
	}
	thread, err := remoteTUIReadThread(ctx, client, threadID, true)
	if err != nil {
		return codextea.AgentThreadSwitchResponse{}, err
	}
	response := remoteTUIAgentSwitchResponseFromThread(thread)
	response.ReadOnly = true
	return response, nil
}

// remoteTUIAgentSwitchResponseFromThread builds the agent-switch response from
// a thread snapshot shared by the resume and read-only fallback paths.
func remoteTUIAgentSwitchResponseFromThread(thread *appserver.Thread) codextea.AgentThreadSwitchResponse {
	threadID := ""
	if thread != nil {
		threadID = strings.TrimSpace(thread.ID)
	}
	primaryThreadID := threadID
	if thread != nil && thread.ParentThreadID != nil && strings.TrimSpace(*thread.ParentThreadID) != "" {
		primaryThreadID = strings.TrimSpace(*thread.ParentThreadID)
	}
	return codextea.AgentThreadSwitchResponse{
		Entry:                  remoteTUIAgentEntryFromThread(thread, primaryThreadID),
		Messages:               remoteTUIThreadMessagesFromThread(thread, reasoningProjectionChatWidget, false),
		Status:                 remoteTUIStatusFromThread(thread),
		Model:                  remoteTUIThreadModel(thread),
		Provider:               remoteTUIThreadProvider(thread),
		WorkingStatusHeader:    remoteTUIThreadActiveReasoningHeading(thread),
		WorkingReasoningTurnID: remoteTUIThreadActiveReasoningTurnID(thread),
		WorkingReasoningItemID: remoteTUIThreadActiveReasoningItemID(thread),
	}
}

func remoteTUIResolveAgentPrimaryThread(ctx context.Context, client *remoteAppServerTUIClient, currentThreadID string) (string, []*appserver.Thread, error) {
	current, err := remoteTUIReadThread(ctx, client, currentThreadID, false)
	if err != nil {
		return "", nil, err
	}
	threads := []*appserver.Thread{current}
	primaryThreadID := strings.TrimSpace(currentThreadID)
	seen := map[string]bool{primaryThreadID: true}
	for current != nil && current.ParentThreadID != nil {
		parentID := strings.TrimSpace(*current.ParentThreadID)
		if parentID == "" || seen[parentID] {
			break
		}
		seen[parentID] = true
		parent, readErr := remoteTUIReadThread(ctx, client, parentID, false)
		if readErr != nil || parent == nil {
			primaryThreadID = parentID
			break
		}
		threads = append(threads, parent)
		primaryThreadID = parentID
		current = parent
	}
	return primaryThreadID, threads, nil
}

func remoteTUILoadedThreadIDs(ctx context.Context, client *remoteAppServerTUIClient) ([]string, error) {
	ids := []string{}
	var cursor *string
	for {
		params := appserver.ThreadLoadedListParams{Cursor: cursor}
		var response appserver.ThreadLoadedListResponse
		if err := remoteSessionRequest(ctx, client, appserver.MethodThreadLoadedList, params, &response); err != nil {
			return ids, err
		}
		ids = append(ids, response.Data...)
		if response.NextCursor == nil || strings.TrimSpace(*response.NextCursor) == "" {
			return ids, nil
		}
		cursor = response.NextCursor
	}
}

func remoteTUIReadThread(ctx context.Context, client *remoteAppServerTUIClient, threadID string, includeTurns bool) (*appserver.Thread, error) {
	var response appserver.ThreadReadResponse
	params := appserver.ThreadReadParams{ThreadID: strings.TrimSpace(threadID), IncludeTurns: includeTurns}
	if err := remoteSessionRequest(ctx, client, appserver.MethodThreadRead, params, &response); err != nil {
		return nil, err
	}
	if response.Thread == nil || strings.TrimSpace(response.Thread.ID) == "" {
		return nil, fmt.Errorf("thread/read returned no thread for %s", strings.TrimSpace(threadID))
	}
	return response.Thread, nil
}

// remoteTUIThreadModel / remoteTUIThreadProvider read the server-reported
// thread metadata the TUI applies when switching or resuming (Rust #43360).
func remoteTUIThreadModel(thread *appserver.Thread) string {
	if thread == nil || thread.Model == nil {
		return ""
	}
	return strings.TrimSpace(*thread.Model)
}

func remoteTUIThreadProvider(thread *appserver.Thread) string {
	if thread == nil {
		return ""
	}
	return strings.TrimSpace(thread.ModelProvider)
}

func remoteTUIAgentEntryFromThread(thread *appserver.Thread, primaryThreadID string) codextui.AgentThreadEntry {
	if thread == nil {
		return codextui.AgentThreadEntry{}
	}
	return codextui.AgentThreadEntry{
		ThreadID:      strings.TrimSpace(thread.ID),
		AgentNickname: strings.TrimSpace(stringPtrValue(thread.AgentNickname)),
		AgentRole:     strings.TrimSpace(stringPtrValue(thread.AgentRole)),
		IsPrimary:     strings.TrimSpace(thread.ID) == strings.TrimSpace(primaryThreadID),
		IsRunning:     strings.EqualFold(strings.TrimSpace(thread.Status.Type), "active"),
		IsClosed:      strings.EqualFold(strings.TrimSpace(thread.Status.Type), "notLoaded"),
	}
}

func appendAgentEntryUnique(entries []codextui.AgentThreadEntry, entry codextui.AgentThreadEntry) []codextui.AgentThreadEntry {
	threadID := strings.TrimSpace(entry.ThreadID)
	if threadID == "" {
		return entries
	}
	for i := range entries {
		if strings.TrimSpace(entries[i].ThreadID) == threadID {
			if entries[i].AgentNickname == "" {
				entries[i].AgentNickname = entry.AgentNickname
			}
			if entries[i].AgentRole == "" {
				entries[i].AgentRole = entry.AgentRole
			}
			entries[i].IsPrimary = entries[i].IsPrimary || entry.IsPrimary
			entries[i].IsRunning = entries[i].IsRunning || entry.IsRunning
			entries[i].IsClosed = entries[i].IsClosed && entry.IsClosed
			return entries
		}
	}
	return append(entries, entry)
}

func remoteTUIAgentThreadDescendsFrom(thread *appserver.Thread, primaryThreadID string, threadsByID map[string]*appserver.Thread) bool {
	primaryThreadID = strings.TrimSpace(primaryThreadID)
	if thread == nil || primaryThreadID == "" {
		return false
	}
	if strings.TrimSpace(thread.ID) == primaryThreadID {
		return true
	}
	parentID := ""
	if thread.ParentThreadID != nil {
		parentID = strings.TrimSpace(*thread.ParentThreadID)
	}
	seen := map[string]bool{}
	for parentID != "" && !seen[parentID] {
		if parentID == primaryThreadID {
			return true
		}
		seen[parentID] = true
		parent := threadsByID[parentID]
		if parent == nil || parent.ParentThreadID == nil {
			return false
		}
		parentID = strings.TrimSpace(*parent.ParentThreadID)
	}
	return false
}

// hasTaskToolsMCP reports whether the session hosts the codex_tui task tools as
// a local MCP server (Rust ThreadToolTransport::Mcp).
func (c *remoteAppServerTUIClient) hasTaskToolsMCP() bool {
	return c != nil && c.taskTools != nil && len(c.taskTools.configValues()) > 0
}

// applyTaskToolTransport selects the thread-tool transport for a thread/start
// request, mirroring Rust ThreadToolTransport::configure: with the hosted MCP
// server the thread drops the dynamic-tools namespace and instead carries the
// `mcp_servers.codex_tui` config override; otherwise the app-server callback
// transport carries the namespace specs.
func (c *remoteAppServerTUIClient) applyTaskToolTransport(params *appserver.ThreadStartParams) {
	if params == nil {
		return
	}
	if overrides := c.taskToolsConfigOverrides(); len(overrides) > 0 {
		params.DynamicTools = nil
		if params.Config == nil {
			params.Config = map[string]any{}
		}
		for key, value := range overrides {
			params.Config[key] = value
		}
		return
	}
	if specs, err := DynamicToolSpecsRaw(); err == nil {
		params.DynamicTools = specs
	}
}

// taskToolsConfigOverrides returns the thread-config overrides that point a
// thread at the hosted MCP server (nil when the session uses the callback
// transport).
func (c *remoteAppServerTUIClient) taskToolsConfigOverrides() map[string]any {
	if c == nil || c.taskTools == nil {
		return nil
	}
	return c.taskTools.configValues()
}

func remoteTUIStatusFromThread(thread *appserver.Thread) string {
	if thread == nil {
		return "idle"
	}
	switch strings.TrimSpace(thread.Status.Type) {
	case "active":
		return "running"
	case "systemError":
		return "error"
	default:
		return "idle"
	}
}

func remoteTUIThreadMessagesFromThread(thread *appserver.Thread, projection reasoningProjection, showRawReasoning bool) []codextui.Message {
	if thread == nil {
		return nil
	}
	messages := []codextui.Message{}
	inReviewMode := false
	for _, turn := range thread.Turns {
		for _, item := range turn.Items {
			itemType := remoteTUINormalizedThreadItemType(item.Type)
			switch itemType {
			case "enteredreviewmode":
				hint := strings.TrimSpace(firstNonEmptyLocal(item.Text, remoteTUIAnyString(item.Data["review"])))
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
			if remoteTUIThreadItemIsReviewUserMessage(item) || (inReviewMode && remoteTUINormalizedThreadItemRole(item.Role) == "user") {
				continue
			}
			message, ok := remoteTUIMessageFromThreadItem(item, projection, showRawReasoning)
			if ok {
				messages = append(messages, message)
			}
		}
		// Rust #43558: restore the saved completion metadata after each
		// completed turn's items. Replay never falls back to the local clock.
		if footer, ok := remoteTUICompletionFooterMessage(turn); ok {
			messages = append(messages, footer)
		}
		if turn.Error != nil && strings.TrimSpace(turn.Error.Message) != "" {
			messages = append(messages, codextui.Message{Role: codextui.RoleSystem, Text: "Error: " + strings.TrimSpace(turn.Error.Message)})
		}
	}
	return messages
}

// remoteTUIThreadActiveReasoning returns the live reasoning item of a thread
// whose latest turn is in progress and whose trailing item is a reasoning item
// (Rust #43921). The heading is the item's latest usable summary line.
func remoteTUIThreadActiveReasoning(thread *appserver.Thread) (turnID string, itemID string, heading string, ok bool) {
	if thread == nil || len(thread.Turns) == 0 {
		return "", "", "", false
	}
	turn := thread.Turns[len(thread.Turns)-1]
	if turn.Status != appserver.TurnStatusInProgress || len(turn.Items) == 0 {
		return "", "", "", false
	}
	item := turn.Items[len(turn.Items)-1]
	if remoteTUINormalizedThreadItemType(item.Type) != "reasoning" {
		return "", "", "", false
	}
	// The heading follows the summary projection; a session that enables raw
	// reasoning re-derives it from the matching transcript entry, so raw
	// chain-of-thought never leaks into the status row by default
	// (Rust RawReasoningVisibility::Hidden).
	summary, _ := reasoningBlockVariants(
		remoteTUIThreadItemReasoningSummaryParts(item),
		nil,
		remoteTUIThreadItemReasoningText(item),
	)
	if line, found := chatwidget.LatestSummaryLine(summary); found {
		heading = line
	}
	return turn.ID, item.ID, heading, true
}

// remoteTUIThreadActiveReasoningHeading returns just the live reasoning heading
// for a resumed or switched-to thread, or "" when none applies.
func remoteTUIThreadActiveReasoningHeading(thread *appserver.Thread) string {
	if _, _, heading, ok := remoteTUIThreadActiveReasoning(thread); ok {
		return heading
	}
	return ""
}

// remoteTUIThreadActiveReasoningTurnID returns the resumed in-progress turn's
// id when its trailing item is an active reasoning item (Rust #43921).
func remoteTUIThreadActiveReasoningTurnID(thread *appserver.Thread) string {
	turnID, _, _, ok := remoteTUIThreadActiveReasoning(thread)
	if !ok {
		return ""
	}
	return turnID
}

// remoteTUIThreadActiveReasoningItemID returns the active reasoning item's id
// for a resumed or switched-to thread (Rust #43921).
func remoteTUIThreadActiveReasoningItemID(thread *appserver.Thread) string {
	_, itemID, _, ok := remoteTUIThreadActiveReasoning(thread)
	if !ok {
		return ""
	}
	return itemID
}

// remoteTUICompletionFooterMessage restores a completed turn's saved completion
// metadata as a transcript history line (Rust #43558).
func remoteTUICompletionFooterMessage(turn appserver.Turn) (codextui.Message, bool) {
	if turn.Status != appserver.TurnStatusCompleted {
		return codextui.Message{}, false
	}
	var elapsedSeconds *int64
	if turn.DurationMS != nil && *turn.DurationMS >= 0 {
		value := *turn.DurationMS / 1000
		elapsedSeconds = &value
	}
	cell := historycell.NewFinalMessageSeparator(elapsedSeconds, nil)
	if turn.CompletedAt != nil {
		cell = cell.WithCompletedAt(time.Unix(*turn.CompletedAt, 0).Local())
	}
	raw := cell.RawLines()
	if len(raw) == 0 {
		return codextui.Message{}, false
	}
	return codextui.Message{Role: codextui.RoleHistory, Text: raw[0], RawText: raw[0]}, true
}

func remoteTUIThreadItemIsReviewUserMessage(item appserver.ThreadItem) bool {
	return strings.TrimSpace(remoteTUIAnyString(item.Data["kind"])) == "review_rollout_user"
}

func remoteTUIMessageFromThreadItem(item appserver.ThreadItem, projection reasoningProjection, showRawReasoning bool) (codextui.Message, bool) {
	itemType := remoteTUINormalizedThreadItemType(item.Type)
	role := remoteTUINormalizedThreadItemRole(item.Role)
	switch {
	case itemType == "usermessage" || role == "user":
		text := remoteTUIThreadItemUserText(item)
		localImages, remoteImages := remoteTUIThreadItemPromptImages(item.Content)
		return codextui.Message{
			Role:                   codextui.RoleUser,
			Text:                   text,
			RawText:                text,
			UserPrompt:             text,
			UserPromptLocalImages:  localImages,
			UserPromptRemoteImages: remoteImages,
		}, strings.TrimSpace(text) != ""
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
		text, raw := reasoningProjectionText(item, projection, showRawReasoning)
		if text == "" && raw == "" {
			return codextui.Message{}, false
		}
		// A reasoning item is retained in the expanded transcript only (Rust
		// new_reasoning_summary_block is transcript-only), and its id lets the
		// live completion replace the restored snapshot instead of duplicating
		// it (Rust ReasoningReplay).
		return codextui.Message{
			Role:             codextui.RoleHistory,
			Text:             text,
			RawText:          text,
			ReasoningRawText: raw,
			TranscriptOnly:   true,
			ItemID:           strings.TrimSpace(item.ID),
		}, true
	case itemType == "commandexecution" || itemType == "mcptoolcall" || itemType == "dynamictoolcall" || itemType == "collabagenttoolcall" || itemType == "subagentactivity":
		text := remoteTUIThreadItemToolText(item)
		if text == "" {
			return codextui.Message{}, false
		}
		return codextui.Message{Role: codextui.RoleHistory, Text: text, RawText: text}, true
	default:
		text := strings.TrimSpace(item.Text)
		if text == "" {
			return codextui.Message{}, false
		}
		return codextui.Message{Role: codextui.RoleHistory, Text: text, RawText: text}, true
	}
}

func remoteTUINormalizedThreadItemType(value string) string {
	value = strings.TrimSpace(value)
	value = strings.ReplaceAll(value, "_", "")
	value = strings.ReplaceAll(value, "-", "")
	return strings.ToLower(value)
}

func remoteTUINormalizedThreadItemRole(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func remoteTUIThreadItemUserText(item appserver.ThreadItem) string {
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

// remoteTUIThreadItemPromptImages splits a persisted user message's content into
// the local file paths and remote URLs a backtracked prompt restores (Rust
// user_message_display_from_inputs: LocalImage vs Image).
func remoteTUIThreadItemPromptImages(contents []appserver.ThreadItemContent) (localImages []string, remoteImages []string) {
	for _, content := range contents {
		switch normalizedThreadItemContentType(content.Type) {
		case "localimage":
			if path := strings.TrimSpace(content.ImageURL); path != "" {
				localImages = append(localImages, path)
			}
		case "inputimage", "image":
			if url := strings.TrimSpace(content.ImageURL); url != "" {
				remoteImages = append(remoteImages, url)
			}
		}
	}
	return localImages, remoteImages
}

// normalizedThreadItemContentType lowercases a content type and drops separators
// so local_image/localImage and input_image/inputImage both match.
func normalizedThreadItemContentType(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.ReplaceAll(value, "_", "")
	value = strings.ReplaceAll(value, "-", "")
	return value
}

func remoteTUIThreadItemReasoningText(item appserver.ThreadItem) string {
	parts := []string{}
	for _, key := range []string{"summary", "reasoningContent", "content"} {
		parts = append(parts, remoteTUIAnyStrings(item.Data[key])...)
	}
	if strings.TrimSpace(item.Text) != "" {
		parts = append(parts, strings.TrimSpace(item.Text))
	}
	return strings.TrimSpace(strings.Join(parts, "\n"))
}

// remoteTUIThreadItemReasoningSummaryParts returns a reasoning item's summary
// parts (Rust ThreadItem::Reasoning { summary }).
func remoteTUIThreadItemReasoningSummaryParts(item appserver.ThreadItem) []string {
	parts := []string{}
	for _, key := range []string{"summary", "summary_text"} {
		parts = append(parts, remoteTUIAnyStrings(item.Data[key])...)
	}
	return parts
}

// remoteTUIThreadItemReasoningContentParts returns a reasoning item's raw
// chain-of-thought parts (Rust ThreadItem::Reasoning { content }).
func remoteTUIThreadItemReasoningContentParts(item appserver.ThreadItem) []string {
	parts := []string{}
	for _, key := range []string{"reasoningContent", "content", "raw_content"} {
		parts = append(parts, remoteTUIAnyStrings(item.Data[key])...)
	}
	return parts
}

// reasoningBlockVariants mirrors Rust's reasoning-item projection: the summary
// parts split into the transcript-only block, and a second variant chains the
// raw content on for RawReasoningVisibility::Visible
// (show_raw_agent_reasoning). fallback is the item's joined text, used only
// when the item carries no structured reasoning parts at all.
func reasoningBlockVariants(summaryParts []string, contentParts []string, fallback string) (summary string, raw string) {
	if len(summaryParts) == 0 && len(contentParts) == 0 {
		return strings.TrimSpace(fallback), ""
	}
	summary = strings.TrimSpace(historycell.NewReasoningSummaryBlock(summaryParts).Content)
	if len(contentParts) > 0 {
		combined := append(append([]string(nil), summaryParts...), contentParts...)
		raw = strings.TrimSpace(historycell.NewReasoningSummaryBlock(combined).Content)
	}
	return summary, raw
}

// reasoningProjection names which Rust reasoning projection an item-to-message
// conversion follows. The in-session chatwidget replays a reasoning item as a
// transcript-only block built from the summary parts (plus the raw parts when
// visible); the session-transcript pager projects the *cell* form, where the raw
// content replaces the split summary outright (thread_transcript.rs).
type reasoningProjection string

const (
	reasoningProjectionChatWidget       reasoningProjection = "chat_widget"
	reasoningProjectionThreadTranscript reasoningProjection = "thread_transcript"
)

// reasoningProjectionText reports the reasoning entry's text and the raw
// chain-of-thought variant the transcript-only renderer selects when raw
// reasoning is visible.
func reasoningProjectionText(item appserver.ThreadItem, projection reasoningProjection, showRawReasoning bool) (text string, raw string) {
	summaryParts := remoteTUIThreadItemReasoningSummaryParts(item)
	contentParts := remoteTUIThreadItemReasoningContentParts(item)
	if projection == reasoningProjectionThreadTranscript {
		// thread_transcript.rs: with raw reasoning visible and content present
		// the cell shows only the raw content under a "Reasoning" heading;
		// otherwise it shows the split summary.
		if showRawReasoning && len(contentParts) > 0 {
			return strings.TrimSpace(strings.Join(contentParts, "\n\n")), ""
		}
		summary, _ := reasoningBlockVariants(summaryParts, nil, remoteTUIThreadItemReasoningText(item))
		return summary, ""
	}
	return reasoningBlockVariants(summaryParts, contentParts, remoteTUIThreadItemReasoningText(item))
}

func remoteTUIThreadItemToolText(item appserver.ThreadItem) string {
	title := remoteTUIFirstNonEmptyString(
		remoteTUIThreadItemDataString(item, "command", "cmd", "tool", "name"),
		strings.TrimSpace(item.Name),
		strings.TrimSpace(item.Type),
		"tool",
	)
	lines := []string{title}
	if status := remoteTUIFirstNonEmptyString(strings.TrimSpace(item.Status), remoteTUIThreadItemDataString(item, "status")); status != "" {
		lines = append(lines, "status: "+status)
	}
	for _, key := range []string{"arguments", "input", "output", "aggregatedOutput", "formattedOutput", "result"} {
		if value := remoteTUIThreadItemDataString(item, key); value != "" {
			lines = append(lines, value)
			break
		}
	}
	if strings.TrimSpace(item.Text) != "" {
		lines = append(lines, strings.TrimSpace(item.Text))
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func remoteTUIFirstNonEmptyString(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func remoteTUIThreadItemDataString(item appserver.ThreadItem, keys ...string) string {
	for _, key := range keys {
		if value := remoteTUIAnyString(item.Data[key]); value != "" {
			return value
		}
	}
	return ""
}

func remoteTUIAnyString(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case fmt.Stringer:
		return strings.TrimSpace(typed.String())
	case json.Number:
		return strings.TrimSpace(typed.String())
	case nil:
		return ""
	default:
		data, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return strings.TrimSpace(string(data))
	}
}

func remoteTUIAnyStrings(value any) []string {
	switch typed := value.(type) {
	case []string:
		out := make([]string, 0, len(typed))
		for _, entry := range typed {
			if strings.TrimSpace(entry) != "" {
				out = append(out, strings.TrimSpace(entry))
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, entry := range typed {
			if value := remoteTUIAnyString(entry); value != "" {
				out = append(out, value)
			}
		}
		return out
	default:
		if value := remoteTUIAnyString(typed); value != "" {
			return []string{value}
		}
		return nil
	}
}

func interactiveRemoteTurnCommand(ctx context.Context, root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint, state *codextui.State, request codextea.SubmitRequest, brokers remoteTUIBrokers, interrupts ...*remoteTUIInterruptController) bubbletea.Cmd {
	return interactiveRemoteTurnCommandWithTaskTools(ctx, root, endpoint, state, request, brokers, nil, interrupts...)
}

func interactiveRemoteTurnCommandWithTaskTools(ctx context.Context, root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint, state *codextui.State, request codextea.SubmitRequest, brokers remoteTUIBrokers, taskTools *taskToolsMCPHost, interrupts ...*remoteTUIInterruptController) bubbletea.Cmd {
	return func() bubbletea.Msg {
		messages := make(chan bubbletea.Msg, 256)
		var interrupt *remoteTUIInterruptController
		if len(interrupts) > 0 {
			interrupt = interrupts[0]
		}
		go runInteractiveRemoteTurn(ctx, root, endpoint, state, request, messages, brokers, interrupt, taskTools)
		return codextea.StreamStartedMsg{Messages: messages}
	}
}

// interactiveRemoteSafetyBufferingRetryCommand confirms a safety-buffered
// retry: it interrupts the buffered turn, forks the thread before that turn
// with the server-selected faster model, and starts a retry on the fork (Rust
// #42380).
func interactiveRemoteSafetyBufferingRetryCommand(ctx context.Context, root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint, state *codextui.State, threadID string, turnID string, model string, prompt string, brokers remoteTUIBrokers, taskTools ...*taskToolsMCPHost) bubbletea.Cmd {
	var taskToolsHost *taskToolsMCPHost
	if len(taskTools) > 0 {
		taskToolsHost = taskTools[0]
	}
	return func() bubbletea.Msg {
		messages := make(chan bubbletea.Msg, 256)
		go func() {
			defer close(messages)
			client, err := openRemoteSessionClient(ctx, endpoint)
			if err != nil {
				sendRemoteTurnError(messages, err)
				return
			}
			var interruptResponse any
			if err := remoteSessionRequest(ctx, client, appserver.MethodTurnInterrupt, turn.TurnInterruptParams{ThreadID: strings.TrimSpace(threadID), TurnID: strings.TrimSpace(turnID)}, &interruptResponse); err != nil {
				client.close()
				sendRemoteTurnError(messages, err)
				return
			}
			if err := waitRemoteTurnStopped(ctx, client, strings.TrimSpace(threadID)); err != nil {
				client.close()
				sendRemoteTurnError(messages, err)
				return
			}
			forkParams := appserver.ThreadForkParams{ThreadID: strings.TrimSpace(threadID), BeforeTurnID: strings.TrimSpace(turnID)}
			mergeTaskToolsMCPConfig(&forkParams.Config, taskToolsHost)
			fasterModel := strings.TrimSpace(model)
			if fasterModel != "" {
				forkParams.Model = &fasterModel
			}
			var forkResponse appserver.ThreadForkResponse
			if err := remoteSessionRequest(ctx, client, appserver.MethodThreadFork, forkParams, &forkResponse); err != nil {
				client.close()
				sendRemoteTurnError(messages, err)
				return
			}
			client.close()
			if forkResponse.Thread == nil || strings.TrimSpace(forkResponse.Thread.ID) == "" {
				sendRemoteTurnError(messages, errors.New("safety retry fork did not return a thread"))
				return
			}
			if state != nil {
				state.SetThreadID(strings.TrimSpace(forkResponse.Thread.ID))
			}
			runInteractiveRemoteTurn(ctx, root, endpoint, state, codextea.SubmitRequest{Prompt: strings.TrimSpace(prompt), Model: fasterModel}, messages, brokers, nil, taskToolsHost)
		}()
		return codextea.StreamStartedMsg{Messages: messages}
	}
}

// mergeTaskToolsMCPConfig adds the hosted task-tools server to a fork/resume
// config override map (Rust ThreadToolTransport::configure_mcp).
func mergeTaskToolsMCPConfig(config *map[string]any, host *taskToolsMCPHost) {
	if config == nil {
		return
	}
	overrides := host.configValues()
	if len(overrides) == 0 {
		return
	}
	merged := *config
	if merged == nil {
		merged = map[string]any{}
	}
	for key, value := range overrides {
		merged[key] = value
	}
	*config = merged
}

// waitRemoteTurnStopped polls thread/read until no turn is still in progress,
// so a safety-buffered retry can safely fork the interrupted thread.
func waitRemoteTurnStopped(ctx context.Context, client *remoteAppServerTUIClient, threadID string) error {
	if strings.TrimSpace(threadID) == "" {
		return nil
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		var response appserver.ThreadReadResponse
		if err := remoteSessionRequest(ctx, client, appserver.MethodThreadRead, appserver.ThreadReadParams{ThreadID: threadID, IncludeTurns: true}, &response); err != nil {
			return err
		}
		running := false
		if response.Thread != nil {
			for i := range response.Thread.Turns {
				if strings.EqualFold(strings.TrimSpace(string(response.Thread.Turns[i].Status)), string(appserver.TurnStatusInProgress)) {
					running = true
					break
				}
			}
		}
		if !running {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("timed out waiting for safety-buffered turn to stop")
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func runInteractiveRemoteTurn(ctx context.Context, root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint, state *codextui.State, request codextea.SubmitRequest, messages chan<- bubbletea.Msg, brokers remoteTUIBrokers, interrupts *remoteTUIInterruptController, hosts ...*taskToolsMCPHost) {
	defer close(messages)
	if ctx == nil {
		ctx = context.Background()
	}
	client := &remoteAppServerTUIClient{
		endpoint: endpoint,
		root:     root,
		state:    state,
		messages: messages,
		brokers:  brokers,
		dial:     websocket.Dial,
	}
	if len(hosts) > 0 {
		client.taskTools = hosts[0]
	}
	if err := client.connect(ctx); err != nil {
		sendRemoteTurnError(messages, err)
		return
	}
	defer client.close()
	if err := client.initialize(ctx); err != nil {
		sendRemoteTurnError(messages, err)
		return
	}
	// The session's task-tools MCP server outlives this per-turn client; the
	// client becomes its live connection for the duration of the turn
	// (Rust DynamicToolMcpServer::reconnect/suspend).
	if client.taskTools != nil {
		if template, err := remoteThreadStartParams(root, state); err == nil {
			client.taskTools.attach(client, template, client.registerDynamicToolThread)
			defer client.taskTools.suspend()
		}
	}
	threadID := ""
	if state != nil {
		threadID = strings.TrimSpace(state.ThreadID)
	}
	if threadID == "" {
		var err error
		threadID, err = client.startThread(ctx, root, state)
		if err != nil {
			sendRemoteTurnError(messages, err)
			return
		}
		if state != nil {
			state.SetThreadID(threadID)
		}
	}
	turnID, err := client.startTurn(ctx, root, state, threadID, request)
	if err != nil {
		sendRemoteTurnError(messages, err)
		return
	}
	if interrupts != nil {
		interrupts.setActive(threadID, turnID)
		defer interrupts.clearActive(threadID, turnID)
	}
	if err := client.readUntilTurnCompleted(ctx); err != nil {
		sendRemoteTurnError(messages, err)
		return
	}
	if !client.turnInterrupted {
		messages <- codextea.TurnCompletedMsg{ThreadID: threadID, DurationMS: client.turnDurationMS}
	}
}

func (c *remoteAppServerTUIClient) connect(ctx context.Context) error {
	if c == nil || c.endpoint == nil {
		return errors.New("remote app-server endpoint is required")
	}
	switch c.endpoint.Kind {
	case appserverdaemon.RemoteEndpointWebSocket:
		if strings.TrimSpace(c.endpoint.WebSocketURL) == "" {
			return errors.New("remote app-server websocket URL is required")
		}
		dial := c.dial
		if dial == nil {
			dial = websocket.Dial
		}
		options := &websocket.DialOptions{}
		if c.endpoint.AuthToken != nil && strings.TrimSpace(*c.endpoint.AuthToken) != "" {
			options.HTTPHeader = http.Header{}
			options.HTTPHeader.Set("Authorization", "Bearer "+strings.TrimSpace(*c.endpoint.AuthToken))
		}
		conn, response, err := dial(ctx, c.endpoint.WebSocketURL, options)
		if err != nil {
			return formatRemoteWebSocketDialError(c.endpoint.WebSocketURL, response, err)
		}
		c.transport = &remoteWebSocketTransport{conn: conn}
		return nil
	case appserverdaemon.RemoteEndpointUnixSocket:
		socketPath := strings.TrimSpace(c.endpoint.SocketPath)
		if socketPath == "" {
			return errors.New("remote app-server unix socket path is required")
		}
		dial := c.unixDial
		if dial == nil {
			dial = remoteDialUnixSocket
		}
		conn, err := dial(ctx, socketPath)
		if err != nil {
			return fmt.Errorf("connect remote app-server unix socket %s: %w", socketPath, err)
		}
		c.transport = newRemoteJSONLineTransport(conn)
		return nil
	default:
		return fmt.Errorf("unknown remote app-server endpoint kind %q", c.endpoint.Kind)
	}
}

func (c *remoteAppServerTUIClient) close() {
	if c != nil && c.transport != nil {
		c.transport.close()
	}
}

func remoteDialUnixSocket(ctx context.Context, socketPath string) (net.Conn, error) {
	var dialer net.Dialer
	return dialer.DialContext(ctx, "unix", socketPath)
}

func (c *remoteAppServerTUIClient) initialize(ctx context.Context) error {
	params := appserver.InitializeParams{
		ClientInfo: appserver.ClientInfo{
			Name:    "codex_go_tui",
			Version: doctor.Version(),
		},
		Capabilities: &appserver.InitializeCapabilities{
			ExperimentalAPI:                true,
			MCPServerOpenAIFormElicitation: true,
		},
	}
	id, err := c.sendRequest(ctx, appserver.MethodInitialize, params)
	if err != nil {
		return err
	}
	var response appserver.InitializeResponse
	if err := c.waitResponse(ctx, id, &response); err != nil {
		return err
	}
	c.codexHome = strings.TrimSpace(response.CodexHome)
	return nil
}

func (c *remoteAppServerTUIClient) startThread(ctx context.Context, root *cli.RootOptions, state *codextui.State) (string, error) {
	params, err := c.remoteManagedThreadStartParams(ctx, root, state)
	if err != nil {
		return "", err
	}
	c.applyTaskToolTransport(&params)
	taskToolsAvailable := len(params.DynamicTools) > 0 || c.hasTaskToolsMCP()
	var response appserver.ThreadStartResponse
	for attempt := 0; ; attempt++ {
		id, err := c.sendRequest(ctx, appserver.MethodThreadStart, params)
		if err != nil {
			return "", err
		}
		if err := c.waitResponse(ctx, id, &response); err != nil {
			// Rust request_thread_start_with_history_fallback: a server that does
			// not support the TUI dynamic-tools namespace starts the thread
			// without it instead of failing the bootstrap.
			if attempt == 0 && taskToolsAvailable && remoteDynamicToolsUnsupportedError(err) {
				params.DynamicTools = nil
				taskToolsAvailable = false
				continue
			}
			return "", err
		}
		break
	}
	if response.Thread == nil || strings.TrimSpace(response.Thread.ID) == "" {
		return "", errors.New("thread/start response did not include a thread id")
	}
	threadID := strings.TrimSpace(response.Thread.ID)
	// The TUI only offers task mentions for threads that host the task-tool
	// namespace (Rust AppServerSession::task_tools_available), and persists the
	// capability so a later process can still offer them.
	if taskToolsAvailable {
		rememberRemoteTaskToolThread(auth.DefaultCodexHome(), threadID)
	}
	c.send(codextea.TaskToolsAvailableMsg{ThreadID: threadID, Available: taskToolsAvailable})
	if c.state == nil || strings.TrimSpace(c.state.ThreadID) != threadID {
		c.send(codextea.ThreadEventMsg{Event: protocol.ThreadStarted(threadID)})
	}
	return threadID, nil
}

// remoteDynamicToolsUnsupportedError mirrors Rust's start-thread downgrade
// matcher: an invalid request/params error naming the dynamic-tools surface
// means the server is too old for the namespace.
func remoteDynamicToolsUnsupportedError(err error) bool {
	var rpcErr *remoteRPCError
	if !errors.As(err, &rpcErr) {
		return false
	}
	if rpcErr.Code != jsonRPCInvalidRequestCode && rpcErr.Code != appserver.JSONRPCInvalidParamsErrorCode {
		return false
	}
	message := strings.ToLower(rpcErr.Message)
	for _, field := range []string{"dynamictools", "dynamic tool", "namespace", "inputschema"} {
		if strings.Contains(message, field) {
			return true
		}
	}
	return false
}

func (c *remoteAppServerTUIClient) startTurn(ctx context.Context, root *cli.RootOptions, state *codextui.State, threadID string, request codextea.SubmitRequest) (string, error) {
	params, err := remoteTurnStartParams(root, state, threadID, request)
	if err != nil {
		return "", err
	}
	id, err := c.sendRequest(ctx, appserver.MethodTurnStart, params)
	if err != nil {
		return "", err
	}
	var response turn.TurnStartResponse
	if err := c.waitResponse(ctx, id, &response); err != nil {
		return "", err
	}
	turnID := strings.TrimSpace(response.Turn.ID)
	if turnID == "" {
		return "", errors.New("turn/start response did not include a turn id")
	}
	return turnID, nil
}

func (c *remoteAppServerTUIClient) readUntilTurnCompleted(ctx context.Context) error {
	for c != nil && !c.turnCompleted {
		if err := c.readOne(ctx); err != nil {
			return err
		}
	}
	return nil
}

func (c *remoteAppServerTUIClient) sendRequest(ctx context.Context, method appserver.Method, params any) (int64, error) {
	if c == nil || c.transport == nil {
		return 0, errors.New("remote app-server transport is not connected")
	}
	c.nextRequestID++
	id := c.nextRequestID
	rawParams, err := json.Marshal(params)
	if err != nil {
		return 0, err
	}
	request := appserver.Request{
		JSONRPC: "2.0",
		ID:      appserver.IntID(id),
		Method:  method,
		Params:  rawParams,
	}
	data, err := json.Marshal(&request)
	if err != nil {
		return 0, err
	}
	if err := c.transport.write(ctx, data); err != nil {
		return 0, err
	}
	return id, nil
}

// remoteRPCError carries the JSON-RPC error code and message returned by a
// remote app-server so callers can react to specific legacy-daemon failures
// (Rust #43279's single-directory cwd fallback).
type remoteRPCError struct {
	Code    int
	Message string
}

func (e *remoteRPCError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (c *remoteAppServerTUIClient) waitResponse(ctx context.Context, id int64, target any) error {
	want := fmt.Sprint(id)
	for {
		message, err := c.readRemoteMessage(ctx)
		if err != nil {
			return err
		}
		if len(message.ID) > 0 && strings.TrimSpace(message.Method) == "" {
			got, err := remoteRequestIDString(message.ID)
			if err != nil {
				return err
			}
			if got != want {
				continue
			}
			if message.Error != nil {
				return &remoteRPCError{Code: message.Error.Code, Message: strings.TrimSpace(message.Error.Message)}
			}
			if target != nil && len(message.Result) > 0 {
				if err := json.Unmarshal(message.Result, target); err != nil {
					return err
				}
			}
			return nil
		}
		if strings.TrimSpace(message.Method) != "" {
			if len(message.ID) > 0 {
				_ = c.respondServerRequest(ctx, message)
				continue
			}
			if err := c.handleNotification(message); err != nil {
				return err
			}
		}
	}
}

func (c *remoteAppServerTUIClient) readOne(ctx context.Context) error {
	message, err := c.readRemoteMessage(ctx)
	if err != nil {
		return err
	}
	if strings.TrimSpace(message.Method) == "" {
		if message.Error != nil {
			return errors.New(strings.TrimSpace(message.Error.Message))
		}
		return nil
	}
	if len(message.ID) > 0 {
		return c.respondServerRequest(ctx, message)
	}
	return c.handleNotification(message)
}

func (c *remoteAppServerTUIClient) readRemoteMessage(ctx context.Context) (remoteAppServerMessage, error) {
	var message remoteAppServerMessage
	if c == nil || c.transport == nil {
		return message, errors.New("remote app-server transport is not connected")
	}
	data, err := c.transport.read(ctx)
	if err != nil {
		return message, err
	}
	if err := json.Unmarshal(data, &message); err != nil {
		return message, err
	}
	return message, nil
}

func (c *remoteAppServerTUIClient) respondServerRequest(ctx context.Context, message remoteAppServerMessage) error {
	var id appserver.RequestID
	if err := json.Unmarshal(message.ID, &id); err != nil {
		return err
	}
	result, code, err := c.remoteServerRequestResult(ctx, appserver.ServerRequestMethod(strings.TrimSpace(message.Method)), message.Params)
	if err != nil {
		return c.writeJSON(ctx, appserver.ErrorResponse(id, code, err.Error(), nil))
	}
	return c.writeJSON(ctx, appserver.OK(id, result))
}

func (c *remoteAppServerTUIClient) remoteServerRequestResult(ctx context.Context, method appserver.ServerRequestMethod, params json.RawMessage) (any, int, error) {
	switch method {
	case appserver.ServerRequestCommandExecutionApproval:
		var payload appserver.CommandExecutionRequestApprovalParams
		if err := remoteDecodeServerRequestParams(params, &payload); err != nil {
			return nil, -32602, err
		}
		c.sendSideParentRequestStatus(payload.ThreadID, codextea.SideParentStatusNeedsApproval)
		result, err := c.commandExecutionApproval(ctx, &payload)
		return result, -32603, err
	case appserver.ServerRequestFileChangeApproval:
		var payload appserver.FileChangeRequestApprovalParams
		if err := remoteDecodeServerRequestParams(params, &payload); err != nil {
			return nil, -32602, err
		}
		c.sendSideParentRequestStatus(payload.ThreadID, codextea.SideParentStatusNeedsApproval)
		result, err := c.fileChangeApproval(ctx, &payload)
		return result, -32603, err
	case appserver.ServerRequestPermissionsApproval:
		var payload appserver.PermissionsRequestApprovalParams
		if err := remoteDecodeServerRequestParams(params, &payload); err != nil {
			return nil, -32602, err
		}
		c.sendSideParentRequestStatus(payload.ThreadID, codextea.SideParentStatusNeedsApproval)
		result, err := c.permissionsApproval(ctx, &payload)
		return result, -32603, err
	case appserver.ServerRequestToolUserInput:
		var payload appserver.ToolRequestUserInputParams
		if err := remoteDecodeServerRequestParams(params, &payload); err != nil {
			return nil, -32602, err
		}
		c.sendSideParentRequestStatus(payload.ThreadID, codextea.SideParentStatusNeedsInput)
		result, err := c.toolUserInput(ctx, &payload)
		return result, -32603, err
	case appserver.ServerRequestMCPElicitation:
		var payload appserver.MCPElicitationRequestParams
		if err := remoteDecodeServerRequestParams(params, &payload); err != nil {
			return nil, -32602, err
		}
		c.sendSideParentRequestStatus(payload.ThreadID, codextea.SideParentStatusNeedsApproval)
		result, err := c.mcpElicitation(ctx, &payload)
		return result, -32603, err
	case appserver.ServerRequestDynamicToolCall:
		var payload appserver.DynamicToolCallParams
		if err := remoteDecodeServerRequestParams(params, &payload); err != nil {
			return nil, -32602, err
		}
		// Rust dynamic_tools::execute: the TUI serves its task-management
		// namespace for the app server that issued the call.
		template, err := remoteThreadStartParams(c.root, c.state)
		if err != nil {
			return nil, -32000, err
		}
		result := ExecuteDynamicTool(ctx, c, payload, DynamicToolOptions{
			ThreadStartParams:        template,
			RegisterBackgroundThread: c.registerDynamicToolThread,
		})
		return result, 0, nil
	case appserver.ServerRequestChatGPTAuthTokensRefresh:
		var payload auth.ChatGPTAuthTokensRefreshParams
		if err := remoteDecodeServerRequestParams(params, &payload); err != nil {
			return nil, -32602, err
		}
		result, err := c.chatGPTAuthTokensRefresh(ctx, &payload)
		return result, -32603, err
	case appserver.ServerRequestAttestationGenerate:
		var payload appserver.AttestationGenerateParams
		if err := remoteDecodeServerRequestParams(params, &payload); err != nil {
			return nil, -32602, err
		}
		return nil, -32000, errors.New("Attestation generation is not available in TUI.")
	case appserver.ServerRequestCurrentTimeRead:
		var payload appserver.CurrentTimeReadParams
		if err := remoteDecodeServerRequestParams(params, &payload); err != nil {
			return nil, -32602, err
		}
		return nil, -32000, errors.New("External current time is not available in TUI.")
	case appserver.ServerRequestApplyPatchApproval:
		var payload appserver.ApplyPatchApprovalParams
		if err := remoteDecodeServerRequestParams(params, &payload); err != nil {
			return nil, -32602, err
		}
		return nil, -32000, errors.New("Legacy patch approval requests are not available in TUI yet.")
	case appserver.ServerRequestExecCommandApproval:
		var payload appserver.ExecCommandApprovalParams
		if err := remoteDecodeServerRequestParams(params, &payload); err != nil {
			return nil, -32602, err
		}
		return nil, -32000, errors.New("Legacy command approval requests are not available in TUI yet.")
	default:
		return nil, -32000, fmt.Errorf("Unsupported app-server request: %s", method)
	}
}

// registerDynamicToolThread tells the TUI about a task a dynamic tool started or
// resumed, so the dashboard can track it (Rust
// AppEvent::DynamicToolThreadStarted).
func (c *remoteAppServerTUIClient) registerDynamicToolThread(threadID string, taskToolsAvailable bool) error {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return errors.New("background task requires a thread id")
	}
	c.send(codextea.DynamicToolThreadStartedMsg{ThreadID: threadID, TaskToolsAvailable: taskToolsAvailable})
	return nil
}

func (c *remoteAppServerTUIClient) chatGPTAuthTokensRefresh(ctx context.Context, params *auth.ChatGPTAuthTokensRefreshParams) (*appserver.ChatGPTAuthTokensRefreshResponse, error) {
	codexHome := auth.DefaultCodexHome()
	storeOptions := c.remoteAuthStoreOptions(codexHome)
	refreshed, err := auth.RefreshChatGPTTokens(ctx, &auth.RefreshChatGPTTokenOptions{
		CodexHome:    codexHome,
		AuthSnapshot: remoteLoadAuthSnapshot(codexHome, storeOptions),
		StoreOptions: storeOptions,
	})
	if err != nil {
		return nil, err
	}
	accessToken := remoteAuthTokenString(refreshed, "access_token")
	accountID := auth.AccountIDFromAuthForRestrictions(refreshed)
	if strings.TrimSpace(accessToken) == "" || strings.TrimSpace(accountID) == "" {
		return nil, errors.New("refreshed ChatGPT auth omitted access token or account id")
	}
	return &appserver.ChatGPTAuthTokensRefreshResponse{
		AccessToken:      strings.TrimSpace(accessToken),
		ChatGPTAccountID: strings.TrimSpace(accountID),
		ChatGPTPlanType:  remoteChatGPTPlanType(refreshed),
	}, nil
}

func (c *remoteAppServerTUIClient) remoteAuthStoreOptions(codexHome string) *auth.StoreOptions {
	loaded, err := config.LoadEffectiveWithOptions(codexHome, interactiveKeymapLoadOptions(c.root))
	if err != nil || loaded == nil {
		return auth.StoreOptionsFromConfig("", false)
	}
	return auth.StoreOptionsFromConfig(loaded.CLIAuthCredentialsStoreMode(), loaded.SecretAuthStorageEnabled())
}

func remoteLoadAuthSnapshot(codexHome string, options *auth.StoreOptions) *auth.AuthDotJSON {
	loaded, err := auth.NewStoreWithOptions(codexHome, options).Load()
	if err != nil {
		return nil
	}
	return loaded
}

func remoteAuthTokenString(snapshot *auth.AuthDotJSON, keys ...string) string {
	if snapshot == nil {
		return ""
	}
	for _, key := range keys {
		if value, ok := snapshot.Tokens[key]; ok {
			if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
				return strings.TrimSpace(text)
			}
		}
	}
	return ""
}

func remoteChatGPTPlanType(snapshot *auth.AuthDotJSON) *string {
	if snapshot == nil {
		return nil
	}
	for _, key := range []string{"plan_type", "chatgpt_plan_type", "planType", "chatgptPlanType"} {
		if value := remoteAuthTokenString(snapshot, key); value != "" {
			return &value
		}
	}
	if account := auth.AccountFromAuth(snapshot); account != nil && account.PlanType != "" && account.PlanType != auth.PlanUnknown {
		value := string(account.PlanType)
		return &value
	}
	for _, key := range []string{"access_token", "id_token"} {
		if claims := auth.ChatGPTClaimsFromJWT(remoteAuthTokenString(snapshot, key)); strings.TrimSpace(claims.PlanType) != "" {
			value := strings.TrimSpace(claims.PlanType)
			return &value
		}
	}
	return nil
}

func remoteDecodeServerRequestParams(params json.RawMessage, target any) error {
	if len(strings.TrimSpace(string(params))) == 0 {
		params = []byte("{}")
	}
	if err := json.Unmarshal(params, target); err != nil {
		return fmt.Errorf("invalid server request params: %w", err)
	}
	return nil
}

func (c *remoteAppServerTUIClient) commandExecutionApproval(ctx context.Context, params *appserver.CommandExecutionRequestApprovalParams) (*appserver.CommandExecutionRequestApprovalResponse, error) {
	response, err := c.remoteApproval(ctx, codextea.ApprovalRequestMsg{
		Title:   "Run command?",
		Body:    remoteCommandExecutionApprovalBody(params),
		Command: remoteCommandExecutionApprovalCommand(params),
	})
	if err != nil {
		return nil, err
	}
	return &appserver.CommandExecutionRequestApprovalResponse{Decision: remoteCommandExecutionDecision(response)}, nil
}

func (c *remoteAppServerTUIClient) fileChangeApproval(ctx context.Context, params *appserver.FileChangeRequestApprovalParams) (*appserver.FileChangeRequestApprovalResponse, error) {
	response, err := c.remoteApproval(ctx, codextea.ApprovalRequestMsg{
		Title: "Approve file changes?",
		Body:  remoteFileChangeApprovalBody(params),
	})
	if err != nil {
		return nil, err
	}
	return &appserver.FileChangeRequestApprovalResponse{Decision: remoteFileChangeDecision(response)}, nil
}

func (c *remoteAppServerTUIClient) applyPatchApproval(ctx context.Context, params *appserver.ApplyPatchApprovalParams) (*appserver.ApplyPatchApprovalResponse, error) {
	response, err := c.remoteApproval(ctx, codextea.ApprovalRequestMsg{
		Title: "Apply patch?",
		Body:  remoteApplyPatchApprovalBody(params),
	})
	if err != nil {
		return nil, err
	}
	return &appserver.ApplyPatchApprovalResponse{Decision: remoteReviewDecision(response)}, nil
}

func (c *remoteAppServerTUIClient) execCommandApproval(ctx context.Context, params *appserver.ExecCommandApprovalParams) (*appserver.ExecCommandApprovalResponse, error) {
	response, err := c.remoteApproval(ctx, codextea.ApprovalRequestMsg{
		Title:   "Run command?",
		Body:    remoteExecCommandApprovalBody(params),
		Command: strings.Join(params.Command, " "),
	})
	if err != nil {
		return nil, err
	}
	return &appserver.ExecCommandApprovalResponse{Decision: remoteReviewDecision(response)}, nil
}

func (c *remoteAppServerTUIClient) permissionsApproval(ctx context.Context, params *appserver.PermissionsRequestApprovalParams) (*appserver.PermissionsRequestApprovalResponse, error) {
	response, err := c.remoteApproval(ctx, codextea.ApprovalRequestMsg{
		Title: "Grant permissions?",
		Body:  remotePermissionsApprovalBody(params),
	})
	if err != nil {
		return nil, err
	}
	scope := appserver.PermissionGrantScopeTurn
	permissions := &appserver.GrantedPermissionProfile{}
	if !response.Cancelled && response.OptionID != "deny" {
		permissions = remoteGrantedPermissionProfile(params.Permissions)
		if response.OptionID == "allow_session" {
			scope = appserver.PermissionGrantScopeSession
		}
	}
	return &appserver.PermissionsRequestApprovalResponse{
		Permissions: permissions,
		Scope:       scope,
	}, nil
}

func (c *remoteAppServerTUIClient) toolUserInput(ctx context.Context, params *appserver.ToolRequestUserInputParams) (*appserver.ToolRequestUserInputResponse, error) {
	questions := remoteUserInputQuestions(params)
	if c == nil || c.brokers.userInput == nil || c.messages == nil {
		return remoteEmptyUserInputResponse(questions), nil
	}
	id, responses := c.brokers.userInput.registerRequest()
	c.send(codextea.RequestUserInputMsg{
		ID:               id,
		Questions:        questions,
		AutoResolutionMS: remoteAutoResolutionMS(params),
	})
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case response := <-responses:
		return remoteUserInputResponse(questions, response), nil
	case <-ctx.Done():
		c.brokers.userInput.forgetRequest(id)
		return nil, ctx.Err()
	}
}

func (c *remoteAppServerTUIClient) mcpElicitation(ctx context.Context, params *appserver.MCPElicitationRequestParams) (*appserver.MCPElicitationRequestResponse, error) {
	if params == nil {
		params = &appserver.MCPElicitationRequestParams{}
	}
	if c == nil || c.brokers.elicitation == nil || c.messages == nil {
		return &appserver.MCPElicitationRequestResponse{Action: appserver.MCPElicitationActionCancel}, nil
	}
	id, responses := c.brokers.elicitation.registerRequest()
	turnID := ""
	if params != nil && params.TurnID != nil {
		turnID = strings.TrimSpace(*params.TurnID)
	}
	c.send(codextea.ElicitationRequestMsg{
		ID:              id,
		ServerName:      remoteMCPServerName(params),
		RequestID:       remoteMCPElicitationID(params),
		ThreadID:        strings.TrimSpace(params.ThreadID),
		TurnID:          turnID,
		Message:         strings.TrimSpace(params.Message),
		URL:             strings.TrimSpace(params.URL),
		RequestedSchema: remoteMCPSchema(params),
		Meta:            interactiveMCPMetaMap(params.Meta),
	})
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case response := <-responses:
		return remoteMCPElicitationResponse(response), nil
	case <-ctx.Done():
		c.brokers.elicitation.forgetRequest(id)
		return nil, ctx.Err()
	}
}

func (c *remoteAppServerTUIClient) remoteApproval(ctx context.Context, message codextea.ApprovalRequestMsg) (codextea.ModalResponse, error) {
	if c == nil || c.brokers.approval == nil || c.messages == nil {
		return codextea.ModalResponse{Kind: codextea.ModalKindApproval, Cancelled: true}, nil
	}
	id, responses := c.brokers.approval.registerRequest()
	message.ID = id
	c.send(message)
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case response := <-responses:
		return response, nil
	case <-ctx.Done():
		c.brokers.approval.forgetRequest(id)
		return codextea.ModalResponse{}, ctx.Err()
	}
}

func (c *remoteAppServerTUIClient) writeJSON(ctx context.Context, value any) error {
	if c == nil || c.transport == nil {
		return errors.New("remote app-server transport is not connected")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.transport.write(ctx, data)
}

func remoteCommandExecutionApprovalBody(params *appserver.CommandExecutionRequestApprovalParams) string {
	if params == nil {
		return "Command requested approval."
	}
	var lines []string
	lines = remoteAppendPtrLine(lines, "Message", params.UserApprovalMessage)
	lines = remoteAppendPtrLine(lines, "Reason", params.Reason)
	lines = remoteAppendPtrLine(lines, "Working directory", params.CWD)
	lines = remoteAppendPtrLine(lines, "Suggested profile", params.SuggestedProfile)
	if params.SandboxDenied {
		lines = append(lines, "Sandbox denied: true")
	}
	lines = remoteAppendJSONLine(lines, "Exec policy amendment", params.ProposedExecPolicyAmendment)
	lines = remoteAppendJSONLine(lines, "Network policy amendments", params.ProposedNetworkPolicyAmendments)
	if len(params.CommandActions) > 0 {
		lines = remoteAppendJSONLine(lines, "Command actions", params.CommandActions)
	}
	lines = remoteAppendJSONLine(lines, "Action", params.Action)
	if len(lines) == 0 {
		return "Command requested approval."
	}
	return strings.Join(lines, "\n")
}

func remoteCommandExecutionApprovalCommand(params *appserver.CommandExecutionRequestApprovalParams) string {
	if params == nil {
		return ""
	}
	if params.Command != nil && strings.TrimSpace(*params.Command) != "" {
		return strings.TrimSpace(*params.Command)
	}
	if len(params.CommandActions) > 0 {
		return remoteJSON(params.CommandActions)
	}
	return remoteJSON(params.Action)
}

func remoteFileChangeApprovalBody(params *appserver.FileChangeRequestApprovalParams) string {
	if params == nil {
		return "File changes requested approval."
	}
	var lines []string
	lines = remoteAppendPtrLine(lines, "Reason", params.Reason)
	lines = remoteAppendPtrLine(lines, "Grant root", params.GrantRoot)
	if len(lines) == 0 {
		return "File changes requested approval."
	}
	return strings.Join(lines, "\n")
}

func remoteApplyPatchApprovalBody(params *appserver.ApplyPatchApprovalParams) string {
	if params == nil {
		return "Patch requested approval."
	}
	var lines []string
	lines = remoteAppendPtrLine(lines, "Reason", params.Reason)
	lines = remoteAppendPtrLine(lines, "Grant root", params.GrantRoot)
	lines = remoteAppendJSONLine(lines, "File changes", params.FileChanges)
	if len(lines) == 0 {
		return "Patch requested approval."
	}
	return strings.Join(lines, "\n")
}

func remoteExecCommandApprovalBody(params *appserver.ExecCommandApprovalParams) string {
	if params == nil {
		return "Command requested approval."
	}
	var lines []string
	lines = remoteAppendPtrLine(lines, "Reason", params.Reason)
	if strings.TrimSpace(params.CWD) != "" {
		lines = append(lines, "Working directory: "+strings.TrimSpace(params.CWD))
	}
	lines = remoteAppendJSONLine(lines, "Parsed command", params.ParsedCmd)
	if len(lines) == 0 {
		return "Command requested approval."
	}
	return strings.Join(lines, "\n")
}

func remotePermissionsApprovalBody(params *appserver.PermissionsRequestApprovalParams) string {
	if params == nil {
		return "Permissions requested approval."
	}
	var lines []string
	lines = remoteAppendPtrLine(lines, "Reason", params.Reason)
	if strings.TrimSpace(params.CWD) != "" {
		lines = append(lines, "Working directory: "+strings.TrimSpace(params.CWD))
	}
	lines = remoteAppendJSONLine(lines, "Permissions", params.Permissions)
	if len(lines) == 0 {
		return "Permissions requested approval."
	}
	return strings.Join(lines, "\n")
}

func remoteAppendPtrLine(lines []string, label string, value *string) []string {
	if value == nil || strings.TrimSpace(*value) == "" {
		return lines
	}
	return append(lines, label+": "+strings.TrimSpace(*value))
}

func remoteAppendJSONLine(lines []string, label string, value any) []string {
	text := remoteJSON(value)
	if text == "" || text == "null" || text == "{}" || text == "[]" {
		return lines
	}
	return append(lines, label+": "+text)
}

func remoteJSON(value any) string {
	if value == nil {
		return ""
	}
	data, err := json.Marshal(value)
	if err != nil {
		return strings.TrimSpace(fmt.Sprint(value))
	}
	return strings.TrimSpace(string(data))
}

func remoteCommandExecutionDecision(response codextea.ModalResponse) appserver.CommandExecutionApprovalDecision {
	if response.Cancelled {
		return appserver.CommandExecutionApprovalCancel
	}
	switch response.OptionID {
	case "allow_once":
		return appserver.CommandExecutionApprovalAccept
	case "allow_session":
		return appserver.CommandExecutionApprovalAcceptForSession
	default:
		return appserver.CommandExecutionApprovalDecline
	}
}

func remoteFileChangeDecision(response codextea.ModalResponse) appserver.FileChangeApprovalDecision {
	if response.Cancelled {
		return appserver.FileChangeApprovalCancel
	}
	switch response.OptionID {
	case "allow_once":
		return appserver.FileChangeApprovalAccept
	case "allow_session":
		return appserver.FileChangeApprovalAcceptForSession
	default:
		return appserver.FileChangeApprovalDecline
	}
}

func remoteReviewDecision(response codextea.ModalResponse) appserver.ReviewDecision {
	if response.Cancelled {
		return appserver.ReviewDecisionAbort
	}
	switch response.OptionID {
	case "allow_once":
		return appserver.ReviewDecisionApproved
	case "allow_session":
		return appserver.ReviewDecisionApprovedForSession
	default:
		return appserver.ReviewDecisionDenied
	}
}

func remoteUserInputQuestions(params *appserver.ToolRequestUserInputParams) []codextui.RequestUserInputQuestion {
	if params == nil {
		return nil
	}
	questions := append([]appserver.ToolRequestUserInputQuestion(nil), params.Questions...)
	if len(questions) == 0 && (strings.TrimSpace(params.Question.ID) != "" || strings.TrimSpace(params.Question.Question) != "" || strings.TrimSpace(params.Question.Prompt) != "") {
		questions = []appserver.ToolRequestUserInputQuestion{params.Question}
	}
	out := make([]codextui.RequestUserInputQuestion, 0, len(questions))
	for _, question := range questions {
		choices := make([]codextui.RequestUserInputChoice, 0, len(question.Options))
		for _, option := range question.Options {
			choices = append(choices, codextui.RequestUserInputChoice{
				Label:       option.Label,
				Description: option.Description,
			})
		}
		text := strings.TrimSpace(question.Question)
		if text == "" {
			text = strings.TrimSpace(question.Prompt)
		}
		out = append(out, codextui.RequestUserInputQuestion{
			Header:   question.Header,
			ID:       question.ID,
			Question: text,
			IsOther:  question.IsOther,
			IsSecret: question.IsSecret,
			Options:  choices,
		})
	}
	return out
}

func remoteAutoResolutionMS(params *appserver.ToolRequestUserInputParams) *int {
	if params == nil {
		return nil
	}
	if params.AutoResolutionMS != nil {
		value := int(*params.AutoResolutionMS)
		return &value
	}
	if params.Timeout != nil {
		value := int(*params.Timeout)
		return &value
	}
	return nil
}

func remoteEmptyUserInputResponse(questions []codextui.RequestUserInputQuestion) *appserver.ToolRequestUserInputResponse {
	answers := make(map[string]appserver.ToolRequestUserInputAnswer, len(questions))
	for _, question := range questions {
		if strings.TrimSpace(question.ID) != "" {
			answers[strings.TrimSpace(question.ID)] = appserver.ToolRequestUserInputAnswer{Answers: []string{}}
		}
	}
	return &appserver.ToolRequestUserInputResponse{Answers: answers}
}

func remoteUserInputResponse(questions []codextui.RequestUserInputQuestion, response codextea.ModalResponse) *appserver.ToolRequestUserInputResponse {
	if response.Cancelled || response.UserInput == nil {
		return remoteEmptyUserInputResponse(questions)
	}
	answers := make(map[string]appserver.ToolRequestUserInputAnswer, len(questions))
	for _, question := range questions {
		id := strings.TrimSpace(question.ID)
		if id == "" {
			continue
		}
		values := append([]string(nil), response.UserInput.AnswerLists[id]...)
		if len(values) == 0 {
			if value := strings.TrimSpace(response.UserInput.Answers[id]); value != "" {
				values = append(values, value)
			}
		}
		answers[id] = appserver.ToolRequestUserInputAnswer{Answers: values}
	}
	for id, values := range response.UserInput.AnswerLists {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if _, ok := answers[id]; !ok {
			answers[id] = appserver.ToolRequestUserInputAnswer{Answers: append([]string(nil), values...)}
		}
	}
	for id, value := range response.UserInput.Answers {
		id = strings.TrimSpace(id)
		value = strings.TrimSpace(value)
		if id == "" || value == "" {
			continue
		}
		if _, ok := answers[id]; !ok {
			answers[id] = appserver.ToolRequestUserInputAnswer{Answers: []string{value}}
		}
	}
	return &appserver.ToolRequestUserInputResponse{Answers: answers}
}

func remoteMCPServerName(params *appserver.MCPElicitationRequestParams) string {
	if params == nil {
		return ""
	}
	if strings.TrimSpace(params.ServerName) != "" {
		return strings.TrimSpace(params.ServerName)
	}
	return strings.TrimSpace(params.Server)
}

func remoteMCPElicitationID(params *appserver.MCPElicitationRequestParams) string {
	if params == nil {
		return ""
	}
	return strings.TrimSpace(params.ElicitationID)
}

func remoteMCPSchema(params *appserver.MCPElicitationRequestParams) any {
	if params == nil {
		return nil
	}
	if params.RequestedSchema != nil {
		return params.RequestedSchema
	}
	return params.Schema
}

func remoteMCPElicitationResponse(response codextea.ModalResponse) *appserver.MCPElicitationRequestResponse {
	if response.Cancelled || response.Elicitation == nil {
		return &appserver.MCPElicitationRequestResponse{Action: appserver.MCPElicitationActionCancel}
	}
	result := &appserver.MCPElicitationRequestResponse{
		Action:  remoteMCPAction(response.Elicitation.Action),
		Content: cloneAnyMapApp(response.Elicitation.Content),
	}
	if persist := strings.TrimSpace(response.Elicitation.Persist); persist != "" {
		result.Meta = map[string]any{"persist": persist}
	}
	return result
}

func remoteMCPAction(action string) appserver.MCPElicitationAction {
	switch strings.TrimSpace(action) {
	case string(appserver.MCPElicitationActionAccept):
		return appserver.MCPElicitationActionAccept
	case string(appserver.MCPElicitationActionDecline):
		return appserver.MCPElicitationActionDecline
	default:
		return appserver.MCPElicitationActionCancel
	}
}

func remoteGrantedPermissionProfile(values map[string]any) *appserver.GrantedPermissionProfile {
	profile := &appserver.GrantedPermissionProfile{}
	if values == nil {
		return profile
	}
	if network := remoteNetworkPermissions(values["network"]); network != nil {
		profile.Network = network
	}
	if fileSystem := remoteFileSystemPermissions(values["fileSystem"]); fileSystem != nil {
		profile.FileSystem = fileSystem
	} else if fileSystem := remoteFileSystemPermissions(values); fileSystem != nil {
		profile.FileSystem = fileSystem
	}
	return profile
}

func remoteNetworkPermissions(value any) *appserver.AdditionalNetworkPermissions {
	switch typed := value.(type) {
	case bool:
		enabled := typed
		return &appserver.AdditionalNetworkPermissions{Enabled: &enabled}
	case map[string]any:
		if enabled, ok := remoteBool(typed["enabled"]); ok {
			return &appserver.AdditionalNetworkPermissions{Enabled: &enabled}
		}
	}
	return nil
}

func remoteFileSystemPermissions(value any) *appserver.AdditionalFileSystemPermissions {
	typed, ok := value.(map[string]any)
	if !ok || typed == nil {
		return nil
	}
	read := remoteStringSlice(typed["read"])
	write := remoteStringSlice(typed["write"])
	entries := remoteAnySlice(typed["entries"])
	depth := remoteUint32Ptr(typed["globScanMaxDepth"])
	if len(read) == 0 && len(write) == 0 && len(entries) == 0 && depth == nil {
		return nil
	}
	return &appserver.AdditionalFileSystemPermissions{
		Read:             read,
		Write:            write,
		Entries:          entries,
		GlobScanMaxDepth: depth,
	}
}

func remoteBool(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true":
			return true, true
		case "false":
			return false, true
		}
	}
	return false, false
}

func remoteStringSlice(value any) []string {
	switch typed := value.(type) {
	case []string:
		out := make([]string, 0, len(typed))
		for _, value := range typed {
			if value = strings.TrimSpace(value); value != "" {
				out = append(out, value)
			}
		}
		return out
	case []any:
		out := make([]string, 0, len(typed))
		for _, value := range typed {
			if text := strings.TrimSpace(fmt.Sprint(value)); text != "" && text != "<nil>" {
				out = append(out, text)
			}
		}
		return out
	}
	return nil
}

func remoteAnySlice(value any) []any {
	switch typed := value.(type) {
	case []any:
		return append([]any(nil), typed...)
	case []map[string]any:
		out := make([]any, 0, len(typed))
		for _, item := range typed {
			out = append(out, item)
		}
		return out
	default:
		return nil
	}
}

func remoteUint32Ptr(value any) *uint32 {
	switch typed := value.(type) {
	case nil:
		return nil
	case uint32:
		value := typed
		return &value
	case int:
		if typed < 0 {
			return nil
		}
		value := uint32(typed)
		return &value
	case int64:
		if typed < 0 {
			return nil
		}
		value := uint32(typed)
		return &value
	case float64:
		if typed < 0 {
			return nil
		}
		value := uint32(typed)
		return &value
	case json.Number:
		parsed, err := strconv.ParseUint(typed.String(), 10, 32)
		if err != nil {
			return nil
		}
		value := uint32(parsed)
		return &value
	case string:
		parsed, err := strconv.ParseUint(strings.TrimSpace(typed), 10, 32)
		if err != nil {
			return nil
		}
		value := uint32(parsed)
		return &value
	default:
		return nil
	}
}

func (c *remoteAppServerTUIClient) notificationThreadIsActive(threadID string) bool {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || c == nil || c.state == nil {
		return true
	}
	current := strings.TrimSpace(c.state.ThreadID)
	return current == "" || current == threadID
}

func (c *remoteAppServerTUIClient) noteNotificationThreadID(threadID string) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" || c == nil || c.state == nil {
		return
	}
	current := strings.TrimSpace(c.state.ThreadID)
	if current == "" || current == threadID {
		c.state.SetThreadID(threadID)
	}
}

func (c *remoteAppServerTUIClient) sendSideParentStatusChange(threadID string, kind codextea.SideParentStatusChangeKind, status codextea.SideParentStatus) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	c.send(codextea.SideParentStatusChangeMsg{
		ParentThreadID: threadID,
		Kind:           kind,
		Status:         status,
	})
}

func (c *remoteAppServerTUIClient) sendSideParentRequestStatus(threadID string, status codextea.SideParentStatus) {
	c.sendSideParentStatusChange(threadID, codextea.SideParentStatusChangeSet, status)
}

func sideParentStatusForTurnStatus(status appserver.TurnStatus) (codextea.SideParentStatus, bool) {
	switch status {
	case appserver.TurnStatusCompleted:
		return codextea.SideParentStatusFinished, true
	case appserver.TurnStatusInterrupted:
		return codextea.SideParentStatusInterrupted, true
	case appserver.TurnStatusFailed:
		return codextea.SideParentStatusFailed, true
	default:
		return "", false
	}
}

func (c *remoteAppServerTUIClient) handleNotification(message remoteAppServerMessage) error {
	method := appserver.NotificationMethod(strings.TrimSpace(message.Method))
	switch method {
	case appserver.NotificationThreadStarted:
		var payload appserver.ThreadStartedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		if payload.Thread != nil && strings.TrimSpace(payload.Thread.ID) != "" {
			threadID := strings.TrimSpace(payload.Thread.ID)
			if !c.notificationThreadIsActive(threadID) {
				return nil
			}
			c.noteNotificationThreadID(threadID)
			c.send(codextea.ThreadEventMsg{Event: protocol.ThreadStarted(threadID)})
		}
	case appserver.NotificationTurnStarted:
		var payload appserver.TurnStartedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		c.sendSideParentStatusChange(payload.ThreadID, codextea.SideParentStatusChangeClear, "")
		if !c.notificationThreadIsActive(payload.ThreadID) {
			c.send(codextea.ThreadScopedEventMsg{ThreadID: payload.ThreadID, Event: protocol.TurnStarted()})
			return nil
		}
		c.noteNotificationThreadID(payload.ThreadID)
		c.resetReasoningStatus(payload.ThreadID)
		c.send(codextea.ThreadEventMsg{Event: protocol.TurnStarted()})
	case appserver.NotificationTurnCompleted:
		var payload appserver.TurnCompletedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		if status, ok := sideParentStatusForTurnStatus(payload.Turn.Status); ok {
			c.sendSideParentStatusChange(payload.ThreadID, codextea.SideParentStatusChangeSet, status)
		}
		if !c.notificationThreadIsActive(payload.ThreadID) {
			c.send(codextea.ThreadScopedEventMsg{ThreadID: payload.ThreadID, Event: protocol.TurnCompleted(protocol.Usage{})})
			return nil
		}
		c.noteNotificationThreadID(payload.ThreadID)
		c.resetReasoningStatus(payload.ThreadID)
		c.turnCompleted = true
		if payload.Turn.DurationMS != nil {
			value := *payload.Turn.DurationMS
			c.turnDurationMS = &value
		} else {
			c.turnDurationMS = nil
		}
		if payload.Turn.Status == appserver.TurnStatusInterrupted {
			c.turnInterrupted = true
			c.send(codextea.TurnInterruptedMsg{})
			return nil
		}
		c.send(codextea.ThreadEventMsg{Event: protocol.TurnCompleted(protocol.Usage{})})
	// Rust chatwidget ServerNotification::ConfigWarning: startup config
	// warnings coalesce into the startup warnings entry until the first turn.
	case appserver.NotificationConfigWarning:
		var payload config.ConfigWarningNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		summary := strings.TrimSpace(payload.Summary)
		if details := strings.TrimSpace(stringPtrValue(payload.Details)); details != "" {
			summary += ": " + details
		}
		if summary != "" {
			c.send(codextea.StartupConfigWarningMsg{Message: summary})
		}
	case appserver.NotificationThreadSettingsUpdated:
		var payload appserver.SettingsUpdatedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		// Rust #43330/#43340: the app server owns the thread's saved settings;
		// apply them so resumed or forked tasks stop inheriting the previous
		// task's permissions and model.
		if !c.notificationThreadIsActive(payload.ThreadID) {
			// Rust #44957: the command center patches the listed task's model
			// from a background thread's settings update immediately.
			c.send(codextea.ThreadScopedSettingsUpdatedMsg{ThreadID: payload.ThreadID, Settings: payload.ThreadSettings})
			return nil
		}
		c.noteNotificationThreadID(payload.ThreadID)
		c.send(codextea.ThreadSettingsUpdatedMsg{ThreadID: payload.ThreadID, Settings: payload.ThreadSettings})
	case appserver.NotificationThreadTokenUsageUpdated:
		var payload appserver.ThreadTokenUsageUpdatedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		event := protocol.TokenUsageUpdated(remoteThreadTokenUsage(payload.TokenUsage))
		if !c.notificationThreadIsActive(payload.ThreadID) {
			// Rust #44970: the agents dashboard tracks a task's live token totals
			// for its usage lines even while another thread is active.
			c.send(codextea.ThreadScopedEventMsg{ThreadID: payload.ThreadID, Event: event})
			return nil
		}
		c.noteNotificationThreadID(payload.ThreadID)
		c.send(codextea.ThreadEventMsg{Event: event})
	case appserver.NotificationTerminalInteraction:
		var payload appserver.TerminalInteractionNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		if !c.notificationThreadIsActive(payload.ThreadID) {
			return nil
		}
		c.noteNotificationThreadID(payload.ThreadID)
		// Rust #43921: an empty stdin polls a background terminal, which owns the
		// status row until the process produces output or the user types.
		c.send(codextea.TerminalInteractionMsg{
			ThreadID:  payload.ThreadID,
			ItemID:    payload.ItemID,
			ProcessID: payload.ProcessID,
			Stdin:     payload.Stdin,
		})
	case appserver.NotificationItemGuardianApprovalReviewStarted:
		var payload appserver.ItemGuardianApprovalReviewStartedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		if !c.notificationThreadIsActive(payload.ThreadID) {
			return nil
		}
		c.send(remoteGuardianReviewStartedMsg(payload))
	case appserver.NotificationItemGuardianApprovalReviewCompleted:
		var payload appserver.ItemGuardianApprovalReviewCompletedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		if !c.notificationThreadIsActive(payload.ThreadID) {
			return nil
		}
		c.send(remoteGuardianReviewCompletedMsg(payload))
	case appserver.NotificationAgentMessageDelta:
		var payload appserver.AgentMessageDeltaNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		if !c.notificationThreadIsActive(payload.ThreadID) {
			c.send(codextea.ThreadScopedEventMsg{ThreadID: payload.ThreadID, Event: protocol.AgentMessageDelta(payload.ItemID, payload.Delta)})
			return nil
		}
		c.send(codextea.ModelRetryStatusMsg{Active: false})
		c.send(codextea.ThreadEventMsg{Event: protocol.AgentMessageDelta(payload.ItemID, payload.Delta)})
	case appserver.NotificationReasoningSummaryTextDelta:
		var payload appserver.ReasoningSummaryTextDeltaNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		if !c.notificationThreadIsActive(payload.ThreadID) {
			// Rust #43921: buffered reasoning deltas let a later switch restore the
			// active reasoning item mid-replay.
			c.send(codextea.ThreadScopedEventMsg{ThreadID: payload.ThreadID, Event: protocol.ReasoningSummaryDelta(payload.ItemID, payload.Delta)})
			return nil
		}
		c.recordReasoningSummaryDelta(payload.ThreadID, payload.TurnID, payload.ItemID, payload.Delta)
	case appserver.NotificationReasoningSummaryPartAdded:
		var payload appserver.ReasoningSummaryPartAddedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		if !c.notificationThreadIsActive(payload.ThreadID) {
			c.send(codextea.ThreadScopedEventMsg{ThreadID: payload.ThreadID, Event: protocol.ReasoningSummaryDelta(payload.ItemID, "\n")})
			return nil
		}
		c.recordReasoningSummaryDelta(payload.ThreadID, payload.TurnID, payload.ItemID, "\n")
	case appserver.NotificationPlanDelta:
		var payload appserver.PlanDeltaNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		if !c.notificationThreadIsActive(payload.ThreadID) {
			c.send(codextea.ThreadScopedEventMsg{ThreadID: payload.ThreadID, Event: protocol.PlanDelta(payload.ItemID, payload.Delta)})
			return nil
		}
		c.send(codextea.ThreadEventMsg{Event: protocol.PlanDelta(payload.ItemID, payload.Delta)})
	case appserver.NotificationItemStarted:
		var payload appserver.ItemStartedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		c.sendSideParentStatusChange(payload.ThreadID, codextea.SideParentStatusChangeClearActionable, "")
		item := remoteProtocolItemFromPayload(payload.Item, false)
		event := protocol.ItemStarted(item)
		if strings.EqualFold(strings.TrimSpace(item.Type), "contextCompaction") && payload.StartedAtMS > 0 {
			event = protocol.ItemStartedAt(item, payload.StartedAtMS)
		}
		if !c.notificationThreadIsActive(payload.ThreadID) {
			c.send(codextea.ThreadScopedEventMsg{ThreadID: payload.ThreadID, Event: event})
			return nil
		}
		c.send(codextea.ThreadEventMsg{Event: event})
	case appserver.NotificationItemCompleted:
		var payload appserver.ItemCompletedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		if !c.notificationThreadIsActive(payload.ThreadID) {
			item := remoteProtocolItemFromPayload(payload.Item, true)
			c.send(codextea.ThreadScopedEventMsg{ThreadID: payload.ThreadID, Event: protocol.ItemCompleted(item)})
			return nil
		}
		item := remoteProtocolItemFromPayload(payload.Item, true)
		c.send(codextea.ThreadEventMsg{Event: protocol.ItemCompleted(item)})
	case appserver.NotificationHookStarted:
		var payload appserver.HookRunStartedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		if !c.notificationThreadIsActive(payload.ThreadID) {
			return nil
		}
		c.send(remoteHookRunMsg(payload.ThreadID, payload.TurnID, payload.Run, true))
	case appserver.NotificationHookCompleted:
		var payload appserver.HookRunCompletedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		if !c.notificationThreadIsActive(payload.ThreadID) {
			return nil
		}
		c.send(remoteHookRunMsg(payload.ThreadID, payload.TurnID, payload.Run, false))
	case appserver.NotificationThreadGoalUpdated:
		var payload appserver.GoalUpdatedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		if !c.notificationThreadIsActive(payload.ThreadID) {
			return nil
		}
		c.noteNotificationThreadID(payload.ThreadID)
		c.send(codextea.GoalUpdatedMsg{Goal: payload.Goal})
	case appserver.NotificationThreadGoalCleared:
		var payload appserver.GoalClearedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		if !c.notificationThreadIsActive(payload.ThreadID) {
			return nil
		}
		c.send(codextea.GoalClearedMsg{ThreadID: payload.ThreadID})
	case appserver.NotificationServerRequestResolved:
		var payload appserver.ServerRequestResolvedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		c.sendSideParentStatusChange(payload.ThreadID, codextea.SideParentStatusChangeClearActionable, "")
	case appserver.NotificationThreadClosed:
		var payload appserver.ThreadClosedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		c.sendSideParentStatusChange(payload.ThreadID, codextea.SideParentStatusChangeSet, codextea.SideParentStatusClosed)
	case appserver.NotificationError:
		var payload appserver.ErrorNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		if payload.WillRetry {
			if !c.notificationThreadIsActive(payload.ThreadID) {
				return nil
			}
			text := strings.TrimSpace(payload.Error.Message)
			if text == "" {
				text = "Reconnecting..."
			}
			if payload.Error.AdditionalDetails != nil {
				if details := strings.TrimSpace(*payload.Error.AdditionalDetails); details != "" {
					text += "\n└ " + details
				}
			}
			c.send(codextea.ModelRetryStatusMsg{Message: text, Active: true})
			return nil
		}
		c.sendSideParentStatusChange(payload.ThreadID, codextea.SideParentStatusChangeSet, codextea.SideParentStatusFailed)
		if !c.notificationThreadIsActive(payload.ThreadID) {
			text := strings.TrimSpace(payload.Error.Message)
			if text == "" {
				text = "remote app-server error"
			}
			c.send(codextea.ThreadScopedEventMsg{ThreadID: payload.ThreadID, Event: protocol.ErrorEvent(text)})
			return nil
		}
		// Rust on_cyber_policy_error: a blocked cyber-safety response renders the
		// Daybreak-aware refusal cell instead of the generic turn error.
		if turnErrorIsCyberPolicy(payload.Error) {
			c.send(codextea.CyberPolicyErrorMsg{ThreadID: payload.ThreadID})
			return nil
		}
		text := strings.TrimSpace(payload.Error.Message)
		if text == "" {
			text = "remote app-server error"
		}
		c.send(codextea.ThreadEventMsg{Event: protocol.ErrorEvent(text)})
	case appserver.NotificationWarning:
		var payload appserver.WarningNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		if payload.ThreadID != nil && !c.notificationThreadIsActive(*payload.ThreadID) {
			return nil
		}
		if strings.TrimSpace(payload.Message) != "" {
			message := strings.TrimSpace(payload.Message)
			if strings.HasPrefix(message, "Reconnecting...") {
				c.send(codextea.ModelRetryStatusMsg{Message: message, Active: true})
			} else if message == "Compacting context..." {
				c.send(codextea.ModelCompactionStatusMsg{Message: message, Active: true})
			} else if message == "Context compaction completed" {
				c.send(codextea.ModelCompactionStatusMsg{Active: false})
			} else {
				c.send(codextea.StatusMsg{Status: "warning: " + message})
			}
		}
	case appserver.NotificationModelSafetyBufferingUpdated:
		var payload appserver.ModelSafetyBufferingUpdatedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		if !c.notificationThreadIsActive(payload.ThreadID) {
			return nil
		}
		c.send(codextea.ModelSafetyBufferingMsg{
			ThreadID:        payload.ThreadID,
			TurnID:          payload.TurnID,
			Model:           payload.Model,
			UseCases:        payload.UseCases,
			Reasons:         payload.Reasons,
			ShowBufferingUI: payload.ShowBufferingUI,
			FasterModel:     payload.FasterModel,
		})
	case appserver.NotificationWindowsSandboxSetupCompleted:
		var payload sandbox.WindowsSetupCompletedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		c.send(codextea.WindowsSandboxSetupCompletedMsg{
			Completion: codextea.WindowsSandboxSetupCompletion{
				Mode:    remoteWindowsSandboxModeFromSandbox(payload.Mode),
				Success: payload.Success,
				Error:   strings.TrimSpace(stringPtrValue(payload.Error)),
			},
		})
	default:
		if voice, ok := DecodeThreadRealtimeNotification(method, message.Params); ok {
			c.send(voice)
		}
	}
	return nil
}

func remoteGuardianReviewStartedMsg(payload appserver.ItemGuardianApprovalReviewStartedNotification) codextea.GuardianReviewMsg {
	status := remoteGuardianAssessmentStatus(payload.Review.Status)
	return codextea.GuardianReviewMsg{
		ThreadID: payload.ThreadID,
		Event: chatwidget.GuardianAssessmentEvent{
			ID:        strings.TrimSpace(payload.ReviewID),
			Status:    status,
			Action:    remoteGuardianAssessmentAction(payload.Action),
			Rationale: strings.TrimSpace(stringPtrValue(payload.Review.Rationale)),
			Raw:       remoteGuardianReviewEventJSON(payload.ThreadID, payload.TurnID, payload.ReviewID, payload.TargetItemID, payload.StartedAtMS, nil, payload.Review, payload.Action, ""),
		},
	}
}

func remoteGuardianReviewCompletedMsg(payload appserver.ItemGuardianApprovalReviewCompletedNotification) codextea.GuardianReviewMsg {
	completedAt := payload.CompletedAtMS
	return codextea.GuardianReviewMsg{
		ThreadID: payload.ThreadID,
		Event: chatwidget.GuardianAssessmentEvent{
			ID:        strings.TrimSpace(payload.ReviewID),
			Status:    remoteGuardianAssessmentStatus(payload.Review.Status),
			Action:    remoteGuardianAssessmentAction(payload.Action),
			Rationale: strings.TrimSpace(stringPtrValue(payload.Review.Rationale)),
			Raw:       remoteGuardianReviewEventJSON(payload.ThreadID, payload.TurnID, payload.ReviewID, payload.TargetItemID, payload.StartedAtMS, &completedAt, payload.Review, payload.Action, string(payload.DecisionSource)),
		},
	}
}

func remoteGuardianAssessmentStatus(status appserver.GuardianApprovalReviewStatus) chatwidget.GuardianAssessmentStatus {
	switch status {
	case appserver.GuardianApprovalReviewInProgress:
		return chatwidget.GuardianAssessmentInProgress
	case appserver.GuardianApprovalReviewApproved:
		return chatwidget.GuardianAssessmentApproved
	case appserver.GuardianApprovalReviewDenied:
		return chatwidget.GuardianAssessmentDenied
	case appserver.GuardianApprovalReviewTimedOut:
		return chatwidget.GuardianAssessmentTimedOut
	default:
		return chatwidget.GuardianAssessmentStatus(status)
	}
}

func remoteGuardianAssessmentAction(action appserver.GuardianApprovalReviewAction) chatwidget.GuardianAssessmentAction {
	kind := chatwidget.GuardianAssessmentActionKind(action.Type)
	switch action.Type {
	case "applyPatch":
		kind = chatwidget.GuardianActionApplyPatch
	case "networkAccess":
		kind = chatwidget.GuardianActionNetworkAccess
	case "mcpToolCall":
		kind = chatwidget.GuardianActionMcpToolCall
	case "requestPermissions":
		kind = chatwidget.GuardianActionRequestPermissions
	}
	target := firstNonEmptyLocal(strings.TrimSpace(action.Target), strings.TrimSpace(action.Host))
	return chatwidget.GuardianAssessmentAction{
		Kind:          kind,
		Command:       strings.TrimSpace(action.Command),
		Program:       strings.TrimSpace(action.Program),
		Argv:          append([]string(nil), action.Argv...),
		Files:         append([]string(nil), action.Files...),
		Target:        target,
		Server:        strings.TrimSpace(action.Server),
		ToolName:      strings.TrimSpace(action.ToolName),
		ConnectorName: strings.TrimSpace(stringPtrValue(action.ConnectorName)),
		Reason:        strings.TrimSpace(stringPtrValue(action.Reason)),
	}
}

func remoteGuardianReviewEventJSON(threadID string, turnID string, reviewID string, targetItemID *string, startedAt uint64, completedAt *uint64, review appserver.GuardianApprovalReview, action appserver.GuardianApprovalReviewAction, decisionSource string) json.RawMessage {
	event := map[string]any{
		"id":          strings.TrimSpace(reviewID),
		"turnId":      strings.TrimSpace(turnID),
		"startedAtMs": startedAt,
		"status":      string(remoteGuardianAssessmentStatus(review.Status)),
		"action":      action,
	}
	if targetItemID != nil && strings.TrimSpace(*targetItemID) != "" {
		event["targetItemId"] = strings.TrimSpace(*targetItemID)
	}
	if completedAt != nil {
		event["completedAtMs"] = *completedAt
	}
	if review.RiskLevel != nil {
		event["riskLevel"] = *review.RiskLevel
	}
	if review.UserAuthorization != nil {
		event["userAuthorization"] = *review.UserAuthorization
	}
	if review.Rationale != nil {
		event["rationale"] = *review.Rationale
	}
	if strings.TrimSpace(decisionSource) != "" {
		event["decisionSource"] = strings.TrimSpace(decisionSource)
	}
	raw, _ := json.Marshal(event)
	return raw
}

func remoteThreadTokenUsage(usage appserver.TokenUsage) protocol.ThreadTokenUsage {
	return protocol.ThreadTokenUsage{Total: remoteUsageBreakdown(usage.Total), Last: remoteUsageBreakdown(usage.Last), ModelContextWindow: usage.ModelContextWindow}
}

func remoteThreadTokenUsageFromThread(thread *appserver.Thread) *protocol.ThreadTokenUsage {
	if thread == nil {
		return nil
	}
	record := sessionRecordFromAppServerThread(thread, false)
	usage := appserver.RestoredTokenUsageForRecord(record)
	if usage == nil {
		return nil
	}
	value := remoteThreadTokenUsage(*usage)
	return &value
}

func remoteUsageBreakdown(usage *appserver.TokenUsageBreakdown) protocol.Usage {
	if usage == nil {
		return protocol.Usage{}
	}
	return protocol.Usage{InputTokens: usage.InputTokens, CachedInputTokens: usage.CachedInputTokens, CacheWriteInputTokens: usage.CacheWriteInputTokens, OutputTokens: usage.OutputTokens, ReasoningOutputTokens: usage.ReasoningOutputTokens, TotalTokens: usage.TotalTokens}
}

func remoteWindowsSandboxModeFromSandbox(mode sandbox.WindowsSetupMode) chatwidget.WindowsSandboxMode {
	switch mode {
	case sandbox.WindowsSetupUnelevated, sandbox.WindowsSetupDefault:
		return chatwidget.WindowsSandboxModeUnelevated
	case sandbox.WindowsSetupElevated:
		return chatwidget.WindowsSandboxModeElevated
	default:
		return chatwidget.WindowsSandboxMode("")
	}
}

func remoteHookRunMsg(threadID string, turnID *string, run appserver.HookRunSummary, running bool) codextea.HookRunMsg {
	entries := make([]codextea.HookOutputEntry, 0, len(run.Entries))
	for _, entry := range run.Entries {
		entries = append(entries, codextea.HookOutputEntry{
			Kind: string(entry.Kind),
			Text: entry.Text,
		})
	}
	status := string(run.Status)
	if running {
		status = string(appserver.HookRunRunning)
	}
	return codextea.HookRunMsg{
		ID:            strings.TrimSpace(run.ID),
		ThreadID:      strings.TrimSpace(threadID),
		TurnID:        strings.TrimSpace(stringPtrValue(turnID)),
		EventName:     string(run.EventName),
		Status:        status,
		StatusMessage: strings.TrimSpace(stringPtrValue(run.StatusMessage)),
		Entries:       entries,
		Running:       running,
	}
}

func (c *remoteAppServerTUIClient) send(message bubbletea.Msg) {
	if c == nil || c.messages == nil {
		return
	}
	c.messages <- message
}

// recordReasoningSummaryDelta appends a streaming reasoning summary fragment
// and publishes the latest usable line as the working indicator's header
// (Rust #43921).
func (c *remoteAppServerTUIClient) recordReasoningSummaryDelta(threadID string, turnID string, itemID string, delta string) {
	if c == nil || delta == "" {
		return
	}
	key := strings.TrimSpace(turnID) + "|" + strings.TrimSpace(itemID)
	if c.reasoningBuffers == nil {
		c.reasoningBuffers = map[string]string{}
	}
	c.reasoningBuffers[key] += delta
	header, ok := chatwidget.LatestSummaryLine(c.reasoningBuffers[key])
	if !ok {
		return
	}
	c.send(codextea.WorkingStatusHeaderMsg{ThreadID: strings.TrimSpace(threadID), Text: header})
}

// resetReasoningStatus drops accumulated reasoning summaries and clears the
// live status header at a turn boundary (Rust #43921).
func (c *remoteAppServerTUIClient) resetReasoningStatus(threadID string) {
	if c == nil {
		return
	}
	c.reasoningBuffers = nil
	c.send(codextea.WorkingStatusHeaderMsg{ThreadID: strings.TrimSpace(threadID)})
}

func remoteThreadStartParams(root *cli.RootOptions, state *codextui.State) (appserver.ThreadStartParams, error) {
	shared := remoteSharedOptions(root, state)
	configValues, err := remoteConfigValues(root, shared)
	if err != nil {
		return appserver.ThreadStartParams{}, err
	}
	params := appserver.ThreadStartParams{
		CWD:                   strings.TrimSpace(shared.CWD),
		Model:                 strings.TrimSpace(shared.Model),
		ApprovalPolicy:        remoteStringAny(shared.ApprovalPolicy),
		Sandbox:               remoteStringAny(shared.Sandbox),
		Config:                configValues,
		ExperimentalRawEvents: true,
	}
	// The TUI hosts the codex_tui task-management dynamic tools for this app
	// server (Rust dynamic_tools_mcp / ThreadToolTransport).
	if specs, err := DynamicToolSpecsRaw(); err == nil {
		params.DynamicTools = specs
	}
	if state != nil && strings.TrimSpace(state.Personality) != "" {
		personality := strings.TrimSpace(state.Personality)
		params.Personality = &personality
	}
	source := appserver.ThreadSourceUser
	params.ThreadSource = &source
	return params, nil
}

func remoteSideThreadForkParams(root *cli.RootOptions, state *codextui.State, parentThreadID string, runtimeWorkspaceRoots []string) (appserver.ThreadForkParams, error) {
	parentThreadID = strings.TrimSpace(parentThreadID)
	if parentThreadID == "" {
		return appserver.ThreadForkParams{}, errors.New("remote thread/fork requires a parent thread id")
	}
	shared := remoteSharedOptions(root, state)
	configValues, err := remoteConfigValues(root, shared)
	if err != nil {
		return appserver.ThreadForkParams{}, err
	}
	developerInstructions := codextea.SideDeveloperInstructions("")
	params := appserver.ThreadForkParams{
		ThreadID:              parentThreadID,
		HistoryMode:           session.ForkAll,
		ApprovalPolicy:        remoteStringAny(shared.ApprovalPolicy),
		DeveloperInstructions: &developerInstructions,
		Config:                configValues,
		Sandbox:               remoteStringAny(shared.Sandbox),
		Ephemeral:             true,
	}
	if cwd := strings.TrimSpace(shared.CWD); cwd != "" {
		params.CWD = &cwd
	}
	if model := strings.TrimSpace(shared.Model); model != "" {
		params.Model = &model
	}
	// Rust #46494 ForkConfigSource::Session: an active-session fork does not
	// restore saved roots when omitted, and the CWD override above would
	// otherwise drop the server-resolved roots, so forward them explicitly.
	if len(runtimeWorkspaceRoots) > 0 {
		params.RuntimeWorkspaceRoots = append([]string(nil), runtimeWorkspaceRoots...)
	}
	source := appserver.ThreadSourceUser
	params.ThreadSource = &source
	return params, nil
}

func remoteTurnStartParams(root *cli.RootOptions, state *codextui.State, threadID string, request codextea.SubmitRequest) (turn.TurnStartParams, error) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return turn.TurnStartParams{}, errors.New("remote turn/start requires a thread id")
	}
	inputs := interactiveSubmitInputs(request)
	prompt := strings.TrimSpace(request.Prompt)
	if prompt != "" {
		textInput := turn.TurnUserInput{Type: "text", Text: prompt}
		if request.IDEContext != nil {
			inputs = append([]turn.TurnUserInput{textInput}, inputs...)
		} else {
			inputs = append(inputs, textInput)
		}
	}
	if request.IDEContext != nil {
		idecontext.ApplyIDEContextToUserInput(request.IDEContext, &inputs)
	}
	inputs = applySubmitTaskReferences(inputs, request.MentionBindings, threadID)
	if len(inputs) == 0 {
		return turn.TurnStartParams{}, errors.New("remote turn/start requires user input")
	}
	if request.CollaborationMode == nil {
		request.CollaborationMode = interactiveCollaborationModeFromState(state)
	}
	shared := remoteSharedOptions(root, state)
	configValues, err := remoteConfigValues(root, shared)
	if err != nil {
		return turn.TurnStartParams{}, err
	}
	turnModel := strings.TrimSpace(shared.Model)
	if requestModel := strings.TrimSpace(request.Model); requestModel != "" {
		turnModel = requestModel
	}
	params := turn.TurnStartParams{
		ThreadID:              threadID,
		Input:                 inputs,
		CWD:                   strings.TrimSpace(shared.CWD),
		Model:                 turnModel,
		ApprovalPolicy:        remoteStringAny(shared.ApprovalPolicy),
		SandboxPolicy:         remoteStringAny(shared.Sandbox),
		Config:                configValues,
		ExperimentalRawEvents: true,
		// Rust's TUI marks user-submitted turns with the "user" trigger (#46569).
		TurnTrigger: "user",
	}
	params.CollaborationMode = interactiveCollaborationModePayload(request.CollaborationMode)
	if state != nil && strings.TrimSpace(state.Personality) != "" {
		personality := strings.TrimSpace(state.Personality)
		params.Personality = &personality
		params.PersonalitySet = true
	}
	if effort := strings.TrimSpace(shared.ModelReasoningEffort); effort != "" {
		params.Effort = &effort
	}
	if state != nil {
		if tier := strings.TrimSpace(state.ServiceTier); tier != "" && tier != "default" {
			params.ServiceTier = &tier
		}
	}
	return params, nil
}

func remoteTurnSteerParams(threadID string, turnID string, clientID string, request codextea.SubmitRequest) (turn.TurnSteerParams, error) {
	threadID = strings.TrimSpace(threadID)
	turnID = strings.TrimSpace(turnID)
	if threadID == "" || turnID == "" {
		return turn.TurnSteerParams{}, errors.New("remote turn/steer requires active thread and turn ids")
	}
	inputs := interactiveSubmitInputs(request)
	prompt := strings.TrimSpace(request.Prompt)
	if prompt != "" {
		textInput := turn.TurnUserInput{Type: "text", Text: prompt}
		if request.IDEContext != nil {
			inputs = append([]turn.TurnUserInput{textInput}, inputs...)
		} else {
			inputs = append(inputs, textInput)
		}
	}
	if request.IDEContext != nil {
		idecontext.ApplyIDEContextToUserInput(request.IDEContext, &inputs)
	}
	inputs = applySubmitTaskReferences(inputs, request.MentionBindings, threadID)
	if len(inputs) == 0 {
		return turn.TurnSteerParams{}, errors.New("remote turn/steer requires user input")
	}
	return turn.TurnSteerParams{
		ThreadID:            threadID,
		ExpectedTurnID:      turnID,
		Input:               inputs,
		ClientUserMessageID: strings.TrimSpace(clientID),
	}, nil
}

// interactiveRemoteWorktreesEnabled reports whether the `worktrees` feature is
// enabled for a remote session. Managed worktree operations stay local, so only
// the flag itself reaches the remote TUI (Rust #43120/#43286).
func interactiveRemoteWorktreesEnabled(root *cli.RootOptions) bool {
	loaded, err := config.LoadEffectiveWithOptions(auth.DefaultCodexHome(), interactiveKeymapLoadOptions(root))
	if err != nil {
		return false
	}
	return features.Enabled(loaded.FeatureSettings(), "worktrees")
}

// interactiveShowRawAgentReasoning reports the configured
// `show_raw_agent_reasoning` value (Rust config default false), which gates the
// raw chain-of-thought variant of TUI reasoning blocks and transcripts
// (RawReasoningVisibility).
func interactiveShowRawAgentReasoning(root *cli.RootOptions) bool {
	loaded, err := config.LoadEffectiveWithOptions(auth.DefaultCodexHome(), interactiveKeymapLoadOptions(root))
	if err != nil || loaded == nil {
		return false
	}
	return loaded.ShowRawAgentReasoning()
}

func remoteSharedOptions(root *cli.RootOptions, state *codextui.State) cli.SharedOptions {
	if root == nil {
		return interactiveSharedOptionsFromState(cli.SharedOptions{}, state)
	}
	return interactiveSharedOptionsFromState(root.Shared, state)
}

func remoteConfigValues(root *cli.RootOptions, shared cli.SharedOptions) (map[string]any, error) {
	values := map[string]any{}
	if root != nil {
		overrides, err := config.ParseOverrides(rootConfigOverridesWithFeatureToggles(*root))
		if err != nil {
			return nil, err
		}
		config.ApplyOverrides(values, overrides)
	}
	if effort := strings.TrimSpace(shared.ModelReasoningEffort); effort != "" {
		values["model_reasoning_effort"] = effort
	}
	if len(values) == 0 {
		return nil, nil
	}
	return values, nil
}

func remoteStringAny(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return strings.TrimSpace(value)
}

func remoteProtocolItemFromPayload(payload appserver.ThreadItemPayload, completed bool) protocol.ThreadItem {
	id := remotePayloadString(payload, "id")
	wireType := remotePayloadString(payload, "type")
	switch wireType {
	case "userMessage":
		return protocol.ThreadItem{ID: id, Type: "user_message", Text: remoteUserMessageText(payload)}
	case "agentMessage":
		item := protocol.AgentMessageItemWithPhase(id, remotePayloadString(payload, "text"), remotePayloadString(payload, "phase"))
		// Rust v2 AgentMessage carries the async delivery marker and the
		// structured async questions (#39312/#42178); the TUI keeps the
		// questions on the item metadata for the pending-question editor.
		item.Delivery = remotePayloadString(payload, "delivery")
		if questions, ok := payload["questions"]; ok && questions != nil {
			if item.Metadata == nil {
				item.Metadata = map[string]any{}
			}
			item.Metadata["questions"] = questions
		}
		return item
	case "commandExecution":
		command := remotePayloadString(payload, "command")
		status := remotePayloadString(payload, "status")
		if !completed || status == string(appserver.CommandExecutionInProgress) {
			status = "in_progress"
		} else if status == "" {
			status = "completed"
		}
		var exitCode *int
		if value, ok := remotePayloadInt(payload, "exitCode", "exit_code"); ok {
			exitCode = &value
		}
		item := protocol.CommandExecutionItem(
			id,
			command,
			remoteFirstPayloadRawString(payload, "aggregatedOutput", "output"),
			exitCode,
			status,
		)
		item.CallID = id
		// Carry the command execution source so the TUI can group consecutive
		// successful Agent / unified-exec startup commands (Rust #38921).
		item.Metadata = map[string]any{"source": firstNonEmptyLocal(remotePayloadString(payload, "source"), string(appserver.CommandExecutionSourceAgent))}
		// Rust #43921: the process id lets the status row track a unified-exec
		// wait streak for the task's background terminals.
		if processID := remotePayloadString(payload, "processId"); processID != "" {
			item.Metadata["processId"] = processID
		}
		return item
	case "mcpToolCall":
		status := remotePayloadString(payload, "status")
		if !completed || status == "inProgress" {
			status = "in_progress"
		} else if status == "" {
			status = "completed"
		}
		return protocol.MCPToolCallItem(
			id,
			remotePayloadString(payload, "server"),
			remotePayloadString(payload, "tool"),
			payload["arguments"],
			remoteMCPToolResult(payload["result"]),
			remoteMCPToolError(payload["error"]),
			status,
		)
	case "collabAgentToolCall":
		status := remotePayloadString(payload, "status")
		if !completed || status == "inProgress" {
			status = "in_progress"
		} else if status == "" {
			status = "completed"
		}
		item := protocol.CollabToolCallItem(
			id,
			remotePayloadString(payload, "tool"),
			remoteFirstPayloadString(payload, "senderThreadId", "sender_thread_id"),
			remotePayloadStringSlice(payload, "receiverThreadIds", "receiver_thread_ids"),
			remotePayloadOptionalString(payload, "prompt"),
			remotePayloadCollabAgentStates(payload, "agentsStates", "agents_states"),
			status,
		)
		return item
	case "subAgentActivity":
		return protocol.SubAgentActivityItem(
			id,
			remotePayloadString(payload, "kind"),
			remoteFirstPayloadString(payload, "agentThreadId", "agent_thread_id"),
			remoteFirstPayloadString(payload, "agentPath", "agent_path", "path"),
		)
	case "webSearch", "web_search":
		action, _ := payload["action"].(map[string]any)
		return protocol.WebSearchItem(
			id,
			remotePayloadString(payload, "query"),
			action,
		)
	case "reasoning":
		// Rust #43921: a completed reasoning item carries the complete summary,
		// which a refreshed snapshot reconciles against partial live deltas.
		parts := []string{}
		for _, key := range []string{"summary", "reasoningContent", "content"} {
			parts = append(parts, remoteTUIAnyStrings(payload[key])...)
		}
		if text := remotePayloadString(payload, "text"); text != "" {
			parts = append(parts, text)
		}
		return protocol.ThreadItem{ID: id, Type: "reasoning", Text: strings.Join(parts, "\n")}
	default:
		itemType := strings.TrimSpace(wireType)
		if itemType == "" {
			itemType = "item"
		}
		return protocol.ThreadItem{
			ID:   id,
			Type: itemType,
			Text: remoteFirstPayloadString(payload, "text", "message", "review"),
		}
	}
}

func remoteUserMessageText(payload appserver.ThreadItemPayload) string {
	content, ok := payload["content"].([]any)
	if !ok {
		return remotePayloadString(payload, "text")
	}
	parts := make([]string, 0, len(content))
	for _, value := range content {
		block, ok := value.(map[string]any)
		if !ok {
			continue
		}
		if text := remotePayloadString(block, "text"); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n")
}

func remotePayloadString(payload map[string]any, key string) string {
	if payload == nil {
		return ""
	}
	value, ok := payload[key]
	if !ok || value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	default:
		return strings.TrimSpace(fmt.Sprint(typed))
	}
}

func remoteFirstPayloadString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		if value := remotePayloadString(payload, key); value != "" {
			return value
		}
	}
	return ""
}

func remoteFirstPayloadRawString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok || value == nil {
			continue
		}
		if text, ok := value.(string); ok {
			if text != "" {
				return text
			}
			continue
		}
		return fmt.Sprint(value)
	}
	return ""
}

func remotePayloadInt(payload map[string]any, keys ...string) (int, bool) {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case int:
			return typed, true
		case int32:
			return int(typed), true
		case int64:
			return int(typed), true
		case float64:
			return int(typed), true
		case json.Number:
			parsed, err := strconv.Atoi(typed.String())
			return parsed, err == nil
		case string:
			parsed, err := strconv.Atoi(strings.TrimSpace(typed))
			return parsed, err == nil
		}
	}
	return 0, false
}

func remotePayloadOptionalString(payload map[string]any, keys ...string) *string {
	for _, key := range keys {
		if payload == nil || payload[key] == nil {
			continue
		}
		value := remotePayloadString(payload, key)
		if strings.HasPrefix(strings.TrimSpace(value), "gAAAA") || value == "" {
			return nil
		}
		return &value
	}
	return nil
}

func remotePayloadStringSlice(payload map[string]any, keys ...string) []string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok || value == nil {
			continue
		}
		switch typed := value.(type) {
		case []string:
			return append([]string(nil), typed...)
		case []any:
			out := make([]string, 0, len(typed))
			for _, entry := range typed {
				text := strings.TrimSpace(fmt.Sprint(entry))
				if text != "" {
					out = append(out, text)
				}
			}
			return out
		}
	}
	return []string{}
}

func remotePayloadCollabAgentStates(payload map[string]any, keys ...string) map[string]protocol.CollabAgentState {
	result := map[string]protocol.CollabAgentState{}
	for _, key := range keys {
		values, ok := payload[key].(map[string]any)
		if !ok {
			continue
		}
		for id, raw := range values {
			state := protocol.CollabAgentState{}
			if entry, ok := raw.(map[string]any); ok {
				state.Status = remotePayloadString(entry, "status")
				state.Message = remotePayloadOptionalString(entry, "message")
			}
			result[id] = state
		}
		break
	}
	return result
}

func remotePayloadJSON(value any) string {
	if value == nil {
		return ""
	}
	if text, ok := value.(string); ok {
		return text
	}
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprint(value)
	}
	return string(data)
}

func remoteMCPToolResult(value any) *protocol.MCPToolResult {
	values, ok := value.(map[string]any)
	if !ok || values == nil {
		return nil
	}
	result := &protocol.MCPToolResult{
		Content: remoteAnySlice(values["content"]),
	}
	if result.Content == nil {
		result.Content = []any{}
	}
	result.Meta = values["_meta"]
	if result.Meta == nil {
		result.Meta = values["meta"]
	}
	result.StructuredContent = values["structuredContent"]
	if result.StructuredContent == nil {
		result.StructuredContent = values["structured_content"]
	}
	return result
}

func remoteMCPToolError(value any) *protocol.MCPToolError {
	if value == nil {
		return nil
	}
	if text, ok := value.(string); ok {
		if strings.TrimSpace(text) == "" {
			return nil
		}
		return &protocol.MCPToolError{Message: strings.TrimSpace(text)}
	}
	if values, ok := value.(map[string]any); ok {
		message := remoteFirstPayloadString(values, "message", "error", "text")
		if message != "" {
			return &protocol.MCPToolError{Message: message}
		}
	}
	return &protocol.MCPToolError{Message: remotePayloadJSON(value)}
}

func remoteRequestIDString(raw json.RawMessage) (string, error) {
	var id appserver.RequestID
	if err := json.Unmarshal(raw, &id); err != nil {
		return "", err
	}
	return id.String(), nil
}

func sendRemoteTurnError(messages chan<- bubbletea.Msg, err error) {
	if err == nil {
		return
	}
	text := strings.TrimSpace(err.Error())
	if text == "" {
		text = "remote app-server error"
	}
	messages <- codextea.ThreadEventMsg{Event: protocol.ErrorEvent(text)}
	messages <- codextea.TurnCompletedMsg{Err: errors.New(text)}
}

func formatRemoteWebSocketDialError(rawURL string, response *http.Response, err error) error {
	if response == nil {
		return fmt.Errorf("connect remote app-server websocket %s: %w", rawURL, err)
	}
	return fmt.Errorf("connect remote app-server websocket %s failed with HTTP %s: %w", rawURL, response.Status, err)
}
