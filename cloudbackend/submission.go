package cloudbackend

import (
	"errors"
	"fmt"
	"reflect"
	"strings"

	"codex_go/appserver"
	"codex_go/cloudclient"
	"codex_go/turn"
)

// CloudUserMessageReceiptKind identifies one server admission outcome. A
// receipt is not a completion claim: a duplicate carries identity only.
type CloudUserMessageReceiptKind string

const (
	CloudUserMessageStarted   CloudUserMessageReceiptKind = "started"
	CloudUserMessageSteered   CloudUserMessageReceiptKind = "steered"
	CloudUserMessageDuplicate CloudUserMessageReceiptKind = "duplicate"
)

// CloudUserMessageReceipt is the typed result of one stable Cloud submission.
type CloudUserMessageReceipt struct {
	Kind   CloudUserMessageReceiptKind `json:"kind"`
	Turn   *appserver.Turn             `json:"turn,omitempty"`
	TurnID *string                     `json:"turnId,omitempty"`
}

type preflightRejection struct {
	message string
}

func (e *preflightRejection) Error() string {
	if e == nil {
		return ""
	}
	return e.message
}

func rejectCloudInput(format string, args ...any) error {
	return &preflightRejection{message: fmt.Sprintf(format, args...)}
}

func typedSubmissionError(err error) *RPCError {
	if err == nil {
		return nil
	}
	code := internalErrorCode
	var preflight *preflightRejection
	var mutation *cloudclient.MutationError
	if errors.As(err, &preflight) || (errors.As(err, &mutation) && mutation.Kind == cloudclient.MutationInvalidRequest) {
		code = invalidParamsCode
	}
	return &RPCError{Code: code, Message: err.Error()}
}

// cloudMessage maps the supported subset of a Cloud turn start onto the native
// admission request. Plain text is the only accepted input.
func cloudMessage(params turn.TurnStartParams) (cloudclient.AddUserMessageRequest, error) {
	threadID := strings.TrimSpace(params.ThreadID)
	submissionID := strings.TrimSpace(params.ClientUserMessageID)
	if threadID == "" {
		return cloudclient.AddUserMessageRequest{}, rejectCloudInput("A cloud message needs a thread ID.")
	}
	if submissionID == "" {
		return cloudclient.AddUserMessageRequest{}, rejectCloudInput("A cloud message needs a stable submission ID.")
	}
	input := append([]turn.TurnUserInput(nil), params.Input...)
	if len(input) == 0 && params.Prompt != "" {
		input = []turn.TurnUserInput{{Type: "text", Text: params.Prompt}}
	}
	trigger := strings.TrimSpace(params.TurnTrigger)
	normalized := params
	normalized.ThreadID = ""
	normalized.ClientUserMessageID = ""
	normalized.Input = nil
	normalized.Prompt = ""
	normalized.TurnTrigger = ""
	if !reflect.DeepEqual(normalized, turn.TurnStartParams{}) {
		return cloudclient.AddUserMessageRequest{}, rejectCloudInput("Changing cloud settings or sending structured input is not supported yet.")
	}
	parts := make([]string, 0, len(input))
	for _, item := range input {
		switch {
		case (item.Type == "" || item.Type == "text") && len(item.TextElements) == 0:
			parts = append(parts, item.Text)
		case item.Type == "text":
			return cloudclient.AddUserMessageRequest{}, rejectCloudInput("Cloud conversations currently accept plain text only.")
		default:
			return cloudclient.AddUserMessageRequest{}, rejectCloudInput("Cloud conversations currently accept plain text only.")
		}
	}
	text := strings.Join(parts, "\n")
	if text == "" {
		return cloudclient.AddUserMessageRequest{}, rejectCloudInput("Cloud conversations currently accept plain text only.")
	}
	var turnTrigger *string
	if trigger != "" {
		value := trigger
		turnTrigger = &value
	}
	return cloudclient.AddUserMessageRequest{
		ThreadID:            threadID,
		ClientUserMessageID: submissionID,
		Text:                text,
		TurnTrigger:         turnTrigger,
		Mode:                cloudclient.SubmissionStartIfIdle,
		SharedAccess:        false,
	}, nil
}

func admittedReceipt(outcome cloudclient.AdmissionOutcome) (CloudUserMessageReceipt, error) {
	switch outcome.Kind {
	case cloudclient.AdmissionStarted, cloudclient.AdmissionSteered:
		if outcome.Turn == nil {
			return CloudUserMessageReceipt{}, errors.New("The cloud returned an unsupported admission receipt.")
		}
		value, err := cloudTurn(*outcome.Turn)
		if err != nil {
			return CloudUserMessageReceipt{}, err
		}
		kind := CloudUserMessageStarted
		if outcome.Kind == cloudclient.AdmissionSteered {
			kind = CloudUserMessageSteered
		}
		return CloudUserMessageReceipt{Kind: kind, Turn: &value}, nil
	case cloudclient.AdmissionDuplicate:
		return CloudUserMessageReceipt{Kind: CloudUserMessageDuplicate, TurnID: cloneStringPointer(outcome.TurnID)}, nil
	case cloudclient.AdmissionQueued:
		return CloudUserMessageReceipt{}, errors.New("Cloud already accepted this message without returning a turn. Reopen the conversation to see its status before sending again.")
	default:
		return CloudUserMessageReceipt{}, errors.New("The cloud returned an unsupported admission receipt.")
	}
}

func admittedTurn(outcome cloudclient.AdmissionOutcome) (appserver.Turn, error) {
	receipt, err := admittedReceipt(outcome)
	if err != nil {
		return appserver.Turn{}, err
	}
	if receipt.Kind == CloudUserMessageDuplicate {
		return appserver.Turn{}, errors.New("Cloud already accepted this message without returning a turn. Reopen the conversation to see its status before sending again.")
	}
	if receipt.Turn == nil {
		return appserver.Turn{}, errors.New("The cloud returned an unsupported admission receipt.")
	}
	return *receipt.Turn, nil
}

func cloneStringPointer(value *string) *string {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}
