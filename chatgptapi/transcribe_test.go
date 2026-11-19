package chatgptapi

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Mirrors Rust #62142 transcribe_audio_posts_multipart_file_and_returns_text:
// the clip travels as the single multipart file field and the reply's text is
// returned.
func TestTranscribeAudioPostsMultipartFileAndReturnsTextLikeRust(t *testing.T) {
	var (
		gotPath        string
		gotAuth        string
		gotContentType string
		gotBody        []byte
	)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		gotPath = request.URL.Path
		gotAuth = request.Header.Get("Authorization")
		gotContentType = request.Header.Get("Content-Type")
		body, err := io.ReadAll(request.Body)
		if err != nil {
			t.Errorf("read body: %v", err)
		}
		gotBody = body
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"text":"Hello there."}`))
	}))
	defer server.Close()

	client := NewCloudClient(&CloudClientOptions{
		BaseURL:    server.URL + "/backend-api",
		HTTPClient: server.Client(),
		Headers:    http.Header{"Authorization": []string{"Bearer test-token"}},
	})
	audio := []byte("\x00\xff\x80\r\n")
	text, err := client.TranscribeAudio(context.Background(), "audio/mp4", audio)
	if err != nil {
		t.Fatalf("TranscribeAudio() error = %v", err)
	}
	if text != "Hello there." {
		t.Fatalf("transcript = %q", text)
	}
	if gotPath != "/backend-api/transcribe" {
		t.Fatalf("path = %q", gotPath)
	}
	if gotAuth != "Bearer test-token" {
		t.Fatalf("authorization = %q", gotAuth)
	}
	boundary, ok := strings.CutPrefix(gotContentType, "multipart/form-data; boundary=")
	if !ok || boundary == "" {
		t.Fatalf("content type = %q", gotContentType)
	}
	expected := "--" + boundary + "\r\n" +
		"Content-Disposition: form-data; name=\"file\"; filename=\"audio.m4a\"\r\n" +
		"Content-Type: audio/mp4\r\n\r\n" +
		string(audio) + "\r\n--" + boundary + "--\r\n"
	if string(gotBody) != expected {
		t.Fatalf("multipart body = %q, want %q", gotBody, expected)
	}
}

// Mirrors Rust #62142's unsupported-format guard: only the five accepted clip
// types reach the endpoint.
func TestTranscribeAudioRejectsUnsupportedFormatLikeRust(t *testing.T) {
	requested := false
	server := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		requested = true
	}))
	defer server.Close()
	client := NewCloudClient(&CloudClientOptions{BaseURL: server.URL, HTTPClient: server.Client()})
	if _, err := client.TranscribeAudio(context.Background(), "audio/flac", []byte("clip")); err == nil {
		t.Fatal("TranscribeAudio() accepted audio/flac")
	}
	if requested {
		t.Fatal("unsupported format still reached the endpoint")
	}
}
