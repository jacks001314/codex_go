package status

import (
	"net/url"
	"strings"
)

type RemoteConnectionKind string

const (
	RemoteConnectionEmbedded   RemoteConnectionKind = "embedded"
	RemoteConnectionWebSocket  RemoteConnectionKind = "websocket"
	RemoteConnectionUnixSocket RemoteConnectionKind = "unix-socket"
)

type RemoteConnectionStatus struct {
	Connected bool
	Endpoint  string
	Address   string
	Version   string
}

func RemoteConnectionStatusValue(kind RemoteConnectionKind, endpoint string, serverVersion *string) *RemoteConnectionStatus {
	switch kind {
	case RemoteConnectionEmbedded:
		return nil
	case RemoteConnectionWebSocket:
		address, ok := SanitizedWebSocketDisplayAddress(endpoint)
		if !ok {
			address = "<invalid websocket URL>"
		}
		return &RemoteConnectionStatus{
			Connected: true,
			Endpoint:  endpoint,
			Address:   address,
			Version:   remoteServerVersionDisplay(serverVersion),
		}
	case RemoteConnectionUnixSocket:
		return &RemoteConnectionStatus{
			Connected: true,
			Endpoint:  endpoint,
			Address:   "unix://" + endpoint,
			Version:   remoteServerVersionDisplay(serverVersion),
		}
	default:
		return nil
	}
}

func SanitizedWebSocketDisplayAddress(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", false
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String(), true
}

func remoteServerVersionDisplay(serverVersion *string) string {
	if serverVersion == nil {
		return "unknown"
	}
	return "v" + *serverVersion
}
