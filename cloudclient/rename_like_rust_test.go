package cloudclient

import (
	"bytes"
	"context"
	"errors"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net"
	"sync/atomic"
	"testing"
)

func TestSetThreadNameWireAndAcknowledgmentLikeRust(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	var mode atomic.Int32
	var calls atomic.Int32
	server := grpc.NewServer(grpc.ForceServerCodec(rawCodec{}))
	server.RegisterService(&grpc.ServiceDesc{
		ServiceName: "oaiproto.codex_app_server_backend.ThreadService",
		HandlerType: (*testThreadService)(nil),
		Methods: []grpc.MethodDesc{{MethodName: "SetThreadName", Handler: func(_ any, _ context.Context, decode func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
			calls.Add(1)
			var request rawMessage
			if err := decode(&request); err != nil {
				return nil, err
			}
			if !bytes.Equal(request, []byte{10, 1, 't', 18, 3, 'N', 'e', 'w'}) {
				return nil, status.Error(codes.InvalidArgument, "wrong wire payload")
			}
			switch mode.Load() {
			case 1:
				return nil, status.Error(codes.Unavailable, "fixture failure")
			case 2:
				return rawMessage([]byte{255}), nil
			default:
				return rawMessage(nil), nil
			}
		}}},
	}, &struct{}{})
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	client, err := New("http://"+listener.Addr().String(), Credentials{BearerToken: "fixture", AccountID: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	for _, tc := range []struct {
		mode      int32
		wantError bool
	}{{0, false}, {1, true}, {2, true}} {
		mode.Store(tc.mode)
		err := client.SetThreadName(context.Background(), "t", "New")
		if (err != nil) != tc.wantError {
			t.Fatalf("mode=%d error=%v", tc.mode, err)
		}
		if tc.mode == 2 && !errors.Is(err, ErrInvalidResponse) {
			t.Fatalf("malformed response error=%v", err)
		}
	}
	for _, tc := range []struct{ id, name string }{{" ", "New"}, {"t", " "}} {
		if err := client.SetThreadName(context.Background(), tc.id, tc.name); err == nil {
			t.Fatal("empty request accepted")
		}
	}
	if calls.Load() != 3 {
		t.Fatalf("RPC calls=%d, want 3", calls.Load())
	}
}
