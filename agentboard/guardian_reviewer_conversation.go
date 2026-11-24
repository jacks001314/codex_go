package agentboard

import (
	"context"
	"errors"
	"sync"

	"codex_go/utils"
)

// GuardianReviewerConversation is a retained Guardian reviewer conversation that
// admits queued evidence without starting a model turn (Rust guardian-v3
// Conversation). The cursor lives on the conversation, so a reused reviewer
// receives each queued original once and a replacement replays the inbox.
type GuardianReviewerConversation struct {
	thread    string
	inbox     *GuardianActorInbox
	policy    utils.TruncationPolicy
	delivered int
}

// NewGuardianReviewerConversation binds a retained reviewer thread to the inbox
// its originals arrive on and the reviewer's selected model budget.
func NewGuardianReviewerConversation(thread string, inbox *GuardianActorInbox, policy utils.TruncationPolicy) *GuardianReviewerConversation {
	return &GuardianReviewerConversation{thread: thread, inbox: inbox, policy: policy}
}

// Thread identifies the retained reviewer conversation.
func (c *GuardianReviewerConversation) Thread() string {
	if c == nil {
		return ""
	}
	return c.thread
}

// Inbox is the queue this conversation drains.
func (c *GuardianReviewerConversation) Inbox() *GuardianActorInbox {
	if c == nil {
		return nil
	}
	return c.inbox
}

// Delivered is the number of queued originals this conversation already
// admitted.
func (c *GuardianReviewerConversation) Delivered() int {
	if c == nil {
		return 0
	}
	return c.delivered
}

// Deliver admits the originals queued when this drain began and records only
// that prefix, so later arrivals wait for the next drain (Rust
// Conversation::deliver).
func (c *GuardianReviewerConversation) Deliver(ctx context.Context, actor string, board AgentMessageBoard, admit func(context.Context, GuardianEvidenceItem) error) error {
	if c == nil || c.inbox == nil {
		return errors.New("Guardian reviewer conversation is unavailable")
	}
	through := c.inbox.Len()
	if c.delivered >= through {
		return nil
	}
	return c.inbox.Deliver(ctx, actor, board, &c.delivered, through, c.policy, admit)
}

// GuardianReviewerSlot holds the conversation currently reserved for verdicts.
// Idle delivery takes the conversation out of the slot so a verdict and a drain
// never write the same retained history, and only restores it after the drain
// succeeded.
type GuardianReviewerSlot struct {
	mu      sync.Mutex
	current *GuardianReviewerConversation
}

// Put installs the current conversation, replacing any previous one.
func (s *GuardianReviewerSlot) Put(conversation *GuardianReviewerConversation) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.current = conversation
}

// Take removes and returns the current conversation, or nil when there is none.
func (s *GuardianReviewerSlot) Take() *GuardianReviewerConversation {
	s.mu.Lock()
	defer s.mu.Unlock()
	current := s.current
	s.current = nil
	return current
}

// TakeIf removes the current conversation only when it is the expected one
// (Rust Arc::ptr_eq on the reviewer thread).
func (s *GuardianReviewerSlot) TakeIf(conversation *GuardianReviewerConversation) *GuardianReviewerConversation {
	s.mu.Lock()
	defer s.mu.Unlock()
	if conversation == nil || s.current != conversation {
		return nil
	}
	current := s.current
	s.current = nil
	return current
}

// CurrentThread reports the thread of the installed conversation, if any. It
// never removes the conversation.
func (s *GuardianReviewerSlot) CurrentThread() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.current.Thread()
}

// ObserveInbox drains queued evidence whenever the reviewer is idle: each inbox
// wakeup takes the conversation out of the slot, admits the originals queued at
// that moment, and restores the conversation only after delivery succeeded. A
// failed delivery retires the conversation instead of restoring a history that
// may be half-written, and the returned error lets the caller rebuild it (Rust
// ActorReviewer::observe_inbox). The observer returns nil when ctx ends or the
// inbox closes.
func (s *GuardianReviewerSlot) ObserveInbox(ctx context.Context, conversation *GuardianReviewerConversation, actor string, board AgentMessageBoard, admit func(context.Context, GuardianEvidenceItem) error) error {
	if s == nil || conversation == nil || conversation.inbox == nil {
		return errors.New("Guardian reviewer conversation is unavailable")
	}
	inbox := conversation.inbox
	for {
		if inbox.Closed() {
			return nil
		}
		if conversation.Delivered() < inbox.Len() {
			current := s.TakeIf(conversation)
			if current == nil {
				return nil
			}
			if err := current.Deliver(ctx, actor, board, admit); err != nil {
				return err
			}
			s.Put(current)
			continue
		}
		wake := inbox.Subscribe()
		if inbox.Closed() {
			return nil
		}
		if conversation.Delivered() < inbox.Len() {
			continue
		}
		select {
		case <-ctx.Done():
			return nil
		case <-wake:
		}
	}
}
