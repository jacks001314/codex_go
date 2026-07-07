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
	RemoteControlProtocolVersion          = "3"
	RemoteControlServerIDHeader           = "x-codex-server-id"
	RemoteControlServerNameHeader         = "x-codex-name"
	RemoteControlProtocolVersionHeader    = "x-codex-protocol-version"
	RemoteControlSubscribeCursorHeader    = "x-codex-subscribe-cursor"
	RemoteControlWebsocketConnectTimeout  = 30 * time.Second
	RemoteControlReconnectBackoffCap      = 30 * time.Second
	remoteControlReconnectInitialDelay    = 200 * time.Millisecond
	remoteControlReconnectBackoffFactor   = 2.0
	remoteAppServerNotFoundDetail         = "Remote app server not found"
	remoteControlWebsocketAuthHeader      = "authorization"
	remoteControlReconnectJitterMinFactor = 0.9
	remoteControlReconnectJitterMaxFactor = 1.1
)

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
	if subscribeCursor != nil {
		if err := setRemoteControlWebsocketHeader(request.Header, RemoteControlSubscribeCursorHeader, *subscribeCursor); err != nil {
			return nil, err
		}
	}
	return request, nil
}

func NextReconnectDelay(reconnectAttempt *uint64) (time.Duration, bool) {
	return nextReconnectDelayWithJitter(reconnectAttempt, reconnectJitter())
}

func nextReconnectDelayWithJitter(reconnectAttempt *uint64, jitter float64) (time.Duration, bool) {
	if reconnectAttempt == nil {
		var attempt uint64
		reconnectAttempt = &attempt
	}
	delay := remoteControlBackoff(*reconnectAttempt, jitter)
	if delay > RemoteControlReconnectBackoffCap {
		delay = RemoteControlReconnectBackoffCap
	}
	reset := delay == RemoteControlReconnectBackoffCap
	if reset {
		*reconnectAttempt = 0
	} else {
		*reconnectAttempt = *reconnectAttempt + 1
	}
	return delay, reset
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

func remoteControlBackoff(attempt uint64, jitter float64) time.Duration {
	if jitter <= 0 {
		jitter = 1
	}
	exp := uint64(0)
	if attempt > 0 {
		exp = attempt - 1
	}
	if exp > 62 {
		return RemoteControlReconnectBackoffCap
	}
	baseMillis := float64(remoteControlReconnectInitialDelay.Milliseconds()) * math.Pow(remoteControlReconnectBackoffFactor, float64(exp))
	delayMillis := baseMillis * jitter
	if delayMillis > float64(math.MaxInt64/int64(time.Millisecond)) {
		return RemoteControlReconnectBackoffCap
	}
	return time.Duration(delayMillis) * time.Millisecond
}

func reconnectJitter() float64 {
	return remoteControlReconnectJitterMinFactor + rand.Float64()*(remoteControlReconnectJitterMaxFactor-remoteControlReconnectJitterMinFactor)
}
