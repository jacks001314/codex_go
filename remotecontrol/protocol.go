package remotecontrol

import (
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strings"

	"github.com/google/uuid"
)

type RemoteControlTarget struct {
	WebSocketURL  string
	EnrollURL     string
	RefreshURL    string
	PairURL       string
	PairStatusURL string
}

type EnrollRemoteServerRequest struct {
	Name             string `json:"name"`
	OS               string `json:"os"`
	Arch             string `json:"arch"`
	AppServerVersion string `json:"app_server_version"`
	InstallationID   string `json:"installation_id"`
}

type EnrollRemoteServerResponse struct {
	ServerID           string `json:"server_id"`
	EnvironmentID      string `json:"environment_id"`
	RemoteControlToken string `json:"remote_control_token"`
	ExpiresAt          string `json:"expires_at"`
}

type RefreshRemoteServerRequest struct {
	ServerID       string `json:"server_id"`
	InstallationID string `json:"installation_id"`
}

type StartRemoteControlPairingRequest struct {
	ManualCode bool `json:"manual_code"`
}

type StartRemoteControlPairingResponse struct {
	PairingCode       string  `json:"pairing_code"`
	ManualPairingCode *string `json:"manual_pairing_code"`
	ServerID          string  `json:"server_id"`
	EnvironmentID     string  `json:"environment_id"`
	ExpiresAt         string  `json:"expires_at"`
}

type RemoteControlPairingStatusRequest struct {
	PairingCode       *string `json:"pairing_code,omitempty"`
	ManualPairingCode *string `json:"manual_pairing_code,omitempty"`
}

type RemoteControlPairingStatusCode struct {
	PairingCode       *string
	ManualPairingCode *string
}

func (c *RemoteControlPairingStatusCode) Request() *RemoteControlPairingStatusRequest {
	if c == nil {
		return &RemoteControlPairingStatusRequest{}
	}
	return &RemoteControlPairingStatusRequest{
		PairingCode:       cloneStringPtr(c.PairingCode),
		ManualPairingCode: cloneStringPtr(c.ManualPairingCode),
	}
}

type RemoteControlPairingStatusResponse struct {
	Claimed bool `json:"claimed"`
}

type ClientID string

type StreamID string

func NewStreamID() StreamID {
	return StreamID(uuid.NewString())
}

type ClientEventType string

const (
	ClientEventClientMessage      ClientEventType = "client_message"
	ClientEventClientMessageChunk ClientEventType = "client_message_chunk"
	ClientEventAck                ClientEventType = "ack"
	ClientEventPing               ClientEventType = "ping"
	ClientEventClientClosed       ClientEventType = "client_closed"
)

type ClientEnvelope struct {
	Type               ClientEventType `json:"type"`
	Message            json.RawMessage `json:"message,omitempty"`
	SegmentID          *int            `json:"segment_id,omitempty"`
	SegmentCount       *int            `json:"segment_count,omitempty"`
	MessageSizeBytes   *int            `json:"message_size_bytes,omitempty"`
	MessageChunkBase64 *string         `json:"message_chunk_base64,omitempty"`
	ClientID           ClientID        `json:"client_id"`
	StreamID           *StreamID       `json:"stream_id,omitempty"`
	SeqID              *uint64         `json:"seq_id,omitempty"`
	Cursor             *string         `json:"cursor,omitempty"`
	ReassembledChunk   bool            `json:"-"`
}

type PongStatus string

const (
	PongStatusActive  PongStatus = "active"
	PongStatusUnknown PongStatus = "unknown"
)

type ServerEventType string

const (
	ServerEventServerMessage      ServerEventType = "server_message"
	ServerEventServerMessageChunk ServerEventType = "server_message_chunk"
	ServerEventAck                ServerEventType = "ack"
	ServerEventPong               ServerEventType = "pong"
)

type ServerEnvelope struct {
	Type               ServerEventType `json:"type"`
	Message            json.RawMessage `json:"message,omitempty"`
	SegmentID          *int            `json:"segment_id,omitempty"`
	SegmentCount       *int            `json:"segment_count,omitempty"`
	MessageSizeBytes   *int            `json:"message_size_bytes,omitempty"`
	MessageChunkBase64 *string         `json:"message_chunk_base64,omitempty"`
	Status             *PongStatus     `json:"status,omitempty"`
	ClientID           ClientID        `json:"client_id"`
	StreamID           StreamID        `json:"stream_id"`
	SeqID              uint64          `json:"seq_id"`
}

func (e *ServerEnvelope) ChunkSegmentID() *int {
	if e == nil || e.Type != ServerEventServerMessageChunk || e.SegmentID == nil {
		return nil
	}
	value := *e.SegmentID
	return &value
}

func NormalizeRemoteControlURL(remoteControlURL string) (*RemoteControlTarget, error) {
	base, err := NormalizeRemoteControlBaseURL(remoteControlURL)
	if err != nil {
		return nil, err
	}
	enrollURL, err := remoteControlEndpointURL(base, "wham/remote/control/server/enroll", remoteControlURL)
	if err != nil {
		return nil, err
	}
	refreshURL, err := remoteControlEndpointURL(base, "wham/remote/control/server/refresh", remoteControlURL)
	if err != nil {
		return nil, err
	}
	pairURL, err := remoteControlEndpointURL(base, "wham/remote/control/server/pair", remoteControlURL)
	if err != nil {
		return nil, err
	}
	pairStatusURL, err := remoteControlEndpointURL(base, "wham/remote/control/server/pair/status", remoteControlURL)
	if err != nil {
		return nil, err
	}
	websocketURL, err := remoteControlEndpointURL(base, "wham/remote/control/server", remoteControlURL)
	if err != nil {
		return nil, err
	}
	switch websocketURL.Scheme {
	case "https":
		websocketURL.Scheme = "wss"
	case "http":
		websocketURL.Scheme = "ws"
	default:
		return nil, fmt.Errorf("invalid remote control URL `%s`", remoteControlURL)
	}
	return &RemoteControlTarget{
		WebSocketURL:  websocketURL.String(),
		EnrollURL:     enrollURL.String(),
		RefreshURL:    refreshURL.String(),
		PairURL:       pairURL.String(),
		PairStatusURL: pairStatusURL.String(),
	}, nil
}

func NormalizeRemoteControlBaseURL(remoteControlURL string) (*url.URL, error) {
	parsed, err := url.Parse(remoteControlURL)
	if err != nil {
		return nil, fmt.Errorf("invalid remote control URL `%s`: %w", remoteControlURL, err)
	}
	if parsed.Path == "" {
		parsed.Path = "/"
	} else if !strings.HasSuffix(parsed.Path, "/") {
		parsed.Path += "/"
	}

	host := parsed.Hostname()
	switch parsed.Scheme {
	case "https":
		if isLocalhostRemoteControlHost(host) || isAllowedRemoteControlChatGPTHost(host) {
			return parsed, nil
		}
	case "http":
		if isLocalhostRemoteControlHost(host) {
			return parsed, nil
		}
	}
	return nil, fmt.Errorf("invalid remote control URL `%s`; expected HTTPS URL for chatgpt.com or chatgpt-staging.com, or HTTP/HTTPS URL for localhost", remoteControlURL)
}

func remoteControlEndpointURL(base *url.URL, endpoint string, raw string) (*url.URL, error) {
	joined := base.ResolveReference(&url.URL{Path: endpoint})
	if joined == nil {
		return nil, fmt.Errorf("invalid remote control URL `%s`", raw)
	}
	return joined, nil
}

func isAllowedRemoteControlChatGPTHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	return host == "chatgpt.com" ||
		host == "chatgpt-staging.com" ||
		strings.HasSuffix(host, ".chatgpt.com") ||
		strings.HasSuffix(host, ".chatgpt-staging.com")
}

func isLocalhostRemoteControlHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
