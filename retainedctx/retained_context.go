// Package retainedctx ports Rust's `codex-history` retained-context model: the
// bounded, host-owned evidence families (verified answers, retained user and
// assistant messages, sender deliveries) that live outside the model's
// compaction contract, together with their storage limits, checkpoint restore,
// rollback, reconciliation and legacy-recovery rules.
//
// Go's `history` package is the counterpart of Rust's `message-history` crate
// (the global history.jsonl), so this model lives in its own package.
package retainedctx

import (
	"encoding/json"
	"reflect"
	"sort"
)

const (
	// MaxFamilyRecords is Rust's `MAX_FAMILY_RECORDS`.
	MaxFamilyRecords = 8
	// MaxRecordBytes is Rust's `MAX_RECORD_BYTES`.
	MaxRecordBytes = 16_384
	// MaxFamilyBytes is Rust's `MAX_FAMILY_BYTES`.
	MaxFamilyBytes = 65_536
)

// VerifiedQuestionAnswer is an original assistant question and host-verified
// user reply, never an inferred permission.
type VerifiedQuestionAnswer struct {
	Question string `json:"question"`
	Answer   string `json:"answer"`
}

// VerifiedAnswer is one accepted request_user_input response. Identity is local
// to the owning thread. An omitted payload records incomplete evidence rather
// than keeping a partial grant.
type VerifiedAnswer struct {
	TurnID    string                   `json:"turn_id"`
	CallID    string                   `json:"call_id"`
	Questions []VerifiedQuestionAnswer `json:"questions"`
}

// RetainedUserMessage is original conversational text retained outside model
// summarization for review. The owning family supplies the source role;
// assistant text never establishes authorization.
type RetainedUserMessage struct {
	TurnID    string          `json:"turn_id"`
	MessageID *string         `json:"message_id,omitempty"`
	Text      string          `json:"text"`
	Complete  bool            `json:"complete"`
	Origin    UserInputOrigin `json:"origin,omitempty"`
	Phase     *string         `json:"phase,omitempty"`
}

// MarshalJSON omits the ordinary-user origin, mirroring Rust's
// `skip_serializing_if = "UserInputOrigin::is_user"`.
func (m RetainedUserMessage) MarshalJSON() ([]byte, error) {
	type wire struct {
		TurnID    string          `json:"turn_id"`
		MessageID *string         `json:"message_id,omitempty"`
		Text      string          `json:"text"`
		Complete  bool            `json:"complete"`
		Origin    UserInputOrigin `json:"origin,omitempty"`
		Phase     *string         `json:"phase,omitempty"`
	}
	out := wire{
		TurnID:    m.TurnID,
		MessageID: m.MessageID,
		Text:      m.Text,
		Complete:  m.Complete,
		Phase:     m.Phase,
	}
	if !m.Origin.IsUser() {
		out.Origin = m.Origin
	}
	return marshalNoEscape(out)
}

// UnmarshalJSON treats a missing origin as ordinary input, mirroring Rust's
// defaulted `origin` field.
func (m *RetainedUserMessage) UnmarshalJSON(data []byte) error {
	type wire struct {
		TurnID    string          `json:"turn_id"`
		MessageID *string         `json:"message_id,omitempty"`
		Text      string          `json:"text"`
		Complete  bool            `json:"complete"`
		Origin    UserInputOrigin `json:"origin"`
		Phase     *string         `json:"phase,omitempty"`
	}
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	m.TurnID = decoded.TurnID
	m.MessageID = decoded.MessageID
	m.Text = decoded.Text
	m.Complete = decoded.Complete
	m.Origin = UserInputOriginUser
	if decoded.Origin != "" {
		m.Origin = decoded.Origin
	}
	m.Phase = decoded.Phase
	return nil
}

// RetainedInputSource mirrors Rust's `RetainedInputSource`: local facts use
// their acceptance counter, copied parent instructions use prefix order.
type RetainedInputSource struct {
	Inherited bool
	Order     *uint64
}

// LocalInputSource records a locally accepted input with an optional accepted
// order.
func LocalInputSource(order *uint64) RetainedInputSource {
	return RetainedInputSource{Order: order}
}

// InheritedInputSource records a copied parent instruction.
func InheritedInputSource() RetainedInputSource {
	return RetainedInputSource{Inherited: true}
}

// AcceptanceOrder returns the local acceptance counter, which parent counters
// never establish.
func (s RetainedInputSource) AcceptanceOrder() (uint64, bool) {
	if s.Inherited || s.Order == nil {
		return 0, false
	}
	return *s.Order, true
}

// HarnessMetadata is the retained model's view of Rust's `CodexHarnessMetadata`.
// Go persists harness metadata as an untyped map, so this type carries only the
// members the retained-context model consumes.
type HarnessMetadata struct {
	UserInputOrder       *uint64             `json:"user_input_order,omitempty"`
	InheritedUserMessage bool                `json:"inherited_user_message,omitempty"`
	SenderUserMessages   *SenderUserMessages `json:"sender_user_messages,omitempty"`
	// RetainedSource is the host-observed version captured when the item was
	// recorded (Rust `CodexHarnessMetadata::retained_source`): it carries the
	// delivery proof a replay restores instead of minting a new revision.
	RetainedSource *RetainedSource `json:"retained_source,omitempty"`
}

// RetainedInputSourceFromMetadata mirrors
// `impl From<Option<&CodexHarnessMetadata>> for RetainedInputSource`.
func RetainedInputSourceFromMetadata(metadata *HarnessMetadata) RetainedInputSource {
	if metadata != nil && metadata.InheritedUserMessage {
		return InheritedInputSource()
	}
	var order *uint64
	if metadata != nil {
		order = metadata.UserInputOrder
	}
	return LocalInputSource(order)
}

// RetainedContextOrder is inherited prefix order or the owning thread's local
// acceptance order. The counters have separate scopes and must not be compared
// without their origin.
type RetainedContextOrder struct {
	Inherited bool
	Order     uint64
}

// Less orders inherited prefixes before local acceptance order.
func (o RetainedContextOrder) Less(other RetainedContextOrder) bool {
	if o.Inherited != other.Inherited {
		return o.Inherited
	}
	return o.Order < other.Order
}

// RetainedContextEntry is a borrowed view of host evidence across the retained
// families. Exactly one field is set.
type RetainedContextEntry struct {
	UserMessage      *RetainedUserMessage
	AssistantMessage *RetainedUserMessage
	VerifiedAnswer   *VerifiedAnswer
}

// OrderedEntry pairs retained evidence with its persisted order.
type OrderedEntry struct {
	Order RetainedContextOrder
	Entry RetainedContextEntry
}

// RetainedContextEvent is a sparse, model-invisible update. Only the host may
// produce these records.
type RetainedContextEvent struct {
	Answer VerifiedAnswer
	// AcceptanceOrder is absent in legacy events, which retain their recorded
	// ordering.
	AcceptanceOrder *uint64
}

// MarshalJSON emits Rust's internally tagged `verified_answer` payload.
func (e RetainedContextEvent) MarshalJSON() ([]byte, error) {
	type wire struct {
		Type            string                   `json:"type"`
		TurnID          string                   `json:"turn_id"`
		CallID          string                   `json:"call_id"`
		Questions       []VerifiedQuestionAnswer `json:"questions"`
		AcceptanceOrder *uint64                  `json:"acceptance_order,omitempty"`
	}
	questions := e.Answer.Questions
	if questions == nil {
		questions = []VerifiedQuestionAnswer{}
	}
	return marshalNoEscape(wire{
		Type:            "verified_answer",
		TurnID:          e.Answer.TurnID,
		CallID:          e.Answer.CallID,
		Questions:       questions,
		AcceptanceOrder: e.AcceptanceOrder,
	})
}

// UnmarshalJSON requires an event of the known `verified_answer` variant.
func (e *RetainedContextEvent) UnmarshalJSON(data []byte) error {
	type wire struct {
		Type            string                   `json:"type"`
		TurnID          string                   `json:"turn_id"`
		CallID          string                   `json:"call_id"`
		Questions       []VerifiedQuestionAnswer `json:"questions"`
		AcceptanceOrder *uint64                  `json:"acceptance_order"`
	}
	var decoded wire
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	e.Answer = VerifiedAnswer{TurnID: decoded.TurnID, CallID: decoded.CallID, Questions: decoded.Questions}
	if e.Answer.Questions == nil {
		e.Answer.Questions = []VerifiedQuestionAnswer{}
	}
	e.AcceptanceOrder = decoded.AcceptanceOrder
	return nil
}

// Bound bounds a persisted event before it enters the rollout or the live
// snapshot.
func (e *RetainedContextEvent) Bound() {
	if e == nil {
		return
	}
	if recordBytes(e.Answer) <= MaxRecordBytes {
		return
	}
	e.Answer.Questions = nil
	// IDs are correlation metadata, not model-visible authorization text.
	e.Answer.TurnID = truncateToBoundary(e.Answer.TurnID, 1_024)
	e.Answer.CallID = truncateToBoundary(e.Answer.CallID, 1_024)
}

// bound bounds one retained record by clearing its text when the serialized
// record exceeds the per-record budget.
func (m *RetainedUserMessage) bound() {
	if recordBytes(*m) <= MaxRecordBytes {
		return
	}
	m.Text = ""
	m.Complete = false
	m.TurnID = truncateToBoundary(m.TurnID, 1_024)
	if m.MessageID != nil {
		truncated := truncateToBoundary(*m.MessageID, 1_024)
		m.MessageID = &truncated
	}
}

func recordBytes(value any) int {
	encoded, err := marshalNoEscape(value)
	if err != nil {
		return MaxRecordBytes + 1
	}
	return len(encoded)
}

// orderedUserMessage is one retained user or assistant record with its order.
type orderedUserMessage struct {
	Value     RetainedUserMessage
	Revision  *ResponseItemID
	Inherited bool
	Order     uint64
}

func (e *orderedUserMessage) key() RetainedContextOrder {
	if e.Inherited {
		return RetainedContextOrder{Inherited: true, Order: e.Order}
	}
	return RetainedContextOrder{Order: e.Order}
}

// MarshalJSON flattens the record, mirroring serde's `#[serde(flatten)]`.
func (e orderedUserMessage) MarshalJSON() ([]byte, error) {
	return marshalOrdered(e.Value, e.Revision, e.Inherited, e.Order)
}

// UnmarshalJSON splits the flattened record back into its order and value.
func (e *orderedUserMessage) UnmarshalJSON(data []byte) error {
	revision, inherited, order, err := unmarshalOrdered(data, &e.Value)
	if err != nil {
		return err
	}
	e.Revision = revision
	e.Inherited = inherited
	e.Order = order
	return nil
}

// orderedVerifiedAnswer is one retained verified answer with its order.
type orderedVerifiedAnswer struct {
	Value     VerifiedAnswer
	Revision  *ResponseItemID
	Inherited bool
	Order     uint64
}

func (e *orderedVerifiedAnswer) key() RetainedContextOrder {
	if e.Inherited {
		return RetainedContextOrder{Inherited: true, Order: e.Order}
	}
	return RetainedContextOrder{Order: e.Order}
}

// MarshalJSON flattens the answer, mirroring serde's `#[serde(flatten)]`.
func (e orderedVerifiedAnswer) MarshalJSON() ([]byte, error) {
	return marshalOrdered(e.Value, e.Revision, e.Inherited, e.Order)
}

// UnmarshalJSON splits the flattened answer back into its order and value.
func (e *orderedVerifiedAnswer) UnmarshalJSON(data []byte) error {
	revision, inherited, order, err := unmarshalOrdered(data, &e.Value)
	if err != nil {
		return err
	}
	e.Revision = revision
	e.Inherited = inherited
	e.Order = order
	return nil
}

// orderedSenderUserMessages is one retained sender delivery with its order.
type orderedSenderUserMessages struct {
	Value     SenderUserMessages
	Revision  *ResponseItemID
	Inherited bool
	Order     uint64
}

func (e *orderedSenderUserMessages) key() RetainedContextOrder {
	if e.Inherited {
		return RetainedContextOrder{Inherited: true, Order: e.Order}
	}
	return RetainedContextOrder{Order: e.Order}
}

// MarshalJSON flattens the delivery, mirroring serde's `#[serde(flatten)]`.
func (e orderedSenderUserMessages) MarshalJSON() ([]byte, error) {
	return marshalOrdered(e.Value, e.Revision, e.Inherited, e.Order)
}

// UnmarshalJSON splits the flattened delivery back into its order and value.
func (e *orderedSenderUserMessages) UnmarshalJSON(data []byte) error {
	revision, inherited, order, err := unmarshalOrdered(data, &e.Value)
	if err != nil {
		return err
	}
	e.Revision = revision
	e.Inherited = inherited
	e.Order = order
	return nil
}

// RetainedContext is a bounded snapshot of retained families, persisted with the
// parent compaction checkpoint. Facts live until their instruction boundary is
// rolled back; compaction does not expire them. The families are unexported, as
// in Rust, so only the retained model's own admission rules can mutate them.
type RetainedContext struct {
	verifiedAnswers             []orderedVerifiedAnswer
	verifiedAnswersIncomplete   bool
	userMessages                []orderedUserMessage
	userMessagesIncomplete      bool
	assistantMessages           []orderedUserMessage
	assistantMessagesIncomplete bool
	nextOrder                   uint64
	senderDeliveries            []orderedSenderUserMessages
}

// MarshalJSON keeps empty families as `[]` and always emits `next_order`.
func (c RetainedContext) MarshalJSON() ([]byte, error) {
	answers, err := marshalList(c.verifiedAnswers)
	if err != nil {
		return nil, err
	}
	users, err := marshalList(c.userMessages)
	if err != nil {
		return nil, err
	}
	assistants, err := marshalList(c.assistantMessages)
	if err != nil {
		return nil, err
	}
	fields := map[string]any{
		"verified_answers":              answers,
		"incomplete":                    c.verifiedAnswersIncomplete,
		"user_messages":                 users,
		"user_messages_incomplete":      c.userMessagesIncomplete,
		"assistant_messages":            assistants,
		"assistant_messages_incomplete": c.assistantMessagesIncomplete,
		"next_order":                    c.nextOrder,
	}
	if len(c.senderDeliveries) > 0 {
		deliveries, err := marshalList(c.senderDeliveries)
		if err != nil {
			return nil, err
		}
		fields["sender_deliveries"] = deliveries
	}
	return marshalNoEscape(fields)
}

// UnmarshalJSON mirrors Rust's field defaults, including the legacy rule that a
// checkpoint without retained user restrictions is incomplete.
func (c *RetainedContext) UnmarshalJSON(data []byte) error {
	var wire struct {
		VerifiedAnswers             []orderedVerifiedAnswer     `json:"verified_answers"`
		VerifiedAnswersIncomplete   bool                        `json:"incomplete"`
		UserMessages                []orderedUserMessage        `json:"user_messages"`
		UserMessagesIncomplete      *bool                       `json:"user_messages_incomplete"`
		AssistantMessages           []orderedUserMessage        `json:"assistant_messages"`
		AssistantMessagesIncomplete bool                        `json:"assistant_messages_incomplete"`
		NextOrder                   uint64                      `json:"next_order"`
		SenderDeliveries            []orderedSenderUserMessages `json:"sender_deliveries"`
	}
	if err := json.Unmarshal(data, &wire); err != nil {
		return err
	}
	c.verifiedAnswers = wire.VerifiedAnswers
	c.verifiedAnswersIncomplete = wire.VerifiedAnswersIncomplete
	c.userMessages = wire.UserMessages
	c.userMessagesIncomplete = true
	if wire.UserMessagesIncomplete != nil {
		c.userMessagesIncomplete = *wire.UserMessagesIncomplete
	}
	c.assistantMessages = wire.AssistantMessages
	c.assistantMessagesIncomplete = wire.AssistantMessagesIncomplete
	c.nextOrder = wire.NextOrder
	c.senderDeliveries = wire.SenderDeliveries
	return nil
}

// Clone returns a deep copy of the retained snapshot.
func (c *RetainedContext) Clone() *RetainedContext {
	if c == nil {
		return nil
	}
	cloned := *c
	cloned.verifiedAnswers = append([]orderedVerifiedAnswer(nil), c.verifiedAnswers...)
	for i := range cloned.verifiedAnswers {
		cloned.verifiedAnswers[i].Value.Questions = append(
			[]VerifiedQuestionAnswer(nil), c.verifiedAnswers[i].Value.Questions...)
		if revision := cloned.verifiedAnswers[i].Revision; revision != nil {
			value := *revision
			cloned.verifiedAnswers[i].Revision = &value
		}
	}
	cloned.userMessages = cloneOrderedUserMessages(c.userMessages)
	cloned.assistantMessages = cloneOrderedUserMessages(c.assistantMessages)
	cloned.senderDeliveries = append([]orderedSenderUserMessages(nil), c.senderDeliveries...)
	for i := range cloned.senderDeliveries {
		if revision := cloned.senderDeliveries[i].Revision; revision != nil {
			value := *revision
			cloned.senderDeliveries[i].Revision = &value
		}
	}
	return &cloned
}

func cloneOrderedUserMessages(items []orderedUserMessage) []orderedUserMessage {
	cloned := append([]orderedUserMessage(nil), items...)
	for i := range cloned {
		if cloned[i].Value.MessageID != nil {
			value := *cloned[i].Value.MessageID
			cloned[i].Value.MessageID = &value
		}
		if cloned[i].Value.Phase != nil {
			value := *cloned[i].Value.Phase
			cloned[i].Value.Phase = &value
		}
		if cloned[i].Revision != nil {
			value := *cloned[i].Revision
			cloned[i].Revision = &value
		}
	}
	return cloned
}

func boundFamily[T any](items *[]T, key func(*T) RetainedContextOrder, incomplete *bool) {
	// Queued instructions can be recorded after later-accepted answers. Evict by
	// acceptance order, not by the order in which persistence happened to finish.
	sort.SliceStable(*items, func(i, j int) bool {
		return key(&(*items)[i]).Less(key(&(*items)[j]))
	})
	for len(*items) > MaxFamilyRecords || familyBytes(*items) > MaxFamilyBytes {
		*items = (*items)[1:]
		*incomplete = true
	}
}

func familyBytes[T any](items []T) int {
	encoded, err := marshalNoEscape(items)
	if err != nil {
		return MaxFamilyBytes + 1
	}
	return len(encoded)
}

// HasOmittedAssistantMessages reports observed storage loss or a retained
// assistant message that cannot be delivered whole. False does not establish
// complete capture coverage for legacy history.
func (c *RetainedContext) HasOmittedAssistantMessages() bool {
	if c.assistantMessagesIncomplete {
		return true
	}
	for i := range c.assistantMessages {
		if !c.assistantMessages[i].Value.Complete {
			return true
		}
	}
	return false
}

func (c *RetainedContext) nextInheritedOrder() uint64 {
	var next uint64
	for i := range c.userMessages {
		entry := &c.userMessages[i]
		if entry.Inherited && entry.Order+1 > next {
			next = entry.Order + 1
		}
	}
	for i := range c.assistantMessages {
		entry := &c.assistantMessages[i]
		if entry.Inherited && entry.Order+1 > next {
			next = entry.Order + 1
		}
	}
	return next
}

// SenderUserMessages returns the context for the latest delivery still present
// in this task's retained history.
func (c *RetainedContext) SenderUserMessages() *SenderUserMessages {
	if len(c.senderDeliveries) == 0 {
		return nil
	}
	return &c.senderDeliveries[len(c.senderDeliveries)-1].Value
}

// RecordSenderUserMessages retains host metadata with the delivery's acceptance
// order, including during replay.
func (c *RetainedContext) RecordSenderUserMessages(metadata *HarnessMetadata) bool {
	if metadata == nil || metadata.SenderUserMessages == nil {
		return false
	}
	messages := metadata.SenderUserMessages
	for i := range c.senderDeliveries {
		if c.senderDeliveries[i].Value.ReceiverMessageID == messages.ReceiverMessageID {
			return false
		}
	}
	bound := cloneSenderUserMessages(messages)
	bound.Bound()
	order := c.recordOrder(metadata.UserInputOrder)
	c.senderDeliveries = append(c.senderDeliveries, orderedSenderUserMessages{
		Value: *bound,
		Order: order,
	})
	var throwaway bool
	boundFamily(&c.senderDeliveries, func(entry *orderedSenderUserMessages) RetainedContextOrder {
		return entry.key()
	}, &throwaway)
	return true
}

// ReserveOrder reserves order without retaining pending input that hooks may
// reject or cancel.
func (c *RetainedContext) ReserveOrder() uint64 {
	order := c.nextOrder
	c.nextOrder = saturatingAdd(c.nextOrder, 1)
	return order
}

func (c *RetainedContext) recordOrder(acceptanceOrder *uint64) uint64 {
	order := c.nextOrder
	if acceptanceOrder != nil {
		order = *acceptanceOrder
	}
	if next := saturatingAdd(order, 1); next > c.nextOrder {
		c.nextOrder = next
	}
	return order
}

// VerifiedAnswers returns retained verified answers in their stored order.
func (c *RetainedContext) VerifiedAnswers() []*VerifiedAnswer {
	out := make([]*VerifiedAnswer, 0, len(c.verifiedAnswers))
	for i := range c.verifiedAnswers {
		out = append(out, &c.verifiedAnswers[i].Value)
	}
	return out
}

// VerifiedAnswersComplete reports whether every retained answer still carries
// its full question set.
func (c *RetainedContext) VerifiedAnswersComplete() bool {
	if c.verifiedAnswersIncomplete {
		return false
	}
	for i := range c.verifiedAnswers {
		if len(c.verifiedAnswers[i].Value.Questions) == 0 {
			return false
		}
	}
	return true
}

// UserMessagesComplete reports whether retained user instructions are complete.
func (c *RetainedContext) UserMessagesComplete() bool {
	if c.HasMissingUserMessages() {
		return false
	}
	for i := range c.userMessages {
		if !c.userMessages[i].Value.Complete {
			return false
		}
	}
	return true
}

// HasMissingUserMessages reports whether instruction records were lost or
// omitted by legacy capture. Bounded excerpts keep their source record and do
// not set this marker.
func (c *RetainedContext) HasMissingUserMessages() bool {
	return c.userMessagesIncomplete
}

// MarkUserMessagesIncomplete records that a skipped instruction leaves a gap
// later checkpoints must preserve.
func (c *RetainedContext) MarkUserMessagesIncomplete() {
	c.userMessagesIncomplete = true
}

// HasInheritedUserMessages reports whether a root checkpoint already contains
// its adopted instruction prefix.
func (c *RetainedContext) HasInheritedUserMessages() bool {
	for i := range c.userMessages {
		if c.userMessages[i].Inherited {
			return true
		}
	}
	return false
}

// OrderedEntries returns retained evidence with its origin and persisted order
// across all families.
func (c *RetainedContext) OrderedEntries() []OrderedEntry {
	entries := make([]OrderedEntry, 0, len(c.verifiedAnswers)+len(c.userMessages)+len(c.assistantMessages))
	for i := range c.verifiedAnswers {
		entries = append(entries, OrderedEntry{
			Order: c.verifiedAnswers[i].key(),
			Entry: RetainedContextEntry{VerifiedAnswer: &c.verifiedAnswers[i].Value},
		})
	}
	for i := range c.userMessages {
		entries = append(entries, OrderedEntry{
			Order: c.userMessages[i].key(),
			Entry: RetainedContextEntry{UserMessage: &c.userMessages[i].Value},
		})
	}
	for i := range c.assistantMessages {
		entries = append(entries, OrderedEntry{
			Order: c.assistantMessages[i].key(),
			Entry: RetainedContextEntry{AssistantMessage: &c.assistantMessages[i].Value},
		})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Order.Less(entries[j].Order)
	})
	return entries
}

func saturatingAdd(value uint64, delta uint64) uint64 {
	if value > ^uint64(0)-delta {
		return ^uint64(0)
	}
	return value + delta
}

func messageIDsEqual(left *string, right *string) bool {
	if left == nil || right == nil {
		return left == right
	}
	return *left == *right
}

func userMessagesEqual(left RetainedUserMessage, right RetainedUserMessage) bool {
	return reflect.DeepEqual(left, right)
}
