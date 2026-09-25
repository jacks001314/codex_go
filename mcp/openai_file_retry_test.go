package mcp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// Rust parity: codex-rs/codex-api/src/files_retry_tests.rs (#47393/#47926): the
// blob upload retries the transient gateway statuses within one deadline, always
// sends the complete contents with a fresh client request ID, and never retries
// terminal statuses or a retry delay that cannot fit the remaining budget.
func TestOpenAIFileBlobUploadRetryBehaviorLikeRust(t *testing.T) {
	for _, testCase := range []struct {
		name              string
		statuses          []int
		retryAfterSeconds string
		retryAfterMillis  string
		deadline          time.Duration
		wantAttempts      int
		wantErr           bool
	}{
		{name: "502 then success", statuses: []int{502, 200}, wantAttempts: 2},
		{name: "503 then success", statuses: []int{503, 200}, wantAttempts: 2},
		{name: "504 then success", statuses: []int{504, 200}, wantAttempts: 2},
		{name: "mixed transient then success", statuses: []int{502, 504, 503, 200}, wantAttempts: 4},
		{name: "exhausted attempts", statuses: []int{502, 502, 502, 502, 502}, wantAttempts: 5, wantErr: true},
		{name: "forbidden is terminal", statuses: []int{403}, wantAttempts: 1, wantErr: true},
		{name: "conflict is terminal", statuses: []int{409}, wantAttempts: 1, wantErr: true},
		{name: "server error is terminal", statuses: []int{500}, wantAttempts: 1, wantErr: true},
		{
			name:              "retry delay exceeds the budget",
			statuses:          []int{503},
			retryAfterSeconds: "301",
			deadline:          50 * time.Millisecond,
			wantAttempts:      1,
			wantErr:           true,
		},
		{
			name:             "azure millisecond hint exceeds the budget",
			statuses:         []int{502},
			retryAfterMillis: "300000",
			deadline:         50 * time.Millisecond,
			wantAttempts:     1,
			wantErr:          true,
		},
		{
			name:             "zero delay retries immediately",
			statuses:         []int{503, 200},
			retryAfterMillis: "0",
			wantAttempts:     2,
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			var (
				mu         sync.Mutex
				attempts   int
				requestIDs []string
				bodies     []string
			)
			var server *httptest.Server
			server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				switch request.URL.Path {
				case "/backend-api/files":
					writeOpenAIFileTestJSON(t, w, map[string]any{"file_id": "file_retry", "upload_url": server.URL + "/blob/file_retry"})
				case "/blob/file_retry":
					body, _ := io.ReadAll(request.Body)
					mu.Lock()
					index := attempts
					attempts++
					requestIDs = append(requestIDs, request.Header.Get("x-ms-client-request-id"))
					bodies = append(bodies, string(body))
					mu.Unlock()
					status := http.StatusOK
					if index < len(testCase.statuses) {
						status = testCase.statuses[index]
					}
					if status != http.StatusOK {
						if testCase.retryAfterSeconds != "" {
							w.Header().Set("retry-after", testCase.retryAfterSeconds)
						}
						if testCase.retryAfterMillis != "" {
							w.Header().Set("x-ms-retry-after-ms", testCase.retryAfterMillis)
						}
					}
					w.WriteHeader(status)
				case "/backend-api/files/file_retry/uploaded":
					writeOpenAIFileTestJSON(t, w, map[string]any{
						"status":       "success",
						"download_url": server.URL + "/download/file_retry",
						"file_name":    "retry.txt",
						"mime_type":    "text/plain",
					})
				default:
					http.NotFound(w, request)
				}
			}))
			defer server.Close()

			path := filepath.Join(t.TempDir(), "retry.txt")
			if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			uploader := &LocalOpenAIFileUploader{
				Auth:              &OpenAIFileAuth{ChatGPTBackend: true, BaseURL: server.URL + "/backend-api"},
				HTTPClient:        server.Client(),
				FinalizeInterval:  time.Millisecond,
				BlobUploadTimeout: testCase.deadline,
			}
			_, err := uploader.UploadOpenAIFile(context.Background(), OpenAIFileUploadRequest{Path: path, FileName: "retry.txt", FileSizeBytes: 7})
			if testCase.wantErr {
				if err == nil {
					t.Fatalf("UploadOpenAIFile() error = nil, want a terminal failure")
				}
			} else if err != nil {
				t.Fatalf("UploadOpenAIFile() error = %v", err)
			}
			mu.Lock()
			defer mu.Unlock()
			if attempts != testCase.wantAttempts {
				t.Fatalf("blob attempts = %d, want %d", attempts, testCase.wantAttempts)
			}
			for _, body := range bodies {
				if body != "payload" {
					t.Fatalf("attempt body = %q, want the complete contents", body)
				}
			}
			seen := map[string]bool{}
			for _, id := range requestIDs {
				if id == "" {
					t.Fatal("attempt omitted the x-ms-client-request-id header")
				}
				if seen[id] {
					t.Fatalf("reused azure client request id %q across attempts", id)
				}
				seen[id] = true
			}
		})
	}
}

// TestOpenAIFileBlobUploadRetriesTransportFailuresLikeRust pins the transport
// arm: a connection failure recovers on retry, while a read failure of the
// upload contents does not.
func TestOpenAIFileBlobUploadRetriesTransportFailuresLikeRust(t *testing.T) {
	var (
		mu       sync.Mutex
		attempts int
	)
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/backend-api/files":
			writeOpenAIFileTestJSON(t, w, map[string]any{"file_id": "file_transport", "upload_url": server.URL + "/blob/file_transport"})
		case "/blob/file_transport":
			mu.Lock()
			attempts++
			first := attempts == 1
			mu.Unlock()
			if first {
				// Abort the response mid-body so the client sees a stream failure.
				hijacker, ok := w.(http.Hijacker)
				if !ok {
					w.WriteHeader(http.StatusBadGateway)
					return
				}
				conn, _, err := hijacker.Hijack()
				if err != nil {
					w.WriteHeader(http.StatusBadGateway)
					return
				}
				_ = conn.Close()
				return
			}
			w.WriteHeader(http.StatusOK)
		case "/backend-api/files/file_transport/uploaded":
			writeOpenAIFileTestJSON(t, w, map[string]any{"status": "success", "download_url": server.URL + "/download/file_transport"})
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	path := filepath.Join(t.TempDir(), "transport.txt")
	if err := os.WriteFile(path, []byte("payload"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	uploader := &LocalOpenAIFileUploader{
		Auth:             &OpenAIFileAuth{ChatGPTBackend: true, BaseURL: server.URL + "/backend-api"},
		HTTPClient:       server.Client(),
		FinalizeInterval: time.Millisecond,
	}
	if _, err := uploader.UploadOpenAIFile(context.Background(), OpenAIFileUploadRequest{Path: path, FileName: "transport.txt", FileSizeBytes: 7}); err != nil {
		t.Fatalf("UploadOpenAIFile() error = %v, want recovery after the interrupted stream", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if attempts != 2 {
		t.Fatalf("blob attempts = %d, want 2", attempts)
	}

	// A read failure is terminal: the contents cannot be reopened.
	failingUploader := &LocalOpenAIFileUploader{
		Auth:       &OpenAIFileAuth{ChatGPTBackend: true, BaseURL: server.URL + "/backend-api"},
		HTTPClient: server.Client(),
	}
	attemptsBefore := attempts
	_, err := failingUploader.UploadOpenAIFile(context.Background(), OpenAIFileUploadRequest{
		FileName:      "missing.txt",
		FileSizeBytes: 7,
		Open: func(context.Context) (io.ReadCloser, error) {
			return nil, os.ErrNotExist
		},
	})
	if err == nil || !strings.Contains(err.Error(), "read") {
		t.Fatalf("read failure error = %v, want a terminal read failure", err)
	}
	if attempts != attemptsBefore {
		t.Fatalf("read failure still uploaded the blob (%d attempts)", attempts-attemptsBefore)
	}
}
