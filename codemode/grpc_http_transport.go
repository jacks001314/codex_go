package codemode

import (
	"bytes"
	"context"

	"encoding/binary"
	"errors"
	"io"

	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"

	codemodev1 "codex_go/codemode/grpc"
	"codex_go/network"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/encoding"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

// grpcHTTPTransport keeps response metadata available even for proxy errors.
type grpcHTTPTransport struct {
	writer    *io.PipeWriter
	cancel    context.CancelFunc
	ready     chan struct{}
	response  *http.Response
	err       error
	writeMu   sync.Mutex
	closeOnce sync.Once
}

func newGrpcHTTPTransport(ctx context.Context, endpoint string, supplied *http.Client) (*grpcHTTPTransport, error) {
	client := network.NewHTTPClient(true, 0)
	if supplied != nil {
		clone := *supplied
		client = &clone
	}
	parsed, err := url.Parse(endpoint)
	if err != nil {
		return nil, err
	}
	if base := network.BaseTransport(client.Transport); base != nil {
		clone := base.Clone()
		clone.Protocols = new(http.Protocols)
		clone.Protocols.SetHTTP2(true)
		clone.Protocols.SetUnencryptedHTTP2(parsed.Scheme == "http")
		clone.ForceAttemptHTTP2 = true
		client.Transport = network.ReplaceBaseTransport(client.Transport, clone)
	}
	// A redirect must never replay the bidi request or forward credentials.
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	streamCtx, cancel := context.WithCancel(ctx)
	reader, writer := io.Pipe()
	request, err := http.NewRequestWithContext(streamCtx, http.MethodPost, strings.TrimRight(endpoint, "/")+codemodev1.CodeModeHost_Transport_FullMethodName, reader)
	if err != nil {
		cancel()
		_ = reader.Close()
		_ = writer.Close()
		return nil, err
	}
	request.Header.Set("Content-Type", "application/grpc")
	request.Header.Set("Te", "trailers")
	request.Header.Set("Grpc-Accept-Encoding", "zstd")
	transport := &grpcHTTPTransport{writer: writer, cancel: cancel, ready: make(chan struct{})}
	go func() {
		transport.response, transport.err = client.Do(request)
		close(transport.ready)
		if transport.err != nil || transport.response.StatusCode != http.StatusOK {
			_ = reader.Close()
		}
	}()
	return transport, nil
}

func (t *grpcHTTPTransport) Write(ctx context.Context, message any) error {
	frame, err := EncodeFrame(message)
	if err != nil {
		return err
	}
	payload, err := proto.Marshal(&codemodev1.FramedMessage{Payload: frame.Payload})
	if err != nil {
		return err
	}
	if len(payload) > ProtocolMaxFrameBytes {
		return status.Error(codes.ResourceExhausted, "code-mode gRPC frame exceeds limit")
	}
	data := make([]byte, 5+len(payload))
	binary.BigEndian.PutUint32(data[1:5], uint32(len(payload)))
	copy(data[5:], payload)
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	_, err = t.writer.Write(data)
	if err != nil {
		select {
		case <-t.ready:
			if t.err != nil {
				return t.err
			}
			if t.response.StatusCode != http.StatusOK {
				return nil
			} // Read reports the response's exact status.
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return err
}

func (t *grpcHTTPTransport) Read(ctx context.Context, target any) (bool, error) {
	select {
	case <-t.ready:
	case <-ctx.Done():
		return false, ctx.Err()
	}
	if t.err != nil {
		return false, t.err
	}
	response := t.response
	if response.StatusCode != http.StatusOK {
		// Drain a bounded proxy body to obtain trailers without parsing it as gRPC.
		_, err := io.Copy(io.Discard, io.LimitReader(response.Body, 1<<20))
		if err != nil {
			return false, err
		}
		return false, grpcHTTPResponseStatus(response)
	}
	var header [5]byte
	if _, err := io.ReadFull(response.Body, header[:]); err != nil {
		if errors.Is(err, io.EOF) {
			return false, grpcHTTPResponseStatus(response)
		}
		return false, err
	}
	length := binary.BigEndian.Uint32(header[1:])
	if length > ProtocolMaxFrameBytes {
		return false, status.Error(codes.ResourceExhausted, "code-mode gRPC frame exceeds limit")
	}
	payload := make([]byte, int(length))
	if _, err := io.ReadFull(response.Body, payload); err != nil {
		return false, err
	}
	if header[0] == 1 {
		compressor := encoding.GetCompressor(response.Header.Get("Grpc-Encoding"))
		if compressor == nil {
			return false, status.Error(codes.Unimplemented, "unsupported gRPC response compression")
		}
		decoded, err := compressor.Decompress(bytes.NewReader(payload))
		if err != nil {
			return false, err
		}
		if closer, ok := decoded.(io.Closer); ok {
			defer closer.Close()
		}
		payload, err = io.ReadAll(io.LimitReader(decoded, ProtocolMaxFrameBytes+1))
		if err != nil {
			return false, err
		}
		if len(payload) > ProtocolMaxFrameBytes {
			return false, status.Error(codes.ResourceExhausted, "decoded code-mode gRPC frame exceeds limit")
		}
	} else if header[0] != 0 {
		return false, status.Error(codes.Internal, "invalid gRPC compression flag")
	}
	var message codemodev1.FramedMessage
	if err := proto.Unmarshal(payload, &message); err != nil {
		return false, err
	}
	return true, decodeGrpcFramedPayload(message.Payload, target)
}

func grpcHTTPResponseStatus(response *http.Response) error {
	raw := response.Trailer.Get("Grpc-Status")
	if raw == "" {
		raw = response.Header.Get("Grpc-Status")
	}
	if raw != "" {
		value, err := strconv.ParseUint(raw, 10, 32)
		if err != nil || value > 16 {
			return status.Error(codes.Unknown, "invalid gRPC response status")
		}
		code := codes.Code(value)
		if code == codes.OK && response.StatusCode != http.StatusOK {
			return status.Error(codes.Unavailable, "HTTP error cannot admit a code-mode session")
		}
		message := response.Trailer.Get("Grpc-Message")
		if message == "" {
			message = response.Header.Get("Grpc-Message")
		}
		if decoded, err := url.PathUnescape(message); err == nil {
			message = decoded
		}
		return status.Error(code, message)
	}
	code := codes.Unknown
	switch response.StatusCode {
	case 400:
		code = codes.Internal
	case 401:
		code = codes.Unauthenticated
	case 403:
		code = codes.PermissionDenied
	case 404:
		code = codes.Unimplemented
	case 429, 502, 503, 504:
		code = codes.Unavailable
	}
	return status.Error(code, "gRPC response missing status")
}

func (t *grpcHTTPTransport) Close() error {
	t.closeOnce.Do(func() {
		t.cancel()
		_ = t.writer.Close()
		<-t.ready
		if t.response != nil && t.response.Body != nil {
			_ = t.response.Body.Close()
		}
	})
	return nil
}
