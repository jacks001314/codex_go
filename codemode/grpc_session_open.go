package codemode

import (
	"context"
	"errors"

	"codex_go/tool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// grpcSessionOpenErrorMaxBytes bounds the diagnostic carried by a typed session
// open failure so a misbehaving host cannot flood the caller (Rust #60496 keeps
// 512 bytes).
const grpcSessionOpenErrorMaxBytes = 512

// GrpcSessionOpenError mirrors Rust GrpcSessionOpenError (#60496): a terminal
// failure while establishing a code-mode session, keeping the admission status
// inspectable instead of collapsing it into a string.
type GrpcSessionOpenError struct {
	// Code is the terminal gRPC admission status. codes.OK means the failure did
	// not come from an admission status (setup failed elsewhere).
	Code codes.Code
	// Timeout marks admission that stopped because the caller's context ended
	// (deadline or cancellation) while the handshake, including its retries, was
	// still waiting.
	Timeout bool
	// Message is the bounded diagnostic.
	Message string
}

func (e *GrpcSessionOpenError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// IsAdmissionStatus reports whether the failure carried a terminal gRPC status,
// so callers can branch on Code (for example PermissionDenied or
// ResourceExhausted after the retries ran out).
func (e *GrpcSessionOpenError) IsAdmissionStatus() bool {
	return e != nil && e.Code != codes.OK
}

// grpcSessionOpenErrorFrom classifies a session-open failure the way Rust's
// open_session does: the caller's context ending becomes an admission timeout,
// any other gRPC status keeps its code, and everything else is a setup failure.
func grpcSessionOpenErrorFrom(err error) *GrpcSessionOpenError {
	if err == nil {
		return nil
	}
	message := grpcSessionOpenErrorMessage(err)
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return &GrpcSessionOpenError{Timeout: true, Message: message}
	}
	var grpcStatus interface{ GRPCStatus() *status.Status }
	if errors.As(err, &grpcStatus) && grpcStatus.GRPCStatus() != nil {
		code := grpcStatus.GRPCStatus().Code()
		if code != codes.OK {
			return &GrpcSessionOpenError{Code: code, Message: message}
		}
	}
	return &GrpcSessionOpenError{Message: message}
}

func grpcSessionOpenErrorMessage(err error) string {
	message := err.Error()
	if len(message) > grpcSessionOpenErrorMaxBytes {
		message = message[:grpcSessionOpenErrorMaxBytes]
	}
	return message
}

// OpenSession opens a code-mode session immediately and returns the typed
// terminal admission error (Rust GrpcCodeModeSessionProvider::open_session,
// #60496). The lazily opened session returned by NewSession keeps the string
// error surface the provider interface expects.
func (p *GrpcCodeModeSessionProvider) OpenSession(ctx context.Context, delegate tool.CodeModeRemoteDelegate, options tool.CodeModeSessionOptions) (tool.CodeModeRemoteSession, *GrpcSessionOpenError) {
	if p == nil {
		return nil, &GrpcSessionOpenError{Message: "code-mode gRPC provider is nil"}
	}
	session := p.NewSessionWithOptions(delegate, options)
	opener, ok := session.(interface{ establish(context.Context) error })
	if !ok {
		return nil, &GrpcSessionOpenError{Message: "code-mode session provider cannot open sessions eagerly"}
	}
	if err := opener.establish(ctx); err != nil {
		return nil, grpcSessionOpenErrorFrom(err)
	}
	return session, nil
}

// establish runs the admission handshake so callers that need an open session
// up front (OpenSession) observe the same retries Execute would.
func (s *remoteSession) establish(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	_, err := s.connection(ctx)
	return err
}
