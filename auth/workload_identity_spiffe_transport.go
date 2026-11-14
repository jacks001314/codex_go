package auth

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

const workloadErrSPIFFEUnavailable workloadIdentityError = "the SPIFFE Workload API is unavailable or timed out"

type spiffeRejectedError struct{ code codes.Code }

func (e spiffeRejectedError) Error() string {
	return fmt.Sprintf("the SPIFFE Workload API rejected the request with gRPC status %s", e.code)
}

type spiffeAssertionSource struct{ socketPath, audience, id string }

func (s spiffeAssertionSource) fetchAssertion(ctx context.Context) (string, error) {
	return s.fetchWithDialer(ctx, func(ctx context.Context, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", s.socketPath)
	})
}

func (s spiffeAssertionSource) fetchWithDialer(ctx context.Context, dial func(context.Context, string) (net.Conn, error)) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	// A new connection per exchange survives workload agent restarts.
	var connected atomic.Bool
	trackedDial := func(ctx context.Context, address string) (net.Conn, error) {
		connection, err := dial(ctx, address)
		if err == nil {
			connected.Store(true)
		}
		return connection, err
	}
	conn, err := grpc.NewClient("passthrough:///spiffe-workload-api", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(trackedDial))
	if err != nil {
		return "", workloadErrSPIFFEUnavailable
	}
	defer conn.Close()
	ctx = metadata.AppendToOutgoingContext(ctx, "workload.spiffe.io", "true")
	var response spiffeJWTResponse
	err = conn.Invoke(ctx, "/SpiffeWorkloadAPI/FetchJWTSVID", &spiffeJWTRequest{Audience: []string{s.audience}, ID: s.id}, &response)
	if err != nil {
		if ctx.Err() != nil || !connected.Load() {
			return "", workloadErrSPIFFEUnavailable
		}
		// Do not retain agent-supplied text or wrapped transport errors.
		return "", spiffeRejectedError{code: status.Code(err)}
	}
	tokens := make([]string, len(response.SVIDs))
	for i, svid := range response.SVIDs {
		if svid != nil {
			tokens[i] = svid.Token
		}
	}
	return selectSPIFFEAssertion(tokens, s.audience, s.id)
}

// Minimal protobuf v1 messages for the upstream FetchJWTSVID wire contract.
// grpc's protobuf codec adapts these tagged messages; no global codec override.
type spiffeJWTRequest struct {
	Audience []string `protobuf:"bytes,1,rep,name=audience,proto3"`
	ID       string   `protobuf:"bytes,2,opt,name=spiffe_id,json=spiffeId,proto3"`
}

func (m *spiffeJWTRequest) Reset()       { *m = spiffeJWTRequest{} }
func (*spiffeJWTRequest) String() string { return "SPIFFE JWT request" }
func (*spiffeJWTRequest) ProtoMessage()  {}

type spiffeJWTResponse struct {
	SVIDs []*spiffeJWTSVID `protobuf:"bytes,1,rep,name=svids,proto3"`
}

func (m *spiffeJWTResponse) Reset()       { *m = spiffeJWTResponse{} }
func (*spiffeJWTResponse) String() string { return "SPIFFE JWT response" }
func (*spiffeJWTResponse) ProtoMessage()  {}

type spiffeJWTSVID struct {
	Token string `protobuf:"bytes,2,opt,name=svid,proto3"`
}

func (m *spiffeJWTSVID) Reset()       { *m = spiffeJWTSVID{} }
func (*spiffeJWTSVID) String() string { return "SPIFFE JWT-SVID" }
func (*spiffeJWTSVID) ProtoMessage()  {}

func newSPIFFEAssertionSource(endpoint, audience, id string) (*spiffeAssertionSource, error) {
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Scheme != "unix" || parsed.Host != "" || parsed.User != nil || parsed.Opaque != "" || !strings.HasPrefix(parsed.Path, "/") || parsed.Path == "/" || parsed.RawQuery != "" || parsed.ForceQuery || strings.Contains(endpoint, "#") || strings.ContainsRune(parsed.Path, 0) {
		return nil, workloadInvalidConfig("the SPIFFE Workload API endpoint must be an absolute unix: socket URI")
	}
	if strings.TrimSpace(audience) == "" || strings.ContainsRune(audience, 0) {
		return nil, workloadInvalidConfig("the SPIFFE JWT-SVID audience must not be empty or contain NUL")
	}
	if id != "" {
		canonical, ok := canonicalSPIFFESelectionID(id)
		if !ok {
			return nil, workloadInvalidConfig("the SPIFFE ID is invalid")
		}
		id = canonical
	}
	return &spiffeAssertionSource{socketPath: parsed.Path, audience: audience, id: id}, nil
}

func (e spiffeRejectedError) IsTransient() bool {
	switch e.code {
	case codes.Unavailable, codes.DeadlineExceeded, codes.ResourceExhausted, codes.Aborted, codes.Canceled, codes.Internal:
		return true
	default:
		return false
	}
}
