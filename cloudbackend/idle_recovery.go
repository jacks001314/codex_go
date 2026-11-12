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
}

type idleRecoveryResult struct {
	threadID  string
	connected bool
	events    <-chan cloudclient.Event
	errs      <-chan error
	cancel    context.CancelFunc
	first     *cloudclient.Event
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
	ctx, cancel := context.WithTimeout(c.runCtx, idleRecoveryTotalTimeout)
	results := c.recoveryResults
	source := c.source
	go func() {
		defer cancel()
		results <- attachWithRetry(ctx, source, threadID, loginCurrent)
	}()
	return true
}

// attachWithRetry runs one recovery episode. It returns a connected stream with
// at most one buffered event, or an unsuccessful result.
func attachWithRetry(ctx context.Context, source cloudSource, threadID string, loginCurrent func() bool) idleRecoveryResult {
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
		if ctx.Err() != nil || !loginCurrent() {
			return idleRecoveryResult{threadID: threadID}
		}
		streamCtx, streamCancel := context.WithCancel(ctx)
		events, errs, err := source.Attach(streamCtx, threadID)
		if err != nil {
			streamCancel()
			if transientCloudError(err) {
				continue
			}
			return idleRecoveryResult{threadID: threadID}
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
				return idleRecoveryResult{threadID: threadID}
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
					return idleRecoveryResult{threadID: threadID}
				}
				continue
			}
		default:
		}
		// A healthy idle stream needs no event to prove that it is ready.
		if !loginCurrent() {
			streamCancel()
			return idleRecoveryResult{threadID: threadID}
		}
		return idleRecoveryResult{threadID: threadID, connected: true, events: events, errs: errs, cancel: streamCancel}
	}
	return idleRecoveryResult{threadID: threadID}
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
		c.reportStreamLoss(result.threadID)
		return
	}
	c.recovery.installed = time.Now()
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
	c.recovery.state = idleReconnectInactive
	c.recovery.root = ""
	c.recovery.pending = false
	c.recovery.installed = time.Time{}
	c.recovery.observedIdle = nil
}

// reportStreamLoss records one undelivered loss and queues its notification.
func (c *Client) reportStreamLoss(threadID string) {
	c.recordStreamLoss(threadID)
	c.enqueue(cloudStreamEndedNotification(threadID))
}
