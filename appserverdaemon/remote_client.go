package appserverdaemon

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"
)

const (
	DefaultRemoteAppServerChannelCapacity      = 1
	UDSWebSocketHandshakeURL                   = "ws://localhost/rpc"
	RemoteAppServerMaxWebSocketMessageSize int = 128 << 20
)

type RemoteAppServerEndpointKind string

const (
	RemoteEndpointWebSocket  RemoteAppServerEndpointKind = "webSocket"
	RemoteEndpointUnixSocket RemoteAppServerEndpointKind = "unixSocket"
)

type RemoteAppServerEndpoint struct {
	Kind         RemoteAppServerEndpointKind
	WebSocketURL string
	AuthToken    *string
	SocketPath   string
}

func NewWebSocketEndpoint(websocketURL string, authToken *string) *RemoteAppServerEndpoint {
	return &RemoteAppServerEndpoint{
		Kind:         RemoteEndpointWebSocket,
		WebSocketURL: websocketURL,
		AuthToken:    cloneString(authToken),
	}
}

func NewUnixSocketEndpoint(socketPath string) *RemoteAppServerEndpoint {
	return &RemoteAppServerEndpoint{Kind: RemoteEndpointUnixSocket, SocketPath: socketPath}
}

func (e *RemoteAppServerEndpoint) String() string {
	if e == nil {
		return ""
	}
	switch e.Kind {
	case RemoteEndpointUnixSocket:
		return e.SocketPath
	default:
		return e.WebSocketURL
	}
}

type RemoteAppServerConnectArgs struct {
	Endpoint                       *RemoteAppServerEndpoint
	ClientName                     string
	ClientVersion                  string
	ExperimentalAPI                bool
	MCPServerOpenAIFormElicitation bool
	OptOutNotificationMethods      []string
	ChannelCapacity                int
}

type RemoteAppServerInitializeCapabilities struct {
	ExperimentalAPI                bool     `json:"experimentalApi,omitempty"`
	RequestAttestation             bool     `json:"requestAttestation,omitempty"`
	OptOutNotificationMethods      []string `json:"optOutNotificationMethods,omitempty"`
	MCPServerOpenAIFormElicitation bool     `json:"mcpServerOpenaiFormElicitation,omitempty"`
}

type RemoteAppServerInitializeClientInfo struct {
	Name    string  `json:"name"`
	Title   *string `json:"title,omitempty"`
	Version string  `json:"version"`
}

type RemoteAppServerInitializeParams struct {
	ClientInfo   RemoteAppServerInitializeClientInfo    `json:"clientInfo"`
	Capabilities *RemoteAppServerInitializeCapabilities `json:"capabilities,omitempty"`
}

type RemoteAppServerInitializeMetadata struct {
	ServerVersion *string
	CodexHome     *string
	PendingEvents []json.RawMessage
}

type RemoteAppServerClientInfo struct {
	ServerVersion *string
	CodexHome     *AppServerPath
}

func (a *RemoteAppServerConnectArgs) EffectiveChannelCapacity() int {
	if a == nil || a.ChannelCapacity < 1 {
		return DefaultRemoteAppServerChannelCapacity
	}
	return a.ChannelCapacity
}

func (a *RemoteAppServerConnectArgs) InitializeParams() RemoteAppServerInitializeParams {
	if a == nil {
		return RemoteAppServerInitializeParams{}
	}
	capabilities := &RemoteAppServerInitializeCapabilities{
		ExperimentalAPI:                a.ExperimentalAPI,
		RequestAttestation:             false,
		MCPServerOpenAIFormElicitation: a.MCPServerOpenAIFormElicitation,
	}
	if len(a.OptOutNotificationMethods) > 0 {
		capabilities.OptOutNotificationMethods = append([]string(nil), a.OptOutNotificationMethods...)
	}
	return RemoteAppServerInitializeParams{
		ClientInfo: RemoteAppServerInitializeClientInfo{
			Name:    a.ClientName,
			Version: a.ClientVersion,
		},
		Capabilities: capabilities,
	}
}

func WebSocketURLSupportsAuthToken(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	switch parsed.Scheme {
	case "wss":
		return parsed.Host != ""
	case "ws":
		host := parsed.Hostname()
		if strings.EqualFold(host, "localhost") {
			return true
		}
		ip := net.ParseIP(host)
		return ip != nil && ip.IsLoopback()
	default:
		return false
	}
}

func ParseRemoteAppServerInitializeMetadata(raw json.RawMessage) (*RemoteAppServerInitializeMetadata, error) {
	var response struct {
		UserAgent string          `json:"userAgent"`
		CodexHome string          `json:"codexHome"`
		Events    json.RawMessage `json:"events"`
	}
	if err := json.Unmarshal(raw, &response); err != nil {
		return nil, err
	}
	var serverVersion *string
	if response.UserAgent != "" {
		version, err := ParseVersionFromUserAgent(response.UserAgent)
		if err == nil {
			serverVersion = &version
		}
	}
	var codexHome *string
	if strings.TrimSpace(response.CodexHome) != "" {
		codexHome = &response.CodexHome
	}
	return &RemoteAppServerInitializeMetadata{
		ServerVersion: serverVersion,
		CodexHome:     codexHome,
	}, nil
}

func RemoteAppServerClientInfoFromMetadata(metadata *RemoteAppServerInitializeMetadata, localCodexHome string, remote bool) *RemoteAppServerClientInfo {
	info := &RemoteAppServerClientInfo{}
	if metadata != nil {
		info.ServerVersion = cloneString(metadata.ServerVersion)
	}
	if !remote {
		info.CodexHome = FromAppServerPath(localCodexHome)
		return info
	}
	if metadata != nil && metadata.CodexHome != nil {
		info.CodexHome = FromAppServerPath(*metadata.CodexHome)
	}
	return info
}

func (i *RemoteAppServerClientInfo) CodexHomeString() string {
	if i == nil || i.CodexHome == nil {
		return ""
	}
	return i.CodexHome.String()
}

func (i *RemoteAppServerClientInfo) ServerVersionString() string {
	if i == nil || i.ServerVersion == nil {
		return ""
	}
	return *i.ServerVersion
}

func ValidateEndpoint(endpoint *RemoteAppServerEndpoint) error {
	if endpoint == nil {
		return fmt.Errorf("remote app-server endpoint is required")
	}
	switch endpoint.Kind {
	case RemoteEndpointWebSocket:
		if endpoint.WebSocketURL == "" {
			return fmt.Errorf("remote app-server websocket URL is required")
		}
		parsed, err := url.Parse(endpoint.WebSocketURL)
		if err != nil {
			return err
		}
		if parsed.Scheme != "ws" && parsed.Scheme != "wss" {
			return fmt.Errorf("unsupported remote app-server websocket scheme %q", parsed.Scheme)
		}
	case RemoteEndpointUnixSocket:
		if endpoint.SocketPath == "" {
			return fmt.Errorf("remote app-server unix socket path is required")
		}
	default:
		return fmt.Errorf("unknown remote app-server endpoint kind %q", endpoint.Kind)
	}
	return nil
}
