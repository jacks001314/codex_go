// Package cloudbackend adapts the authenticated native Cloud client to the
// app-server request and notification shapes (Rust #59493 cloud_backend).
//
// A request is answered from the cloud conversation that is currently
// attached; a replacement attachment becomes active only after its stream
// opens successfully, and turns are admitted only while that attachment is
// connected.
package cloudbackend

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"codex_go/appserver"
	"codex_go/cloudclient"
	"codex_go/turn"
)

const (
	// methodNotFoundCode is the JSON-RPC code for an operation the cloud backend
	// does not implement.
	methodNotFoundCode = -32601
	// internalErrorCode is the JSON-RPC code for a failed cloud operation.
	internalErrorCode = -32000
	// invalidParamsCode rejects a typed submission before it can reach the
	// Cloud mutation endpoint.
	invalidParamsCode = -32602
	commandBufferSize = 32
)

// cloudSource is the subset of the native client this backend drives.
// *cloudclient.Client implements it.
type cloudSource interface {
	Activate(ctx context.Context, threadID string) (*cloudclient.ThreadActivation, error)
	Attach(ctx context.Context, threadID string) (<-chan cloudclient.Event, <-chan error, error)
	ArchiveThread(ctx context.Context, threadID string) error
	UnarchiveThread(ctx context.Context, threadID string) (cloudclient.ThreadSnapshot, error)
	ListThreads(ctx context.Context, query cloudclient.ThreadListQuery) (cloudclient.ThreadPage, error)
	PrewarmDefaultThread(ctx context.Context, model string, modelProvider string) (*cloudclient.ThreadActivation, error)
	PrewarmThread(ctx context.Context, model string, modelProvider string, environment *cloudclient.EnvironmentSelection) (*cloudclient.ThreadActivation, error)
	ListSavedEnvironments(ctx context.Context) ([]cloudclient.SavedEnvironment, error)
	ListTurns(ctx context.Context, request cloudclient.ListTurnsRequest) (cloudclient.HistoryPage[cloudclient.HistoryTurn], error)
	ListItems(ctx context.Context, request cloudclient.ListItemsRequest) (cloudclient.HistoryPage[cloudclient.HistoryItem], error)
	AddUserMessage(ctx context.Context, request cloudclient.AddUserMessageRequest) (*cloudclient.Admission, error)
	InterruptTurn(ctx context.Context, request cloudclient.InterruptTurnRequest) error
	Close() error
}

// RPCError is the JSON-RPC error reported for one request.
type RPCError struct {
	Code    int
	Message string
}

// Error implements error.
// SetLocalProject records the host project the next cloud thread start should
// provision in the default environment (Rust #60007). Each start resolves the
// project from the caller's working directory, so a nil project clears any
// previous selection.
func (c *Client) SetLocalProject(project *cloudclient.ProjectEnvironment) {
	c.SetEnvironmentSelection(cloudclient.ProjectEnvironmentSelection(project))
}

// SetEnvironmentSelection records the explicit environment the next cloud
// thread start should use: a saved configuration or a project checkout in the
// default environment (Rust #61956). A nil selection keeps the default.
func (c *Client) SetEnvironmentSelection(environment *cloudclient.EnvironmentSelection) {
	if c == nil {
		return
	}
	c.environmentMu.Lock()
	c.environment = environment.Clone()
	c.environmentMu.Unlock()
}

func (c *Client) environmentSnapshot() *cloudclient.EnvironmentSelection {
	if c == nil {
		return nil
	}
	c.environmentMu.Lock()
	defer c.environmentMu.Unlock()
	return c.environment.Clone()
}

// ListSavedEnvironments exposes saved environment discovery through this
// connection's authenticated account (Rust #61956). A read that finishes after
// the account changed is discarded instead of being delivered.
func (c *Client) ListSavedEnvironments(ctx context.Context) ([]cloudclient.SavedEnvironment, error) {
	if c == nil || c.source == nil {
		return nil, cloudclient.ErrClientBuild
	}
	environments, err := c.source.ListSavedEnvironments(ctx)
	if err != nil {
		return nil, err
	}
	if c.recoveryOptions.LoginCurrent != nil && !c.recoveryOptions.LoginCurrent() {
		return nil, cloudclient.ErrInvalidCredentials
	}
	return environments, nil
}

func (e *RPCError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// Notification is one app-server server notification produced by the cloud
// conversation.
type Notification struct {
	Method appserver.NotificationMethod
	Params any
	// streamDisconnect marks the queued loss of one Cloud reply stream. A
	// replacement attachment supersedes losses that have not been delivered.
	streamDisconnect bool
}

// Client exposes one attached cloud conversation through app-server requests.
type Client struct {
	source  cloudSource
	account json.RawMessage

	requests  chan *pendingRequest
	nameSaves chan *threadNameSaved
	done      chan struct{}
	closed    chan struct{}
	cancel    context.CancelFunc
	closeOnce sync.Once

	outMu   sync.Mutex
	outCond *sync.Cond
	out     []Notification
	outDone bool
	outCh   chan Notification

	streamMu     sync.Mutex
	streamLosses map[string]*streamLossState

	runCtx          context.Context
	recoveryOptions IdleRecoveryOptions
	titleGenerator  ThreadTitleGenerator
	recovery        idleRecovery
	recoveryResults chan idleRecoveryResult

	environmentMu sync.Mutex
	environment   *cloudclient.EnvironmentSelection

	prepared chan *preparedRequest
	resumes  chan *resumeDisconnectedRequest

	// held is a reattached-but-not-yet-installed reply stream (Rust #62140).
	held         *heldRecovery
	heldOpened   chan heldOpened
	heldFinished chan *heldRecovery

	// Dispatch-goroutine state.
	loaded               *appserver.ThreadResumeResponse
	active               *attachment
	status               map[string]any
	warnedPartialHistory string
	// warningSink captures a history warning for a prepared resume instead of
	// publishing it while the replacement task is still provisional.
	warningSink   *Notification
	preparedState *preparedState
}

type pendingRequest struct {
	method appserver.Method
	params json.RawMessage
	mode   cloudclient.SubmissionMode
	typed  bool
	// ctx is canceled when the caller abandons the request. Reads and thread
	// starts use it so a dropped caller releases the native worker instead of
	// leaving the cloud RPC running (Rust #61599).
	ctx    context.Context
	cancel context.CancelFunc
	reply  chan requestReply
}

type requestReply struct {
	result json.RawMessage
	err    *RPCError
}

type attachment struct {
	threadID string
	events   <-chan cloudclient.Event
	errs     <-chan error
	cancel   context.CancelFunc
}

// streamLossState tracks losses queued for delivery for one conversation. A
// successful replacement marks earlier losses superseded, even if the
// replacement stream later ends (Rust #60327).
type streamLossState struct {
	remaining  int
	superseded int
}

// Connect opens an app-server client backed by the authenticated native Cloud
// client. The returned client owns source and closes it with Close.
func Connect(ctx context.Context, source cloudSource, account json.RawMessage) *Client {
	return ConnectWithIdleRecovery(ctx, source, account, IdleRecoveryOptions{})
}

// ConnectWithIdleRecovery opens an app-server client that quietly reopens the
// lost reply stream of the attached Cloud conversation (Rust #60969).
func ConnectWithIdleRecovery(ctx context.Context, source cloudSource, account json.RawMessage, options IdleRecoveryOptions) *Client {
	return ConnectWithOptions(ctx, source, account, ConnectOptions{IdleRecovery: options})
}

// ConnectWithOptions opens an app-server client with the full set of Cloud
// backend capabilities (Rust cloud_backend::start_worker, #61600).
func ConnectWithOptions(ctx context.Context, source cloudSource, account json.RawMessage, options ConnectOptions) *Client {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, cancel := context.WithCancel(ctx)
	client := &Client{
		source:          source,
		account:         cloneRawMessage(account),
		requests:        make(chan *pendingRequest, commandBufferSize),
		nameSaves:       make(chan *threadNameSaved),
		done:            make(chan struct{}),
		closed:          make(chan struct{}),
		cancel:          cancel,
		outCh:           make(chan Notification),
		status:          map[string]any{},
		runCtx:          runCtx,
		recoveryOptions: options.IdleRecovery,
		titleGenerator:  options.TitleGenerator,
		recoveryResults: make(chan idleRecoveryResult, 1),
		prepared:        make(chan *preparedRequest, preparedChannelSize),
		resumes:         make(chan *resumeDisconnectedRequest, commandBufferSize),
		heldOpened:      make(chan heldOpened, 1),
		heldFinished:    make(chan *heldRecovery, preparedChannelSize),
	}
	client.outCond = sync.NewCond(&client.outMu)
	go client.pumpNotifications()
	go client.run(runCtx)
	return client
}

// Notifications streams the server notifications the cloud conversation
// produces until the client closes.
func (c *Client) Notifications() <-chan Notification {
	if c == nil {
		closed := make(chan Notification)
		close(closed)
		return closed
	}
	return c.outCh
}

// Request performs one app-server request against the attached conversation.
func (c *Client) Request(ctx context.Context, method appserver.Method, params json.RawMessage) (json.RawMessage, *RPCError) {
	return c.request(ctx, method, params, cloudclient.SubmissionStartIfIdle, false)
}

// SubmitUserMessage admits one Cloud user message with a stable submission ID
// and returns the server's typed admission receipt. It sends the mutation once;
// an uncertain transport result must not be retried automatically.
func (c *Client) SubmitUserMessage(ctx context.Context, params turn.TurnStartParams) (CloudUserMessageReceipt, *RPCError) {
	encoded, err := json.Marshal(params)
	if err != nil {
		return CloudUserMessageReceipt{}, typedSubmissionError(err)
	}
	result, rpcErr := c.request(ctx, appserver.MethodTurnStart, encoded, cloudclient.SubmissionSteer, true)
	if rpcErr != nil {
		return CloudUserMessageReceipt{}, rpcErr
	}
	var receipt CloudUserMessageReceipt
	if err := json.Unmarshal(result, &receipt); err != nil {
		return CloudUserMessageReceipt{}, &RPCError{Code: internalErrorCode, Message: "cloud returned an invalid user-message receipt"}
	}
	return receipt, nil
}

func (c *Client) request(ctx context.Context, method appserver.Method, params json.RawMessage, mode cloudclient.SubmissionMode, typed bool) (json.RawMessage, *RPCError) {
	if c == nil {
		return nil, &RPCError{Code: internalErrorCode, Message: "cloud client is unavailable"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	requestCtx, cancelRequest := context.WithCancel(ctx)
	pending := &pendingRequest{
		method: method,
		params: cloneRawMessage(params),
		mode:   mode,
		typed:  typed,
		ctx:    requestCtx,
		cancel: cancelRequest,
		reply:  make(chan requestReply, 1),
	}
	select {
	case c.requests <- pending:
	case <-c.done:
		cancelRequest()
		return nil, &RPCError{Code: internalErrorCode, Message: "cloud client is closed"}
	case <-ctx.Done():
		cancelRequest()
		return nil, &RPCError{Code: internalErrorCode, Message: ctx.Err().Error()}
	}
	select {
	case reply := <-pending.reply:
		cancelRequest()
		return reply.result, reply.err
	case <-c.done:
		cancelRequest()
		return nil, &RPCError{Code: internalErrorCode, Message: "cloud client is closed"}
	case <-ctx.Done():
		// The worker observes the canceled request context and drops the
		// in-flight cloud read.
		cancelRequest()
		return nil, &RPCError{Code: internalErrorCode, Message: ctx.Err().Error()}
	}
}

// Notify reports that client-initiated notifications are unsupported.
func (c *Client) Notify(appserver.Method) error {
	return errors.New("Cloud client notifications are not supported yet.")
}

// AnswerServerRequest reports that a cloud approval must be answered elsewhere.
func (c *Client) AnswerServerRequest(string) error {
	return errors.New("Answer this cloud approval in another client; TUI approval replies are not supported yet.")
}

// Close stops the backend and releases the cloud client.
func (c *Client) Close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		close(c.closed)
		c.cancelIdleRecovery()
		c.cancel()
		if c.active != nil {
			c.active.cancel()
		}
		if c.source != nil {
			_ = c.source.Close()
		}
	})
}

func (c *Client) run(ctx context.Context) {
	defer close(c.done)
	defer c.discardPrepared()
	defer c.stopNotifications()
	// Held replacements are dispatch-owned, so retire them from this goroutine.
	defer c.cancelHeldRecovery()
	for {
		c.serviceHeldRecovery()
		select {
		case <-ctx.Done():
			return
		case request := <-c.prepared:
			if request == nil {
				return
			}
			c.handlePreparedRequest(request)
			continue
		case saved := <-c.nameSaves:
			if saved == nil {
				return
			}
			c.applyThreadNameSaved(saved.params)
			close(saved.done)
			continue
		case pending := <-c.requests:
			if pending == nil {
				return
			}
			// Rust #61481: a queued start whose caller already went away must not
			// replace the active attachment, and it must leave any existing
			// recovery alone. The caller has no way to observe the reply, so
			// answer it non-blockingly and move on.
			if pending.method == appserver.MethodThreadStart && pending.ctx != nil && pending.ctx.Err() != nil {
				if pending.cancel != nil {
					pending.cancel()
				}
				select {
				case pending.reply <- requestReply{err: &RPCError{Code: internalErrorCode, Message: pending.ctx.Err().Error()}}:
				default:
				}
				continue
			}
			// A deliberate selection owns the next attachment, even when its
			// handshake fails (Rust cloud_backend run_worker).
			var canceledRoot string
			var canceledOpening bool
			if pending.method == appserver.MethodThreadStart || pending.method == appserver.MethodThreadResume {
				canceledRoot, canceledOpening = c.cancelIdleRecovery()
			}
			requestCtx := pending.ctx
			if requestCtx == nil {
				requestCtx = ctx
			}
			unloads := c.pendingUnloadsPrepared(pending)
			result, rpcErr := c.handlePendingRequest(requestCtx, pending)
			if unloads && rpcErr == nil {
				// A successful archive or unsubscribe removes the provisional
				// attachment the same way it removes a loaded conversation.
				c.discardPrepared()
			}
			if pending.cancel != nil {
				pending.cancel()
			}
			if rpcErr != nil && canceledOpening {
				c.reportStreamLoss(canceledRoot)
			}
			pending.reply <- requestReply{result: result, err: rpcErr}
		case resume := <-c.resumes:
			if resume == nil {
				return
			}
			c.handleResumeDisconnectedThread(resume)
			continue
		case opened := <-c.heldOpened:
			c.installHeldRecovery(opened)
			continue
		case held := <-c.heldFinished:
			if c.held == held {
				c.held = nil
			}
			continue
		}
	}
}

// handlePendingRequest binds recovery history reads to the held replacement so
// they share its deadline and sign-in (Rust #62140).
func (c *Client) handlePendingRequest(requestCtx context.Context, pending *pendingRequest) (json.RawMessage, *RPCError) {
	if held := c.held; held != nil {
		if threadID := heldHistoryThread(pending.method, pending.params); threadID != "" && threadID == held.threadID {
			return c.handleHeldHistory(requestCtx, held, func(readCtx context.Context) (json.RawMessage, *RPCError) {
				return c.handleRequest(readCtx, pending.method, pending.params, pending.mode, pending.typed)
			})
		}
	}
	return c.handleRequest(requestCtx, pending.method, pending.params, pending.mode, pending.typed)
}

// waitWhileListening runs op while continuing to forward the attached stream,
// matching Rust's Replies::while_listening.
func waitWhileListening[T any](c *Client, ctx context.Context, op func() (T, error)) (T, error) {
	type outcome struct {
		value T
		err   error
	}
	finished := make(chan outcome, 1)
	go func() {
		value, err := op()
		finished <- outcome{value: value, err: err}
	}()
	for {
		c.serviceHeldRecovery()
		if active := c.active; active != nil {
			select {
			case result := <-finished:
				return result.value, result.err
			case recovered := <-c.recoveryResults:
				c.handleIdleRecoveryResult(recovered)
				continue
			case opened := <-c.heldOpened:
				c.installHeldRecovery(opened)
				continue
			case held := <-c.heldFinished:
				if c.held == held {
					c.held = nil
				}
				continue
			case event, ok := <-active.events:
				c.forwardStreamEvent(active, event, ok)
				continue
			case err, ok := <-active.errs:
				c.forwardStreamError(active, err, ok)
				continue
			}
		}
		select {
		case result := <-finished:
			return result.value, result.err
		case recovered := <-c.recoveryResults:
			c.handleIdleRecoveryResult(recovered)
			continue
		case opened := <-c.heldOpened:
			c.installHeldRecovery(opened)
			continue
		case held := <-c.heldFinished:
			if c.held == held {
				c.held = nil
			}
			continue
		case <-ctx.Done():
			var zero T
			return zero, ctx.Err()
		}
	}
}

// forwardStreamEvent handles one event from the active attachment.
func (c *Client) forwardStreamEvent(active *attachment, event cloudclient.Event, ok bool) {
	if !ok {
		c.streamEnded(active, nil)
		return
	}
	c.forwardEvent(active.threadID, event)
}

// forwardStreamError handles the error channel of the active attachment. A
// closed channel without an error is not a failure: the event channel reports
// the end of the stream.
func (c *Client) forwardStreamError(active *attachment, err error, ok bool) {
	if ok && err != nil {
		c.streamEnded(active, err)
		return
	}
	if !ok {
		active.errs = nil
	}
}

// forwardNextNonBlocking notices a ready stream end before a turn is sent.
func (c *Client) forwardNextNonBlocking() {
	select {
	case recovered := <-c.recoveryResults:
		c.handleIdleRecoveryResult(recovered)
	default:
	}
	active := c.active
	if active == nil {
		return
	}
	// Check the event side first because it is authoritative for a clean end.
	// Selecting randomly between two ready channels could otherwise observe a
	// closed error side and admit a turn into a stream that already ended.
	select {
	case event, ok := <-active.events:
		c.forwardStreamEvent(active, event, ok)
		return
	default:
	}
	select {
	case err, ok := <-active.errs:
		c.forwardStreamError(active, err, ok)
	default:
	}
}

func (c *Client) streamEnded(active *attachment, endErr error) {
	if c.active != active {
		return
	}
	c.active.cancel()
	c.active = nil
	threadID := active.threadID
	// Rust only offers a quiet reopen for an idle conversation whose stream had
	// been installed long enough to prove that it stays open, and only when the
	// end was transient.
	stable := c.recovery.installed.IsZero() || time.Since(c.recovery.installed) >= idleRecoveryStableWindow
	if stable {
		// A stream that stayed open for a while ends one recovery sequence
		// (Rust #62151), so the replacement budget starts over.
		c.recovery.attempts = 0
	}
	observedIdle := c.recovery.observedIdle != nil && *c.recovery.observedIdle
	c.recovery.state = idleReconnectInactive
	c.recovery.root = ""
	c.recovery.pending = false
	c.recordStreamLoss(threadID)
	if observedIdle && stable && transientCloudError(endErr) && c.offerIdleReconnect(threadID) {
		// The accepted quiet reopen owns this loss; it is reported again only
		// if the episode fails. The in-process TUI holds submitted input until
		// the reconnect settles (Rust #60969).
		c.holdStreamInput(threadID)
		return
	}
	reason := ""
	if endErr != nil {
		// Missing conversations, lost access, and changed sign-in get their own
		// message; other ends keep the generic reopen hint.
		reason = blockedCloudRecoveryReason(endErr)
	}
	c.enqueue(cloudStreamEndedNotificationWithReason(threadID, reason))
}

// replaceAttachment opens threadID and activates it only once its stream is
// usable, so a failed replacement keeps the previous attachment.
// openedAttachment is one Attach stream that has been verified but not yet
// installed as the active attachment (Rust cloud_backend OpenReplyStream).
type openedAttachment struct {
	threadID string
	events   <-chan cloudclient.Event
	errs     <-chan error
	cancel   context.CancelFunc
	first    *cloudclient.Event
}

// openAttachment verifies an Attach handshake without displacing the active
// attachment. A server can close Attach immediately after returning successful
// headers, so a closed stream is rejected while a healthy quiet stream is
// accepted without an event.
func (c *Client) openAttachment(ctx context.Context, threadID string) (*openedAttachment, error) {
	streamCtx, cancel := context.WithCancel(ctx)
	events, errs, err := c.source.Attach(streamCtx, threadID)
	if err != nil {
		cancel()
		return nil, err
	}
	opened := &openedAttachment{threadID: threadID, events: events, errs: errs, cancel: cancel}
	notConnected := errors.New("Cloud conversation activated but its stream did not connect. Reopen it before sending.")
	// Prefer a ready event so a buffered frame is not mistaken for a closed
	// stream when both channels are already ready.
	select {
	case event, ok := <-events:
		if !ok {
			cancel()
			return nil, notConnected
		}
		opened.first = &event
		return opened, nil
	default:
	}
	// A server can close Attach immediately after returning successful headers.
	// A healthy idle stream needs no event to prove that it is ready.
	select {
	case err, ok := <-errs:
		if !ok || err != nil {
			// The stream ended before producing anything, so it never connected.
			cancel()
			return nil, notConnected
		}
		opened.errs = nil
	default:
	}
	return opened, nil
}

// replaceAttachment opens threadID and activates it only once its stream is
// usable, so a failed replacement keeps the previous attachment.
func (c *Client) replaceAttachment(ctx context.Context, threadID string) error {
	opened, err := c.openAttachment(ctx, threadID)
	if err != nil {
		return err
	}
	delete(c.status, threadID)
	c.resetIdleRecovery()
	c.recovery.installed = time.Now()
	c.active = &attachment{threadID: threadID, events: opened.events, errs: opened.errs, cancel: opened.cancel}
	c.supersedeStreamLosses(threadID)
	// An explicit reopen (resume) is the escape hatch that releases input held
	// while a quiet reopen was failing.
	c.notifyStreamReconnected(threadID)
	if opened.first != nil {
		c.forwardEvent(threadID, *opened.first)
	}
	return nil
}
func (c *Client) recordStreamLoss(threadID string) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	c.streamMu.Lock()
	defer c.streamMu.Unlock()
	if c.streamLosses == nil {
		c.streamLosses = map[string]*streamLossState{}
	}
	state := c.streamLosses[threadID]
	if state == nil {
		state = &streamLossState{}
		c.streamLosses[threadID] = state
	}
	state.remaining++
}

func (c *Client) supersedeStreamLosses(threadID string) {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return
	}
	c.streamMu.Lock()
	defer c.streamMu.Unlock()
	if state := c.streamLosses[threadID]; state != nil {
		state.superseded = state.remaining
	}
}

// shouldDeliverStreamLoss consumes one queued loss and reports whether the
// client should surface it. A single dispatcher sends losses in order, so each
// superseded loss is skipped exactly once.
func (c *Client) shouldDeliverStreamLoss(threadID string) bool {
	threadID = strings.TrimSpace(threadID)
	if threadID == "" {
		return true
	}
	c.streamMu.Lock()
	defer c.streamMu.Unlock()
	state := c.streamLosses[threadID]
	if state == nil {
		return true
	}
	deliver := state.superseded == 0
	if state.superseded > 0 {
		state.superseded--
	}
	state.remaining--
	if state.remaining <= 0 {
		delete(c.streamLosses, threadID)
	}
	return deliver
}

func (c *Client) admitTurn(threadID string) error {
	// Notice a ready disconnect before sending. A continuous stream of events
	// must still allow commands to make progress.
	c.forwardNextNonBlocking()
	if c.active == nil || c.active.threadID != strings.TrimSpace(threadID) {
		return rejectCloudInput("Cloud stream is not connected. Reopen the conversation before sending.")
	}
	// A pending mutation clears the idle observation until the stream reports
	// status again (Rust sets observed_idle to None for selected mutations).
	c.recovery.observedIdle = nil
	return nil
}

func (c *Client) detach(threadID string) {
	delete(c.status, threadID)
	if c.held != nil && c.held.threadID == threadID ||
		c.recovery.pending && c.recovery.root == threadID ||
		c.active != nil && c.active.threadID == threadID {
		c.resetIdleRecovery()
	}
	if c.active != nil && c.active.threadID == threadID {
		c.active.cancel()
		c.active = nil
	}
}

// enqueueWarning routes a warning raised while a prepared resume is loading its
// history to the provisional state instead of publishing it.
func (c *Client) enqueueWarning(notification Notification) {
	if c.warningSink != nil {
		*c.warningSink = notification
		return
	}
	c.enqueue(notification)
}

// enqueue appends a notification without blocking request handling.
func (c *Client) enqueue(notification Notification) {
	c.outMu.Lock()
	if !c.outDone {
		c.out = append(c.out, notification)
		c.outCond.Signal()
	}
	c.outMu.Unlock()
}

func (c *Client) pumpNotifications() {
	defer close(c.outCh)
	for {
		c.outMu.Lock()
		for len(c.out) == 0 && !c.outDone {
			c.outCond.Wait()
		}
		if len(c.out) == 0 {
			c.outMu.Unlock()
			return
		}
		notification := c.out[0]
		c.out = c.out[1:]
		c.outMu.Unlock()
		if notification.streamDisconnect {
			if payload, ok := notification.Params.(appserver.ErrorNotification); ok && !c.shouldDeliverStreamLoss(payload.ThreadID) {
				continue
			}
		}
		select {
		case c.outCh <- notification:
		case <-c.closed:
			return
		}
	}
}

func (c *Client) stopNotifications() {
	c.outMu.Lock()
	c.outDone = true
	c.outCond.Broadcast()
	c.outMu.Unlock()
}

func cloneRawMessage(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}
