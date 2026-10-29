package codexapi

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
)

// ResponsesWebsocketTrackingHeaders are the allowlisted upgrade headers a
// handshake probe retains for diagnostics. Every other header value can carry
// credentials and is dropped (Rust #57597).
var ResponsesWebsocketTrackingHeaders = []string{
	"x-request-id",
	"x-oai-request-id",
	"cf-ray",
	"x-ms-request-id",
}

// ResponsesWebsocketClose is close-frame evidence captured by a probe. String
// keeps the code and hides the server-supplied reason.
type ResponsesWebsocketClose struct {
	Code   string
	Reason string
}

func (c ResponsesWebsocketClose) String() string {
	return fmt.Sprintf("ResponsesWebsocketClose{code:%s reason:[redacted]}", c.Code)
}

// ResponsesWebsocketProbe is the evidence a successful handshake-only probe
// leaves behind. String redacts the URL and header values.
type ResponsesWebsocketProbe struct {
	URL                string
	Status             int
	Headers            http.Header
	ReasoningIncluded  bool
	ServerModelPresent bool
	ImmediateClose     *ResponsesWebsocketClose
}

func (p *ResponsesWebsocketProbe) String() string {
	if p == nil {
		return "ResponsesWebsocketProbe<nil>"
	}
	return fmt.Sprintf(
		"ResponsesWebsocketProbe{status:%d reasoning_included:%t server_model_present:%t immediate_close:%v url:[redacted] headers:[redacted]}",
		p.Status, p.ReasoningIncluded, p.ServerModelPresent, p.ImmediateClose,
	)
}

// ResponsesWebsocketProbeFailureStage is the failure boundary established by
// typed evidence.
type ResponsesWebsocketProbeFailureStage string

const (
	ResponsesWebsocketProbeFailureStageTLS     ResponsesWebsocketProbeFailureStage = "tls"
	ResponsesWebsocketProbeFailureStageUnknown ResponsesWebsocketProbeFailureStage = "unknown"
)

// ResponsesWebsocketProbeError preserves the underlying probe error together
// with any upgrade evidence received before it. String redacts wire values.
type ResponsesWebsocketProbeError struct {
	Err     error
	Stage   ResponsesWebsocketProbeFailureStage
	Status  int
	Headers http.Header
}

func (e *ResponsesWebsocketProbeError) Error() string {
	return "Responses WebSocket probe failed"
}

func (e *ResponsesWebsocketProbeError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func (e *ResponsesWebsocketProbeError) String() string {
	if e == nil {
		return "ResponsesWebsocketProbeError<nil>"
	}
	return fmt.Sprintf("ResponsesWebsocketProbeError{stage:%s status:%d error:[redacted] headers:[redacted]}", e.Stage, e.Status)
}

// ResponsesWebsocketProbeOptions configures a probe. The dial options carry the
// same headers and TLS/proxy behavior the production connection uses.
type ResponsesWebsocketProbeOptions struct {
	DialOptions           *websocket.DialOptions
	ImmediateCloseTimeout time.Duration
}

// ProbeResponsesWebsocket opens a Responses WebSocket, records the upgrade
// status and allowlisted tracking headers, and waits briefly for an immediate
// close. A read failure after the upgrade still returns those upgrade values in
// the probe error (Rust #57597).
func ProbeResponsesWebsocket(ctx context.Context, endpoint string, options *ResponsesWebsocketProbeOptions) (*ResponsesWebsocketProbe, *ResponsesWebsocketProbeError) {
	if ctx == nil {
		ctx = context.Background()
	}
	var dialOptions *websocket.DialOptions
	grace := time.Duration(0)
	if options != nil {
		dialOptions = options.DialOptions
		grace = options.ImmediateCloseTimeout
	}
	conn, response, err := websocket.Dial(ctx, endpoint, dialOptions)
	if err != nil {
		probeErr := &ResponsesWebsocketProbeError{
			Err:   err,
			Stage: ResponsesWebsocketProbeFailureStageUnknown,
		}
		if response != nil {
			probeErr.Status = response.StatusCode
			probeErr.Headers = AllowlistedWebsocketTrackingHeaders(response.Header)
		}
		if websocketProbeErrorIsTLS(err) {
			probeErr.Stage = ResponsesWebsocketProbeFailureStageTLS
		}
		return nil, probeErr
	}
	status := http.StatusSwitchingProtocols
	headers := http.Header{}
	if response != nil {
		status = response.StatusCode
		headers = AllowlistedWebsocketTrackingHeaders(response.Header)
	}
	probe := &ResponsesWebsocketProbe{
		URL:                endpoint,
		Status:             status,
		Headers:            headers,
		ReasoningIncluded:  HTTPHeaderPresent(responseHeaderOf(response), "x-reasoning-included"),
		ServerModelPresent: HTTPHeaderPresent(responseHeaderOf(response), "openai-model", "x-openai-model"),
	}
	immediateClose, readErr := ResponsesWebsocketImmediateClose(conn, grace)
	_ = conn.Close(websocket.StatusNormalClosure, "probe completed")
	if readErr != nil {
		return nil, &ResponsesWebsocketProbeError{
			Err:     readErr,
			Stage:   ResponsesWebsocketProbeFailureStageUnknown,
			Status:  status,
			Headers: headers,
		}
	}
	probe.ImmediateClose = immediateClose
	return probe, nil
}

// ResponsesWebsocketImmediateClose waits up to grace for a close frame. A
// timeout is not an immediate close; an empty close frame is recorded as code
// 1005 by the WebSocket implementation.
func ResponsesWebsocketImmediateClose(conn *websocket.Conn, grace time.Duration) (*ResponsesWebsocketClose, error) {
	if conn == nil {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), grace)
	defer cancel()
	_, _, err := conn.Read(ctx)
	if err == nil {
		return nil, nil
	}
	var closeErr websocket.CloseError
	if errors.As(err, &closeErr) {
		code := int(closeErr.Code)
		if code == 0 {
			code = int(websocket.StatusNoStatusRcvd)
		}
		return &ResponsesWebsocketClose{Code: strconv.Itoa(code), Reason: closeErr.Reason}, nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return nil, nil
	}
	return nil, err
}

// AllowlistedWebsocketTrackingHeaders copies only the request-correlation
// headers a probe may retain.
func AllowlistedWebsocketTrackingHeaders(headers http.Header) http.Header {
	allowlisted := http.Header{}
	for _, name := range ResponsesWebsocketTrackingHeaders {
		for _, value := range headers.Values(name) {
			allowlisted.Add(name, value)
		}
	}
	return allowlisted
}

// HTTPHeaderPresent reports whether any of the named headers carries a value.
func HTTPHeaderPresent(headers http.Header, names ...string) bool {
	for _, name := range names {
		if len(headers.Values(name)) > 0 {
			return true
		}
	}
	return false
}

func responseHeaderOf(response *http.Response) http.Header {
	if response == nil {
		return nil
	}
	return response.Header
}

// websocketProbeErrorIsTLS mirrors Rust's typed TLS classification: the
// WebSocket layer can wrap certificate failures in an I/O error, so the whole
// chain is inspected.
func websocketProbeErrorIsTLS(err error) bool {
	var verificationError *tls.CertificateVerificationError
	var unknownAuthority x509.UnknownAuthorityError
	var hostnameError x509.HostnameError
	var recordHeaderError tls.RecordHeaderError
	return errors.As(err, &verificationError) ||
		errors.As(err, &unknownAuthority) ||
		errors.As(err, &hostnameError) ||
		errors.As(err, &recordHeaderError)
}

// RedactedWebsocketProbeDebug renders probe evidence without wire values for
// debug logs and doctor output.
func RedactedWebsocketProbeDebug(probe *ResponsesWebsocketProbe) string {
	return strings.TrimSpace(probe.String())
}
