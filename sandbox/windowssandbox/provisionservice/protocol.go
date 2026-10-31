// Package provisionservice speaks the authenticated named-pipe protocol of the
// packaged Windows sandbox provisioning service. Rust keeps the client half in
// windows-sandbox-rs (provisioning_protocol, framed_io, service_identity and
// provisioning_client); this package carries it so the Go runtime can prefer
// the service and fall back to the elevated helper on Unavailable.
package provisionservice

import (
	"bytes"
	"errors"
	"fmt"
	"io"

	json "github.com/goccy/go-json"
)

const (
	// ProtocolVersion is shared by provisioning clients and the Windows service.
	ProtocolVersion uint8 = 1

	// PipeName is the base named pipe of the machine-wide provisioning service.
	PipeName = `\\.\pipe\OpenAI.CodexSandbox`

	// GroupChanged is the pre-dispatch refusal that lets the caller reconnect.
	GroupChanged = "sandbox group changed before authentication"

	// MaxFrameLen bounds the memory used by an individual untrusted IPC frame.
	MaxFrameLen = 8 * 1024 * 1024
)

// Settings mirrors Rust WindowsSandboxProvisioningSettings.
type Settings struct {
	// ProxyPorts are loopback proxy ports permitted for the offline identity.
	ProxyPorts []uint16 `json:"proxy_ports"`
	// AllowLocalBinding lets the offline identity exchange arbitrary loopback
	// traffic.
	AllowLocalBinding bool `json:"allow_local_binding"`
}

// Listeners mirrors Rust WindowsSandboxProxyListeners.
type Listeners struct {
	HTTPPorts  []uint16 `json:"http_ports"`
	SOCKSPorts []uint16 `json:"socks_ports"`
}

// ProvisionRequest mirrors Rust SandboxProvisioningRequest. The service rejects
// unknown fields and independently authenticates its installed caller.
type ProvisionRequest struct {
	CodexHome      string    `json:"codex_home"`
	RegisteredCore bool      `json:"registered_core,omitempty"`
	RefreshOnly    bool      `json:"refresh_only,omitempty"`
	Settings       Settings  `json:"settings"`
	Listeners      Listeners `json:"listeners"`
}

// MessageKind tags one ProvisioningMessage variant.
type MessageKind string

const (
	KindRegisterInstallationRequest MessageKind = "register_installation_request"
	KindProvisionSandboxRequest     MessageKind = "provision_sandbox_request"
	KindProvisionSandboxResponse    MessageKind = "provision_sandbox_response"
)

// Message is one variant of the versioned provisioning protocol.
type Message struct {
	Kind      MessageKind
	CodexHome string
	Request   *ProvisionRequest
	Response  *Response
}

// ResponseKind tags one SandboxProvisioningResponse variant.
type ResponseKind string

const (
	ResponseOK          ResponseKind = "ok"
	ResponseUnavailable ResponseKind = "unavailable"
	ResponseError       ResponseKind = "error"
)

// Response is the provisioning service's reply.
type Response struct {
	Kind    ResponseKind
	Message string
}

// Frame carries the protocol version and exactly one message.
type Frame struct {
	Version uint8
	Message Message
}

// MarshalJSON writes the internally tagged response shape.
func (r Response) MarshalJSON() ([]byte, error) {
	switch r.Kind {
	case ResponseOK, ResponseUnavailable:
		return json.Marshal(struct {
			Status ResponseKind `json:"status"`
		}{Status: r.Kind})
	case ResponseError:
		return json.Marshal(struct {
			Status  ResponseKind `json:"status"`
			Message string       `json:"message"`
		}{Status: r.Kind, Message: r.Message})
	default:
		return nil, fmt.Errorf("unknown provisioning response status %q", r.Kind)
	}
}

// UnmarshalJSON reads the internally tagged response shape. A missing status is
// an unknown status, matching serde's untagged refusal.
func (r *Response) UnmarshalJSON(data []byte) error {
	var raw struct {
		Status  ResponseKind `json:"status"`
		Message string       `json:"message"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	switch raw.Status {
	case ResponseOK, ResponseUnavailable:
		r.Kind, r.Message = raw.Status, ""
	case ResponseError:
		r.Kind, r.Message = raw.Status, raw.Message
	default:
		return fmt.Errorf("unknown provisioning response status %q", raw.Status)
	}
	return nil
}

// MarshalJSON flattens the internally tagged message next to the version.
func (f Frame) MarshalJSON() ([]byte, error) {
	switch f.Message.Kind {
	case KindRegisterInstallationRequest:
		return json.Marshal(struct {
			Version   uint8       `json:"version"`
			Type      MessageKind `json:"type"`
			CodexHome string      `json:"codex_home"`
		}{Version: f.Version, Type: f.Message.Kind, CodexHome: f.Message.CodexHome})
	case KindProvisionSandboxRequest:
		if f.Message.Request == nil {
			return nil, errors.New("provision_sandbox_request requires a payload")
		}
		request := *f.Message.Request
		request.normalize()
		return json.Marshal(struct {
			Version uint8             `json:"version"`
			Type    MessageKind       `json:"type"`
			Payload *ProvisionRequest `json:"payload"`
		}{Version: f.Version, Type: f.Message.Kind, Payload: &request})
	case KindProvisionSandboxResponse:
		if f.Message.Response == nil {
			return nil, errors.New("provision_sandbox_response requires a payload")
		}
		response := *f.Message.Response
		return json.Marshal(struct {
			Version uint8       `json:"version"`
			Type    MessageKind `json:"type"`
			Payload Response    `json:"payload"`
		}{Version: f.Version, Type: f.Message.Kind, Payload: response})
	default:
		return nil, fmt.Errorf("unknown provisioning message %q", f.Message.Kind)
	}
}

// UnmarshalJSON reads a flattened versioned provisioning frame.
func (f *Frame) UnmarshalJSON(data []byte) error {
	var raw struct {
		Version   uint8           `json:"version"`
		Type      MessageKind     `json:"type"`
		CodexHome *string         `json:"codex_home"`
		Payload   json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	f.Version = raw.Version
	f.Message = Message{Kind: raw.Type}
	switch raw.Type {
	case KindRegisterInstallationRequest:
		if raw.CodexHome == nil {
			return errors.New("register_installation_request requires codex_home")
		}
		f.Message.CodexHome = *raw.CodexHome
	case KindProvisionSandboxRequest:
		request, err := decodeProvisionRequest(raw.Payload)
		if err != nil {
			return err
		}
		f.Message.Request = request
	case KindProvisionSandboxResponse:
		if len(raw.Payload) == 0 {
			return errors.New("provision_sandbox_response requires a payload")
		}
		var response Response
		if err := json.Unmarshal(raw.Payload, &response); err != nil {
			return err
		}
		f.Message.Response = &response
	default:
		return fmt.Errorf("unknown provisioning message %q", raw.Type)
	}
	return nil
}

// decodeProvisionRequest enforces serde's deny_unknown_fields contract.
func decodeProvisionRequest(data []byte) (*ProvisionRequest, error) {
	if len(data) == 0 || bytes.Equal(bytes.TrimSpace(data), []byte("null")) {
		return nil, errors.New("provision_sandbox_request requires a payload")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var request ProvisionRequest
	if err := decoder.Decode(&request); err != nil {
		return nil, err
	}
	var extra struct{}
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return nil, errors.New("trailing data in the sandbox provisioning request")
	}
	request.normalize()
	return &request, nil
}

func (r *ProvisionRequest) normalize() {
	r.Settings.normalize()
	r.Listeners.normalize()
}

func (s *Settings) normalize() {
	if s.ProxyPorts == nil {
		s.ProxyPorts = []uint16{}
	}
}

func (l *Listeners) normalize() {
	if l.HTTPPorts == nil {
		l.HTTPPorts = []uint16{}
	}
	if l.SOCKSPorts == nil {
		l.SOCKSPorts = []uint16{}
	}
}
