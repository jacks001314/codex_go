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
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/coder/websocket"

	appsapi "codex_go/internal/apps"
	"codex_go/internal/appserver"
	"codex_go/internal/appserverdaemon"
	"codex_go/internal/auth"
	"codex_go/internal/cli"
	"codex_go/internal/config"
	"codex_go/internal/plugin"
	"codex_go/internal/protocol"
	"codex_go/internal/review"
	"codex_go/internal/sandbox"
	"codex_go/internal/session"
	codextui "codex_go/internal/tui"
	chatwidget "codex_go/internal/tui/chatwidget"
	codextea "codex_go/internal/tui/tea"
	"codex_go/internal/turn"
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
	turnCompleted   bool
	turnInterrupted bool
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
	brokers := newRemoteTUIBrokers()
	interrupts := newRemoteTUIInterruptController(ctx, endpoint)
	options := codextea.Options{
		NoAltScreen:          root != nil && root.Shared.NoAltScreen,
		SessionPickerItems:   interactiveRemoteSessionPickerItems(ctx, root, endpoint),
		SessionPickerCWD:     interactiveSessionPickerCWD(root),
		SessionPickerView:    settings.SessionPickerView,
		ShowSessionHeader:    true,
		SessionHeaderVersion: "dev",
		OnSessionAction:      interactiveRemoteSessionActionHandler(ctx, endpoint),
		OnResumeSession:      interactiveRemoteResumeSessionHandler(ctx, endpoint),
		OnReadAgents: func(currentThreadID string) ([]codextui.AgentThreadEntry, error) {
			if strings.TrimSpace(currentThreadID) == "" && state != nil {
				currentThreadID = state.ThreadID
			}
			return interactiveRemoteAgentThreadEntries(ctx, endpoint, currentThreadID)
		},
		OnSwitchAgent: func(threadID string) (codextea.AgentThreadSwitchResponse, error) {
			return interactiveRemoteSwitchAgentThread(ctx, endpoint, threadID)
		},
		OnWriteSettings:         interactiveRemoteSettingsWriteHandler(ctx, endpoint),
		FeatureSettings:         settings.FeatureSettings,
		Personality:             settings.Personality,
		Notifications:           settings.Notifications,
		NotificationMethod:      settings.NotificationMethod,
		NotificationCondition:   settings.NotificationCondition,
		PermissionRequirements:  settings.PermissionRequirements,
		HideRateLimitModelNudge: settings.HideRateLimitModelNudge,
		TUITheme:                settings.TUITheme,
		TUIPet:                  settings.TUIPet,
		OnPostNotification:      interactiveNotificationPoster(stdout),
		OnSubmitRequest: func(request codextea.SubmitRequest) bubbletea.Cmd {
			return interactiveRemoteTurnCommand(ctx, root, endpoint, state, request, brokers, interrupts)
		},
		OnInterrupt: func() bubbletea.Cmd {
			return interrupts.interruptCommand()
		},
		OnModalResponse: func(response codextea.ModalResponse) bubbletea.Cmd {
			brokers.respond(response)
			return nil
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
		OnReadGoal: func(threadID string) (*appserver.Goal, error) {
			return interactiveRemoteReadGoal(ctx, endpoint, threadID)
		},
		OnSetGoal: func(threadID string, objective *string, tokenBudget *int64, status *appserver.GoalStatus) (appserver.Goal, error) {
			return interactiveRemoteSetGoal(ctx, endpoint, threadID, objective, tokenBudget, status)
		},
		OnClearGoal: func(threadID string) (bool, error) {
			return interactiveRemoteClearGoal(ctx, endpoint, threadID)
		},
		OnStartWindowsSandboxSetup: func(mode chatwidget.WindowsSandboxMode, cwd string) (codextea.WindowsSandboxSetupOutcome, error) {
			setupCWD := strings.TrimSpace(cwd)
			if setupCWD == "" {
				setupCWD = interactiveSessionPickerCWD(root)
			}
			return interactiveRemoteStartWindowsSandboxSetup(ctx, endpoint, mode, setupCWD)
		},
		OnReadHooks: func(cwd string) ([]chatwidget.HookRun, error) {
			hooksCWD := strings.TrimSpace(cwd)
			if hooksCWD == "" {
				hooksCWD = interactiveSessionPickerCWD(root)
			}
			return interactiveRemoteReadHooks(ctx, endpoint, hooksCWD)
		},
		OnReadPlugins: func() (plugin.PluginListResponse, error) {
			return interactiveRemoteReadPlugins(ctx, endpoint)
		},
		OnReadSkills: func(cwd string) (appserver.SkillsListResponse, error) {
			skillsCWD := strings.TrimSpace(cwd)
			if skillsCWD == "" {
				skillsCWD = interactiveSessionPickerCWD(root)
			}
			return interactiveRemoteReadSkills(ctx, endpoint, skillsCWD)
		},
		OnReadApps: func(threadID string, forceRefetch bool) (appsapi.AppListResponse, error) {
			if strings.TrimSpace(threadID) == "" && state != nil {
				threadID = state.ThreadID
			}
			return interactiveRemoteReadApps(ctx, endpoint, threadID, forceRefetch)
		},
		OnStartReview: func(params review.StartParams) (review.StartResponse, error) {
			return interactiveRemoteStartReview(ctx, endpoint, params)
		},
		OnStartSide: func(params codextea.SideStartParams) (codextea.SideStartResponse, error) {
			return interactiveRemoteStartSide(ctx, root, endpoint, state, params)
		},
		OnCloseSide: func(params codextea.SideCloseParams) (codextea.SideCloseResponse, error) {
			return interactiveRemoteCloseSide(ctx, endpoint, params)
		},
		HasChatGPTAccount: true,
	}
	_, err := codextea.Run(ctx, state, options, stdin, stdout)
	return err
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
		if err := remoteSessionRequest(reqCtx, client, appserver.MethodConfigBatchWrite, config.ConfigBatchWriteParams{Edits: configEdits}, &response); err != nil {
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

func interactiveRemoteReadHooks(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, cwd string) ([]chatwidget.HookRun, error) {
	reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
	defer cancel()
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return nil, err
	}
	defer client.close()
	params := appserver.HookListParams{}
	if strings.TrimSpace(cwd) != "" {
		params.CWDs = []string{strings.TrimSpace(cwd)}
	}
	var response appserver.HookListResponse
	if err := remoteSessionRequest(reqCtx, client, appserver.MethodHooksList, params, &response); err != nil {
		return nil, err
	}
	return remoteHookRunsFromAppServer(response), nil
}

func remoteHookRunsFromAppServer(response appserver.HookListResponse) []chatwidget.HookRun {
	runs := []chatwidget.HookRun{}
	for _, entry := range response.Data {
		cwd := strings.TrimSpace(entry.CWD)
		for _, hook := range entry.Hooks {
			runs = append(runs, remoteHookRunFromMetadata(cwd, hook))
		}
		for i, warning := range entry.Warnings {
			if strings.TrimSpace(warning) == "" {
				continue
			}
			runs = append(runs, chatwidget.HookRun{
				ID:      fmt.Sprintf("warning:%s:%d", cwd, i),
				Name:    "Hook warning",
				Command: cwd,
				Issue:   strings.TrimSpace(warning),
			})
		}
		for i, hookErr := range entry.Errors {
			message := strings.TrimSpace(hookErr.Message)
			path := strings.TrimSpace(hookErr.Path)
			if message == "" && path == "" {
				continue
			}
			runs = append(runs, chatwidget.HookRun{
				ID:      fmt.Sprintf("error:%s:%s:%d", cwd, path, i),
				Name:    "Hook error",
				Command: firstNonEmptyLocal(path, cwd),
				Status:  chatwidget.HookStatusFailed,
				Issue:   message,
			})
		}
	}
	return runs
}

func remoteHookRunFromMetadata(cwd string, hook appserver.HookMetadata) chatwidget.HookRun {
	id := strings.TrimSpace(hook.Key)
	nameParts := []string{}
	if strings.TrimSpace(string(hook.EventName)) != "" {
		nameParts = append(nameParts, strings.TrimSpace(string(hook.EventName)))
	}
	if hook.Matcher != nil && strings.TrimSpace(*hook.Matcher) != "" {
		nameParts = append(nameParts, strings.TrimSpace(*hook.Matcher))
	}
	name := strings.Join(nameParts, " / ")
	if name == "" {
		name = id
	}
	command := strings.TrimSpace(string(hook.HandlerType))
	if hook.Command != nil && strings.TrimSpace(*hook.Command) != "" {
		command = strings.TrimSpace(*hook.Command)
	}
	issueLines := []string{}
	if cwd != "" {
		issueLines = append(issueLines, "cwd: "+cwd)
	}
	if strings.TrimSpace(string(hook.Source)) != "" {
		issueLines = append(issueLines, "source: "+strings.TrimSpace(string(hook.Source)))
	}
	if strings.TrimSpace(hook.SourcePath) != "" {
		issueLines = append(issueLines, "path: "+strings.TrimSpace(hook.SourcePath))
	}
	if hook.PluginID != nil && strings.TrimSpace(*hook.PluginID) != "" {
		issueLines = append(issueLines, "plugin: "+strings.TrimSpace(*hook.PluginID))
	}
	if strings.TrimSpace(string(hook.TrustStatus)) != "" {
		issueLines = append(issueLines, "trust: "+strings.TrimSpace(string(hook.TrustStatus)))
	}
	if !hook.Enabled {
		issueLines = append(issueLines, "disabled")
	}
	if hook.StatusMessage != nil && strings.TrimSpace(*hook.StatusMessage) != "" {
		issueLines = append(issueLines, strings.TrimSpace(*hook.StatusMessage))
	}
	return chatwidget.HookRun{
		ID:      id,
		Name:    firstNonEmptyLocal(name, id),
		Command: command,
		Issue:   strings.Join(issueLines, "\n"),
		Output: strings.Join([]string{
			cwd,
			strings.TrimSpace(string(hook.Source)),
			strings.TrimSpace(hook.SourcePath),
			strings.TrimSpace(stringPtrValue(hook.Matcher)),
			strings.TrimSpace(stringPtrValue(hook.PluginID)),
			strings.TrimSpace(string(hook.TrustStatus)),
		}, " "),
		Managed: remoteHookManaged(hook),
		Review:  hook.EventName == appserver.HookEventPermissionRequest,
	}
}

func remoteHookManaged(hook appserver.HookMetadata) bool {
	if hook.IsManaged {
		return true
	}
	switch hook.Source {
	case appserver.HookSourceMDM, appserver.HookSourceCloudRequirements, appserver.HookSourceCloudManagedConfig, appserver.HookSourceLegacyConfigMDM:
		return true
	default:
		return false
	}
}

func interactiveRemoteReadPlugins(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint) (plugin.PluginListResponse, error) {
	reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
	defer cancel()
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return plugin.PluginListResponse{}, err
	}
	defer client.close()
	var response plugin.PluginListResponse
	if err := remoteSessionRequest(reqCtx, client, appserver.MethodPluginList, plugin.PluginListParams{IncludeInstalled: true}, &response); err != nil {
		return plugin.PluginListResponse{}, err
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

func interactiveRemoteStartSide(ctx context.Context, root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint, state *codextui.State, params codextea.SideStartParams) (codextea.SideStartResponse, error) {
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
	forkParams, err := remoteSideThreadForkParams(root, state, parentThreadID)
	if err != nil {
		return codextea.SideStartResponse{}, err
	}
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

func interactiveRemoteReadSkills(ctx context.Context, endpoint *appserverdaemon.RemoteAppServerEndpoint, cwd string) (appserver.SkillsListResponse, error) {
	reqCtx, cancel := remoteTUIAccountRequestContext(ctx)
	defer cancel()
	client, err := openRemoteSessionClient(reqCtx, endpoint)
	if err != nil {
		return appserver.SkillsListResponse{}, err
	}
	defer client.close()
	params := appserver.SkillsListParams{}
	if strings.TrimSpace(cwd) != "" {
		params.CWDs = []string{strings.TrimSpace(cwd)}
	}
	var response appserver.SkillsListResponse
	if err := remoteSessionRequest(reqCtx, client, appserver.MethodSkillsList, params, &response); err != nil {
		return appserver.SkillsListResponse{}, err
	}
	return response, nil
}

func remoteTUIAccountRequestContext(ctx context.Context) (context.Context, context.CancelFunc) {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithTimeout(ctx, remoteTUIAccountRequestTimeout)
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
		var response appserver.ThreadListResponse
		if err := remoteSessionRequest(ctx, client, appserver.MethodThreadList, params, &response); err != nil {
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
		thread, err := remoteTUIReadThread(ctx, client, threadID, true)
		if err != nil {
			return codextea.SessionResumeResponse{}, err
		}
		return codextea.SessionResumeResponse{
			Summary:  remoteTUISessionSummaryFromThread(thread, false),
			Messages: remoteTUIThreadMessagesFromThread(thread),
			Status:   remoteTUIStatusFromThread(thread),
		}, nil
	}
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
		params.CWD = &appserver.ThreadListCwdFilter{Values: []string{cwd}}
	}
	return params
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
	thread, err := remoteTUIReadThread(ctx, client, threadID, true)
	if err != nil {
		return codextea.AgentThreadSwitchResponse{}, err
	}
	primaryThreadID := threadID
	if thread != nil && thread.ParentThreadID != nil && strings.TrimSpace(*thread.ParentThreadID) != "" {
		primaryThreadID = strings.TrimSpace(*thread.ParentThreadID)
	}
	return codextea.AgentThreadSwitchResponse{
		Entry:    remoteTUIAgentEntryFromThread(thread, primaryThreadID),
		Messages: remoteTUIThreadMessagesFromThread(thread),
		Status:   remoteTUIStatusFromThread(thread),
	}, nil
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

func remoteTUIThreadMessagesFromThread(thread *appserver.Thread) []codextui.Message {
	if thread == nil {
		return nil
	}
	messages := []codextui.Message{}
	for _, turn := range thread.Turns {
		for _, item := range turn.Items {
			message, ok := remoteTUIMessageFromThreadItem(item)
			if ok {
				messages = append(messages, message)
			}
		}
		if turn.Error != nil && strings.TrimSpace(turn.Error.Message) != "" {
			messages = append(messages, codextui.Message{Role: codextui.RoleSystem, Text: "Error: " + strings.TrimSpace(turn.Error.Message)})
		}
	}
	return messages
}

func remoteTUIMessageFromThreadItem(item appserver.ThreadItem) (codextui.Message, bool) {
	itemType := remoteTUINormalizedThreadItemType(item.Type)
	role := remoteTUINormalizedThreadItemRole(item.Role)
	switch {
	case itemType == "usermessage" || role == "user":
		text := remoteTUIThreadItemUserText(item)
		return codextui.Message{Role: codextui.RoleUser, Text: text, RawText: text}, strings.TrimSpace(text) != ""
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
		text := remoteTUIThreadItemReasoningText(item)
		if text == "" {
			return codextui.Message{}, false
		}
		return codextui.Message{Role: codextui.RoleHistory, Text: "Reasoning\n" + text, RawText: text}, true
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
	return func() bubbletea.Msg {
		messages := make(chan bubbletea.Msg, 256)
		var interrupt *remoteTUIInterruptController
		if len(interrupts) > 0 {
			interrupt = interrupts[0]
		}
		go runInteractiveRemoteTurn(ctx, root, endpoint, state, request, messages, brokers, interrupt)
		return codextea.StreamStartedMsg{Messages: messages}
	}
}

func runInteractiveRemoteTurn(ctx context.Context, root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint, state *codextui.State, request codextea.SubmitRequest, messages chan<- bubbletea.Msg, brokers remoteTUIBrokers, interrupts *remoteTUIInterruptController) {
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
	if err := client.connect(ctx); err != nil {
		sendRemoteTurnError(messages, err)
		return
	}
	defer client.close()
	if err := client.initialize(ctx); err != nil {
		sendRemoteTurnError(messages, err)
		return
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
		messages <- codextea.TurnCompletedMsg{ThreadID: threadID}
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
			Version: "0.0.0",
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
	return c.waitResponse(ctx, id, &response)
}

func (c *remoteAppServerTUIClient) startThread(ctx context.Context, root *cli.RootOptions, state *codextui.State) (string, error) {
	params, err := remoteThreadStartParams(root, state)
	if err != nil {
		return "", err
	}
	id, err := c.sendRequest(ctx, appserver.MethodThreadStart, params)
	if err != nil {
		return "", err
	}
	var response appserver.ThreadStartResponse
	if err := c.waitResponse(ctx, id, &response); err != nil {
		return "", err
	}
	if response.Thread == nil || strings.TrimSpace(response.Thread.ID) == "" {
		return "", errors.New("thread/start response did not include a thread id")
	}
	threadID := strings.TrimSpace(response.Thread.ID)
	if c.state == nil || strings.TrimSpace(c.state.ThreadID) != threadID {
		c.send(codextea.ThreadEventMsg{Event: protocol.ThreadStarted(threadID)})
	}
	return threadID, nil
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
				return errors.New(strings.TrimSpace(message.Error.Message))
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
		return nil, -32000, errors.New("Dynamic tool calls are not available in TUI yet.")
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
		c.turnCompleted = true
		if payload.Turn.Status == appserver.TurnStatusInterrupted {
			c.turnInterrupted = true
			c.send(codextea.TurnInterruptedMsg{})
			return nil
		}
		c.send(codextea.ThreadEventMsg{Event: protocol.TurnCompleted(protocol.Usage{})})
	case appserver.NotificationAgentMessageDelta:
		var payload appserver.AgentMessageDeltaNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		if !c.notificationThreadIsActive(payload.ThreadID) {
			c.send(codextea.ThreadScopedEventMsg{ThreadID: payload.ThreadID, Event: protocol.AgentMessageDelta(payload.ItemID, payload.Delta)})
			return nil
		}
		c.send(codextea.ThreadEventMsg{Event: protocol.AgentMessageDelta(payload.ItemID, payload.Delta)})
	case appserver.NotificationItemStarted:
		var payload appserver.ItemStartedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		c.sendSideParentStatusChange(payload.ThreadID, codextea.SideParentStatusChangeClearActionable, "")
		if !c.notificationThreadIsActive(payload.ThreadID) {
			item := remoteProtocolItemFromPayload(payload.Item, false)
			c.send(codextea.ThreadScopedEventMsg{ThreadID: payload.ThreadID, Event: protocol.ItemStarted(item)})
			return nil
		}
		item := remoteProtocolItemFromPayload(payload.Item, false)
		c.send(codextea.ThreadEventMsg{Event: protocol.ItemStarted(item)})
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
		c.sendSideParentStatusChange(payload.ThreadID, codextea.SideParentStatusChangeSet, codextea.SideParentStatusFailed)
		if !c.notificationThreadIsActive(payload.ThreadID) {
			text := strings.TrimSpace(payload.Error.Message)
			if text == "" {
				text = "remote app-server error"
			}
			c.send(codextea.ThreadScopedEventMsg{ThreadID: payload.ThreadID, Event: protocol.ErrorEvent(text)})
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
			c.send(codextea.StatusMsg{Status: "warning: " + strings.TrimSpace(payload.Message)})
		}
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
	}
	return nil
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
	if state != nil && strings.TrimSpace(state.Personality) != "" {
		personality := strings.TrimSpace(state.Personality)
		params.Personality = &personality
	}
	source := appserver.ThreadSourceUser
	params.ThreadSource = &source
	return params, nil
}

func remoteSideThreadForkParams(root *cli.RootOptions, state *codextui.State, parentThreadID string) (appserver.ThreadForkParams, error) {
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
	if prompt := strings.TrimSpace(request.Prompt); prompt != "" {
		inputs = append(inputs, turn.TurnUserInput{Type: "text", Text: prompt})
	}
	if len(inputs) == 0 {
		return turn.TurnStartParams{}, errors.New("remote turn/start requires user input")
	}
	shared := remoteSharedOptions(root, state)
	configValues, err := remoteConfigValues(root, shared)
	if err != nil {
		return turn.TurnStartParams{}, err
	}
	params := turn.TurnStartParams{
		ThreadID:              threadID,
		Input:                 inputs,
		CWD:                   strings.TrimSpace(shared.CWD),
		Model:                 strings.TrimSpace(shared.Model),
		ApprovalPolicy:        remoteStringAny(shared.ApprovalPolicy),
		SandboxPolicy:         remoteStringAny(shared.Sandbox),
		Config:                configValues,
		ExperimentalRawEvents: true,
	}
	if state != nil && strings.TrimSpace(state.Personality) != "" {
		personality := strings.TrimSpace(state.Personality)
		params.Personality = &personality
		params.PersonalitySet = true
	}
	if effort := strings.TrimSpace(shared.ModelReasoningEffort); effort != "" {
		params.Effort = &effort
	}
	return params, nil
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
	case "agentMessage":
		return protocol.AgentMessageItem(id, remotePayloadString(payload, "text"))
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
		return item
	case "mcpToolCall":
		toolName := remotePayloadString(payload, "tool")
		if server := remotePayloadString(payload, "server"); server != "" {
			toolName = server + "." + toolName
		}
		if completed {
			output := remotePayloadJSON(payload["result"])
			if output == "" {
				output = remotePayloadJSON(payload["error"])
			}
			return protocol.ToolOutputItem(id, toolName, output, remotePayloadString(payload, "status") != "failed")
		}
		return protocol.ToolCallItem(id, toolName, remotePayloadJSON(payload["arguments"]))
	default:
		itemType := strings.TrimSpace(wireType)
		if itemType == "" {
			itemType = "item"
		}
		return protocol.ThreadItem{
			ID:   id,
			Type: itemType,
			Text: remoteFirstPayloadString(payload, "text", "message"),
		}
	}
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
