package codemode

// Rust #57212: the code-mode host compresses gRPC responses with zstd when the
// client advertises support, and keeps identity responses for clients that do
// not. gRPC-Go advertises every registered compressor through
// `grpc-accept-encoding`, and a server handler selects one per stream with
// `grpc.SetSendCompressor`, so registration plus that call mirrors tonic's
// `send_compressed`/`accept_compressed` pairing.

import (
	"io"
	"sync"

	"github.com/klauspost/compress/zstd"
	"google.golang.org/grpc/encoding"
)

// grpcZstdEncodingName is the encoding name both tonic and gRPC-Go use.
const grpcZstdEncodingName = "zstd"

var (
	grpcZstdRegisterOnce sync.Once
	grpcZstdShared       = &grpcZstdCodec{}
)

// ensureGrpcZstdRegistered registers the zstd codec once per process. gRPC
// clients then advertise it in `grpc-accept-encoding` and servers honor it.
func ensureGrpcZstdRegistered() {
	grpcZstdRegisterOnce.Do(func() {
		encoding.RegisterCompressor(grpcZstdShared)
	})
}

// grpcZstdCodec adapts klauspost/compress zstd to gRPC's codec interface. The
// encoder uses the fastest level, matching Rust's latency-over-ratio choice.
type grpcZstdCodec struct {
	encoders sync.Pool
}

func (c *grpcZstdCodec) Name() string { return grpcZstdEncodingName }

func (c *grpcZstdCodec) Compress(writer io.Writer) (io.WriteCloser, error) {
	if encoder, ok := c.encoders.Get().(*zstd.Encoder); ok && encoder != nil {
		encoder.Reset(writer)
		return &grpcZstdWriter{encoder: encoder, pool: &c.encoders}, nil
	}
	encoder, err := zstd.NewWriter(writer,
		zstd.WithEncoderLevel(zstd.SpeedFastest),
		zstd.WithEncoderConcurrency(1),
	)
	if err != nil {
		return nil, err
	}
	return &grpcZstdWriter{encoder: encoder, pool: &c.encoders}, nil
}

func (c *grpcZstdCodec) Decompress(reader io.Reader) (io.Reader, error) {
	decoder, err := zstd.NewReader(reader, zstd.WithDecoderConcurrency(1))
	if err != nil {
		return nil, err
	}
	return &grpcZstdReader{decoder: decoder}, nil
}

type grpcZstdWriter struct {
	encoder *zstd.Encoder
	pool    *sync.Pool
}

func (w *grpcZstdWriter) Write(p []byte) (int, error) { return w.encoder.Write(p) }

func (w *grpcZstdWriter) Close() error {
	err := w.encoder.Close()
	w.pool.Put(w.encoder)
	return err
}

// grpcZstdReader releases the decoder once the stream is exhausted.
type grpcZstdReader struct {
	decoder *zstd.Decoder
	closed  bool
}

func (r *grpcZstdReader) Read(p []byte) (int, error) {
	n, err := r.decoder.Read(p)
	if err != nil && !r.closed {
		r.closed = true
		r.decoder.Close()
	}
	return n, err
}
