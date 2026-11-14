package network

import (
	"bytes"
	"compress/gzip"
	"github.com/klauspost/compress/zstd"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestSharedHTTPCompressionLikeRust(t *testing.T) {
	for _, encoding := range []string{"gzip", "zstd"} {
		t.Run(encoding, func(t *testing.T) {
			var compressed bytes.Buffer
			if encoding == "gzip" {
				writer := gzip.NewWriter(&compressed)
				_, _ = writer.Write([]byte("decoded response"))
				_ = writer.Close()
			} else {
				writer, err := zstd.NewWriter(&compressed)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = writer.Write([]byte("decoded response"))
				_ = writer.Close()
			}
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.Header.Get("Accept-Encoding"), encoding) {
					t.Errorf("Accept-Encoding=%q missing %s", r.Header.Get("Accept-Encoding"), encoding)
				}
				w.Header().Set("Content-Encoding", encoding)
				_, _ = w.Write(compressed.Bytes())
			}))
			defer server.Close()
			client := NewHTTPClient(false, 0)
			defer client.CloseIdleConnections()
			response, err := client.Get(server.URL)
			if err != nil {
				t.Fatal(err)
			}
			defer response.Body.Close()
			body, err := io.ReadAll(response.Body)
			if err != nil || string(body) != "decoded response" {
				t.Fatalf("body=%q err=%v", body, err)
			}
			if response.Header.Get("Content-Encoding") != "" || response.ContentLength != -1 || !response.Uncompressed {
				t.Fatalf("decoded metadata=%#v", response)
			}
		})
	}
}

func TestCompressionTransportPreservesRequestAndBaseSettings(t *testing.T) {
	client := NewHTTPClient(true, 0)
	base := BaseTransport(client.Transport).Clone()
	base.MaxIdleConns = 7
	replaced := ReplaceBaseTransport(client.Transport, base)
	if BaseTransport(replaced) != base {
		t.Fatal("base replacement lost")
	}
	direct, ok := WithoutSystemProxy(replaced)
	if !ok || BaseTransport(direct).Proxy != nil || BaseTransport(direct).MaxIdleConns != 7 {
		t.Fatal("direct conversion lost settings")
	}
	if _, ok := direct.(*compressionTransport); !ok {
		t.Fatal("direct conversion lost compression")
	}
	for _, tc := range []struct{ method, encoding, rangeHeader, want string }{
		{"GET", "", "", "gzip, zstd"}, {"HEAD", "", "", ""}, {"GET", "identity", "", "identity"}, {"GET", "", "bytes=0-10", ""},
	} {
		request, _ := http.NewRequest(tc.method, "https://example.com", nil)
		if tc.encoding != "" {
			request.Header.Set("Accept-Encoding", tc.encoding)
		}
		if tc.rangeHeader != "" {
			request.Header.Set("Range", tc.rangeHeader)
		}
		transport := &compressionTransport{next: roundTripFunc(func(r *http.Request) (*http.Response, error) {
			if r.Header.Get("Accept-Encoding") != tc.want {
				t.Errorf("encoding=%q want %q", r.Header.Get("Accept-Encoding"), tc.want)
			}
			return &http.Response{StatusCode: 200, Header: make(http.Header), Body: http.NoBody}, nil
		})}
		response, err := transport.RoundTrip(request)
		if err != nil {
			t.Fatal(err)
		}
		_ = response.Body.Close()
		if request.Header.Get("Accept-Encoding") != tc.encoding {
			t.Fatal("caller request mutated")
		}
	}
}

type compressionTrackedBody struct {
	io.Reader
	closed bool
}

func (b *compressionTrackedBody) Close() error { b.closed = true; return nil }

func TestCompressionClosesUnderlyingBodyLikeRust(t *testing.T) {
	for _, encoding := range []string{"gzip", "zstd"} {
		for _, malformed := range []bool{false, true} {
			var data bytes.Buffer
			if malformed {
				data.WriteString("invalid compressed fixture")
			} else if encoding == "gzip" {
				writer := gzip.NewWriter(&data)
				_, _ = writer.Write([]byte("fixture"))
				_ = writer.Close()
			} else {
				writer, err := zstd.NewWriter(&data)
				if err != nil {
					t.Fatal(err)
				}
				_, _ = writer.Write([]byte("fixture"))
				_ = writer.Close()
			}
			body := &compressionTrackedBody{Reader: bytes.NewReader(data.Bytes())}
			transport := &compressionTransport{next: roundTripFunc(func(*http.Request) (*http.Response, error) {
				return &http.Response{StatusCode: 200, Header: http.Header{"Content-Encoding": []string{encoding}}, ContentLength: -1, Body: body}, nil
			})}
			request, _ := http.NewRequest("GET", "https://example.com", nil)
			response, err := transport.RoundTrip(request)
			if err == nil {
				_, err = io.ReadAll(response.Body)
				_ = response.Body.Close()
			}
			if (err != nil) != malformed {
				t.Errorf("encoding=%s malformed=%v error=%v", encoding, malformed, err)
			}
			if !body.closed {
				t.Errorf("encoding=%s malformed=%v body not closed", encoding, malformed)
			}
		}
	}
}
