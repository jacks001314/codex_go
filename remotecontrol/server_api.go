package remotecontrol

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	remoteControlEnrollTimeout                       = 30 * time.Second
	remoteControlServerTokenRefreshBackoffMinSeconds = 24
	remoteControlServerTokenRefreshBackoffMaxSeconds = 36
	RemoteControlInstallationIDHeader                = "x-codex-installation-id"
)

type HTTPDoer interface {
	Do(request *http.Request) (*http.Response, error)
}

type ServerAPIOptions struct {
	HTTPClient       HTTPDoer
	Timeout          time.Duration
	Now              func() time.Time
	Backoff          func() time.Duration
	OS               string
	Arch             string
	AppServerVersion string
}

type RemoteControlServerRequestError struct {
	Message    string
	StatusCode *int
	RetryAt    *time.Time
	Kind       error
}

func (e *RemoteControlServerRequestError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func (e *RemoteControlServerRequestError) Is(target error) bool {
	return e != nil && e.Kind != nil && target == e.Kind
}

func (e *RemoteControlServerRequestError) Transient() bool {
	if e == nil {
		return false
	}
	if errors.Is(e, ErrRemoteControlTimedOut) {
		return true
	}
	if e.StatusCode == nil {
		return true
	}
	return *e.StatusCode == http.StatusTooManyRequests || *e.StatusCode >= 500
}

func EnrollRemoteControlServer(ctx context.Context, target *RemoteControlTarget, auth *RemoteControlConnectionAuth, installationID string, serverName string, opts *ServerAPIOptions) (*Enrollment, error) {
	if target == nil {
		return nil, fmt.Errorf("%w: remote control target is nil", ErrInvalidRequest)
	}
	options := normalizeServerAPIOptions(opts)
	request := &EnrollRemoteServerRequest{
		Name:             serverName,
		OS:               options.OS,
		Arch:             options.Arch,
		AppServerVersion: options.AppServerVersion,
		InstallationID:   installationID,
	}
	response, err := sendRemoteControlServerRequest[EnrollRemoteServerResponse](
		ctx,
		target.EnrollURL,
		auth,
		installationID,
		request,
		"enroll",
		"server enrollment",
		options,
	)
	if err != nil {
		return nil, err
	}
	enrollment := &Enrollment{
		RemoteControlTarget: cloneRemoteControlTarget(target),
		AccountID:           strings.TrimSpace(auth.AccountID),
		EnvironmentID:       response.EnvironmentID,
		ServerID:            response.ServerID,
		ServerName:          serverName,
	}
	if err := enrollment.UpdateServerToken(target.EnrollURL, response.RemoteControlToken, response.ExpiresAt); err != nil {
		return nil, err
	}
	return enrollment, nil
}

func RefreshRemoteControlServer(ctx context.Context, auth *RemoteControlConnectionAuth, installationID string, enrollment *Enrollment, opts *ServerAPIOptions) error {
	if enrollment == nil || enrollment.RemoteControlTarget == nil {
		return fmt.Errorf("%w: enrollment is nil", ErrInvalidRequest)
	}
	options := normalizeServerAPIOptions(opts)
	now := options.Now().UTC()
	requirement := enrollment.ServerTokenRefreshRequirementAt(now)
	if requirement == ServerTokenRefreshNotNeeded {
		return nil
	}
	if requirement == ServerTokenRefreshRequired && enrollment.NextRefreshAt != nil && enrollment.NextRefreshAt.UTC().After(now) {
		return fmt.Errorf("%w until %s", ErrRemoteControlRefreshDeferred, enrollment.NextRefreshAt.UTC().Format(time.RFC3339))
	}

	refreshURL := enrollment.RemoteControlTarget.RefreshURL
	request := &RefreshRemoteServerRequest{
		ServerID:       enrollment.ServerID,
		InstallationID: installationID,
	}
	response, err := sendRemoteControlServerRequest[EnrollRemoteServerResponse](
		ctx,
		refreshURL,
		auth,
		installationID,
		request,
		"refresh",
		"server refresh",
		options,
	)
	if err != nil {
		requestError := RemoteControlServerRequestErrorFromError(err)
		if requestError == nil || !requestError.Transient() {
			return err
		}
		now = options.Now().UTC()
		refreshRequired := enrollment.ServerTokenRefreshRequirementAt(now) == ServerTokenRefreshRequired
		_, nextRefreshAt := refreshDeferral(requestError.RetryAt, now, options.Backoff)
		enrollment.NextRefreshAt = &nextRefreshAt
		if refreshRequired {
			return err
		}
		return nil
	}
	if response.ServerID != enrollment.ServerID || response.EnvironmentID != enrollment.EnvironmentID {
		return fmt.Errorf("remote control server refresh returned mismatched enrollment: expected server_id=%s, environment_id=%s; got server_id=%s, environment_id=%s", enrollment.ServerID, enrollment.EnvironmentID, response.ServerID, response.EnvironmentID)
	}
	return enrollment.UpdateServerToken(refreshURL, response.RemoteControlToken, response.ExpiresAt)
}

func sendRemoteControlServerRequest[Response any](
	ctx context.Context,
	url string,
	auth *RemoteControlConnectionAuth,
	installationID string,
	request any,
	action string,
	responseKind string,
	opts *ServerAPIOptions,
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
	httpRequest.Header.Set(RemoteControlInstallationIDHeader, installationID)
	body, err = auth.ApplyRequest(ctx, httpRequest, body)
	if err != nil {
		return nil, err
	}
	response, err := opts.HTTPClient.Do(httpRequest)
	if err != nil {
		timedOut := requestTimedOut(ctx, err)
		return nil, newRemoteControlServerRequestError(
			fmt.Sprintf("failed to %s remote control server at `%s`: %v", action, url, err),
			nil,
			nil,
			timedOut,
		)
	}
	defer response.Body.Close()

	headers := response.Header.Clone()
	statusCode := response.StatusCode
	status := response.Status
	receivedAt := opts.Now().UTC()
	retryAt := ParseRetryAfter(headers, receivedAt)
	responseBody, err := io.ReadAll(response.Body)
	if err != nil {
		timedOut := requestTimedOut(ctx, err)
		return nil, newRemoteControlServerRequestError(
			fmt.Sprintf("failed to read remote control %s response from `%s`: %v", responseKind, url, err),
			&statusCode,
			retryAt,
			timedOut,
		)
	}
	bodyPreview := PreviewRemoteControlResponseBody(responseBody)
	if statusCode < 200 || statusCode >= 300 {
		return nil, newRemoteControlServerRequestError(
			fmt.Sprintf("remote control %s failed at `%s`: HTTP %s, %s, body: %s", responseKind, url, status, FormatRemoteControlHeaders(headers), bodyPreview),
			&statusCode,
			retryAt,
			false,
		)
	}
	var decoded Response
	if err := json.Unmarshal(responseBody, &decoded); err != nil {
		return nil, fmt.Errorf("failed to parse remote control %s response from `%s`: HTTP %s, %s, body: %s, decode error: %w", responseKind, url, status, FormatRemoteControlHeaders(headers), bodyPreview, err)
	}
	_ = body
	return &decoded, nil
}

func newJSONPostRequest(ctx context.Context, url string, body []byte) (*http.Request, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	return request, nil
}

func newRemoteControlServerRequestError(message string, statusCode *int, retryAt *time.Time, timedOut bool) *RemoteControlServerRequestError {
	kind := ErrRemoteControlRequestFailed
	if statusCode != nil {
		switch *statusCode {
		case http.StatusUnauthorized, http.StatusForbidden:
			kind = ErrRemoteControlPermissionDenied
		case http.StatusNotFound:
			kind = ErrRemoteControlNotFound
		default:
			if timedOut && (*statusCode < 400 || *statusCode >= 500) {
				kind = ErrRemoteControlTimedOut
			}
		}
	} else if timedOut {
		kind = ErrRemoteControlTimedOut
	}
	var statusCopy *int
	if statusCode != nil {
		value := *statusCode
		statusCopy = &value
	}
	var retryCopy *time.Time
	if retryAt != nil {
		value := retryAt.UTC()
		retryCopy = &value
	}
	return &RemoteControlServerRequestError{
		Message:    message,
		StatusCode: statusCopy,
		RetryAt:    retryCopy,
		Kind:       kind,
	}
}

func RemoteControlServerRequestErrorFromError(err error) *RemoteControlServerRequestError {
	var requestError *RemoteControlServerRequestError
	if errors.As(err, &requestError) {
		return requestError
	}
	return nil
}

func ParseRetryAfter(headers http.Header, receivedAt time.Time) *time.Time {
	value := strings.TrimSpace(headers.Get("Retry-After"))
	if value == "" {
		return nil
	}
	if seconds, err := strconv.ParseInt(value, 10, 64); err == nil && seconds >= 0 {
		retryAt := receivedAt.UTC().Add(time.Duration(seconds) * time.Second)
		if retryAt.After(receivedAt.UTC()) {
			return &retryAt
		}
		return nil
	}
	retryAt, err := http.ParseTime(value)
	if err != nil {
		return nil
	}
	retryAt = retryAt.UTC()
	if retryAt.After(receivedAt.UTC()) {
		return &retryAt
	}
	return nil
}

func refreshDeferral(retryAt *time.Time, now time.Time, backoff func() time.Duration) (time.Duration, time.Time) {
	now = now.UTC()
	if retryAt != nil && retryAt.UTC().After(now) {
		delay := retryAt.UTC().Sub(now)
		if delay > 0 {
			return delay, retryAt.UTC()
		}
	}
	if backoff == nil {
		backoff = remoteControlServerTokenRefreshBackoff
	}
	delay := backoff()
	if delay <= 0 {
		delay = remoteControlServerTokenRefreshBackoff()
	}
	return delay, now.Add(delay)
}

func remoteControlServerTokenRefreshBackoff() time.Duration {
	span := int64(remoteControlServerTokenRefreshBackoffMaxSeconds - remoteControlServerTokenRefreshBackoffMinSeconds + 1)
	value, err := rand.Int(rand.Reader, big.NewInt(span))
	if err != nil {
		return time.Duration(remoteControlServerTokenRefreshBackoffMinSeconds) * time.Second
	}
	return time.Duration(remoteControlServerTokenRefreshBackoffMinSeconds+int(value.Int64())) * time.Second
}

func normalizeServerAPIOptions(opts *ServerAPIOptions) *ServerAPIOptions {
	if opts == nil {
		opts = &ServerAPIOptions{}
	}
	out := *opts
	if out.HTTPClient == nil {
		out.HTTPClient = http.DefaultClient
	}
	if out.Timeout <= 0 {
		out.Timeout = remoteControlEnrollTimeout
	}
	if out.Now == nil {
		out.Now = func() time.Time { return time.Now().UTC() }
	}
	if out.Backoff == nil {
		out.Backoff = remoteControlServerTokenRefreshBackoff
	}
	if strings.TrimSpace(out.OS) == "" {
		out.OS = runtime.GOOS
	}
	if strings.TrimSpace(out.Arch) == "" {
		out.Arch = runtime.GOARCH
	}
	if strings.TrimSpace(out.AppServerVersion) == "" {
		out.AppServerVersion = "0.0.0"
	}
	return &out
}

func requestTimedOut(ctx context.Context, err error) bool {
	return errors.Is(ctx.Err(), context.DeadlineExceeded) ||
		errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, context.DeadlineExceeded) ||
		strings.Contains(strings.ToLower(err.Error()), "timeout")
}

func cloneRemoteControlTarget(target *RemoteControlTarget) *RemoteControlTarget {
	if target == nil {
		return nil
	}
	clone := *target
	return &clone
}
