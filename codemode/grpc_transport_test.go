package codemode

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	codemodev1 "codex_go/codemode/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestGrpcTransportRoundTrip(t *testing.T) {
	server := grpc.NewServer()
	handler := &framingTransportServer{payloads: make(chan []byte, 8)}
	codemodev1.RegisterCodeModeHostServer(server, handler)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error = %v", err)
	}
	defer listener.Close()
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	transport, err := DialGrpcTransport(ctx, listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		t.Fatalf("DialGrpcTransport() error = %v", err)
	}
	defer transport.Close()

	versions, _ := NewSupportedProtocolVersions(ProtocolV1)
	hello, _ := NewClientHello(versions, CapabilitySet{}, CapabilitySet{})
	if err := transport.Write(ctx, ClientHelloMessage(hello)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	// Server echoes the payload back.
	var response HostToClient
	ok, err := transport.Read(ctx, &response)
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if !ok {
		t.Fatal("Read() = false, want echo")
	}
	if response.Type != "connection/ready" {
		t.Fatalf("response = %+v", response)
	}
}

func TestDecodeGrpcFramedPayload(t *testing.T) {
	payload, _ := json.Marshal(map[string]any{"type": "connection/ready"})
	var target HostToClient
	if err := decodeGrpcFramedPayload(payload, &target); err != nil {
		t.Fatalf("decodeGrpcFramedPayload() error = %v", err)
	}
	if target.Type != "connection/ready" {
		t.Fatalf("target = %+v", target)
	}
	if err := decodeGrpcFramedPayload([]byte{}, &target); err == nil {
		t.Fatal("empty payload accepted")
	}
}

func TestDisableWebSocketNagleHandlesNonTCP(t *testing.T) {
	// A plain reader body (not TCP) must be tolerated without panic.
	response := &http.Response{Body: http.NoBody}
	disableWebSocketNagle(response)
	disableWebSocketNagle(nil)
}

type framingTransportServer struct {
	codemodev1.UnimplementedCodeModeHostServer
	payloads chan []byte
}

func (s *framingTransportServer) Transport(stream codemodev1.CodeModeHost_TransportServer) error {
	for {
		message, err := stream.Recv()
		if err != nil {
			return nil
		}
		if message == nil {
			continue
		}
		// Parse the client hello and answer with a connection/ready response.
		var clientToHost ClientToHost
		if err := json.Unmarshal(message.Payload, &clientToHost); err != nil {
			return err
		}
		ready := HostToClient{
			Type:  "connection/ready",
			Hello: &HostHello{SelectedVersion: ProtocolV1, Capabilities: CapabilitySet{}},
		}
		payload, _ := json.Marshal(ready)
		if err := stream.Send(&codemodev1.FramedMessage{Payload: payload}); err != nil {
			return err
		}
	}
}
