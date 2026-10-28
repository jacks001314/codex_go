package codemode

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/tool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type retryingAdmissionTestProvider struct {
	attempts atomic.Int32
}

func (p *retryingAdmissionTestProvider) connect(context.Context) (*remoteConnection, error) {
	p.attempts.Add(1)
	return nil, status.Error(codes.Unavailable, "no healthy upstream")
}

func (*retryingAdmissionTestProvider) nextGeneration() uint64 { return 1 }

func (*retryingAdmissionTestProvider) sessionAdmissionRetries() int {
	return grpcSessionAdmissionAttempts
}

func TestGrpcSessionAdmissionRetryPolicyLikeRust(t *testing.T) {
	if grpcSessionAdmissionAttempts != 5 {
		t.Fatalf("admission attempts = %d, want 5", grpcSessionAdmissionAttempts)
	}
	previousJitter := sessionAdmissionJitterFactor
	sessionAdmissionJitterFactor = func() float64 { return 1 }
	defer func() { sessionAdmissionJitterFactor = previousJitter }()

	for attempt, want := range map[int]time.Duration{
		1: 0,
		2: 50 * time.Millisecond,
		3: 100 * time.Millisecond,
		4: 200 * time.Millisecond,
	} {
		if got := sessionAdmissionRetryDelay(attempt); got != want {
			t.Fatalf("attempt %d delay = %v, want %v", attempt, got, want)
		}
	}

	provider := &retryingAdmissionTestProvider{}
	session := &remoteSession{provider: provider, delegate: tool.CodeModeRemoteDelegate(nil), id: "session-1"}
	// Remove real backoff while preserving the exact attempt count.
	sessionAdmissionJitterFactor = func() float64 { return 0 }
	if _, err := session.connection(context.Background()); err == nil {
		t.Fatal("admission unexpectedly succeeded")
	}
	if got := provider.attempts.Load(); got != 5 {
		t.Fatalf("admission attempts = %d, want 5", got)
	}
}
