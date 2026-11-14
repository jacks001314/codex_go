package network

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"

	"github.com/klauspost/compress/zstd"
)

// compressionTransport supplies the shared client's gzip/zstd response decoding.
type compressionTransport struct{ next http.RoundTripper }

func (t *compressionTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	nextRequest := request
	if request.Header.Get("Accept-Encoding") == "" && request.Header.Get("Range") == "" && request.Method != http.MethodHead {
		nextRequest = request.Clone(request.Context())
		nextRequest.Header.Set("Accept-Encoding", "gzip, zstd")
	}
	response, err := t.next.RoundTrip(nextRequest)
	if err != nil {
		return nil, err
	}
	if response.Body == nil || response.ContentLength == 0 || request.Method == http.MethodHead {
		return response, nil
	}
	body := response.Body
	var decoded io.Reader
	var closeDecoder func()
	switch strings.ToLower(response.Header.Get("Content-Encoding")) {
	case "gzip":
		reader, err := gzip.NewReader(body)
		if err != nil {
			_ = body.Close()
			return nil, err
		}
		decoded = reader
		closeDecoder = func() { _ = reader.Close() }
	case "zstd":
		reader, err := zstd.NewReader(body, zstd.WithDecoderConcurrency(1))
		if err != nil {
			_ = body.Close()
			return nil, err
		}
		decoded = reader
		closeDecoder = reader.Close
	default:
		return response, nil
	}
	response.Body = &decompressedBody{Reader: decoded, body: body, closeDecoder: closeDecoder}
	response.Header.Del("Content-Encoding")
	response.Header.Del("Content-Length")
	response.ContentLength = -1
	response.Uncompressed = true
	return response, nil
}

func (t *compressionTransport) CloseIdleConnections() {
	if closer, ok := t.next.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
}

type decompressedBody struct {
	io.Reader
	body         io.ReadCloser
	closeDecoder func()
}

func (b *decompressedBody) Close() error {
	b.closeDecoder()
	return b.body.Close()
}
