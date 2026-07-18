package chatwidget

type PendingInputPreview struct {
	QueuedMessages []string
	PendingSteers  []string
	RejectedSteers []string
}

type InputQueueState struct {
	QueuedUserMessages                []QueuedUserMessage
	QueuedUserMessageHistoryRecords   []UserMessageHistoryRecord
	UserTurnPendingStart              bool
	RejectedSteersQueue               []UserMessage
	RejectedSteerHistoryRecords       []UserMessageHistoryRecord
	PendingSteers                     []PendingSteer
	SubmitPendingSteersAfterInterrupt bool
	SuppressQueueAutosend             bool
}

func (s *InputQueueState) HasQueuedFollowUpMessages() bool {
	return s != nil && (len(s.RejectedSteersQueue) > 0 || len(s.QueuedUserMessages) > 0)
}

func (s *InputQueueState) Clear() {
	if s == nil {
		return
	}
	s.QueuedUserMessages = nil
	s.QueuedUserMessageHistoryRecords = nil
	s.UserTurnPendingStart = false
	s.RejectedSteersQueue = nil
	s.RejectedSteerHistoryRecords = nil
	s.PendingSteers = nil
	s.SubmitPendingSteersAfterInterrupt = false
}

func (s *InputQueueState) Preview() PendingInputPreview {
	if s == nil {
		return PendingInputPreview{}
	}
	preview := PendingInputPreview{
		QueuedMessages: make([]string, 0, len(s.QueuedUserMessages)),
		PendingSteers:  make([]string, 0, len(s.PendingSteers)),
		RejectedSteers: make([]string, 0, len(s.RejectedSteersQueue)),
	}
	for index, message := range s.QueuedUserMessages {
		preview.QueuedMessages = append(preview.QueuedMessages, UserMessagePreviewText(message.UserMessage, historyRecordAt(s.QueuedUserMessageHistoryRecords, index)))
	}
	for _, steer := range s.PendingSteers {
		record := steer.HistoryRecord
		preview.PendingSteers = append(preview.PendingSteers, UserMessagePreviewText(steer.UserMessage, &record))
	}
	for index, message := range s.RejectedSteersQueue {
		preview.RejectedSteers = append(preview.RejectedSteers, UserMessagePreviewText(message, historyRecordAt(s.RejectedSteerHistoryRecords, index)))
	}
	return preview
}

func historyRecordAt(records []UserMessageHistoryRecord, index int) *UserMessageHistoryRecord {
	if index < 0 || index >= len(records) {
		return nil
	}
	return &records[index]
}
