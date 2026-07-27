package execserver

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode"

	"codex_go/network"
)

const (
	MethodNetworkPolicyRequest     = "network/policyRequest"
	MaxNetworkPolicyHostBytes      = 253
	MaxNetworkPolicyProcessIDBytes = 256
	MaxNetworkPolicyReasonBytes    = 1024
	MaxInFlightServerRequests      = 256
	NetworkPolicyDenialReason      = "not_allowed"
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
	if containsControlOrWhitespace(p.Request.Host) {
		return errors.New("host must not contain control or whitespace characters")
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
		if containsControl(d.Reason) {
			return errors.New("reason must not contain control characters")
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
	Enabled                        bool              `json:"enabled"`
	EnableSOCKS5                   bool              `json:"enableSocks5"`
	EnableSOCKS5UDP                bool              `json:"enableSocks5Udp"`
	AllowUpstreamProxy             bool              `json:"allowUpstreamProxy"`
	DangerouslyAllowAllUnixSockets bool              `json:"dangerouslyAllowAllUnixSockets"`
	Mode                           string            `json:"mode"`
	Domains                        map[string]string `json:"domains,omitempty"`
	UnixSockets                    map[string]string `json:"unixSockets,omitempty"`
	AllowLocalBinding              bool              `json:"allowLocalBinding"`
}

func RemoteNetworkProxyConfigFromProxyConfig(config network.ProxyConfig) (RemoteNetworkProxyConfig, error) {
	settings := config.Network
	if settings.Enabled && (settings.MITM || settings.CredentialBroker || settings.DangerouslyAllowPlaintextCredentialInjection || len(settings.MITMHooks) > 0) {
		return RemoteNetworkProxyConfig{}, errors.New("remote exec-server network proxy does not support MITM, credential injection, or MITM hooks")
	}
	domains := map[string]string(nil)
	if settings.Domains != nil {
		domains = map[string]string{}
		for _, entry := range settings.Domains.EffectiveEntries() {
			domains[entry.Pattern] = string(entry.Permission)
		}
	}
	unixSockets := map[string]string(nil)
	if settings.UnixSockets != nil {
		unixSockets = map[string]string{}
		for path, permission := range settings.UnixSockets.Entries {
			unixSockets[path] = string(permission)
		}
	}
	return RemoteNetworkProxyConfig{
		Enabled:                        settings.Enabled,
		EnableSOCKS5:                   settings.EnableSocks5,
		EnableSOCKS5UDP:                settings.EnableSocks5UDP,
		AllowUpstreamProxy:             settings.AllowUpstreamProxy,
		DangerouslyAllowAllUnixSockets: settings.DangerouslyAllowAllUnixSockets,
		Mode:                           string(settings.Mode),
		Domains:                        domains,
		UnixSockets:                    unixSockets,
		AllowLocalBinding:              settings.AllowLocalBinding,
	}, nil
}

func (c RemoteNetworkProxyConfig) ProxyConfig() (network.ProxyConfig, error) {
	settings := network.DefaultProxySettings()
	settings.Enabled = c.Enabled
	settings.EnableSocks5 = c.EnableSOCKS5
	settings.EnableSocks5UDP = c.EnableSOCKS5UDP
	settings.AllowUpstreamProxy = c.AllowUpstreamProxy
	settings.DangerouslyAllowAllUnixSockets = c.DangerouslyAllowAllUnixSockets
	settings.AllowLocalBinding = c.AllowLocalBinding
	settings.Mode = network.ProxyMode(c.Mode)
	if settings.Mode != network.ProxyModeFull && settings.Mode != network.ProxyModeLimited {
		return network.ProxyConfig{}, fmt.Errorf("unsupported network proxy mode %q", c.Mode)
	}
	if c.Domains != nil {
		settings.Domains = &network.ProxyDomainPermissions{}
		for pattern, raw := range c.Domains {
			permission := network.ProxyDomainPermission(raw)
			switch permission {
			case network.ProxyDomainNone, network.ProxyDomainAllow, network.ProxyDomainDeny:
			default:
				return network.ProxyConfig{}, fmt.Errorf("unsupported domain permission %q", raw)
			}
			settings.Domains.Entries = append(settings.Domains.Entries, network.ProxyDomainPermissionEntry{Pattern: pattern, Permission: permission})
		}
	}
	if c.UnixSockets != nil {
		settings.UnixSockets = &network.ProxyUnixSocketPermissions{Entries: map[string]network.ProxyUnixSocketPermission{}}
		for path, raw := range c.UnixSockets {
			permission := network.ProxyUnixSocketPermission(raw)
			if permission != network.ProxyUnixSocketAllow && permission != network.ProxyUnixSocketDeny {
				return network.ProxyConfig{}, fmt.Errorf("unsupported unix socket permission %q", raw)
			}
			settings.UnixSockets.Entries[path] = permission
		}
	}
	return network.ProxyConfig{Network: settings}, nil
}

type RemoteNetworkProxyAuditMetadata struct {
	ConversationID string `json:"conversationId,omitempty"`
	AppVersion     string `json:"appVersion,omitempty"`
	UserAccountID  string `json:"userAccountId,omitempty"`
	AuthMode       string `json:"authMode,omitempty"`
	Originator     string `json:"originator,omitempty"`
	UserEmail      string `json:"userEmail,omitempty"`
	TerminalType   string `json:"terminalType,omitempty"`
	Model          string `json:"model,omitempty"`
	Slug           string `json:"slug,omitempty"`
}

type RemoteNetworkProxyLaunchConfig struct {
	Proxy                   RemoteNetworkProxyConfig        `json:"proxy"`
	AuditMetadata           RemoteNetworkProxyAuditMetadata `json:"auditMetadata"`
	EnvironmentID           *string                         `json:"environmentId,omitempty"`
	ExecutionID             *string                         `json:"executionId,omitempty"`
	PolicyDecisionTimeoutMS *uint64                         `json:"policyDecisionTimeoutMs,omitempty"`
}

func NetworkPolicyRequestFromProxy(request network.ProxyPolicyRequest) ExecServerNetworkPolicyRequest {
	return ExecServerNetworkPolicyRequest{
		Protocol: ExecServerNetworkProtocol(request.Protocol),
		Host:     request.Host,
		Port:     request.Port,
	}
}

func (r ExecServerNetworkPolicyRequest) ProxyRequest(environmentID string) network.ProxyPolicyRequest {
	return network.ProxyPolicyRequest{
		Protocol:      network.ProxyProtocol(r.Protocol),
		Host:          r.Host,
		Port:          r.Port,
		EnvironmentID: environmentID,
	}
}

func containsControlOrWhitespace(value string) bool {
	return strings.IndexFunc(value, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0
}

func containsControl(value string) bool {
	return strings.IndexFunc(value, unicode.IsControl) >= 0
}
