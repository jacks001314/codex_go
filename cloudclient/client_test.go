package cloudclient

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protowire"
)

func TestClientEndpointValidationLikeRust(t *testing.T) {
	accepted := []string{
		"https://api.example.com",
		"https://api.example.com/",
		"http://127.0.0.1:8443",
		"http://localhost:8443",
		"http://[::1]:8443",
	}
	for _, endpoint := range accepted {
		client, err := New(endpoint, Credentials{BearerToken: "token", AccountID: "account"})
		if err != nil {
			t.Fatalf("New(%q) error = %v", endpoint, err)
		}
		_ = client.Close()
	}
	rejected := []string{
		"http://api.example.com",
		"https://api.example.com/grpc",
		"https://api.example.com?x=1",
		"https://api.example.com#fragment",
		"https://user:pass@api.example.com",
		"ftp://api.example.com",
		"",
	}
	for _, endpoint := range rejected {
		if client, err := New(endpoint, Credentials{BearerToken: "token", AccountID: "account"}); !errors.Is(err, ErrInvalidEndpoint) {
			if client != nil {
				_ = client.Close()
			}
			t.Fatalf("New(%q) error = %v, want ErrInvalidEndpoint", endpoint, err)
		}
	}
}

func TestClientCredentialsValidationLikeRust(t *testing.T) {
	cases := []Credentials{
		{},
		{BearerToken: "token"},
		{AccountID: "account"},
		{BearerToken: "tok\nen", AccountID: "account"},
		{BearerToken: "token", AccountID: "acc\x00ount"},
	}
	for index, credentials := range cases {
		if _, err := New("https://api.example.com", credentials); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("case %d error = %v, want ErrInvalidCredentials", index, err)
		}
	}
}

func TestWireEnvelopeLikeRust(t *testing.T) {
	encoded := encodeResumeRequest(ResumeRequest{ThreadID: "thread-1", Wait: true, SharedAccess: true})
	fields := map[protowire.Number][]byte{}
	for len(encoded) > 0 {
		number, wireType, length := protowire.ConsumeTag(encoded)
		if length < 0 {
			t.Fatalf("malformed request: %v", encoded)
		}
		encoded = encoded[length:]
		switch wireType {
		case protowire.BytesType:
			value, consumed := protowire.ConsumeBytes(encoded)
			if consumed < 0 {
				t.Fatalf("malformed bytes field %d", number)
			}
			fields[number] = value
			encoded = encoded[consumed:]
		case protowire.VarintType:
			value, consumed := protowire.ConsumeVarint(encoded)
			if consumed < 0 {
				t.Fatalf("malformed varint field %d", number)
			}
			if value != 1 {
				t.Fatalf("field %d = %d, want 1", number, value)
			}
			encoded = encoded[consumed:]
		default:
			t.Fatalf("unexpected wire type %v", wireType)
		}
	}
	if string(fields[1]) != "thread-1" {
		t.Fatalf("thread_id = %q", fields[1])
	}

	notification := protowire.AppendTag(nil, 1, protowire.BytesType)
	notification = protowire.AppendBytes(notification, []byte("notification"))
	event, err := decodeAttachResponse(notification)
	if err != nil || event.Kind != EventNotification || string(event.Payload) != "notification" {
		t.Fatalf("notification event = %#v, %v", event, err)
	}
	request := protowire.AppendTag(nil, 2, protowire.BytesType)
	request = protowire.AppendBytes(request, []byte("request"))
	event, err = decodeAttachResponse(request)
	if err != nil || event.Kind != EventServerRequest || string(event.Payload) != "request" {
		t.Fatalf("server request event = %#v, %v", event, err)
	}
	if _, err := decodeAttachResponse(nil); !errors.Is(err, ErrInvalidResponse) {
		t.Fatalf("empty envelope error = %v", err)
	}
}

type testThreadService interface{}

func TestClientResumeAndAttachLikeRust(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	server := grpc.NewServer(grpc.ForceServerCodec(rawCodec{}))
	server.RegisterService(&grpc.ServiceDesc{
		ServiceName: "oaiproto.codex_app_server_backend.ThreadService",
		HandlerType: (*testThreadService)(nil),
		Methods: []grpc.MethodDesc{{
			MethodName: "Resume",
			Handler: func(_ any, ctx context.Context, decode func(any) error, _ grpc.UnaryServerInterceptor) (any, error) {
				incoming, _ := metadata.FromIncomingContext(ctx)
				if got := incoming.Get("authorization"); len(got) != 1 || got[0] != "Bearer token" {
					return nil, status.Error(codes.Unauthenticated, "missing bearer")
				}
				if got := incoming.Get("chatgpt-account-id"); len(got) != 1 || got[0] != "account" {
					return nil, status.Error(codes.PermissionDenied, "missing account")
				}
				var message rawMessage
				if err := decode(&message); err != nil {
					return nil, err
				}
				threadID, wait, shared := decodeResumeFields([]byte(message))
				if threadID != "thread-1" || !wait || !shared {
					return nil, status.Error(codes.InvalidArgument, "unexpected resume request")
				}
				return rawMessage(nil), nil
			},
		}},
		Streams: []grpc.StreamDesc{{
			StreamName:    "Attach",
			ServerStreams: true,
			Handler: func(_ any, stream grpc.ServerStream) error {
				var message rawMessage
				if err := stream.RecvMsg(&message); err != nil {
					return err
				}
				threadID, _, _ := decodeResumeFields([]byte(message))
				if threadID != "thread-1" {
					return status.Error(codes.InvalidArgument, "unexpected attach request")
				}
				notification := protowire.AppendTag(nil, 1, protowire.BytesType)
				notification = protowire.AppendBytes(notification, []byte("notification"))
				if err := stream.SendMsg(rawMessage(notification)); err != nil {
					return err
				}
				request := protowire.AppendTag(nil, 2, protowire.BytesType)
				request = protowire.AppendBytes(request, []byte("request"))
				return stream.SendMsg(rawMessage(request))
			},
		}},
	}, &struct{}{})
	go func() {
		_ = server.Serve(listener)
	}()
	defer server.Stop()

	client, err := New("http://"+listener.Addr().String(), Credentials{BearerToken: "token", AccountID: "account"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Resume(ctx, ResumeRequest{ThreadID: "thread-1", Wait: true, SharedAccess: true}); err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	events, errs, err := client.Attach(ctx, "thread-1")
	if err != nil {
		t.Fatalf("Attach() error = %v", err)
	}
	var got []Event
	for event := range events {
		got = append(got, event)
	}
	if err := <-errs; err != nil {
		t.Fatalf("Attach() stream error = %v", err)
	}
	if len(got) != 2 || got[0].Kind != EventNotification || string(got[0].Payload) != "notification" ||
		got[1].Kind != EventServerRequest || string(got[1].Payload) != "request" {
		t.Fatalf("events = %#v", got)
	}
}

// decodeResumeFields reads the shared field layout of ResumeRequest and
// AttachRequest (thread_id is field 1; Resume's flags follow).
func decodeResumeFields(data []byte) (string, bool, bool) {
	var threadID string
	var wait, shared bool
	for len(data) > 0 {
		number, wireType, length := protowire.ConsumeTag(data)
		if length < 0 {
			return "", false, false
		}
		data = data[length:]
		switch wireType {
		case protowire.BytesType:
			value, consumed := protowire.ConsumeBytes(data)
			if consumed < 0 {
				return "", false, false
			}
			if number == 1 {
				threadID = string(value)
			}
			data = data[consumed:]
		case protowire.VarintType:
			value, consumed := protowire.ConsumeVarint(data)
			if consumed < 0 {
				return "", false, false
			}
			switch number {
			case 2:
				wait = value != 0
			case 3:
				shared = value != 0
			}
			data = data[consumed:]
		default:
			consumed := protowire.ConsumeFieldValue(number, wireType, data)
			if consumed < 0 {
				return "", false, false
			}
			data = data[consumed:]
		}
	}
	return threadID, wait, shared
}

func TestRpcErrorRedactsMessage(t *testing.T) {
	errorValue := &RpcError{Status: RpcStatus{Code: codes.PermissionDenied, Message: "secret server payload"}}
	if message := errorValue.Error(); strings.Contains(message, "secret") {
		t.Fatalf("RpcError leaked payload: %q", message)
	}
}
