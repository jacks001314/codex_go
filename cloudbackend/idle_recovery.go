// Reopens only the lost reply stream of the displayed Cloud conversation
// (Rust #60969 cloud_backend::idle). A quiet reopen re-runs the same credential
// checks as the original Attach, retries transient failures inside one bounded
// episode, and buffers the first event so it is not lost while the stream is
// being replaced.
package cloudbackend

import (
	"context"
	"errors"
	"strings"
	"time"

	"codex_go/cloudclient"
	"google.golang.org/grpc/codes"
)

const (
	// idleRecoveryTotalTimeout bounds one accepted recovery episode, including
	// credential checks, Attach attempts, and retry backoff.
	idleRecoveryTotalTimeout = 8 * time.Second
	// idleRecoveryStableWindow rejects a reopen for a stream that died right
	// after it was installed; that stream never proved it could stay open.
	idleRecoveryStableWindow = 15 * time.Second
	// signInAgainReason is Rust's SIGN_IN_AGAIN. The credential that opened
	// this client is no longer current, so a reopen cannot succeed.
	signInAgainReason = "Your cloud sign-in changed or expired. Sign in again to access this conversation."
	// recoveryFailedReason reports a nontransient Attach failure that is not
	// one of the specific conversation access errors.
	recoveryFailedReason = "Cloud could not restore the reply stream for this conversation."
	// recoveryExhaustedReason reports that every bounded retry was transient.
	recoveryExhaustedReason = "Cloud did not restore the reply stream after several attempts."
	// recoveryKeepsDisconnectingReason reports that the quiet reopen replaced the
	// stream too many times in a row (Rust #62151). The transcript and draft stay
	// on screen, so the message says they are still available.
	recoveryKeepsDisconnectingReason = "The cloud reply stream keeps disconnecting. Your transcript and draft are still here."
	// idleRecoveryMaxReplacements bounds one recovery sequence. Every installed
	// replacement delays the next reopen further, and the next loss stops the
	// loop instead of reconnecting forever.
	idleRecoveryMaxReplacements = 3
	// idleRecoveryAttemptDelay is added per installed replacement before the next
	// quiet Attach, so a flapping stream backs off instead of hammering Cloud.
	idleRecoveryAttemptDelay = 500 * time.Millisecond
)

// idleRecoveryDelays is the retry schedule of one episode: an immediate
// attempt, then two backed-off retries.
var idleRecoveryDelays = []time.Duration{0, 500 * time.Millisecond, time.Second}

// IdleRecoveryOptions configures the quiet reopen of a lost reply stream. The
// Go cloud client holds a single active attachment, so that attachment is the
// conversation the caller is displaying.
type IdleRecoveryOptions struct {
	// LoginCurrent re-reads the stored ChatGPT credentials and reports whether
	// they still match the ones that opened this client. A nil value disables
	// idle recovery, because only OAuth credentials can be rechecked locally.
	LoginCurrent func() bool
	// Displayed optionally narrows the offer to the displayed conversation.
	// A nil value treats the attached conversation as displayed.
	Displayed func(threadID string) bool
	// OnReconnected reports a successful quiet reopen. It runs on the dispatch
	// goroutine, so it must not block; a client uses it to mark interrupted
	// history as potentially incomplete and to resume queued input.
	OnReconnected func(threadID string)
}

type idleReconnectState int

const (
	idleReconnectInactive idleReconnectState = iota
	idleReconnectOffered
	idleReconnectOpening
	idleReconnectConnected
)

// idleRecovery is owned by the dispatch goroutine.
type idleRecovery struct {
	state   idleReconnectState
	root    string
	pending bool
	// installed is when the current attachment was proven usable; the zero
	// value means an ordinary attachment that never needed recovery.
	installed time.Time
	// observedIdle is nil until either the activation snapshot or the first
	// status event establishes whether the conversation is idle.
	observedIdle *bool
	// disconnectedThread is the conversation whose in-process client was told
	// to hold input; the next live stream for it releases the hold.
	disconnectedThread string
	// attempts counts the replacement streams installed in the current recovery
	// sequence. It is cleared when recovery state resets and when a stream
	// disconnects only after staying connected long enough to be stable.
	attempts int
}

type idleRecoveryResult struct {
	threadID  string
	connected bool
	// failureReason is the specific disconnect reason Rust's idle::Login::attach
	// returns for a rejected recovery; empty means the generic stream-ended
	// message.
	failureReason string
	events        <-chan cloudclient.Event
	errs          <-chan error
	cancel        context.CancelFunc
	first         *cloudclient.Event
}

// blockedCloudRecoveryReason maps a non-retryable RPC status to Rust's
// blocked_recovery message (cloud_backend/idle.rs #61599). A missing
// conversation, lost access, and changed sign-in are reported specifically;
// other nontransient failures use the generic recovery message.
func blockedCloudRecoveryReason(err error) string {
	var rpcErr *cloudclient.RpcError
	if !errors.As(err, &rpcErr) {
		return ""
	}
	switch rpcErr.Status.Code {
	case codes.NotFound:
		return "Cloud could not find this conversation."
	case codes.PermissionDenied:
		return "Your account no longer has access to this cloud conversation."
	case codes.Unauthenticated:
		return signInAgainReason
	default:
		return ""
	}
}

// recoveryFailureReason resolves the disconnect message for a nontransient
// recovery error: the specific access error when known, otherwise the generic
// recovery failure.
func recoveryFailureReason(err error) string {
	if reason := blockedCloudRecoveryReason(err); reason != "" {
		return reason
	}
	return recoveryFailedReason
}

// transientCloudError reports whether a failed Attach or a stream end is worth
// retrying inside the episode (Rust idle::transient). A clean EOF has no error
// and is transient.
func transientCloudError(err error) bool {
	if err == nil {
		return true
	}
	if errors.Is(err, cloudclient.ErrTimeout) {
		return true
	}
	var rpcErr *cloudclient.RpcError
	if errors.As(err, &rpcErr) {
		switch rpcErr.Status.Code {
		case codes.Canceled, codes.Unknown, codes.DeadlineExceeded, codes.Internal, codes.Unavailable:
			return true
		}
		return false
	}
	return false
}

// setObservedIdle records whether the conversation is idle.
func (c *Client) setObservedIdle(idle bool) {
	if c == nil {
		return
	}
	value := idle
	c.recovery.observedIdle = &value
}

// noteActivationStatus seeds the idle observation from a thread start or resume
// snapshot; later events may refine it.
func (c *Client) noteActivationStatus(status string) {
	if c == nil || c.recovery.observedIdle != nil {
		return
	}
	c.setObservedIdle(strings.EqualFold(strings.TrimSpace(status), "idle"))
}

// observeIdleBusy downgrades a completed quiet reopen as soon as the
// conversation starts working again.
func (c *Client) observeIdleBusy() {
	c.setObservedIdle(false)
	if c.recovery.state == idleReconnectConnected {
		c.recovery.state = idleReconnectOpening
	}
}

// notifyIdleConnected records a stream that is usable again and forwards the
// one reconnected event.
func (c *Client) notifyIdleConnected(threadID string) {
	if c.recovery.state != idleReconnectOpening || c.recovery.root != threadID {
		return
	}
	c.recovery.state = idleReconnectConnected
	c.notifyStreamReconnected(threadID)
	if c.recoveryOptions.OnReconnected != nil {
		c.recoveryOptions.OnReconnected(threadID)
	}
}

// offerIdleReconnect offers one quiet Attach for the lost conversation and
// accepts it when that conversation is the displayed one.
func (c *Client) offerIdleReconnect(threadID string) bool {
	threadID = strings.TrimSpace(threadID)
	if c == nil || threadID == "" || c.recoveryOptions.LoginCurrent == nil || c.source == nil {
		return false
	}
	c.recovery.state = idleReconnectOffered
	c.recovery.root = threadID
	if displayed := c.recoveryOptions.Displayed; displayed != nil && !displayed(threadID) {
		return false
	}
	return c.startIdleRecovery(threadID)
}

// ReconnectIdleThread accepts an offered quiet Attach. It reports whether this
// call accepted the offer; hidden or dirty views keep explicit reopen
// behavior until they call this.
func (c *Client) ReconnectIdleThread(threadID string) bool {
	threadID = strings.TrimSpace(threadID)
	if c == nil || threadID == "" {
		return false
	}
	return c.startIdleRecovery(threadID)
}

func (c *Client) startIdleRecovery(threadID string) bool {
	if c.recovery.state != idleReconnectOffered || c.recovery.root != threadID || c.recovery.pending {
		return false
	}
	if c.runCtx == nil || c.runCtx.Err() != nil {
		return false
	}
	loginCurrent := c.recoveryOptions.LoginCurrent
	if loginCurrent == nil {
		return false
	}
	c.recovery.state = idleReconnectOpening
	c.recovery.pending = true
	// Rust #62151 answers a recovery request for a sequence that already
	// installed three replacements with a message that keeps the transcript and
	// draft in place, instead of attaching a fourth time.
	if c.recovery.attempts >= idleRecoveryMaxReplacements {
		results := c.recoveryResults
		results <- idleRecoveryResult{threadID: threadID, failureReason: recoveryKeepsDisconnectingReason}
		return true
	}
	ctx, cancel := context.WithTimeout(c.runCtx, idleRecoveryTotalTimeout)
	results := c.recoveryResults
	source := c.source
	attempts := c.recovery.attempts
	go func() {
		defer cancel()
		results <- attachWithRetry(ctx, source, threadID, loginCurrent, attempts)
	}()
	return true
}

// attachWithRetry runs one recovery episode. It returns a connected stream with
// at most one buffered event, or an unsuccessful result.
func attachWithRetry(ctx context.Context, source cloudSource, threadID string, loginCurrent func() bool, attempts int) idleRecoveryResult {
	// Rust #62151 delays each replacement by 500 ms per replacement already
	// installed in this sequence, then attaches under the same login permit.
	if attempts > 0 {
		timer := time.NewTimer(time.Duration(attempts) * idleRecoveryAttemptDelay)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return idleRecoveryResult{threadID: threadID}
		}
	}
	for _, delay := range idleRecoveryDelays {
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return idleRecoveryResult{threadID: threadID}
			}
		}
		if ctx.Err() != nil {
			return idleRecoveryResult{threadID: threadID}
		}
		if !loginCurrent() {
			return idleRecoveryResult{threadID: threadID, failureReason: signInAgainReason}
		}
		streamCtx, streamCancel := context.WithCancel(ctx)
		events, errs, err := source.Attach(streamCtx, threadID)
		if err != nil {
			streamCancel()
			if transientCloudError(err) {
				continue
			}
			return idleRecoveryResult{threadID: threadID, failureReason: recoveryFailureReason(err)}
		}
		// A buffered first event is kept with the stream, so the event that
		// proved the Attach usable is forwarded once the stream is installed.
		select {
		case event, ok := <-events:
			if !ok {
				streamCancel()
				continue
			}
			if !loginCurrent() {
				streamCancel()
				return idleRecoveryResult{threadID: threadID, failureReason: signInAgainReason}
			}
			return idleRecoveryResult{threadID: threadID, connected: true, events: events, errs: errs, cancel: streamCancel, first: &event}
		default:
		}
		// A server can close Attach right after returning successful headers.
		// An immediate clean end or transient failure uses the retry budget.
		select {
		case err, ok := <-errs:
			if !ok || err != nil {
				streamCancel()
				if ok && err != nil && !transientCloudError(err) {
					return idleRecoveryResult{threadID: threadID, failureReason: recoveryFailureReason(err)}
				}
				continue
			}
		default:
		}
		// A healthy idle stream needs no event to prove that it is ready.
		if !loginCurrent() {
			streamCancel()
			return idleRecoveryResult{threadID: threadID, failureReason: signInAgainReason}
		}
		return idleRecoveryResult{threadID: threadID, connected: true, events: events, errs: errs, cancel: streamCancel}
	}
	return idleRecoveryResult{threadID: threadID, failureReason: recoveryExhaustedReason}
}

// handleIdleRecoveryResult installs a recovered attachment or, when the episode
// failed, reports the loss that was held back while the reopen was attempted.
func (c *Client) handleIdleRecoveryResult(result idleRecoveryResult) {
	if result.threadID == "" {
		return
	}
	if c.recovery.state != idleReconnectOpening || c.recovery.root != result.threadID {
		// A deliberate selection or detach owns the conversation now; drop the
		// stream this episode produced.
		if result.connected && result.cancel != nil {
			result.cancel()
		}
		c.recovery.pending = false
		return
	}
	c.recovery.pending = false
	if !result.connected {
		c.recovery.state = idleReconnectInactive
		c.recovery.root = ""
		c.reportStreamLossWithReason(result.threadID, result.failureReason)
		return
	}
	c.recovery.installed = time.Now()
	c.recovery.attempts++
	c.active = &attachment{threadID: result.threadID, events: result.events, errs: result.errs, cancel: result.cancel}
	c.supersedeStreamLosses(result.threadID)
	if result.first != nil {
		c.forwardEvent(result.threadID, *result.first)
	}
	if c.recovery.observedIdle == nil || *c.recovery.observedIdle {
		c.notifyIdleConnected(result.threadID)
	}
}

// holdStreamInput tells the in-process client to hold submissions for threadID
// until a live stream is installed again.
func (c *Client) holdStreamInput(threadID string) {
	if c == nil || strings.TrimSpace(threadID) == "" {
		return
	}
	c.recovery.disconnectedThread = threadID
	c.enqueue(cloudStreamDisconnectedNotification(threadID))
}

// notifyStreamReconnected releases a hold once a live stream for the same
// conversation is installed, whether by the quiet reopen or an explicit one.
func (c *Client) notifyStreamReconnected(threadID string) {
	if c == nil || c.recovery.disconnectedThread == "" || c.recovery.disconnectedThread != threadID {
		return
	}
	c.recovery.disconnectedThread = ""
	c.enqueue(cloudStreamReconnectedNotification(threadID))
}

// cancelIdleRecovery drops an offered or opening quiet Attach. It reports the
// conversation whose pending reopen was canceled, so a failed selection can
// still surface the loss.
func (c *Client) cancelIdleRecovery() (string, bool) {
	if !c.recovery.pending {
		c.recovery.state = idleReconnectInactive
		c.recovery.root = ""
		return "", false
	}
	threadID := c.recovery.root
	opening := c.recovery.state == idleReconnectOpening
	c.recovery.pending = false
	c.recovery.state = idleReconnectInactive
	c.recovery.root = ""
	return threadID, opening
}

// resetIdleRecovery forgets the previous conversation's recovery state, as a new
// attachment does.
func (c *Client) resetIdleRecovery() {
	c.cancelHeldRecovery()
	c.recovery.state = idleReconnectInactive
	c.recovery.root = ""
	c.recovery.pending = false
	c.recovery.installed = time.Time{}
	c.recovery.observedIdle = nil
	c.recovery.attempts = 0
}

// reportStreamLoss records one undelivered loss and queues its notification.
func (c *Client) reportStreamLoss(threadID string) {
	c.reportStreamLossWithReason(threadID, "")
}

// reportStreamLossWithReason queues one loss with the specific disconnect
// reason Rust's report_loss accepts (#61599). An empty reason keeps the generic
// stream-ended message.
func (c *Client) reportStreamLossWithReason(threadID string, reason string) {
	c.recordStreamLoss(threadID)
	c.enqueue(cloudStreamEndedNotificationWithReason(threadID, reason))
}
