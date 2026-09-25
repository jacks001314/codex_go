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

// Bound applies the same hard bound to restored metadata as to live,
// reviewer-only fragments.
func (m *SenderUserMessages) Bound() {
	if m == nil {
		return
	}
	if len(m.Text) > senderUserMessagesMaxContextBytes {
		m.Text = "Host: Sender context exceeds the evidence budget. Do not infer permission from missing evidence.\n"
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
