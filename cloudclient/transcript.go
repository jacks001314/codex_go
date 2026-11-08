package cloudclient

import (
	"context"
	"strings"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
)

// TranscriptEvent is one live update for an attached thread. Payloads are
// typed from the native Notification oneof; unsupported payloads stay opaque.
type TranscriptEvent struct {
	ThreadID    string
	EmittedAtMS *int64
	Update      TranscriptUpdate
}

type TranscriptUpdateKind int

const (
	TranscriptItemStarted TranscriptUpdateKind = iota + 1
	TranscriptItemCompleted
	TranscriptAgentMessageDelta
	TranscriptCommandOutputDelta
	TranscriptFileChangeOutputDelta
	TranscriptFileChangePatchUpdated
	TranscriptTurnStarted
	TranscriptTurnCompleted
)

// TranscriptUpdate carries exactly one decoded update. The Kind selects which
// value field is meaningful.
type TranscriptUpdate struct {
	Kind  TranscriptUpdateKind
	Item  ItemLifecycle
	Delta ItemDelta
	Patch PatchUpdate
	Turn  TurnSummary
}

type ItemDelta struct {
	ItemID string
	TurnID string
	Delta  string
}

type PatchUpdate struct {
	ItemID  string
	TurnID  string
	Changes []FileUpdate
}

type FileUpdate struct {
	Path string
	Diff string
	Kind PatchKind
}

type PatchKind struct {
	Kind     PatchChangeKind
	MovePath string
}

type PatchChangeKind int

const (
	PatchAdd PatchChangeKind = iota + 1
	PatchDelete
	PatchUpdateKind
	PatchUnknown
)

type TurnSummary struct {
	ID            string
	Status        TurnStatus
	UnknownStatus *int
	StartedAt     *int64
	CompletedAt   *int64
	DurationMS    *int64
	Error         *TurnFailure
}

type TurnStatus int

const (
	TurnCompleted TurnStatus = iota + 1
	TurnInterrupted
	TurnFailed
	TurnInProgress
)

type TurnFailure struct {
	Message           string
	AdditionalDetails *string
}

type ItemLifecycle struct {
	TurnID string
	AtMS   int64
	Item   ItemSummary
}

type ItemSummaryKind int

const (
	ItemUserMessage ItemSummaryKind = iota + 1
	ItemAgentMessage
	ItemReasoning
	ItemCommandExecution
	ItemFileChange
)

type ItemSummary struct {
	Kind      ItemSummaryKind
	User      UserMessageSummary
	Agent     AgentMessageSummary
	Reasoning ReasoningSummary
	Command   CommandSummary
	File      FileChangeSummary
}

type UserMessageSummary struct {
	ID       string
	ClientID *string
	// Text lists the text parts in order; images are absent from it.
	Text []string
	// Content keeps every part typed, in the order the server announced them.
	Content []UserMessageInput
}

type AgentMessageSummary struct {
	ID           string
	Text         string
	Questions    []AgentQuestion
	HasQuestions bool
}

type AgentQuestion struct {
	Title   string
	Options *[]string
}

type ReasoningSummary struct {
	ID      string
	Summary []string
	Content []string
}

type CommandSummary struct {
	ID               string
	Command          string
	CWD              string
	Actions          []CommandAction
	Source           CommandExecutionSource
	AggregatedOutput *string
	DurationMS       *int64
	ExitCode         *int64
	Status           ItemStatus
}

type CommandExecutionSource int

const (
	CommandSourceAgent CommandExecutionSource = iota + 1
	CommandSourceUserShell
	CommandSourceUnifiedExecStartup
	CommandSourceUnifiedExecInteraction
)

type ItemStatus struct {
	Value int
	Known bool
}

type CommandAction struct {
	Kind    CommandActionKind
	Command string
	Name    string
	Path    *string
	Query   *string
}

type CommandActionKind int

const (
	CommandActionRead CommandActionKind = iota + 1
	CommandActionListFiles
	CommandActionSearch
	CommandActionUnknown
)

type FileChangeSummary struct {
	ID      string
	Changes []FileUpdate
	Status  ItemStatus
}

// DecodeTranscriptEvent decodes one Attach notification into a typed transcript
// update (Rust TranscriptEvent::decode).
func DecodeTranscriptEvent(threadID string, payload []byte) (TranscriptEvent, bool, error) {
	return decodeTranscriptEvent(threadID, payload)
}

// ThreadStatusFor returns the supported status update this Attach notification
// carries for threadID (Rust #59493 Event::thread_status_for).
func (e Event) ThreadStatusFor(threadID string) (any, bool) {
	if e.Kind != EventNotification || strings.TrimSpace(threadID) == "" {
		return nil, false
	}
	// Field 3 of Notification.value is thread_status_changed.
	tag, values, ok, err := selectNotification(e.Payload)
	if err != nil || !ok || tag != 3 {
		return nil, false
	}
	envelope, err := decodeEnvelope(values)
	if err != nil {
		return nil, false
	}
	owner, ok := stringField(envelope.Params, 2)
	if !ok || owner != threadID {
		return nil, false
	}
	statusBytes, ok := bytesField(envelope.Params, 1)
	if !ok {
		return nil, false
	}
	status, err := decodeThreadStatus(statusBytes)
	if err != nil {
		return nil, false
	}
	return status, true
}

// AttachTranscript decodes typed transcript updates from Attach. Unknown
// notification payloads are skipped; a malformed supported payload ends the
// stream with ErrInvalidResponse.
func (c *Client) AttachTranscript(ctx context.Context, threadID string) (<-chan TranscriptEvent, <-chan error, error) {
	rawEvents, rawErrs, err := c.Attach(ctx, threadID)
	if err != nil {
		return nil, nil, err
	}
	events := make(chan TranscriptEvent)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		for {
			select {
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			case raw, ok := <-rawEvents:
				if !ok {
					return
				}
				if raw.Kind != EventNotification {
					continue
				}
				event, ok, err := decodeTranscriptEvent(threadID, raw.Payload)
				if err != nil {
					errs <- err
					return
				}
				if !ok {
					continue
				}
				select {
				case events <- event:
				case <-ctx.Done():
					errs <- ctx.Err()
					return
				}
			case err, ok := <-rawErrs:
				if ok && err != nil {
					errs <- err
				}
				return
			}
		}
	}()
	return events, errs, nil
}

func decodeTranscriptEvent(threadID string, payload []byte) (TranscriptEvent, bool, error) {
	tag, values, ok, err := selectNotification(payload)
	if err != nil || !ok {
		return TranscriptEvent{}, false, err
	}
	envelope, err := decodeEnvelope(values)
	if err != nil {
		return TranscriptEvent{}, false, err
	}
	switch tag {
	case 27, 31:
		params := envelope.Params
		itemBytes, ok := bytesField(params, boolInt(tag == 27, 1, 2))
		if !ok {
			return TranscriptEvent{}, false, nil
		}
		atMS, ok := int64Field(params, boolInt(tag == 27, 2, 1))
		if !ok {
			return TranscriptEvent{}, false, nil
		}
		turnID, ok := stringField(params, 4)
		if !ok {
			return TranscriptEvent{}, false, nil
		}
		owner, ok := stringField(params, 3)
		if !ok {
			return TranscriptEvent{}, false, nil
		}
		if owner != threadID {
			return TranscriptEvent{}, false, ErrInvalidResponse
		}
		item, ok, err := decodeItem(itemBytes)
		if err != nil || !ok {
			return TranscriptEvent{}, false, err
		}
		kind := TranscriptItemCompleted
		if tag == 27 {
			kind = TranscriptItemStarted
		}
		return TranscriptEvent{ThreadID: owner, EmittedAtMS: envelope.EmittedAtMS, Update: TranscriptUpdate{
			Kind: kind, Item: ItemLifecycle{TurnID: turnID, AtMS: atMS, Item: item},
		}}, true, nil
	case 32, 37, 39:
		delta, ok, err := decodeDelta(envelope.Params)
		if err != nil || !ok {
			return TranscriptEvent{}, false, err
		}
		owner, ok := stringField(envelope.Params, 3)
		if !ok {
			return TranscriptEvent{}, false, nil
		}
		if owner != threadID {
			return TranscriptEvent{}, false, ErrInvalidResponse
		}
		kind := TranscriptAgentMessageDelta
		if tag == 37 {
			kind = TranscriptCommandOutputDelta
		} else if tag == 39 {
			kind = TranscriptFileChangeOutputDelta
		}
		return TranscriptEvent{ThreadID: owner, EmittedAtMS: envelope.EmittedAtMS, Update: TranscriptUpdate{Kind: kind, Delta: delta}}, true, nil
	case 40:
		patch, ok, err := decodePatchUpdate(envelope.Params)
		if err != nil || !ok {
			return TranscriptEvent{}, false, err
		}
		owner, ok := stringField(envelope.Params, 3)
		if !ok {
			return TranscriptEvent{}, false, nil
		}
		if owner != threadID {
			return TranscriptEvent{}, false, ErrInvalidResponse
		}
		return TranscriptEvent{ThreadID: owner, EmittedAtMS: envelope.EmittedAtMS, Update: TranscriptUpdate{Kind: TranscriptFileChangePatchUpdated, Patch: patch}}, true, nil
	case 21, 23:
		turn, owner, ok, err := decodeTurnUpdate(envelope.Params)
		if err != nil || !ok {
			return TranscriptEvent{}, false, err
		}
		if owner != threadID {
			// Account-wide lifecycle notifications are not this thread's transcript.
			return TranscriptEvent{}, false, nil
		}
		kind := TranscriptTurnCompleted
		if tag == 21 {
			kind = TranscriptTurnStarted
		}
		return TranscriptEvent{ThreadID: owner, EmittedAtMS: envelope.EmittedAtMS, Update: TranscriptUpdate{Kind: kind, Turn: turn}}, true, nil
	default:
		return TranscriptEvent{}, false, nil
	}
}

type envelope struct {
	EmittedAtMS *int64
	Params      []byte
}

func decodeEnvelope(data []byte) (envelope, error) {
	var out envelope
	for len(data) > 0 {
		number, wireType, n := protowire.ConsumeTag(data)
		if n < 0 {
			return envelope{}, ErrInvalidResponse
		}
		data = data[n:]
		switch {
		case number == 1 && wireType == protowire.VarintType:
			value, n := protowire.ConsumeVarint(data)
			if n < 0 {
				return envelope{}, ErrInvalidResponse
			}
			v := int64(value)
			out.EmittedAtMS = &v
			data = data[n:]
		case number == 2 && wireType == protowire.BytesType:
			value, n := protowire.ConsumeBytes(data)
			if n < 0 {
				return envelope{}, ErrInvalidResponse
			}
			out.Params = append([]byte(nil), value...)
			data = data[n:]
		default:
			n := protowire.ConsumeFieldValue(number, wireType, data)
			if n < 0 {
				return envelope{}, ErrInvalidResponse
			}
			data = data[n:]
		}
	}
	if out.Params == nil {
		return envelope{}, ErrInvalidResponse
	}
	return out, nil
}

func decodeDelta(data []byte) (ItemDelta, bool, error) {
	delta, ok := stringField(data, 1)
	if !ok {
		return ItemDelta{}, false, nil
	}
	itemID, ok := stringField(data, 2)
	if !ok {
		return ItemDelta{}, false, nil
	}
	turnID, ok := stringField(data, 4)
	if !ok {
		return ItemDelta{}, false, nil
	}
	return ItemDelta{ItemID: itemID, TurnID: turnID, Delta: delta}, true, nil
}

func decodePatchUpdate(data []byte) (PatchUpdate, bool, error) {
	changesBytes, ok := bytesField(data, 1)
	if !ok {
		return PatchUpdate{}, false, nil
	}
	changes, err := decodeFileUpdates(changesBytes)
	if err != nil {
		return PatchUpdate{}, false, err
	}
	itemID, ok := stringField(data, 2)
	if !ok {
		return PatchUpdate{}, false, nil
	}
	turnID, ok := stringField(data, 4)
	if !ok {
		return PatchUpdate{}, false, nil
	}
	return PatchUpdate{ItemID: itemID, TurnID: turnID, Changes: changes}, true, nil
}

func decodeFileUpdates(data []byte) ([]FileUpdate, error) {
	updates := []FileUpdate{}
	for _, change := range repeatedBytes(data, 1) {
		diff, ok := stringField(change, 1)
		if !ok {
			return nil, ErrInvalidResponse
		}
		kindBytes, _ := bytesField(change, 2)
		kind, err := decodePatchKind(kindBytes)
		if err != nil {
			return nil, err
		}
		path, ok := stringField(change, 3)
		if !ok {
			return nil, ErrInvalidResponse
		}
		updates = append(updates, FileUpdate{Path: path, Diff: diff, Kind: kind})
	}
	return updates, nil
}

func decodePatchKind(data []byte) (PatchKind, error) {
	if len(data) == 0 {
		return PatchKind{Kind: PatchUnknown}, nil
	}
	tag, value, err := selectOneof(data, 1, 3)
	if err != nil || tag == 0 {
		return PatchKind{Kind: PatchUnknown}, err
	}
	switch tag {
	case 1:
		return PatchKind{Kind: PatchAdd}, nil
	case 2:
		return PatchKind{Kind: PatchDelete}, nil
	case 3:
		movePath, _ := stringField(value, 1)
		return PatchKind{Kind: PatchUpdateKind, MovePath: movePath}, nil
	default:
		return PatchKind{Kind: PatchUnknown}, nil
	}
}

func decodeTurnUpdate(data []byte) (TurnSummary, string, bool, error) {
	owner, ok := stringField(data, 1)
	if !ok {
		return TurnSummary{}, "", false, nil
	}
	turnBytes, ok := bytesField(data, 2)
	if !ok {
		return TurnSummary{}, "", false, nil
	}
	summary, err := decodeTurn(turnBytes)
	if err != nil {
		return TurnSummary{}, "", false, err
	}
	return summary, owner, true, nil
}

func decodeTurn(data []byte) (TurnSummary, error) {
	id, ok := stringField(data, 4)
	if !ok {
		return TurnSummary{}, ErrInvalidResponse
	}
	status, ok := int32Field(data, 8)
	if !ok {
		return TurnSummary{}, ErrInvalidResponse
	}
	summary := TurnSummary{ID: id, Status: TurnStatus(status)}
	if status < 1 || status > 4 {
		summary.Status = 0
		unknown := int(status)
		summary.UnknownStatus = &unknown
	}
	summary.StartedAt = optionalInt64Field(data, 7)
	summary.CompletedAt = optionalInt64Field(data, 1)
	summary.DurationMS = optionalInt64Field(data, 2)
	if errorBytes, ok := bytesField(data, 3); ok {
		message, ok := stringField(errorBytes, 3)
		if !ok {
			return TurnSummary{}, ErrInvalidResponse
		}
		failure := &TurnFailure{Message: message}
		if details, ok := optionalStringField(errorBytes, 1); ok {
			failure.AdditionalDetails = &details
		}
		summary.Error = failure
	}
	return summary, nil
}

func decodeItem(data []byte) (ItemSummary, bool, error) {
	tag, value, err := selectOneof(data, 1, 19)
	if err != nil || tag == 0 {
		return ItemSummary{}, false, nil
	}
	switch tag {
	case 1:
		return decodeUserMessageSummary(value)
	case 3:
		return decodeAgentMessageSummary(value)
	case 6:
		return decodeReasoningSummary(value)
	case 7:
		return decodeCommandSummary(value)
	case 8:
		return decodeFileChangeSummary(value)
	default:
		return ItemSummary{}, false, nil
	}
}

func decodeUserMessageSummary(data []byte) (ItemSummary, bool, error) {
	clientIDValue, hasClientID := optionalStringField(data, 1)
	var clientID *string
	if hasClientID {
		clientID = &clientIDValue
	}
	contentBytes, ok := bytesField(data, 2)
	if !ok {
		return ItemSummary{}, false, nil
	}
	id, ok := stringField(data, 3)
	if !ok {
		return ItemSummary{}, false, nil
	}
	texts := []string{}
	content := []UserMessageInput{}
	for _, input := range repeatedBytes(contentBytes, 1) {
		kind, value, err := selectOneof(input, 1, 7)
		if err != nil {
			return ItemSummary{}, false, err
		}
		switch kind {
		case 1:
			text, ok := stringField(value, 1)
			if !ok {
				return ItemSummary{}, false, nil
			}
			elements, ok := decodeUserMessageTextElements(value, text)
			if !ok {
				return ItemSummary{}, false, nil
			}
			content = append(content, UserMessageInput{Kind: UserMessageText, Text: text, TextElements: elements})
			texts = append(texts, text)
		case 2:
			image, detail, ok := decodeUserMessageImage(value)
			if !ok {
				return ItemSummary{}, false, nil
			}
			content = append(content, UserMessageInput{Kind: UserMessageImage, Image: image, Detail: detail})
		default:
			// Rust keeps only text and image parts; on any other alternative the
			// whole summary is dropped rather than flattened to text.
			return ItemSummary{}, false, nil
		}
	}
	return ItemSummary{Kind: ItemUserMessage, User: UserMessageSummary{ID: id, ClientID: clientID, Text: texts, Content: content}}, true, nil
}

// decodeUserMessageTextElements reads the placeholder spans of one text part and
// validates them against the text they describe (Rust items::summary).
func decodeUserMessageTextElements(data []byte, text string) ([]UserMessageTextElement, bool) {
	encoded, ok := bytesField(data, 2)
	if !ok {
		return nil, true
	}
	elements := []UserMessageTextElement{}
	for _, element := range repeatedBytes(encoded, 1) {
		byteRange, ok := bytesField(element, 1)
		if !ok {
			return nil, false
		}
		end, ok := int64Field(byteRange, 1)
		if !ok {
			return nil, false
		}
		start, ok := int64Field(byteRange, 2)
		if !ok {
			return nil, false
		}
		if start < 0 || end < start || end > int64(len(text)) || !utf8.ValidString(text[start:end]) {
			return nil, false
		}
		decoded := UserMessageTextElement{Start: int(start), End: int(end)}
		if placeholder, ok := optionalStringField(element, 2); ok {
			value := placeholder
			decoded.Placeholder = &value
		}
		elements = append(elements, decoded)
	}
	return elements, true
}

// decodeUserMessageImage reads one image part. Unknown detail values keep the
// receipt identity without a detail (Rust items::summary).
func decodeUserMessageImage(data []byte) (ImageReference, *ImageDetail, bool) {
	source, reference, err := selectOneof(data, 1, 2)
	if err != nil || source == 0 {
		return ImageReference{}, nil, false
	}
	value, ok := stringField(reference, 2)
	if !ok {
		return ImageReference{}, nil, false
	}
	var image ImageReference
	if source == 2 {
		image.FileID = value
	} else {
		image.URL = value
	}
	var detail *ImageDetail
	if encoded := optionalInt64Field(reference, 1); encoded != nil {
		if mapped, known := imageDetailFromWire(*encoded); known {
			detail = &mapped
		}
	}
	return image, detail, true
}

// imageDetailFromWire maps Rust's ImageDetail discriminants onto the Go enum.
func imageDetailFromWire(value int64) (ImageDetail, bool) {
	switch value {
	case 1:
		return ImageDetailAuto, true
	case 2:
		return ImageDetailLow, true
	case 3:
		return ImageDetailHigh, true
	case 4:
		return ImageDetailOriginal, true
	default:
		return 0, false
	}
}

func decodeAgentMessageSummary(data []byte) (ItemSummary, bool, error) {
	id, ok := stringField(data, 2)
	if !ok {
		return ItemSummary{}, false, nil
	}
	text, ok := stringField(data, 6)
	if !ok {
		return ItemSummary{}, false, nil
	}
	questions := []AgentQuestion{}
	hasQuestions := false
	if questionBytes, ok := bytesField(data, 5); ok {
		hasQuestions = true
		for _, question := range repeatedBytes(questionBytes, 1) {
			title, ok := stringField(question, 2)
			if !ok {
				return ItemSummary{}, false, nil
			}
			entry := AgentQuestion{Title: title}
			if optionsBytes, ok := bytesField(question, 1); ok {
				values := repeatedStrings(optionsBytes, 1)
				entry.Options = &values
			}
			questions = append(questions, entry)
		}
	}
	return ItemSummary{Kind: ItemAgentMessage, Agent: AgentMessageSummary{ID: id, Text: text, Questions: questions, HasQuestions: hasQuestions}}, true, nil
}

func decodeReasoningSummary(data []byte) (ItemSummary, bool, error) {
	id, ok := stringField(data, 2)
	if !ok {
		return ItemSummary{}, false, nil
	}
	summary := []string{}
	if value, ok := bytesField(data, 3); ok {
		summary = repeatedStrings(value, 1)
	}
	content := []string{}
	if value, ok := bytesField(data, 1); ok {
		content = repeatedStrings(value, 1)
	}
	return ItemSummary{Kind: ItemReasoning, Reasoning: ReasoningSummary{ID: id, Summary: summary, Content: content}}, true, nil
}

func decodeCommandSummary(data []byte) (ItemSummary, bool, error) {
	id, ok := stringField(data, 7)
	if !ok {
		return ItemSummary{}, false, nil
	}
	command, ok := stringField(data, 2)
	if !ok {
		return ItemSummary{}, false, nil
	}
	cwd, ok := stringField(data, 4)
	if !ok {
		return ItemSummary{}, false, nil
	}
	actionsBytes, ok := bytesField(data, 3)
	if !ok {
		return ItemSummary{}, false, nil
	}
	status, ok := int32Field(data, 12)
	if !ok {
		return ItemSummary{}, false, nil
	}
	source, hasSource := int32Field(data, 11)
	if hasSource && (source < 1 || source > 4) {
		return ItemSummary{}, false, nil
	}
	if !hasSource {
		source = 1
	}
	actions := []CommandAction{}
	for _, action := range repeatedBytes(actionsBytes, 1) {
		decoded, ok, err := decodeCommandAction(action)
		if err != nil || !ok {
			return ItemSummary{}, false, err
		}
		actions = append(actions, decoded)
	}
	outputValue, hasOutput := optionalStringField(data, 1)
	var output *string
	if hasOutput {
		output = &outputValue
	}
	summary := CommandSummary{
		ID: id, Command: command, CWD: cwd, Actions: actions,
		Source: CommandExecutionSource(source), AggregatedOutput: output,
		DurationMS: optionalInt64Field(data, 5), ExitCode: optionalInt64Field(data, 6),
		Status: itemStatus(status),
	}
	return ItemSummary{Kind: ItemCommandExecution, Command: summary}, true, nil
}

func decodeCommandAction(data []byte) (CommandAction, bool, error) {
	tag, value, err := selectOneof(data, 1, 4)
	if err != nil || tag == 0 {
		return CommandAction{}, false, err
	}
	command, ok := stringField(value, 1)
	if !ok {
		return CommandAction{}, false, nil
	}
	switch tag {
	case 1:
		name, ok := stringField(value, 2)
		if !ok {
			return CommandAction{}, false, nil
		}
		path, ok := stringField(value, 3)
		if !ok {
			return CommandAction{}, false, nil
		}
		return CommandAction{Kind: CommandActionRead, Command: command, Name: name, Path: &path}, true, nil
	case 2:
		var path *string
		if pathValue, ok := optionalStringField(value, 2); ok {
			path = &pathValue
		}
		return CommandAction{Kind: CommandActionListFiles, Command: command, Path: path}, true, nil
	case 3:
		var queryPtr, pathPtr *string
		if queryValue, ok := optionalStringField(value, 3); ok {
			queryPtr = &queryValue
		}
		if pathValue, ok := optionalStringField(value, 2); ok {
			pathPtr = &pathValue
		}
		return CommandAction{Kind: CommandActionSearch, Command: command, Query: queryPtr, Path: pathPtr}, true, nil
	default:
		return CommandAction{Kind: CommandActionUnknown, Command: command}, true, nil
	}
}

func decodeFileChangeSummary(data []byte) (ItemSummary, bool, error) {
	changesBytes, ok := bytesField(data, 1)
	if !ok {
		return ItemSummary{}, false, nil
	}
	id, ok := stringField(data, 2)
	if !ok {
		return ItemSummary{}, false, nil
	}
	status, ok := int32Field(data, 3)
	if !ok {
		return ItemSummary{}, false, nil
	}
	changes, err := decodeFileUpdates(changesBytes)
	if err != nil {
		return ItemSummary{}, false, err
	}
	return ItemSummary{Kind: ItemFileChange, File: FileChangeSummary{ID: id, Changes: changes, Status: itemStatus(status)}}, true, nil
}

func itemStatus(value int32) ItemStatus {
	if value >= 1 && value <= 4 {
		return ItemStatus{Value: int(value), Known: true}
	}
	return ItemStatus{Value: int(value)}
}

func selectNotification(data []byte) (int, []byte, bool, error) {
	selectedTag := 0
	var selected []byte
	for len(data) > 0 {
		number, wireType, n := protowire.ConsumeTag(data)
		if n < 0 {
			return 0, nil, false, ErrInvalidResponse
		}
		data = data[n:]
		if number >= 1 && number <= 89 && wireType == protowire.BytesType {
			value, n := protowire.ConsumeBytes(data)
			if n < 0 {
				return 0, nil, false, ErrInvalidResponse
			}
			selectedTag = int(number)
			selected = append(selected[:0], value...)
			data = data[n:]
			continue
		}
		n = protowire.ConsumeFieldValue(number, wireType, data)
		if n < 0 {
			return 0, nil, false, ErrInvalidResponse
		}
		data = data[n:]
	}
	return selectedTag, selected, selectedTag != 0, nil
}

func selectOneof(data []byte, minTag int, maxTag int) (int, []byte, error) {
	selectedTag := 0
	var selected []byte
	for len(data) > 0 {
		number, wireType, n := protowire.ConsumeTag(data)
		if n < 0 {
			return 0, nil, ErrInvalidResponse
		}
		data = data[n:]
		if number >= protowire.Number(minTag) && number <= protowire.Number(maxTag) && wireType == protowire.BytesType {
			value, n := protowire.ConsumeBytes(data)
			if n < 0 {
				return 0, nil, ErrInvalidResponse
			}
			selectedTag = int(number)
			selected = append([]byte(nil), value...)
			data = data[n:]
			continue
		}
		n = protowire.ConsumeFieldValue(number, wireType, data)
		if n < 0 {
			return 0, nil, ErrInvalidResponse
		}
		data = data[n:]
	}
	return selectedTag, selected, nil
}

func bytesField(data []byte, want int) ([]byte, bool) {
	var out []byte
	found := false
	for len(data) > 0 {
		number, wireType, n := protowire.ConsumeTag(data)
		if n < 0 {
			return nil, false
		}
		data = data[n:]
		if int(number) == want && wireType == protowire.BytesType {
			value, n := protowire.ConsumeBytes(data)
			if n < 0 {
				return nil, false
			}
			out = append([]byte(nil), value...)
			found = true
			data = data[n:]
			continue
		}
		n = protowire.ConsumeFieldValue(number, wireType, data)
		if n < 0 {
			return nil, false
		}
		data = data[n:]
	}
	return out, found
}

func stringField(data []byte, want int) (string, bool) {
	value, ok := bytesField(data, want)
	if !ok {
		return "", false
	}
	return string(value), true
}

func optionalStringField(data []byte, want int) (string, bool) {
	return stringField(data, want)
}

func int64Field(data []byte, want int) (int64, bool) {
	value := optionalInt64Field(data, want)
	if value == nil {
		return 0, false
	}
	return *value, true
}

func optionalInt64Field(data []byte, want int) *int64 {
	var out *int64
	for len(data) > 0 {
		number, wireType, n := protowire.ConsumeTag(data)
		if n < 0 {
			return nil
		}
		data = data[n:]
		if int(number) == want && wireType == protowire.VarintType {
			value, n := protowire.ConsumeVarint(data)
			if n < 0 {
				return nil
			}
			v := int64(value)
			out = &v
			data = data[n:]
			continue
		}
		n = protowire.ConsumeFieldValue(number, wireType, data)
		if n < 0 {
			return nil
		}
		data = data[n:]
	}
	return out
}

func int32Field(data []byte, want int) (int32, bool) {
	value := optionalInt64Field(data, want)
	if value == nil {
		return 0, false
	}
	return int32(*value), true
}

func repeatedBytes(data []byte, want int) [][]byte {
	out := [][]byte{}
	for len(data) > 0 {
		number, wireType, n := protowire.ConsumeTag(data)
		if n < 0 {
			return out
		}
		data = data[n:]
		if int(number) == want && wireType == protowire.BytesType {
			value, n := protowire.ConsumeBytes(data)
			if n < 0 {
				return out
			}
			out = append(out, append([]byte(nil), value...))
			data = data[n:]
			continue
		}
		n = protowire.ConsumeFieldValue(number, wireType, data)
		if n < 0 {
			return out
		}
		data = data[n:]
	}
	return out
}

func repeatedStrings(data []byte, want int) []string {
	values := repeatedBytes(data, want)
	out := make([]string, 0, len(values))
	for _, value := range values {
		out = append(out, string(value))
	}
	return out
}

func boolInt(condition bool, whenTrue int, whenFalse int) int {
	if condition {
		return whenTrue
	}
	return whenFalse
}

// TurnStatusUnknownValue returns the raw status when it is outside the known
// enum values.
func (s TurnStatus) UnknownValue() (int, bool) {
	value := int(s)
	if value < 1 || value > 4 {
		return value, value != 0
	}
	return 0, false
}
