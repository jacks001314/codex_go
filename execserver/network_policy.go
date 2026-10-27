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
	MethodNetworkPolicyDecision    = "network/policyDecision"
	MaxNetworkPolicyHostBytes      = 253
	MaxNetworkPolicyProcessIDBytes = 256
	MaxNetworkPolicyReasonBytes    = 1024
	MaxInFlightServerRequests      = 256
	NetworkPolicyDenialReason      = "not_allowed"

	MaxNetworkPolicyMethodBytes    = 32
	MaxNetworkPolicyClientBytes    = 256
	MaxNetworkPolicyTimestampBytes = 64
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

// NetworkPolicyDecisionNotification reports an executor-local network policy
// decision to its authenticated controller for auditing (Rust #38670).
type NetworkPolicyDecisionNotification struct {
	ProcessID      string                    `json:"processId"`
	Timestamp      string                    `json:"timestamp"`
	Scope          string                    `json:"scope"`
	Decision       string                    `json:"decision"`
	Source         string                    `json:"source"`
	Reason         string                    `json:"reason"`
	Protocol       ExecServerNetworkProtocol `json:"protocol"`
	Host           string                    `json:"host"`
	Port           uint16                    `json:"port"`
	Method         *string                   `json:"method,omitempty"`
	Client         *string                   `json:"client,omitempty"`
	PolicyOverride bool                      `json:"policyOverride"`
}

// Validate mirrors Rust emit_network_policy_decision's field validation:
// bounded, control-free fields with enumerated scope/decision/source values.
func (n NetworkPolicyDecisionNotification) Validate() bool {
	if n.ProcessID == "" || len(n.ProcessID) > MaxNetworkPolicyProcessIDBytes {
		return false
	}
	if n.Host == "" || len(n.Host) > MaxNetworkPolicyHostBytes || containsControlOrWhitespace(n.Host) {
		return false
	}
	if len(n.Reason) > MaxNetworkPolicyReasonBytes || containsControl(n.Reason) {
		return false
	}
	switch n.Scope {
	case "domain", "non_domain":
	default:
		return false
	}
	switch n.Decision {
	case "allow", "deny", "ask":
	default:
		return false
	}
	switch n.Source {
	case "baseline_policy", "mode_guard", "proxy_state", "decider":
	default:
		return false
	}
	if n.Timestamp == "" || len(n.Timestamp) > MaxNetworkPolicyTimestampBytes || containsControl(n.Timestamp) {
		return false
	}
	if n.Method != nil && (len(*n.Method) > MaxNetworkPolicyMethodBytes || containsControlOrWhitespace(*n.Method)) {
		return false
	}
	if n.Client != nil && (len(*n.Client) > MaxNetworkPolicyClientBytes || containsControlOrWhitespace(*n.Client)) {
		return false
	}
	switch n.Protocol {
	case NetworkProtocolHTTP, NetworkProtocolHTTPSConnect, NetworkProtocolSOCKS5TCP, NetworkProtocolSOCKS5UDP:
		return true
	default:
		return false
	}
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

// RemoteCredentialMasking reports the masking support of one executor or the
// masking mode requested for a remote launch (Rust #56049).
type RemoteCredentialMasking int

const (
	// RemoteCredentialMaskingDisabled means the executor does not mask
	// credentials, or the caller did not request masking.
	RemoteCredentialMaskingDisabled RemoteCredentialMasking = iota
	// RemoteCredentialMaskingBuiltInOnly masks only when the configuration has
	// no configured credential providers.
	RemoteCredentialMaskingBuiltInOnly
	// RemoteCredentialMaskingWithConfiguredProviders masks built-in and
	// configured providers.
	RemoteCredentialMaskingWithConfiguredProviders
)

// NetworkProxyCapabilities is the network-proxy feature set of one executor
// (Rust EnvironmentCapabilities::network_proxy).
type NetworkProxyCapabilities struct {
	Launch            bool
	CredentialMasking RemoteCredentialMasking
}

// NetworkProxy projects the capability bits into the masking mode the caller
// should request.
func (c EnvironmentCapabilities) NetworkProxy() NetworkProxyCapabilities {
	mode := RemoteCredentialMaskingDisabled
	if c.CredentialMasking {
		if c.CredentialMaskingCustomProviders {
			mode = RemoteCredentialMaskingWithConfiguredProviders
		} else {
			mode = RemoteCredentialMaskingBuiltInOnly
		}
	}
	return NetworkProxyCapabilities{
		Launch:            c.NetworkProxyLaunch,
		CredentialMasking: mode,
	}
}

type RemoteNetworkProxyConfig struct {
	Enabled bool `json:"enabled"`
	// CredentialMasking activates the executor-local broker. Credential values,
	// destination hints, CAs, and MITM hooks stay local; only provider rules
	// cross the wire (Rust #56049).
	CredentialMasking bool `json:"credentialMasking,omitempty"`
	// CredentialProviders carries configured provider rules to the executor.
	// They are discarded when masking is off, even when this config is
	// forwarded.
	CredentialProviders            map[string]network.CredentialProviderConfig `json:"credentialProviders,omitempty"`
	EnableSOCKS5                   bool                                        `json:"enableSocks5"`
	EnableSOCKS5UDP                bool                                        `json:"enableSocks5Udp"`
	AllowUpstreamProxy             bool                                        `json:"allowUpstreamProxy"`
	DangerouslyAllowAllUnixSockets bool                                        `json:"dangerouslyAllowAllUnixSockets"`
	Mode                           string                                      `json:"mode"`
	Domains                        map[string]string                           `json:"domains,omitempty"`
	UnixSockets                    map[string]string                           `json:"unixSockets,omitempty"`
	AllowLocalBinding              bool                                        `json:"allowLocalBinding"`
}

// UnmarshalJSON drops provider rules sent with masking disabled (Rust #56049).
func (c *RemoteNetworkProxyConfig) UnmarshalJSON(data []byte) error {
	type alias RemoteNetworkProxyConfig
	var decoded alias
	if err := json.Unmarshal(data, &decoded); err != nil {
		return err
	}
	if !decoded.CredentialMasking {
		decoded.CredentialProviders = nil
	}
	*c = RemoteNetworkProxyConfig(decoded)
	return nil
}

func RemoteNetworkProxyConfigFromProxyConfig(config network.ProxyConfig) (RemoteNetworkProxyConfig, error) {
	return RemoteNetworkProxyConfigFromProxyConfigWithCredentialMaskingMode(config, RemoteCredentialMaskingDisabled)
}

// RemoteNetworkProxyConfigFromProxyConfigWithCredentialMasking requests
// configured-provider masking when credentialMasking is true and disables it
// otherwise. The caller negotiates the effective mode with the executor's
// capabilities before launching a process.
func RemoteNetworkProxyConfigFromProxyConfigWithCredentialMasking(config network.ProxyConfig, credentialMasking bool) (RemoteNetworkProxyConfig, error) {
	mode := RemoteCredentialMaskingDisabled
	if credentialMasking {
		mode = RemoteCredentialMaskingWithConfiguredProviders
	}
	return RemoteNetworkProxyConfigFromProxyConfigWithCredentialMaskingMode(config, mode)
}

// RemoteNetworkProxyConfigFromProxyConfigWithCredentialMaskingMode builds the
// executor-visible proxy config for one masking mode. Built-in-only masking
// falls back to an unmasked proxy when configured providers are present.
func RemoteNetworkProxyConfigFromProxyConfigWithCredentialMaskingMode(config network.ProxyConfig, mode RemoteCredentialMasking) (RemoteNetworkProxyConfig, error) {
	settings := config.Network
	requestedMasking := mode != RemoteCredentialMaskingDisabled
	credentialMasking := requestedMasking &&
		settings.Enabled &&
		settings.CredentialBroker &&
		(mode == RemoteCredentialMaskingWithConfiguredProviders || len(settings.CredentialProviders) == 0)
	brokerOnlyMITM := requestedMasking &&
		settings.CredentialBroker &&
		!settings.DangerouslyAllowPlaintextCredentialInjection &&
		len(settings.MITMHooks) == 0
	if settings.Enabled && (settings.DangerouslyAllowPlaintextCredentialInjection || len(settings.MITMHooks) > 0 ||
		(settings.MITM && !credentialMasking && !brokerOnlyMITM)) {
		return RemoteNetworkProxyConfig{}, errors.New("remote exec-server network proxy does not support MITM, credential injection, or MITM hooks")
	}
	if settings.Enabled && settings.CredentialBroker && !credentialMasking && !settings.MITM {
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
	// Rust #46004: the remote wire keeps a plain bool, so an omitted
	// controller flag resolves to false for remote configuration.
	allowAllUnixSockets := settings.DangerouslyAllowAllUnixSockets != nil && *settings.DangerouslyAllowAllUnixSockets
	providers := map[string]network.CredentialProviderConfig(nil)
	if credentialMasking && mode == RemoteCredentialMaskingWithConfiguredProviders && len(settings.CredentialProviders) > 0 {
		providers = make(map[string]network.CredentialProviderConfig, len(settings.CredentialProviders))
		for id, provider := range settings.CredentialProviders {
			providers[id] = provider
		}
	}
	return RemoteNetworkProxyConfig{
		Enabled:                        settings.Enabled,
		CredentialMasking:              credentialMasking,
		CredentialProviders:            providers,
		EnableSOCKS5:                   settings.EnableSocks5,
		EnableSOCKS5UDP:                settings.EnableSocks5UDP,
		AllowUpstreamProxy:             settings.AllowUpstreamProxy,
		DangerouslyAllowAllUnixSockets: allowAllUnixSockets,
		Mode:                           string(settings.Mode),
		Domains:                        domains,
		UnixSockets:                    unixSockets,
		AllowLocalBinding:              settings.AllowLocalBinding,
	}, nil
}

func (c RemoteNetworkProxyConfig) ProxyConfig() (network.ProxyConfig, error) {
	settings := network.DefaultProxySettings()
	settings.Enabled = c.Enabled
	settings.SetCredentialBrokerEnabled(c.CredentialMasking)
	if c.CredentialMasking {
		settings.CredentialProviders = c.CredentialProviders
	}
	settings.EnableSocks5 = c.EnableSOCKS5
	settings.EnableSocks5UDP = c.EnableSOCKS5UDP
	settings.AllowUpstreamProxy = c.AllowUpstreamProxy
	// Rust #46004: a remote config always supplies an explicit bool, so it
	// becomes Some(value) on the receiving side.
	allowAllUnixSockets := c.DangerouslyAllowAllUnixSockets
	settings.DangerouslyAllowAllUnixSockets = &allowAllUnixSockets
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
