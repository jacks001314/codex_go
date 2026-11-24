package agentboard

import (
	"context"
	"encoding/json"
	"errors"
	"sync"

	"codex_go/utils"
)

// GuardianSamplerConversation models Rust's HousekeeperSampler conversation
// (ext/guardian-v3/src/housekeeper_sampler.rs): one append-only history of
// completed routing replies. The tool-free instruction message is rebuilt by
// the request and never enters this history. Under window pressure only whole,
// completed batches are evicted, oldest first.
type GuardianSamplerConversation struct {
	mu sync.Mutex
	// history items are the committed routing replies in source order; the
	// in-flight batch input is appended for the duration of one sample and
	// replaced by its reply at Commit.
	history        []GuardianSamplerHistoryItem
	batches        []guardianRetainedSamplerBatch
	retainedTokens int
	pending        bool
	pendingTokens  int
}

// GuardianSamplerHistoryItem is one retained model-visible item.
type GuardianSamplerHistoryItem struct {
	Item   json.RawMessage
	Tokens int
}

type guardianRetainedSamplerBatch struct {
	items  int
	tokens int
}

// NewGuardianSamplerConversation starts an empty conversation.
func NewGuardianSamplerConversation() *GuardianSamplerConversation {
	return &GuardianSamplerConversation{}
}

// RetainedTokens is the accounted size of the retained completed batches.
func (c *GuardianSamplerConversation) RetainedTokens() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.retainedTokens
}

// History returns the items a routing request sends after its instructions,
// including the batch input currently in flight.
func (c *GuardianSamplerConversation) History() []GuardianSamplerHistoryItem {
	if c == nil {
		return nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]GuardianSamplerHistoryItem(nil), c.history...)
}

// BeginBatch evicts completed batches until the retained history fits beside
// the in-flight batch, then appends that batch's input. fixedTokens is the
// tool-free instruction plus output schema; inputTokens sizes the batch input.
// It mirrors HousekeeperSampler::sample: a batch that cannot fit even an empty
// history is refused instead of being sent over the window.
func (c *GuardianSamplerConversation) BeginBatch(input json.RawMessage, inputTokens int, fixedTokens int, inputLimit int) error {
	if c == nil {
		return errors.New("Guardian sampler conversation is nil")
	}
	if inputTokens < 0 || fixedTokens < 0 {
		return errors.New("Guardian sampler token counts must not be negative")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.pending {
		return errors.New("Guardian sampler batch already in flight")
	}
	historyBudget := inputLimit - fixedTokens - inputTokens
	if historyBudget < 0 {
		return errors.New("housekeeper batch exceeds model input window")
	}
	removed := 0
	for c.retainedTokens > historyBudget {
		if len(c.batches) == 0 {
			c.history = c.history[:len(c.history)-removed]
			return errors.New("inconsistent housekeeper history")
		}
		batch := c.batches[0]
		c.batches = c.batches[1:]
		c.retainedTokens -= batch.tokens
		removed += batch.items
	}
	if removed > len(c.history) {
		return errors.New("inconsistent housekeeper history")
	}
	c.history = c.history[removed:]
	c.history = append(c.history, GuardianSamplerHistoryItem{Item: append(json.RawMessage(nil), input...), Tokens: inputTokens})
	c.pending = true
	c.pendingTokens = inputTokens
	return nil
}

// Commit records the sampled reply as one completed batch. It runs only after
// routing validation and board writes succeeded, mirroring the Rust commit.
func (c *GuardianSamplerConversation) Commit(output []json.RawMessage) error {
	if c == nil {
		return errors.New("Guardian sampler conversation is nil")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.pending {
		return errors.New("Guardian sampler has no batch in flight")
	}
	outputTokens := 0
	for _, item := range output {
		outputTokens += utils.ApproxTokenCount(string(item))
	}
	tokens := c.pendingTokens + outputTokens
	c.batches = append(c.batches, guardianRetainedSamplerBatch{items: 1 + len(output), tokens: tokens})
	for _, item := range output {
		c.history = append(c.history, GuardianSamplerHistoryItem{
			Item:   append(json.RawMessage(nil), item...),
			Tokens: utils.ApproxTokenCount(string(item)),
		})
	}
	c.retainedTokens += tokens
	c.pending = false
	c.pendingTokens = 0
	return nil
}

// AbandonBatch forgets an in-flight input after a failed sample so the next
// attempt starts from the last committed history.
func (c *GuardianSamplerConversation) AbandonBatch() {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.pending {
		return
	}
	if len(c.history) > 0 {
		c.history = c.history[:len(c.history)-1]
	}
	c.pending = false
	c.pendingTokens = 0
}

// GuardianSamplerAccountWatch cancels a routing batch when the account that
// owns the sampler changes (Rust HousekeeperSampler::account_changed). current
// reports whether the captured account revision is still the live one; changes
// fires whenever a newer revision is installed.
type GuardianSamplerAccountWatch struct {
	current func() bool
	changes <-chan struct{}
}

// NewGuardianSamplerAccountWatch binds one captured account revision. A nil
// current means the transport carries no owner, so the watch never fires.
func NewGuardianSamplerAccountWatch(current func() bool, changes <-chan struct{}) GuardianSamplerAccountWatch {
	return GuardianSamplerAccountWatch{current: current, changes: changes}
}

// Wait blocks until the account changes or ctx ends. It reports whether the
// batch must be abandoned because the account moved; a transport without an
// owner never reports a change (Rust's account_changed stays pending).
func (w GuardianSamplerAccountWatch) Wait(ctx context.Context) bool {
	if ctx == nil {
		ctx = context.Background()
	}
	if w.current == nil {
		<-ctx.Done()
		return false
	}
	for w.current() {
		select {
		case <-ctx.Done():
			return false
		case _, ok := <-w.changes:
			if !ok {
				return !w.current()
			}
		}
	}
	return true
}
