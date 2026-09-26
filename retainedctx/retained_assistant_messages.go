package retainedctx

// AssistantMessageOrder returns the primary acceptance order already recorded for
// an assistant message id. Rust reserves that order once
// (`Session::reserve_assistant_message_order`) and persists it with the item's
// harness metadata; a Go re-derivation reuses the recorded order so repeated
// passes neither advance the thread's counter nor reorder the evidence.
func (c *RetainedContext) AssistantMessageOrder(messageID *string) (uint64, bool) {
	if c == nil || messageID == nil {
		return 0, false
	}
	for i := range c.assistantMessages {
		entry := &c.assistantMessages[i]
		if entry.Value.MessageID == nil || *entry.Value.MessageID != *messageID || entry.Inherited {
			continue
		}
		return entry.Order, true
	}
	return 0, false
}

// RecordAssistantMessage records original assistant text without interpreting it
// as a question or grant. Older unsequenced sources cannot establish order
// relative to queued user replies. It returns the captured source for the
// original envelope, independently of buffer eviction.
func (c *RetainedContext) RecordAssistantMessage(message RetainedUserMessage, source RetainedInputSource) *RetainedSource {
	if !source.Inherited && source.Order == nil {
		// Leave unsequenced sources to legacy transcript selection. Missing
		// ordering alone does not establish an omission from reviewer context.
		return nil
	}
	message.Origin = normalizeOrigin(message.Origin)
	message.bound()
	empty := message.Complete && message.Text == ""
	inherited := source.Inherited
	if index := findOrderedMessageIndex(c.assistantMessages, message.MessageID); index >= 0 {
		if userMessagesEqual(c.assistantMessages[index].Value, message) &&
			c.assistantMessages[index].Inherited == inherited &&
			!empty {
			return c.assistantMessages[index].source(RetainedSourceRoleAssistant)
		}
		c.assistantMessages = append(c.assistantMessages[:index], c.assistantMessages[index+1:]...)
	}
	var order uint64
	if inherited {
		order = c.nextInheritedOrder()
	} else {
		order = c.recordOrder(source.Order)
	}
	if empty {
		return nil
	}
	var revision *ResponseItemID
	if message.MessageID != nil {
		id := NewResponseItemID("retained")
		revision = &id
	}
	entry := orderedUserMessage{Value: message, Revision: revision, Inherited: inherited, Order: order}
	out := entry.source(RetainedSourceRoleAssistant)
	c.assistantMessages = append(c.assistantMessages, entry)
	boundFamily(&c.assistantMessages, func(item *orderedUserMessage) RetainedContextOrder {
		return item.key()
	}, &c.assistantMessagesIncomplete)
	return out
}
