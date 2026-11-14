package codemode

import (
	"bytes"
	"context"
	"encoding/binary"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestGrpcHTTPTransportResponseBoundaries(t *testing.T) {
	oversized := make([]byte, 5)
	binary.BigEndian.PutUint32(oversized[1:], ProtocolMaxFrameBytes+1)
	for _, tc := range []struct {
		name     string
		body     []byte
		trailers http.Header
		want     codes.Code
	}{
		{"missing status", nil, nil, codes.Unknown},
		{"OK trailer", nil, http.Header{"Grpc-Status": {"0"}}, codes.OK},
		{"internal trailer", nil, http.Header{"Grpc-Status": {"13"}}, codes.Internal},
		{"invalid status", nil, http.Header{"Grpc-Status": {"99"}}, codes.Unknown},
		{"oversized frame", oversized, nil, codes.ResourceExhausted},
		{"invalid compression", []byte{2, 0, 0, 0, 0}, nil, codes.Internal},
		{"unknown compression", []byte{1, 0, 0, 0, 0}, nil, codes.Unimplemented},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ready := make(chan struct{})
			close(ready)
			transport := &grpcHTTPTransport{ready: ready, response: &http.Response{
				StatusCode: 200, Header: make(http.Header), Trailer: tc.trailers,
				Body: io.NopCloser(bytes.NewReader(tc.body)),
			}}
			var target any
			got, err := transport.Read(context.Background(), &target)
			if got || status.Code(err) != tc.want {
				t.Fatalf("Read=%v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestGrpcHTTPTransportProxyStatusTrailer(t *testing.T) {
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Header().Set("Trailer", "Grpc-Status, Grpc-Message")
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write([]byte("no healthy upstream"))
		w.Header().Set("Grpc-Status", "13")
		w.Header().Set("Grpc-Message", "explicit%20internal")
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	transport, err := newGrpcHTTPTransport(ctx, server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	var target any
	_, err = transport.Read(ctx, &target)
	if status.Code(err) != codes.Internal || status.Convert(err).Message() != "explicit internal" {
		t.Fatalf("error=%v", err)
	}
}

func TestGrpcHTTPTransportCloseCancelsPendingHeaders(t *testing.T) {
	started := make(chan struct{})
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-r.Context().Done()
	}))
	server.EnableHTTP2 = true
	server.StartTLS()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	transport, err := newGrpcHTTPTransport(ctx, server.URL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	defer transport.Close()
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("request did not start")
	}
	done := make(chan struct{})
	go func() { _ = transport.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not cancel pending headers")
	}
}
