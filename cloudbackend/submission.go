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
// admission request. Ordered text and image parts are accepted; every other
// input kind has no native cloud alternative and stays rejected.
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
	parts := make([]cloudclient.UserMessageInput, 0, len(input))
	for _, item := range input {
		part, ok := cloudUserMessageInput(item)
		if !ok {
			return cloudclient.AddUserMessageRequest{}, rejectCloudInput("Cloud conversations accept ordered text and image parts only.")
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return cloudclient.AddUserMessageRequest{}, rejectCloudInput("A cloud message needs at least one text or image part.")
	}
	var turnTrigger *string
	if trigger != "" {
		value := trigger
		turnTrigger = &value
	}
	return cloudclient.AddUserMessageRequest{
		ThreadID:            threadID,
		ClientUserMessageID: submissionID,
		Input:               parts,
		TurnTrigger:         turnTrigger,
		Mode:                cloudclient.SubmissionStartIfIdle,
		SharedAccess:        false,
	}, nil
}

// cloudUserMessageInput maps one app-server input onto the native part model.
// Local images, audio, skills and mentions have no native cloud alternative, so
// they stay rejected instead of being silently turned into text.
func cloudUserMessageInput(input turn.TurnUserInput) (cloudclient.UserMessageInput, bool) {
	kind := strings.TrimSpace(input.Type)
	if strings.TrimSpace(input.Text) != "" {
		if kind != "" && !strings.EqualFold(kind, "text") {
			return cloudclient.UserMessageInput{}, false
		}
		if input.Path != "" || input.URL != "" || input.FileID != "" {
			return cloudclient.UserMessageInput{}, false
		}
		var elements []cloudclient.UserMessageTextElement
		if len(input.TextElements) > 0 {
			elements = make([]cloudclient.UserMessageTextElement, 0, len(input.TextElements))
		}
		for _, element := range input.TextElements {
			elements = append(elements, cloudclient.UserMessageTextElement{
				Start:       int(element.ByteRange.Start),
				End:         int(element.ByteRange.End),
				Placeholder: element.Placeholder,
			})
		}
		return cloudclient.UserMessageInput{
			Kind:         cloudclient.UserMessageText,
			Text:         input.Text,
			TextElements: elements,
		}, true
	}
	if kind == "" || strings.EqualFold(kind, "image") {
		if fileID := strings.TrimSpace(input.FileID); fileID != "" {
			return cloudclient.UserMessageInput{
				Kind:   cloudclient.UserMessageImage,
				Image:  cloudclient.ImageReference{FileID: fileID},
				Detail: cloudImageDetail(input.Detail),
			}, true
		}
		if imageURL := strings.TrimSpace(input.URL); imageURL != "" {
			return cloudclient.UserMessageInput{
				Kind:   cloudclient.UserMessageImage,
				Image:  cloudclient.ImageReference{URL: imageURL},
				Detail: cloudImageDetail(input.Detail),
			}, true
		}
	}
	return cloudclient.UserMessageInput{}, false
}

// cloudImageDetail maps the app-server detail spelling onto the wire enum. An
// unknown spelling carries no detail rather than failing the submission.
func cloudImageDetail(detail *string) *cloudclient.ImageDetail {
	if detail == nil {
		return nil
	}
	var value cloudclient.ImageDetail
	switch strings.ToLower(strings.TrimSpace(*detail)) {
	case "auto":
		value = cloudclient.ImageDetailAuto
	case "low":
		value = cloudclient.ImageDetailLow
	case "high":
		value = cloudclient.ImageDetailHigh
	case "original":
		value = cloudclient.ImageDetailOriginal
	default:
		return nil
	}
	return &value
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
