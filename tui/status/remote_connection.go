package status

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"

	codextui "codex_go/tui"
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

// Rust parity: codex-rs/tui/src/status/remote_connection.rs (#43622).

// ServerVersionNotice is a connected-service version warning shown to the user.
type ServerVersionNotice struct {
	Message string
	// OfferUpdate suggests `codex app-server daemon update`; only the implicit
	// local daemon can be updated by the client.
	OfferUpdate bool
}

// ServerVersionNoticeMessage returns the warning text when the connected
// server's version policy differs from the client's: an older released service
// reads "older than", while a local build or another release line reads
// "different from" (Rust #43622, #46673).
func ServerVersionNoticeMessage(client string, server *string) (string, bool) {
	if server == nil {
		return "", false
	}
	kind, ok := codextui.ServerVersionNoticeKindFor(client, *server)
	if !ok {
		return "", false
	}
	comparison := "older than"
	if kind == codextui.ServerVersionNoticeDifferent {
		comparison = "different from"
	}
	return fmt.Sprintf("A background Codex service is running v%s, %s your Codex CLI v%s.", *server, comparison, client), true
}

// routingQueryParams are the query parameters that identify a remote routing
// target and therefore keep the notice key distinct between deployments.
var routingQueryParams = map[string]bool{
	"workspace": true, "project": true, "tenant": true, "environment": true,
	"namespace": true, "organization": true, "org": true, "account": true,
	"route": true, "instance": true, "region": true, "deployment": true,
	"cluster": true,
}

// ServerVersionNoticeKey identifies a notice by endpoint, server home, and
// version pair so repeated connect/reconnect cycles suppress an identical
// notice (Rust #43622). The key exists only when a notice exists.
func ServerVersionNoticeKey(kind RemoteConnectionKind, endpoint string, serverHome string, client string, server string) (string, bool) {
	if _, ok := codextui.ServerVersionNoticeKindFor(client, server); !ok {
		return "", false
	}
	hasher := sha256.New()
	if strings.TrimSpace(serverHome) != "" {
		hashVersionNoticeIdentityPart(hasher, "server-home:", []byte(serverHome))
	}
	switch kind {
	case RemoteConnectionUnixSocket:
		hashVersionNoticeIdentityPart(hasher, "unix:", []byte(endpoint))
	case RemoteConnectionWebSocket:
		if identity, ok := websocketNoticeIdentity(endpoint); ok {
			hashVersionNoticeIdentityPart(hasher, "websocket:", []byte(identity))
		}
	default:
		hashVersionNoticeIdentityPart(hasher, "embedded:", nil)
	}
	return hex.EncodeToString(hasher.Sum(nil)) + ":" + client + "-" + server, true
}

// PendingServerVersionNotice returns the notice and its dedup key when the
// connected server is an older official stable release and the same notice has
// not already been shown (Rust #43622).
func PendingServerVersionNotice(kind RemoteConnectionKind, endpoint string, serverHome string, client string, server string, offerUpdate bool, lastShown string) (*ServerVersionNotice, string, bool) {
	message, ok := ServerVersionNoticeMessage(client, &server)
	if !ok {
		return nil, "", false
	}
	key, ok := ServerVersionNoticeKey(kind, endpoint, serverHome, client, server)
	if !ok || key == lastShown {
		return nil, "", false
	}
	return &ServerVersionNotice{Message: message, OfferUpdate: offerUpdate}, key, true
}

func hashVersionNoticeIdentityPart(hasher interface{ Write([]byte) (int, error) }, tag string, value []byte) {
	_, _ = hasher.Write([]byte(tag))
	var length [8]byte
	binary.LittleEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = hasher.Write(length[:])
	_, _ = hasher.Write(value)
}

// websocketNoticeIdentity strips credentials, fragment, and non-routing query
// parameters so equivalent endpoints share one notice identity while distinct
// routing targets stay distinct (Rust #43622).
func websocketNoticeIdentity(raw string) (string, bool) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", false
	}
	routing := filterRoutingQuery(parsed.RawQuery)
	parsed.User = nil
	parsed.RawQuery = routing
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String(), true
}

func filterRoutingQuery(rawQuery string) string {
	if rawQuery == "" {
		return ""
	}
	kept := make([]string, 0, strings.Count(rawQuery, "&")+1)
	for _, pair := range strings.Split(rawQuery, "&") {
		if pair == "" {
			continue
		}
		name := pair
		if index := strings.Index(pair, "="); index >= 0 {
			name = pair[:index]
		}
		decoded, err := url.QueryUnescape(name)
		if err != nil {
			decoded = name
		}
		if routingQueryParams[strings.ToLower(decoded)] {
			kept = append(kept, pair)
		}
	}
	return strings.Join(kept, "&")
}
