package app

// Rust #59828: the TUI can run against the hosted Cloud backend. The remote
// TUI client already speaks JSON-RPC over a byte transport, so Cloud plugs in
// as an in-process transport that bridges those bytes to the cloud backend
// client instead of a socket.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"codex_go/appserver"
	"codex_go/auth"
	"codex_go/cli"
	"codex_go/cloudbackend"
	"codex_go/cloudclient"
	"codex_go/config"
	"codex_go/features"
	codextea "codex_go/tui/tea"
	"codex_go/turn"
)

// cloudTUIOrigin is the hosted Cloud backend the preview talks to (Rust
// cloud_backend::auth::ORIGIN).
const cloudTUIOrigin = "https://codex-cloud-backend.chatgpt.com"

// openCloudTUIAppServer connects the hosted Cloud backend when the
// tui_multi_backend feature is enabled (Rust #59828).
func openCloudTUIAppServer(ctx context.Context, root *cli.RootOptions) (cloudAppServerRequester, error) {
	loaded, err := config.LoadEffectiveWithOptions(auth.DefaultCodexHome(), interactiveKeymapLoadOptions(root))
	if err != nil || loaded == nil {
		return nil, errors.New("Failed to load the Cloud TUI configuration.")
	}
	if !features.Enabled(loaded.FeatureSettings(), "tui_multi_backend") {
		return nil, errors.New("The Cloud TUI backend is disabled. Enable the tui_multi_backend feature to use it.")
	}
	store := auth.NewStore(auth.DefaultCodexHome())
	resolved, err := store.Resolve()
	if err != nil {
		return nil, err
	}
	if resolved == nil || resolved.Auth.Mode() != "chatgpt" {
		return nil, errors.New("Sign in with ChatGPT and reopen Codex to use Cloud.")
	}
	if fedramp, _ := resolved.Auth.Tokens["chatgpt_account_is_fedramp"].(bool); fedramp {
		return nil, errors.New("This account is not supported by the cloud conversation preview.")
	}
	accessToken, _ := resolved.Auth.Tokens["access_token"].(string)
	accountID := cloudTUIAuthString(resolved.Auth.Tokens, "account_id", "chatgpt_account_id")
	if strings.TrimSpace(accessToken) == "" || strings.TrimSpace(accountID) == "" {
		return nil, errors.New("Cloud requires a selected account.")
	}
	client, err := cloudclient.New(cloudTUIOrigin, cloudclient.Credentials{
		BearerToken: accessToken,
		AccountID:   accountID,
	})
	if err != nil {
		return nil, errors.New("Could not connect to Cloud. Reopen Codex to reconnect.")
	}
	account, err := json.Marshal(auth.GetAccountResponse{
		Account:            auth.AccountFromAuth(&resolved.Auth),
		RequiresOpenAIAuth: true,
	})
	if err != nil {
		return nil, err
	}
	// A lost reply stream of the displayed conversation is reopened quietly,
	// but only while the stored ChatGPT credentials still match the ones this
	// client was opened with (Rust #60969).
	return cloudbackend.ConnectWithIdleRecovery(ctx, client, account, cloudbackend.IdleRecoveryOptions{
		LoginCurrent: cloudTUILoginChecker(accessToken, accountID),
	}), nil
}

// cloudTUILoginChecker re-reads the stored ChatGPT credentials before each
// quiet reopen (Rust idle::Login::current). Recovery is abandoned when the
// auth mode, account, token, or token validity changed.
func cloudTUILoginChecker(accessToken string, accountID string) func() bool {
	return func() bool {
		resolved, err := auth.NewStore(auth.DefaultCodexHome()).Resolve()
		if err != nil || resolved == nil || resolved.Auth.Mode() != "chatgpt" {
			return false
		}
		if fedramp, _ := resolved.Auth.Tokens["chatgpt_account_is_fedramp"].(bool); fedramp {
			return false
		}
		token, _ := resolved.Auth.Tokens["access_token"].(string)
		account := cloudTUIAuthString(resolved.Auth.Tokens, "account_id", "chatgpt_account_id")
		if token != accessToken || account != accountID {
			return false
		}
		if expiry, ok := auth.JWTAccessTokenExpiration(token); ok && !expiry.IsZero() && !time.Now().Before(expiry) {
			return false
		}
		return true
	}
}

// cloudTUIAuthString reads the first non-empty string among keys.
func cloudTUIAuthString(values map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := values[key].(string); ok && strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

// cloudAppServerRequester is the slice of cloudbackend.Client the bridge uses.
type cloudAppServerRequester interface {
	Request(ctx context.Context, method appserver.Method, params json.RawMessage) (json.RawMessage, *cloudbackend.RPCError)
	// SubmitUserMessage admits a user message through Cloud's native
	// start-or-steer path (Rust #60462).
	SubmitUserMessage(ctx context.Context, params turn.TurnStartParams) (cloudbackend.CloudUserMessageReceipt, *cloudbackend.RPCError)
	Notifications() <-chan cloudbackend.Notification
	AnswerServerRequest(requestID string) error
	// SetLocalProject carries the host project outside the app-server wire
	// protocol (Rust #60007).
	SetLocalProject(project *cloudclient.ProjectEnvironment)
	Close()
}

// SubmitUserMessage admits a user message through Cloud's native
// start-or-steer path instead of the generic turn/start request (Rust #60462).
func (t *cloudAppServerTransport) SubmitUserMessage(ctx context.Context, params turn.TurnStartParams) (cloudbackend.CloudUserMessageReceipt, *cloudbackend.RPCError) {
	if t == nil || t.client == nil {
		return cloudbackend.CloudUserMessageReceipt{}, &cloudbackend.RPCError{Code: -32603, Message: "cloud app-server client is unavailable"}
	}
	return t.client.SubmitUserMessage(ctx, params)
}

// cloudQuestionAnswerAdmitter is implemented by transports that can admit a
// message with Cloud's start-or-steer semantics.
type cloudQuestionAnswerAdmitter interface {
	SubmitUserMessage(ctx context.Context, params turn.TurnStartParams) (cloudbackend.CloudUserMessageReceipt, *cloudbackend.RPCError)
}

// usesCloudQuestionAnswerAdmission reports whether a submission must use the
// typed Cloud admission. Rust #60462 resolves Cloud question answers to
// start-or-steer and leaves every other submission on the generic request path.
func usesCloudQuestionAnswerAdmission(mode ThreadParamsMode, request codextea.SubmitRequest) bool {
	return mode == ThreadParamsModeCloud && request.Source == codextea.SubmitSourceQuestionAnswer
}

// admitCloudUserMessage sends one start-or-steer admission and reports the turn
// it admitted. A receipt without a turn means the message was accepted before,
// which must not be resent.
func admitCloudUserMessage(ctx context.Context, admitter cloudQuestionAnswerAdmitter, params turn.TurnStartParams) (string, error) {
	receipt, rpcErr := admitter.SubmitUserMessage(ctx, params)
	if rpcErr != nil {
		return "", &remoteRPCError{Code: rpcErr.Code, Message: rpcErr.Message}
	}
	switch receipt.Kind {
	case cloudbackend.CloudUserMessageStarted, cloudbackend.CloudUserMessageSteered:
		if receipt.Turn != nil {
			if turnID := strings.TrimSpace(receipt.Turn.ID); turnID != "" {
				return turnID, nil
			}
		}
		return "", errors.New("cloud admission did not return a turn id")
	case cloudbackend.CloudUserMessageDuplicate:
		if receipt.TurnID != nil {
			if turnID := strings.TrimSpace(*receipt.TurnID); turnID != "" {
				return turnID, nil
			}
		}
		return "", errors.New("Cloud already accepted this message; reopen the conversation to see its status before sending again.")
	default:
		return "", errors.New("The cloud returned an unsupported admission receipt.")
	}
}

// SetLocalProject forwards the host project to the cloud backend for the next
// thread start.
func (t *cloudAppServerTransport) SetLocalProject(project *cloudclient.ProjectEnvironment) {
	if t != nil && t.client != nil {
		t.client.SetLocalProject(project)
	}
}

// cloudAppServerTransport bridges the TUI's byte-oriented transport onto the
// cloud backend client: writes are JSON-RPC requests, reads are the matching
// responses and server notifications.
type cloudAppServerTransport struct {
	client cloudAppServerRequester
	ctx    context.Context
	cancel context.CancelFunc

	inbox  chan []byte
	writes chan []byte
	done   chan struct{}

	closeOnce sync.Once
}

func newCloudAppServerTransport(ctx context.Context, client cloudAppServerRequester) *cloudAppServerTransport {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	transport := &cloudAppServerTransport{
		client: client,
		ctx:    runCtx,
		cancel: cancel,
		inbox:  make(chan []byte, 128),
		writes: make(chan []byte, 16),
		done:   make(chan struct{}),
	}
	go transport.forwardNotifications()
	go transport.dispatchWrites()
	return transport
}

func (t *cloudAppServerTransport) read(ctx context.Context) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case message := <-t.inbox:
		return message, nil
	case <-t.done:
		return nil, errors.New("cloud app-server transport is closed")
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (t *cloudAppServerTransport) write(ctx context.Context, data []byte) error {
	if t == nil {
		return errors.New("cloud app-server transport is closed")
	}
	payload := append([]byte(nil), data...)
	select {
	case t.writes <- payload:
		return nil
	case <-t.done:
		return errors.New("cloud app-server transport is closed")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (t *cloudAppServerTransport) close() {
	if t == nil {
		return
	}
	t.closeOnce.Do(func() {
		close(t.done)
		t.cancel()
		if t.client != nil {
			t.client.Close()
		}
	})
}

func (t *cloudAppServerTransport) enqueue(message []byte) {
	if t == nil || len(message) == 0 {
		return
	}
	select {
	case t.inbox <- message:
	default:
		// A stalled reader must not block the notification pump; the TUI reads
		// continuously, so this only drops bursts beyond the buffer.
	}
}

// forwardNotifications republishes cloud notifications as JSON-RPC
// notifications for the TUI client.
func (t *cloudAppServerTransport) forwardNotifications() {
	if t.client == nil {
		return
	}
	notifications := t.client.Notifications()
	for {
		select {
		case <-t.done:
			return
		case notification, ok := <-notifications:
			if !ok {
				return
			}
			encoded, err := json.Marshal(map[string]any{
				"jsonrpc": "2.0",
				"method":  string(notification.Method),
				"params":  notification.Params,
			})
			if err != nil {
				continue
			}
			t.enqueue(encoded)
		}
	}
}

// dispatchWrites answers requests in arrival order so a client that relies on
// response ordering keeps working.
func (t *cloudAppServerTransport) dispatchWrites() {
	for {
		select {
		case <-t.done:
			return
		case payload := <-t.writes:
			t.handleWrite(payload)
		}
	}
}

func (t *cloudAppServerTransport) handleWrite(payload []byte) {
	var request struct {
		ID     json.RawMessage `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	if err := json.Unmarshal(payload, &request); err != nil || len(request.ID) == 0 {
		return
	}
	if request.Method == "" {
		// A response to a server request: Cloud approvals are answered through
		// the backend client's own request path. Request ids may be numbers or
		// strings, so the raw JSON is unwrapped instead of decoded as one type.
		id := strings.TrimSpace(string(request.ID))
		id = strings.TrimPrefix(id, "\"")
		id = strings.TrimSuffix(id, "\"")
		if id != "" && t.client != nil {
			_ = t.client.AnswerServerRequest(id)
		}
		return
	}
	response := map[string]any{"jsonrpc": "2.0", "id": json.RawMessage(request.ID)}
	if t.client == nil {
		response["error"] = map[string]any{"code": -32603, "message": "cloud app-server client is unavailable"}
	} else if result, rpcErr := t.client.Request(t.ctx, appserver.Method(request.Method), request.Params); rpcErr != nil {
		response["error"] = map[string]any{"code": rpcErr.Code, "message": rpcErr.Message}
	} else {
		response["result"] = json.RawMessage(result)
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return
	}
	t.enqueue(encoded)
}
