package cloudclient

import (
	"context"
	"errors"

	"google.golang.org/protobuf/encoding/protowire"
)

const (
	threadServiceAddUserMessageMethod = "/oaiproto.codex_app_server_backend.ThreadService/AddUserMessage"
	threadServiceInterruptTurnMethod  = "/oaiproto.codex_app_server_backend.ThreadService/InterruptTurn"
)

type SubmissionMode int

const (
	SubmissionSteer SubmissionMode = iota + 1
	SubmissionQueue
	SubmissionStartIfIdle
)

// AddUserMessageRequest is plain text using the thread's existing configuration.
// Persist the message ID before calling and never reuse it for another submission.
type AddUserMessageRequest struct {
	ThreadID            string
	ClientUserMessageID string
	Text                string
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
	if request.ThreadID == "" || request.ClientUserMessageID == "" || request.Text == "" {
		return nil, &MutationError{Kind: MutationInvalidRequest}
	}
	encoded := encodeAddUserMessageRequest(request)
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

func encodeAddUserMessageRequest(request AddUserMessageRequest) []byte {
	text := protowire.AppendTag(nil, 1, protowire.BytesType)
	text = protowire.AppendString(text, request.Text)
	userInput := protowire.AppendTag(nil, 1, protowire.BytesType)
	userInput = protowire.AppendBytes(userInput, text)
	input := protowire.AppendTag(nil, 1, protowire.BytesType)
	input = protowire.AppendBytes(input, userInput)

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
