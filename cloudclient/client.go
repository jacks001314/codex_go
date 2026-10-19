// Package cloudclient is the native gRPC client for the Codex cloud
// ThreadService (Rust #50113). Credentials and account selection belong to the
// caller; requests are never retried, because a timeout or disconnect can leave
// a Resume admitted.
package cloudclient

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

const (
	requestTimeout  = 150 * time.Second
	maxMessageBytes = 64 * 1024 * 1024
)

var (
	// ErrInvalidEndpoint reports an origin that is not a trusted native gRPC
	// endpoint (HTTPS, or plaintext on unrestricted loopback only).
	ErrInvalidEndpoint = errors.New("invalid gRPC origin (HTTPS required except on unrestricted unmanaged loopback)")
	// ErrInvalidCredentials reports an empty or header-unsafe bearer token or
	// account id.
	ErrInvalidCredentials = errors.New("invalid bearer token or account header")
	// ErrClientBuild reports a transport construction failure.
	ErrClientBuild = errors.New("could not construct the HTTP client")
	// ErrTimeout reports an expired request deadline; the outcome may be unknown.
	ErrTimeout = errors.New("gRPC request timed out; request outcome may be unknown")
	// ErrInvalidResponse reports a malformed Attach envelope.
	ErrInvalidResponse = errors.New("invalid gRPC event envelope")
)

// Credentials is the caller-owned ChatGPT bearer token and selected account.
// Diagnostics never include either value.
type Credentials struct {
	BearerToken string
	AccountID   string
}

// ResumeRequest resumes an existing thread. With Wait=false, success means
// admission only; failures may leave work admitted.
type ResumeRequest struct {
	ThreadID     string
	Wait         bool
	SharedAccess bool
}

// EventKind distinguishes the two Attach payload variants.
type EventKind int

const (
	// EventNotification is a native Notification message.
	EventNotification EventKind = iota + 1
	// EventServerRequest is a native ServerRequest message.
	EventServerRequest
)

// Event contains one complete nested protobuf message. Payloads remain opaque.
type Event struct {
	Kind    EventKind
	Payload []byte
}

// RpcStatus is a native gRPC status, including binary error details.
type RpcStatus struct {
	Code    codes.Code
	Message string
	Details []byte
}

// RpcError wraps a non-OK gRPC status, redacting code in diagnostics.
type RpcError struct {
	Status RpcStatus
}

func (e *RpcError) Error() string {
	if e == nil {
		return "gRPC failed; request outcome may be unknown"
	}
	return fmt.Sprintf("gRPC failed; request outcome may be unknown: code=%s", e.Status.Code)
}

// Client is one fixed origin and account. Construct a new client when
// credentials change.
type Client struct {
	conn        *grpc.ClientConn
	credentials Credentials
}

// New validates the endpoint and credentials and builds the gRPC transport.
// The endpoint must be a trusted native gRPC origin without a path; plaintext
// is accepted only for loopback.
func New(endpoint string, credentials Credentials) (*Client, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || validatedEndpoint(parsed) != nil {
		return nil, ErrInvalidEndpoint
	}
	if !validHeaderValue(credentials.BearerToken) || !validHeaderValue(credentials.AccountID) ||
		credentials.BearerToken == "" || credentials.AccountID == "" {
		return nil, ErrInvalidCredentials
	}
	transport, err := transportCredentials(parsed)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(parsed.Host,
		grpc.WithTransportCredentials(transport),
		grpc.WithDefaultCallOptions(
			grpc.ForceCodec(rawCodec{}),
			grpc.MaxCallRecvMsgSize(maxMessageBytes),
			grpc.MaxCallSendMsgSize(maxMessageBytes),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrClientBuild, err)
	}
	return &Client{conn: conn, credentials: credentials}, nil
}

func validatedEndpoint(parsed *url.URL) error {
	if parsed == nil || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" ||
		(parsed.Path != "" && parsed.Path != "/") {
		return ErrInvalidEndpoint
	}
	host := parsed.Hostname()
	switch parsed.Scheme {
	case "https":
		if host == "" {
			return ErrInvalidEndpoint
		}
	case "http":
		if !isLoopbackHost(host) {
			return ErrInvalidEndpoint
		}
	default:
		return ErrInvalidEndpoint
	}
	return nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func validHeaderValue(value string) bool {
	if value == "" {
		return true
	}
	for index := 0; index < len(value); index++ {
		if value[index] < 0x20 || value[index] == 0x7f {
			return false
		}
	}
	return true
}

func transportCredentials(endpoint *url.URL) (credentials.TransportCredentials, error) {
	if endpoint.Scheme == "http" {
		return insecure.NewCredentials(), nil
	}
	return credentials.NewTLS(&tls.Config{MinVersion: tls.VersionTLS12}), nil
}

func (c *Client) outgoingContext(ctx context.Context) context.Context {
	return metadata.AppendToOutgoingContext(ctx,
		"authorization", "Bearer "+c.credentials.BearerToken,
		"chatgpt-account-id", c.credentials.AccountID,
	)
}

// Resume admits (or waits for) an existing cloud thread.
func (c *Client) Resume(ctx context.Context, request ResumeRequest) error {
	if c == nil || c.conn == nil {
		return ErrClientBuild
	}
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()
	var response rawMessage
	err := c.conn.Invoke(c.outgoingContext(requestCtx), threadServiceResumeMethod, rawMessage(encodeResumeRequest(request)), &response)
	return mapCallError(requestCtx, err)
}

// Attach delivers live events only. Dropping the stream detaches without
// interrupting the thread or answering approvals.
func (c *Client) Attach(ctx context.Context, threadID string) (<-chan Event, <-chan error, error) {
	if c == nil || c.conn == nil {
		return nil, nil, ErrClientBuild
	}
	streamCtx, cancel := context.WithCancel(ctx)
	stream, err := c.conn.NewStream(c.outgoingContext(streamCtx), &grpc.StreamDesc{StreamName: "Attach", ServerStreams: true}, threadServiceAttachMethod)
	if err != nil {
		cancel()
		return nil, nil, mapCallError(ctx, err)
	}
	if err := stream.SendMsg(rawMessage(encodeAttachRequest(threadID))); err != nil {
		cancel()
		return nil, nil, mapCallError(ctx, err)
	}
	if err := stream.CloseSend(); err != nil {
		cancel()
		return nil, nil, mapCallError(ctx, err)
	}
	// Setup (through the first event) is bounded; an established stream has no
	// idle or total deadline, matching Rust's attach contract.
	var setupTimedOut atomic.Bool
	setupTimer := time.AfterFunc(requestTimeout, func() {
		setupTimedOut.Store(true)
		cancel()
	})
	events := make(chan Event)
	errs := make(chan error, 1)
	go func() {
		defer close(events)
		defer close(errs)
		defer cancel()
		defer setupTimer.Stop()
		for {
			var message rawMessage
			if err := stream.RecvMsg(&message); err != nil {
				if errors.Is(err, io.EOF) {
					return
				}
				if setupTimedOut.Load() {
					errs <- ErrTimeout
					return
				}
				errs <- mapCallError(ctx, err)
				return
			}
			setupTimer.Stop()
			event, err := decodeAttachResponse(message)
			if err != nil {
				errs <- err
				return
			}
			select {
			case events <- event:
			case <-streamCtx.Done():
				return
			}
		}
	}()
	return events, errs, nil
}

// Close releases the underlying connection.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

func mapCallError(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return ErrTimeout
	}
	statusErr, ok := status.FromError(err)
	if !ok {
		return err
	}
	// Rust keeps the native grpc-status-details-bin payload. grpc-go exposes
	// decoded details, so re-encode the status proto to preserve that contract.
	details, _ := proto.Marshal(statusErr.Proto())
	return &RpcError{Status: RpcStatus{Code: statusErr.Code(), Message: statusErr.Message(), Details: details}}
}

// rawCodec passes protobuf envelopes through unchanged.
type rawCodec struct{}

func (rawCodec) Name() string {
	return "proto"
}

func (rawCodec) Marshal(value any) ([]byte, error) {
	switch typed := value.(type) {
	case rawMessage:
		return typed, nil
	case *rawMessage:
		if typed == nil {
			return nil, nil
		}
		return *typed, nil
	default:
		return nil, fmt.Errorf("cloudclient: unsupported message type %T", value)
	}
}

func (rawCodec) Unmarshal(data []byte, value any) error {
	target, ok := value.(*rawMessage)
	if !ok {
		return fmt.Errorf("cloudclient: unsupported message type %T", value)
	}
	*target = append((*target)[:0], data...)
	return nil
}
