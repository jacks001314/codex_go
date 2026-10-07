package remotecontrol

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

const (
	RemoteControlProtocolVersion         = "3"
	RemoteControlServerIDHeader          = "x-codex-server-id"
	RemoteControlServerNameHeader        = "x-codex-name"
	RemoteControlProtocolVersionHeader   = "x-codex-protocol-version"
	RemoteControlSubscribeCursorHeader   = "x-codex-subscribe-cursor"
	RemoteControlWebsocketConnectTimeout = 30 * time.Second
	RemoteControlReconnectBackoffCap     = 30 * time.Second
	// Rust #50348 `9d2b60303e`: automatic remote control reconnects start at
	// 5s with jitter between half and all of the delay.
	remoteControlReconnectBackoffInitial  = 5 * time.Second
	remoteAppServerNotFoundDetail         = "Remote app server not found"
	remoteControlWebsocketAuthHeader      = "authorization"
	remoteControlReconnectJitterMinFactor = 0.5
	remoteControlReconnectJitterMaxFactor = 1.0
)

// remoteControlReconnectBackoffResetAfter mirrors Rust #50348
// `REMOTE_CONTROL_RECONNECT_BACKOFF_RESET_AFTER`: only a connection that stayed
// healthy this long clears the reconnect backoff. A variable (not a constant)
// so tests can shorten the healthy-connection window.
var remoteControlReconnectBackoffResetAfter = 60 * time.Second

func BuildRemoteControlWebsocketRequest(websocketURL string, enrollment *Enrollment, installationID string, subscribeCursor *string) (*http.Request, error) {
	if enrollment == nil {
		return nil, fmt.Errorf("%w: enrollment is nil", ErrInvalidRequest)
	}
	request, err := http.NewRequest(http.MethodGet, websocketURL, nil)
	if err != nil {
		return nil, fmt.Errorf("invalid remote control websocket URL `%s`: %w", websocketURL, err)
	}
	if request.URL == nil || (request.URL.Scheme != "ws" && request.URL.Scheme != "wss") {
		return nil, fmt.Errorf("%w: invalid remote control websocket URL `%s`", ErrInvalidRequest, websocketURL)
	}
	if err := setRemoteControlWebsocketHeader(request.Header, RemoteControlServerIDHeader, enrollment.ServerID); err != nil {
		return nil, err
	}
	if err := setRemoteControlWebsocketHeader(request.Header, RemoteControlServerNameHeader, base64.StdEncoding.EncodeToString([]byte(enrollment.ServerName))); err != nil {
		return nil, err
	}
	if err := setRemoteControlWebsocketHeader(request.Header, RemoteControlProtocolVersionHeader, RemoteControlProtocolVersion); err != nil {
		return nil, err
	}
	token := strings.TrimSpace(stringValue(enrollment.RemoteControlToken))
	if token == "" {
		return nil, fmt.Errorf("missing remote control server token")
	}
	if err := setRemoteControlWebsocketHeader(request.Header, remoteControlWebsocketAuthHeader, "Bearer "+token); err != nil {
		return nil, err
	}
	if err := setRemoteControlWebsocketHeader(request.Header, RemoteControlInstallationIDHeader, installationID); err != nil {
		return nil, err
	}
	if hostDeviceKind := HostDeviceKind(); hostDeviceKind != "" {
		if err := setRemoteControlWebsocketHeader(request.Header, RemoteControlHostDeviceKindHeader, hostDeviceKind); err != nil {
			return nil, err
		}
	}
	if subscribeCursor != nil {
		if err := setRemoteControlWebsocketHeader(request.Header, RemoteControlSubscribeCursorHeader, *subscribeCursor); err != nil {
			return nil, err
		}
	}
	return request, nil
}

func NextReconnectDelay(reconnectAttempt *uint64) time.Duration {
	return nextReconnectDelayWithJitter(reconnectAttempt, reconnectJitter())
}

func nextReconnectDelayWithJitter(reconnectAttempt *uint64, jitter float64) time.Duration {
	if reconnectAttempt == nil {
		var attempt uint64
		reconnectAttempt = &attempt
	}
	if jitter <= 0 {
		jitter = 1
	}
	backoff := remoteControlReconnectBackoff(*reconnectAttempt)
	// Rust #50348 `9d2b60303e`: advance (saturating) on every attempt, even at
	// the cap, matching `reconnect_attempt.saturating_add(1)`. Only a healthy
	// connection (>= 60s) or an auth change resets the counter.
	if *reconnectAttempt < math.MaxUint64 {
		*reconnectAttempt = *reconnectAttempt + 1
	}
	// Jitter between half and all of the backoff spreads reconnects after a
	// shared outage without dropping below the floor.
	return time.Duration(float64(backoff) * jitter)
}

// remoteControlReconnectBackoff mirrors the exponential term of Rust #50348
// `9d2b60303e` (remote_control::websocket::next_reconnect_delay):
// `REMOTE_CONTROL_RECONNECT_BACKOFF_INITIAL.saturating_mul(2u32.saturating_pow(exponent))`
// capped at 30s. `attempt` saturates through u32 like Rust's
// `u32::try_from(reconnect_attempt).unwrap_or(u32::MAX)`.
func remoteControlReconnectBackoff(attempt uint64) time.Duration {
	exponent := attempt
	if exponent > math.MaxUint32 {
		exponent = math.MaxUint32
	}
	// 2^exponent saturates at u32::MAX, exactly like `u32::saturating_pow`.
	factor := uint64(math.MaxUint32)
	if exponent < 32 {
		factor = uint64(1) << exponent
	}
	initialNanos := uint64(remoteControlReconnectBackoffInitial)
	capNanos := uint64(RemoteControlReconnectBackoffCap)
	// Rust multiplies as a `Duration` (saturating at `Duration::MAX`) before
	// taking the minimum with the 30s cap, so any factor that would reach or
	// exceed the cap yields the cap; this also keeps the product inside int64.
	if factor >= capNanos/initialNanos {
		return RemoteControlReconnectBackoffCap
	}
	return time.Duration(initialNanos * factor)
}

func WebsocketResponseReportsMissingRemoteAppServer(response *http.Response, body []byte) bool {
	if response == nil || response.StatusCode != http.StatusNotFound || len(body) == 0 {
		return false
	}
	var payload struct {
		Detail string `json:"detail"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return false
	}
	return payload.Detail == remoteAppServerNotFoundDetail
}

func FormatRemoteControlWebsocketConnectError(websocketURL string, response *http.Response, body []byte, err error) string {
	message := fmt.Sprintf("failed to connect app-server remote control websocket `%s`", websocketURL)
	if err != nil {
		message += ": " + err.Error()
	}
	if response == nil {
		return message
	}
	message += fmt.Sprintf(", %s", FormatRemoteControlHeaders(response.Header))
	if len(body) > 0 {
		message += ", body: " + PreviewRemoteControlResponseBody(body)
	}
	return message
}

func setRemoteControlWebsocketHeader(headers http.Header, name string, value string) error {
	if !validHTTPHeaderValue(value) {
		return fmt.Errorf("%w: invalid remote control header `%s`", ErrInvalidRequest, name)
	}
	headers.Set(name, value)
	return nil
}

func reconnectJitter() float64 {
	return remoteControlReconnectJitterMinFactor + rand.Float64()*(remoteControlReconnectJitterMaxFactor-remoteControlReconnectJitterMinFactor)
}
