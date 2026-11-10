package realtime

import (
	"context"
	"errors"
	"sync"
	"time"
)

// ErrRealtimeAttachmentCanceled reports that an attachment was cancelled or
// that its start deadline passed before the connection was published. It is
// the Go counterpart of the `TurnAborted` error Rust returns from
// `handle_attach` (Rust #60443).
var ErrRealtimeAttachmentCanceled = errors.New("realtime attachment canceled")

// AttachmentOptions mirrors Rust's `RealtimeAttachmentOptions` (#60443): a
// caller may cancel a pending connection attempt or bound how long the attempt
// may take to publish a connection.
type AttachmentOptions struct {
	// Cancel carries the caller's cancellation. A nil handle keeps the attempt
	// uncancellable. Closing the handle without cancelling lets the attempt
	// continue, which matches a dropped `watch::Sender` whose value stayed
	// `false`.
	Cancel *AttachmentCancellation
	// StartBefore is the optional caller deadline for publishing the
	// connection. Cleanup can still finish after the deadline passes.
	StartBefore *time.Time
}

// AttachmentCancellation mirrors Rust's `watch::Receiver<bool>` used for
// attachment cancellation: the value is readable at any time, waiters wake
// when it changes, and a handle closed without ever cancelling never cancels
// the attempt.
type AttachmentCancellation struct {
	mu      sync.Mutex
	changed chan struct{}
	value   bool
	closed  bool
}

// NewAttachmentCancellation returns a cancellation handle whose initial value
// is false.
func NewAttachmentCancellation() *AttachmentCancellation {
	return &AttachmentCancellation{changed: make(chan struct{})}
}

// Cancel flips the value to true and wakes every waiter.
func (c *AttachmentCancellation) Cancel() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.value || c.closed {
		return
	}
	c.value = true
	close(c.changed)
}

// Close drops the caller's handle without cancelling, so a pending attachment
// keeps running.
func (c *AttachmentCancellation) Close() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.value || c.closed {
		return
	}
	c.closed = true
	close(c.changed)
}

// Cancelled reports the current value.
func (c *AttachmentCancellation) Cancelled() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.value
}

// changedSignal returns the channel that closes when the value changes.
func (c *AttachmentCancellation) changedSignal() <-chan struct{} {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.changed
}

// realtimeAttachmentControl mirrors Rust's `RealtimeAttachmentControl`
// (#60443). It combines the caller's cancellation with the start deadline,
// interrupts pending connection work, and leaves a published connection
// resident when cancellation arrives afterwards.
type realtimeAttachmentControl struct {
	options  AttachmentOptions
	ctx      context.Context
	cancel   context.CancelFunc
	stop     chan struct{}
	sealOnce sync.Once
	mu       sync.Mutex
	sealed   bool
}

// newRealtimeAttachmentControl returns nil when the start carries no
// attachment options, which leaves every start path unchanged.
func newRealtimeAttachmentControl(parent context.Context, options *StartOptions) *realtimeAttachmentControl {
	if options == nil || options.Attachment == nil {
		return nil
	}
	if parent == nil {
		parent = context.Background()
	}
	control := &realtimeAttachmentControl{options: *options.Attachment, stop: make(chan struct{})}
	control.ctx, control.cancel = context.WithCancel(parent)
	control.watch()
	return control
}

func (c *realtimeAttachmentControl) context() context.Context {
	if c == nil {
		return nil
	}
	return c.ctx
}

// close stops watching the cancellation sources. It never touches a published
// connection.
func (c *realtimeAttachmentControl) close() {
	if c == nil {
		return
	}
	c.sealOnce.Do(func() {
		close(c.stop)
	})
}

// isCancelled reports whether the caller cancelled or the deadline passed.
func (c *realtimeAttachmentControl) isCancelled() bool {
	if c == nil {
		return false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.cancelledLocked()
}

func (c *realtimeAttachmentControl) cancelledLocked() bool {
	if c.options.Cancel.Cancelled() {
		return true
	}
	return c.options.StartBefore != nil && !time.Now().Before(*c.options.StartBefore)
}

// attachmentError reports a cancelled or expired attachment instead of the
// transport error its interruption produced.
func (c *realtimeAttachmentControl) attachmentError(err error) error {
	if err != nil && c.isCancelled() {
		return ErrRealtimeAttachmentCanceled
	}
	return err
}

// commit publishes the connection while blocking concurrent cancellation, so a
// connection that is published stays resident even when its caller cancels
// right after publication. Rust holds the cancellation guard through the same
// publication for this reason (#60443); commit reports false when the attempt
// lost the race and must be cleaned up instead.
func (c *realtimeAttachmentControl) commit(publish func()) bool {
	if c == nil {
		publish()
		return true
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.cancelledLocked() {
		return false
	}
	publish()
	c.sealed = true
	c.close()
	return true
}

func (c *realtimeAttachmentControl) watch() {
	var changed <-chan struct{}
	if c.options.Cancel != nil {
		changed = c.options.Cancel.changedSignal()
	}
	var expiry <-chan time.Time
	var timer *time.Timer
	if c.options.StartBefore != nil {
		timer = time.NewTimer(time.Until(*c.options.StartBefore))
		expiry = timer.C
	}
	if changed == nil && expiry == nil {
		return
	}
	go func() {
		if timer != nil {
			defer timer.Stop()
		}
		select {
		case <-changed:
			if !c.options.Cancel.Cancelled() {
				// The caller dropped its handle without cancelling.
				return
			}
			c.interrupt()
		case <-expiry:
			c.interrupt()
		case <-c.stop:
		}
	}()
}

// interrupt cancels pending connection work unless the connection was already
// published.
func (c *realtimeAttachmentControl) interrupt() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.sealed {
		// Publication commits success; a later cancellation must preserve
		// residency until the caller observes the started session.
		return
	}
	c.cancel()
}
