package model

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"codex_go/codexapi"
)

func TestResponsesHTTPErrorPreservesRetryAfterMetadata(t *testing.T) {
	err := responsesHTTPError("openai", http.StatusServiceUnavailable, http.Header{
		"Retry-After": []string{"17"},
	}, []byte(`{"error":{"message":"busy"}}`))

	var apiErr *ResponsesAPIError
	if !errors.As(err, &apiErr) {
		t.Fatalf("responsesHTTPError() = %T, want *ResponsesAPIError", err)
	}
	if got := codexapi.RetryDelay(err); got != 17*time.Second {
		t.Fatalf("RetryDelay() = %v", got)
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
