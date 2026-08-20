package chatwidget

import (
	"sort"
	"strings"

	idecontext "codex_go/tui/ide_context"
	"codex_go/turn"
)

type UserMessage struct {
	Text            string
	LocalImages     []string
	RemoteImageURLs []string
	LocalAudio      []string
	RemoteAudioURLs []string
	TextElements    []turn.TextElement
	MentionBindings []string
}

func NewUserMessage(text string) UserMessage {
	return UserMessage{Text: text}
}

func (m UserMessage) HasContent() bool {
	return m.Text != "" ||
		len(m.LocalImages) > 0 ||
		len(m.RemoteImageURLs) > 0 ||
		len(m.LocalAudio) > 0 || len(m.RemoteAudioURLs) > 0 ||
		len(m.TextElements) > 0 ||
		len(m.MentionBindings) > 0
}

type UserMessageHistoryRecordKind string

const (
	UserMessageHistoryText     UserMessageHistoryRecordKind = "user_message_text"
	UserMessageHistoryOverride UserMessageHistoryRecordKind = "override"
)

type UserMessageHistoryRecord struct {
	Kind         UserMessageHistoryRecordKind
	Text         string
	TextElements []turn.TextElement
}

func UserMessageTextHistoryRecord() UserMessageHistoryRecord {
	return UserMessageHistoryRecord{Kind: UserMessageHistoryText}
}

func UserMessageOverrideHistoryRecord(text string) UserMessageHistoryRecord {
	return UserMessageHistoryRecord{Kind: UserMessageHistoryOverride, Text: text}
}

func UserMessageOverrideHistoryRecordWithElements(text string, elements []turn.TextElement) UserMessageHistoryRecord {
	return UserMessageHistoryRecord{
		Kind:         UserMessageHistoryOverride,
		Text:         text,
		TextElements: cloneTextElements(elements),
	}
}

type ShellEscapePolicy string

const (
	ShellEscapeAllow    ShellEscapePolicy = "allow"
	ShellEscapeDisallow ShellEscapePolicy = "disallow"
)

type QueuedInputAction string

const (
	QueuedInputPlain      QueuedInputAction = "plain"
	QueuedInputLiteral    QueuedInputAction = "literal"
	QueuedInputParseSlash QueuedInputAction = "parse_slash"
	QueuedInputRunShell   QueuedInputAction = "run_shell"
)

type QueuedUserMessage struct {
	UserMessage   UserMessage
	Action        QueuedInputAction
	PendingPastes [][2]string
}

func NewQueuedUserMessage(message UserMessage, action QueuedInputAction) QueuedUserMessage {
	if strings.TrimSpace(string(action)) == "" {
		action = QueuedInputPlain
	}
	return QueuedUserMessage{UserMessage: message, Action: action}
}

func (m QueuedUserMessage) IntoUserMessage() UserMessage {
	return m.UserMessage
}

type QueueDrain string

const (
	QueueDrainContinue QueueDrain = "continue"
	QueueDrainStop     QueueDrain = "stop"
)

type PendingSteerCompareKey struct {
	Message    string
	ImageCount int
}

type PendingSteer struct {
	UserMessage   UserMessage
	HistoryRecord UserMessageHistoryRecord
	CompareKey    PendingSteerCompareKey
}

type ThreadComposerState struct {
	Text            string
	LocalImages     []string
	RemoteImageURLs []string
	LocalAudio      []string
	RemoteAudioURLs []string
	TextElements    []turn.TextElement
	MentionBindings []string
	PendingPastes   [][2]string
}

func (s ThreadComposerState) HasContent() bool {
	return s.Text != "" ||
		len(s.LocalImages) > 0 ||
		len(s.RemoteImageURLs) > 0 ||
		len(s.LocalAudio) > 0 || len(s.RemoteAudioURLs) > 0 ||
		len(s.TextElements) > 0 ||
		len(s.MentionBindings) > 0 ||
		len(s.PendingPastes) > 0
}

type ThreadInputState struct {
	Composer                          *ThreadComposerState
	PendingSteers                     []UserMessage
	PendingSteerHistoryRecords        []UserMessageHistoryRecord
	PendingSteerCompareKeys           []PendingSteerCompareKey
	RejectedSteersQueue               []UserMessage
	RejectedSteerHistoryRecords       []UserMessageHistoryRecord
	QueuedUserMessages                []QueuedUserMessage
	QueuedUserMessageHistoryRecords   []UserMessageHistoryRecord
	UserTurnPendingStart              bool
	CurrentCollaborationMode          string
	ActiveCollaborationModeMask       *string
	TaskRunning                       bool
	AgentTurnRunning                  bool
	SubmitPendingSteersAfterInterrupt bool
}

type UserMessageDisplay struct {
	Message         string
	LocalImages     []string
	RemoteImageURLs []string
	LocalAudio      []string
	RemoteAudioURLs []string
	TextElements    []turn.TextElement
}

func InitialUserMessage(text string, localImagePaths []string, textElements []turn.TextElement) (UserMessage, bool) {
	if text == "" && len(localImagePaths) == 0 {
		return UserMessage{}, false
	}
	localImages := make([]string, 0, len(localImagePaths))
	for index, path := range localImagePaths {
		_ = localImageLabelText(index + 1)
		localImages = append(localImages, path)
	}
	return UserMessage{
		Text:         text,
		LocalImages:  localImages,
		TextElements: cloneTextElements(textElements),
	}, true
}

func AppServerTextElements(elements []turn.TextElement) []turn.TextElement {
	return cloneTextElements(elements)
}

func MergeUserMessages(messages []UserMessage) UserMessage {
	withRecords := make([]messageWithHistoryRecord, 0, len(messages))
	for _, message := range messages {
		withRecords = append(withRecords, messageWithHistoryRecord{
			Message: message,
			Record:  UserMessageTextHistoryRecord(),
		})
	}
	remapped := remapUserMessagesWithHistoryRecords(withRecords)
	merged := make([]UserMessage, 0, len(remapped))
	for _, item := range remapped {
		merged = append(merged, item.Message)
	}
	return mergeRemappedUserMessages(merged)
}

func MergeUserMessagesWithHistoryRecord(messages []messageWithHistoryRecord) (UserMessage, UserMessageHistoryRecord) {
	remapped := remapUserMessagesWithHistoryRecords(messages)
	allText := true
	for _, item := range remapped {
		if item.Record.Kind != UserMessageHistoryText {
			allText = false
			break
		}
	}
	if allText {
		merged := make([]UserMessage, 0, len(remapped))
		for _, item := range remapped {
			merged = append(merged, item.Message)
		}
		return mergeRemappedUserMessages(merged), UserMessageTextHistoryRecord()
	}

	historyText := strings.Builder{}
	var historyElements []turn.TextElement
	segments := 0
	appendHistory := func(text string, elements []turn.TextElement) {
		if segments > 0 {
			historyText.WriteByte('\n')
		}
		appendTextWithRebasedElements(&historyText, &historyElements, text, elements)
		segments++
	}
	for _, item := range remapped {
		switch {
		case item.Record.Kind == UserMessageHistoryOverride && item.Record.Text != "":
			appendHistory(item.Record.Text, item.Record.TextElements)
		case item.Record.Kind == UserMessageHistoryOverride && item.Message.Text == "":
			continue
		default:
			appendHistory(item.Message.Text, item.Message.TextElements)
		}
	}

	merged := make([]UserMessage, 0, len(remapped))
	for _, item := range remapped {
		merged = append(merged, item.Message)
	}
	return mergeRemappedUserMessages(merged), UserMessageOverrideHistoryRecordWithElements(historyText.String(), historyElements)
}

type messageWithHistoryRecord struct {
	Message UserMessage
	Record  UserMessageHistoryRecord
}

func UserMessageWithHistory(message UserMessage, record UserMessageHistoryRecord) messageWithHistoryRecord {
	return messageWithHistoryRecord{Message: message, Record: record}
}

func UserMessageForRestore(message UserMessage, record UserMessageHistoryRecord) UserMessage {
	if record.Kind == UserMessageHistoryOverride && record.Text != "" {
		message.Text = record.Text
		message.TextElements = cloneTextElements(record.TextElements)
	}
	return message
}

func UserMessageDisplayForHistory(message UserMessage, record UserMessageHistoryRecord) UserMessageDisplay {
	return UserMessageDisplayFromParts(UserMessageForRestore(message, record))
}

func UserMessageDisplayFromParts(message UserMessage) UserMessageDisplay {
	visibleMessage, promptRequestOffset := idecontext.ExtractPromptRequestWithOffset(message.Text)
	promptRequestEnd := promptRequestOffset + len(visibleMessage)
	textElements := make([]turn.TextElement, 0, len(message.TextElements))
	for _, element := range message.TextElements {
		start := int(element.ByteRange.Start)
		end := int(element.ByteRange.End)
		if start < promptRequestOffset || end > promptRequestEnd {
			continue
		}
		element.ByteRange.Start = uint(start - promptRequestOffset)
		element.ByteRange.End = uint(end - promptRequestOffset)
		textElements = append(textElements, element)
	}
	return UserMessageDisplay{
		Message:         visibleMessage,
		LocalImages:     append([]string(nil), message.LocalImages...),
		RemoteImageURLs: append([]string(nil), message.RemoteImageURLs...),
		LocalAudio:      append([]string(nil), message.LocalAudio...),
		RemoteAudioURLs: append([]string(nil), message.RemoteAudioURLs...),
		TextElements:    cloneTextElements(textElements),
	}
}

func UserMessagePreviewText(message UserMessage, record *UserMessageHistoryRecord) string {
	if record != nil && record.Kind == UserMessageHistoryOverride && record.Text != "" {
		return record.Text
	}
	return message.Text
}

func PendingSteerCompareKeyFromItems(items []turn.TurnUserInput) PendingSteerCompareKey {
	key := PendingSteerCompareKey{}
	for _, item := range items {
		switch item.Type {
		case "image", "localImage":
			key.ImageCount++
		case "skill", "mention":
			continue
		default:
			if item.Text != "" {
				key.Message += item.Text
			} else if item.URL != "" || item.Path != "" {
				key.ImageCount++
			}
		}
	}
	return key
}

func UserMessageDisplayFromInputs(items []turn.TurnUserInput) UserMessageDisplay {
	message := UserMessage{}
	for _, item := range items {
		switch item.Type {
		case "image":
			if item.URL != "" {
				message.RemoteImageURLs = append(message.RemoteImageURLs, item.URL)
			}
		case "localImage":
			if item.Path != "" {
				message.LocalImages = append(message.LocalImages, item.Path)
			}
		case "audio":
			if item.URL != "" {
				message.RemoteAudioURLs = append(message.RemoteAudioURLs, item.URL)
			}
		case "localAudio":
			if item.Path != "" {
				message.LocalAudio = append(message.LocalAudio, item.Path)
			}
		case "skill", "mention":
			continue
		default:
			appendTextWithRebasedElementsString(&message.Text, &message.TextElements, item.Text, item.TextElements)
		}
	}
	return UserMessageDisplayFromParts(message)
}

func remapCollidingPastePlaceholders(message UserMessage, pendingPastes [][2]string, used map[string]bool) (UserMessage, [][2]string) {
	if used == nil {
		used = map[string]bool{}
	}
	mapping := map[string]string{}
	for index := range pendingPastes {
		placeholder := pendingPastes[index][0]
		if !used[placeholder] {
			used[placeholder] = true
			continue
		}
		base := "[Pasted Content " + formatInt64(int64(len([]rune(pendingPastes[index][1])))) + " chars]"
		suffix := int64(2)
		for {
			replacement := base + " #" + formatInt64(suffix)
			if !used[replacement] {
				used[replacement] = true
				mapping[placeholder] = replacement
				pendingPastes[index][0] = replacement
				break
			}
			suffix++
		}
	}
	message.Text, message.TextElements = remapPlaceholdersInText(message.Text, message.TextElements, mapping)
	return message, pendingPastes
}

func remapUserMessagesWithHistoryRecords(messages []messageWithHistoryRecord) []messageWithHistoryRecord {
	totalRemoteImages := 0
	for _, item := range messages {
		totalRemoteImages += len(item.Message.RemoteImageURLs)
	}
	nextLabel := totalRemoteImages + 1
	out := make([]messageWithHistoryRecord, 0, len(messages))
	for _, item := range messages {
		message, record := remapPlaceholdersForMessageAndHistoryRecord(item.Message, item.Record, &nextLabel)
		out = append(out, messageWithHistoryRecord{Message: message, Record: record})
	}
	return out
}

func remapPlaceholdersForMessageAndHistoryRecord(message UserMessage, record UserMessageHistoryRecord, nextLabel *int) (UserMessage, UserMessageHistoryRecord) {
	mapping := map[string]string{}
	remappedImages := make([]string, 0, len(message.LocalImages))
	for _, path := range message.LocalImages {
		oldPlaceholder := localImageLabelText(len(remappedImages) + 1)
		newPlaceholder := localImageLabelText(*nextLabel)
		*nextLabel++
		mapping[oldPlaceholder] = newPlaceholder
		remappedImages = append(remappedImages, path)
	}
	message.Text, message.TextElements = remapPlaceholdersInText(message.Text, message.TextElements, mapping)
	message.LocalImages = remappedImages
	if record.Kind == UserMessageHistoryOverride && record.Text != "" {
		record.Text, record.TextElements = remapPlaceholdersInText(record.Text, record.TextElements, mapping)
	}
	return message, record
}

func mergeRemappedUserMessages(messages []UserMessage) UserMessage {
	combined := UserMessage{}
	for index, message := range messages {
		if index > 0 {
			combined.Text += "\n"
		}
		appendTextWithRebasedElementsString(&combined.Text, &combined.TextElements, message.Text, message.TextElements)
		combined.LocalImages = append(combined.LocalImages, message.LocalImages...)
		combined.RemoteImageURLs = append(combined.RemoteImageURLs, message.RemoteImageURLs...)
		combined.LocalAudio = append(combined.LocalAudio, message.LocalAudio...)
		combined.RemoteAudioURLs = append(combined.RemoteAudioURLs, message.RemoteAudioURLs...)
		combined.MentionBindings = append(combined.MentionBindings, message.MentionBindings...)
	}
	return combined
}

func remapPlaceholdersInText(text string, elements []turn.TextElement, mapping map[string]string) (string, []turn.TextElement) {
	if len(mapping) == 0 || len(elements) == 0 {
		return text, cloneTextElements(elements)
	}
	elements = cloneTextElements(elements)
	sort.SliceStable(elements, func(i, j int) bool {
		return elements[i].ByteRange.Start < elements[j].ByteRange.Start
	})

	cursor := 0
	var rebuilt strings.Builder
	rebuiltElements := []turn.TextElement{}
	for _, element := range elements {
		start := clampByteIndex(int(element.ByteRange.Start), len(text))
		end := clampByteIndex(int(element.ByteRange.End), len(text))
		if end < start {
			end = start
		}
		if cursor < start {
			rebuilt.WriteString(text[cursor:start])
		}
		original := text[start:end]
		placeholder := original
		if element.Placeholder != nil {
			placeholder = *element.Placeholder
		}
		replacement := original
		if value, ok := mapping[placeholder]; ok {
			replacement = value
			element.Placeholder = cloneStringPtr(value)
		}
		elemStart := rebuilt.Len()
		rebuilt.WriteString(replacement)
		elemEnd := rebuilt.Len()
		element.ByteRange.Start = uint(elemStart)
		element.ByteRange.End = uint(elemEnd)
		rebuiltElements = append(rebuiltElements, element)
		cursor = end
	}
	if cursor < len(text) {
		rebuilt.WriteString(text[cursor:])
	}
	return rebuilt.String(), rebuiltElements
}

func appendTextWithRebasedElements(builder *strings.Builder, target *[]turn.TextElement, text string, elements []turn.TextElement) {
	offset := uint(builder.Len())
	builder.WriteString(text)
	for _, element := range elements {
		element.ByteRange.Start += offset
		element.ByteRange.End += offset
		*target = append(*target, element)
	}
}

func appendTextWithRebasedElementsString(target *string, targetElements *[]turn.TextElement, text string, elements []turn.TextElement) {
	offset := uint(len(*target))
	*target += text
	for _, element := range elements {
		element.ByteRange.Start += offset
		element.ByteRange.End += offset
		*targetElements = append(*targetElements, element)
	}
}

func cloneTextElements(elements []turn.TextElement) []turn.TextElement {
	if elements == nil {
		return nil
	}
	out := make([]turn.TextElement, len(elements))
	copy(out, elements)
	for i := range out {
		if out[i].Placeholder != nil {
			value := *out[i].Placeholder
			out[i].Placeholder = &value
		}
	}
	return out
}

func cloneStringPtr(value string) *string {
	return &value
}

func clampByteIndex(value, size int) int {
	if value < 0 {
		return 0
	}
	if value > size {
		return size
	}
	return value
}

func localImageLabelText(index int) string {
	if index < 1 {
		index = 1
	}
	return "[Image #" + formatInt64(int64(index)) + "]"
}
