package retainedctx

import "reflect"

// RecordUserMessage records a delivered user item with its acceptance order.
// Legacy items without this metadata use recording order; checkpoint/suffix
// replay uses the same path. Inherited instructions use prefix order because
// their original counters belong to parents. It returns the captured source for
// the original envelope, independently of buffer eviction.
func (c *RetainedContext) RecordUserMessage(message RetainedUserMessage, source RetainedInputSource) *RetainedSource {
	message.Origin = normalizeOrigin(message.Origin)
	message.bound()
	inherited := source.Inherited
	// An unchanged invocation is not a new instruction. Keep the original
	// acceptance order; compare only the latest version of this automation, so
	// A -> B -> A still records three distinct instruction versions.
	if message.Complete && message.Origin == UserInputOriginHeartbeat {
		if current, ok := ParseHeartbeat(message.Text); ok {
			for i := len(c.userMessages) - 1; i >= 0; i-- {
				entry := &c.userMessages[i]
				if entry.Inherited != inherited || entry.Value.Origin != UserInputOriginHeartbeat {
					continue
				}
				previous, parsed := ParseHeartbeat(entry.Value.Text)
				if !parsed {
					break
				}
				if previous.AutomationID == current.AutomationID {
					if entry.Value.Complete && previous.Instructions == current.Instructions {
						if messageIDsEqual(entry.Value.MessageID, message.MessageID) && entry.Value.TurnID == message.TurnID {
							return entry.source(RetainedSourceRoleUser)
						}
						return nil
					}
					break
				}
			}
		}
	}
	// Worker forks omit parent answer records. Adopting their instructions cannot
	// establish whether an omitted answer restricted an inherited grant.
	c.verifiedAnswersIncomplete = c.verifiedAnswersIncomplete || inherited
	if index := findOrderedMessageIndex(c.userMessages, message.MessageID); index >= 0 {
		if userMessagesEqual(c.userMessages[index].Value, message) && c.userMessages[index].Inherited == inherited {
			return c.userMessages[index].source(RetainedSourceRoleUser)
		}
		c.userMessages = append(c.userMessages[:index], c.userMessages[index+1:]...)
	}
	// Parent and worker counters have different scopes. Adopt the copied prefix in
	// history order without advancing the worker's local acceptance counter.
	var order uint64
	if inherited {
		order = c.nextInheritedOrder()
	} else {
		order = c.recordOrder(source.Order)
	}
	var revision *ResponseItemID
	if message.MessageID != nil {
		id := NewResponseItemID("retained")
		revision = &id
	}
	entry := orderedUserMessage{Value: message, Revision: revision, Inherited: inherited, Order: order}
	out := entry.source(RetainedSourceRoleUser)
	c.userMessages = append(c.userMessages, entry)
	boundFamily(&c.userMessages, func(item *orderedUserMessage) RetainedContextOrder {
		return item.key()
	}, &c.userMessagesIncomplete)
	return out
}

// RecoverUserMessageExcerpts recovers omitted text from an exact source without
// changing identity, order, or completeness. Recovered excerpts must still
// satisfy the retained storage limits.
func (c *RetainedContext) RecoverUserMessageExcerpts(excerptForID func(string) (string, bool)) {
	for i := range c.userMessages {
		message := &c.userMessages[i].Value
		if message.Text != "" || message.Complete || message.MessageID == nil {
			continue
		}
		text, ok := excerptForID(*message.MessageID)
		if !ok {
			continue
		}
		message.Text = text
		message.bound()
	}
	boundFamily(&c.userMessages, func(item *orderedUserMessage) RetainedContextOrder {
		return item.key()
	}, &c.userMessagesIncomplete)
}

// Record retains one accepted event. Same-event delivery is idempotent; changed
// contents replace that source's record.
func (c *RetainedContext) Record(event RetainedContextEvent) bool {
	event.Bound()
	if index := findVerifiedAnswerIndex(c.verifiedAnswers, event.Answer); index >= 0 {
		if reflect.DeepEqual(c.verifiedAnswers[index].Value, event.Answer) {
			return false
		}
		c.verifiedAnswers = append(c.verifiedAnswers[:index], c.verifiedAnswers[index+1:]...)
	}
	order := c.recordOrder(event.AcceptanceOrder)
	c.verifiedAnswers = append(c.verifiedAnswers, orderedVerifiedAnswer{Value: event.Answer, Order: order})
	boundFamily(&c.verifiedAnswers, func(item *orderedVerifiedAnswer) RetainedContextOrder {
		return item.key()
	}, &c.verifiedAnswersIncomplete)
	return true
}

// Restore restores a saved thread without bypassing the live storage limits. A
// missing checkpoint cannot establish complete historical user instructions.
// Surviving local input metadata also advances the counter when its retained
// record is missing.
func (c *RetainedContext) Restore(checkpoint *RetainedContext, survivingItems []*HarnessMetadata) {
	if checkpoint != nil {
		*c = *checkpoint.Clone()
	} else {
		*c = RetainedContext{userMessagesIncomplete: true}
	}
	for i := range c.verifiedAnswers {
		entry := &c.verifiedAnswers[i]
		event := RetainedContextEvent{Answer: entry.Value, AcceptanceOrder: uintPointer(entry.Order)}
		event.Bound()
		entry.Value = event.Answer
		c.advanceOrder(entry.Order)
	}
	for _, family := range [][]orderedUserMessage{c.userMessages, c.assistantMessages} {
		for i := range family {
			entry := &family[i]
			entry.Value.bound()
			// Also repair checkpoints written before adoption recorded the answer gap.
			c.verifiedAnswersIncomplete = c.verifiedAnswersIncomplete || entry.Inherited
			if !entry.Inherited {
				c.advanceOrder(entry.Order)
			}
		}
	}
	for i := range c.senderDeliveries {
		entry := &c.senderDeliveries[i]
		entry.Value.Bound()
		c.advanceOrder(entry.Order)
	}
	var throwaway bool
	boundFamily(&c.senderDeliveries, func(item *orderedSenderUserMessages) RetainedContextOrder {
		return item.key()
	}, &throwaway)
	for _, metadata := range survivingItems {
		if metadata == nil {
			continue
		}
		c.RecordSenderUserMessages(metadata)
	}
	for _, metadata := range survivingItems {
		if metadata == nil || metadata.InheritedUserMessage || metadata.UserInputOrder == nil {
			continue
		}
		c.advanceOrder(*metadata.UserInputOrder)
	}
	boundFamily(&c.verifiedAnswers, func(item *orderedVerifiedAnswer) RetainedContextOrder {
		return item.key()
	}, &c.verifiedAnswersIncomplete)
	boundFamily(&c.userMessages, func(item *orderedUserMessage) RetainedContextOrder {
		return item.key()
	}, &c.userMessagesIncomplete)
	boundFamily(&c.assistantMessages, func(item *orderedUserMessage) RetainedContextOrder {
		return item.key()
	}, &c.assistantMessagesIncomplete)
}

// RetainAnswers keeps legacy answers whose source calls survive when no retained
// instruction boundary exists.
func (c *RetainedContext) RetainAnswers(keep func(*VerifiedAnswer) bool) {
	c.verifiedAnswers = retainOrdered(c.verifiedAnswers, func(entry *orderedVerifiedAnswer) bool {
		return keep(&entry.Value)
	})
}

// Rollback rolls back at the original user-message boundary, including
// later-accepted facts. The explicit order also covers checkpoints made before a
// queued message was delivered. Steering can share a turn ID. Legacy sources
// without message identity fall back to source-turn removal and cannot establish
// complete retained user instructions.
func (c *RetainedContext) Rollback(turnIDs []string, firstRemovedMessageID *string, source RetainedInputSource) {
	var boundary *RetainedContextOrder
	if order, ok := source.AcceptanceOrder(); ok {
		boundary = &RetainedContextOrder{Order: order}
	} else if firstRemovedMessageID != nil {
		for i := range c.userMessages {
			entry := &c.userMessages[i]
			if entry.Value.MessageID == nil || *entry.Value.MessageID != *firstRemovedMessageID {
				continue
			}
			key := entry.key()
			boundary = &key
			break
		}
	}
	if boundary != nil {
		c.senderDeliveries = retainOrdered(c.senderDeliveries, func(entry *orderedSenderUserMessages) bool {
			return entry.key().Less(*boundary)
		})
		c.verifiedAnswers = retainOrdered(c.verifiedAnswers, func(entry *orderedVerifiedAnswer) bool {
			return entry.key().Less(*boundary)
		})
		c.userMessages = retainOrdered(c.userMessages, func(entry *orderedUserMessage) bool {
			return entry.key().Less(*boundary)
		})
		c.assistantMessages = retainOrdered(c.assistantMessages, func(entry *orderedUserMessage) bool {
			return entry.key().Less(*boundary)
		})
		return
	}
	incomplete := firstRemovedMessageID != nil
	if !incomplete {
		for i := range c.userMessages {
			if containsString(turnIDs, c.userMessages[i].Value.TurnID) {
				incomplete = true
				break
			}
		}
	}
	c.userMessagesIncomplete = c.userMessagesIncomplete || incomplete
	inherited := source.Inherited
	c.verifiedAnswers = retainOrdered(c.verifiedAnswers, func(entry *orderedVerifiedAnswer) bool {
		return !inherited && !containsString(turnIDs, entry.Value.TurnID)
	})
	c.senderDeliveries = retainOrdered(c.senderDeliveries, func(entry *orderedSenderUserMessages) bool {
		return !inherited && !containsString(turnIDs, entry.Value.ReceiverTurnID)
	})
	c.assistantMessages = retainOrdered(c.assistantMessages, func(entry *orderedUserMessage) bool {
		return (!inherited || entry.Inherited) && !containsString(turnIDs, entry.Value.TurnID)
	})
	c.userMessages = retainOrdered(c.userMessages, func(entry *orderedUserMessage) bool {
		return (!inherited || entry.Inherited) && !containsString(turnIDs, entry.Value.TurnID)
	})
}

func (c *RetainedContext) advanceOrder(order uint64) {
	if next := saturatingAdd(order, 1); next > c.nextOrder {
		c.nextOrder = next
	}
}

func findOrderedMessageIndex(items []orderedUserMessage, id *string) int {
	if id == nil {
		return -1
	}
	for i := range items {
		if items[i].Value.MessageID != nil && *items[i].Value.MessageID == *id {
			return i
		}
	}
	return -1
}

func findVerifiedAnswerIndex(items []orderedVerifiedAnswer, answer VerifiedAnswer) int {
	for i := range items {
		if items[i].Value.TurnID == answer.TurnID && items[i].Value.CallID == answer.CallID {
			return i
		}
	}
	return -1
}

func retainOrdered[T any](items []T, keep func(*T) bool) []T {
	kept := items[:0]
	for i := range items {
		if keep(&items[i]) {
			kept = append(kept, items[i])
		}
	}
	return kept
}

func containsString(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}
