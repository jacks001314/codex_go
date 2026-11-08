package cloudclient

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	"google.golang.org/protobuf/encoding/protowire"
)

const (
	threadServiceAddUserMessageMethod = "/oaiproto.codex_app_server_backend.ThreadService/AddUserMessage"
	threadServiceInterruptTurnMethod  = "/oaiproto.codex_app_server_backend.ThreadService/InterruptTurn"
)

// maxImageMessageBytes mirrors Rust's MAX_IMAGE_MESSAGE_BYTES: CCA forwards the
// envelope through a 4 MiB boundary and reserves 256 KiB for forwarding.
const maxImageMessageBytes = 4*1024*1024 - 256*1024

type SubmissionMode int

const (
	SubmissionSteer SubmissionMode = iota + 1
	SubmissionQueue
	SubmissionStartIfIdle
)

// UserMessageInputKind selects one ordered part of a user message.
type UserMessageInputKind int

const (
	// UserMessageText is a text part with optional placeholder elements.
	UserMessageText UserMessageInputKind = iota + 1
	// UserMessageImage is an inline data URL or an uploaded file reference.
	UserMessageImage
)

// ImageDetail is the server-side rendering detail of one image part. The wire
// values are Rust's ImageDetail discriminants.
type ImageDetail int

const (
	ImageDetailAuto ImageDetail = iota + 1
	ImageDetailLow
	ImageDetailHigh
	ImageDetailOriginal
)

// String renders the detail the way the app-server thread item payload spells it.
func (d ImageDetail) String() string {
	switch d {
	case ImageDetailLow:
		return "low"
	case ImageDetailHigh:
		return "high"
	case ImageDetailOriginal:
		return "original"
	default:
		return "auto"
	}
}

// ImageReference is one image source. FileID takes precedence when both fields
// are set: an uploaded file reference carries no URL (Rust ImageReference).
// HTTP image URLs are not supported by the native cloud API.
type ImageReference struct {
	URL    string
	FileID string
}

func (r ImageReference) value() string {
	if r.FileID != "" {
		return r.FileID
	}
	return r.URL
}

// UserMessageTextElement is a placeholder span measured in UTF-8 bytes of the
// text part it belongs to (Rust UserMessageTextElement).
type UserMessageTextElement struct {
	Start       int
	End         int
	Placeholder *string
}

// UserMessageInput is one ordered part of one logical user message. Rust's
// AddUserMessageRequest carries the same `Vec<UserMessageInput>`.
type UserMessageInput struct {
	Kind         UserMessageInputKind
	Text         string
	TextElements []UserMessageTextElement
	Image        ImageReference
	Detail       *ImageDetail
}

// AddUserMessageRequest sends text and image parts using the thread's existing
// configuration. Persist the message ID before calling and never reuse it for
// another submission.
type AddUserMessageRequest struct {
	ThreadID            string
	ClientUserMessageID string
	// Input is the ordered content of one logical user message.
	Input []UserMessageInput
	// TurnTrigger is the caller-supplied classification for this turn.
	TurnTrigger  *string
	Mode         SubmissionMode
	SharedAccess bool
}

type InterruptTurnRequest struct {
	ThreadID     string
	TurnID       string
	SharedAccess bool
}

type AdmissionOutcomeKind int

const (
	AdmissionStarted AdmissionOutcomeKind = iota + 1
	AdmissionSteered
	AdmissionDuplicate
	AdmissionQueued
)

type AdmissionOutcome struct {
	Kind         AdmissionOutcomeKind
	Turn         *TurnSummary
	TurnID       *string
	SubmissionID string
}

type Admission struct {
	ThreadID            string
	ClientUserMessageID string
	Outcome             AdmissionOutcome
	Payload             []byte
}

type MutationErrorKind int

const (
	MutationInvalidRequest MutationErrorKind = iota + 1
	MutationOutcomeUnknown
)

type MutationError struct {
	Kind MutationErrorKind
	Err  error
}

func (e *MutationError) Error() string {
	if e == nil {
		return ""
	}
	if e.Kind == MutationInvalidRequest {
		return "invalid or oversized mutation request; nothing was sent"
	}
	return "mutation outcome unknown; do not automatically retry"
}

func (e *MutationError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// AddUserMessage sends once. Success means admission, not completion. A
// transport failure has an unknown outcome and must not be retried blindly.
func (c *Client) AddUserMessage(ctx context.Context, request AddUserMessageRequest) (*Admission, error) {
	if c == nil || c.conn == nil {
		return nil, &MutationError{Kind: MutationOutcomeUnknown, Err: ErrClientBuild}
	}
	if !validAddUserMessageRequest(request) {
		return nil, &MutationError{Kind: MutationInvalidRequest}
	}
	encoded := encodeAddUserMessageRequest(request)
	if hasImageInput(request.Input) && len(encoded) > maxImageMessageBytes {
		return nil, &MutationError{Kind: MutationInvalidRequest}
	}
	if len(encoded) > maxMessageBytes {
		return nil, &MutationError{Kind: MutationInvalidRequest}
	}
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var response rawMessage
	if err := c.conn.Invoke(c.outgoingContext(requestCtx), threadServiceAddUserMessageMethod, rawMessage(encoded), &response); err != nil {
		return nil, &MutationError{Kind: MutationOutcomeUnknown, Err: mapCallError(requestCtx, err)}
	}
	outcome, err := decodeAdmission(request.ClientUserMessageID, response)
	if err != nil {
		return nil, &MutationError{Kind: MutationOutcomeUnknown, Err: ErrInvalidResponse}
	}
	return &Admission{
		ThreadID: request.ThreadID, ClientUserMessageID: request.ClientUserMessageID,
		Outcome: outcome, Payload: append([]byte(nil), response...),
	}, nil
}

// InterruptTurn interrupts only the requested turn. Observe Attach for its final status.
func (c *Client) InterruptTurn(ctx context.Context, request InterruptTurnRequest) error {
	if c == nil || c.conn == nil {
		return &MutationError{Kind: MutationOutcomeUnknown, Err: ErrClientBuild}
	}
	if request.ThreadID == "" || request.TurnID == "" {
		return &MutationError{Kind: MutationInvalidRequest}
	}
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var response rawMessage
	if err := c.conn.Invoke(c.outgoingContext(requestCtx), threadServiceInterruptTurnMethod, rawMessage(encodeInterruptTurnRequest(request)), &response); err != nil {
		return &MutationError{Kind: MutationOutcomeUnknown, Err: mapCallError(requestCtx, err)}
	}
	// The response is a ResumeResponse envelope. An empty message is a valid
	// default response; malformed bytes leave the outcome unacknowledged.
	if err := validateProtoMessage(response); err != nil {
		return &MutationError{Kind: MutationOutcomeUnknown, Err: ErrInvalidResponse}
	}
	return nil
}

// validAddUserMessageRequest mirrors Rust's add_user_message preflight: at
// least one non-blank text part or one image, valid placeholder ranges, and an
// inline image that really is a data: URL or a non-blank file ID.
func validAddUserMessageRequest(request AddUserMessageRequest) bool {
	if strings.TrimSpace(request.ThreadID) == "" || strings.TrimSpace(request.ClientUserMessageID) == "" {
		return false
	}
	hasContent := false
	for _, input := range request.Input {
		switch input.Kind {
		case UserMessageText:
			if strings.TrimSpace(input.Text) != "" {
				hasContent = true
			}
			for _, element := range input.TextElements {
				if element.Start < 0 || element.End < element.Start || element.End > len(input.Text) ||
					!utf8.ValidString(input.Text[element.Start:element.End]) {
					return false
				}
			}
		case UserMessageImage:
			hasContent = true
			if input.Image.FileID != "" {
				if strings.TrimSpace(input.Image.FileID) == "" {
					return false
				}
				continue
			}
			if !validInlineImageURL(input.Image.URL) {
				return false
			}
		default:
			return false
		}
	}
	return hasContent
}

// validInlineImageURL accepts only a data: URL with a non-blank payload.
func validInlineImageURL(value string) bool {
	prefix, payload, ok := strings.Cut(value, ",")
	if !ok || len(prefix) < len("data:") || !strings.EqualFold(prefix[:len("data:")], "data:") {
		return false
	}
	return strings.TrimSpace(payload) != ""
}

func hasImageInput(inputs []UserMessageInput) bool {
	for _, input := range inputs {
		if input.Kind == UserMessageImage {
			return true
		}
	}
	return false
}

// encodeUserInputs renders the ordered parts shared by the AddRequest input and
// a user-message item's content: one `Input`-shaped body, i.e. one field-1 entry
// per part, each holding one `UserInput` (Rust turns_wire::UserInput, tags 1..7;
// this client emits the text and image alternatives).
func encodeUserInputs(inputs []UserMessageInput) []byte {
	var out []byte
	for _, input := range inputs {
		if input.Kind == UserMessageImage {
			var reference []byte
			if input.Detail != nil {
				reference = appendProtoVarint(reference, 1, int64(*input.Detail))
			}
			reference = appendProtoString(reference, 2, input.Image.value())
			source := int64(1)
			if input.Image.FileID != "" {
				source = 2
			}
			image := appendProtoBytes(nil, source, reference)
			part := appendProtoBytes(nil, 2, image)
			out = appendProtoBytes(out, 1, part)
			continue
		}
		text := appendProtoString(nil, 1, input.Text)
		if len(input.TextElements) > 0 {
			var elements []byte
			for _, element := range input.TextElements {
				var byteRange []byte
				byteRange = appendProtoVarint(byteRange, 1, int64(element.End))
				byteRange = appendProtoVarint(byteRange, 2, int64(element.Start))
				entry := appendProtoBytes(nil, 1, byteRange)
				if element.Placeholder != nil {
					entry = appendProtoString(entry, 2, *element.Placeholder)
				}
				elements = appendProtoBytes(elements, 1, entry)
			}
			text = appendProtoBytes(text, 2, elements)
		}
		out = appendProtoBytes(out, 1, appendProtoBytes(nil, 1, text))
	}
	return out
}

func appendProtoVarint(dst []byte, tag int64, value int64) []byte {
	dst = protowire.AppendTag(dst, protowire.Number(tag), protowire.VarintType)
	return protowire.AppendVarint(dst, uint64(value))
}

func appendProtoBytes(dst []byte, tag int64, value []byte) []byte {
	dst = protowire.AppendTag(dst, protowire.Number(tag), protowire.BytesType)
	return protowire.AppendBytes(dst, value)
}

func appendProtoString(dst []byte, tag int64, value string) []byte {
	dst = protowire.AppendTag(dst, protowire.Number(tag), protowire.BytesType)
	return protowire.AppendString(dst, value)
}

func encodeAddUserMessageRequest(request AddUserMessageRequest) []byte {
	input := encodeUserInputs(request.Input)

	out := protowire.AppendTag(nil, 4, protowire.BytesType)
	out = protowire.AppendString(out, request.ClientUserMessageID)
	out = protowire.AppendTag(out, 11, protowire.BytesType)
	out = protowire.AppendBytes(out, input)
	out = protowire.AppendTag(out, 23, protowire.BytesType)
	out = protowire.AppendString(out, request.ThreadID)
	if request.TurnTrigger != nil && *request.TurnTrigger != "" {
		out = protowire.AppendTag(out, 25, protowire.BytesType)
		out = protowire.AppendString(out, *request.TurnTrigger)
	}
	if request.SharedAccess {
		out = protowire.AppendTag(out, 26, protowire.VarintType)
		out = protowire.AppendVarint(out, 1)
	}
	mode := int(request.Mode)
	if mode == 0 {
		mode = int(SubmissionStartIfIdle)
	}
	out = protowire.AppendTag(out, 30, protowire.VarintType)
	out = protowire.AppendVarint(out, uint64(mode))
	return out
}

func encodeInterruptTurnRequest(request InterruptTurnRequest) []byte {
	out := protowire.AppendTag(nil, 1, protowire.BytesType)
	out = protowire.AppendString(out, request.ThreadID)
	out = protowire.AppendTag(out, 2, protowire.BytesType)
	out = protowire.AppendString(out, request.TurnID)
	if request.SharedAccess {
		out = protowire.AppendTag(out, 3, protowire.VarintType)
		out = protowire.AppendVarint(out, 1)
	}
	return out
}

func decodeAdmission(messageID string, payload []byte) (AdmissionOutcome, error) {
	responseID, ok := stringField(payload, 3)
	if !ok || responseID != messageID {
		return AdmissionOutcome{}, ErrInvalidResponse
	}
	outcome, ok := int32Field(payload, 2)
	if !ok {
		return AdmissionOutcome{}, ErrInvalidResponse
	}
	var turn *TurnSummary
	if turnBytes, ok := bytesField(payload, 1); ok {
		decoded, err := decodeTurn(turnBytes)
		if err != nil || decoded.ID == "" {
			return AdmissionOutcome{}, ErrInvalidResponse
		}
		turn = &decoded
	}
	switch outcome {
	case 1:
		if turn == nil {
			return AdmissionOutcome{}, ErrInvalidResponse
		}
		return AdmissionOutcome{Kind: AdmissionStarted, Turn: turn}, nil
	case 2:
		if turn == nil {
			return AdmissionOutcome{}, ErrInvalidResponse
		}
		return AdmissionOutcome{Kind: AdmissionSteered, Turn: turn}, nil
	case 3:
		var turnID *string
		if turn != nil {
			value := turn.ID
			turnID = &value
		}
		return AdmissionOutcome{Kind: AdmissionDuplicate, TurnID: turnID}, nil
	case 4:
		queuedBytes, ok := bytesField(payload, 5)
		if !ok {
			return AdmissionOutcome{}, ErrInvalidResponse
		}
		submissionID, ok := stringField(queuedBytes, 1)
		if !ok || submissionID == "" {
			return AdmissionOutcome{}, ErrInvalidResponse
		}
		queuedClientID, ok := stringField(queuedBytes, 3)
		if !ok || queuedClientID != messageID {
			return AdmissionOutcome{}, ErrInvalidResponse
		}
		return AdmissionOutcome{Kind: AdmissionQueued, SubmissionID: submissionID}, nil
	default:
		return AdmissionOutcome{}, ErrInvalidResponse
	}
}

func validateProtoMessage(data []byte) error {
	for len(data) > 0 {
		number, wireType, n := protowire.ConsumeTag(data)
		if n < 0 {
			return ErrInvalidResponse
		}
		data = data[n:]
		n = protowire.ConsumeFieldValue(number, wireType, data)
		if n < 0 {
			return ErrInvalidResponse
		}
		data = data[n:]
	}
	return nil
}

// IsMutationOutcomeUnknown reports whether a mutation may have reached the server.
func IsMutationOutcomeUnknown(err error) bool {
	var mutationErr *MutationError
	return errors.As(err, &mutationErr) && mutationErr.Kind == MutationOutcomeUnknown
}
