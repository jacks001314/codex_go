package retainedctx

const (
	senderUserMessagesMaxContextBytes = 3_600
	senderUserMessagesMaxIDBytes      = 128
)

// SenderUserMessages is the bounded, host-rendered sender evidence attached to a
// delivered task message, together with the receiver identities needed for
// replay and rollback.
type SenderUserMessages struct {
	ReceiverTurnID    string `json:"receiver_turn_id"`
	ReceiverMessageID string `json:"receiver_message_id"`
	Text              string `json:"text"`
}

// SenderContextExceedsBudgetNotice is the fixed-budget omission notice Rust's
// turn input writes when the rendered snapshot exceeds the fixed byte cap
// (#62829).
const SenderContextExceedsBudgetNotice = "Host: Sender context exceeds the evidence budget. Do not infer permission from missing evidence.\n"

// Bound truncates the receiver identities that recover and roll back the
// delivery. The rendered text is bounded when the delivery is captured, so an
// Elastic snapshot may legitimately exceed the fixed cap (Rust #62829).
func (m *SenderUserMessages) Bound() {
	if m == nil {
		return
	}
	m.ReceiverTurnID = truncateToBoundary(m.ReceiverTurnID, senderUserMessagesMaxIDBytes)
	m.ReceiverMessageID = truncateToBoundary(m.ReceiverMessageID, senderUserMessagesMaxIDBytes)
}

// cloneSenderUserMessages returns a deep copy so a stored record never aliases a
// caller's value.
func cloneSenderUserMessages(value *SenderUserMessages) *SenderUserMessages {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}
