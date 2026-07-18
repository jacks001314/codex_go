package chatwidget

type TurnAbortReason string

const (
	TurnAbortInterrupted   TurnAbortReason = "interrupted"
	TurnAbortBudgetLimited TurnAbortReason = "budget_limited"
	TurnAbortReviewDone    TurnAbortReason = "review_done"
)

type InterruptedTurnNoticeMode string

const (
	InterruptedTurnNoticeDefault  InterruptedTurnNoticeMode = ""
	InterruptedTurnNoticeSuppress InterruptedTurnNoticeMode = "suppress"
)

type InterruptedTurnNoticeKind string

const (
	InterruptedTurnNoticeInfo  InterruptedTurnNoticeKind = "info"
	InterruptedTurnNoticeError InterruptedTurnNoticeKind = "error"
)

type InterruptedTurnRestoreOptions struct {
	Reason          TurnAbortReason
	Composer        ThreadComposerState
	NoticeMode      InterruptedTurnNoticeMode
	InterruptNotice string
}

type InterruptedTurnRestoreResult struct {
	FinalizeTurn           bool
	RequestRedraw          bool
	NoticeKind             InterruptedTurnNoticeKind
	NoticeMessage          string
	RestoredComposer       *ThreadComposerState
	SubmittedMessage       *UserMessage
	SubmittedHistoryRecord UserMessageHistoryRecord
	CancelledPrompt        *UserMessage
	PendingInputPreview    PendingInputPreview
}

type CancelEditState struct {
	Prompt   *UserMessage
	Eligible bool
	Armed    bool
}

func (s *CancelEditState) RecordCancelEditCandidate(prompt UserMessage) {
	if s == nil {
		return
	}
	copy := prompt
	s.Prompt = &copy
	s.Eligible = true
	s.Armed = false
}

func (s *CancelEditState) RecordVisibleTurnActivity() {
	if s == nil {
		return
	}
	s.Eligible = false
	s.Armed = false
}

func (s *CancelEditState) Arm(composerEmpty bool, queue InputQueueState, activeSideConversation bool) {
	if s == nil {
		return
	}
	s.Armed = s.Eligible &&
		s.Prompt != nil &&
		composerEmpty &&
		len(queue.PendingSteers) == 0 &&
		!queue.HasQueuedFollowUpMessages() &&
		!activeSideConversation
}

func (s *CancelEditState) TakeArmedPrompt(reason TurnAbortReason) (UserMessage, bool) {
	if s == nil || reason != TurnAbortInterrupted || !s.Armed || !s.Eligible || s.Prompt == nil {
		return UserMessage{}, false
	}
	prompt := *s.Prompt
	s.Prompt = nil
	s.Armed = false
	s.Eligible = false
	return prompt, true
}

func (s *CancelEditState) Clear() {
	if s == nil {
		return
	}
	*s = CancelEditState{}
}

func (s *InputQueueState) PopNextQueuedUserMessage() (QueuedUserMessage, UserMessageHistoryRecord, bool) {
	if s == nil {
		return QueuedUserMessage{}, UserMessageHistoryRecord{}, false
	}
	if len(s.RejectedSteersQueue) == 0 {
		if len(s.QueuedUserMessages) == 0 {
			return QueuedUserMessage{}, UserMessageHistoryRecord{}, false
		}
		message := s.QueuedUserMessages[0]
		s.QueuedUserMessages = s.QueuedUserMessages[1:]
		record := UserMessageTextHistoryRecord()
		if len(s.QueuedUserMessageHistoryRecords) > 0 {
			record = s.QueuedUserMessageHistoryRecords[0]
			s.QueuedUserMessageHistoryRecords = s.QueuedUserMessageHistoryRecords[1:]
		}
		return message, record, true
	}

	withRecords := make([]messageWithHistoryRecord, 0, len(s.RejectedSteersQueue))
	for i, message := range s.RejectedSteersQueue {
		record := UserMessageTextHistoryRecord()
		if i < len(s.RejectedSteerHistoryRecords) {
			record = s.RejectedSteerHistoryRecords[i]
		}
		withRecords = append(withRecords, UserMessageWithHistory(message, record))
	}
	s.RejectedSteersQueue = nil
	s.RejectedSteerHistoryRecords = nil
	merged, record := MergeUserMessagesWithHistoryRecord(withRecords)
	return NewQueuedUserMessage(merged, QueuedInputPlain), record, true
}

func (s *InputQueueState) PopLatestQueuedComposerState() (ThreadComposerState, bool) {
	if s == nil {
		return ThreadComposerState{}, false
	}
	if len(s.QueuedUserMessages) > 0 {
		index := len(s.QueuedUserMessages) - 1
		message := s.QueuedUserMessages[index]
		s.QueuedUserMessages = s.QueuedUserMessages[:index]
		record := UserMessageTextHistoryRecord()
		if index < len(s.QueuedUserMessageHistoryRecords) {
			record = s.QueuedUserMessageHistoryRecords[index]
			s.QueuedUserMessageHistoryRecords = s.QueuedUserMessageHistoryRecords[:index]
		}
		return ComposerStateFromUserMessage(UserMessageForRestore(message.UserMessage, record), message.PendingPastes), true
	}
	if len(s.RejectedSteersQueue) == 0 {
		return ThreadComposerState{}, false
	}
	index := len(s.RejectedSteersQueue) - 1
	message := s.RejectedSteersQueue[index]
	s.RejectedSteersQueue = s.RejectedSteersQueue[:index]
	record := UserMessageTextHistoryRecord()
	if index < len(s.RejectedSteerHistoryRecords) {
		record = s.RejectedSteerHistoryRecords[index]
		s.RejectedSteerHistoryRecords = s.RejectedSteerHistoryRecords[:index]
	}
	return ComposerStateFromUserMessage(UserMessageForRestore(message, record), nil), true
}

func (s *InputQueueState) DrainPendingMessagesForRestore(composer ThreadComposerState) (ThreadComposerState, bool) {
	if s == nil || (len(s.PendingSteers) == 0 && !s.HasQueuedFollowUpMessages()) {
		return ThreadComposerState{}, false
	}

	toMerge := make([]UserMessage, 0, len(s.RejectedSteersQueue)+len(s.PendingSteers)+len(s.QueuedUserMessages)+1)
	for i, message := range s.RejectedSteersQueue {
		toMerge = append(toMerge, UserMessageForRestore(message, historyRecordValueAt(s.RejectedSteerHistoryRecords, i)))
	}
	s.RejectedSteersQueue = nil
	s.RejectedSteerHistoryRecords = nil

	for _, steer := range s.PendingSteers {
		toMerge = append(toMerge, UserMessageForRestore(steer.UserMessage, recordOrText(steer.HistoryRecord)))
	}
	s.PendingSteers = nil

	pendingPastes := make([][2]string, 0)
	usedPastePlaceholders := map[string]bool{}
	for i, queued := range s.QueuedUserMessages {
		message := UserMessageForRestore(queued.UserMessage, historyRecordValueAt(s.QueuedUserMessageHistoryRecords, i))
		message, messagePastes := remapCollidingPastePlaceholders(message, append([][2]string(nil), queued.PendingPastes...), usedPastePlaceholders)
		pendingPastes = append(pendingPastes, messagePastes...)
		toMerge = append(toMerge, message)
	}
	s.QueuedUserMessages = nil
	s.QueuedUserMessageHistoryRecords = nil

	existingMessage := UserMessage{
		Text:            composer.Text,
		LocalImages:     append([]string(nil), composer.LocalImages...),
		RemoteImageURLs: append([]string(nil), composer.RemoteImageURLs...),
		TextElements:    cloneTextElements(composer.TextElements),
		MentionBindings: append([]string(nil), composer.MentionBindings...),
	}
	if composerHasRestoreContent(composer) {
		existingMessage, composerPastes := remapCollidingPastePlaceholders(existingMessage, append([][2]string(nil), composer.PendingPastes...), usedPastePlaceholders)
		toMerge = append(toMerge, existingMessage)
		pendingPastes = append(pendingPastes, composerPastes...)
	}

	return ComposerStateFromUserMessage(MergeUserMessages(toMerge), pendingPastes), true
}

func (s *InputQueueState) DrainPendingSteersForSubmit() (UserMessage, UserMessageHistoryRecord, bool) {
	if s == nil || len(s.PendingSteers) == 0 {
		return UserMessage{}, UserMessageHistoryRecord{}, false
	}
	withRecords := make([]messageWithHistoryRecord, 0, len(s.PendingSteers))
	for _, steer := range s.PendingSteers {
		withRecords = append(withRecords, UserMessageWithHistory(steer.UserMessage, recordOrText(steer.HistoryRecord)))
	}
	s.PendingSteers = nil
	returnValue, record := MergeUserMessagesWithHistoryRecord(withRecords)
	return returnValue, record, true
}

func (s *InputQueueState) OnInterruptedTurn(cancelEdit *CancelEditState, options InterruptedTurnRestoreOptions) InterruptedTurnRestoreResult {
	result := InterruptedTurnRestoreResult{
		FinalizeTurn:           true,
		RequestRedraw:          true,
		SubmittedHistoryRecord: UserMessageTextHistoryRecord(),
	}
	if s == nil {
		return result
	}

	cancelledPrompt, cancelled := cancelEdit.TakeArmedPrompt(options.Reason)
	if cancelled {
		copy := cancelledPrompt
		result.CancelledPrompt = &copy
	}

	sendPendingSteersImmediately := s.SubmitPendingSteersAfterInterrupt
	s.SubmitPendingSteersAfterInterrupt = false
	if !cancelled && options.NoticeMode != InterruptedTurnNoticeSuppress {
		if sendPendingSteersImmediately {
			result.NoticeKind = InterruptedTurnNoticeInfo
			result.NoticeMessage = "Model interrupted to submit steer instructions."
		} else {
			result.NoticeKind = InterruptedTurnNoticeError
			result.NoticeMessage = options.InterruptNotice
		}
	}

	if sendPendingSteersImmediately {
		if message, record, ok := s.DrainPendingSteersForSubmit(); ok {
			copy := message
			result.SubmittedMessage = &copy
			result.SubmittedHistoryRecord = record
		} else if restored, ok := s.DrainPendingMessagesForRestore(options.Composer); ok {
			result.RestoredComposer = &restored
		}
	} else if restored, ok := s.DrainPendingMessagesForRestore(options.Composer); ok {
		result.RestoredComposer = &restored
	}

	result.PendingInputPreview = s.Preview()
	return result
}

func ComposerStateFromUserMessage(message UserMessage, pendingPastes [][2]string) ThreadComposerState {
	return ThreadComposerState{
		Text:            message.Text,
		LocalImages:     append([]string(nil), message.LocalImages...),
		RemoteImageURLs: append([]string(nil), message.RemoteImageURLs...),
		TextElements:    cloneTextElements(message.TextElements),
		MentionBindings: append([]string(nil), message.MentionBindings...),
		PendingPastes:   append([][2]string(nil), pendingPastes...),
	}
}

func historyRecordValueAt(records []UserMessageHistoryRecord, index int) UserMessageHistoryRecord {
	if index < 0 || index >= len(records) || records[index].Kind == "" {
		return UserMessageTextHistoryRecord()
	}
	return records[index]
}

func composerHasRestoreContent(composer ThreadComposerState) bool {
	return composer.Text != "" || len(composer.LocalImages) > 0 || len(composer.RemoteImageURLs) > 0
}
