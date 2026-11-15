package cloudclient

import (
	"bytes"
	"context"
	"errors"
	"net"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
)

// Mirrors Rust cloud-client read_thread_name (#61600): the saved name is read
// from ReadThread without attaching, and a reply for another conversation is
// rejected.
func TestReadThreadNameWireLikeRust(t *testing.T) {
	var mode int32
	server := grpc.NewServer(grpc.ForceServerCodec(rawCodec{}))
	server.RegisterService(&grpc.ServiceDesc{
		ServiceName: "oaiproto.codex_app_server_backend.ThreadService",
		HandlerType: (*testThreadService)(nil),
		Methods: []grpc.MethodDesc{{MethodName: "ReadThread", Handler: func(_ any, _ context.Context, decode func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
			var request rawMessage
			if err := decode(&request); err != nil {
				return nil, err
			}
			if !bytes.Equal(request, []byte{10, 1, 't'}) {
				return nil, status.Error(codes.InvalidArgument, "wrong wire payload")
			}
			switch mode {
			case 1:
				return nil, status.Error(codes.NotFound, "fixture failure")
			case 2:
				return rawMessage(encodeReadThreadResponse("other", "Wrong")), nil
			case 3:
				return rawMessage(encodeReadThreadResponse("t", "")), nil
			case 4:
				return rawMessage([]byte{255}), nil
			default:
				return rawMessage(encodeReadThreadResponse("t", "Saved title")), nil
			}
		}}},
	}, &struct{}{})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()
	client, err := New("http://"+listener.Addr().String(), Credentials{BearerToken: "fixture", AccountID: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	name, err := client.ReadThreadName(context.Background(), "t")
	if err != nil || name != "Saved title" {
		t.Fatalf("ReadThreadName = %q, %v", name, err)
	}
	mode = 3
	if name, err := client.ReadThreadName(context.Background(), "t"); err != nil || name != "" {
		t.Fatalf("unnamed ReadThreadName = %q, %v", name, err)
	}
	mode = 2
	if _, err := client.ReadThreadName(context.Background(), "t"); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("mismatched id error = %v, want ErrInvalidResponse", err)
	}
	mode = 4
	if _, err := client.ReadThreadName(context.Background(), "t"); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("malformed reply error = %v, want ErrInvalidResponse", err)
	}
	mode = 1
	if _, err := client.ReadThreadName(context.Background(), "t"); err == nil {
		t.Fatal("failed ReadThread accepted")
	}
	if _, err := client.ReadThreadName(context.Background(), " "); err == nil {
		t.Fatal("empty thread id accepted")
	}
}

// encodeReadThreadResponse builds a ReadThreadResponse whose ThreadName carries
// the conversation id and optional saved name.
func encodeReadThreadResponse(threadID string, name string) []byte {
	var thread []byte
	thread = protowire.AppendTag(thread, 14, protowire.BytesType)
	thread = protowire.AppendString(thread, threadID)
	if name != "" {
		thread = protowire.AppendTag(thread, 17, protowire.BytesType)
		thread = protowire.AppendString(thread, name)
	}
	var response []byte
	response = protowire.AppendTag(response, 1, protowire.BytesType)
	return protowire.AppendBytes(response, thread)
}
