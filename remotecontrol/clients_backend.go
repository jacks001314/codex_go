package remotecontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const remoteControlClientManagementTimeout = 30 * time.Second

type listRemoteControlClientsResponse struct {
	Items  []remoteControlClientResponse `json:"items"`
	Cursor *string                       `json:"cursor,omitempty"`
}

type remoteControlClientResponse struct {
	ClientID    string  `json:"client_id"`
	DisplayName *string `json:"display_name"`
	DeviceType  *string `json:"device_type"`
	Platform    *string `json:"platform"`
	OSVersion   *string `json:"os_version"`
	DeviceModel *string `json:"device_model"`
	AppVersion  *string `json:"app_version"`
	LastSeenAt  *string `json:"last_seen_at"`
}

func listRemoteControlClients(ctx context.Context, backend *ManagerBackendOptions, params *ClientsListParams) (*ClientsListResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if err := backend.ensureClientManagementReady(); err != nil {
		return nil, err
	}
	clientsURL, err := remoteControlEnvironmentClientsURL(backend.RemoteControlURL, backend.Target, params.EnvironmentID)
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(clientsURL)
	if err != nil {
		return nil, err
	}
	query := parsed.Query()
	if params.Cursor != nil {
		query.Set("cursor", *params.Cursor)
	}
	if params.Limit != nil {
		query.Set("limit", fmt.Sprint(*params.Limit))
	}
	if params.Order != nil {
		query.Set("order", string(*params.Order))
	}
	parsed.RawQuery = query.Encode()
	body, err := sendClientManagementRequestWithRecovery(ctx, backend, http.MethodGet, parsed.String(), "list remote control clients", "client list")
	if err != nil {
		return nil, err
	}
	var response listRemoteControlClientsResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return nil, fmt.Errorf("failed to parse remote control client list response from `%s`: decode error: %w", parsed.String(), err)
	}
	clients := make([]Client, 0, len(response.Items))
	for _, item := range response.Items {
		client, err := item.toClient()
		if err != nil {
			return nil, err
		}
		clients = append(clients, client)
	}
	return &ClientsListResponse{Data: clients, NextCursor: cloneStringPtr(response.Cursor)}, nil
}

func revokeRemoteControlClient(ctx context.Context, backend *ManagerBackendOptions, params *ClientsRevokeParams) (*ClientsRevokeResponse, error) {
	if err := params.Validate(); err != nil {
		return nil, err
	}
	if err := backend.ensureClientManagementReady(); err != nil {
		return nil, err
	}
	clientsURL, err := remoteControlEnvironmentClientsURL(backend.RemoteControlURL, backend.Target, params.EnvironmentID)
	if err != nil {
		return nil, err
	}
	parsed, err := url.Parse(clientsURL)
	if err != nil {
		return nil, err
	}
	appendURLPathSegment(parsed, params.ClientID)
	if _, err := sendClientManagementRequestWithRecovery(ctx, backend, http.MethodDelete, parsed.String(), "revoke remote control client", "client revoke"); err != nil {
		return nil, err
	}
	return &ClientsRevokeResponse{}, nil
}

func sendClientManagementRequestWithRecovery(ctx context.Context, backend *ManagerBackendOptions, method string, requestURL string, action string, responseKind string) ([]byte, error) {
	auth, err := backend.AuthLoader(ctx)
	if err != nil {
		return nil, err
	}
	body, err := sendClientManagementRequestOnce(ctx, backend, auth, method, requestURL, action, responseKind)
	if err == nil {
		return body, nil
	}
	requestError := RemoteControlServerRequestErrorFromError(err)
	if requestError == nil || requestError.StatusCode == nil || *requestError.StatusCode != http.StatusUnauthorized || backend.AuthRecovery == nil {
		return nil, err
	}
	recovered, ok, recoverErr := backend.AuthRecovery(ctx, auth)
	if recoverErr != nil || !ok || recovered == nil {
		return nil, err
	}
	return sendClientManagementRequestOnce(ctx, backend, recovered, method, requestURL, action, responseKind)
}

func sendClientManagementRequestOnce(ctx context.Context, backend *ManagerBackendOptions, auth *RemoteControlConnectionAuth, method string, requestURL string, action string, responseKind string) ([]byte, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	options := normalizeClientManagementOptions(backend.ServerAPIOptions)
	if options.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, options.Timeout)
		defer cancel()
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL, nil)
	if err != nil {
		return nil, err
	}
	request.Header.Set("Accept", "application/json")
	if _, err := auth.ApplyRequest(ctx, request, nil); err != nil {
		return nil, err
	}
	response, err := options.HTTPClient.Do(request)
	if err != nil {
		timedOut := requestTimedOut(ctx, err)
		return nil, newRemoteControlServerRequestError(
			fmt.Sprintf("failed to %s at `%s`: %v", action, requestURL, err),
			nil,
			nil,
			timedOut,
		)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		timedOut := requestTimedOut(ctx, err)
		statusCode := response.StatusCode
		return nil, newRemoteControlServerRequestError(
			fmt.Sprintf("failed to read remote control %s response from `%s`: %v", responseKind, requestURL, err),
			&statusCode,
			nil,
			timedOut,
		)
	}
	if response.StatusCode >= 200 && response.StatusCode < 300 {
		return responseBody, nil
	}
	statusCode := response.StatusCode
	return nil, newRemoteControlClientManagementRequestError(
		fmt.Sprintf("remote control %s failed at `%s`: HTTP %s, %s, body: %s", responseKind, requestURL, response.Status, FormatRemoteControlHeaders(response.Header), PreviewRemoteControlResponseBody(responseBody)),
		&statusCode,
	)
}

func newRemoteControlClientManagementRequestError(message string, statusCode *int) *RemoteControlServerRequestError {
	kind := ErrRemoteControlRequestFailed
	if statusCode != nil {
		switch *statusCode {
		case http.StatusBadRequest:
			kind = ErrInvalidRequest
		case http.StatusUnauthorized, http.StatusForbidden:
			kind = ErrRemoteControlPermissionDenied
		case http.StatusNotFound:
			kind = ErrRemoteControlNotFound
		}
	}
	var statusCopy *int
	if statusCode != nil {
		value := *statusCode
		statusCopy = &value
	}
	return &RemoteControlServerRequestError{
		Message:    message,
		StatusCode: statusCopy,
		Kind:       kind,
	}
}

func normalizeClientManagementOptions(opts *ServerAPIOptions) *ServerAPIOptions {
	options := normalizeServerAPIOptions(opts)
	if options.Timeout <= 0 || options.Timeout == remoteControlEnrollTimeout {
		options.Timeout = remoteControlClientManagementTimeout
	}
	return options
}

func remoteControlEnvironmentClientsURL(remoteControlURL string, target *RemoteControlTarget, environmentID string) (string, error) {
	base, err := remoteControlBaseURL(remoteControlURL, target)
	if err != nil {
		return "", err
	}
	envURL, err := remoteControlEndpointURL(base, "wham/remote/control/environments", firstNonEmpty(remoteControlURL, targetURLForError(target)))
	if err != nil {
		return "", err
	}
	appendURLPathSegment(envURL, environmentID)
	appendURLPathSegment(envURL, "clients")
	return envURL.String(), nil
}

func remoteControlBaseURL(remoteControlURL string, target *RemoteControlTarget) (*url.URL, error) {
	if strings.TrimSpace(remoteControlURL) != "" {
		return NormalizeRemoteControlBaseURL(remoteControlURL)
	}
	if target == nil {
		return nil, fmt.Errorf("%w: remote control target is nil", ErrInvalidRequest)
	}
	for _, raw := range []string{target.EnrollURL, target.RefreshURL, target.PairURL, target.PairStatusURL, target.WebSocketURL} {
		if base := remoteControlBaseURLFromEndpoint(raw); base != nil {
			return base, nil
		}
	}
	return nil, fmt.Errorf("%w: remote control base URL is unavailable", ErrInvalidRequest)
}

func remoteControlBaseURLFromEndpoint(raw string) *url.URL {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil
	}
	switch parsed.Scheme {
	case "ws":
		parsed.Scheme = "http"
	case "wss":
		parsed.Scheme = "https"
	}
	for _, suffix := range []string{
		"/wham/remote/control/server/enroll",
		"/wham/remote/control/server/refresh",
		"/wham/remote/control/server/pair/status",
		"/wham/remote/control/server/pair",
		"/wham/remote/control/server",
	} {
		if strings.HasSuffix(parsed.Path, suffix) {
			parsed.Path = strings.TrimSuffix(parsed.Path, suffix) + "/"
			parsed.RawPath = ""
			parsed.RawQuery = ""
			parsed.Fragment = ""
			return parsed
		}
	}
	return nil
}

func appendURLPathSegment(u *url.URL, segment string) {
	if u == nil {
		return
	}
	if !strings.HasSuffix(u.Path, "/") {
		u.Path += "/"
	}
	escapedSegment := url.PathEscape(segment)
	u.Path += segment
	if u.RawPath != "" || escapedSegment != segment {
		rawPath := u.EscapedPath()
		if !strings.HasSuffix(rawPath, "/") {
			rawPath += "/"
		}
		u.RawPath = rawPath + escapedSegment
	}
}

func (r remoteControlClientResponse) toClient() (Client, error) {
	var lastSeenAt *int64
	if r.LastSeenAt != nil && strings.TrimSpace(*r.LastSeenAt) != "" {
		parsed, err := time.Parse(time.RFC3339Nano, *r.LastSeenAt)
		if err != nil {
			return Client{}, fmt.Errorf("failed to parse remote control client last_seen_at `%s`: %w", *r.LastSeenAt, err)
		}
		value := parsed.Unix()
		lastSeenAt = &value
	}
	return Client{
		ClientID:    r.ClientID,
		DisplayName: cloneStringPtr(r.DisplayName),
		DeviceType:  cloneStringPtr(r.DeviceType),
		Platform:    cloneStringPtr(r.Platform),
		OSVersion:   cloneStringPtr(r.OSVersion),
		DeviceModel: cloneStringPtr(r.DeviceModel),
		AppVersion:  cloneStringPtr(r.AppVersion),
		LastSeenAt:  lastSeenAt,
	}, nil
}

func targetURLForError(target *RemoteControlTarget) string {
	if target == nil {
		return ""
	}
	return firstNonEmpty(target.WebSocketURL, target.EnrollURL, target.RefreshURL, target.PairURL, target.PairStatusURL)
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}
