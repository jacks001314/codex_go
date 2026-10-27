package turn

import (
	"fmt"
	"strings"
	"sync"

	"codex_go/state"
	"codex_go/tool"
)

type SteerMailbox struct {
	mu       sync.Mutex
	items    map[string][]any
	metadata map[string]map[string]string
	// phases carries the turn's mailbox delivery phase (Rust
	// state::MailboxDeliveryPhase, held by TurnState in Rust). The absence of a
	// key is Rust's default CurrentTurn.
	phases map[string]state.MailboxDeliveryPhase
	// finishing carries the turns whose final batch is being recorded (Rust
	// #53452's ActiveTurn::finishing). Input admission is closed for those turns
	// even though their delivery phase has not moved to the next turn, so late
	// mail cannot be queued after the final batch was drained. A turn that is
	// reserved but has not started has no entry and still accepts input.
	finishing map[string]chan struct{}
	// changed is closed and replaced on every enqueue that stores input, so
	// watchers wake without polling (Rust InputQueue::activity_tx).
	changed chan struct{}
	// pendingInjections records the turns holding response items queued by a
	// notifying injection (Rust TurnInputQueue::has_pending_injection, #55644).
	// Inter-agent mail does not count: it wakes a waiter as mailbox activity
	// instead of steering the turn, and passive context does not wake a waiter
	// at all.
	pendingInjections map[string]bool
}

type SteerEnqueueParams struct {
	ThreadID       string
	TurnID         string
	InputItems     []any
	ClientMetadata map[string]string
}

type SteerDrainParams struct {
	ThreadID string
	TurnID   string
}

type SteerDrainResult struct {
	InputItems     []any
	ClientMetadata map[string]string
}

func NewSteerMailbox() *SteerMailbox {
	return &SteerMailbox{items: map[string][]any{}}
}

func (m *SteerMailbox) Enqueue(params *SteerEnqueueParams) error {
	if m == nil {
		return fmt.Errorf("%w: steer mailbox is nil", ErrInvalidTurnRequest)
	}
	if params == nil || strings.TrimSpace(params.ThreadID) == "" || strings.TrimSpace(params.TurnID) == "" {
		return fmt.Errorf("%w: threadId and turnId are required", ErrInvalidTurnRequest)
	}
	items := compactInputItems(params.InputItems)
	metadata := compactStringMap(params.ClientMetadata)
	if len(items) == 0 && len(metadata) == 0 {
		return nil
	}
	key := steerMailboxKey(params.ThreadID, params.TurnID)
	m.mu.Lock()
	defer m.mu.Unlock()
	// Rust input_queue.rs::extend_pending_input_and_accept_mailbox_delivery_for_turn_state
	// records a pending injection before it publishes the activity, so a waiter
	// that observes the wake already sees the injection (#55644).
	if steerItemsCarryInjection(items) {
		if m.pendingInjections == nil {
			m.pendingInjections = map[string]bool{}
		}
		m.pendingInjections[key] = true
	}
	m.storeLocked(key, items, metadata)
	// Rust input_queue.rs::extend_pending_input_and_accept_mailbox_delivery_for_turn_state:
	// explicit same-turn input (a steered user message) reopens the current turn
	// for mailbox delivery after terminal output closed it.
	if steerItemsHaveUserInput(items) {
		m.setMailboxDeliveryPhaseLocked(key, state.MailboxCurrentTurn)
	}
	return nil
}

// EnqueueIfAcceptingDelivery mirrors Rust
// InputQueue::deliver_mailbox_communication_to_current_turn (#48982): the input
// is stored only while the turn still accepts mailbox delivery. The phase check
// and the store run under the same lock, so a notification that races the turn's
// final answer cannot be queued after delivery closed. It reports whether the
// input was stored; a closed turn keeps the mail out of the queue instead of
// reopening a finalized answer with one more sampling request.
//
// Only inter-agent mail uses this path. Explicit same-turn work (a steered user
// message) goes through Enqueue, which reopens the current turn
// (Rust TurnInputQueue::extend_pending_input_and_accept_mailbox_delivery_for_turn_state).
func (m *SteerMailbox) EnqueueIfAcceptingDelivery(params *SteerEnqueueParams) (bool, error) {
	if m == nil {
		return false, fmt.Errorf("%w: steer mailbox is nil", ErrInvalidTurnRequest)
	}
	if params == nil || strings.TrimSpace(params.ThreadID) == "" || strings.TrimSpace(params.TurnID) == "" {
		return false, fmt.Errorf("%w: threadId and turnId are required", ErrInvalidTurnRequest)
	}
	items := compactInputItems(params.InputItems)
	metadata := compactStringMap(params.ClientMetadata)
	if len(items) == 0 && len(metadata) == 0 {
		return false, nil
	}
	key := steerMailboxKey(params.ThreadID, params.TurnID)
	m.mu.Lock()
	defer m.mu.Unlock()
	if !m.acceptsMailboxDeliveryLocked(key) {
		return false, nil
	}
	m.storeLocked(key, items, metadata)
	return true, nil
}

// BeginTurnFinish closes input admission for the turn while its final batch is
// recorded and returns the function that reopens it (Rust #53452: tasks/mod.rs
// sets ActiveTurn.finishing before pending input is drained and drops the guard
// once the final items are recorded). The release wakes every waiter and is safe
// to call more than once.
func (m *SteerMailbox) BeginTurnFinish(threadID string, turnID string) func() {
	if m == nil || strings.TrimSpace(threadID) == "" || strings.TrimSpace(turnID) == "" {
		return func() {}
	}
	key := steerMailboxKey(threadID, turnID)
	m.mu.Lock()
	if m.finishing == nil {
		m.finishing = map[string]chan struct{}{}
	}
	if m.finishing[key] == nil {
		m.finishing[key] = make(chan struct{})
	}
	m.mu.Unlock()
	return func() { m.releaseTurnFinish(key) }
}

// releaseTurnFinish reopens the turn and wakes waiters. Releasing twice is a
// no-op, so a deferred release and an explicit one can both run.
func (m *SteerMailbox) releaseTurnFinish(key string) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	release := m.finishing[key]
	delete(m.finishing, key)
	if release != nil {
		close(release)
	}
}

// IsTurnFinishing reports whether the turn closed input admission while its final
// batch is recorded (Rust #53452).
func (m *SteerMailbox) IsTurnFinishing(threadID string, turnID string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.finishing[steerMailboxKey(threadID, turnID)] != nil
}

// WaitForTurnFinish blocks until the turn's final batch is recorded (Rust #53452:
// a late client response or notification waits for `finishing.cancelled()` before
// it is appended to history, so it is recorded after the final batch instead of
// ahead of it). It returns immediately when the turn is not finishing.
func (m *SteerMailbox) WaitForTurnFinish(threadID string, turnID string) {
	if m == nil {
		return
	}
	key := steerMailboxKey(threadID, turnID)
	m.mu.Lock()
	release := m.finishing[key]
	m.mu.Unlock()
	if release != nil {
		<-release
	}
}

// storeLocked appends the input under an already held lock and wakes watchers.
func (m *SteerMailbox) storeLocked(key string, items []any, metadata map[string]string) {
	if m.items == nil {
		m.items = map[string][]any{}
	}
	if len(items) > 0 {
		m.items[key] = append(m.items[key], items...)
	}
	if len(metadata) > 0 {
		if m.metadata == nil {
			m.metadata = map[string]map[string]string{}
		}
		m.metadata[key] = metadata
	}
	if m.changed != nil {
		close(m.changed)
	}
	m.changed = make(chan struct{})
}

// SteerMailboxActivity mirrors Rust InputQueueActivity: the kind of queued turn
// input that a waiter observes.
type SteerMailboxActivity int

const (
	// SteerMailboxActivityNone reports no queued input that wakes a waiter.
	SteerMailboxActivityNone SteerMailboxActivity = iota
	// SteerMailboxActivityMailbox reports queued inter-agent mail (Rust
	// TurnInput::InterAgentCommunication).
	SteerMailboxActivityMailbox
	// SteerMailboxActivitySteer reports input that steers the turn: a notifying
	// injection, a user message, or a function-call output.
	SteerMailboxActivitySteer
)

// PendingActivity mirrors Rust TurnInputQueue::pending_activity combined with
// InputQueue::subscribe_activity (#55644): steering input wins over mail, and
// passive context alone reports no activity.
func (m *SteerMailbox) PendingActivity(threadID string, turnID string) SteerMailboxActivity {
	if m == nil {
		return SteerMailboxActivityNone
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pendingActivityLocked(steerMailboxKey(threadID, turnID))
}

func (m *SteerMailbox) pendingActivityLocked(key string) SteerMailboxActivity {
	if m.pendingInjections[key] {
		return SteerMailboxActivitySteer
	}
	items := m.items[key]
	if steerItemsHaveUserInput(items) || steerItemsHaveFunctionCallOutput(items) {
		return SteerMailboxActivitySteer
	}
	if steerItemsHaveAgentMail(items) {
		return SteerMailboxActivityMailbox
	}
	return SteerMailboxActivityNone
}

// WatchActivity mirrors Rust InputQueue::subscribe_activity (#55644): it
// subscribes before the first check, then cancels the signal once the turn has
// queued any activity. A waiting tool (clock.sleep) uses it to end its wait,
// unlike WatchUserInput, which only arms code-mode yielding for user messages.
func (m *SteerMailbox) WatchActivity(threadID string, turnID string, signal *tool.YieldSignal) func() {
	return m.watchActivity(threadID, turnID, signal, func(activity SteerMailboxActivity) bool {
		return activity != SteerMailboxActivityNone
	})
}

// WatchSteer mirrors Rust's Steer outcome of subscribe_activity (#55644): it
// cancels the signal only for steering input. wait_agent uses it so queued mail
// keeps its existing notification path and passive context leaves the wait
// asleep.
func (m *SteerMailbox) WatchSteer(threadID string, turnID string, signal *tool.YieldSignal) func() {
	return m.watchActivity(threadID, turnID, signal, func(activity SteerMailboxActivity) bool {
		return activity == SteerMailboxActivitySteer
	})
}

// watchActivity subscribes before the first check, so an arrival between the
// check and the wait cannot be missed, and releases the watcher on return.
func (m *SteerMailbox) watchActivity(threadID string, turnID string, signal *tool.YieldSignal, matches func(SteerMailboxActivity) bool) func() {
	if m == nil || signal == nil {
		return func() {}
	}
	key := steerMailboxKey(threadID, turnID)
	stopped := make(chan struct{})
	released := make(chan struct{})

	m.mu.Lock()
	if m.changed == nil {
		m.changed = make(chan struct{})
	}
	changed := m.changed
	pending := matches(m.pendingActivityLocked(key))
	m.mu.Unlock()
	if pending {
		signal.Cancel()
		return func() {}
	}

	go func() {
		defer close(released)
		for {
			select {
			case <-changed:
				m.mu.Lock()
				pending := matches(m.pendingActivityLocked(key))
				changed = m.changed
				m.mu.Unlock()
				if pending {
					signal.Cancel()
					return
				}
			case <-stopped:
				return
			}
		}
	}()
	return func() {
		close(stopped)
		<-released
	}
}

// WatchUserInput mirrors Rust InputQueue::watch_user_input (#48135): it
// subscribes before the first check so an arrival between the check and the wait
// cannot be missed, then cancels the signal once a queued user message is
// waiting for the turn. Other turn inputs (agent mail, message-board
// notifications) are not instant-interrupt triggers, matching Rust's
// `TurnInputQueue::has_user_input`. The returned function releases the watcher.
func (m *SteerMailbox) WatchUserInput(threadID string, turnID string, signal *tool.YieldSignal) func() {
	if m == nil || signal == nil {
		return func() {}
	}
	key := steerMailboxKey(threadID, turnID)
	stopped := make(chan struct{})
	released := make(chan struct{})

	m.mu.Lock()
	if m.changed == nil {
		m.changed = make(chan struct{})
	}
	changed := m.changed
	pending := steerItemsHaveUserInput(m.items[key])
	m.mu.Unlock()
	if pending {
		signal.Cancel()
		return func() {}
	}

	go func() {
		defer close(released)
		for {
			select {
			case <-changed:
				m.mu.Lock()
				pending := steerItemsHaveUserInput(m.items[key])
				changed = m.changed
				m.mu.Unlock()
				if pending {
					signal.Cancel()
					return
				}
			case <-stopped:
				return
			}
		}
	}()
	return func() {
		close(stopped)
		<-released
	}
}

// steerItemsHaveAgentMail reports whether the queued items carry inter-agent
// mail (Rust TurnInput::InterAgentCommunication). Go renders both agent mail and
// message-board notices as an `agent_message` input item
// (runtimeAgentCommunicationInputItem / execAgentCommunicationInputItem).
func steerItemsHaveAgentMail(items []any) bool {
	for _, item := range items {
		if steerItemIsAgentMail(item) {
			return true
		}
	}
	return false
}

func steerItemIsAgentMail(item any) bool {
	raw, ok := item.(map[string]any)
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(fmt.Sprint(raw["type"]))) {
	case "agent_message", "agent_message_board_notification":
		return true
	}
	return false
}

// steerItemsCarryInjection reports whether a notifying enqueue carried a
// response-item injection (Rust #55644 extends
// `TurnInput::ResponseItem` through the accepting variant). Go renders
// inter-agent mail as an `agent_message` item, which Rust models as
// `TurnInput::InterAgentCommunication` and therefore does not count.
func steerItemsCarryInjection(items []any) bool {
	for _, item := range items {
		if !steerItemIsAgentMail(item) {
			return true
		}
	}
	return false
}

// steerItemsHaveFunctionCallOutput mirrors Rust pending_activity's
// `TurnInput::FunctionCallOutput` arm: a queued function-call output steers the
// turn just like a user message.
func steerItemsHaveFunctionCallOutput(items []any) bool {
	for _, item := range items {
		raw, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(fmt.Sprint(raw["type"])), "function_call_output") {
			return true
		}
	}
	return false
}

// steerItemsHaveUserInput mirrors Rust TurnInputQueue::has_user_input: only a
// queued user message counts, not the agent mail and notification inputs the
// shared mailbox also carries.
func steerItemsHaveUserInput(items []any) bool {
	for _, item := range items {
		raw, ok := item.(map[string]any)
		if !ok {
			continue
		}
		if strings.EqualFold(strings.TrimSpace(fmt.Sprint(raw["type"])), "message") &&
			strings.EqualFold(strings.TrimSpace(fmt.Sprint(raw["role"])), "user") {
			return true
		}
	}
	return false
}

func (m *SteerMailbox) Drain(params *SteerDrainParams) []any {
	return m.DrainWithMetadata(params).InputItems
}

// HasPending reports whether queued input is still waiting for the turn. Rust's
// post-turn compaction skips while the turn's input queue is non-empty
// (#46541); the steer mailbox is Go's queue for input that arrives mid-turn.
// Rust InputQueue::has_pending_input gates the answer on the turn's mailbox
// delivery phase, so late child mail that was deferred to the next turn does not
// keep the current turn open.
func (m *SteerMailbox) HasPending(threadID string, turnID string) bool {
	if m == nil {
		return false
	}
	key := steerMailboxKey(threadID, turnID)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.phases[key] == state.MailboxNextTurn {
		return false
	}
	if len(m.items[key]) > 0 {
		return true
	}
	return len(m.metadata[key]) > 0
}

// HasPendingMailboxItems mirrors Rust
// InputQueue::has_pending_mailbox_items (#49262) for mail Go holds in the turn
// queue: inter-agent mail (agent mail and message-board notices) that arrived
// since the queue was last drained, which is the same window in which Rust's
// `mailbox_pending_mails` is non-empty (Rust drains it into the request from
// get_pending_input, Go from the loop's top-of-iteration drain).
//
// Pending *user* input does not count. In Rust a user steer is
// TurnInput::UserInput in the turn's pending input (input_queue.rs has_user_input,
// #48135), never mailbox mail, and mailbox preemption is the one path that cuts a
// response's remaining tool calls short. Go's SteerMailbox carries both kinds of
// input in one queue, so the mail-only predicate is what keeps the preemption
// boundary aligned with upstream.
func (m *SteerMailbox) HasPendingMailboxItems(threadID string, turnID string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return steerItemsHaveAgentMail(m.items[steerMailboxKey(threadID, turnID)])
}

// HasPendingUserInput reports whether a queued user message is waiting for the
// turn (Rust TurnInputQueue::has_user_input, #48135).
func (m *SteerMailbox) HasPendingUserInput(threadID string, turnID string) bool {
	if m == nil {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return steerItemsHaveUserInput(m.items[steerMailboxKey(threadID, turnID)])
}

// SetMailboxDeliveryPhase mirrors TurnState::set_mailbox_delivery_phase.
func (m *SteerMailbox) SetMailboxDeliveryPhase(threadID string, turnID string, phase state.MailboxDeliveryPhase) {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setMailboxDeliveryPhaseLocked(steerMailboxKey(threadID, turnID), phase)
}

func (m *SteerMailbox) setMailboxDeliveryPhaseLocked(key string, phase state.MailboxDeliveryPhase) {
	if phase != state.MailboxNextTurn {
		// CurrentTurn is the default, so it is stored as absence.
		delete(m.phases, key)
		return
	}
	if m.phases == nil {
		m.phases = map[string]state.MailboxDeliveryPhase{}
	}
	m.phases[key] = phase
}

// AcceptMailboxDeliveryForCurrentTurn mirrors
// TurnState::accept_mailbox_delivery_for_current_turn: the turn accepted
// explicit same-turn work again, so queued mail may be consumed by this turn.
func (m *SteerMailbox) AcceptMailboxDeliveryForCurrentTurn(threadID string, turnID string) {
	m.SetMailboxDeliveryPhase(threadID, turnID, state.MailboxCurrentTurn)
}

// AcceptsMailboxDeliveryForCurrentTurn mirrors
// TurnState::accepts_mailbox_delivery_for_current_turn.
func (m *SteerMailbox) AcceptsMailboxDeliveryForCurrentTurn(threadID string, turnID string) bool {
	if m == nil {
		return true
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.acceptsMailboxDeliveryLocked(steerMailboxKey(threadID, turnID))
}

// acceptsMailboxDeliveryLocked is AcceptsMailboxDeliveryForCurrentTurn for
// callers that already hold the mailbox lock.
func (m *SteerMailbox) acceptsMailboxDeliveryLocked(key string) bool {
	// Rust #53452 (session/inject.rs::inject_if_running) filters on
	// `turn.finishing.is_none()` before it extends the turn's pending input, so a
	// finishing turn rejects injection even though its phase is still the current
	// turn. The phase check below is the pre-existing terminal-output gate.
	if m.finishing[key] != nil {
		return false
	}
	return m.phases[key] != state.MailboxNextTurn
}

func (m *SteerMailbox) DrainWithMetadata(params *SteerDrainParams) *SteerDrainResult {
	if m == nil || params == nil || strings.TrimSpace(params.ThreadID) == "" || strings.TrimSpace(params.TurnID) == "" {
		return &SteerDrainResult{}
	}
	key := steerMailboxKey(params.ThreadID, params.TurnID)
	m.mu.Lock()
	defer m.mu.Unlock()
	items := append([]any(nil), m.items[key]...)
	metadata := cloneStringMap(m.metadata[key])
	delete(m.items, key)
	delete(m.metadata, key)
	// Rust take_pending_input_for_turn_state replaces the whole TurnInputQueue,
	// so consuming the injection clears its pending bit.
	delete(m.pendingInjections, key)
	return &SteerDrainResult{InputItems: items, ClientMetadata: metadata}
}

func (m *SteerMailbox) Clear(params *SteerDrainParams) {
	if m == nil || params == nil || strings.TrimSpace(params.ThreadID) == "" || strings.TrimSpace(params.TurnID) == "" {
		return
	}
	key := steerMailboxKey(params.ThreadID, params.TurnID)
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.items, key)
	delete(m.metadata, key)
	delete(m.pendingInjections, key)
}

func compactInputItems(items []any) []any {
	out := make([]any, 0, len(items))
	for i := range items {
		if items[i] != nil {
			out = append(out, items[i])
		}
	}
	return out
}

func steerMailboxKey(threadID string, turnID string) string {
	return strings.TrimSpace(threadID) + "\x00" + strings.TrimSpace(turnID)
}
