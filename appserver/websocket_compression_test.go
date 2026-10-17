package appserver

import (
	"context"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
)

func TestZstdStreamCodecRetainsHistoryAcrossMessages(t *testing.T) {
	encoder, err := NewZstdEncoder()
	if err != nil {
		t.Fatalf("NewZstdEncoder() error = %v", err)
	}
	defer encoder.Close()
	decoder, err := NewZstdDecoder(MaxWebsocketDecompressedMessageSize)
	if err != nil {
		t.Fatalf("NewZstdDecoder() error = %v", err)
	}
	defer decoder.Close()

	messages := []string{
		`{"jsonrpc":"2.0","id":1,"method":"config/read","params":{}}`,
		`{"jsonrpc":"2.0","id":2,"method":"config/read","params":{}}`,
		`{"jsonrpc":"2.0","id":3,"method":"config/read","params":{}}`,
	}
	encodedSizes := make([]int, 0, len(messages))
	for _, message := range messages {
		payload, err := encoder.Encode(message)
		if err != nil {
			t.Fatalf("Encode(%q) error = %v", message, err)
		}
		encodedSizes = append(encodedSizes, len(payload))
		decoded, err := decoder.Decode(payload)
		if err != nil {
			t.Fatalf("Decode(%q) error = %v", message, err)
		}
		if decoded != message {
			t.Fatalf("Decode() = %q, want %q", decoded, message)
		}
	}
	if encodedSizes[2] >= encodedSizes[0] {
		t.Fatalf("retained stream did not reuse history: sizes = %v", encodedSizes)
	}
}

func TestWebsocketCompressionNegotiationFallback(t *testing.T) {
	if got, err := WebsocketCompressionFromResponseHeader(""); err != nil || got != WebsocketCompressionPlain {
		t.Fatalf("missing header = %v, %v; want plain fallback", got, err)
	}
	if got, err := WebsocketCompressionFromResponseHeader(WebsocketZstdStreamProtocol); err != nil || got != WebsocketCompressionZstd {
		t.Fatalf("zstd header = %v, %v", got, err)
	}
	if _, err := WebsocketCompressionFromResponseHeader("br-stream-v1"); err == nil {
		t.Fatal("unknown compression protocol succeeded")
	}
}

func TestZstdDecoderEnforcesMessageLimit(t *testing.T) {
	encoder, err := NewZstdEncoder()
	if err != nil {
		t.Fatalf("NewZstdEncoder() error = %v", err)
	}
	defer encoder.Close()
	decoder, err := NewZstdDecoder(64)
	if err != nil {
		t.Fatalf("NewZstdDecoder() error = %v", err)
	}
	defer decoder.Close()
	payload, err := encoder.Encode(strings.Repeat("x", 128))
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if _, err := decoder.Decode(payload); err == nil {
		t.Fatal("oversized decompressed message succeeded")
	}
}

func TestZstdStreamCodecHandlesLargeIncompressibleMessage(t *testing.T) {
	encoder, err := NewZstdEncoder()
	if err != nil {
		t.Fatalf("NewZstdEncoder() error = %v", err)
	}
	defer encoder.Close()
	decoder, err := NewZstdDecoder(MaxWebsocketDecompressedMessageSize)
	if err != nil {
		t.Fatalf("NewZstdDecoder() error = %v", err)
	}
	defer decoder.Close()

	content := make([]byte, 4<<20)
	if _, err := rand.Read(content); err != nil {
		t.Fatalf("rand.Read() error = %v", err)
	}
	for index := range content {
		content[index] &= 0x7f
	}
	payload, err := encoder.Encode(string(content))
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	decoded, err := decoder.Decode(payload)
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	if decoded != string(content) {
		t.Fatal("large compressed round trip changed the payload")
	}
	payload, err = encoder.Encode(`{"after":"large"}`)
	if err != nil {
		t.Fatalf("Encode(after large) error = %v", err)
	}
	decoded, err = decoder.Decode(payload)
	if err != nil {
		t.Fatalf("Decode(after large) error = %v", err)
	}
	if decoded != `{"after":"large"}` {
		t.Fatalf("Decode(after large) = %q", decoded)
	}
}

func TestZstdDecoderRejectsInvalidUTF8(t *testing.T) {
	encoder, err := NewZstdEncoder()
	if err != nil {
		t.Fatalf("NewZstdEncoder() error = %v", err)
	}
	defer encoder.Close()
	decoder, err := NewZstdDecoder(MaxWebsocketDecompressedMessageSize)
	if err != nil {
		t.Fatalf("NewZstdDecoder() error = %v", err)
	}
	defer decoder.Close()
	payload, err := encoder.Encode(string([]byte{0xff, 0xfe}))
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	if _, err := decoder.Decode(payload); err == nil {
		t.Fatal("invalid UTF-8 message succeeded")
	}
}

func TestZstdDecoderCancellationClosesBlockedStream(t *testing.T) {
	encoder, err := NewZstdEncoder()
	if err != nil {
		t.Fatalf("NewZstdEncoder() error = %v", err)
	}
	defer encoder.Close()
	decoder, err := NewZstdDecoder(MaxWebsocketDecompressedMessageSize)
	if err != nil {
		t.Fatalf("NewZstdDecoder() error = %v", err)
	}
	defer decoder.Close()
	payload, err := encoder.Encode(strings.Repeat("truncated", 1024))
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if _, err := decoder.DecodeContext(ctx, payload[:len(payload)/2]); err == nil {
		t.Fatal("truncated compressed message succeeded")
	}
}

func TestZstdDecoderRejectsMalformedInput(t *testing.T) {
	decoder, err := NewZstdDecoder(MaxWebsocketDecompressedMessageSize)
	if err != nil {
		t.Fatalf("NewZstdDecoder() error = %v", err)
	}
	defer decoder.Close()
	if _, err := decoder.Decode([]byte("not a zstd stream")); err == nil {
		t.Fatal("malformed zstd input succeeded")
	}
}

func TestZstdDecoderRejectsOversizedWindow(t *testing.T) {
	encoder, err := zstd.NewWriter(nil, zstd.WithWindowSize(2<<20))
	if err != nil {
		t.Fatalf("zstd.NewWriter() error = %v", err)
	}
	content := make([]byte, 4<<20)
	if _, err := rand.Read(content); err != nil {
		t.Fatalf("rand.Read() error = %v", err)
	}
	payload := encoder.EncodeAll(content, nil)
	if err := encoder.Close(); err != nil {
		t.Fatalf("encoder.Close() error = %v", err)
	}
	decoder, err := NewZstdDecoder(MaxWebsocketDecompressedMessageSize)
	if err != nil {
		t.Fatalf("NewZstdDecoder() error = %v", err)
	}
	defer decoder.Close()
	if _, err := decoder.Decode(payload); err == nil {
		t.Fatal("oversized zstd window succeeded")
	}
}
