package mcp

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"
)

type fakeFileRuntimeAuth struct {
	destinations []string
}

func (f *fakeFileRuntimeAuth) HeadersForDestination(_ context.Context, destination string) (http.Header, error) {
	f.destinations = append(f.destinations, destination)
	return http.Header{
		"Authorization":            []string{"AgentAssertion 1.header.payload.signature"},
		"x-openai-agent-assertion": []string{"1.header.payload.signature"},
		"chatgpt-account-id":       []string{"account-1"},
	}, nil
}

// Mirrors Rust #59564: file storage requests carry the runtime assertion, the
// create response's upload_headers apply to the blob PUT only, and the blob
// request keeps the static API credentials off it.
func TestLocalOpenAIFileUploaderAppliesUploadHeadersAndRuntimeAssertionsLikeRust(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/backend-api/files":
			if got := request.Header.Get("Authorization"); got != "AgentAssertion 1.header.payload.signature" {
				t.Errorf("create Authorization = %q", got)
			}
			if request.Header.Get("x-openai-agent-assertion") == "" {
				t.Error("create request is missing the runtime assertion")
			}
			if request.Header.Get("x-amz-tagging") != "" {
				t.Error("upload headers must not reach the create request")
			}
			writeOpenAIFileTestJSON(t, w, map[string]any{
				"file_id":    "file_123",
				"upload_url": server.URL + "/blob/file_123",
				"upload_headers": map[string]any{
					"Content-Type":        "text/plain",
					"Content-Disposition": "attachment; filename=hello.txt",
					"x-amz-tagging":       "purpose=codex",
				},
			})
		case "/blob/file_123":
			contents, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read blob: %v", err)
			}
			if string(contents) != "hello" {
				t.Errorf("blob body = %q", contents)
			}
			if got := request.Header.Get("Content-Type"); got != "text/plain" {
				t.Errorf("blob Content-Type = %q", got)
			}
			if got := request.Header.Get("Content-Disposition"); got != "attachment; filename=hello.txt" {
				t.Errorf("blob Content-Disposition = %q", got)
			}
			if got := request.Header.Get("x-amz-tagging"); got != "purpose=codex" {
				t.Errorf("blob x-amz-tagging = %q", got)
			}
			if request.Header.Get("Authorization") != "" || request.Header.Get("chatgpt-account-id") != "" {
				t.Errorf("blob request carried API credentials: %v", request.Header)
			}
			w.WriteHeader(http.StatusOK)
		case "/backend-api/files/file_123/uploaded":
			if got := request.Header.Get("Authorization"); got != "AgentAssertion 1.header.payload.signature" {
				t.Errorf("finalize Authorization = %q", got)
			}
			if request.Header.Get("x-amz-tagging") != "" {
				t.Error("upload headers must not reach the finalize request")
			}
			writeOpenAIFileTestJSON(t, w, map[string]any{
				"status":       "success",
				"download_url": server.URL + "/download/file_123",
				"file_name":    "hello.txt",
				"mime_type":    "text/plain",
			})
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "hello.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	runtimeAuth := &fakeFileRuntimeAuth{}
	uploader := &LocalOpenAIFileUploader{
		Auth: &OpenAIFileAuth{
			ChatGPTBackend: true,
			BaseURL:        server.URL + "/backend-api",
			Headers: http.Header{
				"Authorization": []string{"Bearer static"},
			},
			RuntimeAuth: runtimeAuth,
		},
		HTTPClient:       server.Client(),
		FinalizeInterval: time.Millisecond,
	}
	uploaded, err := uploader.UploadOpenAIFile(context.Background(), OpenAIFileUploadRequest{Path: path, FileName: "hello.txt", FileSizeBytes: 5})
	if err != nil {
		t.Fatalf("UploadOpenAIFile() error = %v", err)
	}
	if uploaded.FileID != "file_123" || len(runtimeAuth.destinations) != 2 {
		t.Fatalf("uploaded = %#v runtime destinations = %#v", uploaded, runtimeAuth.destinations)
	}
}
