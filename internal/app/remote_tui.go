package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	bubbletea "github.com/charmbracelet/bubbletea"
	"github.com/coder/websocket"

	"codex_go/internal/appserver"
	"codex_go/internal/appserverdaemon"
	"codex_go/internal/cli"
	"codex_go/internal/config"
	"codex_go/internal/protocol"
	codextui "codex_go/internal/tui"
	codextea "codex_go/internal/tui/tea"
	"codex_go/internal/turn"
)

type remoteAppServerDialFunc func(context.Context, string, *websocket.DialOptions) (*websocket.Conn, *http.Response, error)

type remoteAppServerTUIClient struct {
	endpoint      *appserverdaemon.RemoteAppServerEndpoint
	state         *codextui.State
	messages      chan<- bubbletea.Msg
	brokers       remoteTUIBrokers
	dial          remoteAppServerDialFunc
	conn          *websocket.Conn
	nextRequestID int64
	turnCompleted bool
}

type remoteTUIBrokers struct {
	approval    *interactiveApprovalBroker
	elicitation *interactiveElicitationBroker
	userInput   *interactiveUserInputBroker
}

func newRemoteTUIBrokers() remoteTUIBrokers {
	return remoteTUIBrokers{
		approval:    newInteractiveApprovalBroker(),
		elicitation: newInteractiveElicitationBroker(),
		userInput:   newInteractiveUserInputBroker(),
	}
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

func runInteractiveRemoteTUI(ctx context.Context, root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint, stdin io.Reader, stdout io.Writer) error {
	state := interactiveUIState(root)
	brokers := newRemoteTUIBrokers()
	options := codextea.Options{
		NoAltScreen: root != nil && root.Shared.NoAltScreen,
		OnSubmitRequest: func(request codextea.SubmitRequest) bubbletea.Cmd {
			return interactiveRemoteTurnCommand(ctx, root, endpoint, state, request, brokers)
		},
		OnModalResponse: func(response codextea.ModalResponse) bubbletea.Cmd {
			brokers.respond(response)
			return nil
		},
	}
	_, err := codextea.Run(ctx, state, options, stdin, stdout)
	return err
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
	runInteractiveRemoteTurn(ctx, root, endpoint, state, codextea.SubmitRequest{Prompt: root.Prompt}, messages, remoteTUIBrokers{})
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

func interactiveRemoteTurnCommand(ctx context.Context, root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint, state *codextui.State, request codextea.SubmitRequest, brokers remoteTUIBrokers) bubbletea.Cmd {
	return func() bubbletea.Msg {
		messages := make(chan bubbletea.Msg, 256)
		go runInteractiveRemoteTurn(ctx, root, endpoint, state, request, messages, brokers)
		return codextea.StreamStartedMsg{Messages: messages}
	}
}

func runInteractiveRemoteTurn(ctx context.Context, root *cli.RootOptions, endpoint *appserverdaemon.RemoteAppServerEndpoint, state *codextui.State, request codextea.SubmitRequest, messages chan<- bubbletea.Msg, brokers remoteTUIBrokers) {
	defer close(messages)
	if ctx == nil {
		ctx = context.Background()
	}
	client := &remoteAppServerTUIClient{
		endpoint: endpoint,
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
	if err := client.startTurn(ctx, root, state, threadID, request); err != nil {
		sendRemoteTurnError(messages, err)
		return
	}
	if err := client.readUntilTurnCompleted(ctx); err != nil {
		sendRemoteTurnError(messages, err)
		return
	}
	messages <- codextea.TurnCompletedMsg{ThreadID: threadID}
}

func (c *remoteAppServerTUIClient) connect(ctx context.Context) error {
	if c == nil || c.endpoint == nil {
		return errors.New("remote app-server endpoint is required")
	}
	if c.endpoint.Kind != appserverdaemon.RemoteEndpointWebSocket {
		return fmt.Errorf("interactive remote app-server TUI currently supports ws:// and wss:// endpoints; got %s", c.endpoint.Kind)
	}
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
	c.conn = conn
	return nil
}

func (c *remoteAppServerTUIClient) close() {
	if c != nil && c.conn != nil {
		_ = c.conn.Close(websocket.StatusNormalClosure, "")
	}
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

func (c *remoteAppServerTUIClient) startTurn(ctx context.Context, root *cli.RootOptions, state *codextui.State, threadID string, request codextea.SubmitRequest) error {
	params, err := remoteTurnStartParams(root, state, threadID, request)
	if err != nil {
		return err
	}
	id, err := c.sendRequest(ctx, appserver.MethodTurnStart, params)
	if err != nil {
		return err
	}
	var response turn.TurnStartResponse
	return c.waitResponse(ctx, id, &response)
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
	if c == nil || c.conn == nil {
		return 0, errors.New("remote app-server websocket is not connected")
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
	if err := c.conn.Write(ctx, websocket.MessageText, data); err != nil {
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
	if c == nil || c.conn == nil {
		return message, errors.New("remote app-server websocket is not connected")
	}
	messageType, data, err := c.conn.Read(ctx)
	if err != nil {
		if ctx.Err() != nil {
			return message, ctx.Err()
		}
		if websocket.CloseStatus(err) == websocket.StatusNormalClosure || websocket.CloseStatus(err) == websocket.StatusGoingAway {
			return message, io.EOF
		}
		return message, err
	}
	if messageType != websocket.MessageText {
		return message, errors.New("remote app-server websocket returned a non-text message")
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
		result, err := c.commandExecutionApproval(ctx, &payload)
		return result, -32603, err
	case appserver.ServerRequestFileChangeApproval:
		var payload appserver.FileChangeRequestApprovalParams
		if err := remoteDecodeServerRequestParams(params, &payload); err != nil {
			return nil, -32602, err
		}
		result, err := c.fileChangeApproval(ctx, &payload)
		return result, -32603, err
	case appserver.ServerRequestPermissionsApproval:
		var payload appserver.PermissionsRequestApprovalParams
		if err := remoteDecodeServerRequestParams(params, &payload); err != nil {
			return nil, -32602, err
		}
		result, err := c.permissionsApproval(ctx, &payload)
		return result, -32603, err
	case appserver.ServerRequestToolUserInput:
		var payload appserver.ToolRequestUserInputParams
		if err := remoteDecodeServerRequestParams(params, &payload); err != nil {
			return nil, -32602, err
		}
		result, err := c.toolUserInput(ctx, &payload)
		return result, -32603, err
	case appserver.ServerRequestMCPElicitation:
		var payload appserver.MCPElicitationRequestParams
		if err := remoteDecodeServerRequestParams(params, &payload); err != nil {
			return nil, -32602, err
		}
		result, err := c.mcpElicitation(ctx, &payload)
		return result, -32603, err
	case appserver.ServerRequestCurrentTimeRead:
		return &appserver.CurrentTimeReadResponse{CurrentTimeAt: time.Now().Unix()}, -32603, nil
	case appserver.ServerRequestApplyPatchApproval:
		var payload appserver.ApplyPatchApprovalParams
		if err := remoteDecodeServerRequestParams(params, &payload); err != nil {
			return nil, -32602, err
		}
		result, err := c.applyPatchApproval(ctx, &payload)
		return result, -32603, err
	case appserver.ServerRequestExecCommandApproval:
		var payload appserver.ExecCommandApprovalParams
		if err := remoteDecodeServerRequestParams(params, &payload); err != nil {
			return nil, -32602, err
		}
		result, err := c.execCommandApproval(ctx, &payload)
		return result, -32603, err
	default:
		return nil, -32601, fmt.Errorf("server request %s is not implemented in the Go TUI remote client yet", method)
	}
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
		ThreadID:        remoteString(params.ThreadID),
		TurnID:          turnID,
		Message:         remoteString(params.Message),
		URL:             remoteString(params.URL),
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
	if c == nil || c.conn == nil {
		return errors.New("remote app-server websocket is not connected")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	return c.conn.Write(ctx, websocket.MessageText, data)
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
			if c.state != nil {
				c.state.SetThreadID(threadID)
			}
			c.send(codextea.ThreadEventMsg{Event: protocol.ThreadStarted(threadID)})
		}
	case appserver.NotificationTurnStarted:
		var payload appserver.TurnStartedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		if strings.TrimSpace(payload.ThreadID) != "" && c.state != nil {
			c.state.SetThreadID(payload.ThreadID)
		}
		c.send(codextea.ThreadEventMsg{Event: protocol.TurnStarted()})
	case appserver.NotificationTurnCompleted:
		var payload appserver.TurnCompletedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		if strings.TrimSpace(payload.ThreadID) != "" && c.state != nil {
			c.state.SetThreadID(payload.ThreadID)
		}
		c.turnCompleted = true
		c.send(codextea.ThreadEventMsg{Event: protocol.TurnCompleted(protocol.Usage{})})
	case appserver.NotificationAgentMessageDelta:
		var payload appserver.AgentMessageDeltaNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		c.send(codextea.ThreadEventMsg{Event: protocol.AgentMessageDelta(payload.ItemID, payload.Delta)})
	case appserver.NotificationItemStarted:
		var payload appserver.ItemStartedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		item := remoteProtocolItemFromPayload(payload.Item, false)
		c.send(codextea.ThreadEventMsg{Event: protocol.ItemStarted(item)})
	case appserver.NotificationItemCompleted:
		var payload appserver.ItemCompletedNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
		}
		item := remoteProtocolItemFromPayload(payload.Item, true)
		c.send(codextea.ThreadEventMsg{Event: protocol.ItemCompleted(item)})
	case appserver.NotificationError:
		var payload appserver.ErrorNotification
		if err := json.Unmarshal(message.Params, &payload); err != nil {
			return err
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
		if strings.TrimSpace(payload.Message) != "" {
			c.send(codextea.StatusMsg{Status: "warning: " + strings.TrimSpace(payload.Message)})
		}
	default:
	}
	return nil
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
		if completed {
			success := remotePayloadString(payload, "status") != string(appserver.CommandExecutionFailed) && remotePayloadString(payload, "status") != string(appserver.CommandExecutionDeclined)
			return protocol.ToolOutputItem(id, "exec_command", remoteFirstPayloadString(payload, "aggregatedOutput", "output"), success)
		}
		return protocol.ToolCallItem(id, "exec_command", command)
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
