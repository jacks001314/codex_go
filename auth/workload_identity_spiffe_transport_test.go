package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
)

func TestSPIFFETransportProtocolAndRedaction(t *testing.T) {
	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	var reject atomic.Bool
	var calls atomic.Int32
	token := "e30." + base64.RawURLEncoding.EncodeToString([]byte(`{"sub":"spiffe://example.org/test","aud":"codex"}`)) + ".synthetic"
	server.RegisterService(&grpc.ServiceDesc{ServiceName: "SpiffeWorkloadAPI", HandlerType: (*interface{})(nil), Methods: []grpc.MethodDesc{{MethodName: "FetchJWTSVID", Handler: func(_ any, ctx context.Context, decode func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
		var request spiffeJWTRequest
		if err := decode(&request); err != nil {
			return nil, err
		}
		calls.Add(1)
		md, _ := metadata.FromIncomingContext(ctx)
		if len(request.Audience) != 1 || request.Audience[0] != "codex" || request.ID != "spiffe://example.org/test" || len(md.Get("workload.spiffe.io")) != 1 || md.Get("workload.spiffe.io")[0] != "true" {
			return nil, status.Error(codes.InvalidArgument, "bad request")
		}
		if reject.Load() {
			return nil, status.Error(codes.PermissionDenied, "synthetic-secret-agent-detail")
		}
		return &spiffeJWTResponse{SVIDs: []*spiffeJWTSVID{{Token: token}}}, nil
	}}}}, struct{}{})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() { server.Stop(); _ = listener.Close() })
	var dials atomic.Int32
	dial := func(ctx context.Context, _ string) (net.Conn, error) { dials.Add(1); return listener.DialContext(ctx) }
	source := spiffeAssertionSource{audience: "codex", id: "spiffe://example.org/test"}
	for i := 0; i < 2; i++ {
		got, err := source.fetchWithDialer(context.Background(), dial)
		if err != nil || got != token {
			t.Fatalf("fetch=%v", err)
		}
	}
	if dials.Load() != 2 || calls.Load() != 2 {
		t.Fatalf("dials=%d calls=%d", dials.Load(), calls.Load())
	}
	reject.Store(true)
	_, err := source.fetchWithDialer(context.Background(), dial)
	if err == nil || !strings.Contains(err.Error(), "PermissionDenied") || strings.Contains(err.Error(), "synthetic-secret") {
		t.Fatalf("redaction=%v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	_, err = source.fetchWithDialer(ctx, func(ctx context.Context, _ string) (net.Conn, error) { <-ctx.Done(); return nil, ctx.Err() })
	if err != workloadErrSPIFFEUnavailable {
		t.Fatalf("timeout=%v", err)
	}
}

func TestSPIFFESourceValidationAndSessionFingerprint(t *testing.T) {
	source, err := newSPIFFEAssertionSource("unix:///run/agent%20one.sock", "codex", "SPIFFE://EXAMPLE.ORG/Test")
	if err != nil || source.socketPath != "/run/agent one.sock" || source.id != "spiffe://example.org/Test" {
		t.Fatalf("source=%v err=%v", source, err)
	}
	for _, endpoint := range []string{"tcp://localhost:1234", "unix:relative.sock", "unix:///run/a?query", "unix:///run/a#", "unix:///run/a%00", "unix://host/run/a"} {
		if _, err := newSPIFFEAssertionSource(endpoint, "codex", ""); err == nil {
			t.Errorf("endpoint accepted: %s", endpoint)
		}
	}
	base := workloadIdentitySessionConfig{spiffe: source, federationRuleID: "rule", tokenURL: "http://127.0.0.1/token"}
	fingerprint := base.fingerprint()
	for _, variant := range []spiffeAssertionSource{
		{socketPath: "/other", audience: source.audience, id: source.id},
		{socketPath: source.socketPath, audience: "other", id: source.id},
		{socketPath: source.socketPath, audience: source.audience, id: "spiffe://example.org/Other"},
	} {
		changed := base
		changed.spiffe = &variant
		if fingerprint == changed.fingerprint() {
			t.Fatal("source change not reflected in fingerprint")
		}
	}
	exchange, err := base.exchange()
	if err != nil || exchange.config.spiffe == nil || exchange.config.AssertionFile != "" {
		t.Fatalf("exchange routing: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = exchange.Resolve(ctx)
	if err == nil {
		t.Fatal("cancelled acquisition succeeded")
	}
}

func TestSPIFFETransientClassificationLikeRust(t *testing.T) {
	retryable := map[codes.Code]bool{codes.Unavailable: true, codes.DeadlineExceeded: true, codes.ResourceExhausted: true, codes.Aborted: true, codes.Canceled: true, codes.Internal: true}
	for code := codes.OK; code <= codes.Unauthenticated; code++ {
		for _, err := range []error{spiffeRejectedError{code: code}, fmt.Errorf("wrapped: %w", spiffeRejectedError{code: code})} {
			if got := workloadErrorIsTransient(err); got != retryable[code] {
				t.Errorf("code=%s transient=%v want=%v", code, got, retryable[code])
			}
		}
	}
	if !workloadErrorIsTransient(workloadErrSPIFFEUnavailable) {
		t.Error("unavailable must be transient")
	}
	for _, err := range []error{workloadErrAmbiguousSPIFFE, workloadErrInvalidAssertion, workloadErrAssertionTooLarge} {
		if workloadErrorIsTransient(err) {
			t.Errorf("permanent error retried: %v", err)
		}
	}
}

func TestSPIFFEEndpointRootAndShorthandLikeRust(t *testing.T) {
	for _, endpoint := range []string{"unix:///", "unix:/", "unix:", "unix:///tmp/a?", "unix:///tmp/a#"} {
		if _, err := newSPIFFEAssertionSource(endpoint, "codex", ""); err == nil {
			t.Errorf("invalid endpoint accepted: %q", endpoint)
		}
	}
	for _, endpoint := range []string{"unix:///tmp/agent.sock", "unix:/tmp/agent.sock"} {
		source, err := newSPIFFEAssertionSource(endpoint, "codex", "")
		if err != nil || source.socketPath != "/tmp/agent.sock" {
			t.Errorf("endpoint %q source=%v err=%v", endpoint, source, err)
		}
	}
}

func TestSPIFFEConnectionFailureIsUnavailable(t *testing.T) {
	source := spiffeAssertionSource{audience: "codex"}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := source.fetchWithDialer(ctx, func(context.Context, string) (net.Conn, error) {
		return nil, errors.New("synthetic-private-socket-detail")
	})
	if err != workloadErrSPIFFEUnavailable {
		t.Fatalf("connection failure=%v", err)
	}
	if strings.Contains(err.Error(), "private-socket") {
		t.Fatal("connection details leaked")
	}
}
