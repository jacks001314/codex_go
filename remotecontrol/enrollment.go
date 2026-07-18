package remotecontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	remoteControlPairingTimeout         = 30 * time.Second
	remoteControlResponseBodyMaxBytes   = 4096
	remoteControlServerTokenRefreshSkew = 5 * time.Minute
	remoteControlRequestIDHeader        = "x-request-id"
	remoteControlOAIRequestIDHeader     = "x-oai-request-id"
	remoteControlCFRayHeader            = "cf-ray"
)

type RemoteControlServerTokenRefreshRequirement string

const (
	ServerTokenRefreshRequired  RemoteControlServerTokenRefreshRequirement = "required"
	ServerTokenRefreshProactive RemoteControlServerTokenRefreshRequirement = "proactive"
	ServerTokenRefreshNotNeeded RemoteControlServerTokenRefreshRequirement = "not_needed"
)

type Enrollment struct {
	RemoteControlTarget *RemoteControlTarget
	AccountID           string
	EnvironmentID       string
	ServerID            string
	ServerName          string
	RemoteControlToken  *string
	ExpiresAt           *time.Time
	NextRefreshAt       *time.Time
}

func (e *Enrollment) ServerTokenRefreshRequirement() RemoteControlServerTokenRefreshRequirement {
	return e.ServerTokenRefreshRequirementAt(time.Now().UTC())
}

func (e *Enrollment) ShouldRefreshServerToken() bool {
	return e.ServerTokenRefreshRequirement() != ServerTokenRefreshNotNeeded
}

func (e *Enrollment) ServerTokenRefreshRequirementAt(now time.Time) RemoteControlServerTokenRefreshRequirement {
	if e == nil || e.RemoteControlToken == nil || strings.TrimSpace(*e.RemoteControlToken) == "" || e.ExpiresAt == nil {
		return ServerTokenRefreshRequired
	}
	now = now.UTC()
	expiresAt := e.ExpiresAt.UTC()
	if !expiresAt.After(now) {
		return ServerTokenRefreshRequired
	}
	if expiresAt.After(now.Add(remoteControlServerTokenRefreshSkew)) {
		return ServerTokenRefreshNotNeeded
	}
	if e.NextRefreshAt != nil && e.NextRefreshAt.UTC().After(now) {
		return ServerTokenRefreshNotNeeded
	}
	return ServerTokenRefreshProactive
}

func (e *Enrollment) ClearServerToken() {
	if e == nil {
		return
	}
	e.RemoteControlToken = nil
	e.ExpiresAt = nil
}

func (e *Enrollment) UpdateServerToken(url string, token string, expiresAt string) error {
	if e == nil {
		return fmt.Errorf("%w: enrollment is nil", ErrInvalidRequest)
	}
	parsed, err := time.Parse(time.RFC3339Nano, expiresAt)
	if err != nil {
		return fmt.Errorf("failed to parse remote control server token expiry from `%s`: %w", url, err)
	}
	e.RemoteControlToken = stringPtrIfNotEmpty(strings.TrimSpace(token))
	expiry := parsed.UTC()
	e.ExpiresAt = &expiry
	e.NextRefreshAt = nil
	return nil
}

func (e *Enrollment) StartPairing(ctx context.Context, params *PairingStartParams, opts *PairingOptions) (*PairingStartResponse, error) {
	if params == nil {
		params = &PairingStartParams{}
	}
	if err := e.ensurePairingAvailable(); err != nil {
		return nil, err
	}
	options := normalizePairingOptions(opts)
	request := &StartRemoteControlPairingRequest{ManualCode: params.ManualCode}
	response, err := sendRemoteControlPairingRequest[StartRemoteControlPairingResponse](
		ctx,
		options,
		e.RemoteControlTarget.PairURL,
		stringValue(e.RemoteControlToken),
		request,
		"start remote control pairing",
		"pairing",
		func(statusCode int) error {
			switch statusCode {
			case http.StatusUnauthorized, http.StatusForbidden:
				return ErrRemoteControlPermissionDenied
			case http.StatusNotFound:
				return ErrRemoteControlNotFound
			default:
				return ErrRemoteControlRequestFailed
			}
		},
	)
	if err != nil {
		return nil, err
	}
	if response.ServerID != e.ServerID || response.EnvironmentID != e.EnvironmentID {
		return nil, fmt.Errorf("remote control pairing returned mismatched enrollment: expected server_id=%s, environment_id=%s; got server_id=%s, environment_id=%s", e.ServerID, e.EnvironmentID, response.ServerID, response.EnvironmentID)
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, response.ExpiresAt)
	if err != nil {
		return nil, fmt.Errorf("failed to parse remote control pairing response from `%s`: expires_at parse error: %w", e.RemoteControlTarget.PairURL, err)
	}
	return &PairingStartResponse{
		PairingCode:       response.PairingCode,
		ManualPairingCode: cloneStringPtr(response.ManualPairingCode),
		EnvironmentID:     response.EnvironmentID,
		ExpiresAt:         expiresAt.Unix(),
	}, nil
}

func (e *Enrollment) PairingStatus(ctx context.Context, params *PairingStatusParams, opts *PairingOptions) (*PairingStatusResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if err := e.ensurePairingAvailable(); err != nil {
		return nil, err
	}
	options := normalizePairingOptions(opts)
	request := &RemoteControlPairingStatusRequest{
		PairingCode:       cloneStringPtr(params.PairingCode),
		ManualPairingCode: cloneStringPtr(params.ManualPairingCode),
	}
	response, err := sendRemoteControlPairingRequest[RemoteControlPairingStatusResponse](
		ctx,
		options,
		e.RemoteControlTarget.PairStatusURL,
		stringValue(e.RemoteControlToken),
		request,
		"check remote control pairing status",
		"pairing status",
		func(statusCode int) error {
			switch statusCode {
			case http.StatusUnauthorized, http.StatusForbidden:
				return ErrRemoteControlPermissionDenied
			case http.StatusNotFound, http.StatusGone:
				return ErrInvalidRequest
			default:
				return ErrRemoteControlRequestFailed
			}
		},
	)
	if err != nil {
		return nil, err
	}
	return &PairingStatusResponse{Claimed: response.Claimed}, nil
}

func (e *Enrollment) ensurePairingAvailable() error {
	if e == nil || e.RemoteControlTarget == nil {
		return fmt.Errorf("%w: enrollment is nil", ErrInvalidRequest)
	}
	if e.ServerTokenRefreshRequirement() == ServerTokenRefreshRequired {
		return ErrRemoteControlPairingUnavailable
	}
	if e.RemoteControlToken == nil || strings.TrimSpace(*e.RemoteControlToken) == "" {
		return ErrRemoteControlPairingUnavailable
	}
	return nil
}

type PairingOptions struct {
	HTTPClient HTTPDoer
	Timeout    time.Duration
}

func normalizePairingOptions(opts *PairingOptions) *PairingOptions {
	if opts == nil {
		opts = &PairingOptions{}
	}
	out := *opts
	if out.HTTPClient == nil {
		out.HTTPClient = http.DefaultClient
	}
	if out.Timeout <= 0 {
		out.Timeout = remoteControlPairingTimeout
	}
	return &out
}

func sendRemoteControlPairingRequest[Response any](
	ctx context.Context,
	opts *PairingOptions,
	url string,
	token string,
	request any,
	action string,
	responseKind string,
	statusKind func(statusCode int) error,
) (*Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	}
	body, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	httpRequest, err := newJSONPostRequest(ctx, url, body)
	if err != nil {
		return nil, err
	}
	httpRequest.Header.Set("Authorization", "Bearer "+strings.TrimSpace(token))
	response, err := opts.HTTPClient.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("failed to %s at `%s`: %w", action, url, err)
	}
	defer response.Body.Close()

	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read remote control %s response from `%s`: %w", responseKind, url, err)
	}
	bodyPreview := PreviewRemoteControlResponseBody(responseBody)
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		kind := statusKind(response.StatusCode)
		return nil, fmt.Errorf("%w: remote control %s failed at `%s`: HTTP %s, %s, body: %s", kind, responseKind, url, response.Status, FormatRemoteControlHeaders(response.Header), bodyPreview)
	}
	var decoded Response
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return nil, fmt.Errorf("failed to parse remote control %s response from `%s`: HTTP %s, %s, body: %s, decode error: %w", responseKind, url, response.Status, FormatRemoteControlHeaders(response.Header), bodyPreview, err)
	}
	return &decoded, nil
}

func PreviewRemoteControlResponseBody(body []byte) string {
	text := strings.ToValidUTF8(string(body), "\uFFFD")
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return "<empty>"
	}
	redacted := redactRemoteControlResponseBody(trimmed)
	if len(redacted) <= remoteControlResponseBodyMaxBytes {
		return redacted
	}
	cut := remoteControlResponseBodyMaxBytes
	for cut > 0 && !utf8Boundary(redacted, cut) {
		cut--
	}
	return redacted[:cut] + "..."
}

func redactRemoteControlResponseBody(body string) string {
	var value any
	if err := json.Unmarshal([]byte(body), &value); err != nil {
		return body
	}
	object, ok := value.(map[string]any)
	if !ok {
		return body
	}
	for _, field := range []string{"remote_control_token", "pairing_code", "manual_pairing_code"} {
		if _, ok := object[field]; ok {
			object[field] = "<redacted>"
		}
	}
	redacted, err := json.Marshal(object)
	if err != nil {
		return body
	}
	return string(redacted)
}

func FormatRemoteControlHeaders(headers http.Header) string {
	requestID := firstHeaderValue(headers, remoteControlRequestIDHeader, remoteControlOAIRequestIDHeader)
	if requestID == "" {
		requestID = "<none>"
	}
	cfRay := firstHeaderValue(headers, remoteControlCFRayHeader)
	if cfRay == "" {
		cfRay = "<none>"
	}
	return fmt.Sprintf("request-id: %s, cf-ray: %s", requestID, cfRay)
}

func firstHeaderValue(headers http.Header, keys ...string) string {
	for _, key := range keys {
		if value := headers.Get(key); value != "" {
			return value
		}
	}
	return ""
}

func utf8Boundary(value string, index int) bool {
	if index <= 0 || index >= len(value) {
		return true
	}
	return (value[index] & 0xc0) != 0x80
}
