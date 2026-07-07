package codemode

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

type RequestID int64

type DelegateRequestID int64

type ProtocolVersion uint32

const ProtocolV1 ProtocolVersion = 1

func NewProtocolVersion(value uint32) (ProtocolVersion, bool) {
	if value == 0 {
		return 0, false
	}
	return ProtocolVersion(value), true
}

type Capability string

func NewCapability(value string) (Capability, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("identifier must not be empty")
	}
	return Capability(value), nil
}

type SessionID string

func NewSessionID(value string) (SessionID, error) {
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("identifier must not be empty")
	}
	return SessionID(value), nil
}

type CapabilitySet []Capability

func NewCapabilitySet(capabilities ...Capability) (CapabilitySet, error) {
	seen := map[Capability]bool{}
	out := append([]Capability(nil), capabilities...)
	sort.Slice(out, func(i int, j int) bool { return out[i] < out[j] })
	for _, capability := range out {
		if seen[capability] {
			return nil, fmt.Errorf("duplicate capability `%s`", capability)
		}
		seen[capability] = true
	}
	return CapabilitySet(out), nil
}

func (s *CapabilitySet) Contains(capability Capability) bool {
	if s == nil {
		return false
	}
	for _, existing := range *s {
		if existing == capability {
			return true
		}
	}
	return false
}

type SupportedProtocolVersions []ProtocolVersion

func NewSupportedProtocolVersions(versions ...ProtocolVersion) (SupportedProtocolVersions, error) {
	if len(versions) == 0 {
		return nil, fmt.Errorf("at least one protocol version is required")
	}
	seen := map[ProtocolVersion]bool{}
	out := append([]ProtocolVersion(nil), versions...)
	sort.Slice(out, func(i int, j int) bool { return out[i] < out[j] })
	for _, version := range out {
		if version == 0 {
			return nil, fmt.Errorf("protocol version must be non-zero")
		}
		if seen[version] {
			return nil, fmt.Errorf("duplicate protocol version %d", version)
		}
		seen[version] = true
	}
	return SupportedProtocolVersions(out), nil
}

func (v *SupportedProtocolVersions) Contains(version ProtocolVersion) bool {
	if v == nil {
		return false
	}
	for _, existing := range *v {
		if existing == version {
			return true
		}
	}
	return false
}

type ClientHello struct {
	SupportedVersions    SupportedProtocolVersions `json:"supportedVersions"`
	RequiredCapabilities CapabilitySet             `json:"requiredCapabilities"`
	OptionalCapabilities CapabilitySet             `json:"optionalCapabilities"`
}

func NewClientHello(versions SupportedProtocolVersions, required CapabilitySet, optional CapabilitySet) (ClientHello, error) {
	for _, capability := range required {
		if (&optional).Contains(capability) {
			return ClientHello{}, fmt.Errorf("capability `%s` cannot be both required and optional", capability)
		}
	}
	return ClientHello{SupportedVersions: versions, RequiredCapabilities: required, OptionalCapabilities: optional}, nil
}

type HostHello struct {
	SelectedVersion ProtocolVersion `json:"selectedVersion"`
	Capabilities    CapabilitySet   `json:"capabilities"`
}

type HandshakeRejectReason struct {
	Type              string                    `json:"type"`
	SupportedVersions SupportedProtocolVersions `json:"supportedVersions,omitempty"`
	Capability        Capability                `json:"capability,omitempty"`
	Message           string                    `json:"message,omitempty"`
}

func NoCompatibleVersion(versions SupportedProtocolVersions) HandshakeRejectReason {
	return HandshakeRejectReason{Type: "noCompatibleVersion", SupportedVersions: versions}
}

func MissingRequiredCapability(capability Capability) HandshakeRejectReason {
	return HandshakeRejectReason{Type: "missingRequiredCapability", Capability: capability}
}

func InvalidHello(message string) HandshakeRejectReason {
	return HandshakeRejectReason{Type: "invalidHello", Message: message}
}

type WireResult[T any] struct {
	Status  string `json:"status"`
	Value   T      `json:"value,omitempty"`
	Message string `json:"message,omitempty"`
}

func ResultOK[T any](value T) WireResult[T] {
	return WireResult[T]{Status: "ok", Value: value}
}

func ResultErr[T any](message string) WireResult[T] {
	return WireResult[T]{Status: "error", Message: message}
}

func (r *WireResult[T]) IntoResult() (T, error) {
	var zero T
	if r == nil {
		return zero, fmt.Errorf("wire result is nil")
	}
	if r.Status == "ok" {
		return r.Value, nil
	}
	if r.Message == "" {
		return zero, fmt.Errorf("operation failed")
	}
	return zero, fmt.Errorf("%s", r.Message)
}

type ClientToHost struct {
	Type             string                        `json:"type"`
	ID               RequestID                     `json:"id,omitempty"`
	Hello            *ClientHello                  `json:"-"`
	Request          *HostRequest                  `json:"request,omitempty"`
	DelegateID       DelegateRequestID             `json:"-"`
	DelegateResponse *WireResult[DelegateResponse] `json:"result,omitempty"`
}

func ClientHelloMessage(hello ClientHello) ClientToHost {
	return ClientToHost{Type: "connection/hello", Hello: &hello}
}

func OperationRequest(id RequestID, request HostRequest) ClientToHost {
	return ClientToHost{Type: "operation/request", ID: id, Request: &request}
}

func CancelRequest(id RequestID) ClientToHost {
	return ClientToHost{Type: "operation/cancel", ID: id}
}

func DelegateResponseMessage(id DelegateRequestID, result WireResult[DelegateResponse]) ClientToHost {
	return ClientToHost{Type: "delegate/response", DelegateID: id, DelegateResponse: &result}
}

func (m ClientToHost) MarshalJSON() ([]byte, error) {
	switch m.Type {
	case "connection/hello":
		type hello ClientHello
		if m.Hello == nil {
			return nil, fmt.Errorf("client hello is nil")
		}
		body, err := json.Marshal((*hello)(m.Hello))
		if err != nil {
			return nil, err
		}
		var object map[string]any
		if err := json.Unmarshal(body, &object); err != nil {
			return nil, err
		}
		object["type"] = m.Type
		return json.Marshal(object)
	case "operation/request":
		return json.Marshal(struct {
			Type    string       `json:"type"`
			ID      RequestID    `json:"id"`
			Request *HostRequest `json:"request"`
		}{Type: m.Type, ID: m.ID, Request: m.Request})
	case "operation/cancel":
		return json.Marshal(struct {
			Type string    `json:"type"`
			ID   RequestID `json:"id"`
		}{Type: m.Type, ID: m.ID})
	case "delegate/response":
		return json.Marshal(struct {
			Type   string                        `json:"type"`
			ID     DelegateRequestID             `json:"id"`
			Result *WireResult[DelegateResponse] `json:"result"`
		}{Type: m.Type, ID: m.DelegateID, Result: m.DelegateResponse})
	default:
		return nil, fmt.Errorf("unknown client-to-host type %q", m.Type)
	}
}

type HostToClient struct {
	Type       string
	ID         RequestID
	DelegateID DelegateRequestID
	SessionID  SessionID
	CellID     CellID
	Hello      *HostHello
	Reason     *HandshakeRejectReason
	Result     *WireResult[HostResponse]
	Initial    *WireResult[RuntimeResponse]
	Request    *DelegateRequest
}

func HostHelloMessage(hello HostHello) HostToClient {
	return HostToClient{Type: "connection/ready", Hello: &hello}
}

func HandshakeRejected(reason HandshakeRejectReason) HostToClient {
	return HostToClient{Type: "connection/rejected", Reason: &reason}
}

func HostOperationResponse(id RequestID, result WireResult[HostResponse]) HostToClient {
	return HostToClient{Type: "operation/response", ID: id, Result: &result}
}

func InitialResponse(id RequestID, result WireResult[RuntimeResponse]) HostToClient {
	return HostToClient{Type: "execute/initialResponse", ID: id, Initial: &result}
}

func DelegateRequestMessage(id DelegateRequestID, sessionID SessionID, request DelegateRequest) HostToClient {
	return HostToClient{Type: "delegate/request", DelegateID: id, SessionID: sessionID, Request: &request}
}

func CancelDelegateRequest(id DelegateRequestID) HostToClient {
	return HostToClient{Type: "delegate/cancel", DelegateID: id}
}

func CellClosed(sessionID SessionID, cellID CellID) HostToClient {
	return HostToClient{Type: "cell/closed", SessionID: sessionID, CellID: cellID}
}

func (m HostToClient) MarshalJSON() ([]byte, error) {
	switch m.Type {
	case "connection/ready":
		type hello HostHello
		body, err := json.Marshal((*hello)(m.Hello))
		if err != nil {
			return nil, err
		}
		var object map[string]any
		if err := json.Unmarshal(body, &object); err != nil {
			return nil, err
		}
		object["type"] = m.Type
		return json.Marshal(object)
	case "connection/rejected":
		return json.Marshal(struct {
			Type   string                 `json:"type"`
			Reason *HandshakeRejectReason `json:"reason"`
		}{Type: m.Type, Reason: m.Reason})
	case "operation/response":
		return json.Marshal(struct {
			Type   string                    `json:"type"`
			ID     RequestID                 `json:"id"`
			Result *WireResult[HostResponse] `json:"result"`
		}{Type: m.Type, ID: m.ID, Result: m.Result})
	case "execute/initialResponse":
		return json.Marshal(struct {
			Type   string                       `json:"type"`
			ID     RequestID                    `json:"id"`
			Result *WireResult[RuntimeResponse] `json:"result"`
		}{Type: m.Type, ID: m.ID, Result: m.Initial})
	case "delegate/request":
		return json.Marshal(struct {
			Type      string            `json:"type"`
			ID        DelegateRequestID `json:"id"`
			SessionID SessionID         `json:"sessionId"`
			Request   *DelegateRequest  `json:"request"`
		}{Type: m.Type, ID: m.DelegateID, SessionID: m.SessionID, Request: m.Request})
	case "delegate/cancel":
		return json.Marshal(struct {
			Type string            `json:"type"`
			ID   DelegateRequestID `json:"id"`
		}{Type: m.Type, ID: m.DelegateID})
	case "cell/closed":
		return json.Marshal(struct {
			Type      string    `json:"type"`
			SessionID SessionID `json:"sessionId"`
			CellID    CellID    `json:"cellId"`
		}{Type: m.Type, SessionID: m.SessionID, CellID: m.CellID})
	default:
		return nil, fmt.Errorf("unknown host-to-client type %q", m.Type)
	}
}

type HostRequest struct {
	Method    string          `json:"method"`
	SessionID SessionID       `json:"sessionId,omitempty"`
	Request   *ExecuteRequest `json:"request,omitempty"`
	Wait      *WaitRequest    `json:"-"`
	CellID    CellID          `json:"cellId,omitempty"`
}

func OpenSessionRequest(sessionID SessionID) HostRequest {
	return HostRequest{Method: "session/open", SessionID: sessionID}
}

func ExecuteSessionRequest(sessionID SessionID, request ExecuteRequest) HostRequest {
	return HostRequest{Method: "session/execute", SessionID: sessionID, Request: &request}
}

func WaitSessionRequest(sessionID SessionID, request WaitRequest) HostRequest {
	return HostRequest{Method: "session/wait", SessionID: sessionID, Wait: &request}
}

func TerminateSessionRequest(sessionID SessionID, cellID CellID) HostRequest {
	return HostRequest{Method: "session/terminate", SessionID: sessionID, CellID: cellID}
}

func ShutdownSessionRequest(sessionID SessionID) HostRequest {
	return HostRequest{Method: "session/shutdown", SessionID: sessionID}
}

func (r HostRequest) MarshalJSON() ([]byte, error) {
	switch r.Method {
	case "session/wait":
		return json.Marshal(struct {
			Method    string       `json:"method"`
			SessionID SessionID    `json:"sessionId"`
			Request   *WaitRequest `json:"request"`
		}{Method: r.Method, SessionID: r.SessionID, Request: r.Wait})
	default:
		type alias HostRequest
		return json.Marshal(alias(r))
	}
}

type HostResponse struct {
	Type      string       `json:"type"`
	SessionID SessionID    `json:"sessionId,omitempty"`
	CellID    CellID       `json:"cellId,omitempty"`
	Outcome   *WaitOutcome `json:"outcome,omitempty"`
}

func SessionReady(sessionID SessionID) HostResponse {
	return HostResponse{Type: "session/ready", SessionID: sessionID}
}

func ExecutionStarted(cellID CellID) HostResponse {
	return HostResponse{Type: "execution/started", CellID: cellID}
}

func WaitCompleted(outcome WaitOutcome) HostResponse {
	return HostResponse{Type: "wait/completed", Outcome: &outcome}
}

func SessionClosed(sessionID SessionID) HostResponse {
	return HostResponse{Type: "session/closed", SessionID: sessionID}
}

type DelegateRequest struct {
	Type       string          `json:"type"`
	Invocation *NestedToolCall `json:"invocation,omitempty"`
	CallID     string          `json:"callId,omitempty"`
	CellID     CellID          `json:"cellId,omitempty"`
	Text       string          `json:"text,omitempty"`
}

func InvokeToolRequest(invocation NestedToolCall) DelegateRequest {
	return DelegateRequest{Type: "tool/invoke", Invocation: &invocation}
}

func NotifyRequest(callID string, cellID CellID, text string) DelegateRequest {
	return DelegateRequest{Type: "notification/send", CallID: callID, CellID: cellID, Text: text}
}

type DelegateResponse struct {
	Type   string          `json:"type"`
	Result json.RawMessage `json:"result,omitempty"`
}

func ToolResultResponse(result json.RawMessage) DelegateResponse {
	return DelegateResponse{Type: "tool/result", Result: result}
}

func NotificationDeliveredResponse() DelegateResponse {
	return DelegateResponse{Type: "notification/delivered"}
}
