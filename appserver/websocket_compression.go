package appserver

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/klauspost/compress/zstd"
)

const (
	// WebsocketCompressionHeader negotiates app-server WebSocket payload
	// compression. Both the request and response must carry the protocol value.
	WebsocketCompressionHeader = "x-codex-app-server-compression"
	// WebsocketZstdStreamProtocol selects one persistent zstd stream per
	// direction across the lifetime of the connection.
	WebsocketZstdStreamProtocol = "zstd-stream-v1"
	// MaxWebsocketDecompressedMessageSize is the decoded JSON message limit.
	MaxWebsocketDecompressedMessageSize = 64 << 20
	websocketZstdWindowSize             = 1 << 20
	websocketCompressionDecodeTimeout   = 30 * time.Second
)

// WebsocketCompression identifies the negotiated WebSocket payload format.
type WebsocketCompression int

const (
	// WebsocketCompressionPlain keeps JSON text messages.
	WebsocketCompressionPlain WebsocketCompression = iota
	// WebsocketCompressionZstd sends JSON as binary zstd-stream messages.
	WebsocketCompressionZstd
)

// WebsocketCompressionFromResponseHeader validates the negotiated protocol
// returned by the server. A missing header keeps the existing text protocol;
// an unknown protocol fails instead of silently changing formats.
func WebsocketCompressionFromResponseHeader(value string) (WebsocketCompression, error) {
	switch value {
	case "":
		return WebsocketCompressionPlain, nil
	case WebsocketZstdStreamProtocol:
		return WebsocketCompressionZstd, nil
	default:
		return WebsocketCompressionPlain, fmt.Errorf(
			"unsupported app-server WebSocket compression protocol %q", value)
	}
}

// ZstdEncoder maintains one zstd stream across encoded messages. Flush marks a
// message boundary without ending the frame or discarding the compression
// history.
type ZstdEncoder struct {
	mu      sync.Mutex
	encoder *zstd.Encoder
	payload bytes.Buffer
	closed  bool
}

// NewZstdEncoder creates a low-latency encoder bounded to a one-MiB window.
func NewZstdEncoder() (*ZstdEncoder, error) {
	encoder, err := zstd.NewWriter(
		&bytes.Buffer{},
		zstd.WithEncoderLevel(zstd.SpeedFastest),
		zstd.WithEncoderConcurrency(1),
		zstd.WithWindowSize(websocketZstdWindowSize),
	)
	if err != nil {
		return nil, err
	}
	compressor := &ZstdEncoder{encoder: encoder}
	encoder.Reset(&compressor.payload)
	return compressor, nil
}

// Encode writes one JSON message and flushes it as a zstd block without ending
// the stream.
func (e *ZstdEncoder) Encode(text string) ([]byte, error) {
	if e == nil || e.encoder == nil {
		return nil, errors.New("zstd encoder is not configured")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil, errors.New("zstd encoder is closed")
	}
	e.payload.Reset()
	if _, err := io.WriteString(e.encoder, text); err != nil {
		return nil, err
	}
	if err := e.encoder.Flush(); err != nil {
		return nil, err
	}
	return append([]byte(nil), e.payload.Bytes()...), nil
}

// Close releases the encoder after flushing its final stream state.
func (e *ZstdEncoder) Close() error {
	if e == nil || e.encoder == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	e.closed = true
	return e.encoder.Close()
}

// ZstdDecoder maintains the peer's zstd stream across messages and bounds the
// decoded size of each message.
type ZstdDecoder struct {
	mu             sync.Mutex
	decoder        *zstd.Decoder
	source         *websocketZstdMessageSource
	maxMessageSize int
	messageBuffer  []byte
	closed         bool
}

// NewZstdDecoder creates a decoder whose window and decoded message size are
// bounded by the protocol limits.
func NewZstdDecoder(maxMessageSize int) (*ZstdDecoder, error) {
	if maxMessageSize <= 0 {
		return nil, errors.New("zstd decoder message limit must be positive")
	}
	source := newWebsocketZstdMessageSource()
	decoder, err := zstd.NewReader(
		source,
		zstd.WithDecoderConcurrency(1),
		zstd.WithDecoderMaxWindow(websocketZstdWindowSize),
		zstd.WithDecoderMaxMemory(uint64(maxMessageSize)),
	)
	if err != nil {
		source.CloseWithError(err)
		return nil, err
	}
	return &ZstdDecoder{
		decoder:        decoder,
		source:         source,
		maxMessageSize: maxMessageSize,
		messageBuffer:  make([]byte, maxMessageSize+1),
	}, nil
}

// Decode consumes one flushed compressed message and returns exactly the JSON
// emitted for that message while retaining decoder history.
func (d *ZstdDecoder) Decode(payload []byte) (string, error) {
	return d.DecodeContext(context.Background(), payload)
}

// DecodeContext is Decode with cancellation support. Cancellation invalidates
// the compressed stream because a partially consumed block cannot be resumed
// safely.
func (d *ZstdDecoder) DecodeContext(ctx context.Context, payload []byte) (string, error) {
	if d == nil || d.decoder == nil {
		return "", errors.New("zstd decoder is not configured")
	}
	if len(payload) == 0 {
		return "", errors.New("app-server WebSocket compressed message is empty")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return "", errors.New("zstd decoder is closed")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	d.source.Feed(payload)
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ctx.Done():
			d.source.CloseWithError(ctx.Err())
		case <-finished:
		}
	}()
	decodedSize := 0
	for {
		if decodedSize >= len(d.messageBuffer) {
			return "", errors.New("decompressed app-server WebSocket message exceeds size limit")
		}
		n, err := d.decoder.Read(d.messageBuffer[decodedSize:])
		decodedSize += n
		if err != nil {
			return "", err
		}
		if decodedSize > d.maxMessageSize {
			return "", errors.New("decompressed app-server WebSocket message exceeds size limit")
		}
		if d.source.Buffered() == 0 {
			break
		}
	}
	if buffered := d.source.Buffered(); buffered != 0 {
		return "", fmt.Errorf("app-server WebSocket compressed message left %d unconsumed bytes", buffered)
	}
	decoded := d.messageBuffer[:decodedSize]
	if !utf8.Valid(decoded) {
		return "", errors.New("decompressed app-server WebSocket message is not valid UTF-8")
	}
	return string(decoded), nil
}

// Close releases decoder resources and wakes any blocked source read.
func (d *ZstdDecoder) Close() error {
	if d == nil || d.decoder == nil {
		return nil
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	d.source.CloseWithError(io.EOF)
	d.decoder.Close()
	return nil
}

// websocketZstdMessageSource presents successive compressed WebSocket messages
// to the persistent decoder as one continuous input stream.
type websocketZstdMessageSource struct {
	mu     sync.Mutex
	cond   *sync.Cond
	data   []byte
	err    error
	closed bool
}

func newWebsocketZstdMessageSource() *websocketZstdMessageSource {
	source := &websocketZstdMessageSource{}
	source.cond = sync.NewCond(&source.mu)
	return source
}

func (s *websocketZstdMessageSource) Feed(payload []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.data = append(s.data, payload...)
	s.cond.Broadcast()
}

func (s *websocketZstdMessageSource) Read(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for len(s.data) == 0 && !s.closed {
		s.cond.Wait()
	}
	if len(s.data) > 0 {
		n := copy(p, s.data)
		s.data = s.data[n:]
		if len(s.data) == 0 {
			s.data = nil
		}
		return n, nil
	}
	if s.err != nil {
		return 0, s.err
	}
	return 0, io.EOF
}

func (s *websocketZstdMessageSource) Buffered() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.data)
}

func (s *websocketZstdMessageSource) CloseWithError(err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.closed = true
	s.err = err
	s.cond.Broadcast()
}
