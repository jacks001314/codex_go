package codemode

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	codemodev1 "codex_go/codemode/grpc"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// Rust #57212: the codec round-trips messages and reuses its pooled encoder.
func TestGrpcZstdCodecRoundTripsRepeatedly(t *testing.T) {
	codec := &grpcZstdCodec{}
	payload := []byte(strings.Repeat("code-mode response ", 512))
	for attempt := 0; attempt < 3; attempt++ {
		var compressed bytes.Buffer
		writer, err := codec.Compress(&compressed)
		if err != nil {
			t.Fatalf("Compress() error = %v", err)
		}
		if _, err := writer.Write(payload); err != nil {
			t.Fatalf("Write() error = %v", err)
		}
		if err := writer.Close(); err != nil {
			t.Fatalf("Close() error = %v", err)
		}
		if compressed.Len() >= len(payload) {
			t.Fatalf("attempt %d: compressed %d bytes, payload %d", attempt, compressed.Len(), len(payload))
		}
		reader, err := codec.Decompress(bytes.NewReader(compressed.Bytes()))
		if err != nil {
			t.Fatalf("Decompress() error = %v", err)
		}
		decoded, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("ReadAll() error = %v", err)
		}
		if !bytes.Equal(decoded, payload) {
			t.Fatalf("attempt %d: decoded payload mismatch", attempt)
		}
	}
	if codec.Name() != grpcZstdEncodingName {
		t.Fatalf("Name() = %q", codec.Name())
	}
}

// Rust #57212: a zstd-capable client negotiates compressed responses, and the
// large response still decodes through the normal transport.
func TestGrpcCodeModeNegotiatesZstdResponses(t *testing.T) {
	ensureGrpcZstdRegistered()
	negotiated := make(chan error, 1)
	server := grpc.NewServer()
	codemodev1.RegisterCodeModeHostServer(server, &zstdProbeServer{negotiated: negotiated})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen error = %v", err)
	}
	defer listener.Close()
	go func() { _ = server.Serve(listener) }()
	defer server.Stop()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	transport, err := DialGrpcTransport(ctx, listener.Addr().String(),
		grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		t.Fatalf("DialGrpcTransport() error = %v", err)
	}
	defer transport.Close()

	versions, _ := NewSupportedProtocolVersions(ProtocolV1)
	hello, _ := NewClientHello(versions, CapabilitySet{}, CapabilitySet{})
	if err := transport.Write(ctx, ClientHelloMessage(hello)); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	var response HostToClient
	ok, err := transport.Read(ctx, &response)
	if err != nil || !ok {
		t.Fatalf("Read() = %v, %v", ok, err)
	}
	if response.Type != "connection/ready" {
		t.Fatalf("response = %+v", response)
	}
	if response.Hello == nil || response.Hello.BulkConnectionToken == nil {
		t.Fatalf("large bulk token missing from response: %+v", response)
	}
	select {
	case err := <-negotiated:
		if err != nil {
			t.Fatalf("client did not advertise zstd: %v", err)
		}
	default:
		t.Fatal("server did not attempt to select a send compressor")
	}
}

type zstdProbeServer struct {
	codemodev1.UnimplementedCodeModeHostServer
	negotiated chan error
}

func (s *zstdProbeServer) Transport(stream codemodev1.CodeModeHost_TransportServer) error {
	s.negotiated <- grpc.SetSendCompressor(stream.Context(), grpcZstdEncodingName)
	if _, err := stream.Recv(); err != nil {
		return err
	}
	payload, err := json.Marshal(map[string]any{
		"type":                "connection/ready",
		"bulkConnectionToken": strings.Repeat("x", 128*1024),
	})
	if err != nil {
		return err
	}
	return stream.Send(&codemodev1.FramedMessage{Payload: payload})
}
