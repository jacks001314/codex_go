package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/codexapi"
)

func TestResponsesHTTPErrorPreservesRetryAfterMetadata(t *testing.T) {
	receivedAt := time.Now()
	err := responsesHTTPError("openai", http.StatusServiceUnavailable, http.Header{
		"Retry-After": []string{"17"},
	}, []byte(`{"error":{"message":"busy"}}`), receivedAt)

	var apiErr *ResponsesAPIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("responsesHTTPError() = %T, want *ResponsesAPIError", err)
	}
	retryAfter, ok := codexapi.RetryAfterInfo(err)
	if !ok {
		t.Fatal("RetryAfterInfo() = absent, want the captured deadline")
	}
	if deadline, want := retryAfter.Deadline(), receivedAt.Add(17*time.Second); deadline.Before(want) || deadline.After(want.Add(10*time.Millisecond)) {
		t.Fatalf("retry deadline = %v, want about %v", deadline, want)
	}
	if got := codexapi.RetryDelay(err); got <= 0 || got > 17*time.Second {
		t.Fatalf("RetryDelay() = %v, want remaining duration in (0, 17s]", got)
	}
}

func TestResponsesHTTPErrorPreservesExplicitZeroRetryAfter(t *testing.T) {
	err := responsesHTTPError("openai", http.StatusServiceUnavailable, http.Header{
		"Retry-After": []string{"0"},
	}, nil)
	if got, ok := codexapi.RetryDelayInfo(err); !ok || got != 0 {
		t.Fatalf("RetryDelayInfo() = %v, %t", got, ok)
	}
}

func TestResponsesHTTPErrorPreservesRetryDeadlineAcrossWrapping(t *testing.T) {
	before := time.Now()
	err := responsesHTTPError("openai", http.StatusServiceUnavailable, http.Header{
		"Retry-After": []string{"17"},
	}, []byte(`{"error":{"message":"busy"}}`))
	after := time.Now()
	wrapped := fmt.Errorf("outer: %w", err)
	retryAfter, ok := codexapi.RetryAfterInfo(wrapped)
	if !ok {
		t.Fatal("wrapped response error lost retry advice")
	}
	deadline := retryAfter.Deadline()
	if deadline.Before(before.Add(17*time.Second)) || deadline.After(after.Add(17*time.Second)) {
		t.Fatalf("retry deadline = %v, want between %v and %v", deadline, before.Add(17*time.Second), after.Add(17*time.Second))
	}
}

func TestResponsesHTTPErrorAnchorsRetryAfterAtHeaderReceipt(t *testing.T) {
	receivedAt := time.Now().Add(-5 * time.Second)
	err := responsesHTTPError("openai", http.StatusServiceUnavailable, http.Header{
		"Retry-After": []string{"17"},
	}, []byte(`{"error":{"message":"busy"}}`), receivedAt)
	retryAfter, ok := codexapi.RetryAfterInfo(err)
	if !ok {
		t.Fatal("response error lost retry advice")
	}
	deadline := retryAfter.Deadline()
	if deadline.Before(receivedAt.Add(17*time.Second)) || deadline.After(receivedAt.Add(17*time.Second+10*time.Millisecond)) {
		t.Fatalf("retry deadline = %v, want about %v", deadline, receivedAt.Add(17*time.Second))
	}
}

func TestRecordResponsesRetryEmitsRustShape(t *testing.T) {
	t.Setenv(responsesDiagnosticsEnv, "1")
	path := filepath.Join(t.TempDir(), "retry.jsonl")
	t.Setenv(responsesDiagnosticsFileEnv, path)

	recordResponsesRetry("sampling", 2, 10*time.Second, "stream")

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var record map[string]any
	if err := json.Unmarshal(data, &record); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if record["event"] != "codex.retry" ||
		record["retry.attempt"].(float64) != 2 ||
		record["retry.delay_ms"].(float64) != 10000 ||
		record["retry.layer"] != "stream" ||
		record["retry.operation"] != "sampling" {
		t.Fatalf("record = %#v", record)
	}
}
