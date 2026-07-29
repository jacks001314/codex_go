package mcp

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type fakeUploader struct {
	requests []OpenAIFileUploadRequest
}

func (u *fakeUploader) UploadOpenAIFile(ctx context.Context, request OpenAIFileUploadRequest) (*OpenAIUploadedFile, error) {
	_ = ctx
	u.requests = append(u.requests, request)
	return &OpenAIUploadedFile{
		DownloadURL:   "http://download/" + request.FileName,
		FileID:        "file_123",
		MimeType:      "text/plain",
		FileName:      request.FileName,
		URI:           "sediment://file_123",
		FileSizeBytes: request.FileSizeBytes,
	}, nil
}

func TestRewriteArgumentsRequiresDeclaredFileParams(t *testing.T) {
	args := map[string]any{"file": "report.txt"}
	rewriter := NewOpenAIFileRewriter(t.TempDir(), &OpenAIFileAuth{ChatGPTBackend: true}, nil)
	rewritten, err := rewriter.RewriteArguments(context.Background(), args, nil)
	if err != nil {
		t.Fatalf("rewrite failed: %v", err)
	}
	object, ok := rewritten.(map[string]any)
	if !ok || object["file"] != "report.txt" {
		t.Fatalf("expected original arguments when params are absent, got %#v", rewritten)
	}
}

func TestRewriteArgumentsUploadsScalarPath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	uploader := &fakeUploader{}
	rewriter := NewOpenAIFileRewriter(dir, &OpenAIFileAuth{ChatGPTBackend: true}, uploader)
	rewritten, err := rewriter.RewriteArguments(context.Background(), map[string]any{"file": "report.txt", "x": "y"}, []string{"file"})
	if err != nil {
		t.Fatalf("rewrite failed: %v", err)
	}
	object := rewritten.(map[string]any)
	uploaded := object["file"].(map[string]any)
	if uploaded["file_id"] != "file_123" || uploaded["download_url"] != "http://download/report.txt" {
		t.Fatalf("unexpected upload: %#v", uploaded)
	}
	if _, ok := uploaded["file_name"]; ok {
		t.Fatalf("undeclared optional file_name was included: %#v", uploaded)
	}
	if object["x"] != "y" {
		t.Fatalf("unrelated argument changed: %#v", object)
	}
	if len(uploader.requests) != 1 || uploader.requests[0].Path != path {
		t.Fatalf("unexpected upload requests: %#v", uploader.requests)
	}
}

func TestRewriteArgumentsUploadsArrayPaths(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.txt"), []byte("one"), 0o600); err != nil {
		t.Fatalf("write one: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "two.txt"), []byte("two"), 0o600); err != nil {
		t.Fatalf("write two: %v", err)
	}
	rewriter := NewOpenAIFileRewriter(dir, &OpenAIFileAuth{ChatGPTBackend: true}, &fakeUploader{})
	rewritten, err := rewriter.RewriteArguments(context.Background(), map[string]any{"files": []any{"one.txt", "two.txt"}}, []string{"files"})
	if err != nil {
		t.Fatalf("rewrite failed: %v", err)
	}
	values := rewritten.(map[string]any)["files"].([]any)
	if len(values) != 2 {
		t.Fatalf("expected two uploaded values, got %#v", values)
	}
}

func TestRewriteArgumentsLeavesArrayObjectPathsUnchangedLikeRust(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.txt"), []byte("one"), 0o600); err != nil {
		t.Fatalf("write one: %v", err)
	}
	uploader := &fakeUploader{}
	rewriter := NewOpenAIFileRewriter(dir, &OpenAIFileAuth{ChatGPTBackend: true}, uploader)
	rewritten, err := rewriter.RewriteArguments(context.Background(), map[string]any{
		"files": []any{map[string]any{"path": "one.txt", "label": "One"}},
	}, []string{"files"})
	if err != nil {
		t.Fatalf("rewrite failed: %v", err)
	}
	values := rewritten.(map[string]any)["files"].([]any)
	item := values[0].(map[string]any)
	if item["label"] != "One" || item["path"] != "one.txt" || len(uploader.requests) != 0 {
		t.Fatalf("unsupported array object was rewritten: %#v requests=%#v", item, uploader.requests)
	}
}

func TestRewriteArgumentsIncludesOnlySchemaSupportedOptionalFields(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "report.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	rewriter := NewOpenAIFileRewriter(dir, &OpenAIFileAuth{ChatGPTBackend: true}, &fakeUploader{})
	rewritten, err := rewriter.RewriteArgumentsWithOptionalFields(context.Background(), map[string]any{"file": "report.txt"}, map[string][]string{
		"file": {"mime_type", "file_name"},
	})
	if err != nil {
		t.Fatalf("rewrite failed: %v", err)
	}
	uploaded := rewritten.(map[string]any)["file"].(map[string]any)
	if uploaded["file_name"] != "report.txt" || uploaded["mime_type"] != "text/plain" || uploaded["file_id"] != "file_123" {
		t.Fatalf("uploaded payload = %#v", uploaded)
	}
	for _, omitted := range []string{"uri", "file_size_bytes"} {
		if _, ok := uploaded[omitted]; ok {
			t.Fatalf("internal field %q leaked: %#v", omitted, uploaded)
		}
	}
}

func TestRewriteArgumentsRejectsMissingAuth(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "report.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	rewriter := NewOpenAIFileRewriter(dir, nil, nil)
	_, err := rewriter.RewriteArguments(context.Background(), map[string]any{"file": "report.txt"}, []string{"file"})
	if err == nil || err.Error() != "ChatGPT auth is required to upload files for Codex Apps tools" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRewriteArgumentsRejectsOversizedFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "big.bin")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	rewriter := NewOpenAIFileRewriter(dir, &OpenAIFileAuth{ChatGPTBackend: true}, nil)
	rewriter.UploadLimitBytes = 4
	_, err := rewriter.RewriteArguments(context.Background(), map[string]any{"file": "big.bin"}, []string{"file"})
	if err == nil {
		t.Fatalf("expected oversized file error")
	}
}

func TestLocalOpenAIFileUploaderStreamsAndFinalizesLikeRust(t *testing.T) {
	var finalizeAttempts atomic.Int32
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/backend-api/files":
			if request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer test" || request.Header.Get("ChatGPT-Account-ID") != "account-1" {
				t.Errorf("create request = %s headers=%v", request.Method, request.Header)
			}
			var body map[string]any
			if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
				t.Errorf("decode create body: %v", err)
			}
			if body["file_name"] != "hello.txt" || body["file_size"] != float64(5) || body["use_case"] != "codex" {
				t.Errorf("create body = %#v", body)
			}
			writeOpenAIFileTestJSON(t, w, map[string]any{"file_id": "file_123", "upload_url": server.URL + "/blob/file_123?sig=secret"})
		case "/blob/file_123":
			contents, err := io.ReadAll(request.Body)
			if err != nil {
				t.Errorf("read blob: %v", err)
			}
			if request.Method != http.MethodPut || string(contents) != "hello" || request.ContentLength != 5 || request.Header.Get("x-ms-blob-type") != "BlockBlob" || request.Header.Get("x-ms-client-request-id") == "" {
				t.Errorf("blob request = %s length=%d headers=%v body=%q", request.Method, request.ContentLength, request.Header, contents)
			}
			w.WriteHeader(http.StatusOK)
		case "/backend-api/files/file_123/uploaded":
			if request.Method != http.MethodPost || request.Header.Get("Authorization") != "Bearer test" {
				t.Errorf("finalize request = %s headers=%v", request.Method, request.Header)
			}
			if finalizeAttempts.Add(1) == 1 {
				writeOpenAIFileTestJSON(t, w, map[string]any{"status": "retry"})
				return
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
	uploader := &LocalOpenAIFileUploader{
		Auth: &OpenAIFileAuth{
			ChatGPTBackend: true,
			BaseURL:        server.URL + "/backend-api",
			Headers: http.Header{
				"Authorization":      []string{"Bearer test"},
				"ChatGPT-Account-ID": []string{"account-1"},
			},
		},
		HTTPClient:       server.Client(),
		FinalizeInterval: time.Millisecond,
	}
	uploaded, err := uploader.UploadOpenAIFile(context.Background(), OpenAIFileUploadRequest{Path: path, FileName: "hello.txt", FileSizeBytes: 5})
	if err != nil {
		t.Fatalf("UploadOpenAIFile() error = %v", err)
	}
	if uploaded.FileID != "file_123" || uploaded.URI != "sediment://file_123" || uploaded.FileName != "hello.txt" || uploaded.MimeType != "text/plain" || uploaded.FileSizeBytes != 5 || finalizeAttempts.Load() != 2 {
		t.Fatalf("uploaded = %#v finalize attempts=%d", uploaded, finalizeAttempts.Load())
	}
}

func TestLocalOpenAIFileUploaderRedactsSignedBlobURL(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/backend-api/files":
			writeOpenAIFileTestJSON(t, w, map[string]any{"file_id": "file_123", "upload_url": server.URL + "/blob?sig=secret"})
		case "/blob":
			w.Header().Set("x-ms-request-id", "azure-request")
			w.Header().Set("x-ms-error-code", "ServerBusy")
			http.Error(w, "signed response body must not leak", http.StatusInternalServerError)
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	path := filepath.Join(t.TempDir(), "hello.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	uploader := &LocalOpenAIFileUploader{Auth: &OpenAIFileAuth{ChatGPTBackend: true, BaseURL: server.URL + "/backend-api"}, HTTPClient: server.Client()}
	_, err := uploader.UploadOpenAIFile(context.Background(), OpenAIFileUploadRequest{Path: path, FileName: "hello.txt", FileSizeBytes: 5})
	if err == nil {
		t.Fatal("UploadOpenAIFile() error = nil")
	}
	message := err.Error()
	for _, want := range []string{"status 500", "azure_client_request_id=", "azure_request_id=azure-request", "azure_error_code=ServerBusy"} {
		if !strings.Contains(message, want) {
			t.Fatalf("error %q missing %q", message, want)
		}
	}
	for _, secret := range []string{"sig=secret", "signed response body must not leak"} {
		if strings.Contains(message, secret) {
			t.Fatalf("error leaked %q: %s", secret, message)
		}
	}
}

func writeOpenAIFileTestJSON(t *testing.T, w http.ResponseWriter, value any) {
	t.Helper()
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(value); err != nil {
		t.Fatalf("encode response: %v", err)
	}
}
