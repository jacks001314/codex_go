package cloudbackend

// Rust #62140: reattach a disconnected, loaded Cloud conversation while its
// replacement reply stream stays held until the caller refreshes turn and item
// history. Reattachment, history reads, and installation share one deadline
// and the original Cloud sign-in. Canceling the release handle drops the
// replacement.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"codex_go/appserver"
)

const (
	// cloudRecoveryTimeoutReason mirrors Rust idle::RECOVERY_TIMEOUT.
	cloudRecoveryTimeoutReason = "Cloud could not restore the reply stream in time."
	// cloudRecoveryExpiredReason is Rust CloudRecoveryRelease::install's message
	// when the replacement cannot be committed before its deadline.
	cloudRecoveryExpiredReason = "Cloud reconnection expired."
	// cloudRecoveryPATReason tells a personal-access-token session to reopen the
	// conversation, which is its only reconnect path.
	cloudRecoveryPATReason = "Reopen this cloud conversation to reconnect with your personal access token."
	// cloudRecoveryNoAttachmentReason rejects recovery when no loaded,
	// disconnected conversation matches the request.
	cloudRecoveryNoAttachmentReason = "This cloud conversation no longer has a reply attachment to restore."
	// cloudRecoveryStreamLostReason reports a held stream that ended before it
	// could be installed.
	cloudRecoveryStreamLostReason = "Cloud reply stream disconnected again."
)

// CloudRecoverySignInChanged reports that recovery history no longer belongs to
// the sign-in bound when the replacement started. It carries no credentials.
type CloudRecoverySignInChanged struct{}

func (CloudRecoverySignInChanged) Error() string { return signInAgainReason }

// CloudRecoveryRelease installs or cancels one held Cloud reply stream. Install
// returns the worker's acknowledgement; Cancel drops the replacement and
// leaves the conversation disconnected. Cancel after a successful Install is a
// no-op, so a deferred Cancel cannot tear down the committed stream.
type CloudRecoveryRelease struct {
	held *heldRecovery

	mu        sync.Mutex
	released  bool
	installed bool

	cancelOnce sync.Once
}

// Install asks the worker to commit the held stream before the next ordinary
// command, so buffered events start flowing. The caller must have finished
// refreshing history first. The shared recovery deadline bounds the wait.
func (r *CloudRecoveryRelease) Install(ctx context.Context) *RPCError {
	if r == nil || r.held == nil {
		return recoveryRPCError(cloudRecoveryExpiredReason)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	r.mu.Lock()
	if r.released {
		r.mu.Unlock()
		return recoveryRPCError(cloudRecoveryExpiredReason)
	}
	r.released = true
	r.mu.Unlock()
	if !r.held.deadline.IsZero() {
		if !time.Now().Before(r.held.deadline) {
			r.abandon()
			return recoveryRPCError(cloudRecoveryExpiredReason)
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, r.held.deadline)
		defer cancel()
	}
	ack := make(chan *RPCError)
	select {
	case r.held.install <- ack:
	case <-r.held.abandoned:
		return recoveryRPCError(cloudRecoveryExpiredReason)
	case <-ctx.Done():
		r.abandon()
		return recoveryRPCError(cloudRecoveryExpiredReason)
	}
	select {
	case rpcErr := <-ack:
		if rpcErr == nil {
			r.mu.Lock()
			r.installed = true
			r.mu.Unlock()
		}
		return rpcErr
	case <-ctx.Done():
		r.abandon()
		return recoveryRPCError(cloudRecoveryExpiredReason)
	}
}

// Cancel abandons the held replacement. It is safe to call more than once and
// after Install.
func (r *CloudRecoveryRelease) Cancel() {
	if r == nil || r.held == nil {
		return
	}
	r.mu.Lock()
	if r.released {
		r.mu.Unlock()
		return
	}
	r.released = true
	r.mu.Unlock()
	r.abandon()
}

// forceCancel drops the replacement even while Install is waiting, unless the
// stream was already committed.
func (r *CloudRecoveryRelease) forceCancel() {
	if r == nil || r.held == nil {
		return
	}
	r.mu.Lock()
	if r.installed {
		r.mu.Unlock()
		return
	}
	r.released = true
	r.mu.Unlock()
	r.abandon()
}

func (r *CloudRecoveryRelease) abandon() {
	r.cancelOnce.Do(func() {
		close(r.held.abandoned)
		r.held.cancel()
	})
}

func (r *CloudRecoveryRelease) markInstalled() {
	r.mu.Lock()
	r.installed = true
	r.mu.Unlock()
}

func (r *CloudRecoveryRelease) clearInstalled() {
	r.mu.Lock()
	r.installed = false
	r.mu.Unlock()
}

// heldRecovery is the dispatch-goroutine record of one held replacement.
type heldRecovery struct {
	threadID  string
	deadline  time.Time
	install   chan chan *RPCError
	abandoned chan struct{}
	// done is closed once the stream is committed, so the attach goroutine can
	// stop waiting without cancelling the installed stream.
	done      chan struct{}
	doneOnce  sync.Once
	cancel    context.CancelFunc
	release   *CloudRecoveryRelease
	installed atomic.Bool
	finished  atomic.Bool
}

// heldOpened is the handoff from the attach goroutine to the dispatch
// goroutine. The worker installs the attachment and acknowledges through ack.
type heldOpened struct {
	held       *heldRecovery
	attachment *openedAttachment
	ack        chan *RPCError
}

type resumeDisconnectedRequest struct {
	threadID       string
	requireHistory bool
	deadline       time.Time
	reply          chan resumeDisconnectedReply
}

type resumeDisconnectedReply struct {
	settings *appserver.ThreadResumeResponse
	release  *CloudRecoveryRelease
	err      *RPCError
}

type openedHeldStream struct {
	attachment *openedAttachment
	err        error
}

func recoveryRPCError(message string) *RPCError {
	return &RPCError{Code: internalErrorCode, Message: message}
}

func signInChangedRPCError() *RPCError {
	return recoveryRPCError(CloudRecoverySignInChanged{}.Error())
}

// deliverRecoveryAck commits through the installer's acknowledgement. A caller
// that gave up first leaves no receiver, so the send fails and the stream is
// dropped instead of installed.
func deliverRecoveryAck(ack chan *RPCError, rpcErr *RPCError) bool {
	if ack == nil {
		return false
	}
	select {
	case ack <- rpcErr:
		return true
	default:
		return false
	}
}

// ResumeDisconnectedThread reattaches a loaded, disconnected Cloud
// conversation and returns its cached settings plus a release handle. The
// replacement stream is held until Install, and Cancel drops it. requireHistory
// mirrors the upstream flag, which the client accepts but does not use to
// change reattachment.
func (c *Client) ResumeDisconnectedThread(
	ctx context.Context,
	threadID string,
	requireHistory bool,
	deadline time.Time,
) (*appserver.ThreadResumeResponse, *CloudRecoveryRelease, *RPCError) {
	if c == nil {
		return nil, nil, &RPCError{Code: internalErrorCode, Message: "cloud client is unavailable"}
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if !deadline.IsZero() {
		if !time.Now().Before(deadline) {
			return nil, nil, recoveryRPCError(cloudRecoveryTimeoutReason)
		}
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
	}
	request := &resumeDisconnectedRequest{
		threadID:       strings.TrimSpace(threadID),
		requireHistory: requireHistory,
		deadline:       deadline,
		reply:          make(chan resumeDisconnectedReply, 1),
	}
	select {
	case c.resumes <- request:
	case <-c.done:
		return nil, nil, &RPCError{Code: internalErrorCode, Message: "cloud client is closed"}
	case <-ctx.Done():
		return nil, nil, resumedContextError(ctx)
	}
	select {
	case reply := <-request.reply:
		return reply.settings, reply.release, reply.err
	case <-c.done:
		return nil, nil, &RPCError{Code: internalErrorCode, Message: "cloud client is closed"}
	case <-ctx.Done():
		return nil, nil, resumedContextError(ctx)
	}
}

// resumedContextError maps an expired recovery deadline to Rust's timeout
// message and any other cancellation to its context error.
func resumedContextError(ctx context.Context) *RPCError {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return recoveryRPCError(cloudRecoveryTimeoutReason)
	}
	return &RPCError{Code: internalErrorCode, Message: ctx.Err().Error()}
}

// handleResumeDisconnectedThread starts one held reattachment on the dispatch
// goroutine.
func (c *Client) handleResumeDisconnectedThread(request *resumeDisconnectedRequest) {
	reply := func(err *RPCError) {
		request.reply <- resumeDisconnectedReply{err: err}
	}
	// A personal-access-token session has no locally recheckable sign-in, so it
	// must reopen the conversation instead (Rust idle::Recovery::start).
	if c.recoveryOptions.LoginCurrent == nil {
		reply(recoveryRPCError(cloudRecoveryPATReason))
		return
	}
	// Retire a canceled hold before deciding whether another can start.
	c.serviceHeldRecovery()
	if request.threadID == "" || c.loaded == nil || c.loaded.Thread == nil ||
		strings.TrimSpace(c.loaded.Thread.ID) != request.threadID ||
		c.active != nil || c.held != nil {
		reply(recoveryRPCError(cloudRecoveryNoAttachmentReason))
		return
	}
	if !request.deadline.IsZero() && !time.Now().Before(request.deadline) {
		reply(recoveryRPCError(cloudRecoveryTimeoutReason))
		return
	}
	// Rust's Recovery::start cancels a pending quiet reopen before holding a
	// replacement; two live streams for one conversation must not race.
	c.cancelIdleRecovery()
	// Start a fresh warning epoch before any recovery history is read.
	c.warnedPartialHistory = ""
	parent := c.runCtx
	if parent == nil {
		parent = context.Background()
	}
	recoveryCtx, cancel := context.WithCancel(parent)
	if !request.deadline.IsZero() {
		recoveryCtx, cancel = context.WithDeadline(parent, request.deadline)
	}
	held := &heldRecovery{
		threadID:  request.threadID,
		deadline:  request.deadline,
		install:   make(chan chan *RPCError),
		abandoned: make(chan struct{}),
		done:      make(chan struct{}),
		cancel:    cancel,
	}
	held.release = &CloudRecoveryRelease{held: held}
	c.held = held
	go c.attachHeldRecovery(recoveryCtx, held, cloneResumeResponse(c.loaded), request.reply)
}

// cloneResumeResponse keeps the cached settings returned to the caller stable
// while later commands may update the loaded conversation.
func cloneResumeResponse(settings *appserver.ThreadResumeResponse) *appserver.ThreadResumeResponse {
	if settings == nil {
		return nil
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return settings
	}
	clone := new(appserver.ThreadResumeResponse)
	if err := json.Unmarshal(encoded, clone); err != nil {
		return settings
	}
	return clone
}

// attachHeldRecovery opens the replacement stream, returns the cached settings
// once the stream is usable, and then waits for the caller to install it.
func (c *Client) attachHeldRecovery(
	ctx context.Context,
	held *heldRecovery,
	cached *appserver.ThreadResumeResponse,
	reply chan resumeDisconnectedReply,
) {
	// The deadline bounds the attempt and the install wait, never the stream it
	// commits: an installed replacement outlives the recovery deadline.
	streamCtx, streamCancel := context.WithCancel(context.WithoutCancel(ctx))
	keepStream := false
	defer func() {
		if !keepStream {
			streamCancel()
		}
	}()
	opened := make(chan openedHeldStream, 1)
	go func() {
		attachment, err := c.openAttachment(streamCtx, held.threadID)
		opened <- openedHeldStream{attachment: attachment, err: err}
	}()
	var attachment *openedAttachment
	select {
	case result := <-opened:
		if result.err != nil {
			streamCancel()
			reply <- resumeDisconnectedReply{err: c.heldOpenError(ctx, result.err)}
			c.finishHeldRecovery(held)
			return
		}
		attachment = result.attachment
		// The attachment owns the stream context from here on.
		keepStream = true
	case <-ctx.Done():
		streamCancel()
		reply <- resumeDisconnectedReply{err: recoveryRPCError(cloudRecoveryTimeoutReason)}
		c.finishHeldRecovery(held)
		return
	case <-held.abandoned:
		streamCancel()
		c.finishHeldRecovery(held)
		return
	}
	if !c.recoveryLoginCurrent() {
		attachment.cancel()
		reply <- resumeDisconnectedReply{err: signInChangedRPCError()}
		c.finishHeldRecovery(held)
		return
	}
	// Attach cannot prove that no whole turn was missed while disconnected.
	// Return the settings already loaded for this thread; the caller refreshes
	// history before installing the stream.
	reply <- resumeDisconnectedReply{settings: cached, release: held.release}
	select {
	case ack := <-held.install:
		if !c.recoveryLoginCurrent() {
			attachment.cancel()
			deliverRecoveryAck(ack, signInChangedRPCError())
			c.finishHeldRecovery(held)
			return
		}
		select {
		case c.heldOpened <- heldOpened{held: held, attachment: attachment, ack: ack}:
		case <-ctx.Done():
			attachment.cancel()
			deliverRecoveryAck(ack, recoveryRPCError(cloudRecoveryTimeoutReason))
			c.finishHeldRecovery(held)
			return
		case <-held.abandoned:
			attachment.cancel()
			c.finishHeldRecovery(held)
			return
		}
		// Hold until the dispatch side commits or drops the stream so a
		// replacement never outlives its deadline.
		select {
		case <-held.done:
		case <-ctx.Done():
			c.finishHeldRecovery(held)
		case <-held.abandoned:
			c.finishHeldRecovery(held)
		}
	case <-ctx.Done():
		attachment.cancel()
		c.finishHeldRecovery(held)
	case <-held.abandoned:
		attachment.cancel()
		c.finishHeldRecovery(held)
	}
}

func (c *Client) heldOpenError(ctx context.Context, err error) *RPCError {
	if errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return recoveryRPCError(cloudRecoveryTimeoutReason)
	}
	return recoveryRPCError(recoveryFailureReason(err))
}

// finishHeldRecovery retires a replacement that will never be installed.
func (c *Client) finishHeldRecovery(held *heldRecovery) {
	if c == nil || held == nil || !held.finished.CompareAndSwap(false, true) {
		return
	}
	held.cancel()
	select {
	case c.heldFinished <- held:
	case <-c.done:
	}
}

// serviceHeldRecovery retires abandoned holds and commits a ready handoff
// through its acknowledgement. It never blocks and is safe to call before any
// command or while a request is listening to the reply stream.
func (c *Client) serviceHeldRecovery() {
	// Retire a hold whose release was canceled or whose deadline passed even
	// before the attach goroutine unwinds, so an immediate retry can start.
	if held := c.held; held != nil {
		expired := !held.deadline.IsZero() && !time.Now().Before(held.deadline)
		if expired || held.isAbandoned() {
			c.held = nil
			held.cancel()
		}
	}
	for {
		select {
		case held := <-c.heldFinished:
			if c.held == held {
				c.held = nil
			}
		case opened := <-c.heldOpened:
			c.installHeldRecovery(opened)
		default:
			return
		}
	}
}

func (h *heldRecovery) isAbandoned() bool {
	if h == nil {
		return false
	}
	select {
	case <-h.abandoned:
		return true
	default:
		return false
	}
}

func (c *Client) installHeldRecovery(opened heldOpened) {
	if opened.held != c.held {
		opened.attachment.cancel()
		deliverRecoveryAck(opened.ack, recoveryRPCError(cloudRecoveryTimeoutReason))
		return
	}
	c.held = nil
	drop := func(rpcErr *RPCError) {
		opened.attachment.cancel()
		deliverRecoveryAck(opened.ack, rpcErr)
		c.finishHeldRecovery(opened.held)
	}
	if !c.recoveryLoginCurrent() {
		drop(signInChangedRPCError())
		return
	}
	if err := checkHeldAttachment(opened.attachment); err != nil {
		drop(recoveryRPCError(cloudRecoveryStreamLostReason))
		return
	}
	// Commit through the acknowledgement before installing, so a concurrent
	// Cancel cannot tear down a stream the caller was told is live.
	opened.held.release.markInstalled()
	if !deliverRecoveryAck(opened.ack, nil) {
		opened.held.release.clearInstalled()
		opened.attachment.cancel()
		c.finishHeldRecovery(opened.held)
		return
	}
	opened.held.doneOnce.Do(func() { close(opened.held.done) })
	c.recovery.installed = time.Now()
	c.recovery.observedIdle = nil
	c.supersedeStreamLosses(opened.held.threadID)
	c.active = &attachment{
		threadID: opened.held.threadID,
		events:   opened.attachment.events,
		errs:     opened.attachment.errs,
		cancel:   opened.attachment.cancel,
	}
	if opened.attachment.first != nil {
		c.forwardEvent(opened.held.threadID, *opened.attachment.first)
	}
}

// checkHeldAttachment rejects a held stream that ended between Attach and
// installation. A buffered event proves the stream is alive and is kept as the
// first event to forward.
func checkHeldAttachment(opened *openedAttachment) error {
	if opened == nil || opened.cancel == nil {
		return errCloudRecoveryStreamLost
	}
	if opened.first == nil {
		select {
		case event, ok := <-opened.events:
			if !ok {
				return errCloudRecoveryStreamLost
			}
			opened.first = &event
			return nil
		default:
		}
		select {
		case err, ok := <-opened.errs:
			if !ok || err != nil {
				return errCloudRecoveryStreamLost
			}
			opened.errs = nil
		default:
		}
	}
	return nil
}

var errCloudRecoveryStreamLost = errors.New(cloudRecoveryStreamLostReason)

func (c *Client) recoveryLoginCurrent() bool {
	return c != nil && c.recoveryOptions.LoginCurrent != nil && c.recoveryOptions.LoginCurrent()
}

// cancelHeldRecovery drops a held replacement, leaving the conversation
// disconnected for a later attempt.
func (c *Client) cancelHeldRecovery() {
	if c == nil || c.held == nil {
		return
	}
	held := c.held
	c.held = nil
	held.release.forceCancel()
}

// heldHistoryThread reports the conversation a history read targets.
func heldHistoryThread(method appserver.Method, params json.RawMessage) string {
	if method != appserver.MethodThreadTurnsList && method != appserver.MethodThreadItemsList {
		return ""
	}
	var request struct {
		ThreadID string `json:"threadId"`
	}
	if err := json.Unmarshal(params, &request); err != nil {
		return ""
	}
	return strings.TrimSpace(request.ThreadID)
}

// handleHeldHistory runs one recovery history read under the shared deadline
// and the original sign-in, withholding a page that finishes after the sign-in
// changed (Rust cloud_backend run_worker).
func (c *Client) handleHeldHistory(
	requestCtx context.Context,
	held *heldRecovery,
	read func(context.Context) (json.RawMessage, *RPCError),
) (json.RawMessage, *RPCError) {
	if held == nil {
		return read(requestCtx)
	}
	if !held.deadline.IsZero() && !time.Now().Before(held.deadline) {
		return nil, recoveryRPCError(cloudRecoveryTimeoutReason)
	}
	parent := requestCtx
	if parent == nil {
		parent = c.runCtx
	}
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	if !held.deadline.IsZero() {
		ctx, cancel = context.WithDeadline(parent, held.deadline)
	}
	defer cancel()
	if !c.recoveryLoginCurrent() {
		return nil, signInChangedRPCError()
	}
	result, rpcErr := read(ctx)
	if !held.deadline.IsZero() && !time.Now().Before(held.deadline) {
		return nil, recoveryRPCError(cloudRecoveryTimeoutReason)
	}
	if !c.recoveryLoginCurrent() {
		return nil, signInChangedRPCError()
	}
	return result, rpcErr
}
