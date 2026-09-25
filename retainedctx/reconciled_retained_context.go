package retainedctx

import "sort"

// LiveUserMessage is one eligible live user message with its persisted
// acceptance order, mirroring Rust's `(Option<u64>, RetainedUserMessage)` input.
type LiveUserMessage struct {
	Order   *uint64
	Message RetainedUserMessage
}

type recoveredUserMessage struct {
	Order   RetainedContextOrder
	Message RetainedUserMessage
}

// ReconciledRetainedContext is retained evidence supplemented by surviving user
// messages without changing the checkpoint.
type ReconciledRetainedContext struct {
	retainedContext *RetainedContext
	recovered       []recoveredUserMessage
	// LatestUserTurnID is the latest ordered input turn, including invocations
	// omitted from instruction evidence.
	LatestUserTurnID *string
	// MissingUserMessages reports that instructions were already missing, or a
	// recovered source could not be ordered.
	MissingUserMessages bool
}

// NewReconciledRetainedContext reconciles eligible live user messages with
// retained sources in acceptance order. The caller supplies original source
// identities and filters out non-user context. Inherited sources must supply no
// local order; retained identities match before ordering. Live messages also
// identify the latest input turn when retained instructions coalesce.
func NewReconciledRetainedContext(retained *RetainedContext, liveMessages []LiveUserMessage) *ReconciledRetainedContext {
	missingUserMessages := retained == nil || retained.HasMissingUserMessages()
	var retainedEntries []OrderedEntry
	if retained != nil {
		retainedEntries = retained.OrderedEntries()
	}
	var latest *struct {
		order  RetainedContextOrder
		turnID string
	}
	for i := len(retainedEntries) - 1; i >= 0; i-- {
		if retainedEntries[i].Entry.UserMessage == nil {
			continue
		}
		latest = &struct {
			order  RetainedContextOrder
			turnID string
		}{order: retainedEntries[i].Order, turnID: retainedEntries[i].Entry.UserMessage.TurnID}
		break
	}
	recover := retained == nil || !retained.UserMessagesComplete()
	matchedEntries := map[int]bool{}
	usedOrders := map[RetainedContextOrder]bool{}
	for i := range retainedEntries {
		usedOrders[retainedEntries[i].Order] = true
	}
	var recovered []recoveredUserMessage
	for _, live := range liveMessages {
		if live.Order != nil {
			order := RetainedContextOrder{Order: *live.Order}
			if latest == nil || latest.order.Less(order) {
				latest = &struct {
					order  RetainedContextOrder
					turnID string
				}{order: order, turnID: live.Message.TurnID}
			}
		}
		if !recover {
			continue
		}
		matchedExisting := false
		for index := range retainedEntries {
			retainedMessage := retainedEntries[index].Entry.UserMessage
			if retainedMessage == nil {
				continue
			}
			if sameUserMessageSource(retainedMessage, &live.Message) && !matchedEntries[index] {
				matchedEntries[index] = true
				matchedExisting = true
				break
			}
		}
		if matchedExisting {
			continue
		}
		// Queued steering can enter raw history after a later-accepted answer.
		// Compare their persisted sequence numbers, including checkpoint-only
		// answers. Parent counters cannot establish an adopted prefix position.
		if live.Order == nil {
			missingUserMessages = true
			continue
		}
		order := RetainedContextOrder{Order: *live.Order}
		if usedOrders[order] {
			missingUserMessages = true
			continue
		}
		usedOrders[order] = true
		recovered = append(recovered, recoveredUserMessage{Order: order, Message: live.Message})
	}
	reconciled := &ReconciledRetainedContext{
		retainedContext:     retained,
		recovered:           recovered,
		MissingUserMessages: missingUserMessages,
	}
	if latest != nil {
		turnID := latest.turnID
		reconciled.LatestUserTurnID = &turnID
	}
	return reconciled
}

// OrderedEntries returns retained and recovered evidence in persisted acceptance
// order, coalescing heartbeat versions after ordering.
func (r *ReconciledRetainedContext) OrderedEntries() []OrderedEntry {
	var entries []OrderedEntry
	if r.retainedContext != nil {
		entries = append(entries, r.retainedContext.OrderedEntries()...)
	}
	for i := range r.recovered {
		entries = append(entries, OrderedEntry{
			Order: r.recovered[i].Order,
			Entry: RetainedContextEntry{UserMessage: &r.recovered[i].Message},
		})
	}
	sort.SliceStable(entries, func(i, j int) bool {
		return entries[i].Order.Less(entries[j].Order)
	})
	heartbeatVersions := map[string]string{}
	var previousScope *bool
	kept := entries[:0]
	for _, entry := range entries {
		inherited := entry.Order.Inherited
		if previousScope == nil || *previousScope != inherited {
			heartbeatVersions = map[string]string{}
			scope := inherited
			previousScope = &scope
		}
		message := entry.Entry.UserMessage
		if message == nil || message.Origin != UserInputOriginHeartbeat {
			kept = append(kept, entry)
			continue
		}
		heartbeat, ok := ParseHeartbeat(message.Text)
		if !ok {
			heartbeatVersions = map[string]string{}
			kept = append(kept, entry)
			continue
		}
		// Checkpoint recovery cannot prove completeness, but an exact bounded
		// envelope can still identify a replay. Keep the checkpoint's
		// missing-evidence warning.
		previous, existed := heartbeatVersions[heartbeat.AutomationID]
		heartbeatVersions[heartbeat.AutomationID] = heartbeat.Instructions
		if !existed || previous != heartbeat.Instructions {
			kept = append(kept, entry)
		}
	}
	return kept
}

// UnmatchedUserMessages filters legacy candidates already represented by
// retained or recovered instructions. Unmatched sources keep their original text
// and identity; this does not assign an order.
func (r *ReconciledRetainedContext) UnmatchedUserMessages(messages []RetainedUserMessage) []RetainedUserMessage {
	var sources []*RetainedUserMessage
	for _, entry := range r.OrderedEntries() {
		if entry.Entry.UserMessage != nil {
			sources = append(sources, entry.Entry.UserMessage)
		}
	}
	out := make([]RetainedUserMessage, 0, len(messages))
	for i := range messages {
		matched := false
		for _, source := range sources {
			if sameUserMessageSource(source, &messages[i]) {
				matched = true
				break
			}
		}
		if !matched {
			out = append(out, messages[i])
		}
	}
	return out
}

func sameUserMessageSource(retained *RetainedUserMessage, candidate *RetainedUserMessage) bool {
	if retained.MessageID != nil {
		return candidate.MessageID != nil && *candidate.MessageID == *retained.MessageID
	}
	return candidate.TurnID == retained.TurnID && candidate.Text == retained.Text
}
