package mcp

import (
	"context"
	"os"
	"path/filepath"
	"testing"
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
	uploaded := object["file"].(*OpenAIUploadedFile)
	if uploaded.FileName != "report.txt" || uploaded.FileSizeBytes != 5 {
		t.Fatalf("unexpected upload: %#v", uploaded)
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

func TestRewriteArgumentsUploadsArrayObjectPaths(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "one.txt"), []byte("one"), 0o600); err != nil {
		t.Fatalf("write one: %v", err)
	}
	rewriter := NewOpenAIFileRewriter(dir, &OpenAIFileAuth{ChatGPTBackend: true}, &fakeUploader{})
	rewritten, err := rewriter.RewriteArguments(context.Background(), map[string]any{
		"files": []any{map[string]any{"path": "one.txt", "label": "One"}},
	}, []string{"files"})
	if err != nil {
		t.Fatalf("rewrite failed: %v", err)
	}
	values := rewritten.(map[string]any)["files"].([]any)
	item := values[0].(map[string]any)
	if item["label"] != "One" {
		t.Fatalf("label changed: %#v", item)
	}
	if uploaded, ok := item["path"].(*OpenAIUploadedFile); !ok || uploaded.FileName != "one.txt" {
		t.Fatalf("path = %#v", item["path"])
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
