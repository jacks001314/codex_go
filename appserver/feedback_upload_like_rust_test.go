package appserver

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"codex_go/config"
)

// feedbackTestDSN builds a Sentry DSN that points at an httptest server: the
// envelope endpoint is `<server>/api/42/envelope/`.
func feedbackTestDSN(t *testing.T, serverURL string) string {
	t.Helper()
	parsed, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	return fmt.Sprintf("%s://public@%s/42", parsed.Scheme, parsed.Host)
}

// newFeedbackTestTransport accepts every envelope so feedback/upload callers do
// not reach the real Sentry ingest endpoint.
func newFeedbackTestTransport(t *testing.T) *FeedbackTransport {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)
	return &FeedbackTransport{HTTP: server.Client(), DSN: feedbackTestDSN(t, server.URL), Timeout: 5 * time.Second}
}

func TestFeedbackEnvelopeEncodingMatchesSentryWireLikeRust(t *testing.T) {
	payload := []byte(strings.Repeat(`{"message":"diagnostic fixture"}`, 64))
	body, decodedBytes, err := encodeFeedbackEnvelope(
		feedbackEnvelopeHeaders{EventID: "3f0c2b1e-1f2a-4a5b-8c9d-0e1f2a3b4c5d"},
		[]feedbackEnvelopeItem{{Type: feedbackEnvelopeEventType, Payload: payload}},
	)
	if err != nil {
		t.Fatalf("encodeFeedbackEnvelope error = %v", err)
	}
	if decodedBytes <= len(payload) {
		t.Fatalf("decoded bytes = %d, want framing plus payload", decodedBytes)
	}
	if len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b {
		reader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			t.Fatalf("gzip.NewReader error = %v", err)
		}
		decoded, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("ReadAll(gzip) error = %v", err)
		}
		if len(decoded) != decodedBytes {
			t.Fatalf("gzip payload = %d bytes, want %d", len(decoded), decodedBytes)
		}
		body = decoded
	}
	lines := strings.SplitN(string(body), "\n", 3)
	if len(lines) != 3 {
		t.Fatalf("envelope = %q", body)
	}
	var header map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &header); err != nil {
		t.Fatalf("envelope header = %q error %v", lines[0], err)
	}
	if header["event_id"] != "3f0c2b1e-1f2a-4a5b-8c9d-0e1f2a3b4c5d" {
		t.Fatalf("envelope header = %#v", header)
	}
	var item map[string]any
	if err := json.Unmarshal([]byte(lines[1]), &item); err != nil {
		t.Fatalf("item header = %q error %v", lines[1], err)
	}
	if item["type"] != "event" || int(item["length"].(float64)) != len(payload) {
		t.Fatalf("item header = %#v", item)
	}
	if !strings.HasPrefix(lines[2], string(payload)) {
		t.Fatalf("item payload = %q", lines[2][:min(len(lines[2]), 32)])
	}
}

func TestFeedbackEnvelopeKeepsIncompressiblePayloadRawLikeRust(t *testing.T) {
	payload := make([]byte, 4096)
	for i := range payload {
		payload[i] = byte(i * 37 % 251)
	}
	body, decodedBytes, err := encodeFeedbackEnvelope(feedbackEnvelopeHeaders{}, []feedbackEnvelopeItem{{
		Type:    feedbackEnvelopeAttachmentType,
		Payload: payload,
	}})
	if err != nil {
		t.Fatalf("encodeFeedbackEnvelope error = %v", err)
	}
	if decodedBytes >= len(body)+1 {
		return
	}
	// Gzip did not shrink the payload: the raw envelope must still be readable,
	// never a headerless compressed stream.
	if len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b {
		t.Fatalf("incompressible envelope should stay raw, got gzip")
	}
}

func TestFeedbackTransportPostsSentryEnvelopeAuthLikeRust(t *testing.T) {
	var path string
	var auth string
	var encoding string
	var raw []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path = r.URL.Path
		auth = r.Header.Get("X-Sentry-Auth")
		encoding = r.Header.Get("Content-Encoding")
		raw, _ = io.ReadAll(r.Body)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	transport := &FeedbackTransport{HTTP: server.Client(), DSN: feedbackTestDSN(t, server.URL), Timeout: 5 * time.Second}
	body, _, err := encodeFeedbackEnvelope(feedbackEnvelopeHeaders{}, []feedbackEnvelopeItem{{
		Type:    feedbackEnvelopeEventType,
		Payload: []byte(strings.Repeat("compressible ", 64)),
	}})
	if err != nil {
		t.Fatalf("encodeFeedbackEnvelope error = %v", err)
	}
	delivery := transport.Send(context.Background(), body)
	if !delivery.Confirmed || !delivery.Accepted {
		t.Fatalf("delivery = %#v", delivery)
	}
	if path != "/api/42/envelope/" {
		t.Fatalf("path = %q", path)
	}
	if !strings.Contains(auth, "sentry_version=7") || !strings.Contains(auth, "sentry_key=public") {
		t.Fatalf("X-Sentry-Auth = %q", auth)
	}
	if encoding != "gzip" {
		t.Fatalf("Content-Encoding = %q", encoding)
	}
	if len(raw) == 0 {
		t.Fatal("envelope body was not sent")
	}
}

func TestFeedbackTransportDoesNotFollowRedirectsLikeRust(t *testing.T) {
	var followed int32
	redirected := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&followed, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer redirected.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirected.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	router := NewRuntimeRouter(RuntimeServices{Config: config.NewConfigService(t.TempDir())})
	transport := router.feedbackTransport()
	transport.DSN = feedbackTestDSN(t, server.URL)
	delivery := transport.Send(context.Background(), []byte("{}\n"))
	if !delivery.Confirmed || delivery.Accepted {
		t.Fatalf("delivery = %#v, want the redirect itself reported", delivery)
	}
	if delivery.Status != http.StatusTemporaryRedirect {
		t.Fatalf("status = %d, want %d", delivery.Status, http.StatusTemporaryRedirect)
	}
	if got := atomic.LoadInt32(&followed); got != 0 {
		t.Fatalf("redirect target requests = %d, want 0", got)
	}
}

func TestFeedbackRetryAfterParsesSecondsAndDatesLikeRust(t *testing.T) {
	if delay, ok := parseFeedbackRetryAfter("8"); !ok || delay != 8*time.Second {
		t.Fatalf("seconds = %v %v", delay, ok)
	}
	future := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	delay, ok := parseFeedbackRetryAfter(future)
	if !ok || delay < 25*time.Second || delay > 31*time.Second {
		t.Fatalf("http date = %v %v", delay, ok)
	}
	if _, ok := parseFeedbackRetryAfter("not-a-date"); ok {
		t.Fatal("invalid Retry-After should not parse")
	}
}

func TestFeedbackSentryRateLimitDelayIgnoresUnrelatedCategoriesLikeRust(t *testing.T) {
	header := http.Header{}
	header.Set("X-Sentry-Rate-Limits", "60:transaction:project")
	if _, ok := feedbackSentryRateLimitDelay(header); ok {
		t.Fatal("transaction quota must not delay feedback")
	}
	header.Set("X-Sentry-Rate-Limits", "60:attachment:project")
	if delay, ok := feedbackSentryRateLimitDelay(header); !ok || delay != time.Minute {
		t.Fatalf("attachment quota = %v %v", delay, ok)
	}
	header.Set("X-Sentry-Rate-Limits", "0::project")
	delay, ok := feedbackSentryRateLimitDelay(header)
	if !ok || delay != 0 {
		t.Fatalf("global zero quota = %v %v", delay, ok)
	}
}

func TestFeedbackUploadRetriesOnlyAttachmentsLikeRust(t *testing.T) {
	var eventAttempts int32
	var attachmentAttempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		decoded := decodeFeedbackEnvelopeForTest(t, body)
		if strings.Contains(decoded, `"type":"event"`) {
			atomic.AddInt32(&eventAttempts, 1)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		atomic.AddInt32(&attachmentAttempts, 1)
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	transport := &FeedbackTransport{HTTP: server.Client(), DSN: feedbackTestDSN(t, server.URL), Timeout: 5 * time.Second}
	snapshot := &FeedbackSnapshot{ThreadID: "thread-1", Diagnostics: NewFeedbackDiagnostics(nil)}
	err := snapshot.UploadFeedback(context.Background(), &FeedbackUploadOptions{
		Classification: "bug",
		ExtraAttachments: []FeedbackAttachment{{
			Filename:    "diagnostic.txt",
			ContentType: "text/plain",
			Buffer:      []byte("attachment"),
		}},
	}, transport)
	if err == nil || !strings.Contains(err.Error(), "Sentry rejected feedback upload with HTTP status 503") {
		t.Fatalf("error = %v", err)
	}
	if got := atomic.LoadInt32(&eventAttempts); got != 1 {
		t.Fatalf("event attempts = %d, want 1 (events are never replayed)", got)
	}
	if got := atomic.LoadInt32(&attachmentAttempts); got != 0 {
		t.Fatalf("attachment attempts = %d, want 0 before the event is accepted", got)
	}
}

func TestFeedbackUploadRetriesAttachmentTwiceThenContinuesLikeRust(t *testing.T) {
	var mu sync.Mutex
	var attachmentBodies [][]byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		decoded := decodeFeedbackEnvelopeForTest(t, body)
		if strings.Contains(decoded, `"type":"event"`) {
			w.WriteHeader(http.StatusOK)
			return
		}
		mu.Lock()
		attachmentBodies = append(attachmentBodies, body)
		attempt := len(attachmentBodies)
		mu.Unlock()
		if attempt == 2 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	transport := &FeedbackTransport{HTTP: server.Client(), DSN: feedbackTestDSN(t, server.URL), Timeout: 5 * time.Second}
	snapshot := &FeedbackSnapshot{ThreadID: "thread-2", Diagnostics: NewFeedbackDiagnostics(nil)}
	err := snapshot.UploadFeedback(context.Background(), &FeedbackUploadOptions{
		Classification: "bug",
		ExtraAttachments: []FeedbackAttachment{
			{Filename: "first.txt", Buffer: []byte("first")},
			{Filename: "second.txt", Buffer: []byte("second")},
			{Filename: "third.txt", Buffer: []byte("third")},
		},
	}, transport)
	if err != nil {
		t.Fatalf("UploadFeedback error = %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(attachmentBodies) != 4 {
		t.Fatalf("attachment requests = %d, want 4 (one retry plus the remaining files)", len(attachmentBodies))
	}
	if !bytes.Equal(attachmentBodies[1], attachmentBodies[2]) {
		t.Fatal("the retry must resend the exact same bytes")
	}
}

func TestFeedbackUploadSkipsLaterAttachmentsAfterRateLimitLikeRust(t *testing.T) {
	var attachments int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		decoded := decodeFeedbackEnvelopeForTest(t, body)
		if strings.Contains(decoded, `"type":"event"`) {
			w.WriteHeader(http.StatusOK)
			return
		}
		atomic.AddInt32(&attachments, 1)
		w.Header().Set("X-Sentry-Rate-Limits", "60:attachment:project")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	transport := &FeedbackTransport{HTTP: server.Client(), DSN: feedbackTestDSN(t, server.URL), Timeout: 5 * time.Second}
	snapshot := &FeedbackSnapshot{ThreadID: "thread-3", Diagnostics: NewFeedbackDiagnostics(nil)}
	err := snapshot.UploadFeedback(context.Background(), &FeedbackUploadOptions{
		Classification: "bug",
		ExtraAttachments: []FeedbackAttachment{
			{Filename: "first.txt", Buffer: []byte("first")},
			{Filename: "second.txt", Buffer: []byte("second")},
		},
	}, transport)
	if err == nil || !strings.Contains(err.Error(), "some attachments failed to upload") {
		t.Fatalf("error = %v", err)
	}
	if got := atomic.LoadInt32(&attachments); got != 1 {
		t.Fatalf("attachment requests = %d, want 1 (the quota skips later files)", got)
	}
}

func TestFeedbackUploadLimitRejectsFourthConcurrentRequestLikeRust(t *testing.T) {
	arrived := make(chan struct{}, 8)
	release := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		arrived <- struct{}{}
		<-release
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	transport := &FeedbackTransport{HTTP: server.Client(), DSN: feedbackTestDSN(t, server.URL), Timeout: 30 * time.Second}
	router := NewRuntimeRouter(RuntimeServices{Feedback: &FeedbackSnapshot{Diagnostics: NewFeedbackDiagnostics(nil)}, FeedbackUpload: transport})
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			response := router.Handle(requestWithParams(t, IntID(int64(id+1)), MethodFeedbackUpload, FeedbackUploadParams{Classification: "bug"}))
			if response.Error != nil {
				t.Errorf("feedback upload %d error = %+v", id, response.Error)
			}
		}(i)
	}
	for i := 0; i < 3; i++ {
		select {
		case <-arrived:
		case <-time.After(10 * time.Second):
			t.Fatal("upload did not start")
		}
	}
	excess := router.Handle(requestWithParams(t, IntID(9), MethodFeedbackUpload, FeedbackUploadParams{Classification: "bug"}))
	if excess.Error == nil {
		t.Fatal("fourth concurrent upload should be rejected")
	}
	if excess.Error.Code != JSONRPCOverloadedErrorCode {
		t.Fatalf("error code = %d, want %d", excess.Error.Code, JSONRPCOverloadedErrorCode)
	}
	if !strings.Contains(excess.Error.Message, "Three feedback uploads are already in progress") {
		t.Fatalf("error message = %q", excess.Error.Message)
	}
	close(release)
	wg.Wait()
}

func TestFeedbackUploadRejectsDisabledFeedbackLikeRust(t *testing.T) {
	home := t.TempDir()
	if err := os.WriteFile(filepath.Join(home, "config.toml"), []byte("feedback.enabled = false\n"), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	router := NewRuntimeRouter(RuntimeServices{
		Feedback:       &FeedbackSnapshot{Diagnostics: NewFeedbackDiagnostics(nil)},
		FeedbackUpload: newFeedbackTestTransport(t),
		Config:         config.NewConfigService(home),
	})
	response := router.Handle(requestWithParams(t, IntID(1), MethodFeedbackUpload, FeedbackUploadParams{Classification: "bug"}))
	if response.Error == nil {
		t.Fatal("feedback/upload should be rejected when feedback is disabled")
	}
	if !strings.Contains(response.Error.Message, "sending feedback is disabled by configuration") {
		t.Fatalf("error = %+v", response.Error)
	}
}

// decodeFeedbackEnvelopeForTest returns the decompressed envelope text so a
// handler can tell events from attachments.
func decodeFeedbackEnvelopeForTest(t *testing.T, body []byte) string {
	t.Helper()
	if len(body) >= 2 && body[0] == 0x1f && body[1] == 0x8b {
		reader, err := gzip.NewReader(bytes.NewReader(body))
		if err != nil {
			t.Fatalf("gzip.NewReader error = %v", err)
		}
		decoded, err := io.ReadAll(reader)
		if err != nil {
			t.Fatalf("ReadAll(gzip) error = %v", err)
		}
		return string(decoded)
	}
	return string(body)
}
