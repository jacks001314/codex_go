package model

import (
	"errors"
	"net/http"
	"testing"
	"time"

	"codex_go/codexapi"
)

// audioValidationCodes mirrors Rust #58434 across every transport the
// classifier covers.
var audioValidationCodes = []string{
	"audio_too_large",
	"audio_too_short",
	"invalid_audio",
	"invalid_audio_data",
	"invalid_audio_format",
	"missing_audio_sample_rate",
}

// Mirrors Rust #58434 (codex-api/src/sse/responses_error_tests.rs): audio
// validation codes end the turn without retries on HTTP 400, streamed error
// events, response.failed events, and WebSocket error events, keeping the
// provider message.
func TestAudioValidationCodesTerminateLikeRust(t *testing.T) {
	for _, code := range audioValidationCodes {
		t.Run(code, func(t *testing.T) {
			rawDetail := `{"code":"` + code + `","message":"Invalid recording."}`
			httpErr := responsesHTTPError("openai", http.StatusBadRequest, nil, []byte(`{"error":`+rawDetail+`}`), time.Now())
			requireAudioInputError(t, httpErr, http.StatusBadRequest)

			acc := &responsesStreamAccumulator{}
			_, streamErr := acc.apply(&responsesSSEEvent{
				Event: "error",
				Data:  []byte(`{"type":"error","error":` + rawDetail + `}`),
			}, nil)
			requireAudioInputError(t, streamErr, 0)

			failedErr := responseFailedError([]byte(`{"type":"response.failed","response":{"error":` + rawDetail + `}}`))
			requireAudioInputError(t, failedErr, 0)

			wsErr := websocketTerminalProviderError(map[string]any{
				"type":  "error",
				"error": map[string]any{"code": code, "message": "Invalid recording."},
			})
			requireAudioInputError(t, wsErr, 0)
		})
	}
}

func requireAudioInputError(t *testing.T, err error, wantStatus int) {
	t.Helper()
	if err == nil {
		t.Fatal("error = nil, want invalid audio input")
	}
	var apiErr *codexapi.APIError
	if !errors.As(err, &apiErr) || apiErr.Kind != codexapi.ErrorInvalidAudioInput {
		t.Fatalf("error = %#v, want invalidAudioInput", err)
	}
	if apiErr.Status != wantStatus {
		t.Fatalf("status = %d, want %d", apiErr.Status, wantStatus)
	}
	if apiErr.Message != "Invalid recording." {
		t.Fatalf("message = %q, want the provider message", apiErr.Message)
	}
	if apiErr.Error() != "Invalid recording." {
		t.Fatalf("display = %q, want the provider message", apiErr.Error())
	}
	if isRetryableResponsesStreamError(err) {
		t.Fatal("audio validation failures must not be retryable")
	}
}

// Rust #58434 uses a fixed copy when the provider message is absent or blank.
func TestAudioValidationFallbackMessageLikeRust(t *testing.T) {
	for _, raw := range []string{
		`{"type":"response.failed","response":{"error":{"code":"invalid_audio"}}}`,
		`{"type":"response.failed","response":{"error":{"code":"invalid_audio","message":"   "}}}`,
	} {
		err := responseFailedError([]byte(raw))
		var apiErr *codexapi.APIError
		if !errors.As(err, &apiErr) || apiErr.Kind != codexapi.ErrorInvalidAudioInput {
			t.Fatalf("error = %#v, want invalidAudioInput", err)
		}
		if apiErr.Message != AudioInputFallbackMessage {
			t.Fatalf("message = %q, want %q", apiErr.Message, AudioInputFallbackMessage)
		}
	}
}

// The audio code alone does not classify a failure: Rust only maps it for a
// statusless error or an HTTP 400.
func TestAudioValidationCodeOutsideBadRequestStaysUnclassified(t *testing.T) {
	raw := []byte(`{"error":{"code":"invalid_audio_format","message":"Invalid recording."}}`)
	err := responsesHTTPError("openai", http.StatusInternalServerError, nil, raw, time.Now())
	var apiErr *codexapi.APIError
	if errors.As(err, &apiErr) && apiErr.Kind == codexapi.ErrorInvalidAudioInput {
		t.Fatalf("500 audio code classified as terminal audio input: %#v", err)
	}
	wsErr := websocketTerminalProviderError(map[string]any{
		"type":   "error",
		"status": float64(http.StatusInternalServerError),
		"error":  map[string]any{"code": "invalid_audio_format", "message": "Invalid recording."},
	})
	if wsErr != nil {
		t.Fatalf("websocket error = %#v, want nil", wsErr)
	}
}
