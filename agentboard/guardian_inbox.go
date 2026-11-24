package agentboard

import (
	"context"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"sync"

	"codex_go/utils"
)

// GuardianQueuedEvidence is one committed board post queued for an actor: the
// source ordinal it was routed from and the post that stores it.
type GuardianQueuedEvidence struct {
	Ordinal   uint64
	MessageID string
}

// GuardianLiveDelivery holds the subscriber inboxes for originals routed by one
// root thread's housekeeper. A persisted subscription can receive a post before
// its actor's reviewer starts, and each reviewer conversation owns its own
// admission cursor, so a replacement conversation replays what is still queued
// (Rust guardian-v3 LiveDelivery).
type GuardianLiveDelivery struct {
	root string

	mu     sync.Mutex
	actors map[string]*GuardianActorInbox
	closed bool
}

// NewGuardianLiveDelivery creates the inbox set for one root thread.
func NewGuardianLiveDelivery(root string) *GuardianLiveDelivery {
	return &GuardianLiveDelivery{root: root, actors: map[string]*GuardianActorInbox{}}
}

// Root is the board identity whose routed originals these inboxes carry.
func (d *GuardianLiveDelivery) Root() string {
	if d == nil {
		return ""
	}
	return d.root
}

// Inbox returns the actor's inbox, creating it on first use so evidence routed
// before the reviewer exists is retained rather than dropped.
func (d *GuardianLiveDelivery) Inbox(actor string) *GuardianActorInbox {
	if d == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.actors == nil {
		d.actors = map[string]*GuardianActorInbox{}
	}
	inbox, ok := d.actors[actor]
	if !ok {
		inbox = &GuardianActorInbox{root: d.root, actor: actor, wake: make(chan struct{})}
		d.actors[actor] = inbox
	}
	return inbox
}

// CloseInbox retires an actor's inbox and forgets it, so a reviewer that
// reloads starts with an empty inbox while a replacement conversation for a
// still-loaded agent reuses the same inbox through Inbox (Rust #63357).
func (d *GuardianLiveDelivery) CloseInbox(actor string) {
	if d == nil {
		return
	}
	d.mu.Lock()
	inbox := d.actors[actor]
	delete(d.actors, actor)
	d.mu.Unlock()
	inbox.Close()
}

// GuardianLiveDeliveries keeps one live delivery per root thread so a root
// reload reuses the inboxes of its surviving child reviewers instead of
// dropping their subscriptions and queued evidence (Rust #63361). Retiring a
// root closes its inboxes, so a root reloaded after shutdown starts empty.
type GuardianLiveDeliveries struct {
	mu     sync.Mutex
	byRoot map[string]*GuardianLiveDelivery
}

// NewGuardianLiveDeliveries creates an empty registry.
func NewGuardianLiveDeliveries() *GuardianLiveDeliveries {
	return &GuardianLiveDeliveries{byRoot: map[string]*GuardianLiveDelivery{}}
}

// ForRoot returns the root's live delivery, creating it on first use and
// reusing the same stream across reloads.
func (r *GuardianLiveDeliveries) ForRoot(root string) *GuardianLiveDelivery {
	if r == nil {
		return nil
	}
	root = strings.TrimSpace(root)
	if root == "" {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.byRoot == nil {
		r.byRoot = map[string]*GuardianLiveDelivery{}
	}
	delivery, ok := r.byRoot[root]
	if !ok {
		delivery = NewGuardianLiveDelivery(root)
		r.byRoot[root] = delivery
	}
	return delivery
}

// Existing returns the root's live delivery without creating one.
func (r *GuardianLiveDeliveries) Existing(root string) (*GuardianLiveDelivery, bool) {
	if r == nil {
		return nil, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	delivery, ok := r.byRoot[strings.TrimSpace(root)]
	return delivery, ok
}

// Retire closes every inbox of the root and forgets the delivery stream.
func (r *GuardianLiveDeliveries) Retire(root string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	delivery := r.byRoot[strings.TrimSpace(root)]
	delete(r.byRoot, strings.TrimSpace(root))
	r.mu.Unlock()
	if delivery != nil {
		delivery.CloseAll()
	}
}

// RetireAll closes every root's inboxes, e.g. when the host shuts down.
func (r *GuardianLiveDeliveries) RetireAll() {
	if r == nil {
		return
	}
	r.mu.Lock()
	deliveries := r.byRoot
	r.byRoot = map[string]*GuardianLiveDelivery{}
	r.mu.Unlock()
	for _, delivery := range deliveries {
		delivery.CloseAll()
	}
}

// Closed reports whether this delivery stream was retired.
func (d *GuardianLiveDelivery) Closed() bool {
	if d == nil {
		return true
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.closed
}

// CloseAll closes every actor inbox owned by this delivery and prevents
// further publishes, so a reloaded root uses a fresh stream (Rust #63361).
func (d *GuardianLiveDelivery) CloseAll() {
	if d == nil {
		return
	}
	d.mu.Lock()
	inboxes := d.actors
	d.actors = map[string]*GuardianActorInbox{}
	d.closed = true
	d.mu.Unlock()
	for _, inbox := range inboxes {
		inbox.Close()
	}
}

// Publish queues one committed original for every recipient, one post per
// actor even when several subscribed channels carried the same original. The
// housekeeper is the single root writer, so an ordinal that is not newer than
// what an inbox already holds means routing went backwards.
func (d *GuardianLiveDelivery) Publish(id GuardianEvidenceID, recipients map[string]string) error {
	if d == nil {
		return errors.New("Guardian delivery is unavailable")
	}
	if d.Closed() {
		return errors.New("Guardian delivery is closed")
	}
	actors := make([]string, 0, len(recipients))
	for actor := range recipients {
		actors = append(actors, actor)
	}
	sort.Strings(actors)
	for _, actor := range actors {
		inbox := d.Inbox(actor)
		if err := inbox.publish(id, recipients[actor]); err != nil {
			return err
		}
	}
	return nil
}

// GuardianActorInbox queues committed evidence for one subscribed actor. It
// retains board references only: the reviewer conversation keeps the admission
// cursor (Rust guardian-v3 ActorInbox).
type GuardianActorInbox struct {
	root  string
	actor string

	mu     sync.Mutex
	posts  []GuardianQueuedEvidence
	wake   chan struct{}
	closed bool
}

// guardianClosedWake is returned by Subscribe on a closed inbox so a waiting
// reviewer wakes immediately and can notice the closure.
var guardianClosedWake = func() chan struct{} {
	closed := make(chan struct{})
	close(closed)
	return closed
}()

// Root is the board identity this inbox was opened with.
func (i *GuardianActorInbox) Root() string {
	if i == nil {
		return ""
	}
	return i.root
}

// Actor is the agent whose evidence this inbox queues.
func (i *GuardianActorInbox) Actor() string {
	if i == nil {
		return ""
	}
	return i.actor
}

// Closed reports whether the inbox was retired. A closed inbox never admits
// another original (Rust guardian-v3 ActorInbox::close, #63357).
func (i *GuardianActorInbox) Closed() bool {
	if i == nil {
		return true
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return i.closed
}

// Close retires the inbox: queued evidence is dropped so it cannot reach a
// later reviewer conversation for the same agent, and pending wakeups fire.
func (i *GuardianActorInbox) Close() {
	if i == nil {
		return
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return
	}
	i.closed = true
	i.posts = nil
	if i.wake != nil {
		close(i.wake)
		i.wake = nil
	}
}

// Len is the number of queued originals, including ones a conversation already
// admitted: the cursor, not the queue head, decides what is still owed.
func (i *GuardianActorInbox) Len() int {
	if i == nil {
		return 0
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return len(i.posts)
}

// Subscribe returns a channel that closes on the next arrival, so an idle
// reviewer can be woken without holding the housekeeper's write path.
func (i *GuardianActorInbox) Subscribe() <-chan struct{} {
	if i == nil {
		return nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return guardianClosedWake
	}
	if i.wake == nil {
		i.wake = make(chan struct{})
	}
	return i.wake
}

// QueuedPosts copies the queue for inspection.
func (i *GuardianActorInbox) QueuedPosts() []GuardianQueuedEvidence {
	if i == nil {
		return nil
	}
	i.mu.Lock()
	defer i.mu.Unlock()
	return append([]GuardianQueuedEvidence(nil), i.posts...)
}

func (i *GuardianActorInbox) publish(id GuardianEvidenceID, messageID string) error {
	i.mu.Lock()
	defer i.mu.Unlock()
	if i.closed {
		return errors.New("Guardian inbox is closed")
	}
	if count := len(i.posts); count != 0 && i.posts[count-1].Ordinal >= id.Ordinal {
		return errors.New("Guardian inbox received evidence out of source order")
	}
	i.posts = append(i.posts, GuardianQueuedEvidence{Ordinal: id.Ordinal, MessageID: messageID})
	if i.wake == nil {
		i.wake = make(chan struct{})
	}
	close(i.wake)
	i.wake = make(chan struct{})
	return nil
}

// Deliver admits queued originals in source order up to through, the queue
// length the caller observed. Bounded pages keep a slow admission from holding
// anything but the reviewer conversation, and new arrivals wait for the next
// call instead of changing this one's target. The callback must record the item
// in the retained conversation before reporting success, because only then is
// the cursor advanced (Rust ActorInbox::deliver).
func (i *GuardianActorInbox) Deliver(ctx context.Context, actor string, board AgentMessageBoard, delivered *int, through int, policy utils.TruncationPolicy, admit func(context.Context, GuardianEvidenceItem) error) error {
	if i == nil {
		return errors.New("Guardian inbox is unavailable")
	}
	if board == nil || board.Identity() != i.root || board.StorageNamespace() != GuardianBoardStorageNamespace {
		return errors.New("Guardian inbox opened with the wrong board or caller")
	}
	if actor != i.actor {
		return errors.New("Guardian inbox opened with the wrong board or caller")
	}
	if delivered == nil {
		return errors.New("Guardian inbox delivery requires a cursor")
	}
	if i.Closed() {
		return errors.New("Guardian inbox is closed")
	}
	if admit == nil {
		return errors.New("evidence admission callback is required")
	}
	length := i.Len()
	if *delivered < 0 || *delivered > length || through < 0 || through > length {
		return errors.New("invalid Guardian inbox cursor")
	}
	for *delivered < through {
		start := *delivered
		end := start + DefaultPageLimit
		if end > through {
			end = through
		}
		positions := i.rangePosts(start, end)
		ids := make([]string, 0, len(positions))
		for _, position := range positions {
			ids = append(ids, position.MessageID)
		}
		texts, err := board.ReadPostTextsByID(ctx, actor, ids)
		if err != nil {
			return err
		}
		if len(texts) != len(positions) {
			return errors.New("board returned an incomplete evidence page")
		}
		for *delivered < end {
			pending := make([]guardianPendingEvidence, 0, end-*delivered)
			for index := *delivered - start; index < len(positions); index++ {
				pending = append(pending, guardianPendingEvidence{Ordinal: positions[index].Ordinal, Text: texts[index]})
			}
			item, count, err := guardianEvidenceBatch(pending, policy)
			if err != nil {
				return err
			}
			if err := admit(ctx, item); err != nil {
				return err
			}
			*delivered += count
		}
	}
	return nil
}

// DeliverPending drains every original queued when the reviewer became idle
// (Rust #62136: drain each actor's inbox when its reviewer becomes idle and
// before reserving the conversation for a verdict). The queue length is
// observed once, so evidence that arrives while the callback runs waits for
// the next drain, and the caller's cursor advances only after each admitted
// batch was recorded.
func (i *GuardianActorInbox) DeliverPending(ctx context.Context, actor string, board AgentMessageBoard, delivered *int, policy utils.TruncationPolicy, admit func(context.Context, GuardianEvidenceItem) error) error {
	if i == nil {
		return errors.New("Guardian inbox is unavailable")
	}
	return i.Deliver(ctx, actor, board, delivered, i.Len(), policy, admit)
}

func (i *GuardianActorInbox) rangePosts(start int, end int) []GuardianQueuedEvidence {
	i.mu.Lock()
	defer i.mu.Unlock()
	if start < 0 || end > len(i.posts) || start > end {
		return nil
	}
	return append([]GuardianQueuedEvidence(nil), i.posts[start:end]...)
}

// guardianPendingEvidence is one admitted board body with its source ordinal.
type guardianPendingEvidence struct {
	Ordinal uint64
	Text    string
}

// guardianEvidenceBatch encodes one whole-original batch in source order within
// the reviewer's selected model budget. It refuses a single original larger
// than the budget and a post whose ordinal is not the one it was queued for
// (Rust delivery::evidence_batch).
func guardianEvidenceBatch(pending []guardianPendingEvidence, policy utils.TruncationPolicy) (GuardianEvidenceItem, int, error) {
	input := guardianDeliveryInput{Sources: []guardianRoutedOriginal{}}
	empty, err := json.Marshal(input)
	if err != nil {
		return GuardianEvidenceItem{}, 0, err
	}
	budget := policy.ByteBudget()
	encoded := len(empty)
	for _, entry := range pending {
		additional := len(entry.Text)
		if len(input.Sources) != 0 {
			additional++
		}
		if additional > budget-encoded {
			if len(input.Sources) == 0 {
				return GuardianEvidenceItem{}, 0, errors.New("original evidence exceeds the model's tool-output budget")
			}
			break
		}
		source, err := parseGuardianRoutedOriginal(entry.Text)
		if err != nil {
			return GuardianEvidenceItem{}, 0, err
		}
		if source.ID.Ordinal != entry.Ordinal {
			return GuardianEvidenceItem{}, 0, errors.New("selected board post has a different source ordinal")
		}
		input.Sources = append(input.Sources, source)
		encoded += additional
	}
	if len(input.Sources) == 0 {
		return GuardianEvidenceItem{}, 0, errors.New("empty evidence batch")
	}
	payload, err := json.Marshal(input)
	if err != nil {
		return GuardianEvidenceItem{}, 0, err
	}
	return GuardianEvidenceItem{
		Name:      GuardianEvidenceContextName,
		Namespace: GuardianEvidenceNamespace,
		Text:      utils.TruncateText(string(payload), policy),
	}, len(input.Sources), nil
}
