package idecontext

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

const (
	IDEContextRequestTimeout = 5 * time.Second
	MaxIPCFrameBytes         = 256 * 1024 * 1024
	TUISourceClientID        = "codex-tui"

	OpenIDEHint                 = "Open this project in VS Code or Cursor with the Codex extension active."
	IDEDidNotProvideContextHint = "The IDE extension did not provide context."
	KeepTryingHint              = "Codex will keep trying on future messages."
)

var ErrIDEContextTimedOut = errors.New("timed out waiting for IDE context")

type IPCMessage struct {
	Type           string         `json:"type,omitempty"`
	RequestID      string         `json:"requestId,omitempty"`
	SourceClientID string         `json:"sourceClientId,omitempty"`
	Version        *int           `json:"version,omitempty"`
	Method         string         `json:"method,omitempty"`
	Params         map[string]any `json:"params,omitempty"`
	ResultType     string         `json:"resultType,omitempty"`
	Result         map[string]any `json:"result,omitempty"`
	Response       map[string]any `json:"response,omitempty"`
	Error          string         `json:"error,omitempty"`
}

type IdeContextErrorKind string

const (
	IdeContextErrorConnect             IdeContextErrorKind = "connect"
	IdeContextErrorSend                IdeContextErrorKind = "send"
	IdeContextErrorRead                IdeContextErrorKind = "read"
	IdeContextErrorInvalidResponse     IdeContextErrorKind = "invalid_response"
	IdeContextErrorResponseTooLarge    IdeContextErrorKind = "response_too_large"
	IdeContextErrorRequestFailed       IdeContextErrorKind = "request_failed"
	IdeContextErrorUnsupportedPlatform IdeContextErrorKind = "unsupported_platform"
)

type IdeContextError struct {
	Kind    IdeContextErrorKind
	Err     error
	Message string
}

func (e *IdeContextError) Error() string {
	if e == nil {
		return ""
	}
	switch e.Kind {
	case IdeContextErrorConnect:
		return "failed to connect to IDE context provider: " + errorText(e)
	case IdeContextErrorSend:
		return "failed to request IDE context: " + errorText(e)
	case IdeContextErrorRead:
		return "failed to read IDE context: " + errorText(e)
	case IdeContextErrorInvalidResponse:
		return "invalid IDE context response: " + errorText(e)
	case IdeContextErrorResponseTooLarge:
		return "IDE context response exceeded maximum size"
	case IdeContextErrorRequestFailed:
		return "IDE context request failed"
	case IdeContextErrorUnsupportedPlatform:
		return "IDE context is not supported on this platform"
	default:
		return errorText(e)
	}
}

func (e *IdeContextError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *IdeContextError) UserFacingHint() string {
	if e == nil {
		return ""
	}
	switch e.Kind {
	case IdeContextErrorConnect:
		return OpenIDEHint
	case IdeContextErrorRequestFailed:
		if e.Message == "no-client-found" {
			return OpenIDEHint
		}
		return IDEDidNotProvideContextHint + " Try /ide again."
	case IdeContextErrorResponseTooLarge:
		return "The selected IDE context is too large. Clear any large selection in your IDE and try /ide again."
	case IdeContextErrorSend:
		return "Codex could not request IDE context. Try /ide again."
	case IdeContextErrorRead, IdeContextErrorInvalidResponse:
		return "Codex could not read IDE context. Try /ide again."
	default:
		return e.Error()
	}
}

func (e *IdeContextError) PromptSkipHint() string {
	if e == nil {
		return ""
	}
	switch e.Kind {
	case IdeContextErrorResponseTooLarge:
		return "The selected IDE context is too large. Clear any large selection in your IDE."
	case IdeContextErrorConnect:
		return OpenIDEHint
	case IdeContextErrorRequestFailed:
		switch e.Message {
		case "no-client-found":
			return OpenIDEHint
		case "client-disconnected":
			return HintWithRetry("The IDE connection changed while Codex was requesting context.")
		case "request-timeout":
			return HintWithRetry("The IDE extension did not answer in time.")
		case "request-version-mismatch":
			return "The connected IDE extension is not compatible with this IDE context request."
		case "no-handler-for-request":
			return "The connected IDE client does not support IDE context requests."
		default:
			return HintWithRetry(IDEDidNotProvideContextHint)
		}
	case IdeContextErrorRead:
		if errors.Is(e.Err, ErrIDEContextTimedOut) {
			return "Codex timed out waiting for IDE context. It will keep trying on future messages."
		}
		return HintWithRetry("Codex could not read IDE context.")
	case IdeContextErrorSend:
		return HintWithRetry("Codex lost the IDE connection while requesting context.")
	case IdeContextErrorInvalidResponse:
		return HintWithRetry("Codex received an unexpected IDE context response.")
	default:
		return e.Error()
	}
}

func HintWithRetry(message string) string {
	return strings.TrimSpace(message) + " " + KeepTryingHint
}

func DefaultIPCSocketPath() string {
	if runtime.GOOS == "windows" {
		return `\\.\pipe\codex-ipc`
	}
	uid := strings.TrimSpace(getenv("UID"))
	if uid == "" {
		uid = "0"
	}
	return filepath.Join(tempDir(), "codex-ipc", "ipc-"+uid+".sock")
}

func IDEContextRequestMessage(requestID string, workspaceRoot string) IPCMessage {
	version := 0
	return IPCMessage{
		Type:           "request",
		RequestID:      requestID,
		SourceClientID: TUISourceClientID,
		Version:        &version,
		Method:         "ide-context",
		Params: map[string]any{
			"workspaceRoot": workspaceRoot,
		},
	}
}

func WriteIDEContextRequest(stream io.Writer, requestID string, workspaceRoot string) error {
	request := IDEContextRequestMessage(requestID, workspaceRoot)
	return WriteFrame(stream, request)
}

func WriteFrame(stream io.Writer, message any) error {
	payload, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("invalid IDE context JSON message: %w", err)
	}
	if len(payload) > math.MaxUint32 {
		return errors.New("IDE context payload exceeds u32 length")
	}
	var lenBytes [4]byte
	binary.LittleEndian.PutUint32(lenBytes[:], uint32(len(payload)))
	if _, err := stream.Write(lenBytes[:]); err != nil {
		return err
	}
	if _, err := stream.Write(payload); err != nil {
		return err
	}
	if flusher, ok := stream.(interface{ Flush() error }); ok {
		return flusher.Flush()
	}
	return nil
}

func ReadFrame(stream io.Reader) (map[string]any, error) {
	return ReadFrameBeforeDeadline(stream, time.Time{})
}

func ReadFrameBeforeDeadline(stream io.Reader, deadline time.Time) (map[string]any, error) {
	var lenBytes [4]byte
	if err := ReadExactBeforeDeadline(stream, lenBytes[:], deadline); err != nil {
		return nil, &IdeContextError{Kind: IdeContextErrorRead, Err: err}
	}
	length := int(binary.LittleEndian.Uint32(lenBytes[:]))
	if length > MaxIPCFrameBytes {
		return nil, &IdeContextError{Kind: IdeContextErrorResponseTooLarge}
	}

	payload := make([]byte, length)
	if err := ReadExactBeforeDeadline(stream, payload, deadline); err != nil {
		return nil, &IdeContextError{Kind: IdeContextErrorRead, Err: err}
	}

	var message map[string]any
	if err := json.Unmarshal(payload, &message); err != nil {
		return nil, &IdeContextError{Kind: IdeContextErrorInvalidResponse, Message: "invalid JSON payload: " + err.Error()}
	}
	return message, nil
}

func ReadExactBeforeDeadline(stream io.Reader, buf []byte, deadline time.Time) error {
	readSoFar := 0
	for readSoFar < len(buf) {
		if err := EnsureDeadlineNotExpired(deadline); err != nil {
			return err
		}
		n, err := stream.Read(buf[readSoFar:])
		if n > 0 {
			readSoFar += n
		}
		if err == nil {
			continue
		}
		if errors.Is(err, io.ErrNoProgress) {
			continue
		}
		if errors.Is(err, io.EOF) && readSoFar == len(buf) {
			break
		}
		if errors.Is(err, io.EOF) {
			return io.ErrUnexpectedEOF
		}
		return err
	}
	return EnsureDeadlineNotExpired(deadline)
}

func ReadResponseFrame(stream io.ReadWriter, requestID string, deadline time.Time) (map[string]any, error) {
	for {
		if err := EnsureDeadlineNotExpired(deadline); err != nil {
			return nil, &IdeContextError{Kind: IdeContextErrorRead, Err: err}
		}
		message, err := ReadFrameBeforeDeadline(stream, deadline)
		if err != nil {
			return nil, err
		}
		messageType, _ := message["type"].(string)
		switch messageType {
		case "response":
			if value, _ := message["requestId"].(string); value == requestID {
				return message, nil
			}
		case "broadcast", "client-discovery-response":
		case "client-discovery-request":
			if discoveryRequestID, _ := message["requestId"].(string); discoveryRequestID != "" {
				response := IPCMessage{
					Type:      "client-discovery-response",
					RequestID: discoveryRequestID,
					Response:  map[string]any{"canHandle": false},
				}
				if err := WriteFrame(stream, response); err != nil {
					return nil, &IdeContextError{Kind: IdeContextErrorSend, Err: err}
				}
			}
		case "request":
			if err := AnswerUnsupportedRequest(stream, message); err != nil {
				return nil, err
			}
		case "":
			return nil, &IdeContextError{Kind: IdeContextErrorInvalidResponse, Message: "IDE context message did not include a type"}
		default:
			return nil, &IdeContextError{Kind: IdeContextErrorInvalidResponse, Message: "unexpected IDE context message type: " + messageType}
		}
	}
}

func AnswerUnsupportedRequest(stream io.Writer, message map[string]any) error {
	inboundRequestID, _ := message["requestId"].(string)
	if inboundRequestID == "" {
		return nil
	}
	response := IPCMessage{
		Type:       "response",
		RequestID:  inboundRequestID,
		ResultType: "error",
		Error:      "no-handler-for-request",
	}
	if err := WriteFrame(stream, response); err != nil {
		return &IdeContextError{Kind: IdeContextErrorSend, Err: err}
	}
	return nil
}

func ExtractIDEContext(response map[string]any) (*IdeContext, error) {
	if err := EnsureSuccessResponse(response); err != nil {
		return nil, err
	}
	result, ok := response["result"].(map[string]any)
	if !ok {
		return nil, &IdeContextError{Kind: IdeContextErrorInvalidResponse, Message: "ide-context response did not include result.ideContext"}
	}
	rawContext, ok := result["ideContext"]
	if !ok {
		return nil, &IdeContextError{Kind: IdeContextErrorInvalidResponse, Message: "ide-context response did not include result.ideContext"}
	}
	data, err := json.Marshal(rawContext)
	if err != nil {
		return nil, &IdeContextError{Kind: IdeContextErrorInvalidResponse, Message: err.Error()}
	}
	var context IdeContext
	if err := json.Unmarshal(data, &context); err != nil {
		return nil, &IdeContextError{Kind: IdeContextErrorInvalidResponse, Message: err.Error()}
	}
	return &context, nil
}

func EnsureSuccessResponse(response map[string]any) error {
	resultType, _ := response["resultType"].(string)
	switch resultType {
	case "success":
		return nil
	case "error":
		message, _ := response["error"].(string)
		if message == "" {
			message = "unknown error"
		}
		return &IdeContextError{Kind: IdeContextErrorRequestFailed, Message: message}
	default:
		return &IdeContextError{Kind: IdeContextErrorInvalidResponse, Message: "response did not include a success or error resultType"}
	}
}

func EnsureDeadlineNotExpired(deadline time.Time) error {
	if !deadline.IsZero() && !time.Now().Before(deadline) {
		return ErrIDEContextTimedOut
	}
	return nil
}

func errorText(e *IdeContextError) string {
	if e == nil {
		return ""
	}
	if e.Err != nil {
		return e.Err.Error()
	}
	return e.Message
}

var getenv = os.Getenv
var tempDir = os.TempDir
