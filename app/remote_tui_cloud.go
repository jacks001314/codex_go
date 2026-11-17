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
	"codex_go/model"
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
	// Rust #61600: Cloud titles are generated with the selected account's own
	// backend routing. An unusable title client leaves generation unavailable,
	// so the TUI saves the first-message title instead.
	var titleGenerator cloudbackend.ThreadTitleGenerator
	if titleClient, err := model.NewCloudTitleClient(model.CloudTitleOptions{
		BaseURL:            loaded.ChatGPTBaseURL(),
		BearerToken:        accessToken,
		AccountID:          accountID,
		RequireOriginMatch: cloudChatGPTBaseURLIsExplicit(loaded),
		Originator:         tuiClientName,
	}); err == nil {
		titleGenerator = titleClient
	}
	return cloudbackend.ConnectWithOptions(ctx, client, account, cloudbackend.ConnectOptions{
		IdleRecovery: cloudbackend.IdleRecoveryOptions{
			LoginCurrent: cloudTUILoginChecker(accessToken, accountID),
		},
		TitleGenerator: titleGenerator,
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
	// SetEnvironmentSelection carries the Cloud environment choice outside the
	// app-server wire protocol (Rust #60007/#61956).
	SetEnvironmentSelection(environment *cloudclient.EnvironmentSelection)
	// ListSavedEnvironments reads the saved environments of the account this
	// connection is bound to (Rust #61956).
	ListSavedEnvironments(ctx context.Context) ([]cloudclient.SavedEnvironment, error)
	Close()
}

// cloudThreadTitleRequester is the optional title/name capability of a Cloud
// requester (Rust #61600 AppServerRequestHandle::Cloud).
type cloudThreadTitleRequester interface {
	GenerateThreadTitle(ctx context.Context, model string, effort string, prompt string, schema map[string]any) (string, error)
	ReadThreadName(ctx context.Context, threadID string) (string, bool, error)
	SaveThreadName(ctx context.Context, threadID string, name string) (bool, error)
}

// GenerateThreadTitle delegates to the Cloud backend's title client.
func (t *cloudAppServerTransport) GenerateThreadTitle(ctx context.Context, model string, effort string, prompt string, schema map[string]any) (string, error) {
	requester, ok := t.titleRequester()
	if !ok {
		return "", errors.New("Cloud title generation is unavailable")
	}
	return requester.GenerateThreadTitle(ctx, model, effort, prompt, schema)
}

// ReadThreadName delegates to the native Cloud name read.
func (t *cloudAppServerTransport) ReadThreadName(ctx context.Context, threadID string) (string, bool, error) {
	requester, ok := t.titleRequester()
	if !ok {
		return "", false, nil
	}
	return requester.ReadThreadName(ctx, threadID)
}

// SaveThreadName delegates to the native Cloud name write.
func (t *cloudAppServerTransport) SaveThreadName(ctx context.Context, threadID string, name string) (bool, error) {
	requester, ok := t.titleRequester()
	if !ok {
		return false, nil
	}
	return requester.SaveThreadName(ctx, threadID, name)
}

func (t *cloudAppServerTransport) titleRequester() (cloudThreadTitleRequester, bool) {
	if t == nil || t.client == nil {
		return nil, false
	}
	requester, ok := t.client.(cloudThreadTitleRequester)
	return requester, ok
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

// SetEnvironmentSelection forwards the Cloud environment choice to the backend
// for the next thread start.
func (t *cloudAppServerTransport) SetEnvironmentSelection(environment *cloudclient.EnvironmentSelection) {
	if t != nil && t.client != nil {
		t.client.SetEnvironmentSelection(environment)
	}
}

// ListSavedEnvironments delegates saved environment discovery to the bound
// Cloud account.
func (t *cloudAppServerTransport) ListSavedEnvironments(ctx context.Context) ([]cloudclient.SavedEnvironment, error) {
	if t == nil || t.client == nil {
		return nil, cloudclient.ErrClientBuild
	}
	return t.client.ListSavedEnvironments(ctx)
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

// cloudResumePreparer is the optional prepared-resume capability of a Cloud
// requester (Rust #61490 AppServerRequestHandle::Cloud).
type cloudResumePreparer interface {
	PrepareThreadResume(ctx context.Context, params appserver.ThreadResumeParams) (*appserver.ThreadResumeResponse, *cloudbackend.PreparedResume, *cloudbackend.RPCError)
}

// cloudPreparedResume exposes the Cloud backend's prepared-resume handle for a
// session whose transport is the in-process Cloud bridge.
func (c *remoteAppServerTUIClient) cloudPreparedResume() (cloudResumePreparer, bool) {
	if c == nil {
		return nil, false
	}
	transport, ok := c.transport.(*cloudAppServerTransport)
	if !ok || transport == nil || transport.client == nil {
		return nil, false
	}
	preparer, ok := transport.client.(cloudResumePreparer)
	return preparer, ok
}

// remoteTUICloudInitialHistoryTurns mirrors Rust INITIAL_HISTORY_TURN_LIMIT.
const remoteTUICloudInitialHistoryTurns = 5

// remoteTUICloudHistoryItemPageLimit mirrors Rust HISTORY_ITEM_PAGE_LIMIT.
const remoteTUICloudHistoryItemPageLimit = 100

// remoteTUILoadCloudInitialHistory hydrates a provisional Cloud attachment:
// the newest turn page plus every thread item page that continues from it,
// extending the turn window when an item belongs to an older turn (Rust
// hydrate_initial_thread_history, HistoryHydrationScope::Initial). The
// attachment is not displayed until the caller commits it.
func remoteTUILoadCloudInitialHistory(ctx context.Context, prepared *cloudbackend.PreparedResume, threadID string) ([]appserver.Turn, error) {
	if prepared == nil {
		return nil, errors.New("Cloud resume preparation is unavailable")
	}
	threadID = strings.TrimSpace(threadID)
	limit := remoteTUICloudInitialHistoryTurns
	result, rpcErr := prepared.Request(ctx, appserver.MethodThreadTurnsList, appserver.ThreadTurnsListParams{
		ThreadID:      threadID,
		Limit:         &limit,
		SortDirection: appserver.SortDesc,
		ItemsView:     appserver.TurnItemsNotLoaded,
	})
	if rpcErr != nil {
		return nil, &remoteRPCError{Code: rpcErr.Code, Message: rpcErr.Message}
	}
	var page appserver.TurnsPage
	if err := json.Unmarshal(result, &page); err != nil {
		return nil, errors.New("The cloud returned an unsupported history page.")
	}
	turns := make([]appserver.Turn, 0, len(page.Data))
	// The page arrives newest first; the transcript replays chronologically.
	for index := len(page.Data) - 1; index >= 0; index-- {
		turns = append(turns, page.Data[index])
	}
	nextTurnCursor := ""
	if page.NextCursor != nil {
		nextTurnCursor = strings.TrimSpace(*page.NextCursor)
	}
	seenTurnCursors := map[string]bool{}
	seenItemCursors := map[string]bool{}
	itemCursor := ""
	for {
		itemLimit := remoteTUICloudHistoryItemPageLimit
		params := appserver.ThreadItemsListParams{
			ThreadID:      threadID,
			Limit:         &itemLimit,
			SortDirection: appserver.SortDesc,
		}
		if itemCursor != "" {
			params.Cursor = &appserver.ThreadItemsListCursor{Opaque: itemCursor}
		}
		result, rpcErr := prepared.Request(ctx, appserver.MethodThreadItemsList, params)
		if rpcErr != nil {
			return nil, &remoteRPCError{Code: rpcErr.Code, Message: rpcErr.Message}
		}
		var itemsPage appserver.ThreadItemsListResponse
		if err := json.Unmarshal(result, &itemsPage); err != nil {
			return nil, errors.New("The cloud returned an unsupported history items page.")
		}
		for _, entry := range itemsPage.Data {
			turnID := strings.TrimSpace(entry.TurnID)
			// The item window can reach past the first turn page when the
			// newest turns are itemless, so fetch the turns it belongs to.
			for turnID != "" && cloudHistoryTurnIndex(turns, turnID) < 0 && nextTurnCursor != "" {
				turnLimit := remoteTUICloudHistoryItemPageLimit
				turnCursor := nextTurnCursor
				turnParams := appserver.ThreadTurnsListParams{
					ThreadID:      threadID,
					Cursor:        &turnCursor,
					Limit:         &turnLimit,
					SortDirection: appserver.SortDesc,
					ItemsView:     appserver.TurnItemsNotLoaded,
				}
				turnResult, turnErr := prepared.Request(ctx, appserver.MethodThreadTurnsList, turnParams)
				if turnErr != nil {
					return nil, &remoteRPCError{Code: turnErr.Code, Message: turnErr.Message}
				}
				var turnPage appserver.TurnsPage
				if err := json.Unmarshal(turnResult, &turnPage); err != nil {
					return nil, errors.New("The cloud returned an unsupported history page.")
				}
				prependCloudHistoryTurns(&turns, turnPage.Data)
				nextTurnCursor = advanceCloudHistoryCursor(nextTurnCursor, turnPage.NextCursor, seenTurnCursors)
			}
			index := cloudHistoryTurnIndex(turns, turnID)
			if index < 0 || cloudHistoryTurnHasItem(turns[index].Items, entry.Item.ID) {
				continue
			}
			// Item pages arrive newest first; prepending keeps each turn in
			// chronological order.
			turns[index].Items = append([]appserver.ThreadItem{entry.Item}, turns[index].Items...)
			turns[index].ItemsView = appserver.TurnItemsSummary
		}
		itemCursor = advanceCloudHistoryCursor(itemCursor, itemsPage.NextCursor, seenItemCursors)
		if itemCursor == "" {
			break
		}
	}
	return turns, nil
}

func cloudHistoryTurnIndex(turns []appserver.Turn, turnID string) int {
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return -1
	}
	for index := range turns {
		if strings.TrimSpace(turns[index].ID) == turnID {
			return index
		}
	}
	return -1
}

func cloudHistoryTurnHasItem(items []appserver.ThreadItem, itemID string) bool {
	itemID = strings.TrimSpace(itemID)
	if itemID == "" {
		return false
	}
	for _, item := range items {
		if strings.TrimSpace(item.ID) == itemID {
			return true
		}
	}
	return false
}

func prependCloudHistoryTurns(turns *[]appserver.Turn, older []appserver.Turn) {
	if turns == nil || len(older) == 0 {
		return
	}
	known := make(map[string]struct{}, len(*turns)+len(older))
	for _, turn := range *turns {
		known[turn.ID] = struct{}{}
	}
	prepended := make([]appserver.Turn, 0, len(older))
	for index := len(older) - 1; index >= 0; index-- {
		if _, duplicate := known[older[index].ID]; duplicate {
			continue
		}
		known[older[index].ID] = struct{}{}
		prepended = append(prepended, older[index])
	}
	*turns = append(prepended, *turns...)
}

func advanceCloudHistoryCursor(current string, next *string, seen map[string]bool) string {
	if current != "" {
		seen[current] = true
	}
	if next == nil {
		return ""
	}
	trimmed := strings.TrimSpace(*next)
	if trimmed == "" || seen[trimmed] {
		return ""
	}
	seen[trimmed] = true
	return trimmed
}

// remoteTUIResumeThreadForNavigation resumes a saved task without displacing
// the displayed conversation until its initial history is ready (Rust #61490).
func remoteTUIResumeThreadForNavigation(ctx context.Context, client *remoteAppServerTUIClient, params appserver.ThreadResumeParams) (*appserver.ThreadResumeResponse, error) {
	if client == nil {
		return nil, errors.New("remote app-server transport is not connected")
	}
	if client.threadParamsMode != ThreadParamsModeCloud {
		var resumed appserver.ThreadResumeResponse
		if err := remoteSessionRequest(ctx, client, appserver.MethodThreadResume, params, &resumed); err != nil {
			return nil, err
		}
		return &resumed, nil
	}
	preparer, ok := client.cloudPreparedResume()
	if !ok {
		return nil, errors.New("Cloud task navigation is unavailable. Reopen Codex to reconnect.")
	}
	params.ThreadID = strings.TrimSpace(params.ThreadID)
	params.CWD = nil
	params.ExcludeTurns = true
	settings, prepared, rpcErr := preparer.PrepareThreadResume(ctx, params)
	if rpcErr != nil {
		return nil, &remoteRPCError{Code: rpcErr.Code, Message: rpcErr.Message}
	}
	if prepared == nil {
		return nil, errors.New("Cloud resume did not return a prepared conversation.")
	}
	turns, err := remoteTUILoadCloudInitialHistory(ctx, prepared, params.ThreadID)
	if err != nil {
		prepared.Discard()
		return nil, err
	}
	if rpcErr := prepared.Commit(ctx); rpcErr != nil {
		prepared.Discard()
		return nil, &remoteRPCError{Code: rpcErr.Code, Message: rpcErr.Message}
	}
	if settings.Thread != nil {
		settings.Thread.Turns = turns
	}
	return settings, nil
}
