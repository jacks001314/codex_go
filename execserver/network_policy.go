package execserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const (
	MethodNetworkPolicyRequest     = "network/policyRequest"
	MaxNetworkPolicyHostBytes      = 253
	MaxNetworkPolicyProcessIDBytes = 256
	MaxNetworkPolicyReasonBytes    = 1024
)

type NetworkPolicyRequestParams struct {
	ProcessID string                         `json:"processId"`
	Request   ExecServerNetworkPolicyRequest `json:"request"`
}

type ExecServerNetworkProtocol string

const (
	NetworkProtocolHTTP         ExecServerNetworkProtocol = "http"
	NetworkProtocolHTTPSConnect ExecServerNetworkProtocol = "https_connect"
	NetworkProtocolSOCKS5TCP    ExecServerNetworkProtocol = "socks5_tcp"
	NetworkProtocolSOCKS5UDP    ExecServerNetworkProtocol = "socks5_udp"
)

type ExecServerNetworkPolicyRequest struct {
	Protocol ExecServerNetworkProtocol `json:"protocol"`
	Host     string                    `json:"host"`
	Port     uint16                    `json:"port"`
}

type NetworkPolicyRequestResponse struct {
	Decision ExecServerNetworkPolicyDecision `json:"decision"`
}

type ExecServerNetworkPolicyDecision struct {
	Type   string `json:"type"`
	Reason string `json:"reason,omitempty"`
}

func AllowNetworkPolicyDecision() ExecServerNetworkPolicyDecision {
	return ExecServerNetworkPolicyDecision{Type: "allow"}
}
func DenyNetworkPolicyDecision(reason string) ExecServerNetworkPolicyDecision {
	return ExecServerNetworkPolicyDecision{Type: "deny", Reason: reason}
}
func AskNetworkPolicyDecision(reason string) ExecServerNetworkPolicyDecision {
	return ExecServerNetworkPolicyDecision{Type: "ask", Reason: reason}
}

func (p NetworkPolicyRequestParams) Validate() error {
	if len(p.ProcessID) == 0 || len(p.ProcessID) > MaxNetworkPolicyProcessIDBytes {
		return fmt.Errorf("processId must be 1..%d bytes", MaxNetworkPolicyProcessIDBytes)
	}
	if len(p.Request.Host) == 0 || len(p.Request.Host) > MaxNetworkPolicyHostBytes {
		return fmt.Errorf("host must be 1..%d bytes", MaxNetworkPolicyHostBytes)
	}
	switch p.Request.Protocol {
	case NetworkProtocolHTTP, NetworkProtocolHTTPSConnect, NetworkProtocolSOCKS5TCP, NetworkProtocolSOCKS5UDP:
	default:
		return fmt.Errorf("unsupported network protocol %q", p.Request.Protocol)
	}
	if p.Request.Port == 0 {
		return errors.New("port must be non-zero")
	}
	return nil
}
func (d ExecServerNetworkPolicyDecision) Validate() error {
	switch d.Type {
	case "allow":
		if strings.TrimSpace(d.Reason) != "" {
			return errors.New("allow decision must not include reason")
		}
	case "deny", "ask":
		if len(d.Reason) > MaxNetworkPolicyReasonBytes {
			return fmt.Errorf("reason exceeds %d bytes", MaxNetworkPolicyReasonBytes)
		}
	default:
		return fmt.Errorf("unsupported decision type %q", d.Type)
	}
	return nil
}
func (d *ExecServerNetworkPolicyDecision) UnmarshalJSON(data []byte) error {
	type alias ExecServerNetworkPolicyDecision
	var v alias
	if err := json.Unmarshal(data, &v); err != nil {
		return err
	}
	if err := ExecServerNetworkPolicyDecision(v).Validate(); err != nil {
		return err
	}
	*d = ExecServerNetworkPolicyDecision(v)
	return nil
}

type RemoteNetworkProxyConfig struct {
	Enabled                        bool   `json:"enabled"`
	EnableSOCKS5                   bool   `json:"enableSocks5"`
	EnableSOCKS5UDP                bool   `json:"enableSocks5Udp"`
	AllowUpstreamProxy             bool   `json:"allowUpstreamProxy"`
	DangerouslyAllowAllUnixSockets bool   `json:"dangerouslyAllowAllUnixSockets"`
	Mode                           string `json:"mode"`
	Domains                        any    `json:"domains"`
	UnixSockets                    any    `json:"unixSockets"`
	AllowLocalBinding              bool   `json:"allowLocalBinding"`
	RequestPolicyDecisions         bool   `json:"requestPolicyDecisions,omitempty"`
}
