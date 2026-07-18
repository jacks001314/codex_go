package remotecontrol

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

type RemoteControlWebsocketDialFunc func(ctx context.Context, url string, options *websocket.DialOptions) (*websocket.Conn, *http.Response, error)

type RemoteControlWebsocketConnectOptions struct {
	SubscribeCursor *string
	Dial            RemoteControlWebsocketDialFunc
	Timeout         time.Duration
}

func (m *Manager) ConnectWebsocketContext(ctx context.Context, options *RemoteControlWebsocketConnectOptions) (*websocket.Conn, *http.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if options == nil {
		options = &RemoteControlWebsocketConnectOptions{}
	}
	m.mu.Lock()
	backend := cloneManagerBackendOptions(m.backend)
	m.mu.Unlock()
	if backend == nil {
		return nil, nil, fmt.Errorf("%w: remote control backend is nil", ErrInvalidRequest)
	}
	auth, enrollment, err := m.prepareWebsocketEnrollment(ctx, backend)
	if err != nil {
		return nil, nil, err
	}
	_ = auth
	request, err := BuildRemoteControlWebsocketRequest(backend.Target.WebSocketURL, enrollment, m.installationIDSnapshot(), options.SubscribeCursor)
	if err != nil {
		return nil, nil, err
	}
	timeout := options.Timeout
	if timeout <= 0 {
		timeout = RemoteControlWebsocketConnectTimeout
	}
	connectCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	dial := options.Dial
	if dial == nil {
		dial = websocket.Dial
	}
	conn, response, err := dial(connectCtx, request.URL.String(), &websocket.DialOptions{
		HTTPHeader: request.Header,
	})
	if err != nil {
		body := readRemoteControlWebsocketErrorBody(response)
		if WebsocketResponseReportsMissingRemoteAppServer(response, body) {
			if replaceErr := m.replaceWebsocketEnrollmentIfMatches(ctx, backend, auth, enrollment); replaceErr != nil {
				return nil, response, replaceErr
			}
		}
		if response != nil && (response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden) {
			if clearErr := m.clearRemoteControlServerTokenIfMatches(enrollment); clearErr != nil {
				return nil, response, clearErr
			}
			return nil, response, fmt.Errorf("remote control websocket auth failed with HTTP %s; refreshing server token before reconnect", remoteControlHTTPStatus(response))
		}
		return nil, response, errors.New(FormatRemoteControlWebsocketConnectError(request.URL.String(), response, body, err))
	}
	return conn, response, nil
}

func readRemoteControlWebsocketErrorBody(response *http.Response) []byte {
	if response == nil || response.Body == nil {
		return nil
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, remoteControlResponseBodyMaxBytes+1))
	if err != nil {
		return nil
	}
	return body
}

func remoteControlHTTPStatus(response *http.Response) string {
	if response == nil {
		return ""
	}
	if response.Status != "" {
		return response.Status
	}
	if text := http.StatusText(response.StatusCode); text != "" {
		return fmt.Sprintf("%d %s", response.StatusCode, text)
	}
	return fmt.Sprintf("%d", response.StatusCode)
}
